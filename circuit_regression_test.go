package circuit

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type regTimeoutCounter struct {
	timeouts atomic.Int64
	success  atomic.Int64
}

func (a *regTimeoutCounter) Success(context.Context, time.Time, time.Duration)       { a.success.Add(1) }
func (a *regTimeoutCounter) ErrFailure(context.Context, time.Time, time.Duration)    {}
func (a *regTimeoutCounter) ErrTimeout(context.Context, time.Time, time.Duration)    { a.timeouts.Add(1) }
func (a *regTimeoutCounter) ErrBadRequest(context.Context, time.Time, time.Duration) {}
func (a *regTimeoutCounter) ErrInterrupt(context.Context, time.Time, time.Duration)  {}
func (a *regTimeoutCounter) ErrConcurrencyLimitReject(context.Context, time.Time)    {}
func (a *regTimeoutCounter) ErrShortCircuit(context.Context, time.Time)              {}

// ExecutionTimeout used to be loaded twice in run(); a SetConfigThreadSafe between the loads produced a deadline
// in the past and a spurious ErrTimeout for an instant, successful runFunc.
func TestRegression_TimeoutReadOnceUnderConfigChange(t *testing.T) {
	var m regTimeoutCounter
	c := NewCircuitFromConfig("t", Config{
		Execution: ExecutionConfig{Timeout: time.Hour, MaxConcurrentRequests: -1},
		Metrics:   MetricsCollectors{Run: []RunMetrics{&m}},
	})
	noTimeout := c.Config()
	noTimeout.Execution.Timeout = -1
	withTimeout := c.Config()
	withTimeout.Execution.Timeout = time.Hour

	ctx := context.Background()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			c.SetConfigThreadSafe(noTimeout)
			c.SetConfigThreadSafe(withTimeout)
		}
	}()
	const workers, iters = 4, 20000
	var runners sync.WaitGroup
	for g := 0; g < workers; g++ {
		runners.Add(1)
		go func() {
			defer runners.Done()
			for i := 0; i < iters; i++ {
				_ = c.Execute(ctx, func(context.Context) error { return nil }, nil)
			}
		}()
	}
	runners.Wait()
	close(stop)
	wg.Wait()
	if got := m.timeouts.Load(); got > 0 {
		t.Fatalf("instant runFunc with a 1h (or disabled) timeout saw %d spurious ErrTimeout", got)
	}
}

type regAlwaysAllowCloser struct{ neverCloses }

func (regAlwaysAllowCloser) Allow(context.Context, time.Time) bool       { return true }
func (regAlwaysAllowCloser) ShouldClose(context.Context, time.Time) bool { return true }

// ForceOpen means "reject everything"; it must not consult the closer for half-open probes.
func TestRegression_ForceOpenNeverRuns(t *testing.T) {
	c := NewCircuitFromConfig("fo", Config{General: GeneralConfig{
		ForceOpen:           true,
		OpenToClosedFactory: func() OpenToClosed { return regAlwaysAllowCloser{} },
	}})
	var ran int64
	for i := 0; i < 10; i++ {
		err := c.Execute(context.Background(), func(context.Context) error { atomic.AddInt64(&ran, 1); return nil }, nil)
		var ce Error
		if !errors.As(err, &ce) || !ce.CircuitOpen() {
			t.Fatalf("expected circuit-open error, got %v", err)
		}
	}
	if ran != 0 {
		t.Fatalf("ForceOpen circuit executed runFunc %d/10 times", ran)
	}
}

func TestRegression_NilRunFuncNeverPanics(t *testing.T) {
	ctx := context.Background()
	if err := NewCircuitFromConfig("n", Config{}).Run(ctx, nil); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if err := NewCircuitFromConfig("d", Config{General: GeneralConfig{Disabled: true}}).Run(ctx, nil); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	var nilC *Circuit
	if err := nilC.Run(ctx, nil); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if err := nilC.Go(ctx, nil, nil); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if err := (&Circuit{}).Run(ctx, nil); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

type regTransitionCounter struct{ opened, closed atomic.Int64 }

func (a *regTransitionCounter) Opened(context.Context, time.Time) { a.opened.Add(1) }
func (a *regTransitionCounter) Closed(context.Context, time.Time) { a.closed.Add(1) }

// Manual CloseCircuit()/OpenCircuit() and automatic closes act on the underlying state even while a Force* override
// is set, so the state does not spring back when the override is cleared.
func TestRegression_ManualTransitionsUnderForceFlags(t *testing.T) {
	ctx := context.Background()
	t.Run("CloseCircuit while ForcedClosed", func(t *testing.T) {
		var tc regTransitionCounter
		c := NewCircuitFromConfig("fc", Config{Metrics: MetricsCollectors{Circuit: []Metrics{&tc}}})
		c.OpenCircuit(ctx)
		cfg := c.Config()
		cfg.General.ForcedClosed = true
		c.SetConfigThreadSafe(cfg)
		c.CloseCircuit(ctx)
		cfg.General.ForcedClosed = false
		c.SetConfigThreadSafe(cfg)
		if c.IsOpen() {
			t.Fatalf("CloseCircuit() ignored while ForcedClosed; Closed() emitted %d times", tc.closed.Load())
		}
		if tc.opened.Load() != 1 || tc.closed.Load() != 1 {
			t.Fatalf("expected exactly one Opened and one Closed, got %d/%d", tc.opened.Load(), tc.closed.Load())
		}
	})
	t.Run("successes while ForcedClosed close the circuit", func(t *testing.T) {
		c := NewCircuitFromConfig("fc2", Config{General: GeneralConfig{
			OpenToClosedFactory: func() OpenToClosed { return regAlwaysAllowCloser{} },
		}})
		c.OpenCircuit(ctx)
		cfg := c.Config()
		cfg.General.ForcedClosed = true
		c.SetConfigThreadSafe(cfg)
		for i := 0; i < 10; i++ {
			if err := c.Run(ctx, func(context.Context) error { return nil }); err != nil {
				t.Fatal(err)
			}
		}
		cfg.General.ForcedClosed = false
		c.SetConfigThreadSafe(cfg)
		if c.IsOpen() {
			t.Fatal("successes with a ShouldClose()==true closer while ForcedClosed did not close the circuit")
		}
	})
	t.Run("OpenCircuit while ForceOpen", func(t *testing.T) {
		c := NewCircuitFromConfig("fo2", Config{General: GeneralConfig{ForceOpen: true}})
		c.OpenCircuit(ctx)
		cfg := c.Config()
		cfg.General.ForceOpen = false
		c.SetConfigThreadSafe(cfg)
		if !c.IsOpen() {
			t.Fatal("OpenCircuit() ignored while ForceOpen")
		}
	})
	t.Run("CloseCircuit while ForceOpen", func(t *testing.T) {
		c := NewCircuitFromConfig("fo3", Config{})
		c.OpenCircuit(ctx)
		cfg := c.Config()
		cfg.General.ForceOpen = true
		c.SetConfigThreadSafe(cfg)
		if !c.IsOpen() {
			t.Fatal("ForceOpen should read as open")
		}
		c.CloseCircuit(ctx)
		if !c.IsOpen() {
			t.Fatal("ForceOpen should still read as open")
		}
		cfg.General.ForceOpen = false
		c.SetConfigThreadSafe(cfg)
		if c.IsOpen() {
			t.Fatal("CloseCircuit() ignored while ForceOpen")
		}
	})
}

type regFlappyOpener struct{ neverOpens }

func (regFlappyOpener) ShouldOpen(context.Context, time.Time) bool { return true }

type regOrderedEvents struct {
	mu  sync.Mutex
	seq []byte
}

func (a *regOrderedEvents) Opened(context.Context, time.Time) {
	a.mu.Lock()
	a.seq = append(a.seq, 'O')
	a.mu.Unlock()
}
func (a *regOrderedEvents) Closed(context.Context, time.Time) {
	a.mu.Lock()
	a.seq = append(a.seq, 'C')
	a.mu.Unlock()
}

// Opened()/Closed() notifications must alternate in delivery order and end on the circuit's actual state, even
// when many goroutines flap the circuit concurrently.
func TestRegression_TransitionEventsDeliveredInOrder(t *testing.T) {
	const trials = 30
	for trial := 0; trial < trials; trial++ {
		ev := &regOrderedEvents{}
		c := NewCircuitFromConfig("flap", Config{
			General: GeneralConfig{
				ClosedToOpenFactory: func() ClosedToOpen { return regFlappyOpener{} },
				OpenToClosedFactory: func() OpenToClosed { return regAlwaysAllowCloser{} },
			},
			Execution: ExecutionConfig{MaxConcurrentRequests: -1, Timeout: -1},
			Metrics:   MetricsCollectors{Circuit: []Metrics{ev}},
		})
		var wg sync.WaitGroup
		bad := errors.New("bad")
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				for i := 0; i < 200; i++ {
					if (i+g)%2 == 0 {
						_ = c.Run(context.Background(), func(context.Context) error { return bad })
					} else {
						_ = c.Run(context.Background(), func(context.Context) error { return nil })
					}
				}
			}(g)
		}
		wg.Wait()
		ev.mu.Lock()
		seq := append([]byte(nil), ev.seq...)
		ev.mu.Unlock()
		for i := 1; i < len(seq); i++ {
			if seq[i] == seq[i-1] {
				t.Fatalf("trial %d: adjacent duplicate transition events at %d: ...%s", trial, i, seq[max(0, i-5):min(len(seq), i+2)])
			}
		}
		if len(seq) > 0 {
			wantLast := byte('C')
			if c.IsOpen() {
				wantLast = 'O'
			}
			if seq[len(seq)-1] != wantLast {
				t.Fatalf("trial %d: last delivered event %c but IsOpen()=%v", trial, seq[len(seq)-1], c.IsOpen())
			}
		}
	}
}

type regOneProbeCloser struct {
	neverCloses
	tokens atomic.Int64
}

func (a *regOneProbeCloser) Opened(context.Context, time.Time)           { a.tokens.Store(1) }
func (a *regOneProbeCloser) Allow(context.Context, time.Time) bool       { return a.tokens.Add(-1) >= 0 }
func (a *regOneProbeCloser) ShouldClose(context.Context, time.Time) bool { return true }

// A request that would be rejected by the concurrency limit must not spend the closer's half-open permit.
func TestRegression_HalfOpenTokenNotConsumedByThrottle(t *testing.T) {
	cl := &regOneProbeCloser{}
	c := NewCircuitFromConfig("hp", Config{
		General:   GeneralConfig{OpenToClosedFactory: func() OpenToClosed { return cl }},
		Execution: ExecutionConfig{MaxConcurrentRequests: 1, Timeout: -1},
	})
	ctx := context.Background()
	c.OpenCircuit(ctx)

	// Saturate the concurrency limit with an in-flight request
	release := make(chan struct{})
	started := make(chan struct{})
	cfg := c.Config()
	cfg.General.ForcedClosed = true
	c.SetConfigThreadSafe(cfg)
	go func() {
		_ = c.Run(ctx, func(context.Context) error { close(started); <-release; return nil })
	}()
	<-started
	cfg.General.ForcedClosed = false
	c.SetConfigThreadSafe(cfg)

	var ran int
	err := c.Run(ctx, func(context.Context) error { ran++; return nil })
	var ce Error
	if !errors.As(err, &ce) || !ce.CircuitOpen() {
		// Still classified as a short circuit (the circuit *is* open); we just must not have burned the probe permit
		t.Fatalf("expected short-circuit error, got %v", err)
	}
	close(release)
	for c.ConcurrentCommands() != 0 {
		time.Sleep(time.Millisecond)
	}
	if err = c.Run(ctx, func(context.Context) error { ran++; return nil }); err != nil {
		t.Fatalf("half-open permit was consumed by a throttled request that never ran: %v (ran=%d, open=%v)", err, ran, c.IsOpen())
	}
	if c.IsOpen() {
		t.Fatal("successful probe should have closed the circuit")
	}
}

func TestRegression_SimpleBadRequestUnwrap(t *testing.T) {
	sentinel := errors.New("sentinel")
	var err error = SimpleBadRequest{Err: sentinel}
	if !errors.Is(err, sentinel) {
		t.Fatal("errors.Is cannot see through SimpleBadRequest")
	}
	if !IsBadRequest(err) || !IsBadRequest(&SimpleBadRequest{}) {
		t.Fatal("SimpleBadRequest should be a bad request by value and pointer")
	}
}

func TestRegression_IsBadRequestClassification(t *testing.T) {
	plain := errors.New("plain")
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{plain, false},
		{errCircuitOpen, false},
		{errThrottledConcurrentCommands, false},
		{errThrottledConcurrentFallbacks, false},
		{SimpleBadRequest{}, true},
		{&SimpleBadRequest{Err: plain}, true},
		{errors.Join(plain, SimpleBadRequest{}), true},
		{errors.Join(plain, plain), false},
		{wrapErr{SimpleBadRequest{}}, true},
		{wrapErr{plain}, false},
		{asBadRequest{}, true},
	}
	for _, tc := range cases {
		if got := IsBadRequest(tc.err); got != tc.want {
			t.Errorf("IsBadRequest(%v) = %v want %v", tc.err, got, tc.want)
		}
	}
}

type wrapErr struct{ error }

func (w wrapErr) Unwrap() error { return w.error }

// asBadRequest exposes a BadRequest only through an As method (no Unwrap): errors.As honors that, so must we
type asBadRequest struct{}

func (asBadRequest) Error() string { return "as" }
func (asBadRequest) As(target interface{}) bool {
	if br, ok := target.(*BadRequest); ok {
		*br = SimpleBadRequest{}
		return true
	}
	return false
}

// Reconfiguring an open circuit with SetConfigNotThreadSafe replaces its open/close logic; the new logic must be
// told the circuit is open or it may never allow a probe.
func TestRegression_SetConfigNotThreadSafeWhileOpenStillRecovers(t *testing.T) {
	ctx := context.Background()
	cl := &regOneProbeCloser{}
	c := NewCircuitFromConfig("reconf", Config{
		General:   GeneralConfig{OpenToClosedFactory: func() OpenToClosed { return cl }},
		Execution: ExecutionConfig{Timeout: -1},
	})
	c.OpenCircuit(ctx)
	fresh := &regOneProbeCloser{}
	cfg := c.Config()
	cfg.General.OpenToClosedFactory = func() OpenToClosed { return fresh }
	c.SetConfigNotThreadSafe(cfg)
	if err := c.Run(ctx, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("freshly configured closer was never told the circuit is open; probe rejected: %v", err)
	}
	if c.IsOpen() {
		t.Fatal("expected the successful probe to close the circuit")
	}
}

// While open and saturated, rejections are still short circuits (CircuitOpen()==true), not concurrency rejects.
func TestRegression_OpenAndSaturatedIsShortCircuit(t *testing.T) {
	ctx := context.Background()
	var sc, rej atomic.Int64
	m := &regCountingRunMetrics{shortCircuit: &sc, reject: &rej}
	c := NewCircuitFromConfig("sat", Config{
		General:   GeneralConfig{OpenToClosedFactory: func() OpenToClosed { return regAlwaysAllowCloser{} }},
		Execution: ExecutionConfig{MaxConcurrentRequests: 1, Timeout: -1},
		Metrics:   MetricsCollectors{Run: []RunMetrics{m}},
	})
	release, started := make(chan struct{}), make(chan struct{})
	go func() { _ = c.Run(ctx, func(context.Context) error { close(started); <-release; return nil }) }()
	<-started
	c.OpenCircuit(ctx)
	for i := 0; i < 5; i++ {
		err := c.Run(ctx, func(context.Context) error { return nil })
		var ce Error
		if !errors.As(err, &ce) || !ce.CircuitOpen() || ce.ConcurrencyLimitReached() {
			t.Fatalf("expected circuit-open error, got %v", err)
		}
	}
	close(release)
	if sc.Load() != 5 || rej.Load() != 0 {
		t.Fatalf("expected 5 short circuits / 0 rejects, got %d / %d", sc.Load(), rej.Load())
	}
}

type regCountingRunMetrics struct {
	shortCircuit, reject *atomic.Int64
}

func (a *regCountingRunMetrics) Success(context.Context, time.Time, time.Duration)       {}
func (a *regCountingRunMetrics) ErrFailure(context.Context, time.Time, time.Duration)    {}
func (a *regCountingRunMetrics) ErrTimeout(context.Context, time.Time, time.Duration)    {}
func (a *regCountingRunMetrics) ErrBadRequest(context.Context, time.Time, time.Duration) {}
func (a *regCountingRunMetrics) ErrInterrupt(context.Context, time.Time, time.Duration)  {}
func (a *regCountingRunMetrics) ErrConcurrencyLimitReject(context.Context, time.Time) {
	a.reject.Add(1)
}
func (a *regCountingRunMetrics) ErrShortCircuit(context.Context, time.Time) { a.shortCircuit.Add(1) }

func TestRegression_CircuitErrorStrings(t *testing.T) {
	// The precomputed messages must match what the old fmt.Sprintf produced
	if got, want := errCircuitOpen.Error(), "circuit is open: concurrencyReached=false circuitOpen=true"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if got, want := errThrottledConcurrentCommands.Error(), "throttling connections to command: concurrencyReached=true circuitOpen=false"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if got, want := errThrottledConcurrentFallbacks.Error(), "throttling concurrency to fallbacks: concurrencyReached=true circuitOpen=false"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRegression_ManagerCreateCircuitReentrant(t *testing.T) {
	var m Manager
	m.DefaultCircuitProperties = append(m.DefaultCircuitProperties, func(_ string) Config {
		if parent := m.GetCircuit("parent"); parent != nil {
			return parent.Config()
		}
		_ = m.AllCircuits()
		return Config{}
	})
	done := make(chan struct{})
	go func() { _, _ = m.CreateCircuit("child"); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("CreateCircuit deadlocked when a DefaultCircuitProperties callback used the Manager")
	}
}

// DefaultCircuitProperties constructors commonly register per-name state (e.g. rolling.StatFactory); they must not
// run for a create that is doomed to fail as a duplicate.
func TestRegression_ManagerDuplicateDoesNotRunConstructors(t *testing.T) {
	var m Manager
	calls := map[string]int{}
	m.DefaultCircuitProperties = append(m.DefaultCircuitProperties, func(name string) Config {
		calls[name]++
		return Config{}
	})
	m.MustCreateCircuit("dup")
	if _, err := m.CreateCircuit("dup"); err == nil {
		t.Fatal("expected duplicate error")
	}
	if calls["dup"] != 1 {
		t.Fatalf("constructor ran %d times for one successful create", calls["dup"])
	}
}

func TestRegression_ManagerVarNil(t *testing.T) {
	var m *Manager
	if s := m.Var().String(); s != "{}" {
		t.Fatalf("unexpected: %s", s)
	}
}

type regReentrantMetrics struct {
	c      **Circuit
	events []string
}

func (r *regReentrantMetrics) Opened(ctx context.Context, _ time.Time) {
	r.events = append(r.events, "opened")
	// Re-enter the circuit from inside the notification, as a "veto" style collector might
	(*r.c).CloseCircuit(ctx)
	r.events = append(r.events, "opened-returned")
}
func (r *regReentrantMetrics) Closed(context.Context, time.Time) {
	r.events = append(r.events, "closed")
}

// A Metrics listener that calls back into the circuit must not deadlock, and still observes ordered delivery.
func TestRegression_ReentrantMetricsListener(t *testing.T) {
	var c *Circuit
	m := &regReentrantMetrics{c: &c}
	c = NewCircuitFromConfig("reentrant", Config{Metrics: MetricsCollectors{Circuit: []Metrics{m}}})
	done := make(chan struct{})
	go func() { c.OpenCircuit(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("OpenCircuit deadlocked when a Metrics.Opened listener called CloseCircuit")
	}
	if c.IsOpen() {
		t.Fatal("listener's CloseCircuit should have taken effect")
	}
	want := []string{"opened", "opened-returned", "closed"}
	if len(m.events) != len(want) {
		t.Fatalf("events %v want %v", m.events, want)
	}
	for i := range want {
		if m.events[i] != want[i] {
			t.Fatalf("events %v want %v", m.events, want)
		}
	}
}

type regPanickyMetrics struct{ calls atomic.Int64 }

func (p *regPanickyMetrics) Opened(context.Context, time.Time) {
	if p.calls.Add(1) == 1 {
		panic("listener bug")
	}
}
func (p *regPanickyMetrics) Closed(context.Context, time.Time) { p.calls.Add(1) }

// A panicking listener (recovered by the caller, as net/http would) must not stop later notifications.
func TestRegression_PanickingMetricsListenerDoesNotWedgeDelivery(t *testing.T) {
	p := &regPanickyMetrics{}
	c := NewCircuitFromConfig("panicky", Config{Metrics: MetricsCollectors{Circuit: []Metrics{p}}})
	ctx := context.Background()
	func() {
		defer func() { _ = recover() }()
		c.OpenCircuit(ctx)
	}()
	if !c.IsOpen() {
		t.Fatal("state change should stick even though a listener panicked")
	}
	c.CloseCircuit(ctx)
	c.OpenCircuit(ctx)
	if got := p.calls.Load(); got != 3 {
		t.Fatalf("expected 3 listener calls (open, close, open), got %d", got)
	}
}
