package testhelp

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

// BehaviorCheck tracks Run commands to help you test their logic
type BehaviorCheck struct {
	TotalRuns          int64
	totalErrors        int64
	LongestRunDuration time.Duration
	MostConcurrent     int64
	currentConcurrent  int64

	mu      sync.Mutex
	RunFunc func(ctx context.Context) error
}

// MustTesting errors if err != nil
func MustTesting(t *testing.T, err error) {
	if err != nil {
		t.Errorf("unexpected error: %s", err)
	}
}

// MustNotTesting errors if err == nil
func MustNotTesting(t *testing.T, err error) {
	if err == nil {
		t.Errorf("Saw nil, expected an error")
	}
}

// Run is a runFunc.  Use this as the runFunc for your circuit
func (b *BehaviorCheck) Run(ctx context.Context) (err error) {
	start := time.Now()
	defer func() {
		end := time.Now()
		thisRun := end.Sub(start)

		b.mu.Lock()
		defer b.mu.Unlock()

		if err != nil {
			b.totalErrors++
		}
		if b.LongestRunDuration < thisRun {
			b.LongestRunDuration = thisRun
		}
		b.currentConcurrent--
	}()
	b.mu.Lock()
	b.TotalRuns++
	b.currentConcurrent++
	if b.currentConcurrent > b.MostConcurrent {
		b.MostConcurrent = b.currentConcurrent
	}
	b.mu.Unlock()
	return b.RunFunc(ctx)
}

// SleepsForX waits for a duration, or until the passed in context fails
func SleepsForX(d time.Duration) func(context.Context) error {
	return func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
			return nil
		}
	}
}

// AlwaysPassesFallback is a fallback circuit that always passes
func AlwaysPassesFallback(_ context.Context, _ error) error {
	return nil
}

// AlwaysFailsFallback is a fallback circuit that always fails
func AlwaysFailsFallback(_ context.Context, err error) error {
	return fmt.Errorf("failed: %s", err)
}

var errFailure = errors.New("alwaysFails failure")

// AlwaysFails is a runFunc that always fails
func AlwaysFails(_ context.Context) error {
	return errFailure
}

// AlwaysPasses is a runFunc that always passes
func AlwaysPasses(_ context.Context) error {
	return nil
}

// DoTillTime concurrently calls f() in a forever loop until endTime
func DoTillTime(endTime time.Time, wg *sync.WaitGroup, f func()) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		for time.Now().Before(endTime) {
			f()
			// Don't need to sleep.  Just busy loop.  But let another thread take over if it wants (to get some concurrency)
			runtime.Gosched()
		}
	}()
}
