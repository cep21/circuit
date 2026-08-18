package circuit

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// regRunCounter is a RunMetrics that counts the outcomes the regression tests care about
type regRunCounter struct {
	success, timeout, shortCircuit, reject atomic.Int64
}

func (a *regRunCounter) Success(context.Context, time.Time, time.Duration)       { a.success.Add(1) }
func (a *regRunCounter) ErrFailure(context.Context, time.Time, time.Duration)    {}
func (a *regRunCounter) ErrTimeout(context.Context, time.Time, time.Duration)    { a.timeout.Add(1) }
func (a *regRunCounter) ErrBadRequest(context.Context, time.Time, time.Duration) {}
func (a *regRunCounter) ErrInterrupt(context.Context, time.Time, time.Duration)  {}
func (a *regRunCounter) ErrConcurrencyLimitReject(context.Context, time.Time)    { a.reject.Add(1) }
func (a *regRunCounter) ErrShortCircuit(context.Context, time.Time)              { a.shortCircuit.Add(1) }

// ExecutionTimeout used to be loaded twice in run(); a SetConfigThreadSafe between the loads produced a deadline
// in the past and a spurious ErrTimeout for an instant, successful runFunc.  This test is probabilistic
// (iteration-based): there is no deterministic seam between the two atomic reads to hook, so it hammers the
// window instead and can only ever false-pass, never false-fail.
func TestRegression_TimeoutReadOnceUnderConfigChange(t *testing.T) {
	var m regRunCounter
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
	if got := m.timeout.Load(); got > 0 {
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

// Manual CloseCircuit()/OpenCircuit() and automatic closes act on the underlying state even while a Force* override
// is set, so the state does not spring back when the override is cleared.
func TestRegression_ManualTransitionsUnderForceFlags(t *testing.T) {
	ctx := context.Background()
	t.Run("CloseCircuit while ForcedClosed", func(t *testing.T) {
		var tc transitionCounter
		c := NewCircuitFromConfig("fc", Config{Metrics: MetricsCollectors{Circuit: []Metrics{&tc}}})
		c.OpenCircuit(ctx)
		cfg := c.Config()
		cfg.General.ForcedClosed = true
		c.SetConfigThreadSafe(cfg)
		c.CloseCircuit(ctx)
		cfg.General.ForcedClosed = false
		c.SetConfigThreadSafe(cfg)
		if c.IsOpen() {
			t.Fatalf("CloseCircuit() ignored while ForcedClosed; Closed() emitted %d times", tc.closed.Get())
		}
		if tc.opened.Get() != 1 || tc.closed.Get() != 1 {
			t.Fatalf("expected exactly one Opened and one Closed, got %d/%d", tc.opened.Get(), tc.closed.Get())
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
	done := make(chan struct{})
	go func() {
		defer close(done)
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
	regWait(t, done, "the in-flight request to finish")
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
	m := &regRunCounter{}
	c := NewCircuitFromConfig("sat", Config{
		General:   GeneralConfig{OpenToClosedFactory: func() OpenToClosed { return regAlwaysAllowCloser{} }},
		Execution: ExecutionConfig{MaxConcurrentRequests: 1, Timeout: -1},
		Metrics:   MetricsCollectors{Run: []RunMetrics{m}},
	})
	release, started, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		_ = c.Run(ctx, func(context.Context) error { close(started); <-release; return nil })
	}()
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
	regWait(t, done, "the in-flight request to finish")
	if sc, rej := m.shortCircuit.Load(), m.reject.Load(); sc != 5 || rej != 0 {
		t.Fatalf("expected 5 short circuits / 0 rejects, got %d / %d", sc, rej)
	}
}

// compat pin: these strings are part of the de-facto contract
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

// The circuit's own OpenToClosed logic is told about each transition directly, exactly once per transition, and is
// not an element of CircuitMetricsCollector (which only holds the configured Metrics.Circuit listeners).
func TestRegression_StateMachineNotifiedOnceAndNotACollector(t *testing.T) {
	sm := newRegStreamCloser()
	c := NewCircuitFromConfig("recording-closer", Config{General: GeneralConfig{
		OpenToClosedFactory: func() OpenToClosed { return sm },
	}})
	if c.OpenToClose != OpenToClosed(sm) {
		t.Fatalf("unexpected OpenToClose %#v", c.OpenToClose)
	}
	if n := len(c.CircuitMetricsCollector); n != 0 {
		t.Fatalf("CircuitMetricsCollector should only hold configured Metrics.Circuit listeners, has %d", n)
	}
	ctx := context.Background()
	c.OpenCircuit(ctx)
	c.CloseCircuit(ctx)
	sm.expect(t, "OC")
	sm.expectNoMore(t)
}

// A panicking listener (recovered by the caller, as net/http would) must not stop later notifications.
func TestRegression_PanickingMetricsListenerDoesNotWedgeDelivery(t *testing.T) {
	p := newRegBlockingMetrics('O', nil, func() { panic("listener bug") })
	c := NewCircuitFromConfig("panicky", Config{Metrics: MetricsCollectors{Circuit: []Metrics{p}}})
	ctx := context.Background()
	func() {
		defer func() { _ = recover() }()
		c.OpenCircuit(ctx)
	}()
	if !c.IsOpen() {
		t.Fatal("state change should stick even though a listener panicked")
	}
	p.expect(t, "O")
	c.CloseCircuit(ctx)
	c.OpenCircuit(ctx)
	p.expect(t, "CO")
	p.expectNoMore(t)
}

const regDeadlockTimeout = 2 * time.Second

func regWait(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(regDeadlockTimeout):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// regEventStream is a Metrics listener that publishes each delivered event ('O' / 'C') on a channel so tests can
// wait for deliveries deterministically.
type regEventStream struct{ ch chan byte }

func newRegEventStream() *regEventStream { return &regEventStream{ch: make(chan byte, 64)} }

func (s *regEventStream) Opened(context.Context, time.Time) { s.ch <- 'O' }
func (s *regEventStream) Closed(context.Context, time.Time) { s.ch <- 'C' }

// expect waits for exactly the events in want, in order.
func (s *regEventStream) expect(t *testing.T, want string) {
	t.Helper()
	for i := 0; i < len(want); i++ {
		select {
		case got := <-s.ch:
			if got != want[i] {
				t.Fatalf("event %d: got %c, want %c (of %q)", i, got, want[i], want)
			}
		case <-time.After(regDeadlockTimeout):
			t.Fatalf("timed out waiting for event %d (%c) of %q", i, want[i], want)
		}
	}
}

// expectNoMore asserts nothing further has been delivered *yet*.  Only sound when the test has arranged that no
// goroutine can currently be delivering.
func (s *regEventStream) expectNoMore(t *testing.T) {
	t.Helper()
	select {
	case got := <-s.ch:
		t.Fatalf("unexpected extra event %c", got)
	default:
	}
}

// regBlockingMetrics records events like regEventStream, and the FIRST time it sees blockOn ('O' or 'C') it
// closes entered, waits for release (if non-nil) and then runs then (if non-nil; e.g. panic or runtime.Goexit).
type regBlockingMetrics struct {
	*regEventStream
	blockOn byte
	entered chan struct{}
	release chan struct{}
	then    func()
	once    sync.Once
}

func newRegBlockingMetrics(blockOn byte, release chan struct{}, then func()) *regBlockingMetrics {
	return &regBlockingMetrics{
		regEventStream: newRegEventStream(),
		blockOn:        blockOn,
		entered:        make(chan struct{}),
		release:        release,
		then:           then,
	}
}

func (b *regBlockingMetrics) Opened(ctx context.Context, now time.Time) {
	b.regEventStream.Opened(ctx, now)
	b.hook('O')
}

func (b *regBlockingMetrics) Closed(ctx context.Context, now time.Time) {
	b.regEventStream.Closed(ctx, now)
	b.hook('C')
}

func (b *regBlockingMetrics) hook(ev byte) {
	if ev != b.blockOn {
		return
	}
	first := false
	b.once.Do(func() { first = true })
	if !first {
		return
	}
	close(b.entered)
	if b.release != nil {
		<-b.release
	}
	if b.then != nil {
		b.then()
	}
}

// regStreamCloser is an OpenToClosed stand-in that records the Opened/Closed calls the circuit makes to its own
// state machine.
type regStreamCloser struct {
	neverCloses
	*regEventStream
}

func newRegStreamCloser() *regStreamCloser {
	return &regStreamCloser{regEventStream: newRegEventStream()}
}

func (r *regStreamCloser) Opened(ctx context.Context, now time.Time) {
	r.regEventStream.Opened(ctx, now)
}
func (r *regStreamCloser) Closed(ctx context.Context, now time.Time) {
	r.regEventStream.Closed(ctx, now)
}

// A user listener that blocks and then panics (recovered by its caller) while another goroutine's transition is
// queued behind it must not strand that queued notification: both the state machine and the other user listeners
// still hear about the later Opened.
func TestRegression_PanickingListenerDoesNotStrandQueuedTransition(t *testing.T) {
	ctx := context.Background()
	sm := newRegStreamCloser()
	good := newRegEventStream()
	bad := newRegBlockingMetrics('C', make(chan struct{}), func() { panic("listener bug") })
	c := NewCircuitFromConfig("strand", Config{
		General: GeneralConfig{OpenToClosedFactory: func() OpenToClosed { return sm }},
		Metrics: MetricsCollectors{Circuit: []Metrics{good, bad}},
	})

	c.OpenCircuit(ctx)
	sm.expect(t, "O")
	good.expect(t, "O")
	bad.expect(t, "O")

	aDone := make(chan interface{}, 1)
	go func() {
		defer func() { aDone <- recover() }()
		c.CloseCircuit(ctx)
	}()
	regWait(t, bad.entered, "goroutine A to be inside the blocking Closed listener")
	sm.expect(t, "C")
	good.expect(t, "C")
	bad.expect(t, "C")

	// A holds the deliverer role, parked in bad.Closed.  This open flips the state, tells the state machine inline
	// and queues its Opened for user listeners behind A.
	opened := make(chan struct{})
	go func() { c.OpenCircuit(ctx); close(opened) }()
	regWait(t, opened, "OpenCircuit to return while another goroutine's listener is blocked")
	if !c.IsOpen() {
		t.Fatal("expected open")
	}
	sm.expect(t, "O")    // the state machine was told synchronously, at the moment of the flip ...
	good.expectNoMore(t) // ... while user listeners are still queued behind A

	close(bad.release) // A's listener now panics; A recovers it
	select {
	case r := <-aDone:
		if r == nil {
			t.Fatal("expected goroutine A to observe (and recover) the listener panic")
		}
	case <-time.After(regDeadlockTimeout):
		t.Fatal("goroutine A never returned")
	}
	// The Opened that was queued behind the dead delivery loop must still be delivered.
	good.expect(t, "O")
	bad.expect(t, "O")
	good.expectNoMore(t)
	sm.expectNoMore(t)
}

// A listener that calls runtime.Goexit kills the delivering goroutine; the deliverer role must still be released
// so later transitions are delivered.
func TestRegression_GoexitListenerDoesNotWedgeDelivery(t *testing.T) {
	ctx := context.Background()
	good := newRegEventStream()
	bad := newRegBlockingMetrics('O', nil, runtime.Goexit)
	c := NewCircuitFromConfig("goexit", Config{Metrics: MetricsCollectors{Circuit: []Metrics{good, bad}}})

	aExited := make(chan struct{})
	go func() {
		defer close(aExited) // deferred calls still run on Goexit
		c.OpenCircuit(ctx)
		t.Error("OpenCircuit returned; expected the listener's Goexit to end this goroutine")
	}()
	regWait(t, aExited, "goroutine A to exit via the listener's runtime.Goexit")
	if !c.IsOpen() {
		t.Fatal("state change should stick even though a listener called Goexit")
	}
	good.expect(t, "O")
	bad.expect(t, "O")

	closed := make(chan struct{})
	go func() { c.CloseCircuit(ctx); c.OpenCircuit(ctx); close(closed) }()
	regWait(t, closed, "CloseCircuit/OpenCircuit after a Goexit listener")
	good.expect(t, "CO")
	bad.expect(t, "CO")
	good.expectNoMore(t)
	if !c.IsOpen() {
		t.Fatal("expected open")
	}
}

// A slow user listener on one goroutine must not delay another goroutine's transition, nor the circuit telling
// its own open/close logic about it.
func TestRegression_SlowListenerDoesNotDelayStateMachines(t *testing.T) {
	ctx := context.Background()
	sm := newRegStreamCloser()
	user := newRegBlockingMetrics('O', make(chan struct{}), nil)
	c := NewCircuitFromConfig("slow", Config{
		General: GeneralConfig{OpenToClosedFactory: func() OpenToClosed { return sm }},
		Metrics: MetricsCollectors{Circuit: []Metrics{user}},
	})

	aDone := make(chan struct{})
	go func() { defer close(aDone); c.OpenCircuit(ctx) }()
	regWait(t, user.entered, "goroutine A to be inside the slow Opened listener")
	sm.expect(t, "O")
	user.expect(t, "O")

	closeReturned := make(chan struct{})
	go func() { c.CloseCircuit(ctx); close(closeReturned) }()
	regWait(t, closeReturned, "CloseCircuit to return while another goroutine's Opened listener is still running")
	if c.IsOpen() {
		t.Fatal("CloseCircuit returned but the circuit still reads open")
	}
	sm.expect(t, "C")    // told synchronously inside CloseCircuit
	user.expectNoMore(t) // Closed is queued behind A, which is still inside Opened
	sm.expectNoMore(t)

	close(user.release)
	regWait(t, aDone, "goroutine A to finish delivering")
	user.expect(t, "C")
	user.expectNoMore(t)
	sm.expectNoMore(t)
}

// regFlapOnceOpener's first ShouldOpen (attemptToOpen's unlocked pre-filter) deterministically simulates another
// goroutine opening and closing the circuit before the locked re-ask, then says "open"; every later call says no.
// Real openers must not re-enter the circuit; this one only does so from the unlocked call.
type regFlapOnceOpener struct {
	neverOpens
	c     *Circuit
	calls atomic.Int32
}

func (o *regFlapOnceOpener) ShouldOpen(ctx context.Context, _ time.Time) bool {
	if o.calls.Add(1) == 1 {
		o.c.OpenCircuit(ctx)
		o.c.CloseCircuit(ctx)
		return true
	}
	return false
}

// The automatic open path must re-ask ShouldOpen under the transition lock: the unlocked answer is stale if the
// circuit opened and closed in between.
func TestRegression_StaleShouldOpenRecheckedUnderLock(t *testing.T) {
	ctx := context.Background()
	opener := &regFlapOnceOpener{}
	user := newRegEventStream()
	c := NewCircuitFromConfig("stale-open", Config{
		General:   GeneralConfig{ClosedToOpenFactory: func() ClosedToOpen { return opener }},
		Execution: ExecutionConfig{Timeout: -1, MaxConcurrentRequests: -1},
		Metrics:   MetricsCollectors{Circuit: []Metrics{user}},
	})
	opener.c = c

	if err := c.Run(ctx, func(context.Context) error { return errors.New("boom") }); err == nil {
		t.Fatal("expected the failure to be returned")
	}
	user.expect(t, "OC")
	user.expectNoMore(t) // without the locked re-ask this would be O,C,O
	if c.IsOpen() {
		t.Fatal("stale ShouldOpen answer opened the circuit")
	}
	if got := opener.calls.Load(); got != 2 {
		t.Fatalf("ShouldOpen asked %d times, want 2 (unlocked pre-filter + locked re-ask)", got)
	}
}

// regFlapOnceCloser admits every probe; its first ShouldClose (checkSuccess's unlocked pre-check) simulates
// another goroutine closing and re-opening the circuit before the locked re-ask, then says "close"; every later
// call says no.
type regFlapOnceCloser struct {
	neverCloses
	c     *Circuit
	calls atomic.Int32
}

func (o *regFlapOnceCloser) Allow(context.Context, time.Time) bool { return true }

func (o *regFlapOnceCloser) ShouldClose(ctx context.Context, _ time.Time) bool {
	if o.calls.Add(1) == 1 {
		o.c.CloseCircuit(ctx)
		o.c.OpenCircuit(ctx)
		return true
	}
	return false
}

// The automatic close path must re-ask ShouldClose under the transition lock: the unlocked answer is stale if the
// circuit closed and re-opened in between.
func TestRegression_StaleShouldCloseRecheckedUnderLock(t *testing.T) {
	ctx := context.Background()
	closer := &regFlapOnceCloser{}
	user := newRegEventStream()
	c := NewCircuitFromConfig("stale-close", Config{
		General:   GeneralConfig{OpenToClosedFactory: func() OpenToClosed { return closer }},
		Execution: ExecutionConfig{Timeout: -1, MaxConcurrentRequests: -1},
		Metrics:   MetricsCollectors{Circuit: []Metrics{user}},
	})
	closer.c = c

	c.OpenCircuit(ctx)
	user.expect(t, "O")
	if err := c.Run(ctx, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("half-open probe should have run and passed: %v", err)
	}
	user.expect(t, "CO")
	user.expectNoMore(t) // without the locked re-ask this would be O,C,O,C
	if !c.IsOpen() {
		t.Fatal("stale ShouldClose answer closed the circuit")
	}
	if got := closer.calls.Load(); got != 2 {
		t.Fatalf("ShouldClose asked %d times, want 2 (unlocked pre-check + locked re-ask)", got)
	}
}
