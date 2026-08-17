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

	// totalSum counts every event ever.  There is deliberately no rollingSum field: the rolling sum is derived on read
	// as the sum of buckets (a handful of atomic loads) rather than maintained as a second contended counter on every
	// Inc, which is both faster on the hot path and can never disagree with the buckets (a separately-maintained sum
	// could transiently go negative).
	totalSum AtomicInt64

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
	var rollingSum AtomicInt64
	rollingSum.Set(r.sumBuckets())
	return json.Marshal(jsonCounter{
		Buckets:       r.buckets,
		RollingSum:    &rollingSum,
		TotalSum:      &r.totalSum,
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
	r.totalSum.Store(into.TotalSum.Get())
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
	r.totalSum.Add(1)
	if len(r.buckets) == 0 {
		return
	}
	idx := r.rollingBucket.Advance(now, r.clearBucket)
	if idx < 0 {
		return
	}
	r.buckets[idx].Add(1)
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
	return r.totalSum.Get()
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

func (r *RollingCounter) clearBucket(idx int) {
	r.buckets[idx].Set(0)
}

// Reset the counter to all zero values.
func (r *RollingCounter) Reset(now time.Time) {
	r.rollingBucket.Advance(now, r.clearBucket)
	for i := range r.buckets {
		r.clearBucket(i)
	}
}
