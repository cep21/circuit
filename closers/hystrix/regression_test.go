package hystrix

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cep21/circuit/v4"
	"github.com/cep21/circuit/v4/internal/clock"
)

func newMockClockCircuit(t *testing.T, name string, mc *clock.MockClock, closerCfg ConfigureCloser) *circuit.Circuit {
	t.Helper()
	closerCfg.AfterFunc = mc.AfterFunc
	return circuit.NewCircuitFromConfig(name, circuit.Config{
		General: circuit.GeneralConfig{
			TimeKeeper:          circuit.TimeKeeper{Now: mc.Now, AfterFunc: mc.AfterFunc},
			OpenToClosedFactory: CloserFactory(closerCfg),
			ClosedToOpenFactory: OpenerFactory(ConfigureOpener{RequestVolumeThreshold: 1, Now: mc.Now}),
		},
		Execution: circuit.ExecutionConfig{Timeout: -1, MaxConcurrentRequests: -1},
	})
}

// A fresh Closer (never Opened) must not hand out half-open probes.
func TestRegression_CloserDeniesBeforeOpened(t *testing.T) {
	c := CloserFactory(ConfigureCloser{SleepWindow: time.Hour})().(*Closer)
	if c.Allow(context.Background(), time.Now()) {
		t.Fatal("Allow returned true although Opened() was never called")
	}
}

// After Closed(), Allow must stay false (until the next Opened arms a new sleep window), otherwise a probe leaks
// through at the instant of the next open.
func TestRegression_CloserDeniesAfterClosed(t *testing.T) {
	mc := &clock.MockClock{}
	mc.Set(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	c := CloserFactory(ConfigureCloser{SleepWindow: time.Second, AfterFunc: mc.AfterFunc})().(*Closer)
	ctx := context.Background()
	c.Opened(ctx, mc.Now())
	mc.Add(2 * time.Second)
	if !c.Allow(ctx, mc.Now()) {
		t.Fatal("expected a probe after the sleep window")
	}
	c.Success(ctx, mc.Now(), time.Millisecond)
	if !c.ShouldClose(ctx, mc.Now()) {
		t.Fatal("expected to close after a successful probe")
	}
	c.Closed(ctx, mc.Now())
	mc.Add(time.Hour)
	if c.Allow(ctx, mc.Now()) {
		t.Fatal("Allow returned true while closed")
	}
}

// ForceOpen with the real hystrix closer must never run runFunc.
func TestRegression_ForceOpenWithHystrixCloser(t *testing.T) {
	mc := &clock.MockClock{}
	mc.Set(time.Now())
	c := circuit.NewCircuitFromConfig("fo", circuit.Config{General: circuit.GeneralConfig{
		ForceOpen:           true,
		TimeKeeper:          circuit.TimeKeeper{Now: mc.Now, AfterFunc: mc.AfterFunc},
		OpenToClosedFactory: CloserFactory(ConfigureCloser{SleepWindow: time.Millisecond, AfterFunc: mc.AfterFunc}),
		ClosedToOpenFactory: OpenerFactory(ConfigureOpener{Now: mc.Now}),
	}})
	ran := 0
	for i := 0; i < 50; i++ {
		mc.Add(time.Millisecond)
		_ = c.Execute(context.Background(), func(context.Context) error { ran++; return nil }, nil)
	}
	if ran != 0 {
		t.Fatalf("ForceOpen circuit executed runFunc %d time(s)", ran)
	}
}

// A request that started while the circuit was closed and succeeds right after it opens must not close it: the
// SleepWindow has to be honored and only half-open probes count.
func TestRegression_InFlightSuccessDoesNotCloseFreshlyOpenedCircuit(t *testing.T) {
	mc := &clock.MockClock{}
	mc.Set(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	c := newMockClockCircuit(t, "inflight", mc, ConfigureCloser{SleepWindow: time.Hour})
	ctx := context.Background()

	releaseA, aStarted, aDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		aDone <- c.Execute(ctx, func(context.Context) error { close(aStarted); <-releaseA; return nil }, nil)
	}()
	<-aStarted
	mc.Add(time.Millisecond)
	_ = c.Execute(ctx, func(context.Context) error { return errors.New("boom") }, nil)
	if !c.IsOpen() {
		t.Fatal("precondition: circuit should have opened")
	}
	mc.Add(time.Millisecond)
	close(releaseA)
	if err := <-aDone; err != nil {
		t.Fatalf("in-flight request should still succeed: %v", err)
	}
	if !c.IsOpen() {
		t.Fatal("circuit closed immediately due to a pre-open in-flight success; SleepWindow (1h) never honored")
	}

	// And the normal recovery path still works
	mc.Add(time.Hour + time.Millisecond)
	if err := c.Execute(ctx, func(context.Context) error { return nil }, nil); err != nil {
		t.Fatalf("probe after sleep window should run and pass: %v", err)
	}
	if c.IsOpen() {
		t.Fatal("successful probe should close the circuit")
	}
}

type hookRunMetrics struct {
	circuit.RunMetrics
	onSuccess func()
}

func (h *hookRunMetrics) Success(ctx context.Context, now time.Time, d time.Duration) {
	if h.onSuccess != nil {
		h.onSuccess()
	}
}

// A stale failure from a request that started before the circuit opened must not reset the half-open success
// streak and waste a healthy probe.
func TestRegression_StaleFailureDoesNotPoisonProbe(t *testing.T) {
	mc := &clock.MockClock{}
	mc.Set(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	hook := &hookRunMetrics{RunMetrics: circuit.RunMetricsCollection{}}
	c := circuit.NewCircuitFromConfig("stale", circuit.Config{
		General: circuit.GeneralConfig{
			TimeKeeper:          circuit.TimeKeeper{Now: mc.Now, AfterFunc: mc.AfterFunc},
			OpenToClosedFactory: CloserFactory(ConfigureCloser{SleepWindow: time.Minute, AfterFunc: mc.AfterFunc}),
			ClosedToOpenFactory: OpenerFactory(ConfigureOpener{RequestVolumeThreshold: 1, Now: mc.Now}),
		},
		Execution: circuit.ExecutionConfig{Timeout: -1, MaxConcurrentRequests: -1},
		Metrics:   circuit.MetricsCollectors{Run: []circuit.RunMetrics{hook}},
	})
	ctx := context.Background()

	// A: slow request started while closed, will eventually fail
	releaseA, aStarted, aDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(aDone)
		_ = c.Execute(ctx, func(context.Context) error { close(aStarted); <-releaseA; return errors.New("late") }, nil)
	}()
	<-aStarted
	mc.Add(time.Millisecond)
	// B fails and opens the circuit
	_ = c.Execute(ctx, func(context.Context) error { return errors.New("boom") }, nil)
	if !c.IsOpen() {
		t.Fatal("precondition: open")
	}
	mc.Add(time.Minute + time.Millisecond)
	// Probe C succeeds; while its Success is being processed (before the circuit asks ShouldClose) A's stale
	// failure lands.
	hook.onSuccess = func() {
		close(releaseA)
		<-aDone
	}
	if err := c.Execute(ctx, func(context.Context) error { return nil }, nil); err != nil {
		t.Fatalf("probe should have been allowed and passed: %v", err)
	}
	hook.onSuccess = nil
	if c.IsOpen() {
		t.Fatal("healthy half-open probe succeeded but a stale pre-open failure kept the circuit open")
	}
}

type countingCloser struct {
	circuit.OpenToClosed
	allowed atomic.Int64
}

func (c *countingCloser) Allow(ctx context.Context, now time.Time) bool {
	r := c.OpenToClosed.Allow(ctx, now)
	if r {
		c.allowed.Add(1)
	}
	return r
}

// At the closed->open transition, concurrent requests must not be granted a "probe" before the sleep window is armed.
func TestRegression_NoProbeLeakOnOpenTransition(t *testing.T) {
	var leaks int64
	for iter := 0; iter < 50; iter++ {
		var cc *countingCloser
		c := circuit.NewCircuitFromConfig("leak", circuit.Config{
			General: circuit.GeneralConfig{
				OpenToClosedFactory: func() circuit.OpenToClosed {
					cc = &countingCloser{OpenToClosed: CloserFactory(ConfigureCloser{SleepWindow: time.Hour})()}
					return cc
				},
				ClosedToOpenFactory: OpenerFactory(ConfigureOpener{RequestVolumeThreshold: 1}),
			},
			Execution: circuit.ExecutionConfig{Timeout: -1, MaxConcurrentRequests: -1},
		})
		var wg sync.WaitGroup
		start := make(chan struct{})
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for i := 0; i < 200; i++ {
					_ = c.Execute(context.Background(), func(context.Context) error { return errors.New("fail") }, nil)
				}
			}()
		}
		close(start)
		wg.Wait()
		leaks += cc.allowed.Load()
	}
	if leaks > 0 {
		t.Fatalf("Allow returned true %d time(s) within a 1h SleepWindow", leaks)
	}
}

// SetConfigThreadSafe used to write TimedCheck.TimeAfterFunc without the TimedCheck's lock.
func TestRegression_CloserSetConfigThreadSafeRace(t *testing.T) {
	ctx := context.Background()
	c := CloserFactory(ConfigureCloser{SleepWindow: time.Millisecond})().(*Closer)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			c.SetConfigThreadSafe(ConfigureCloser{SleepWindow: time.Millisecond, HalfOpenAttempts: 1, RequiredConcurrentSuccessful: 1, AfterFunc: time.AfterFunc})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			now := time.Now()
			c.Opened(ctx, now)
			c.Allow(ctx, now.Add(time.Second))
		}
		close(stop)
	}()
	wg.Wait()
}

func TestRegression_OpenerExactThreshold(t *testing.T) {
	ctx := context.Background()
	var bad []int64
	for pct := int64(1); pct <= 100; pct++ {
		now := time.Now()
		o := OpenerFactory(ConfigureOpener{ErrorThresholdPercentage: pct, RequestVolumeThreshold: 100, Now: func() time.Time { return now }})().(*Opener)
		for i := int64(0); i < 100-pct; i++ {
			o.Success(ctx, now, time.Millisecond)
		}
		for i := int64(0); i < pct; i++ {
			o.ErrFailure(ctx, now, time.Millisecond)
		}
		if !o.ShouldOpen(ctx, now) {
			bad = append(bad, pct)
		}
		// and one fewer error must NOT open
		o2 := OpenerFactory(ConfigureOpener{ErrorThresholdPercentage: pct, RequestVolumeThreshold: 100, Now: func() time.Time { return now }})().(*Opener)
		for i := int64(0); i < 100-pct+1; i++ {
			o2.Success(ctx, now, time.Millisecond)
		}
		for i := int64(0); i < pct-1; i++ {
			o2.ErrFailure(ctx, now, time.Millisecond)
		}
		if o2.ShouldOpen(ctx, now) {
			t.Fatalf("opened below threshold %d", pct)
		}
	}
	if len(bad) > 0 {
		t.Fatalf("ShouldOpen returned false at exact threshold for ErrorThresholdPercentage in %v", bad)
	}
}

func TestRegression_OpenerSetConfigNotThreadSafePartialConfig(t *testing.T) {
	var o Opener
	o.SetConfigNotThreadSafe(ConfigureOpener{RequestVolumeThreshold: 3})
	ctx := context.Background()
	now := time.Now()
	for i := 0; i < 3; i++ {
		o.ErrFailure(ctx, now, time.Millisecond)
	}
	if !o.ShouldOpen(ctx, now) {
		t.Fatal("expected to open with default buckets")
	}
	var o2 Opener
	o2.SetConfigNotThreadSafe(ConfigureOpener{NumBuckets: -1, RollingDuration: -1})
}

// Reconfiguring an open circuit (SetConfigNotThreadSafe replaces the Closer) must not wedge it open.
func TestRegression_SetConfigNotThreadSafeOnOpenCircuitRecovers(t *testing.T) {
	ctx := context.Background()
	mc := &clock.MockClock{}
	mc.Set(time.Now())
	c := newMockClockCircuit(t, "reconf", mc, ConfigureCloser{SleepWindow: time.Second})
	_ = c.Run(ctx, func(context.Context) error { return errors.New("boom") })
	if !c.IsOpen() {
		t.Fatal("expected open")
	}
	c.SetConfigNotThreadSafe(c.Config())
	mc.Add(2 * time.Second)
	ran := false
	if err := c.Run(ctx, func(context.Context) error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("never let a probe through after reconfigure; err=%v IsOpen=%v", err, c.IsOpen())
	}
	if c.IsOpen() {
		t.Fatal("expected closed after successful probe")
	}
}
