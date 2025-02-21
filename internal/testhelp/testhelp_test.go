package testhelp

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMustTesting(t *testing.T) {
	mockT := &testing.T{}
	// Should not cause an error
	MustTesting(mockT, nil)
	
	// Should cause an error
	MustTesting(mockT, errors.New("test error"))
	// Note: we can't easily check that the mock testing.T recorded an error
}

func TestMustNotTesting(t *testing.T) {
	mockT := &testing.T{}
	// Should cause an error
	MustNotTesting(mockT, nil)
	
	// Should not cause an error
	MustNotTesting(mockT, errors.New("test error"))
	// Note: we can't easily check that the mock testing.T recorded an error
}

func TestBehaviorCheck_Run(t *testing.T) {
	b := &BehaviorCheck{
		RunFunc: func(ctx context.Context) error {
			return nil
		},
	}
	
	// Test success case
	err := b.Run(context.Background())
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if b.TotalRuns != 1 {
		t.Errorf("Expected 1 total run, got %d", b.TotalRuns)
	}
	if b.totalErrors != 0 {
		t.Errorf("Expected 0 total errors, got %d", b.totalErrors)
	}
	if b.MostConcurrent != 1 {
		t.Errorf("Expected most concurrent to be 1, got %d", b.MostConcurrent)
	}
	if b.currentConcurrent != 0 {
		t.Errorf("Expected current concurrent to be 0, got %d", b.currentConcurrent)
	}
	
	// Test error case
	b.RunFunc = func(ctx context.Context) error {
		return errors.New("test error")
	}
	
	err = b.Run(context.Background())
	if err == nil {
		t.Error("Expected an error")
	}
	if b.TotalRuns != 2 {
		t.Errorf("Expected 2 total runs, got %d", b.TotalRuns)
	}
	if b.totalErrors != 1 {
		t.Errorf("Expected 1 total error, got %d", b.totalErrors)
	}
	
	// Test concurrency tracking
	wg := sync.WaitGroup{}
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.Run(context.Background())
		}()
	}
	wg.Wait()
	
	if b.TotalRuns != 7 {
		t.Errorf("Expected 7 total runs, got %d", b.TotalRuns)
	}
	if b.totalErrors != 6 {
		t.Errorf("Expected 6 total errors, got %d", b.totalErrors)
	}
	if b.MostConcurrent < 1 {
		t.Errorf("Expected most concurrent to be at least 1, got %d", b.MostConcurrent)
	}
}

func TestSleepsForX(t *testing.T) {
	// Test normal sleep
	start := time.Now()
	fn := SleepsForX(100 * time.Millisecond)
	err := fn(context.Background())
	elapsed := time.Since(start)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if elapsed < 100*time.Millisecond {
		t.Errorf("Expected sleep of at least 100ms, got %v", elapsed)
	}
	
	// Test context cancellation
	ctx, cancel := context.WithCancel(context.Background())
	start = time.Now()
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	fn = SleepsForX(10 * time.Second)
	err = fn(ctx)
	elapsed = time.Since(start)
	
	if err == nil {
		t.Error("Expected an error due to context cancellation")
	}
	if err != context.Canceled {
		t.Errorf("Expected context.Canceled error, got %v", err)
	}
	if elapsed >= 10*time.Second {
		t.Errorf("Expected earlier termination than 10s, got %v", elapsed)
	}
}

func TestAlwaysPassesFallback(t *testing.T) {
	err := AlwaysPassesFallback(context.Background(), errors.New("test error"))
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
}

func TestAlwaysFailsFallback(t *testing.T) {
	origErr := errors.New("original error")
	err := AlwaysFailsFallback(context.Background(), origErr)
	if err == nil {
		t.Error("Expected an error")
	}
	if err != nil {
		errorText := err.Error()
		if !strings.Contains(errorText, "original error") {
			t.Errorf("Expected error to contain 'original error', got: %v", err)
		}
	}
}


func TestAlwaysFails(t *testing.T) {
	err := AlwaysFails(context.Background())
	if err == nil {
		t.Error("Expected an error")
	}
	if err != errFailure {
		t.Errorf("Expected errFailure, got %v", err)
	}
}

func TestAlwaysPasses(t *testing.T) {
	err := AlwaysPasses(context.Background())
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
}

func TestDoTillTime(t *testing.T) {
	var counter int
	wg := &sync.WaitGroup{}
	
	endTime := time.Now().Add(500 * time.Millisecond)
	DoTillTime(endTime, wg, func() {
		counter++
	})
	
	wg.Wait()
	if counter <= 0 {
		t.Error("Function should have executed at least once")
	}
}