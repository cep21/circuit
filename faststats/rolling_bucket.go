package faststats

import (
	"fmt"
	"math"
	"math/bits"
	"sync"
	"time"
)

// RollingBuckets simulates a time rolling list of buckets of items.  It is safe to use JSON to encode this object
// in a thread safe way.  A RollingBuckets must not be copied after first use.
//
// The steady state (now falls in the current bucket) is a single atomic load and no 64-bit division.  Rolling the
// window forward takes a mutex, at most once per BucketWidth, so that buckets are always cleared *before* the new
// index is published to concurrent writers.
//
// One inherent limitation remains: a write whose bucket index was computed a full window (NumBuckets*BucketWidth) or
// more before it lands -- because the caller's `now` lags that far behind another caller's, or the goroutine was
// descheduled that long between Advance and the write -- may be cleared or land in a newer bucket.  With realistic
// windows this does not happen.
type RollingBuckets struct {
	NumBuckets   int
	StartTime    time.Time
	BucketWidth  time.Duration
	LastAbsIndex AtomicInt64
	// advanceMu is only taken when the window actually needs to roll forward
	advanceMu sync.Mutex
}

var _ fmt.Stringer = &RollingBuckets{}

func (r *RollingBuckets) String() string {
	return fmt.Sprintf("RollingBucket(num=%d, width=%s)", r.NumBuckets, r.BucketWidth)
}

// Advance to now, clearing buckets as needed.  Returns the bucket index that `now` falls into, or -1 if `now` is
// before StartTime or a full window or more behind the most recent Advance.
func (r *RollingBuckets) Advance(now time.Time, clearBucket func(int)) int {
	if r.NumBuckets <= 0 || r.BucketWidth <= 0 {
		return -1
	}
	diff := now.Sub(r.StartTime)
	if diff < 0 {
		// This point is before init.  That is invalid.  We should ignore it.
		return -1
	}
	n := int64(r.NumBuckets)
	last := r.LastAbsIndex.Get()
	// Steady state: now falls inside bucket `last`, i.e. last*width <= diff < (last+1)*width.  Testing that takes a
	// multiply where computing absIndex takes a (much slower) 64-bit divide.  The 128-bit product keeps the test exact
	// for any LastAbsIndex, including a negative or absurdly large one written by the user.
	hi, lo := bits.Mul64(uint64(last), uint64(r.BucketWidth)) //nolint:gosec // negative last is rejected below
	if hi == 0 && lo <= uint64(diff) && uint64(diff)-lo < uint64(r.BucketWidth) {
		return int(mod(last, n))
	}
	// Keep the absolute index in int64: as an int it wraps on 32-bit platforms (1ms buckets => 24 days).
	absIndex := diff.Nanoseconds() / r.BucketWidth.Nanoseconds()
	bucket := int(mod(absIndex, n))
	if behind := last - absIndex; behind >= 0 {
		// No need to roll: we are behind the current time.
		if behind >= n {
			// We rolled past the list.  This point is before the start of our rolling window. Ignore it.
			return -1
		}
		return bucket
	}

	// Slow path: the window has to roll forward.
	if r.rollForward(absIndex, n, clearBucket)-absIndex >= n {
		// While we waited for the lock someone advanced an entire window (or more) past us
		return -1
	}
	return bucket
}

// mod returns a % n for n > 0.  It uses a 32-bit divide when the operands allow it, which is several times
// faster than a 64-bit one on many CPUs (and avoids a runtime call on 32-bit platforms).
func mod(a int64, n int64) int64 {
	if a|n >= 0 && a|n <= math.MaxUint32 {
		return int64(uint32(a) % uint32(n)) //nolint:gosec // range checked above
	}
	return a % n
}

// rollForward moves LastAbsIndex forward to absIndex, unless another goroutine got there (or further) first, and
// returns the LastAbsIndex it left behind.  It serializes on advanceMu so that exactly one goroutine clears each
// expired bucket, and so LastAbsIndex is only published once those buckets are empty.  Publishing first (as the
// previous lock-free implementation did) lets other goroutines start writing into a bucket that is about to be wiped.
func (r *RollingBuckets) rollForward(absIndex int64, n int64, clearBucket func(int)) int64 {
	r.advanceMu.Lock()
	defer r.advanceMu.Unlock()
	last := r.LastAbsIndex.Get()
	if absIndex <= last {
		return last
	}
	for i := last + 1; i <= absIndex && i <= last+n; i++ {
		clearBucket(int(mod(i, n)))
	}
	r.LastAbsIndex.Set(absIndex)
	return absIndex
}

// Store copies the exported state of bucket into r.  It is not thread safe.
func (r *RollingBuckets) Store(bucket *RollingBuckets) {
	r.NumBuckets = bucket.NumBuckets
	r.StartTime = bucket.StartTime
	r.BucketWidth = bucket.BucketWidth
	r.LastAbsIndex.Store(bucket.LastAbsIndex.Get())
}

// validate returns an error if these settings could never produce a usable window (used to reject corrupt JSON)
func (r *RollingBuckets) validate() error {
	if r.NumBuckets < 0 || r.BucketWidth < 0 || r.LastAbsIndex.Get() < 0 {
		return fmt.Errorf("invalid RollingBuckets: negative NumBuckets(%d), BucketWidth(%d) or LastAbsIndex(%d)",
			r.NumBuckets, r.BucketWidth, r.LastAbsIndex.Get())
	}
	return nil
}
