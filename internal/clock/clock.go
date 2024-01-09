package clock

import (
	"sync"
	"time"
)

// MockClock allows mocking time for testing
type MockClock struct {
	currentTime time.Time
	callbacks   []timedCallbacks
	mu          sync.Mutex
}

type timedCallbacks struct {
	when time.Time
	f    func()
}

// Set the current time
func (m *MockClock) Set(t time.Time) time.Time {
	// Note: do this after the lock is released
	defer m.triggerCallbacks()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.currentTime = t
	return m.currentTime
}

// Add some time, triggering sleeping callbacks
func (m *MockClock) Add(d time.Duration) time.Time {
	return m.Set(m.Now().Add(d))
}

func (m *MockClock) triggerCallbacks() {
	var newArray []timedCallbacks
	var toCall []timedCallbacks
	m.mu.Lock()
	for _, c := range m.callbacks {
		if m.currentTime.Before(c.when) {
			newArray = append(newArray, c)
		} else {
			toCall = append(toCall, c)
		}
	}
	m.callbacks = newArray
	m.mu.Unlock()
	for _, cb := range toCall {
		cb.f()
	}
}

// Now simulates time.Now()
func (m *MockClock) Now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.currentTime
}

// AfterFunc simulates time.AfterFunc
func (m *MockClock) AfterFunc(d time.Duration, f func()) *time.Timer {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d == 0 {
		f()
		return nil
	}
	m.callbacks = append(m.callbacks, timedCallbacks{when: m.currentTime.Add(d), f: f})
	// Do not use what is returned ...
	return nil
}

// AfterFunc simulates time.After
func (m *MockClock) After(d time.Duration) <-chan time.Time {
	c := make(chan time.Time, 1)
	m.AfterFunc(d, func() {
		c <- m.Now()
	})
	return c
}

// TickUntil will tick the mock clock until shouldStop returns false.  Real sleep should be very small
func TickUntil(m *MockClock, shouldStop func() bool, realSleep time.Duration, mockIncr time.Duration) {
	for !shouldStop() {
		time.Sleep(realSleep)
		m.Add(mockIncr)
	}
}
