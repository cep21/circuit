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

// A Closer that is asked Allow without ever having been Opened must not hand out a probe right away, but must
// also not deny forever: the first Allow self-arms a sleep window starting now, and a probe is granted once it
// elapses.
func TestRegression_CloserSelfArmsWhenNeverOpened(t *testing.T) {
	mc := &clock.MockClock{}
	t0 := mc.Set(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	c := CloserFactory(ConfigureCloser{SleepWindow: time.Second, AfterFunc: mc.AfterFunc})().(*Closer)
	ctx := context.Background()
	if c.Allow(ctx, t0) {
		t.Fatal("Allow returned true although Opened() was never called")
	}
	mc.Add(500 * time.Millisecond)
	if c.Allow(ctx, mc.Now()) {
		t.Fatal("Allow returned true inside the self-armed sleep window")
	}
	mc.Add(500*time.Millisecond + time.Millisecond)
	if !c.Allow(ctx, mc.Now()) {
		t.Fatal("Allow still false after the self-armed sleep window elapsed; a never-Opened closer would deny forever")
	}
}

// One Closer instance (wrongly) shared by two circuits must not wedge either of them open: when B recovers it calls
// Closed on the shared closer while A is still open.  A's requests are denied (without re-arming) for one
// SleepWindow after that close, then the next Allow self-arms, and A gets its probe one SleepWindow after that.
func TestRegression_SharedCloserDoesNotWedge(t *testing.T) {
	mc := &clock.MockClock{}
	mc.Set(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	shared := CloserFactory(ConfigureCloser{SleepWindow: time.Second, AfterFunc: mc.AfterFunc})()
	newCircuit := func(name string) *circuit.Circuit {
		return circuit.NewCircuitFromConfig(name, circuit.Config{
			General: circuit.GeneralConfig{
				TimeKeeper:          circuit.TimeKeeper{Now: mc.Now, AfterFunc: mc.AfterFunc},
				OpenToClosedFactory: func() circuit.OpenToClosed { return shared },
				ClosedToOpenFactory: OpenerFactory(ConfigureOpener{RequestVolumeThreshold: 1, Now: mc.Now}),
			},
			Execution: circuit.ExecutionConfig{Timeout: -1, MaxConcurrentRequests: -1},
		})
	}
	a, b := newCircuit("shared-a"), newCircuit("shared-b")
	ctx := context.Background()
	fail := func(context.Context) error { return errors.New("boom") }
	pass := func(context.Context) error { return nil }

	_ = a.Execute(ctx, fail, nil)
	mc.Add(time.Millisecond)
	_ = b.Execute(ctx, fail, nil)
	if !a.IsOpen() || !b.IsOpen() {
		t.Fatalf("precondition: both open; a=%v b=%v", a.IsOpen(), b.IsOpen())
	}

	// B probes and recovers.  Its Closed() lands on the shared closer while A is still open.
	mc.Add(time.Second + time.Millisecond)
	if err := b.Execute(ctx, pass, nil); err != nil {
		t.Fatalf("B's probe should have run and passed: %v", err)
	}
	if b.IsOpen() {
		t.Fatal("B should have closed after a successful probe")
	}
	if !a.IsOpen() {
		t.Fatal("precondition: A still open")
	}

	// A: the shared closer no longer knows about A's open episode.  Right after B's close A is short-circuited and
	// the closer, having just been told Closed, does not re-arm ...
	shortCircuited := func(when string) {
		t.Helper()
		ran := false
		if err := a.Execute(ctx, func(context.Context) error { ran = true; return nil }, nil); err == nil || ran {
			t.Fatalf("expected A's request %s to be short circuited; err=%v ran=%v", when, err, ran)
		}
	}
	shortCircuited("right after B closed")
	// ... one SleepWindow after that close it is still short-circuited, but this Allow self-arms a window ...
	mc.Add(time.Second + time.Millisecond)
	shortCircuited("one SleepWindow after B closed")
	// ... and once that window elapses A gets its probe and recovers.  Before the fix A short-circuited forever.
	mc.Add(time.Second + time.Millisecond)
	ran := false
	if err := a.Execute(ctx, func(context.Context) error { ran = true; return nil }, nil); err != nil || !ran {
		t.Fatalf("A never got a probe after the self-armed window; err=%v ran=%v IsOpen=%v", err, ran, a.IsOpen())
	}
	if a.IsOpen() {
		t.Fatal("A should have closed after its successful probe")
	}
}

// A request that read the open state just before the circuit closed asks Allow right after Closed().  That must not
// re-arm the Closer: while the circuit is closed openedAt stays nil (so closed-circuit results are not counted as
// probe results), and only once a full SleepWindow has passed since the close does an Allow self-arm again.
func TestRegression_AllowRacingCloseDoesNotArm(t *testing.T) {
	mc := &clock.MockClock{}
	t0 := mc.Set(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	c := CloserFactory(ConfigureCloser{SleepWindow: time.Hour, AfterFunc: mc.AfterFunc})().(*Closer)
	ctx := context.Background()

	c.Opened(ctx, t0)
	t1 := mc.Add(2 * time.Hour)
	c.Closed(ctx, t1)

	if c.Allow(ctx, t1) {
		t.Fatal("Allow returned true right after Closed")
	}
	if c.openedAt.Load() != nil {
		t.Fatal("an Allow that raced Closed re-armed the closer")
	}
	for i := 0; i < 5; i++ {
		c.Success(ctx, t1.Add(10*time.Millisecond), time.Millisecond)
	}
	if got := c.concurrentSuccessfulAttempts.Get(); got != 0 {
		t.Fatalf("successes while closed were counted as probe results: %d", got)
	}

	if c.Allow(ctx, mc.Add(30*time.Minute)) {
		t.Fatal("Allow returned true half a SleepWindow after Closed")
	}
	if c.openedAt.Load() != nil {
		t.Fatal("Allow re-armed the closer less than a SleepWindow after Closed")
	}

	// A full SleepWindow after the close nobody is going to tell us any more: self-arm (deny now, probe later).
	if c.Allow(ctx, mc.Add(30*time.Minute+time.Millisecond)) {
		t.Fatal("the self-arming Allow must deny")
	}
	if c.openedAt.Load() == nil {
		t.Fatal("Allow a full SleepWindow after Closed should have self-armed")
	}
	if !c.Allow(ctx, mc.Add(time.Hour+time.Millisecond)) {
		t.Fatal("expected a probe one SleepWindow after the self-arm")
	}
}

// The circuit flips to open at ~t1 and a request that observed the flipped state reaches Allow before the real
// Opened(t2) does: on a never-opened Closer that Allow self-arms openedAt=t1.  Meanwhile a straggler that started at
// t0' (t1 < t0' < t2, i.e. while the circuit was for all practical purposes still closed) succeeds while Opened(t2)
// is half way through arming, is measured against the stale t1 and counted.  Once Opened(t2) returns that count must
// be gone, otherwise the circuit closes again right after opening and the SleepWindow is never honored.  The
// straggler's Success is injected from the AfterFunc hook that arm reaches inside SleepStart, i.e. while openedAt is
// still the self-armed t1 and before Opened(t2) publishes t2 and resets the streak.
func TestRegression_OpenedWipesSuccessCountedUnderSelfArmedOpenedAt(t *testing.T) {
	mc := &clock.MockClock{}
	t1 := mc.Set(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	ctx := context.Background()
	var straggler func()
	afterFunc := func(d time.Duration, f func()) *time.Timer {
		if straggler != nil {
			run := straggler
			straggler = nil
			run()
		}
		return mc.AfterFunc(d, f)
	}
	c := CloserFactory(ConfigureCloser{SleepWindow: time.Hour, AfterFunc: afterFunc})().(*Closer)

	// R1 saw the open state and asks before Opened() arrived: denied, but it self-arms openedAt=t1.
	if c.Allow(ctx, t1) {
		t.Fatal("Allow on a never-opened closer must deny")
	}
	if got := c.openedAt.Load(); got == nil || !got.Equal(t1) {
		t.Fatalf("precondition: Allow should have self-armed openedAt=t1, got %v", got)
	}

	// The real Opened(t2).  While it arms, straggler R0 (started at t0' = t1+2ms < t2, ran 1ms) reports success.
	t0p := t1.Add(2 * time.Millisecond)
	t2 := mc.Add(5 * time.Millisecond)
	straggler = func() { c.Success(ctx, t0p.Add(time.Millisecond), time.Millisecond) }
	c.Opened(ctx, t2)
	if straggler != nil {
		t.Fatal("precondition: Opened should have gone through AfterFunc and run the straggler")
	}
	if c.ShouldClose(ctx, t2) {
		t.Fatal("a success that started before the circuit opened survived Opened(); the circuit would close right after opening")
	}

	// A genuine probe of the new episode still counts.
	t3 := mc.Add(time.Hour + time.Millisecond)
	if !c.Allow(ctx, t3) {
		t.Fatal("expected a probe one SleepWindow after Opened")
	}
	t4 := mc.Add(time.Millisecond)
	c.Success(ctx, t4, t4.Sub(t3))
	if !c.ShouldClose(ctx, t4) {
		t.Fatal("a successful probe that started after Opened should close the circuit")
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

// hookRunMetrics forwards to the embedded RunMetrics except for Success / ErrFailure, which call the optional hooks
type hookRunMetrics struct {
	circuit.RunMetrics
	onSuccess    func()
	onErrFailure func()
}

func (h *hookRunMetrics) Success(context.Context, time.Time, time.Duration) {
	if h.onSuccess != nil {
		h.onSuccess()
	}
}

func (h *hookRunMetrics) ErrFailure(context.Context, time.Time, time.Duration) {
	if h.onErrFailure != nil {
		h.onErrFailure()
	}
}

// The `now` handed to Opened() must be no earlier than the instant the circuit actually flipped to open, not merely
// the tripping request's completion time: a request that starts in between (after the tripping failure finished but
// before the state flipped) saw a closed circuit, is not a half-open probe, and its success must not close the
// circuit.
func TestRegression_RequestStartedBeforeOpenFlipIsNotAProbe(t *testing.T) {
	mc := &clock.MockClock{}
	mc.Set(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	hook := &hookRunMetrics{RunMetrics: circuit.RunMetricsCollection{}}
	c := circuit.NewCircuitFromConfig("pre-flip", circuit.Config{
		General: circuit.GeneralConfig{
			TimeKeeper:          circuit.TimeKeeper{Now: mc.Now, AfterFunc: mc.AfterFunc},
			OpenToClosedFactory: CloserFactory(ConfigureCloser{SleepWindow: time.Hour, AfterFunc: mc.AfterFunc}),
			ClosedToOpenFactory: OpenerFactory(ConfigureOpener{RequestVolumeThreshold: 1, Now: mc.Now}),
		},
		Execution: circuit.ExecutionConfig{Timeout: -1, MaxConcurrentRequests: -1},
		Metrics:   circuit.MetricsCollectors{Run: []circuit.RunMetrics{hook}},
	})
	ctx := context.Background()
	const deadlock = 10 * time.Second

	release, started, cDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	// ErrFailure runs after the tripping request's completion time was taken but before the circuit decides to open.
	hook.onErrFailure = func() {
		once.Do(func() {
			mc.Add(5 * time.Millisecond)
			// C starts now: strictly after the tripping failure finished, strictly before the circuit flips to open.
			go func() {
				cDone <- c.Execute(ctx, func(context.Context) error { close(started); <-release; return nil }, nil)
			}()
			select {
			case <-started:
			case err := <-cDone:
				t.Fatalf("C should have been admitted by the still-closed circuit and be running: %v", err)
			case <-time.After(deadlock):
				t.Fatal("timed out waiting for C to start")
			}
			mc.Add(5 * time.Millisecond)
		})
	}

	if err := c.Execute(ctx, func(context.Context) error { return errors.New("boom") }, nil); err == nil {
		t.Fatal("expected the tripping request to fail")
	}
	if !c.IsOpen() {
		t.Fatal("precondition: circuit should have opened")
	}
	select {
	case <-started:
	default:
		t.Fatal("precondition: the ErrFailure hook should have started C")
	}

	mc.Add(time.Millisecond)
	close(release)
	select {
	case err := <-cDone:
		if err != nil {
			t.Fatalf("C was admitted while closed and should succeed: %v", err)
		}
	case <-time.After(deadlock):
		t.Fatal("timed out waiting for C to finish")
	}
	if !c.IsOpen() {
		t.Fatal("a request that started before the circuit flipped to open closed it on success; SleepWindow (1h) not honored")
	}

	// Sanity: the normal recovery path still works
	mc.Add(time.Hour + time.Millisecond)
	if err := c.Execute(ctx, func(context.Context) error { return nil }, nil); err != nil {
		t.Fatalf("probe after the sleep window should run and pass: %v", err)
	}
	if c.IsOpen() {
		t.Fatal("successful probe should close the circuit")
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

// Probabilistic guard for the closed->open transition under real concurrency: hammer the circuit and count any
// "probe" granted inside a 1h SleepWindow.  TestRegression_NoProbeBeforeCloserArmedOnOpen is the deterministic
// version.
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

// reenteringCloser wraps the real hystrix Closer.  From inside Opened -- i.e. while the circuit is transitioning to
// open, after its state already reads open but before the wrapped Closer has been armed for this open event -- it
// re-enters the circuit with one request, which is exactly the position a concurrent request is in when it observes
// the freshly flipped state.  It records what that request saw and counts every Allow()==true.
type reenteringCloser struct {
	circuit.OpenToClosed
	c *circuit.Circuit

	allowed    atomic.Int64
	reentered  atomic.Int64
	innerRan   atomic.Int64
	openInside atomic.Bool
	innerErr   atomic.Pointer[error]
}

func (r *reenteringCloser) Allow(ctx context.Context, now time.Time) bool {
	ok := r.OpenToClosed.Allow(ctx, now)
	if ok {
		r.allowed.Add(1)
	}
	return ok
}

func (r *reenteringCloser) Opened(ctx context.Context, now time.Time) {
	if r.reentered.Add(1) == 1 {
		r.openInside.Store(r.c.IsOpen())
		err := r.c.Execute(ctx, func(context.Context) error { r.innerRan.Add(1); return nil }, nil)
		r.innerErr.Store(&err)
	}
	r.OpenToClosed.Opened(ctx, now)
}

// Deterministic version of TestRegression_NoProbeLeakOnOpenTransition: a request that arrives after the circuit
// flipped to open but before the Closer heard Opened() must be short-circuited, not handed a half-open probe (which,
// on success, would close the circuit again immediately and void the SleepWindow).
func TestRegression_NoProbeBeforeCloserArmedOnOpen(t *testing.T) {
	mc := &clock.MockClock{}
	mc.Set(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	rc := &reenteringCloser{}
	c := circuit.NewCircuitFromConfig("reenter-on-open", circuit.Config{
		General: circuit.GeneralConfig{
			TimeKeeper: circuit.TimeKeeper{Now: mc.Now, AfterFunc: mc.AfterFunc},
			OpenToClosedFactory: func() circuit.OpenToClosed {
				rc.OpenToClosed = CloserFactory(ConfigureCloser{SleepWindow: time.Hour, AfterFunc: mc.AfterFunc})()
				return rc
			},
			ClosedToOpenFactory: OpenerFactory(ConfigureOpener{RequestVolumeThreshold: 1, Now: mc.Now}),
		},
		Execution: circuit.ExecutionConfig{Timeout: -1, MaxConcurrentRequests: -1},
	})
	rc.c = c
	ctx := context.Background()

	if err := c.Execute(ctx, func(context.Context) error { return errors.New("boom") }, nil); err == nil {
		t.Fatal("expected the tripping request to fail")
	}
	if got := rc.reentered.Load(); got != 1 {
		t.Fatalf("expected the closer to be told Opened exactly once, got %d", got)
	}
	if !rc.openInside.Load() {
		t.Fatal("precondition: the circuit should already read open from inside the open transition")
	}
	if ran := rc.innerRan.Load(); ran != 0 {
		t.Fatalf("a request re-entering during the open transition ran runFunc %d time(s); it must be short-circuited", ran)
	}
	if errp := rc.innerErr.Load(); errp == nil || *errp == nil {
		t.Fatal("the re-entering request should have been rejected with a short-circuit error")
	}
	if n := rc.allowed.Load(); n != 0 {
		t.Fatalf("Allow returned true %d time(s) before the Closer was armed for this open event (SleepWindow is 1h)", n)
	}
	if !c.IsOpen() {
		t.Fatal("circuit must still be open: nothing may close it inside its own open transition")
	}

	// Normal recovery afterwards
	mc.Add(time.Hour + time.Millisecond)
	if err := c.Execute(ctx, func(context.Context) error { return nil }, nil); err != nil {
		t.Fatalf("probe after the sleep window should run and pass: %v", err)
	}
	if c.IsOpen() {
		t.Fatal("successful probe should close the circuit")
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
