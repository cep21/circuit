package fastmath

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

	// Constant.  no mutex needed
	bucketWidth time.Duration

	// This uses a combination of RWMutex and atomic operations.  Very strange!
	// The mutex controls access to currentBucket and bucketStart.
	mu            sync.Mutex
	currentBucket int
	bucketStart   time.Time

	fastPathCurrentBucket atomic.Value
}

type currentBucketInfo struct {
	currentBucket int
	bucketStart   time.Time
}

// NewRollingCounter initializes a rolling counter with a bucket width and # of buckets
func NewRollingCounter(bucketWidth time.Duration, numBuckets int) RollingCounter {
	return RollingCounter{
		buckets:     make([]AtomicInt64, numBuckets),
		bucketWidth: bucketWidth,
	}
}

// String for debugging
func (r *RollingCounter) String() string {
	return r.StringAt(time.Now())
}

// StringAt converts the counter to a string at a given time.
func (r *RollingCounter) StringAt(now time.Time) string {
	r.mu.Lock()
	r.advance(now)
	parts := make([]string, 0, len(r.buckets))
	for _, v := range r.getBucketsRequireReadLock() {
		parts = append(parts, strconv.FormatInt(v, 10))
	}
	r.mu.Unlock()
	return fmt.Sprintf("rolling_sum=%d total_sum=%d parts=(%s)", r.RollingSum(now), r.TotalSum(), strings.Join(parts, ","))
}

// Inc adds a single event to the current bucket
func (r *RollingCounter) Inc(now time.Time) {
	r.totalSum.Add(1)
	if r.incFastPath(now) {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.advance(now)
	if len(r.buckets) != 0 {
		r.buckets[r.currentBucket].Add(1)
	}
	r.rollingSum.Add(1)
}

func (r *RollingCounter) incFastPath(now time.Time) bool {
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
	r.buckets[currentBucket.currentBucket].Add(1)
	r.rollingSum.Add(1)
	return true
}

// RollingSum returns the total number of events in the rolling time window
func (r *RollingCounter) RollingSum(now time.Time) int64 {
	if ret := r.rollingSumFastPath(now); ret != -1 {
		return ret
	}
	r.mu.Lock()
	r.advance(now)
	r.mu.Unlock()
	return r.rollingSum.Get()
}

func (r *RollingCounter) rollingSumFastPath(now time.Time) int64 {
	currentBucketPtr := r.fastPathCurrentBucket.Load()
	if currentBucketPtr == nil {
		return -1
	}
	currentBucket := currentBucketPtr.(*currentBucketInfo)
	if currentBucket.bucketStart.After(now) {
		return -1
	}
	diff := now.Sub(currentBucket.bucketStart)
	if diff >= r.bucketWidth {
		return -1
	}
	return r.rollingSum.Get()
}

// RollingSum returns the total number of events of all time
func (r *RollingCounter) TotalSum() int64 {
	return r.totalSum.Get()
}

// GetBuckets returns a copy of the buckets in order backwards in time
func (r *RollingCounter) GetBuckets(now time.Time) []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.advance(now)
	return r.getBucketsRequireReadLock()
}

// Returns the values inside the buckets.  Requires having a read lock on the counter.
func (r *RollingCounter) getBucketsRequireReadLock() []int64 {
	ret := make([]int64, 0, len(r.buckets))
	for i := 0; i < len(r.buckets); i++ {
		index := r.currentBucket - i
		if index < 0 {
			index += len(r.buckets)
		}
		ret = append(ret, r.buckets[index].Get())
	}
	return ret
}

// Reset the counter to all zero values.
func (r *RollingCounter) Reset(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resetMustHaveWriteLock(now)
}

// Resets the values of the counter.  It is required to have a write lock at this time.
func (r *RollingCounter) resetMustHaveWriteLock(now time.Time) {
	for i := range r.buckets {
		r.buckets[i].Set(0)
	}
	r.currentBucket = 0
	r.rollingSum.Set(0)
	r.bucketStart = now
	r.fastPathCurrentBucket.Store(&currentBucketInfo{
		bucketStart: r.bucketStart,
	})
}

// Advance forward, clearing buckets as needed.  Will return
// with a read lock on the mutex (meaning access to currentBucket
// and bucketStart are ok for reading).  You must release this
// read lock when you're done reading from either value.
func (r *RollingCounter) advance(now time.Time) {
	if !now.After(r.bucketStart) {
		// advance backwards is ignored
		return
	}
	diff := now.Sub(r.bucketStart)
	if diff < r.bucketWidth {
		return
	}

	if r.bucketStart.IsZero() {
		r.fastPathCurrentBucket.Store(&currentBucketInfo{
			currentBucket: r.currentBucket,
			bucketStart:   r.bucketStart,
		})
		r.bucketStart = now
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
		toSubtract := r.buckets[r.currentBucket].Swap(0)
		r.rollingSum.Add(-toSubtract)
		r.bucketStart = r.bucketStart.Add(r.bucketWidth)
		r.fastPathCurrentBucket.Store(&currentBucketInfo{
			currentBucket: r.currentBucket,
			bucketStart:   r.bucketStart,
		})
	}
	r.resetMustHaveWriteLock(now)
}
