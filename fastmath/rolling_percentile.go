package fastmath

import (
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type RollingPercentile struct {
	buckets       []durationsBucket
	currentBucket int

	bucketWidth time.Duration
	bucketStart time.Time
	mu          sync.Mutex

	fastPathCurrentBucket atomic.Value
}

type SortedDurations []time.Duration

func (s SortedDurations) String() string {
	ret := []string{}
	for _, d := range s {
		ret = append(ret, d.String())
	}
	return "(" + strings.Join(ret, ",") + ")"
}

func (s SortedDurations) Mean() time.Duration {
	if len(s) == 0 {
		// A meaningless value for a meaningless list
		return -1
	}
	sum := int64(0)
	for _, d := range s {
		sum += d.Nanoseconds()
	}
	return time.Duration(sum / int64(len(s)))
}

func (s SortedDurations) Percentile(p float64) time.Duration {
	if len(s) == 0 {
		// A meaningless value for a meaningless list
		return -1
	}
	if len(s) == 1 {
		return s[0]
	}
	if p <= 0 {
		return s[0]
	}
	if p >= 100 {
		return s[len(s)-1]
	}
	absoluteIndex := p / 100 * float64(len(s)-1)

	// The real value is now an approximation between here
	// For example, if absoluteIndex is 5.5, then we want to return a value
	// exactly between the [5] and [6] index of the array.
	//
	// However, if the absoluteIndex is 5.1, then we want to return a value
	// that is closer to [5], but still has a tiny part of [6]
	firstValue := s[int(math.Floor(absoluteIndex))]
	secondValue := s[int(math.Ceil(absoluteIndex))]

	firstWeight := absoluteIndex - math.Floor(absoluteIndex)
	return firstValue + time.Duration(int64(float64(secondValue-firstValue)*firstWeight))
}

// NewRollingPercentile creates a new rolling percentile bucketer
func NewRollingPercentile(bucketWidth time.Duration, numBuckets int, bucketSize int) RollingPercentile {
	return RollingPercentile{
		bucketWidth: bucketWidth,
		buckets:     makeBuckets(numBuckets, bucketSize),
	}
}

func makeBuckets(numBuckets int, bucketSize int) []durationsBucket {
	ret := make([]durationsBucket, numBuckets)
	for i := 0; i < numBuckets; i++ {
		ret[i] = newDurationsBucket(bucketSize)
	}
	return ret
}

func (r *RollingPercentile) Snapshot(now time.Time) SortedDurations {
	if len(r.buckets) == 0 {
		return SortedDurations(nil)
	}
	r.mu.Lock()
	r.advance(now)
	ret := make([]time.Duration, 0, len(r.buckets)*10)
	for idx := range r.buckets {
		ret = append(ret, r.buckets[idx].Durations()...)
	}
	r.mu.Unlock()
	sort.Slice(ret, func(i, j int) bool {
		return ret[i] < ret[j]
	})
	return SortedDurations(ret)
}

func (r *RollingPercentile) AddDuration(d time.Duration, now time.Time) {
	if len(r.buckets) == 0 {
		return
	}
	if r.addFastPath(now, d) {
		return
	}
	r.mu.Lock()
	r.advance(now)
	r.buckets[r.currentBucket].addDuration(d)
	r.mu.Unlock()
}

type currentBucketInfo struct {
	currentBucket int
	bucketStart   time.Time
	rollingBucket RollingBuckets
}


func (r *RollingPercentile) addFastPath(now time.Time, d time.Duration) bool {
	currentBucketPtr := r.fastPathCurrentBucket.Load()
	if currentBucketPtr == nil {
		return false
	}
	currentBucket := currentBucketPtr.(*currentBucketInfo)
	if currentBucket.bucketStart.After(now) {
		return false
	}
	diff := now.Sub(currentBucket.bucketStart)
	if diff >= r.bucketWidth {
		return false
	}
	if len(r.buckets) == 0 {
		return true
	}
	r.buckets[currentBucket.currentBucket].addDuration(d)
	return true
}

func (r *RollingPercentile) resetNoLock(now time.Time) {
	for i := range r.buckets {
		r.buckets[i].clear()
	}
	r.currentBucket = 0
	r.bucketStart = now
	r.fastPathCurrentBucket.Store(&currentBucketInfo{
		currentBucket: r.currentBucket,
		bucketStart:   r.bucketStart,
	})
}

// advance forward, clearing buckets as needed
func (r *RollingPercentile) advance(now time.Time) {
	if !now.After(r.bucketStart) {
		// advance backwards is ignored
		return
	}
	if r.bucketStart.IsZero() {
		r.bucketStart = now
		r.fastPathCurrentBucket.Store(&currentBucketInfo{
			currentBucket: r.currentBucket,
			bucketStart:   r.bucketStart,
		})
		return
	}
	diff := now.Sub(r.bucketStart)
	if diff < r.bucketWidth {
		return
	}

	// At this point, we must advance forward in the buckets list
	for i := 0; i < len(r.buckets); i++ {
		diff := now.Sub(r.bucketStart)
		if diff < r.bucketWidth {
			return
		}

		// Move to the next bucket and clear out the value there
		r.currentBucket = (r.currentBucket + 1) % len(r.buckets)
		r.buckets[r.currentBucket].clear()
		r.bucketStart = r.bucketStart.Add(r.bucketWidth)
		r.fastPathCurrentBucket.Store(&currentBucketInfo{
			currentBucket: r.currentBucket,
			bucketStart:   r.bucketStart,
		})
	}
	r.resetNoLock(now)
}

// durationsBucket supports atomically adding durations to a size limited list
type durationsBucket struct {
	// durations is a fixed size and cannot change during operation
	durationsSomeInvalid []AtomicInt64
	currentIndex         AtomicInt64
}

func newDurationsBucket(bucketSize int) durationsBucket {
	return durationsBucket{
		durationsSomeInvalid: make([]AtomicInt64, bucketSize),
	}
}

func (b *durationsBucket) Durations() []time.Duration {
	maxIndex := b.currentIndex.Get()
	if maxIndex > int64(len(b.durationsSomeInvalid)) {
		maxIndex = int64(len(b.durationsSomeInvalid))
	}
	ret := make([]time.Duration, maxIndex)
	for i := 0; i < int(maxIndex); i++ {
		ret[i] = b.durationsSomeInvalid[i].Duration()
	}
	return ret
}

func (b *durationsBucket) clear() {
	b.currentIndex.Set(0)
}

func (b *durationsBucket) addDuration(d time.Duration) {
	if len(b.durationsSomeInvalid) == 0 {
		return
	}
	nextIndex := b.currentIndex.Add(1) - 1
	arrayIndex := nextIndex % int64(len(b.durationsSomeInvalid))
	b.durationsSomeInvalid[arrayIndex].Set(d.Nanoseconds())
}
