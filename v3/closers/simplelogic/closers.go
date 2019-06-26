package simplelogic

import (
	"time"

	"github.com/cep21/circuit/v3"
	"github.com/cep21/circuit/v3/faststats"
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
func (c *ConsecutiveErrOpener) Closed(_ time.Time) {
	c.consecutiveCount.Set(0)
}

// Prevent always returns false
func (c *ConsecutiveErrOpener) Prevent(now time.Time) bool {
	return false
}

// Success resets the consecutive error count
func (c *ConsecutiveErrOpener) Success(now time.Time, duration time.Duration) {
	c.consecutiveCount.Set(0)
}

// ErrBadRequest is ignored
func (c *ConsecutiveErrOpener) ErrBadRequest(now time.Time, duration time.Duration) {}

// ErrInterrupt is ignored
func (c *ConsecutiveErrOpener) ErrInterrupt(now time.Time, duration time.Duration) {}

// ErrConcurrencyLimitReject is ignored
func (c *ConsecutiveErrOpener) ErrConcurrencyLimitReject(now time.Time) {}

// ErrShortCircuit is ignored
func (c *ConsecutiveErrOpener) ErrShortCircuit(now time.Time) {}

// ErrFailure increments the consecutive error counter
func (c *ConsecutiveErrOpener) ErrFailure(now time.Time, duration time.Duration) {
	c.consecutiveCount.Add(1)
}

// ErrTimeout increments the consecutive error counter
func (c *ConsecutiveErrOpener) ErrTimeout(now time.Time, duration time.Duration) {
	c.consecutiveCount.Add(1)
}

// Opened resets the error counter
func (c *ConsecutiveErrOpener) Opened(now time.Time) {
	c.consecutiveCount.Set(0)
}

// ShouldOpen returns true if enough consecutive errors have returned
func (c *ConsecutiveErrOpener) ShouldOpen(now time.Time) bool {
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
