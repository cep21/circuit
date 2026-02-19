package faststats

import (
	"encoding/json"
	"expvar"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cep21/circuit/v4/internal/evar"
)

// RollingPercentile is a bucketed array of time.Duration that cycles over time
type RollingPercentile struct {
	buckets       []durationsBucket
	rollingBucket RollingBuckets
}

// SortedDurations is a sorted list of time.Duration that allows fast Percentile operations
type SortedDurations []time.Duration

var _ fmt.Stringer = SortedDurations(nil)

func (s SortedDurations) String() string {
	ret := make([]string, 0, len(s))
	for _, d := range s {
		ret = append(ret, d.String())
	}
	return "(" + strings.Join(ret, ",") + ")"
}

// Mean (average) of the current list
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

// Min returns the first (smallest) item, or -1 if the list is empty
func (s SortedDurations) Min() time.Duration {
	if len(s) == 0 {
		return -1
	}
	return s[0]
}

// Max returns the last (largest) item, or -1 if the list is empty
func (s SortedDurations) Max() time.Duration {
	if len(s) == 0 {
		return -1
	}
	return s[len(s)-1]
}

// Var allows exposing the durations on expvar
func (s SortedDurations) Var() expvar.Var {
	return expvar.Func(func() interface{} {
		return map[string]string{
			// Convert to string because it's easier to read
			"min":  s.Min().String(),
			"p25":  s.Percentile(25).String(),
			"p50":  s.Percentile(50).String(),
			"p90":  s.Percentile(90).String(),
			"p99":  s.Percentile(99).String(),
			"max":  s.Max().String(),
			"mean": s.Mean().String(),
		}
	})
}

// Percentile returns a p [0 - 100] percentile of the list
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
	return RollingPercentile{
		buckets: makeBuckets(numBuckets, bucketSize),
		rollingBucket: RollingBuckets{
			NumBuckets:  numBuckets,
			BucketWidth: bucketWidth,
			StartTime:   now,
		},
	}
}

func makeBuckets(numBuckets int, bucketSize int) []durationsBucket {
	ret := make([]durationsBucket, numBuckets)
	for i := 0; i < numBuckets; i++ {
		ret[i] = newDurationsBucket(bucketSize)
	}
	return ret
}

// Var allows exposing a rolling percentile snapshot on expvar
func (r *RollingPercentile) Var() expvar.Var {
	return expvar.Func(func() interface{} {
		return map[string]interface{}{
			"snap": evar.ForExpvar(r.Snapshot()),
		}
	})
}

// SortedDurations creates a raw []time.Duration in sorted order that is stored in these buckets
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

// Snapshot the current rolling buckets, allowing easy p99 calculations
func (r *RollingPercentile) Snapshot() SortedDurations {
	return r.SnapshotAt(time.Now())
}

// SnapshotAt is an optimization on Snapshot that takes the current time
func (r *RollingPercentile) SnapshotAt(now time.Time) SortedDurations {
	return SortedDurations(r.SortedDurations(now))
}

func (r *RollingPercentile) clearBucket(idx int) {
	r.buckets[idx].clear()
}

// AddDuration adds a duration to the rolling buckets
func (r *RollingPercentile) AddDuration(d time.Duration, now time.Time) {
	if len(r.buckets) == 0 {
		return
	}
	idx := r.rollingBucket.Advance(now, r.clearBucket)
	if idx < 0 {
		return
	}
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

var _ json.Marshaler = &durationsBucket{}
var _ json.Unmarshaler = &durationsBucket{}
var _ fmt.Stringer = &durationsBucket{}

func newDurationsBucket(bucketSize int) durationsBucket {
	return durationsBucket{
		durationsSomeInvalid: make([]AtomicInt64, bucketSize),
	}
}

// String displays the current index
func (b *durationsBucket) String() string {
	return fmt.Sprintf("durationsBucket(idx=%d)", b.currentIndex.Get())
}

type forMarshal struct {
	DurationsSomeInvalid []int64
	CurrentIndex         int64
}

// MarshalJSON returns the durations as JSON.  It is thread safe.
func (b *durationsBucket) MarshalJSON() ([]byte, error) {
	m := forMarshal{
		DurationsSomeInvalid: make([]int64, len(b.durationsSomeInvalid)),
	}
	m.CurrentIndex = b.currentIndex.Get()
	for idx := range b.durationsSomeInvalid {
		m.DurationsSomeInvalid[idx] = b.durationsSomeInvalid[idx].Get()
	}
	return json.Marshal(m)
}

// UnmarshalJSON stores JSON encoded durations into the bucket.  It is thread safe *only* if durations length matches.
func (b *durationsBucket) UnmarshalJSON(data []byte) error {
	var m forMarshal
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	if len(b.durationsSomeInvalid) != len(m.DurationsSomeInvalid) {
		b.durationsSomeInvalid = make([]AtomicInt64, len(m.DurationsSomeInvalid))
	}
	for idx := range m.DurationsSomeInvalid {
		b.durationsSomeInvalid[idx].Set(m.DurationsSomeInvalid[idx])
	}
	b.currentIndex.Set(m.CurrentIndex)
	return nil
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

// IterateDurations allows executing a callback on the rolling durations bucket, returning a cursor you can pass into
// future iteration calls
func (b *durationsBucket) IterateDurations(startingIndex int64, callback func(time.Duration)) int64 {
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
