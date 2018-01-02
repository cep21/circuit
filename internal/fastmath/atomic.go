package fastmath

import (
	"strconv"
	"sync/atomic"
	"time"
)

// AtomicBoolean is a helper struct to simulate atomic operations on a boolean
type AtomicBoolean struct{ flag uint32 }

// Get the current atomic value
func (a *AtomicBoolean) Get() bool {
	return atomic.LoadUint32(&a.flag) == 1
}

// Set the atomic boolean value
func (a *AtomicBoolean) Set(value bool) {
	if value {
		atomic.StoreUint32(&a.flag, 1)
	} else {
		atomic.StoreUint32(&a.flag, 0)
	}
}

func (a *AtomicBoolean) String() string {
	return strconv.FormatBool(a.Get())
}

// AtomicInt64 is a helper struct to simulate atomic operations on an int64
// Note that I could have used `type AtomicInt642 int64`, but I did not want to make it easy
// to do + and - operations so easily without using atomic functions.
type AtomicInt64 struct{ val int64 }

// Get the current int64
func (a *AtomicInt64) Get() int64 {
	return atomic.LoadInt64(&a.val)
}

func (a *AtomicInt64) String() string {
	return strconv.FormatInt(a.Get(), 10)
}

// Swap the current value with a new one
func (a *AtomicInt64) Swap(newValue int64) int64 {
	return atomic.SwapInt64(&a.val, newValue)
}

// Add a value to the current store
func (a *AtomicInt64) Add(value int64) int64 {
	return atomic.AddInt64(&a.val, value)
}

// Set the current store to a value
func (a *AtomicInt64) Set(value int64) {
	atomic.StoreInt64(&a.val, value)
}

// CompareAndSwap acts like a CompareAndSwap operation on the value
func (a *AtomicInt64) CompareAndSwap(expected int64, newVal int64) bool {
	return atomic.CompareAndSwapInt64(&a.val, expected, newVal)
}

// Duration returns the currently stored value as a time.Duration
func (a *AtomicInt64) Duration() time.Duration {
	return time.Duration(a.Get())
}
