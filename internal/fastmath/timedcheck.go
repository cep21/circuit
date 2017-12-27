package fastmath

import (
	"sync"
	"time"
)

// TimedCheck lets X events happen every sleepDuration units of time
type TimedCheck struct {
	sleepDuration     AtomicInt64
	eventCountToAllow AtomicInt64

	nextOpenTime               time.Time
	currentlyAllowedEventCount int64
	mu                         sync.RWMutex
}

// SetSleepDuration modifies how long time timed check will sleep.  It will not change
// alredy sleeping checks, but will change during the next check.
func (c *TimedCheck) SetSleepDuration(newDuration time.Duration) {
	c.sleepDuration.Set(newDuration.Nanoseconds())
}

// SetEventCountToAllow configures how many times Check() can return true before moving time
// to the next interval
func (c *TimedCheck) SetEventCountToAllow(newCount int64) {
	c.eventCountToAllow.Set(newCount)
}

// SleepStart resets the checker to trigger after now + sleepDuration
func (c *TimedCheck) SleepStart(now time.Time) {
	c.mu.Lock()
	c.nextOpenTime = now.Add(c.sleepDuration.Duration())
	c.currentlyAllowedEventCount = 0
	c.mu.Unlock()
}

// Check returns true if a check is allowed at this time
func (c *TimedCheck) Check(now time.Time) bool {
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
