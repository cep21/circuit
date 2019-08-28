package circuit

import "fmt"

var errThrottledConcucrrentCommands = &circuitError{concurrencyLimitReached: true, msg: "throttling connections to command"}
var errCircuitOpen = &circuitError{circuitOpen: true, msg: "circuit is open"}

// circuitError is used for internally generated errors
type circuitError struct {
	concurrencyLimitReached bool
	circuitOpen             bool
	msg                     string
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
	return fmt.Sprintf("%s: concurrencyReached=%t circuitOpen=%t", m.msg, m.ConcurrencyLimitReached(), m.CircuitOpen())
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

// IsBadRequest returns true if the error is of type BadRequest
func IsBadRequest(err error) bool {
	if err == nil {
		return false
	}
	br, ok := err.(BadRequest)
	return ok && br.BadRequest()
}

// SimpleBadRequest is a simple wrapper for an error to mark it as a bad request
type SimpleBadRequest struct {
	Err error
}

// Cause returns the wrapped error
func (s SimpleBadRequest) Cause() error {
	return s.Err
}

// Cause returns the wrapped error
func (s SimpleBadRequest) Error() string {
	return s.Err.Error()
}

// BadRequest always returns true
func (s SimpleBadRequest) BadRequest() bool {
	return true
}

var _ error = &SimpleBadRequest{}
var _ BadRequest = &SimpleBadRequest{}

var _ error = &circuitError{}
