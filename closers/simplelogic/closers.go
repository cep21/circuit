package simplelogic

import (
	"context"
	"time"

	"github.com/cep21/circuit/v4"
	"github.com/cep21/circuit/v4/faststats"
)

// ConsecutiveErrOpener is simple closed->open logic that opens on consecutive error counts
type ConsecutiveErrOpener struct {
	consecutiveCount faststats.AtomicInt64
	closeThreshold   faststats.AtomicInt64
}

// ConsecutiveErrOpenerFactory constructs a new ConsecutiveErrOpener
func ConsecutiveErrOpenerFactory(config ConfigConsecutiveErrOpener) func() circuit.ClosedToOpen {
	return func() circuit.ClosedToOpen {
		ret := &ConsecutiveErrOpener{}
		config.Merge(defaultConfigConsecutiveErrOpener)
		ret.SetConfigThreadSafe(config)
		return ret
	}
}

// ConfigConsecutiveErrOpener configures a ConsecutiveErrOpener
type ConfigConsecutiveErrOpener struct {
	ErrorThreshold int64
}

// Merge this config with another
func (c *ConfigConsecutiveErrOpener) Merge(other ConfigConsecutiveErrOpener) {
	if c.ErrorThreshold == 0 {
		c.ErrorThreshold = other.ErrorThreshold
	}
}

var defaultConfigConsecutiveErrOpener = ConfigConsecutiveErrOpener{
	ErrorThreshold: 10,
}

// Closed resets the consecutive error count
func (c *ConsecutiveErrOpener) Closed(ctx context.Context, _ time.Time) {
	c.consecutiveCount.Set(0)
}

// Prevent always returns false
func (c *ConsecutiveErrOpener) Prevent(ctx context.Context, now time.Time) bool {
	return false
}

// Success resets the consecutive error count
func (c *ConsecutiveErrOpener) Success(ctx context.Context, now time.Time, duration time.Duration) {
	c.consecutiveCount.Set(0)
}

// ErrBadRequest is ignored
func (c *ConsecutiveErrOpener) ErrBadRequest(ctx context.Context, now time.Time, duration time.Duration) {
}

// ErrInterrupt is ignored
func (c *ConsecutiveErrOpener) ErrInterrupt(ctx context.Context, now time.Time, duration time.Duration) {
}

// ErrConcurrencyLimitReject is ignored
func (c *ConsecutiveErrOpener) ErrConcurrencyLimitReject(ctx context.Context, now time.Time) {}

// ErrShortCircuit is ignored
func (c *ConsecutiveErrOpener) ErrShortCircuit(ctx context.Context, now time.Time) {}

// ErrFailure increments the consecutive error counter
func (c *ConsecutiveErrOpener) ErrFailure(ctx context.Context, now time.Time, duration time.Duration) {
	c.consecutiveCount.Add(1)
}

// ErrTimeout increments the consecutive error counter
func (c *ConsecutiveErrOpener) ErrTimeout(ctx context.Context, now time.Time, duration time.Duration) {
	c.consecutiveCount.Add(1)
}

// Opened resets the error counter
func (c *ConsecutiveErrOpener) Opened(ctx context.Context, now time.Time) {
	c.consecutiveCount.Set(0)
}

// ShouldOpen returns true if enough consecutive errors have returned
func (c *ConsecutiveErrOpener) ShouldOpen(ctx context.Context, now time.Time) bool {
	return c.consecutiveCount.Get() >= c.closeThreshold.Get()
}

// SetConfigThreadSafe updates the error threshold
func (c *ConsecutiveErrOpener) SetConfigThreadSafe(props ConfigConsecutiveErrOpener) {
	c.closeThreshold.Set(props.ErrorThreshold)
}

// SetConfigNotThreadSafe updates the error threshold
func (c *ConsecutiveErrOpener) SetConfigNotThreadSafe(props ConfigConsecutiveErrOpener) {
	c.SetConfigThreadSafe(props)
}

var _ circuit.ClosedToOpen = &ConsecutiveErrOpener{}
