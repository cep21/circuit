package fastmath

import (
	"sync"
	"time"
)

// TimedCheck lets X events happen every sleepDuration units of time
type TimedCheck struct {
	sleepDuration     AtomicInt64
	eventCountToAllow AtomicInt64

	isFastFail        AtomicBoolean
	isFailFastVersion AtomicInt64

	TimeAfterFunc              func(time.Duration, func()) *time.Timer
	nextOpenTime               time.Time
	currentlyAllowedEventCount int64
	lastSetTimer               *time.Timer
	mu                         sync.RWMutex
}

// SetSleepDuration modifies how long time timed check will sleep.  It will not change
// alredy sleeping checks, but will change during the next check.
func (c *TimedCheck) SetSleepDuration(newDuration time.Duration) {
	c.sleepDuration.Set(newDuration.Nanoseconds())
}

func (c *TimedCheck) afterFunc(d time.Duration, f func()) *time.Timer {
	if c.TimeAfterFunc == nil {
		return time.AfterFunc(d, f)
	}
	return c.TimeAfterFunc(d, f)
}

// SetEventCountToAllow configures how many times Check() can return true before moving time
// to the next interval
func (c *TimedCheck) SetEventCountToAllow(newCount int64) {
	c.eventCountToAllow.Set(newCount)
}

// SleepStart resets the checker to trigger after now + sleepDuration
func (c *TimedCheck) SleepStart(now time.Time) {
	c.mu.Lock()
	if c.lastSetTimer != nil {
		c.lastSetTimer.Stop()
		c.lastSetTimer = nil
	}
	c.nextOpenTime = now.Add(c.sleepDuration.Duration())
	c.currentlyAllowedEventCount = 0
	c.isFastFail.Set(true)
	currentVersion := c.isFailFastVersion.Add(1)
	c.lastSetTimer = c.afterFunc(c.sleepDuration.Duration(), func() {
		// If sleep start is called again, don't reset from an old version
		if currentVersion == c.isFailFastVersion.Get() {
			c.isFastFail.Set(false)
		}
	})
	c.mu.Unlock()
}

// Check returns true if a check is allowed at this time
func (c *TimedCheck) Check(now time.Time) bool {
	if c.isFastFail.Get() {
		return false
	}
	c.mu.RLock()
	// Common condition fast check
	if c.nextOpenTime.After(now) {
		c.mu.RUnlock()
		return false
	}
	c.mu.RUnlock()

	c.mu.Lock()
	if !c.nextOpenTime.After(now) {
		c.currentlyAllowedEventCount++
		if c.currentlyAllowedEventCount >= c.eventCountToAllow.Get() {
			c.currentlyAllowedEventCount = 0
			c.nextOpenTime = now.Add(c.sleepDuration.Duration())
		}
		c.mu.Unlock()
		return true
	}
	c.mu.Unlock()
	return false
}
