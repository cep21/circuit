package circuit

import (
	"errors"
	"fmt"
)

var errThrottledConcurrentCommands = newCircuitError("throttling connections to command", true, false)
var errThrottledConcurrentFallbacks = newCircuitError("throttling concurrency to fallbacks", true, false)
var errCircuitOpen = newCircuitError("circuit is open", false, true)

// circuitError is used for internally generated errors
type circuitError struct {
	concurrencyLimitReached bool
	circuitOpen             bool
	// msg is the fully formatted Error() string, precomputed so the (shared, immutable) sentinel errors do not
	// allocate on every Error() call.
	msg string
}

func newCircuitError(msg string, concurrencyLimitReached bool, circuitOpen bool) *circuitError {
	return &circuitError{
		concurrencyLimitReached: concurrencyLimitReached,
		circuitOpen:             circuitOpen,
		msg:                     fmt.Sprintf("%s: concurrencyReached=%t circuitOpen=%t", msg, concurrencyLimitReached, circuitOpen),
	}
}

var _ Error = &circuitError{}

// Error is the type of error returned by internal errors using the circuit library.
type Error interface {
	error
	// ConcurrencyLimitReached returns true if this error is because the concurrency limit has been reached.
	ConcurrencyLimitReached() bool
	// CircuitOpen returns true if this error is because the circuit is open.
	CircuitOpen() bool
}

func (m *circuitError) Error() string {
	return m.msg
}

func (m *circuitError) ConcurrencyLimitReached() bool {
	return m.concurrencyLimitReached
}

func (m *circuitError) CircuitOpen() bool {
	return m.circuitOpen
}

// BadRequest is implemented by an error returned by runFunc if you want to consider the requestor bad, not the circuit
// bad.  See http://netflix.github.io/Hystrix/javadoc/com/netflix/hystrix/exception/HystrixBadRequestException.html
// and https://github.com/Netflix/Hystrix/wiki/How-To-Use#error-propagation for information.
type BadRequest interface {
	BadRequest() bool
}

// IsBadRequest returns true if the error is of type BadRequest, checking wrapped errors like errors.As.
func IsBadRequest(err error) bool {
	if err == nil {
		return false
	}
	// Fast paths first: this runs on every failed Execute (including the short-circuit/throttle shed path that
	// matters most under overload), and reflective errors.As costs ~20x more plus an allocation.
	switch e := err.(type) {
	case *circuitError:
		return false
	case BadRequest:
		return e.BadRequest()
	case interface{ Unwrap() error }, interface{ Unwrap() []error }, interface{ As(interface{}) bool }:
		var br BadRequest
		return errors.As(err, &br) && br.BadRequest()
	}
	return false
}

// SimpleBadRequest is a simple wrapper for an error to mark it as a bad request
type SimpleBadRequest struct {
	Err error
}

// Cause returns the wrapped error
func (s SimpleBadRequest) Cause() error {
	return s.Err
}

// Unwrap returns the wrapped error so errors.Is / errors.As can see through a SimpleBadRequest
func (s SimpleBadRequest) Unwrap() error {
	return s.Err
}

// Error returns the error message
func (s SimpleBadRequest) Error() string {
	if s.Err == nil {
		return "bad request"
	}
	return s.Err.Error()
}

// BadRequest always returns true
func (s SimpleBadRequest) BadRequest() bool {
	return true
}

var _ error = &SimpleBadRequest{}
var _ BadRequest = &SimpleBadRequest{}

var _ error = &circuitError{}
