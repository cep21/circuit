package fastmath

import (
	"strconv"
	"sync/atomic"
	"time"
)

// AtomicBoolean is an atomic boolean
type AtomicBoolean struct{ flag int64 }

func (a *AtomicBoolean) Get() bool {
	return atomic.LoadInt64(&a.flag) == 1
}

func (a *AtomicBoolean) Set(value bool) {
	if value {
		atomic.StoreInt64(&a.flag, 1)
	} else {
		atomic.StoreInt64(&a.flag, 0)
	}
}

func (a *AtomicBoolean) String() string {
	return strconv.FormatBool(a.Get())
}

// AtomicInt64 is an atomic int64
type AtomicInt64 struct{ val int64 }

func (a *AtomicInt64) Get() int64 {
	return atomic.LoadInt64(&a.val)
}

func (a *AtomicInt64) String() string {
	return strconv.FormatInt(a.Get(), 10)
}

func (a *AtomicInt64) Swap(newValue int64) int64 {
	return atomic.SwapInt64(&a.val, newValue)
}

func (a *AtomicInt64) Add(value int64) int64 {
	return atomic.AddInt64(&a.val, value)
}

func (a *AtomicInt64) Set(value int64) {
	atomic.StoreInt64(&a.val, value)
}

func (a *AtomicInt64) CompareAndSwap(expected int64, newVal int64) bool {
	return atomic.CompareAndSwapInt64(&a.val, expected, newVal)
}

func (a *AtomicInt64) Duration() time.Duration {
	return time.Duration(a.Get())
}
