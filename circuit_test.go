package circuit

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cep21/circuit/v4/faststats"
	"github.com/cep21/circuit/v4/internal/testhelp"
)

func TestHappyCircuit(t *testing.T) {
	c := NewCircuitFromConfig("TestHappyCircuit", Config{})
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

func testCircuit(t *testing.T, c *Circuit) {
	val := 1
	err := c.Run(context.Background(), func(ctx context.Context) error {
		val = 0
		return nil
	})
	if err != nil {
		t.Error("Expected a nil error:", err)
	}
	if val != 0 {
		t.Error("Val never got reset")
	}

	err = c.Run(context.Background(), func(ctx context.Context) error {
		return errors.New("an error")
	})
	if err == nil {
		t.Error("Expected a error:", err)
	}
	if c.IsOpen() {
		t.Error("Expected it to not be open")
	}
	err = c.Run(context.Background(), func(ctx context.Context) error {
		return nil
	})
	if err != nil {
		t.Error("Expected a nil error:", err)
	}
}

func TestNilCircuit(t *testing.T) {
	testCircuit(t, nil)
}

func TestEmptyCircuit(t *testing.T) {
	testCircuit(t, &Circuit{})
}

func TestBadRequest(t *testing.T) {
	c := NewCircuitFromConfig("TestBadRequest", Config{})
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
	c := NewCircuitFromConfig("TestManyConcurrent", Config{
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
	c := NewCircuitFromConfig("TestGoFunction", Config{
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
	c := NewCircuitFromConfig("TestGoFunction", Config{
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
	c := NewCircuitFromConfig("TestGoFunction", Config{
		Execution: ExecutionConfig{
			// Make this test not timeout
			Timeout: time.Minute,
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
	c := NewCircuitFromConfig("TestGoFunction", Config{
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
	c := NewCircuitFromConfig("TestThrottled", Config{
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

func TestCircuitCloses(t *testing.T) {
	ctx := context.Background()
	c := NewCircuitFromConfig("TestCircuitCloses", Config{})
	c.OpenCircuit(ctx)
	err := c.Run(context.Background(), func(_ context.Context) error {
		panic("I should be open")
	})
	if err == nil {
		t.Errorf("I expect to fail now")
	}

	c.CloseCircuit(ctx)
	err = c.Run(context.Background(), func(_ context.Context) error {
		return errors.New("some string")
	})
	if err.Error() != "some string" {
		t.Errorf("Never executed inside logic on close circuit")
	}
}

func TestTimeout(t *testing.T) {
	c := NewCircuitFromConfig("TestThrottled", Config{
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
	c := NewCircuitFromConfig("TestFailingCircuit", Config{})
	err := c.Execute(context.Background(), testhelp.AlwaysFails, nil)
	if err == nil || err.Error() != "alwaysFails failure" {
		t.Error("saw no error from circuit that always fails")
	}
}

func TestFallbackCircuit(t *testing.T) {
	c := NewCircuitFromConfig("TestFallbackCircuit", Config{})
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

	t.Run("ignore context.DeadlineExceeded by default", func(t *testing.T) {
		c := circuitFactory(t)

		for i := 0; i < 100; i++ {
			rootCtx, cancel := context.WithTimeout(context.Background(), time.Millisecond*3)
			err := c.Execute(rootCtx, testhelp.SleepsForX(time.Second), nil)
			if err != context.DeadlineExceeded {
				t.Errorf("saw no error from circuit that should end in an error(%d):%v", i, err)
				cancel()
				break
			}
			cancel()
		}
		if c.IsOpen() {
			t.Error("Parent context cancellations should not close the circuit by default")
		}
	})

	t.Run("ignore context.Canceled by default", func(t *testing.T) {
		c := circuitFactory(t)

		for i := 0; i < 100; i++ {
			rootCtx, cancel := context.WithCancel(context.Background())
			time.AfterFunc(time.Millisecond*3, func() { cancel() })
			err := c.Execute(rootCtx, testhelp.SleepsForX(time.Second), nil)
			if err != context.Canceled {
				t.Errorf("saw no error from circuit that should end in an error(%d):%v", i, err)
				cancel()
				break
			}
			cancel()
		}
		if c.IsOpen() {
			t.Error("Parent context cancellations should not close the circuit by default")
		}
	})

	t.Run("open circuit on context.DeadlineExceeded with IgnoreInterrupts", func(t *testing.T) {
		c := circuitFactory(t, withIgnoreInterrupts(true))

		for i := 0; i < 100; i++ {
			rootCtx, cancel := context.WithTimeout(context.Background(), time.Millisecond*3)
			err := c.Execute(rootCtx, testhelp.SleepsForX(time.Second), nil)

			if err != context.DeadlineExceeded && err != errCircuitOpen {
				t.Errorf("saw no error from circuit that should end in an error(%d):%v", i, err)
				cancel()
				break
			}
			cancel()
		}
		if !c.IsOpen() {
			t.Error("Parent context cancellations should open the circuit when IgnoreInterrupts sets to true")
		}
	})

	t.Run("open circuit on context.Canceled with IgnoreInterrupts", func(t *testing.T) {
		c := circuitFactory(t, withIgnoreInterrupts(true))

		for i := 0; i < 100; i++ {
			rootCtx, cancel := context.WithCancel(context.Background())
			time.AfterFunc(time.Millisecond*3, func() { cancel() })
			err := c.Execute(rootCtx, testhelp.SleepsForX(time.Second), nil)

			if err != context.Canceled && err != errCircuitOpen {
				t.Errorf("saw no error from circuit that should end in an error(%d):%v", i, err)
				cancel()
				break
			}
			cancel()
		}
		if !c.IsOpen() {
			t.Error("Parent context cancellations should open the circuit when IgnoreInterrupts sets to true")
		}
	})

	t.Run("open circuit on context.DeadlineExceeded with IgnoreInterrupts and IsErrInterrupt", func(t *testing.T) {
		c := circuitFactory(
			t,
			withIgnoreInterrupts(true),
			withIsErrInterrupt(func(err error) bool { return err == context.Canceled }),
		)

		for i := 0; i < 100; i++ {
			rootCtx, cancel := context.WithTimeout(context.Background(), time.Millisecond*3)
			err := c.Execute(rootCtx, testhelp.SleepsForX(time.Second), nil)

			if err != context.DeadlineExceeded && err != errCircuitOpen {
				t.Errorf("saw no error from circuit that should end in an error(%d):%v", i, err)
				cancel()
				break
			}
			cancel()
		}
		if !c.IsOpen() {
			t.Error("Parent context cancellations should open the circuit when IgnoreInterrupts sets to true")
		}
	})

	t.Run("ignore context.Canceled with IgnoreInterrupts and IsErrInterrupt", func(t *testing.T) {
		c := circuitFactory(
			t,
			withIgnoreInterrupts(false),
			withIsErrInterrupt(func(err error) bool { return err == context.Canceled }),
		)

		for i := 0; i < 100; i++ {
			rootCtx, cancel := context.WithCancel(context.Background())
			rootCtx = &alwaysCanceledContext{rootCtx}
			err := c.Execute(rootCtx, func(ctx context.Context) error {
				cancel()
				return rootCtx.Err()
			}, nil)

			if err != context.Canceled && err != errCircuitOpen {
				t.Errorf("saw no error from circuit that should end in an error(%d):%v", i, err)
				cancel()
				break
			}
			if c.IsOpen() {
				t.Errorf("Iteration %d: Parent context cancellations should not open the circuit when IgnoreInterrupts sets to true", i)
				return
			}
		}
	})
}

type alwaysCanceledContext struct {
	context.Context
}

func (a *alwaysCanceledContext) Err() error {
	if a.Context.Err() != nil {
		return context.Canceled
	}
	return nil
}

func TestFallbackCircuitConcurrency(t *testing.T) {
	c := NewCircuitFromConfig("TestFallbackCircuitConcurrency", Config{
		Fallback: FallbackConfig{
			MaxConcurrentRequests: 2,
		},
	})
	wg := sync.WaitGroup{}
	workingCircuitCount := int64(0)
	var fallbackExecuted faststats.AtomicInt64
	var totalExecuted faststats.AtomicInt64
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
	c := NewCircuitFromConfig("TestFailingCircuit", Config{})
	err := c.Execute(context.Background(), testhelp.AlwaysFails, testhelp.AlwaysFailsFallback)
	if err == nil {
		t.Error("expected error back")
		t.FailNow()
	}
	if err.Error() != "failed: alwaysFails failure" {
		t.Error("unexpected error string", err)
	}
}

func TestSetConfigThreadSafe(t *testing.T) {
	var breaker Circuit

	if breaker.threadSafeConfig.CircuitBreaker.Disabled.Get() {
		t.Error("Circuit should start off not disabled")
	}
	breaker.SetConfigThreadSafe(Config{
		General: GeneralConfig{
			Disabled: true,
		},
	})
	if !breaker.threadSafeConfig.CircuitBreaker.Disabled.Get() {
		t.Error("Circuit should be disabled after setting config to disabled")
	}
}

func TestFallbackAfterTimeout(t *testing.T) {
	c := NewCircuitFromConfig("TestThrottled", Config{
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
	c := NewCircuitFromConfig("TestVariousRaceConditions", Config{
		Execution: ExecutionConfig{
			MaxConcurrentRequests: int64(-1),
		},
		Fallback: FallbackConfig{
			MaxConcurrentRequests: int64(-1),
		},
	})

	doNotPassTime := time.Now().Add(time.Millisecond * 20)
	ctx := context.Background()

	wg := sync.WaitGroup{}
	for i := 0; i < concurrentThreads; i++ {
		testhelp.DoTillTime(doNotPassTime, &wg, func() {
			c.Var()
		})
		testhelp.DoTillTime(doNotPassTime, &wg, func() {
			c.IsOpen()
		})
		testhelp.DoTillTime(doNotPassTime, &wg, func() {
			c.CloseCircuit(ctx)
		})
		testhelp.DoTillTime(doNotPassTime, &wg, func() {
			c.OpenCircuit(ctx)
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

func openOnFirstErrorFactory() ClosedToOpen {
	return &closeOnFirstErrorOpener{
		ClosedToOpen: neverOpensFactory(),
	}
}

type closeOnFirstErrorOpener struct {
	ClosedToOpen
	isOpened bool
}

func (o *closeOnFirstErrorOpener) ShouldOpen(_ context.Context, _ time.Time) bool {
	o.isOpened = true
	return true
}
func (o *closeOnFirstErrorOpener) Prevent(_ context.Context, _ time.Time) bool {
	return o.isOpened
}

type configOverride func(*Config) *Config

func withIgnoreInterrupts(b bool) configOverride {
	return func(c *Config) *Config {
		c.Execution.IgnoreInterrupts = b
		return c
	}
}

func withIsErrInterrupt(fn func(error) bool) configOverride {
	return func(c *Config) *Config {
		c.Execution.IsErrInterrupt = fn
		return c
	}
}

func circuitFactory(t *testing.T, cfgOpts ...configOverride) *Circuit {
	t.Helper()

	cfg := Config{
		General: GeneralConfig{
			ClosedToOpenFactory: openOnFirstErrorFactory,
		},
		Execution: ExecutionConfig{
			Timeout: time.Hour,
		},
	}
	for _, co := range cfgOpts {
		co(&cfg)
	}

	return NewCircuitFromConfig(t.Name(), cfg)
}

// Bug 5: OpenCircuit should use c.now() not time.Now()
func TestOpenCircuitUsesMockTime(t *testing.T) {
	mockTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	var openedTime time.Time

	c := NewCircuitFromConfig("TestOpenCircuitMockTime", Config{
		General: GeneralConfig{
			TimeKeeper: TimeKeeper{
				Now: func() time.Time { return mockTime },
			},
		},
	})
	// Override the CircuitMetricsCollector to capture the Opened time
	origCollector := c.CircuitMetricsCollector
	c.CircuitMetricsCollector = MetricsCollection{&openedTimeCapture{
		underlying: origCollector,
		capturedTime: &openedTime,
	}}
	c.OpenCircuit(context.Background())
	if !openedTime.Equal(mockTime) {
		t.Errorf("OpenCircuit should use mock time, got %v, want %v", openedTime, mockTime)
	}
}

type openedTimeCapture struct {
	underlying Metrics
	capturedTime *time.Time
}

func (o *openedTimeCapture) Opened(ctx context.Context, now time.Time) {
	*o.capturedTime = now
	if o.underlying != nil {
		o.underlying.Opened(ctx, now)
	}
}

func (o *openedTimeCapture) Closed(ctx context.Context, now time.Time) {
	if o.underlying != nil {
		o.underlying.Closed(ctx, now)
	}
}

// Bug 7: Double now() call at timeout boundary
func TestNoSpuriousTimeout(t *testing.T) {
	mockTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	c := NewCircuitFromConfig("TestNoSpuriousTimeout", Config{
		General: GeneralConfig{
			TimeKeeper: TimeKeeper{
				// Always return the same time — the function completes "instantly",
				// so it should never be considered timed out.
				Now: func() time.Time {
					return mockTime
				},
			},
		},
		Execution: ExecutionConfig{
			Timeout: time.Millisecond * 10,
		},
	})
	var timeoutSeen bool
	c.CmdMetricCollector = append(c.CmdMetricCollector, &timeoutChecker{seen: &timeoutSeen})
	err := c.Execute(context.Background(), func(_ context.Context) error {
		return nil
	}, nil)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if timeoutSeen {
		t.Error("should not see a timeout when runFunc completes within deadline")
	}
}

type timeoutChecker struct {
	seen *bool
}

func (tc *timeoutChecker) Success(_ context.Context, _ time.Time, _ time.Duration)       {}
func (tc *timeoutChecker) ErrFailure(_ context.Context, _ time.Time, _ time.Duration)    {}
func (tc *timeoutChecker) ErrTimeout(_ context.Context, _ time.Time, _ time.Duration)    { *tc.seen = true }
func (tc *timeoutChecker) ErrBadRequest(_ context.Context, _ time.Time, _ time.Duration) {}
func (tc *timeoutChecker) ErrInterrupt(_ context.Context, _ time.Time, _ time.Duration)  {}
func (tc *timeoutChecker) ErrConcurrencyLimitReject(_ context.Context, _ time.Time)       {}
func (tc *timeoutChecker) ErrShortCircuit(_ context.Context, _ time.Time)                 {}
