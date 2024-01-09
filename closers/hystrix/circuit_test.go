package hystrix

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"sync/atomic"

	"github.com/cep21/circuit/v4"
	"github.com/cep21/circuit/v4/internal/testhelp"
)

func TestCloser_closes(t *testing.T) {
	f := Factory{
		ConfigureOpener: ConfigureOpener{
			RequestVolumeThreshold: 1,
		},
	}
	h := circuit.Manager{
		DefaultCircuitProperties: []circuit.CommandPropertiesConstructor{
			f.Configure,
		},
	}
	c := h.MustCreateCircuit("TestCircuitCloses")

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
	c := circuit.NewCircuitFromConfig("TestCircuitAttemptsToReopen", circuit.Config{
		General: circuit.GeneralConfig{
			OpenToClosedFactory: CloserFactory(ConfigureCloser{
				SleepWindow: time.Millisecond,
			}),
			ClosedToOpenFactory: OpenerFactory(ConfigureOpener{
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

	// Takes some time for slow servers to trigger the timer
	var i int
	// Try for 20 sec
	for i = 0; i < 200; i++ {
		err = c.Execute(context.Background(), testhelp.AlwaysPasses, nil)
		if err == nil {
			break
		}
		time.Sleep(time.Millisecond * 100)
	}

	if i == 200 {
		t.Fatal("Circuit should try to reopen")
	}
}

func TestCircuitAttemptsToReopenOnlyOnce(t *testing.T) {
	c := circuit.NewCircuitFromConfig("TestCircuitAttemptsToReopenOnlyOnce", circuit.Config{
		General: circuit.GeneralConfig{
			OpenToClosedFactory: CloserFactory(ConfigureCloser{
				SleepWindow: time.Millisecond,
			}),
			ClosedToOpenFactory: OpenerFactory(ConfigureOpener{
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
	c := circuit.NewCircuitFromConfig("TestLargeSleepWindow", circuit.Config{
		General: circuit.GeneralConfig{
			OpenToClosedFactory: CloserFactory(ConfigureCloser{
				SleepWindow: time.Hour,
			}),
			ClosedToOpenFactory: OpenerFactory(ConfigureOpener{
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
	concurrentThreads := 10
	sleepWindow := time.Millisecond * 25
	c := circuit.NewCircuitFromConfig("TestSleepDurationWorks", circuit.Config{
		Execution: circuit.ExecutionConfig{
			MaxConcurrentRequests: int64(concurrentThreads),
		},
		Fallback: circuit.FallbackConfig{
			MaxConcurrentRequests: int64(concurrentThreads),
		},
		General: circuit.GeneralConfig{
			OpenToClosedFactory: CloserFactory(ConfigureCloser{
				SleepWindow: sleepWindow * 2,
			}),
			ClosedToOpenFactory: OpenerFactory(ConfigureOpener{
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

	if c.OpenToClose.(*Closer).Config().SleepWindow != time.Millisecond*50 {
		t.Errorf("I expect a 30 ms sleep window")
	}

	bc := testhelp.BehaviorCheck{
		RunFunc: testhelp.AlwaysFails,
	}

	var lastRequestTime atomic.Value
	lastRequestTime.Store(time.Now())
	c.OpenCircuit()
	if !c.IsOpen() {
		t.Errorf("circuit should be open after I open it")
	}

	wg := sync.WaitGroup{}
	for ct := 0; ct < concurrentThreads; ct++ {
		testhelp.DoTillTime(doNotPassTime, &wg, func() {
			err := c.Execute(context.Background(), func(_ context.Context) error {
				now := time.Now()
				if now.Sub(lastRequestTime.Load().(time.Time)) < sleepWindow {
					t.Errorf("I am getting too many requests: %s", time.Since(lastRequestTime.Load().(time.Time)))
				}
				lastRequestTime.Store(now)
				if !c.IsOpen() {
					t.Error("This circuit should never close itself")
				}
				return errors.New("failure")
			}, testhelp.AlwaysPassesFallback)
			if err != nil {
				t.Errorf("The fallback was fine.  It should not fail (but should stay open): %s", err)
			}
		})
	}
	wg.Wait()
	if bc.TotalRuns > 3 {
		t.Error("Too many requests", bc.TotalRuns)
	}
}

func TestCircuitRecovers(t *testing.T) {
	concurrentThreads := 25
	sleepWindow := time.Millisecond * 5
	c := circuit.NewCircuitFromConfig("TestCircuitRecovers", circuit.Config{
		General: circuit.GeneralConfig{
			OpenToClosedFactory: CloserFactory(ConfigureCloser{
				//		// This should allow a new request every 10 milliseconds
				SleepWindow: time.Millisecond * 5,
			}),
			ClosedToOpenFactory: OpenerFactory(ConfigureOpener{
				RequestVolumeThreshold:   1,
				ErrorThresholdPercentage: 1,
			}),
		},
		Execution: circuit.ExecutionConfig{
			MaxConcurrentRequests: int64(concurrentThreads),
		},
		Fallback: circuit.FallbackConfig{
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
