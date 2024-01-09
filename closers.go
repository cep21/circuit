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
	// AttemptToOpen a circuit that is currently closed, after a bad request comes in.  Only called after bad requests,
	// never called after a successful request
	ShouldOpen(ctx context.Context, now time.Time) bool
	// Even though the circuit is closed, and we want to allow the circuit to remain closed, we still prevent this
	// command from happening.  The error will return as a short circuit to the caller, as well as trigger fallback
	// logic.  This could be useful if your circuit is closed, but some external force wants you to pretend to be open.
	Prevent(ctx context.Context, now time.Time) bool
}

// OpenToClosed controls logic that tries to close an open circuit
type OpenToClosed interface {
	RunMetrics
	Metrics
	// AttemptToOpen a circuit that is currently closed, after a bad request comes in
	ShouldClose(ctx context.Context, now time.Time) bool
	// Allow a single request while remaining in the closed state
	Allow(ctx context.Context, now time.Time) bool
}

func neverOpensFactory() ClosedToOpen {
	return neverOpens{}
}

type neverOpens struct{}

var _ ClosedToOpen = neverOpens{}

func (c neverOpens) Prevent(ctx context.Context, now time.Time) bool {
	return false
}

func (c neverOpens) Success(ctx context.Context, now time.Time, duration time.Duration)       {}
func (c neverOpens) ErrFailure(ctx context.Context, now time.Time, duration time.Duration)    {}
func (c neverOpens) ErrTimeout(ctx context.Context, now time.Time, duration time.Duration)    {}
func (c neverOpens) ErrBadRequest(ctx context.Context, now time.Time, duration time.Duration) {}
func (c neverOpens) ErrInterrupt(ctx context.Context, now time.Time, duration time.Duration)  {}
func (c neverOpens) ErrConcurrencyLimitReject(ctx context.Context, now time.Time)             {}
func (c neverOpens) ErrShortCircuit(ctx context.Context, now time.Time)                       {}
func (c neverOpens) Opened(ctx context.Context, now time.Time)                                {}
func (c neverOpens) Closed(ctx context.Context, now time.Time)                                {}

func (c neverOpens) ShouldOpen(ctx context.Context, now time.Time) bool {
	return false
}

func neverClosesFactory() OpenToClosed {
	return neverCloses{}
}

type neverCloses struct{}

var _ OpenToClosed = neverCloses{}

func (c neverCloses) Allow(ctx context.Context, now time.Time) bool {
	return false
}

func (c neverCloses) Success(ctx context.Context, now time.Time, duration time.Duration)       {}
func (c neverCloses) ErrFailure(ctx context.Context, now time.Time, duration time.Duration)    {}
func (c neverCloses) ErrTimeout(ctx context.Context, now time.Time, duration time.Duration)    {}
func (c neverCloses) ErrBadRequest(ctx context.Context, now time.Time, duration time.Duration) {}
func (c neverCloses) ErrInterrupt(ctx context.Context, now time.Time, duration time.Duration)  {}
func (c neverCloses) ErrConcurrencyLimitReject(ctx context.Context, now time.Time)             {}
func (c neverCloses) ErrShortCircuit(ctx context.Context, now time.Time)                       {}
func (c neverCloses) Opened(ctx context.Context, now time.Time)                                {}
func (c neverCloses) Closed(ctx context.Context, now time.Time)                                {}
func (c neverCloses) ShouldClose(ctx context.Context, now time.Time) bool {
	return false
}
