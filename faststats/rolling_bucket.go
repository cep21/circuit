package faststats

import (
	"fmt"
	"time"
)

// RollingBuckets simulates a time rolling list of buckets of items.  It is safe to use JSON to encode this object
// in a thread safe way.
//
// This implementation cheats in order to not take a lock.  It is correct, but only if the total size of the buckets (NumBuckets * BucketWidth)
// is less than any duration of how long Advance will take to execute.  In anything but super small bucket sizes this should
// be fine.  The common case, where (NumBuckets * BucketWidth >= 1sec) should always work.
type RollingBuckets struct {
	NumBuckets   int
	StartTime    time.Time
	BucketWidth  time.Duration
	LastAbsIndex AtomicInt64
}

var _ fmt.Stringer = &RollingBuckets{}

func (r *RollingBuckets) String() string {
	return fmt.Sprintf("RollingBucket(num=%d, width=%s)", r.NumBuckets, r.BucketWidth)
}

// Advance to now, clearing buckets as needed
func (r *RollingBuckets) Advance(now time.Time, clearBucket func(int)) int {
	if r.NumBuckets == 0 {
		return -1
	}
	diff := now.Sub(r.StartTime)
	if diff < 0 {
		// This point is before init.  That is invalid.  We should ignore it.
		return -1
	}
	absIndex := int(diff.Nanoseconds() / r.BucketWidth.Nanoseconds())
	lastAbsVal := int(r.LastAbsIndex.Get())
	indexDiff := absIndex - lastAbsVal
	if indexDiff == 0 {
		// We are at the right time
		return absIndex % r.NumBuckets
	}
	if indexDiff < 0 {
		// This point is backwards in time.  We should return a valid
		// index past where we are
		if indexDiff >= r.NumBuckets {
			// We rolled past the list.  This point is before the start
			// of our rolling window.  We should just do what ... ignore it?
			return -1
		}
		return absIndex % r.NumBuckets
	}
	for i := 0; i < r.NumBuckets && lastAbsVal < absIndex; i++ {
		if !r.LastAbsIndex.CompareAndSwap(int64(lastAbsVal), int64(lastAbsVal)+1) {
			// someone else is swapping
			return r.Advance(now, clearBucket)
		}
		lastAbsVal++
		clearBucket(lastAbsVal % r.NumBuckets)
	}
	// indexDiff > 0 at this point.  We have to roll our window forward
	// Cleared all the buckets.  Try to advance back to wherever we need
	r.LastAbsIndex.CompareAndSwap(int64(lastAbsVal), int64(absIndex))
	return r.Advance(now, clearBucket)
}

func (r *RollingBuckets) Store(bucket *RollingBuckets) {
	r.NumBuckets = bucket.NumBuckets
	r.StartTime = bucket.StartTime
	r.BucketWidth = bucket.BucketWidth
	r.LastAbsIndex.Store(bucket.LastAbsIndex.Get())
}
