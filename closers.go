package circuit

import (
	"context"
	"time"
)

// ClosedToOpen receives events and controls if the circuit should open or close as a result of those events.
// Return true if the circuit should open, false if the circuit should close.
//
// Every method, including the embedded Metrics' Opened/Closed (which the circuit delivers to its own ClosedToOpen
// synchronously, inside the transition critical section, before any other listener), is invoked on the request
// path or inside that critical section.  Implementations must be fast and non-blocking and must not call
// OpenCircuit/CloseCircuit on the same circuit.
type ClosedToOpen interface {
	RunMetrics
	Metrics
	// ShouldOpen will attempt to open a circuit that is currently closed, after a bad request comes in.  Only called
	// after bad requests, never called after a successful request.  It may be asked twice for one failure: once as
	// a cheap pre-filter and, if that says yes, again under the circuit's transition lock right before the state
	// flips (the first answer can go stale if the circuit opened and closed in between).  Not called while
	// ForcedClosed is set.
	ShouldOpen(ctx context.Context, now time.Time) bool
	// Prevent a single request from going through while the circuit is closed.
	// Even though the circuit is closed, and we want to allow the circuit to remain closed, we still prevent this
	// command from happening.  The error will return as a short circuit to the caller, as well as trigger fallback
	// logic.  This could be useful if your circuit is closed, but some external force wants you to pretend to be open.
	Prevent(ctx context.Context, now time.Time) bool
}

// OpenToClosed controls logic that tries to close an open circuit.
//
// Every method, including the embedded Metrics' Opened/Closed (which the circuit delivers to its own OpenToClosed
// synchronously, inside the transition critical section, before any other listener), is invoked on the request
// path or inside that critical section.  Implementations must be fast and non-blocking and must not call
// OpenCircuit/CloseCircuit on the same circuit.
type OpenToClosed interface {
	RunMetrics
	Metrics
	// ShouldClose is called after a request is allowed to go through and succeeds while the circuit is open.  If
	// the circuit should now close, return true.  If the circuit should remain open, return false.  Like
	// ClosedToOpen.ShouldOpen it may be asked twice for one success: once as a cheap pre-filter and again under the
	// circuit's transition lock right before the state flips.  Not called while ForceOpen is set (only an explicit
	// CloseCircuit closes a forced-open circuit).
	ShouldClose(ctx context.Context, now time.Time) bool
	// Allow is consulted while the circuit is OPEN to admit a single half-open probe request: return true to let
	// this one request through to test whether the backend has recovered.  It is not called while ForceOpen is set
	// or when the request would already exceed MaxConcurrentRequests.  Allow may race with Opened for the same open
	// event (a request on another goroutine can observe the open state the instant it flips); implementations that
	// arm state in Opened should return false until armed.
	Allow(ctx context.Context, now time.Time) bool
}

func neverOpensFactory() ClosedToOpen {
	return neverOpens{}
}

type neverOpens struct{}

var _ ClosedToOpen = neverOpens{}

func (c neverOpens) Prevent(_ context.Context, _ time.Time) bool {
	return false
}

func (c neverOpens) Success(_ context.Context, _ time.Time, _ time.Duration)       {}
func (c neverOpens) ErrFailure(_ context.Context, _ time.Time, _ time.Duration)    {}
func (c neverOpens) ErrTimeout(_ context.Context, _ time.Time, _ time.Duration)    {}
func (c neverOpens) ErrBadRequest(_ context.Context, _ time.Time, _ time.Duration) {}
func (c neverOpens) ErrInterrupt(_ context.Context, _ time.Time, _ time.Duration)  {}
func (c neverOpens) ErrConcurrencyLimitReject(_ context.Context, _ time.Time)      {}
func (c neverOpens) ErrShortCircuit(_ context.Context, _ time.Time)                {}
func (c neverOpens) Opened(_ context.Context, _ time.Time)                         {}
func (c neverOpens) Closed(_ context.Context, _ time.Time)                         {}

func (c neverOpens) ShouldOpen(_ context.Context, _ time.Time) bool {
	return false
}

func neverClosesFactory() OpenToClosed {
	return neverCloses{}
}

type neverCloses struct{}

var _ OpenToClosed = neverCloses{}

func (c neverCloses) Allow(_ context.Context, _ time.Time) bool {
	return false
}

func (c neverCloses) Success(_ context.Context, _ time.Time, _ time.Duration)       {}
func (c neverCloses) ErrFailure(_ context.Context, _ time.Time, _ time.Duration)    {}
func (c neverCloses) ErrTimeout(_ context.Context, _ time.Time, _ time.Duration)    {}
func (c neverCloses) ErrBadRequest(_ context.Context, _ time.Time, _ time.Duration) {}
func (c neverCloses) ErrInterrupt(_ context.Context, _ time.Time, _ time.Duration)  {}
func (c neverCloses) ErrConcurrencyLimitReject(_ context.Context, _ time.Time)      {}
func (c neverCloses) ErrShortCircuit(_ context.Context, _ time.Time)                {}
func (c neverCloses) Opened(_ context.Context, _ time.Time)                         {}
func (c neverCloses) Closed(_ context.Context, _ time.Time)                         {}
func (c neverCloses) ShouldClose(_ context.Context, _ time.Time) bool {
	return false
}
