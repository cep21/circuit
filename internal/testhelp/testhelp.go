package testhelp

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"
)

type BehaviorCheck struct {
	TotalRuns          int64
	totalErrors        int64
	LongestRunDuration time.Duration
	MostConcurrent     int64
	currentConcurrent  int64

	mu      sync.Mutex
	RunFunc func(ctx context.Context) error
}

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

func AlwaysPassesFallback(_ context.Context, _ error) error {
	return nil
}

func AlwaysFailsFallback(_ context.Context, err error) error {
	return fmt.Errorf("failed: %s", err)
}

var errFailure = errors.New("alwaysFails failure")

func AlwaysFails(_ context.Context) error {
	return errFailure
}

func AlwaysPasses(_ context.Context) error {
	return nil
}

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
