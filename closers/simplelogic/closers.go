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
func (c *ConsecutiveErrOpener) Closed(_ context.Context, _ time.Time) {
	c.consecutiveCount.Set(0)
}

// Prevent always returns false
func (c *ConsecutiveErrOpener) Prevent(_ context.Context, _ time.Time) bool {
	return false
}

// Success resets the consecutive error count
func (c *ConsecutiveErrOpener) Success(_ context.Context, _ time.Time, _ time.Duration) {
	c.consecutiveCount.Set(0)
}

// ErrBadRequest is ignored
func (c *ConsecutiveErrOpener) ErrBadRequest(_ context.Context, _ time.Time, _ time.Duration) {
}

// ErrInterrupt is ignored
func (c *ConsecutiveErrOpener) ErrInterrupt(_ context.Context, _ time.Time, _ time.Duration) {
}

// ErrConcurrencyLimitReject is ignored
func (c *ConsecutiveErrOpener) ErrConcurrencyLimitReject(_ context.Context, _ time.Time) {}

// ErrShortCircuit is ignored
func (c *ConsecutiveErrOpener) ErrShortCircuit(_ context.Context, _ time.Time) {}

// ErrFailure increments the consecutive error counter
func (c *ConsecutiveErrOpener) ErrFailure(_ context.Context, _ time.Time, _ time.Duration) {
	c.consecutiveCount.Add(1)
}

// ErrTimeout increments the consecutive error counter
func (c *ConsecutiveErrOpener) ErrTimeout(_ context.Context, _ time.Time, _ time.Duration) {
	c.consecutiveCount.Add(1)
}

// Opened resets the error counter
func (c *ConsecutiveErrOpener) Opened(_ context.Context, _ time.Time) {
	c.consecutiveCount.Set(0)
}

// ShouldOpen returns true if enough consecutive errors have returned
func (c *ConsecutiveErrOpener) ShouldOpen(_ context.Context, _ time.Time) bool {
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
