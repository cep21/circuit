package hystrix

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/cep21/hystrix"
	"github.com/cep21/hystrix/internal/testhelp"
)

func TestCircuitCloses(t *testing.T) {
	c := hystrix.NewCircuitFromConfig("TestCircuitCloses", hystrix.CommandProperties{
		GoSpecific: hystrix.GoSpecificConfig{
			OpenToClosedFactory: SleepyCloseCheckFactory(ConfigureSleepyCloseCheck{}),
			ClosedToOpenFactory: OpenOnErrPercentageFactory(ConfigureOpenOnErrPercentage{
				RequestVolumeThreshold: 1,
			}),
		},
	})

	if c.IsOpen() {
		t.Fatal("Circuit should not start out open")
	}
	err := c.Execute(context.Background(), testhelp.AlwaysFails, nil)
	if err == nil {
		t.Fatal("Circuit should have failed if run fails")
	}
	if !c.IsOpen() {
		t.Fatal("Circuit should be open after having failed once")
	}
	err = c.Execute(context.Background(), testhelp.AlwaysPasses, nil)
	if err == nil {
		t.Fatal("Circuit should be open")
	}
}

func TestCircuitAttemptsToReopen(t *testing.T) {
	c := hystrix.NewCircuitFromConfig("TestCircuitAttemptsToReopen", hystrix.CommandProperties{
		GoSpecific: hystrix.GoSpecificConfig{
			OpenToClosedFactory: SleepyCloseCheckFactory(ConfigureSleepyCloseCheck{
				SleepWindow: time.Millisecond,
			}),
			ClosedToOpenFactory: OpenOnErrPercentageFactory(ConfigureOpenOnErrPercentage{
				RequestVolumeThreshold: 1,
			}),
		},
	})
	if c.IsOpen() {
		t.Fatal("Circuit should not start out open")
	}
	err := c.Execute(context.Background(), testhelp.AlwaysFails, nil)
	if err == nil {
		t.Fatal("Circuit should have failed if run fails")
	}
	if !c.IsOpen() {
		t.Fatal("Circuit should be open after having failed once")
	}
	err = c.Execute(context.Background(), testhelp.AlwaysPasses, nil)
	if err == nil {
		t.Fatal("Circuit should be open")
	}

	time.Sleep(time.Millisecond * 3)
	err = c.Execute(context.Background(), testhelp.AlwaysPasses, nil)
	if err != nil {
		t.Fatal("Circuit should try to reopen")
	}
}

func TestCircuitAttemptsToReopenOnlyOnce(t *testing.T) {
	c := hystrix.NewCircuitFromConfig("TestCircuitAttemptsToReopenOnlyOnce", hystrix.CommandProperties{
		GoSpecific: hystrix.GoSpecificConfig{
			OpenToClosedFactory: SleepyCloseCheckFactory(ConfigureSleepyCloseCheck{
				SleepWindow: time.Millisecond,
			}),
			ClosedToOpenFactory: OpenOnErrPercentageFactory(ConfigureOpenOnErrPercentage{
				RequestVolumeThreshold: 1,
			}),
		},
	})
	if c.IsOpen() {
		t.Fatal("Circuit should not start out open")
	}
	err := c.Execute(context.Background(), testhelp.AlwaysFails, nil)
	if err == nil {
		t.Fatal("Circuit should have failed if run fails")
	}
	if !c.IsOpen() {
		t.Fatal("Circuit should be open after having failed once")
	}
	err = c.Execute(context.Background(), testhelp.AlwaysPasses, nil)
	if err == nil {
		t.Fatal("Circuit should be open")
	}

	time.Sleep(time.Millisecond * 3)
	err = c.Execute(context.Background(), testhelp.AlwaysFails, nil)
	if err == nil {
		t.Fatal("Circuit should try to reopen, but fail")
	}
	err = c.Execute(context.Background(), testhelp.AlwaysPasses, nil)
	if err == nil {
		t.Fatal("Circuit should only try to reopen once")
	}
}

func TestLargeSleepWindow(t *testing.T) {
	c := hystrix.NewCircuitFromConfig("TestLargeSleepWindow", hystrix.CommandProperties{
		GoSpecific: hystrix.GoSpecificConfig{
			OpenToClosedFactory: SleepyCloseCheckFactory(ConfigureSleepyCloseCheck{
				SleepWindow: time.Hour,
			}),
			ClosedToOpenFactory: OpenOnErrPercentageFactory(ConfigureOpenOnErrPercentage{
				RequestVolumeThreshold:   1,
				ErrorThresholdPercentage: 1,
			}),
		},
	})

	err := c.Execute(context.Background(), testhelp.AlwaysFails, testhelp.AlwaysPassesFallback)
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
				err := c.Execute(context.Background(), testhelp.SleepsForX(time.Millisecond/10), nil)
				if err == nil {
					t.Errorf("I expect this to always fail, now that it's in the failure state")
				}
				time.Sleep(time.Millisecond / 10)
			}
		}()
	}
	wg.Wait()
}

func TestSleepDurationWorks(t *testing.T) {
	concurrentThreads := 25
	sleepWindow := time.Millisecond * 20
	c := hystrix.NewCircuitFromConfig("TestSleepDurationWorks", hystrix.CommandProperties{
		Execution: hystrix.ExecutionConfig{
			MaxConcurrentRequests: int64(concurrentThreads),
		},
		Fallback: hystrix.FallbackConfig{
			MaxConcurrentRequests: int64(concurrentThreads),
		},
		GoSpecific: hystrix.GoSpecificConfig{
			OpenToClosedFactory: SleepyCloseCheckFactory(ConfigureSleepyCloseCheck{
				SleepWindow: sleepWindow + time.Millisecond*5,
			}),
			ClosedToOpenFactory: OpenOnErrPercentageFactory(ConfigureOpenOnErrPercentage{
				RequestVolumeThreshold:   1,
				ErrorThresholdPercentage: 1,
			}),
		},
	})

	// Once failing, c should never see more than one request every 40 ms
	// If I wait 110 ms, I should see exactly 2 requests (the one at 40 and at 80)
	doNotPassTime := time.Now().Add(sleepWindow * 4)
	err := c.Execute(context.Background(), testhelp.AlwaysFails, testhelp.AlwaysPassesFallback)
	if err != nil {
		t.Errorf("I expect this to not fail since it has a fallback")
	}

	bc := testhelp.BehaviorCheck{
		RunFunc: testhelp.AlwaysFails,
	}

	lastRequestTime := time.Now()
	c.OpenCircuit()
	if !c.IsOpen() {
		t.Errorf("circuit should be open after I open it")
	}
	var mu sync.Mutex

	wg := sync.WaitGroup{}
	for ct := 0; ct < concurrentThreads; ct++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if time.Now().After(doNotPassTime) {
					break
				}
				err := c.Execute(context.Background(), func(_ context.Context) error {
					now := time.Now()
					mu.Lock()
					if time.Since(lastRequestTime) < sleepWindow {
						t.Errorf("I am getting too many requests: %s", time.Since(lastRequestTime))
					}
					lastRequestTime = now
					mu.Unlock()
					return errors.New("failure")
				}, testhelp.AlwaysPassesFallback)
				if err != nil {
					t.Errorf("The fallback was fine.  It should not fail (but should stay open): %s", err)
				}
				// Don't need to sleep.  Just busy loop.  But let another thread take over if it wants (to get some concurrency)
				runtime.Gosched()
			}
		}()
	}
	wg.Wait()
	if bc.TotalRuns > 3 {
		t.Error("Too many requests", bc.TotalRuns)
	}
}

func TestCircuitRecovers(t *testing.T) {
	concurrentThreads := 25
	sleepWindow := time.Millisecond * 5
	c := hystrix.NewCircuitFromConfig("TestCircuitRecovers", hystrix.CommandProperties{
		GoSpecific: hystrix.GoSpecificConfig{
			OpenToClosedFactory: SleepyCloseCheckFactory(ConfigureSleepyCloseCheck{
				//		// This should allow a new request every 10 milliseconds
				SleepWindow: time.Millisecond * 5,
			}),
			ClosedToOpenFactory: OpenOnErrPercentageFactory(ConfigureOpenOnErrPercentage{
				RequestVolumeThreshold:   1,
				ErrorThresholdPercentage: 1,
			}),
		},
		Execution: hystrix.ExecutionConfig{
			MaxConcurrentRequests: int64(concurrentThreads),
		},
		Fallback: hystrix.FallbackConfig{
			MaxConcurrentRequests: int64(concurrentThreads),
		},
	})

	// This is when the circuit starts working again
	startWorkingTime := time.Now().Add(sleepWindow * 2)
	// This is the latest that the circuit should keep failing requests
	circuitOkTime := startWorkingTime.Add(sleepWindow).Add(time.Millisecond * 200)

	// Give some buffer so time.AfterFunc can get called
	doNotPassTime := time.Now().Add(time.Millisecond * 250)
	err := c.Execute(context.Background(), testhelp.AlwaysFails, testhelp.AlwaysPassesFallback)
	if err != nil {
		t.Errorf("I expect this to not fail since it has a fallback")
	}
	if !c.IsOpen() {
		t.Errorf("I expect the circuit to open after that one, first failure")
	}

	workingAtThisTime := func(t time.Time) bool {
		return t.After(startWorkingTime)
	}

	failure := errors.New("a failure")
	bc := testhelp.BehaviorCheck{
		RunFunc: func(_ context.Context) error {
			if workingAtThisTime(time.Now()) {
				return nil
			}
			return failure
		},
	}

	wg := sync.WaitGroup{}
	for ct := 0; ct < concurrentThreads; ct++ {
		testhelp.DoTillTime(doNotPassTime, &wg, func() {
			isCircuitOk := time.Now().After(circuitOkTime)
			justBeforeTime := time.Now()
			err := c.Execute(context.Background(), bc.Run, nil)
			if err != nil {
				if isCircuitOk {
					t.Fatalf("Should not get an error after this time: The circuit should be ok: %s", err)
				}
				if circuitOkTime.Before(justBeforeTime) {
					t.Fatalf("Should not get an error after the circuit healed itself")
				}
			}
			if err == nil {
				if time.Now().Before(startWorkingTime) {
					t.Fatalf("The circuit should not work before I correct the service")
				}
			}
		})
	}
	wg.Wait()
}
