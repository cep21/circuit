package simplelogic

import (
	"time"

	"github.com/cep21/hystrix"
	"github.com/cep21/hystrix/internal/fastmath"
)

type ConsecutiveErrOpener struct {
	consecutiveCount fastmath.AtomicInt64
	closeThreshold   fastmath.AtomicInt64
}

func ConsecutiveErrOpenerFactory(config ConfigConsecutiveErrOpener) func() hystrix.ClosedToOpen {
	return func() hystrix.ClosedToOpen {
		ret := &ConsecutiveErrOpener{}
		config.Merge(defaultConfigConsecutiveErrOpener)
		ret.SetConfigThreadSafe(config)
		return ret
	}
}

type ConfigConsecutiveErrOpener struct {
	ErrorThreshold int64
}

func (c *ConfigConsecutiveErrOpener) Merge(other ConfigConsecutiveErrOpener) {
	if c.ErrorThreshold == 0 {
		c.ErrorThreshold = other.ErrorThreshold
	}
}

var defaultConfigConsecutiveErrOpener = ConfigConsecutiveErrOpener{
	ErrorThreshold: 10,
}

func (c *ConsecutiveErrOpener) Closed(now time.Time) {
	c.consecutiveCount.Set(0)
}
func (c *ConsecutiveErrOpener) Prevent(now time.Time) bool {
	return false
}
func (c *ConsecutiveErrOpener) SuccessfulAttempt(now time.Time, duration time.Duration) {
	c.consecutiveCount.Set(0)
}
func (c *ConsecutiveErrOpener) BackedOutAttempt(now time.Time) {}
func (c *ConsecutiveErrOpener) ErrorAttempt(now time.Time) {
	c.consecutiveCount.Add(1)
}
func (c *ConsecutiveErrOpener) AttemptToOpen(now time.Time) bool {
	return c.consecutiveCount.Get() >= c.closeThreshold.Get()
}

func (c *ConsecutiveErrOpener) SetConfigThreadSafe(props ConfigConsecutiveErrOpener) {
	c.closeThreshold.Set(props.ErrorThreshold)
}

func (c *ConsecutiveErrOpener) SetConfigNotThreadSafe(props ConfigConsecutiveErrOpener) {
	c.SetConfigThreadSafe(props)
}

var _ hystrix.ClosedToOpen = &ConsecutiveErrOpener{}
