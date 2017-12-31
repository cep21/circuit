package hystrix

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cep21/hystrix/internal/fastmath"
)

func alwaysPasses(_ context.Context) error {
	return nil
}

type behaviorCheck struct {
	totalRuns          int64
	totalErrors        int64
	longestRunDuration time.Duration
	mostConcurrent     int64
	currentConcurrent  int64

	mu      sync.Mutex
	runFunc func(ctx context.Context) error
}

func (b *behaviorCheck) run(ctx context.Context) (err error) {
	start := time.Now()
	defer func() {
		end := time.Now()
		thisRun := end.Sub(start)

		b.mu.Lock()
		defer b.mu.Unlock()

		if err != nil {
			b.totalErrors++
		}
		if b.longestRunDuration < thisRun {
			b.longestRunDuration = thisRun
		}
		b.currentConcurrent--
	}()
	b.mu.Lock()
	b.totalRuns++
	b.currentConcurrent++
	if b.currentConcurrent > b.mostConcurrent {
		b.mostConcurrent = b.currentConcurrent
	}
	b.mu.Unlock()
	return b.runFunc(ctx)
}

func sleepsForX(d time.Duration) func(context.Context) error {
	return func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
			return nil
		}
	}
}

func alwaysPassesFallback(_ context.Context, _ error) error {
	return nil
}

func alwaysFailsFallback(_ context.Context, err error) error {
	return fmt.Errorf("failed: %s", err)
}

var errFailure = errors.New("alwaysFails failure")

func alwaysFails(_ context.Context) error {
	return errFailure
}

func TestHappyCircuit(t *testing.T) {
	c := NewCircuitFromConfig("TestHappyCircuit", CommandProperties{})
	// Should work 100 times in a row
	for i := 0; i < 100; i++ {
		err := c.Execute(context.Background(), alwaysPasses, func(_ context.Context, _ error) error {
			panic("should never be called")
		})
		if err != nil {
			t.Error("saw error from circuit that always passes")
		}
	}
	if c.IsOpen() {
		t.Error("happy circuits should not open")
	}
}

func TestBadRequest(t *testing.T) {
	c := NewCircuitFromConfig("TestBadRequest", CommandProperties{})
	// Should work 100 times in a row
	for i := 0; i < 100; i++ {
		err := c.Execute(context.Background(), func(_ context.Context) error {
			return SimpleBadRequest{
				errors.New("this request is bad"),
			}
		}, func(_ context.Context, _ error) error {
			panic("fallbacks don't get called on bad requests")
		})
		if err == nil {
			t.Error("I really expected an error here!")
		}
	}
	if c.IsOpen() {
		t.Error("bad requests should never break")
	}
}

func TestManyConcurrent(t *testing.T) {
	concurrency := 20
	c := NewCircuitFromConfig("TestManyConcurrent", CommandProperties{
		Execution: ExecutionConfig{
			MaxConcurrentRequests: int64(concurrency),
		},
	})
	wg := sync.WaitGroup{}
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := c.Execute(context.Background(), alwaysPasses, nil)
			if err != nil {
				t.Errorf("saw error from circuit that always passes: %s", err)
			}
		}()
	}
	wg.Wait()
}

func TestExecuteBlocks(t *testing.T) {
	c := NewCircuitFromConfig("TestGoFunction", CommandProperties{
		Execution: ExecutionConfig{
			Timeout: time.Nanosecond,
		},
	})
	ctx := context.Background()
	var startTime time.Time
	err := c.Execute(ctx, func(_ context.Context) error {
		startTime = time.Now()
		time.Sleep(time.Millisecond * 25)
		return nil
	}, nil)
	if err != nil {
		t.Errorf("Did not expect any errors from function that finally finished: %s", err)
	}
	if time.Since(startTime) < time.Millisecond*24 {
		t.Errorf("I expected Execute to block, but it did not")
	}
}

func TestDoForwardsPanics(t *testing.T) {
	c := NewCircuitFromConfig("TestGoFunction", CommandProperties{
		Execution: ExecutionConfig{
			Timeout: time.Millisecond * 1,
		},
	})
	ctx := context.Background()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("should recover")
		}
	}()
	c.Execute(ctx, func(_ context.Context) error {
		if true {
			panic(1)
		}
		return nil
	}, nil)
	t.Fatal("Should never get this far")
}

func TestCircuit_Go_ForwardsPanic(t *testing.T) {
	c := NewCircuitFromConfig("TestGoFunction", CommandProperties{
		Execution: ExecutionConfig{
			Timeout: time.Millisecond * 2,
		},
	})
	ctx := context.Background()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("should recover")
		}
	}()
	var x []int
	c.Go(ctx, func(ctx2 context.Context) error {
		x[0] = 0 // will panic
		return nil
	}, nil)
	t.Fatal("Should never get this far")
}

func TestCircuit_Go_CanEnd(t *testing.T) {
	c := NewCircuitFromConfig("TestGoFunction", CommandProperties{
		Execution: ExecutionConfig{
			Timeout: time.Millisecond * 2,
		},
	})
	ctx := context.Background()
	startTime := time.Now()
	err := c.Go(ctx, sleepsForX(time.Hour), nil)
	if err == nil {
		t.Errorf("expected a timeout error")
	}
	if time.Since(startTime) > time.Second*10 {
		t.Errorf("Took too long to run %s", time.Since(startTime))
	}
}

func TestThrottled(t *testing.T) {
	c := NewCircuitFromConfig("TestThrottled", CommandProperties{
		Execution: ExecutionConfig{
			MaxConcurrentRequests: 2,
		},
	})
	bc := behaviorCheck{
		runFunc: sleepsForX(time.Millisecond),
	}
	wg := sync.WaitGroup{}
	errCount := 0
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := c.Execute(context.Background(), bc.run, nil)
			if err != nil {
				errCount++
			}
		}()
	}
	wg.Wait()
	if bc.mostConcurrent != 2 {
		t.Errorf("Concurrent count not correct: %d", bc.mostConcurrent)
	}
	if errCount != 1 {
		t.Errorf("did not see error return count: %d", errCount)
	}
}

func TestTimeout(t *testing.T) {
	c := NewCircuitFromConfig("TestThrottled", CommandProperties{
		Execution: ExecutionConfig{
			Timeout: time.Millisecond,
		},
	})
	bc := behaviorCheck{
		runFunc: sleepsForX(time.Millisecond * 35),
	}
	err := c.Execute(context.Background(), bc.run, nil)
	if err == nil {
		t.Log("expected an error, got none")
	}
	if bc.longestRunDuration >= time.Millisecond*20 {
		t.Log("A cancel didn't happen fast enough")
	}
}

func TestFailingCircuit(t *testing.T) {
	c := NewCircuitFromConfig("TestFailingCircuit", CommandProperties{})
	err := c.Execute(context.Background(), alwaysFails, nil)
	if err == nil || err.Error() != "alwaysFails failure" {
		t.Error("saw no error from circuit that always fails")
	}
}

func TestFallbackCircuit(t *testing.T) {
	c := NewCircuitFromConfig("TestFallbackCircuit", CommandProperties{})
	// Fallback circuit should consistently fail
	for i := 0; i < 100; i++ {
		err := c.Execute(context.Background(), alwaysFails, alwaysPassesFallback)
		if err != nil {
			t.Error("saw error from circuit that has happy fallback", err)
		}
	}

	// By default, after 100 failures, we should be open
	if !c.IsOpen() {
		t.Error("I expected to be open after so many failures")
	}
}

func TestCircuitIgnoreContextFailures(t *testing.T) {
	c := NewCircuitFromConfig("TestFailingCircuit", CommandProperties{
		Execution: ExecutionConfig{
			Timeout: time.Hour,
		},
	})
	for i := 0; i < 100; i++ {
		rootCtx, cancel := context.WithTimeout(context.Background(), time.Millisecond*3)
		err := c.Execute(rootCtx, sleepsForX(time.Second), nil)
		if err == nil {
			t.Error("saw no error from circuit that should end in an error")
		}
		cancel()
	}
	if c.IsOpen() {
		t.Error("Parent context cacelations should not close the circuit by default")
	}
}

func TestFallbackCircuitConcurrency(t *testing.T) {
	c := NewCircuitFromConfig("TestFallbackCircuitConcurrency", CommandProperties{
		Fallback: FallbackConfig{
			MaxConcurrentRequests: 2,
		},
	})
	wg := sync.WaitGroup{}
	workingCircuitCount := int64(0)
	var fallbackExecuted fastmath.AtomicInt64
	var totalExecuted fastmath.AtomicInt64
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			totalExecuted.Add(1)
			defer wg.Done()
			err := c.Execute(context.Background(), alwaysFails, func(ctx context.Context, err error) error {
				fallbackExecuted.Add(1)
				return sleepsForX(time.Millisecond * 500)(ctx)
			})
			if err == nil {
				atomic.AddInt64(&workingCircuitCount, 1)
			}
		}()
	}
	wg.Wait()
	if totalExecuted.Get() == fallbackExecuted.Get() {
		t.Error("At least one fallback call should never happen due to concurrency")
	}
	if workingCircuitCount != 2 {
		t.Error("Should see 2 working examples")
	}
}

func TestCircuitCloses(t *testing.T) {
	c := NewCircuitFromConfig("TestCircuitCloses", CommandProperties{
		CircuitBreaker: CircuitBreakerConfig{
			// A single failed request should be enough to close the circuit
			RequestVolumeThreshold: 1,
		},
	})
	if c.IsOpen() {
		t.Fatal("Circuit should not start out open")
	}
	err := c.Execute(context.Background(), alwaysFails, nil)
	if err == nil {
		t.Fatal("Circuit should have failed if run fails")
	}
	if !c.IsOpen() {
		t.Fatal("Circuit should be open after having failed once")
	}
	err = c.Execute(context.Background(), alwaysPasses, nil)
	if err == nil {
		t.Fatal("Circuit should be open")
	}
}

func TestCircuitAttemptsToReopen(t *testing.T) {
	c := NewCircuitFromConfig("TestCircuitAttemptsToReopen", CommandProperties{
		CircuitBreaker: CircuitBreakerConfig{
			// A single failed request should be enough to close the circuit
			RequestVolumeThreshold: 1,
			SleepWindow:            time.Millisecond * 1,
		},
	})
	if c.IsOpen() {
		t.Fatal("Circuit should not start out open")
	}
	err := c.Execute(context.Background(), alwaysFails, nil)
	if err == nil {
		t.Fatal("Circuit should have failed if run fails")
	}
	if !c.IsOpen() {
		t.Fatal("Circuit should be open after having failed once")
	}
	err = c.Execute(context.Background(), alwaysPasses, nil)
	if err == nil {
		t.Fatal("Circuit should be open")
	}

	time.Sleep(time.Millisecond * 3)
	err = c.Execute(context.Background(), alwaysPasses, nil)
	if err != nil {
		t.Fatal("Circuit should try to reopen")
	}
}

func TestCircuitAttemptsToReopenOnlyOnce(t *testing.T) {
	c := NewCircuitFromConfig("TestCircuitAttemptsToReopen", CommandProperties{
		CircuitBreaker: CircuitBreakerConfig{
			// A single failed request should be enough to close the circuit
			RequestVolumeThreshold: 1,
			SleepWindow:            time.Millisecond * 1,
		},
	})
	if c.IsOpen() {
		t.Fatal("Circuit should not start out open")
	}
	err := c.Execute(context.Background(), alwaysFails, nil)
	if err == nil {
		t.Fatal("Circuit should have failed if run fails")
	}
	if !c.IsOpen() {
		t.Fatal("Circuit should be open after having failed once")
	}
	err = c.Execute(context.Background(), alwaysPasses, nil)
	if err == nil {
		t.Fatal("Circuit should be open")
	}

	time.Sleep(time.Millisecond * 3)
	err = c.Execute(context.Background(), alwaysFails, nil)
	if err == nil {
		t.Fatal("Circuit should try to reopen, but fail")
	}
	err = c.Execute(context.Background(), alwaysPasses, nil)
	if err == nil {
		t.Fatal("Circuit should only try to reopen once")
	}
}

func TestLargeSleepWindow(t *testing.T) {
	c := NewCircuitFromConfig("TestLargeSleepWindow", CommandProperties{
		CircuitBreaker: CircuitBreakerConfig{
			// Once this fails, it should never reopen
			SleepWindow:              time.Hour,
			ErrorThresholdPercentage: 1,
			RequestVolumeThreshold:   1,
		},
	})

	err := c.Execute(context.Background(), alwaysFails, alwaysPassesFallback)
	if err != nil {
		t.Errorf("I expect this to not fail since it has a fallback")
	}

	if !c.IsOpen() {
		t.Fatalf("I expect the circuit to now be open, since the previous failure happened")
	}

	wg := sync.WaitGroup{}
	// Create many goroutines that never fail
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20*2; i++ {
				err := c.Execute(context.Background(), sleepsForX(time.Millisecond/10), nil)
				if err == nil {
					t.Errorf("I expect this to always fail, now that it's in the failure state")
				}
				time.Sleep(time.Millisecond / 10)
			}
		}()
	}
	wg.Wait()
}

func TestFailingFallbackCircuit(t *testing.T) {
	c := NewCircuitFromConfig("TestFailingCircuit", CommandProperties{})
	err := c.Execute(context.Background(), alwaysFails, alwaysFailsFallback)
	if err == nil {
		t.Error("expected error back")
		t.FailNow()
	}
	if err.Error() != "failed: alwaysFails failure" {
		t.Error("unexpected error string", err)
	}
}

//func TestSLO(t *testing.T) {
//	c := NewCircuitFromConfig("TestFailingCircuit", CommandProperties{
//		GoSpecific: GoSpecificConfig{
//			ResponseTimeSLO: time.Millisecond,
//		},
//	})
//	err := c.Execute(context.Background(), sleepsForX(time.Millisecond*5), nil)
//	if err != nil {
//		t.Error("This request should not fail")
//	}
//	if c.errorsCount.TotalSum() != 0 {
//		t.Error("the request should not be an error")
//	}
//	if c.responseTimeSLO.MeetsSLOCount.Get() != 0 {
//		t.Error("the request should not be healthy")
//	}
//	if c.responseTimeSLO.FailsSLOCount.Get() != 1 {
//		t.Error("the request should be failed")
//	}
//}

func TestFallbackAfterTimeout(t *testing.T) {
	c := NewCircuitFromConfig("TestThrottled", CommandProperties{
		Execution: ExecutionConfig{
			Timeout: time.Millisecond,
		},
	})
	bc := behaviorCheck{
		runFunc: sleepsForX(time.Millisecond * 35),
	}
	err := c.Execute(context.Background(), bc.run, func(ctx context.Context, err error) error {
		if ctx.Err() != nil {
			return errors.New("the passed in context should not be finished")
		}
		return nil
	})
	if err != nil {
		t.Log("Should be no error since the fallback didn't error")
	}
	if bc.longestRunDuration >= time.Millisecond*20 {
		t.Log("A cancel didn't happen fast enough")
	}
}

func TestSleepDurationWorks(t *testing.T) {
	concurrentThreads := 25
	c := NewCircuitFromConfig("TestFailureBehavior", CommandProperties{
		CircuitBreaker: CircuitBreakerConfig{
			// This should allow a new request every 50 milliseconds
			// This value needs to be large enough to let time.AfterFunc trigger
			SleepWindow: time.Millisecond * 40,

			// The first failure should open the circuit
			ErrorThresholdPercentage: 1,
			RequestVolumeThreshold:   1,
		},
		Execution: ExecutionConfig{
			MaxConcurrentRequests: int64(concurrentThreads),
		},
		Fallback: FallbackConfig{
			MaxConcurrentRequests: int64(concurrentThreads),
		},
	})

	// Once failing, c should never see more than one request every 40 ms
	// If I wait 110 ms, I should see exactly 2 requests (the one at 40 and at 80)
	doNotPassTime := time.Now().Add(time.Millisecond * 110)
	err := c.Execute(context.Background(), alwaysFails, alwaysPassesFallback)
	if err != nil {
		t.Errorf("I expect this to not fail since it has a fallback")
	}

	bc := behaviorCheck{
		runFunc: alwaysFails,
	}

	wg := sync.WaitGroup{}
	for ct := 0; ct < concurrentThreads; ct++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if time.Now().After(doNotPassTime) {
					break
				}
				err := c.Execute(context.Background(), bc.run, alwaysPassesFallback)
				if err != nil {
					t.Errorf("The fallback was fine.  It should not fail (but should stay open): %s", err)
				}
				// Don't need to sleep.  Just busy loop.  But let another thread take over if it wants (to get some concurrency)
				runtime.Gosched()
			}
		}()
	}
	wg.Wait()
	if bc.totalRuns != 2 {
		t.Errorf("Circuit should pass thru exactly 2 requests: %d", bc.totalRuns)
	}
}

func doTillTime(endTime time.Time, wg *sync.WaitGroup, f func()) {
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

// Just test to make sure the -race detector doesn't find anything with a public function
func TestVariousRaceConditions(t *testing.T) {
	concurrentThreads := 5
	c := NewCircuitFromConfig("TestVariousRaceConditions", CommandProperties{
		CircuitBreaker: CircuitBreakerConfig{
			// This should allow a new request every 10 milliseconds
			SleepWindow: time.Millisecond * 5,

			// The first failure should open the circuit
			ErrorThresholdPercentage: 1,
			RequestVolumeThreshold:   1,
		},
		Execution: ExecutionConfig{
			MaxConcurrentRequests: int64(concurrentThreads),
		},
		Fallback: FallbackConfig{
			MaxConcurrentRequests: int64(concurrentThreads),
		},
	})

	doNotPassTime := time.Now().Add(time.Millisecond * 20)

	wg := sync.WaitGroup{}
	for i := 0; i < concurrentThreads; i++ {
		doTillTime(doNotPassTime, &wg, func() {
			c.Var()
		})
		doTillTime(doNotPassTime, &wg, func() {
			c.IsOpen()
		})
		doTillTime(doNotPassTime, &wg, func() {
			c.CloseCircuit()
		})
		doTillTime(doNotPassTime, &wg, func() {
			c.OpenCircuit()
		})
		doTillTime(doNotPassTime, &wg, func() {
			c.Name()
		})
		doTillTime(doNotPassTime, &wg, func() {
			c.Execute(context.Background(), alwaysPasses, nil)
		})
		doTillTime(doNotPassTime, &wg, func() {
			c.Execute(context.Background(), alwaysFails, nil)
		})
		doTillTime(doNotPassTime, &wg, func() {
			c.Execute(context.Background(), alwaysFails, alwaysPassesFallback)
		})
		doTillTime(doNotPassTime, &wg, func() {
			c.Execute(context.Background(), alwaysFails, alwaysFailsFallback)
		})
	}
	wg.Wait()
}

func TestCircuitRecovers(t *testing.T) {
	concurrentThreads := 25
	c := NewCircuitFromConfig("TestCircuitRecovers", CommandProperties{
		CircuitBreaker: CircuitBreakerConfig{
			// This should allow a new request every 10 milliseconds
			SleepWindow: time.Millisecond * 5,

			// The first failure should open the circuit
			ErrorThresholdPercentage: 1,
			RequestVolumeThreshold:   1,
		},
		Execution: ExecutionConfig{
			MaxConcurrentRequests: int64(concurrentThreads),
		},
		Fallback: FallbackConfig{
			MaxConcurrentRequests: int64(concurrentThreads),
		},
	})

	// This is when the circuit starts working again
	startWorkingTime := time.Now().Add(time.Millisecond * 11)
	// This is the latest that the circuit should keep failing requests
	circuitOkTime := startWorkingTime.Add(c.Config().CircuitBreaker.SleepWindow).Add(time.Millisecond * 200)

	// Give some buffer so time.AfterFunc can get called
	doNotPassTime := time.Now().Add(time.Millisecond * 250)
	err := c.Execute(context.Background(), alwaysFails, alwaysPassesFallback)
	if err != nil {
		t.Errorf("I expect this to not fail since it has a fallback")
	}

	failure := errors.New("a failure")
	bc := behaviorCheck{
		runFunc: func(_ context.Context) error {
			if time.Now().After(startWorkingTime) {
				return nil
			}
			return failure
		},
	}

	wg := sync.WaitGroup{}
	for ct := 0; ct < concurrentThreads; ct++ {
		hasHealed := false
		doTillTime(doNotPassTime, &wg, func() {
			isCircuitOk := time.Now().After(circuitOkTime)
			err := c.Execute(context.Background(), bc.run, nil)
			if err != nil {
				if isCircuitOk {
					t.Fatalf("Should not get an error after this time: The circuit should be ok: %s", err)
				}
				if hasHealed {
					t.Fatalf("Should not get an error after the circuit healed itself")
				}
			}
			if err == nil {
				if time.Now().Before(startWorkingTime) {
					t.Fatalf("The circuit should not work before I correct the service")
				}
				hasHealed = true
			}
		})
	}
	wg.Wait()
}
