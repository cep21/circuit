package faststats

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// TimedCheck lets X events happen every sleepDuration units of time.  For optimizations, it uses TimeAfterFunc to reset
// an internal atomic boolean for when events are allowed.  This timer could run a little bit behind real time since
// it depends on when the OS decides to trigger the timer.
type TimedCheck struct {
	sleepDuration     AtomicInt64
	eventCountToAllow AtomicInt64

	isFastFail        AtomicBoolean
	isFailFastVersion AtomicInt64

	TimeAfterFunc func(time.Duration, func()) *time.Timer

	// All 3 of these variables must be accessed with the RWMutex
	nextOpenTime               time.Time
	currentlyAllowedEventCount int64
	lastSetTimer               *time.Timer
	mu                         sync.RWMutex
}

var _ json.Marshaler = &TimedCheck{}
var _ json.Unmarshaler = &TimedCheck{}
var _ fmt.Stringer = &TimedCheck{}

// marshalStruct is used by JSON marshalling
type marshalStruct struct {
	SleepDuration              int64
	EventCountToAllow          int64
	NextOpenTime               time.Time
	CurrentlyAllowedEventCount int64
}

func (c *TimedCheck) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return fmt.Sprintf("TimedCheck(open=%s)", c.nextOpenTime)
}

// MarshalJSON writes the object as JSON.  It is thread safe.
func (c *TimedCheck) MarshalJSON() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return json.Marshal(marshalStruct{
		SleepDuration:              c.sleepDuration.Get(),
		EventCountToAllow:          c.eventCountToAllow.Get(),
		NextOpenTime:               c.nextOpenTime,
		CurrentlyAllowedEventCount: c.currentlyAllowedEventCount,
	})
}

// UnmarshalJSON changes the object from JSON.  It is *NOT* thread safe.
func (c *TimedCheck) UnmarshalJSON(b []byte) error {
	var into marshalStruct
	if err := json.Unmarshal(b, &into); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sleepDuration.Set(into.SleepDuration)
	c.eventCountToAllow.Set(into.EventCountToAllow)
	c.nextOpenTime = into.NextOpenTime
	c.currentlyAllowedEventCount = into.CurrentlyAllowedEventCount
	return nil
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
	c.resetOpenTimeWithLock(now)
	c.mu.Unlock()
}

func (c *TimedCheck) resetOpenTimeWithLock(now time.Time) {
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
	defer c.mu.Unlock()
	if c.nextOpenTime.After(now) {
		return false
	}
	c.currentlyAllowedEventCount++
	if c.currentlyAllowedEventCount >= c.eventCountToAllow.Get() {
		c.resetOpenTimeWithLock(now)
	}
	return true
}
