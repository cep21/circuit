package faststats

import (
	"encoding/json"
	"strconv"
	"sync/atomic"
	"time"
)

// AtomicBoolean is a helper struct to simulate atomic operations on a boolean
type AtomicBoolean struct {
	atomic.Bool
}

// Get the current atomic value
func (a *AtomicBoolean) Get() bool {
	return a.Load()
}

// Set the atomic boolean value
func (a *AtomicBoolean) Set(value bool) {
	a.Store(value)
}

// String returns "true" or "false"
func (a *AtomicBoolean) String() string {
	return strconv.FormatBool(a.Get())
}

var _ json.Marshaler = &AtomicBoolean{}
var _ json.Unmarshaler = &AtomicBoolean{}

// MarshalJSON encodes this value in a thread safe way as a json bool
func (a *AtomicBoolean) MarshalJSON() ([]byte, error) {
	return json.Marshal(a.Get())
}

// UnmarshalJSON decodes this value in a thread safe way as a json bool
func (a *AtomicBoolean) UnmarshalJSON(b []byte) error {
	var into bool
	if err := json.Unmarshal(b, &into); err != nil {
		return err
	}
	a.Set(into)
	return nil
}

// AtomicInt64 is a helper struct to simulate atomic operations on an int64
// Note that I could have used `type AtomicInt642 int64`, but I did not want to make it easy
// to do + and - operations so easily without using atomic functions.
type AtomicInt64 struct {
	atomic.Int64
}

var _ json.Marshaler = &AtomicInt64{}
var _ json.Unmarshaler = &AtomicInt64{}

// MarshalJSON encodes this value as an int in a thread safe way
func (a *AtomicInt64) MarshalJSON() ([]byte, error) {
	return json.Marshal(a.Get())
}

// UnmarshalJSON decodes this value as an int in a thread safe way
func (a *AtomicInt64) UnmarshalJSON(b []byte) error {
	var into int64
	if err := json.Unmarshal(b, &into); err != nil {
		return err
	}
	a.Set(into)
	return nil
}

// Get the current int64
func (a *AtomicInt64) Get() int64 {
	return a.Load()
}

// String returns the integer as a string in a thread safe way
func (a *AtomicInt64) String() string {
	return strconv.FormatInt(a.Get(), 10)
}

// Set the current store to a value
func (a *AtomicInt64) Set(value int64) {
	a.Store(value)
}

// Duration returns the currently stored value as a time.Duration
func (a *AtomicInt64) Duration() time.Duration {
	return time.Duration(a.Get())
}
