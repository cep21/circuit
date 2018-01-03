package circuit

import "fmt"

var errThrottledConcucrrentCommands = &hystrixError{concurrencyLimitReached: true, msg: "throttling connections to command"}
var errCircuitOpen = &hystrixError{circuitOpen: true, msg: "circuit is open"}

// hystrixError is used for internally generated errors
type hystrixError struct {
	concurrencyLimitReached bool
	circuitOpen             bool
	msg                     string
}

func (m *hystrixError) Error() string {
	return fmt.Sprintf("%s: concurrencyReached=%t circuitOpen=%t", m.msg, m.HystrixConcurrencyLimitReached(), m.HystrixCiruitOpen())
}

func (m *hystrixError) HystrixConcurrencyLimitReached() bool {
	return m.concurrencyLimitReached
}

func (m *hystrixError) HystrixCiruitOpen() bool {
	return m.circuitOpen
}

// BadRequest is implemented by an error returned by runFunc if you want to consider the requestor bad, not the circuit
// bad.  See http://netflix.github.io/Hystrix/javadoc/com/netflix/hystrix/exception/HystrixBadRequestException.html
// and https://github.com/Netflix/Hystrix/wiki/How-To-Use#error-propagation for information.
type BadRequest interface {
	HystrixBadRequest() bool
}

// IsBadRequest returns true if the error is of type BadRequest
func IsBadRequest(err error) bool {
	if err == nil {
		return false
	}
	br, ok := err.(BadRequest)
	return ok && br.HystrixBadRequest()
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

// HystrixBadRequest always returns true
func (s SimpleBadRequest) HystrixBadRequest() bool {
	return true
}

var _ error = &SimpleBadRequest{}
var _ BadRequest = &SimpleBadRequest{}

var _ error = &hystrixError{}
