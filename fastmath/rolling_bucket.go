package fastmath

import "time"

type RollingBuckets struct {
	NumBuckets int
	StartTime time.Time
	BucketWidth time.Duration
	ClearBucket func(int)

	lastAbsIndex AtomicInt64
}

func (r *RollingBuckets) Init(numBuckets int, bucketWidth time.Duration, now time.Time, clearBucket func(int)) {
	r.NumBuckets = numBuckets
	r.StartTime = now
	r.BucketWidth = bucketWidth
	r.ClearBucket = clearBucket
}

func (r *RollingBuckets) Advance(now time.Time) int {
	diff := now.Sub(r.StartTime)
	if diff < 0 {
		// This point is before init.  That is invalid.  We should ignore it.
		return -1
	}
	absIndex := int(diff.Nanoseconds() / r.BucketWidth.Nanoseconds())
	lastAbsVal := int(r.lastAbsIndex.Get())
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
		ret := (absIndex % r.NumBuckets) - indexDiff
		if ret < 0 {
			ret += r.NumBuckets
		}
		return int(ret)
	}
	for i :=0;i<r.NumBuckets;i++ {
		if !r.lastAbsIndex.CompareAndSwap(int64(lastAbsVal), int64(lastAbsVal) + 1) {
			// someone else is swapping
			return r.Advance(now)
		}
		lastAbsVal = lastAbsVal + 1
		r.ClearBucket(lastAbsVal)
	}
	// indexDiff > 0 at this point.  We have to roll our window forward
	// Cleared all the buckets.  Try to advance back to wherever we need
	r.lastAbsIndex.CompareAndSwap(int64(lastAbsVal), int64(absIndex))
	return r.Advance(now)
}