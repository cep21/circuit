package hystrix

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cep21/hystrix/internal/fastmath"
	"github.com/cep21/hystrix/internal/testhelp"
)

func TestHappyCircuit(t *testing.T) {
	c := NewCircuitFromConfig("TestHappyCircuit", CommandProperties{})
	// Should work 100 times in a row
	for i := 0; i < 100; i++ {
		err := c.Execute(context.Background(), testhelp.AlwaysPasses, func(_ context.Context, _ error) error {
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
			err := c.Execute(context.Background(), testhelp.AlwaysPasses, nil)
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
	// Is never returned
	_ = c.Execute(ctx, func(_ context.Context) error {
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
	// Go never returns
	_ = c.Go(ctx, func(ctx2 context.Context) error {
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
	err := c.Go(ctx, testhelp.SleepsForX(time.Hour), nil)
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
	bc := testhelp.BehaviorCheck{
		RunFunc: testhelp.SleepsForX(time.Millisecond),
	}
	wg := sync.WaitGroup{}
	errCount := 0
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := c.Execute(context.Background(), bc.Run, nil)
			if err != nil {
				errCount++
			}
		}()
	}
	wg.Wait()
	if bc.MostConcurrent != 2 {
		t.Errorf("Concurrent count not correct: %d", bc.MostConcurrent)
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
	bc := testhelp.BehaviorCheck{
		RunFunc: testhelp.SleepsForX(time.Millisecond * 35),
	}
	err := c.Execute(context.Background(), bc.Run, nil)
	if err == nil {
		t.Log("expected an error, got none")
	}
	if bc.LongestRunDuration >= time.Millisecond*20 {
		t.Log("A cancel didn't happen fast enough")
	}
}

func TestFailingCircuit(t *testing.T) {
	c := NewCircuitFromConfig("TestFailingCircuit", CommandProperties{})
	err := c.Execute(context.Background(), testhelp.AlwaysFails, nil)
	if err == nil || err.Error() != "alwaysFails failure" {
		t.Error("saw no error from circuit that always fails")
	}
}

func TestFallbackCircuit(t *testing.T) {
	c := NewCircuitFromConfig("TestFallbackCircuit", CommandProperties{})
	// Fallback circuit should consistently fail
	for i := 0; i < 100; i++ {
		err := c.Execute(context.Background(), testhelp.AlwaysFails, testhelp.AlwaysPassesFallback)
		if err != nil {
			t.Error("saw error from circuit that has happy fallback", err)
		}
	}

	// By default, we never open/close
	if c.IsOpen() {
		t.Error("I expected to never open by default")
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
		err := c.Execute(rootCtx, testhelp.SleepsForX(time.Second), nil)
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
			err := c.Execute(context.Background(), testhelp.AlwaysFails, func(ctx context.Context, err error) error {
				fallbackExecuted.Add(1)
				return testhelp.SleepsForX(time.Millisecond * 500)(ctx)
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

func TestFailingFallbackCircuit(t *testing.T) {
	c := NewCircuitFromConfig("TestFailingCircuit", CommandProperties{})
	err := c.Execute(context.Background(), testhelp.AlwaysFails, testhelp.AlwaysFailsFallback)
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
	bc := testhelp.BehaviorCheck{
		RunFunc: testhelp.SleepsForX(time.Millisecond * 35),
	}
	err := c.Execute(context.Background(), bc.Run, func(ctx context.Context, err error) error {
		if ctx.Err() != nil {
			return errors.New("the passed in context should not be finished")
		}
		return nil
	})
	if err != nil {
		t.Log("Should be no error since the fallback didn't error")
	}
	if bc.LongestRunDuration >= time.Millisecond*20 {
		t.Log("A cancel didn't happen fast enough")
	}
}

// Just test to make sure the -race detector doesn't find anything with a public function
func TestVariousRaceConditions(t *testing.T) {
	concurrentThreads := 5
	c := NewCircuitFromConfig("TestVariousRaceConditions", CommandProperties{
		Execution: ExecutionConfig{
			MaxConcurrentRequests: int64(-1),
		},
		Fallback: FallbackConfig{
			MaxConcurrentRequests: int64(-1),
		},
	})

	doNotPassTime := time.Now().Add(time.Millisecond * 20)

	wg := sync.WaitGroup{}
	for i := 0; i < concurrentThreads; i++ {
		testhelp.DoTillTime(doNotPassTime, &wg, func() {
			c.Var()
		})
		testhelp.DoTillTime(doNotPassTime, &wg, func() {
			c.IsOpen()
		})
		testhelp.DoTillTime(doNotPassTime, &wg, func() {
			c.CloseCircuit()
		})
		testhelp.DoTillTime(doNotPassTime, &wg, func() {
			c.OpenCircuit()
		})
		testhelp.DoTillTime(doNotPassTime, &wg, func() {
			c.Name()
		})
		testhelp.DoTillTime(doNotPassTime, &wg, func() {
			// Circuit could be forced open
			_ = c.Execute(context.Background(), testhelp.AlwaysPasses, nil)
		})
		testhelp.DoTillTime(doNotPassTime, &wg, func() {
			testhelp.MustNotTesting(t, c.Execute(context.Background(), testhelp.AlwaysFails, nil))
		})
		testhelp.DoTillTime(doNotPassTime, &wg, func() {
			testhelp.MustTesting(t, c.Execute(context.Background(), testhelp.AlwaysFails, testhelp.AlwaysPassesFallback))
		})
		testhelp.DoTillTime(doNotPassTime, &wg, func() {
			testhelp.MustNotTesting(t, c.Execute(context.Background(), testhelp.AlwaysFails, testhelp.AlwaysFailsFallback))
		})
	}
	wg.Wait()
}
