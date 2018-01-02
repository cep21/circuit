package fastmath

import (
	"math"
	"sort"
	"strings"
	"time"
	"expvar"
)

type RollingPercentile struct {
	buckets       []durationsBucket
	rollingBucket RollingBuckets
}

type SortedDurations []time.Duration

func (s SortedDurations) String() string {
	ret := make([]string, 0, len(s))
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

func (s SortedDurations) Var() expvar.Var {
	return expvar.Func(func() interface{} {
		return map[string]time.Duration{
			"p25":  s.Percentile(.25),
			"p50":  s.Percentile(.5),
			"p90":  s.Percentile(.9),
			"p99":  s.Percentile(.99),
			"mean": s.Mean(),
		}
	})
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
func NewRollingPercentile(bucketWidth time.Duration, numBuckets int, bucketSize int, now time.Time) RollingPercentile {
	ret := RollingPercentile{
		buckets: makeBuckets(numBuckets, bucketSize),
	}
	ret.rollingBucket.Init(numBuckets, bucketWidth, now)
	return ret
}

func makeBuckets(numBuckets int, bucketSize int) []durationsBucket {
	ret := make([]durationsBucket, numBuckets)
	for i := 0; i < numBuckets; i++ {
		ret[i] = newDurationsBucket(bucketSize)
	}
	return ret
}

func (r *RollingPercentile) SortedDurations(now time.Time) []time.Duration {
	if len(r.buckets) == 0 {
		return nil
	}
	r.rollingBucket.Advance(now, r.clearBucket)
	ret := make([]time.Duration, 0, len(r.buckets)*10)
	for idx := range r.buckets {
		ret = append(ret, r.buckets[idx].Durations()...)
	}
	sort.Slice(ret, func(i, j int) bool {
		return ret[i] < ret[j]
	})
	return ret
}

func (r *RollingPercentile) Snapshot() SortedDurations {
	return r.SnapshotAt(time.Now())
}

func (r *RollingPercentile) SnapshotAt(now time.Time) SortedDurations {
	return SortedDurations(r.SortedDurations(now))
}

func (r *RollingPercentile) clearBucket(idx int) {
	r.buckets[idx].clear()
}

func (r *RollingPercentile) AddDuration(d time.Duration, now time.Time) {
	if len(r.buckets) == 0 {
		return
	}
	idx := r.rollingBucket.Advance(now, r.clearBucket)
	r.buckets[idx].addDuration(d)
}

// Reset the counter to all zero values.
func (r *RollingPercentile) Reset(now time.Time) {
	r.rollingBucket.Advance(now, r.clearBucket)
	for i := 0; i < r.rollingBucket.NumBuckets; i++ {
		r.clearBucket(i)
	}
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

func (b *durationsBucket) iterateDurations(startingIndex int64, callback func(time.Duration)) int64 {
	lastAbsoluteIndex := b.currentIndex.Get() - 1
	// work backwards from this value till we get to starting index
	for i := lastAbsoluteIndex; i >= startingIndex; i-- {
		arrayIndex := i % int64(len(b.durationsSomeInvalid))
		val := b.durationsSomeInvalid[arrayIndex].Duration()
		callback(val)
	}
	return lastAbsoluteIndex + 1
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
