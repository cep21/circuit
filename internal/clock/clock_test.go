package clock

import (
	"sync"
	"testing"
	"time"
)

func TestMockClock_Set(t *testing.T) {
	m := &MockClock{}
	now := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)

	// Test setting the time
	result := m.Set(now)
	if !result.Equal(now) {
		t.Errorf("Expected time %v, got %v", now, result)
	}
	if !m.Now().Equal(now) {
		t.Errorf("Expected time %v, got %v", now, m.Now())
	}

	// Test setting time again
	later := now.Add(time.Hour)
	result = m.Set(later)
	if !result.Equal(later) {
		t.Errorf("Expected time %v, got %v", later, result)
	}
	if !m.Now().Equal(later) {
		t.Errorf("Expected time %v, got %v", later, m.Now())
	}
}

func TestMockClock_Add(t *testing.T) {
	m := &MockClock{}
	now := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	m.Set(now)

	// Test adding time
	result := m.Add(time.Hour)
	expected := now.Add(time.Hour)
	if !result.Equal(expected) {
		t.Errorf("Expected time %v, got %v", expected, result)
	}
	if !m.Now().Equal(expected) {
		t.Errorf("Expected time %v, got %v", expected, m.Now())
	}
}

func TestMockClock_AfterFunc(t *testing.T) {
	m := &MockClock{}
	now := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	m.Set(now)

	var callCount int
	var mu sync.Mutex

	incrementCallCount := func() {
		mu.Lock()
		defer mu.Unlock()
		callCount++
	}

	getCallCount := func() int {
		mu.Lock()
		defer mu.Unlock()
		return callCount
	}

	// Test AfterFunc with immediate execution
	m.AfterFunc(0, incrementCallCount)
	if count := getCallCount(); count != 1 {
		t.Errorf("Expected call count to be 1, got %d", count)
	}

	// Test AfterFunc with delayed execution
	m.AfterFunc(time.Hour, incrementCallCount)
	if count := getCallCount(); count != 1 {
		t.Errorf("Function should not be called before time advances, got count %d", count)
	}

	// Add half the time - callback shouldn't fire yet
	m.Add(30 * time.Minute)
	if count := getCallCount(); count != 1 {
		t.Errorf("Function should not be called before time reaches target, got count %d", count)
	}

	// Add remaining time - callback should fire
	m.Add(30 * time.Minute)
	if count := getCallCount(); count != 2 {
		t.Errorf("Function should be called after time reaches target, got count %d", count)
	}
}

func TestMockClock_After(t *testing.T) {
	m := &MockClock{}
	now := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	m.Set(now)

	// Test After channel
	ch := m.After(time.Hour)

	// Shouldn't be any value yet
	select {
	case <-ch:
		t.Fatal("Channel should not have a value yet")
	default:
		// This is correct
	}

	// Add time and check channel
	later := now.Add(time.Hour)
	m.Add(time.Hour)

	select {
	case receivedTime := <-ch:
		if !receivedTime.Equal(later) {
			t.Errorf("Expected received time %v, got %v", later, receivedTime)
		}
	default:
		t.Fatal("Channel should have a value after time advances")
	}
}

func TestMockClock_triggerCallbacks(t *testing.T) {
	m := &MockClock{}
	now := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	m.Set(now)

	var calls []int
	var mu sync.Mutex

	addCall := func(i int) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, i)
	}

	m.AfterFunc(time.Hour, func() { addCall(1) })
	m.AfterFunc(2*time.Hour, func() { addCall(2) })
	m.AfterFunc(3*time.Hour, func() { addCall(3) })

	// Add enough time to trigger first callback
	m.Add(65 * time.Minute) // Just past 1 hour

	mu.Lock()
	if len(calls) != 1 || calls[0] != 1 {
		t.Errorf("Expected calls to be [1], got %v", calls)
	}
	mu.Unlock()

	// Add enough time to trigger second callback
	m.Add(65 * time.Minute) // Now at 2h10m

	mu.Lock()
	if len(calls) != 2 || calls[0] != 1 || calls[1] != 2 {
		t.Errorf("Expected calls to be [1, 2], got %v", calls)
	}
	mu.Unlock()

	// Add enough time to trigger third callback
	m.Add(65 * time.Minute) // Now at 3h15m

	mu.Lock()
	if len(calls) != 3 || calls[0] != 1 || calls[1] != 2 || calls[2] != 3 {
		t.Errorf("Expected calls to be [1, 2, 3], got %v", calls)
	}
	mu.Unlock()
}

func TestTickUntil(t *testing.T) {
	m := &MockClock{}
	now := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	m.Set(now)

	var tickCount int
	done := make(chan struct{})

	// Run TickUntil in a goroutine
	go func() {
		defer close(done)
		TickUntil(m, func() bool {
			return tickCount >= 3
		}, time.Millisecond, time.Hour)
	}()

	// Create a function that increments tickCount when time advances
	var mu sync.Mutex
	incrementTickCount := func() {
		mu.Lock()
		defer mu.Unlock()
		tickCount++
	}

	// Set up callbacks at hourly intervals
	m.AfterFunc(time.Hour, incrementTickCount)
	m.AfterFunc(2*time.Hour, incrementTickCount)
	m.AfterFunc(3*time.Hour, incrementTickCount)

	// Wait for TickUntil to complete
	select {
	case <-done:
		// Success
	case <-time.After(time.Second):
		t.Fatal("TickUntil did not complete in time")
	}

	if tickCount != 3 {
		t.Errorf("Expected tick count to be 3, got %d", tickCount)
	}

	expected := now.Add(3 * time.Hour)
	if !m.Now().Equal(expected) {
		t.Errorf("Expected time to be %v, got %v", expected, m.Now())
	}
}

func TestMockClock_AfterFunc_ReturnsNonNil(t *testing.T) {
	m := &MockClock{}
	now := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	m.Set(now)

	timer := m.AfterFunc(time.Hour, func() {})
	if timer == nil {
		t.Fatal("AfterFunc should return a non-nil *time.Timer")
	}
	// Should not panic
	timer.Stop()
}
