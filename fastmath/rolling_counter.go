package fastmath

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// RollingCounter uses a slice of buckets to keep track of counts of an event over time with a sliding window
type RollingCounter struct {
	// The len(buckets) is constant and not mutable
	// The values of the individual buckets are atomic, so they do not take the mutex
	buckets []AtomicInt64

	// Neither of these need to be locked (atomic operations)
	rollingSum AtomicInt64
	totalSum   AtomicInt64

	rollingBucket RollingBuckets
}

// NewRollingCounter initializes a rolling counter with a bucket width and # of buckets
func NewRollingCounter(bucketWidth time.Duration, numBuckets int, now time.Time) RollingCounter {
	ret := RollingCounter{
		buckets: make([]AtomicInt64, numBuckets),
	}
	ret.rollingBucket.Init(numBuckets, bucketWidth, now)
	return ret
}

// String for debugging
func (r *RollingCounter) String() string {
	return r.StringAt(time.Now())
}

// StringAt converts the counter to a string at a given time.
func (r *RollingCounter) StringAt(now time.Time) string {
	b := r.GetBuckets(now)
	parts := make([]string, 0, len(r.buckets))
	for _, v := range b {
		parts = append(parts, strconv.FormatInt(v, 10))
	}
	return fmt.Sprintf("rolling_sum=%d total_sum=%d parts=(%s)", r.RollingSum(now), r.TotalSum(), strings.Join(parts, ","))
}

// Inc adds a single event to the current bucket
func (r *RollingCounter) Inc(now time.Time) {
	r.totalSum.Add(1)
	if len(r.buckets) == 0 {
		return
	}
	idx := r.rollingBucket.Advance(now, r.clearBucket)
	if idx < 0 {
		return
	}
	r.buckets[idx].Add(1)
	r.rollingSum.Add(1)
}

// RollingSum returns the total number of events in the rolling time window
func (r *RollingCounter) RollingSum(now time.Time) int64 {
	r.rollingBucket.Advance(now, r.clearBucket)
	return r.rollingSum.Get()
}

// RollingSum returns the total number of events of all time
func (r *RollingCounter) TotalSum() int64 {
	return r.totalSum.Get()
}

// GetBuckets returns a copy of the buckets in order backwards in time
func (r *RollingCounter) GetBuckets(now time.Time) []int64 {
	r.rollingBucket.Advance(now, r.clearBucket)
	startIdx := int(r.rollingBucket.lastAbsIndex.Get() % int64(r.rollingBucket.NumBuckets))
	ret := make([]int64, r.rollingBucket.NumBuckets)
	for i := 0; i < r.rollingBucket.NumBuckets; i++ {
		idx := startIdx - i
		if idx < 0 {
			idx += r.rollingBucket.NumBuckets
		}
		ret[i] = r.buckets[idx].Get()
	}
	return ret
}

func (r *RollingCounter) clearBucket(idx int) {
	toDec := r.buckets[idx].Swap(0)
	r.rollingSum.Add(-toDec)
}

// Reset the counter to all zero values.
func (r *RollingCounter) Reset(now time.Time) {
	r.rollingBucket.Advance(now, r.clearBucket)
	for i := 0; i < r.rollingBucket.NumBuckets; i++ {
		r.clearBucket(i)
	}
}
