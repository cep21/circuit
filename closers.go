package hystrix

import (
	"time"
)

// ClosedToOpen receives events and controls if the circuit should open or close as a result of those events.
// Return true if the circuit should open, false if the circuit should close.
type ClosedToOpen interface {
	// TODO: These could just be `RunMetrics`
	// Closed is called when the circuit transitions from Open to Closed.  Your logic should reinitialize itself
	// to prepare for when it should try to open back up again.
	Closed(now time.Time)
	// Even though the circuit is closed, and we want to allow the circuit to remain closed, we still prevent this
	// command from happening.  The error will return as a short circuit to the caller, as well as trigger fallback
	// logic.  This could be useful if your circuit is closed, but some external force wants you to pretend to be open.
	Prevent(now time.Time) bool
	// SuccessfulAttempt is called any time runFunc was called and appeared healthy
	SuccessfulAttempt(now time.Time, duration time.Duration)
	// Any time run was called, but we backed out of the attempt or the return value doesn't appear like a legitimate run
	// These are probably not failures that would make you open a circuit, but could be useful to know about
	BackedOutAttempt(now time.Time)
	// Any time the run function failed for a real reason.  You should expect an AttemptToOpen call later, to see if
	// the circuit should be opened because of this error
	ErrorAttempt(now time.Time)
	// AttemptToOpen a circuit that is currently closed, after a bad request comes in.  Only called after bad requests,
	// never called after a successful request
	AttemptToOpen(now time.Time) bool
}

// OpenToClosed controls logic that tries to close an open circuit
type OpenToClosed interface {
	// Opened circuit. It should now check to see if it should ever allow various requests in an attempt to become closed
	Opened(now time.Time)
	// The circuit is currently closed.  Check and return true if this request should be allowed.  This will signal
	// the circuit in a "half-open" state, allowing that one request.
	// If any requests are allowed, the circuit moves into a half open state.
	Allow(now time.Time) (shouldAllow bool)
	// SuccessfulAttempt any time runFunc was called and appeared healthy
	SuccessfulAttempt(now time.Time, duration time.Duration)
	// Any time run was called, but we backed out of the attempt or the return value doesn't appear like a legitimate run
	BackedOutAttempt(now time.Time)
	// Any time the run function failed for a real reason
	ErrorAttempt(now time.Time)
	// AttemptToOpen a circuit that is currently closed, after a bad request comes in
	AttemptToClose(now time.Time) bool
}

func neverOpensFactory() ClosedToOpen {
	return &neverOpens{}
}

type neverOpens struct{}

func (c neverOpens) Closed(now time.Time) {}

func (c *neverOpens) Prevent(now time.Time) bool {
	return false
}

func (c *neverOpens) SuccessfulAttempt(now time.Time, duration time.Duration) {}

func (c *neverOpens) BackedOutAttempt(now time.Time) {}

func (c *neverOpens) ErrorAttempt(now time.Time) {}

func (c *neverOpens) AttemptToOpen(now time.Time) bool {
	return false
}

var _ ClosedToOpen = &neverOpens{}

func neverClosesFactory() OpenToClosed {
	return neverCloses{}
}

type neverCloses struct{}

func (c neverCloses) Opened(now time.Time) {}

func (c neverCloses) Allow(now time.Time) (shouldAllow bool) {
	return false
}

func (c neverCloses) SuccessfulAttempt(now time.Time, duration time.Duration) {}

func (c neverCloses) BackedOutAttempt(now time.Time) {}

func (c neverCloses) ErrorAttempt(now time.Time) {}

func (c neverCloses) AttemptToClose(now time.Time) bool {
	return false
}

var _ OpenToClosed = neverCloses{}
