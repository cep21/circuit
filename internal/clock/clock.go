package clock

import (
	"sync"
	"time"
)

type MockClock struct {
	currentTime time.Time
	callbacks   []timedCallbacks
	mu          sync.Mutex
}

type timedCallbacks struct {
	when time.Time
	f    func()
}

func (m *MockClock) Set(t time.Time) time.Time {
	// Note: do this after the lock is released
	defer m.triggerCallbacks()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.currentTime = t
	return m.currentTime
}

func (m *MockClock) Add(d time.Duration) time.Time {
	return m.Set(m.Now().Add(d))
}

func (m *MockClock) triggerCallbacks() {
	m.mu.Lock()
	newArray := []timedCallbacks{}
	toCall := []timedCallbacks{}
	for _, c := range m.callbacks {
		if m.currentTime.After(c.when) {
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

func (m *MockClock) Now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.currentTime
}

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
