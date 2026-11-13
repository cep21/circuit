package faststats

import (
	"encoding/json"
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

	// retired counts every event that is not in a bucket: it was moved here when its bucket was cleared, or never
	// had a bucket to begin with.  There are deliberately no rollingSum or totalSum fields: both are derived on read
	// (the sum of buckets, plus retired for the total) rather than maintained as further contended counters on every
	// Inc.  That leaves Inc with a single atomic write, keeps this struct's own cache lines read-mostly, and the sums
	// can never disagree with the buckets.
	//
	// Counts only move from a bucket to retired while holding rollingBucket.advanceMu.  Apart from those moves, buckets
	// and retired only ever grow.
	retired AtomicInt64

	rollingBucket RollingBuckets
}

// NewRollingCounter initializes a rolling counter with a bucket width and # of buckets
func NewRollingCounter(bucketWidth time.Duration, numBuckets int, now time.Time) RollingCounter {
	return RollingCounter{
		buckets: make([]AtomicInt64, numBuckets),
		rollingBucket: RollingBuckets{
			NumBuckets:  numBuckets,
			BucketWidth: bucketWidth,
			StartTime:   now,
		},
	}
}

var _ json.Marshaler = &RollingCounter{}
var _ json.Unmarshaler = &RollingCounter{}
var _ fmt.Stringer = &RollingCounter{}

type jsonCounter struct {
	Buckets []AtomicInt64
	// RollingSum is derived from Buckets.  It is still emitted for compatibility/readability, and ignored on read.
	RollingSum    *AtomicInt64
	TotalSum      *AtomicInt64
	RollingBucket *RollingBuckets
}

// MarshalJSON JSON encodes a counter.  It is thread safe.
func (r *RollingCounter) MarshalJSON() ([]byte, error) {
	var buckets []int64
	if r.buckets != nil {
		buckets = make([]int64, len(r.buckets))
	}
	var rollingSum int64
	for i := range r.buckets {
		buckets[i] = r.buckets[i].Get()
		rollingSum += buckets[i]
	}
	// Same shape as jsonCounter.  TotalSum is read after the buckets so it always covers them.
	return json.Marshal(struct {
		Buckets       []int64
		RollingSum    int64
		TotalSum      int64
		RollingBucket *RollingBuckets
	}{
		Buckets:       buckets,
		RollingSum:    rollingSum,
		TotalSum:      r.TotalSum(),
		RollingBucket: &r.rollingBucket,
	})
}

// UnmarshalJSON stores the previous JSON encoding.  Note, this is *NOT* thread safe.
// Returns an error if the JSON is missing required fields (i.e., was not produced
// by MarshalJSON or was truncated) or is internally inconsistent; the receiver is left unmodified in that case.
func (r *RollingCounter) UnmarshalJSON(b []byte) error {
	var into jsonCounter
	if err := json.Unmarshal(b, &into); err != nil {
		return err
	}
	if into.TotalSum == nil || into.RollingBucket == nil {
		return fmt.Errorf("RollingCounter.UnmarshalJSON: incomplete JSON (missing required fields)")
	}
	if len(into.Buckets) != into.RollingBucket.NumBuckets {
		return fmt.Errorf("RollingCounter.UnmarshalJSON: inconsistent JSON (Buckets len=%d, NumBuckets=%d)",
			len(into.Buckets), into.RollingBucket.NumBuckets)
	}
	if err := into.RollingBucket.validate(); err != nil {
		return fmt.Errorf("RollingCounter.UnmarshalJSON: %w", err)
	}
	r.buckets = into.Buckets
	r.retired.Store(into.TotalSum.Get() - r.sumBuckets())
	r.rollingBucket.Store(into.RollingBucket)
	return nil
}

// String for debugging
func (r *RollingCounter) String() string {
	return r.StringAt(time.Now())
}

// StringAt converts the counter to a string at a given time.
func (r *RollingCounter) StringAt(now time.Time) string {
	b := r.GetBuckets(now)
	parts := make([]string, 0, len(r.buckets))
	var rollingSum int64
	for _, v := range b {
		rollingSum += v
		parts = append(parts, strconv.FormatInt(v, 10))
	}
	return fmt.Sprintf("rolling_sum=%d total_sum=%d parts=(%s)", rollingSum, r.TotalSum(), strings.Join(parts, ","))
}

// Inc adds a single event to the current bucket
func (r *RollingCounter) Inc(now time.Time) {
	if len(r.buckets) != 0 {
		if idx := r.rollingBucket.Advance(now, r.clearBucket); idx >= 0 {
			r.buckets[idx].Add(1)
			return
		}
	}
	// Outside of the rolling window, but still part of TotalSum
	r.retired.Add(1)
}

func (r *RollingCounter) sumBuckets() int64 {
	var ret int64
	for i := range r.buckets {
		ret += r.buckets[i].Get()
	}
	return ret
}

// RollingSumAt returns the total number of events in the rolling time window
func (r *RollingCounter) RollingSumAt(now time.Time) int64 {
	r.rollingBucket.Advance(now, r.clearBucket)
	return r.sumBuckets()
}

// RollingSum returns the total number of events in the rolling time window (With time time.Now())
func (r *RollingCounter) RollingSum() int64 {
	return r.RollingSumAt(time.Now())
}

// TotalSum returns the total number of events of all time
func (r *RollingCounter) TotalSum() int64 {
	// The lock keeps counts from moving between a bucket and retired while we add them up.  Every term can then only
	// grow, so the result is exact and never smaller than an earlier TotalSum.
	r.rollingBucket.advanceMu.Lock()
	defer r.rollingBucket.advanceMu.Unlock()
	return r.retired.Get() + r.sumBuckets()
}

// GetBuckets returns a copy of the buckets in order backwards in time
func (r *RollingCounter) GetBuckets(now time.Time) []int64 {
	if r.rollingBucket.NumBuckets <= 0 || len(r.buckets) == 0 {
		return nil
	}
	r.rollingBucket.Advance(now, r.clearBucket)
	startIdx := int(r.rollingBucket.LastAbsIndex.Get() % int64(r.rollingBucket.NumBuckets))
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

// clearBucket retires everything in a bucket.  rollingBucket.advanceMu must be held (Advance holds it).
func (r *RollingCounter) clearBucket(idx int) {
	if r.buckets[idx].Get() != 0 {
		r.retired.Add(r.buckets[idx].Swap(0))
	}
}

// Reset the rolling window to all zero values.  TotalSum is not affected.
func (r *RollingCounter) Reset(now time.Time) {
	r.rollingBucket.Advance(now, r.clearBucket)
	r.rollingBucket.advanceMu.Lock()
	defer r.rollingBucket.advanceMu.Unlock()
	for i := range r.buckets {
		r.clearBucket(i)
	}
}
