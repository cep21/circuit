package faststats

import (
	"encoding/json"
	"expvar"
	"fmt"
	"math"
	"slices"
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
	if len(s) == 0 || math.IsNaN(p) {
		// A meaningless value for a meaningless list or meaningless percentile
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
	size := 0
	for idx := range r.buckets {
		size += r.buckets[idx].size()
	}
	ret := make([]time.Duration, 0, size)
	for idx := range r.buckets {
		ret = r.buckets[idx].appendDurations(ret)
	}
	slices.Sort(ret)
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
	// durations is a fixed size and cannot change during operation.  Each slot holds nanoseconds+1 so that zero can
	// mean "reserved by addDuration but not yet written" (or cleared): readers skip those instead of reporting a
	// stale value from a previous window.
	durationsSomeInvalid []AtomicInt64
	currentIndex         AtomicInt64
}

const unsetDuration = 0

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
	// DurationsSomeInvalid holds the valid durations (nanoseconds) followed by zero padding up to the bucket size
	DurationsSomeInvalid []int64
	// CurrentIndex is how many leading entries of DurationsSomeInvalid are valid
	CurrentIndex int64
}

// MarshalJSON returns the durations (in nanoseconds) as JSON.  It is thread safe.
func (b *durationsBucket) MarshalJSON() ([]byte, error) {
	m := forMarshal{
		DurationsSomeInvalid: make([]int64, 0, len(b.durationsSomeInvalid)),
	}
	for _, d := range b.Durations() {
		m.DurationsSomeInvalid = append(m.DurationsSomeInvalid, d.Nanoseconds())
	}
	m.CurrentIndex = int64(len(m.DurationsSomeInvalid))
	for len(m.DurationsSomeInvalid) < len(b.durationsSomeInvalid) {
		m.DurationsSomeInvalid = append(m.DurationsSomeInvalid, 0)
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
	if m.CurrentIndex < 0 {
		m.CurrentIndex = 0
	}
	if m.CurrentIndex > int64(len(m.DurationsSomeInvalid)) {
		m.CurrentIndex = int64(len(m.DurationsSomeInvalid))
	}
	for idx := range m.DurationsSomeInvalid {
		if int64(idx) < m.CurrentIndex {
			b.durationsSomeInvalid[idx].Set(encodeDuration(time.Duration(m.DurationsSomeInvalid[idx])))
		} else {
			b.durationsSomeInvalid[idx].Set(unsetDuration)
		}
	}
	b.currentIndex.Set(m.CurrentIndex)
	return nil
}

// encodeDuration maps a duration onto the slot encoding (ns+1, so 0 stays free as the unset sentinel).  Negative
// durations are meaningless as latencies and are clamped to zero.
func encodeDuration(d time.Duration) int64 {
	if d < 0 {
		d = 0
	}
	if d == math.MaxInt64 {
		// Cannot +1; sacrifice 1ns of range rather than collide with the unset sentinel
		return math.MaxInt64
	}
	return d.Nanoseconds() + 1
}

// size is an upper bound on how many durations are currently stored
func (b *durationsBucket) size() int {
	maxIndex := b.currentIndex.Get()
	if maxIndex > int64(len(b.durationsSomeInvalid)) {
		return len(b.durationsSomeInvalid)
	}
	if maxIndex < 0 {
		return 0
	}
	return int(maxIndex)
}

// Durations returns the durations currently stored in this bucket
func (b *durationsBucket) Durations() []time.Duration {
	return b.appendDurations(make([]time.Duration, 0, b.size()))
}

func (b *durationsBucket) appendDurations(ret []time.Duration) []time.Duration {
	maxIndex := b.size()
	for i := 0; i < maxIndex; i++ {
		v := b.durationsSomeInvalid[i].Get()
		if v == unsetDuration {
			continue
		}
		ret = append(ret, time.Duration(v-1))
	}
	return ret
}

func (b *durationsBucket) clear() {
	for i := range b.durationsSomeInvalid {
		b.durationsSomeInvalid[i].Set(unsetDuration)
	}
	b.currentIndex.Set(0)
}

func (b *durationsBucket) addDuration(d time.Duration) {
	if len(b.durationsSomeInvalid) == 0 {
		return
	}
	nextIndex := b.currentIndex.Add(1) - 1
	arrayIndex := nextIndex % int64(len(b.durationsSomeInvalid))
	b.durationsSomeInvalid[arrayIndex].Set(encodeDuration(d))
}
