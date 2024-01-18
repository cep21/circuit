package circuit

import (
	"context"
	"time"
)

// ClosedToOpen receives events and controls if the circuit should open or close as a result of those events.
// Return true if the circuit should open, false if the circuit should close.
type ClosedToOpen interface {
	RunMetrics
	Metrics
	// ShouldOpen will attempt to open a circuit that is currently closed, after a bad request comes in.  Only called
	// after bad requests, never called after a successful request
	ShouldOpen(ctx context.Context, now time.Time) bool
	// Prevent a single request from going through while the circuit is closed.
	// Even though the circuit is closed, and we want to allow the circuit to remain closed, we still prevent this
	// command from happening.  The error will return as a short circuit to the caller, as well as trigger fallback
	// logic.  This could be useful if your circuit is closed, but some external force wants you to pretend to be open.
	Prevent(ctx context.Context, now time.Time) bool
}

// OpenToClosed controls logic that tries to close an open circuit
type OpenToClosed interface {
	RunMetrics
	Metrics
	// ShouldClose is called after a request is allowed to go through, and the circuit is open.  If the circuit should
	// now close, return true.  If the circuit should remain open, return false.
	ShouldClose(ctx context.Context, now time.Time) bool
	// Allow a single request while remaining in the closed state
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
