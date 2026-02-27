package circuit

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"testing/quick"
	"time"

	"github.com/cep21/circuit/v4/faststats"
	"github.com/cep21/circuit/v4/internal/clock"
)

// countingRunMetrics records every RunMetrics callback. Used to verify the
// "exactly one callback per Execute" contract stated at metrics.go (RunMetrics
// interface doc).
type countingRunMetrics struct {
	success                   faststats.AtomicInt64
	errFailure                faststats.AtomicInt64
	errTimeout                faststats.AtomicInt64
	errBadRequest             faststats.AtomicInt64
	errInterrupt              faststats.AtomicInt64
	errConcurrencyLimitReject faststats.AtomicInt64
	errShortCircuit           faststats.AtomicInt64
}

var _ RunMetrics = &countingRunMetrics{}

func (m *countingRunMetrics) Success(_ context.Context, _ time.Time, _ time.Duration) {
	m.success.Add(1)
}
func (m *countingRunMetrics) ErrFailure(_ context.Context, _ time.Time, _ time.Duration) {
	m.errFailure.Add(1)
}
func (m *countingRunMetrics) ErrTimeout(_ context.Context, _ time.Time, _ time.Duration) {
	m.errTimeout.Add(1)
}
func (m *countingRunMetrics) ErrBadRequest(_ context.Context, _ time.Time, _ time.Duration) {
	m.errBadRequest.Add(1)
}
func (m *countingRunMetrics) ErrInterrupt(_ context.Context, _ time.Time, _ time.Duration) {
	m.errInterrupt.Add(1)
}
func (m *countingRunMetrics) ErrConcurrencyLimitReject(_ context.Context, _ time.Time) {
	m.errConcurrencyLimitReject.Add(1)
}
func (m *countingRunMetrics) ErrShortCircuit(_ context.Context, _ time.Time) {
	m.errShortCircuit.Add(1)
}

// total returns the sum of all callback counts. The RunMetrics contract
// guarantees exactly one callback fires per Execute call, so total() should
// equal the number of Execute calls.
func (m *countingRunMetrics) total() int64 {
	return m.success.Get() +
		m.errFailure.Get() +
		m.errTimeout.Get() +
		m.errBadRequest.Get() +
		m.errInterrupt.Get() +
		m.errConcurrencyLimitReject.Get() +
		m.errShortCircuit.Get()
}

// countingFallbackMetrics records every FallbackMetrics callback.
type countingFallbackMetrics struct {
	success                   faststats.AtomicInt64
	errFailure                faststats.AtomicInt64
	errConcurrencyLimitReject faststats.AtomicInt64
}

var _ FallbackMetrics = &countingFallbackMetrics{}

func (m *countingFallbackMetrics) Success(_ context.Context, _ time.Time, _ time.Duration) {
	m.success.Add(1)
}
func (m *countingFallbackMetrics) ErrFailure(_ context.Context, _ time.Time, _ time.Duration) {
	m.errFailure.Add(1)
}
func (m *countingFallbackMetrics) ErrConcurrencyLimitReject(_ context.Context, _ time.Time) {
	m.errConcurrencyLimitReject.Add(1)
}

func (m *countingFallbackMetrics) total() int64 {
	return m.success.Get() + m.errFailure.Get() + m.errConcurrencyLimitReject.Get()
}

// ============================================================================
// Section 1: Invariant-asserting stress tests
// These run the circuit hot under concurrency and verify global invariants
// that the existing stress tests miss.
// ============================================================================

// TestCircuit_RunMetricsExactlyOnce_Concurrent verifies the RunMetrics contract
// ("exactly one callback per Execute") holds under high concurrency with a mix
// of success/failure/bad-request/interrupt outcomes. This is the class of bug
// fixed by the CAS changes in openCircuit/close — protocol violations that
// -race does not catch.
func TestCircuit_RunMetricsExactlyOnce_Concurrent(t *testing.T) {
	rm := &countingRunMetrics{}
	tc := &transitionCounter{}
	c := NewCircuitFromConfig("RunMetricsExactlyOnce", Config{
		Execution: ExecutionConfig{
			MaxConcurrentRequests: -1,
			Timeout:               time.Hour,
		},
		Metrics: MetricsCollectors{
			Run:     []RunMetrics{rm},
			Circuit: []Metrics{tc},
		},
	})

	const goroutines = 50
	const iterations = 2000
	ctx := context.Background()

	errFail := errors.New("fail")
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				switch (id + i) % 3 {
				case 0:
					_ = c.Execute(ctx, func(context.Context) error { return nil }, nil)
				case 1:
					_ = c.Execute(ctx, func(context.Context) error { return errFail }, nil)
				case 2:
					_ = c.Execute(ctx, func(context.Context) error {
						return SimpleBadRequest{Err: errFail}
					}, nil)
				}
			}
		}(g)
	}
	wg.Wait()

	const want = int64(goroutines * iterations)
	if got := rm.total(); got != want {
		t.Errorf("RunMetrics exactly-once violated: total callbacks = %d, Execute calls = %d\n"+
			"  success=%d failure=%d timeout=%d badReq=%d interrupt=%d concReject=%d shortCircuit=%d",
			got, want,
			rm.success.Get(), rm.errFailure.Get(), rm.errTimeout.Get(),
			rm.errBadRequest.Get(), rm.errInterrupt.Get(),
			rm.errConcurrencyLimitReject.Get(), rm.errShortCircuit.Get())
	}

	// Default circuit uses neverOpens/neverCloses — no transitions expected.
	if tc.opened.Get() != 0 || tc.closed.Get() != 0 {
		t.Errorf("unexpected transitions with neverOpens/neverCloses: opened=%d closed=%d",
			tc.opened.Get(), tc.closed.Get())
	}

	if cc := c.ConcurrentCommands(); cc != 0 {
		t.Errorf("concurrentCommands counter unbalanced after all Execute returned: %d", cc)
	}
}

// TestCircuit_TransitionAlternation_Concurrent hammers OpenCircuit/CloseCircuit
// concurrently with Execute and verifies the Opened/Closed callbacks alternate
// correctly: at any point |opened - closed| ≤ 1, and opened ≥ closed (you can
// only close what was opened). This is the exact invariant bugs #4/#5 broke.
func TestCircuit_TransitionAlternation_Concurrent(t *testing.T) {
	rm := &countingRunMetrics{}
	tc := &transitionCounter{}
	c := NewCircuitFromConfig("TransitionAlternation", Config{
		Execution: ExecutionConfig{
			MaxConcurrentRequests: -1,
			Timeout:               time.Hour,
		},
		Metrics: MetricsCollectors{
			Run:     []RunMetrics{rm},
			Circuit: []Metrics{tc},
		},
	})

	ctx := context.Background()
	var running faststats.AtomicBoolean
	running.Set(true)
	var wg sync.WaitGroup

	// Goroutines that bang on Execute
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for running.Get() {
				var runFn func(context.Context) error
				if id%2 == 0 {
					runFn = func(context.Context) error { return nil }
				} else {
					runFn = func(context.Context) error { return errors.New("x") }
				}
				_ = c.Execute(ctx, runFn, nil)
			}
		}(g)
	}

	// Goroutines that toggle the circuit. They race with each other and with
	// Execute on the CAS in openCircuit/close.
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for running.Get() {
				c.OpenCircuit(ctx)
				c.CloseCircuit(ctx)
			}
		}()
	}

	time.Sleep(50 * time.Millisecond)
	running.Set(false)
	wg.Wait()

	opened := tc.opened.Get()
	closed := tc.closed.Get()

	// Circuit starts closed, so Opened must fire first; and each Closed must
	// have a matching Opened before it. Hence: closed ≤ opened ≤ closed+1.
	if closed > opened {
		t.Errorf("transition ordering violated: closed=%d > opened=%d", closed, opened)
	}
	if opened > closed+1 {
		t.Errorf("Opened() emitted without matching Closed(): opened=%d closed=%d (diff=%d, max diff is 1)",
			opened, closed, opened-closed)
	}

	if cc := c.ConcurrentCommands(); cc != 0 {
		t.Errorf("concurrentCommands unbalanced: %d", cc)
	}

	// Sanity: we expect many transitions (not 0) given 50ms of hammering;
	// if 0 the test setup is wrong.
	if opened == 0 {
		t.Logf("warning: no transitions observed in 50ms — test may be ineffective on this machine")
	}
}

// TestCircuit_FallbackMetricsExactlyOnce_Concurrent verifies the FallbackMetrics
// contract under concurrency: exactly one fallback callback per fallback invocation.
func TestCircuit_FallbackMetricsExactlyOnce_Concurrent(t *testing.T) {
	rm := &countingRunMetrics{}
	fm := &countingFallbackMetrics{}
	c := NewCircuitFromConfig("FallbackExactlyOnce", Config{
		Execution: ExecutionConfig{
			MaxConcurrentRequests: -1,
			Timeout:               time.Hour,
		},
		Fallback: FallbackConfig{
			MaxConcurrentRequests: -1,
		},
		Metrics: MetricsCollectors{
			Run:      []RunMetrics{rm},
			Fallback: []FallbackMetrics{fm},
		},
	})

	const goroutines = 30
	const iterations = 1000
	ctx := context.Background()
	errFail := errors.New("fail")

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				// Always fail runFunc to trigger fallback; vary fallback outcome.
				fallbackFails := (id+i)%3 == 0
				_ = c.Execute(ctx,
					func(context.Context) error { return errFail },
					func(context.Context, error) error {
						if fallbackFails {
							return errFail
						}
						return nil
					})
			}
		}(g)
	}
	wg.Wait()

	const want = int64(goroutines * iterations)

	if got := rm.total(); got != want {
		t.Errorf("RunMetrics total = %d, want %d", got, want)
	}

	// Every Execute failed the runFunc (no bad requests, no short circuits
	// since neverOpens). So every one should hit the fallback path and emit
	// exactly one FallbackMetrics callback.
	if got := fm.total(); got != want {
		t.Errorf("FallbackMetrics exactly-once violated: total = %d, want %d\n"+
			"  success=%d failure=%d concReject=%d",
			got, want, fm.success.Get(), fm.errFailure.Get(), fm.errConcurrencyLimitReject.Get())
	}

	if cf := c.ConcurrentFallbacks(); cf != 0 {
		t.Errorf("concurrentFallbacks unbalanced: %d", cf)
	}
}

// ============================================================================
// Section 2: Deterministic state-machine property test
// Uses MockClock and a programmable opener/closer so the circuit is fully
// deterministic. Replays random operation sequences and checks invariants
// after every step.
// ============================================================================

// commandableOpener is a test ClosedToOpen whose ShouldOpen result is set
// externally — lets the property test drive state transitions deterministically.
type commandableOpener struct {
	neverOpens
	wantOpen faststats.AtomicBoolean
}

func (o *commandableOpener) ShouldOpen(_ context.Context, _ time.Time) bool {
	return o.wantOpen.Get()
}

// commandableCloser mirrors commandableOpener for OpenToClosed.
type commandableCloser struct {
	neverCloses
	wantClose faststats.AtomicBoolean
	allow     faststats.AtomicBoolean
}

func (c *commandableCloser) ShouldClose(_ context.Context, _ time.Time) bool {
	return c.wantClose.Get()
}
func (c *commandableCloser) Allow(_ context.Context, _ time.Time) bool {
	return c.allow.Get()
}

// circuitOp is an operation we can perform against the circuit in the property
// test. Using a dense int8 enum plays well with testing/quick's shrinking.
type circuitOp int8

const (
	opExecSuccess circuitOp = iota
	opExecFailure
	opExecBadRequest
	opExecFallbackSuccess
	opExecFallbackFailure
	opOpenCircuit
	opCloseCircuit
	opSetWantOpenTrue
	opSetWantOpenFalse
	opSetAllowTrue
	opSetAllowFalse
	opSetWantCloseTrue
	opSetWantCloseFalse
	opAdvanceClock
	numOps
)

// circuitModel is a parallel, trivially-correct model of the circuit state.
// The property test keeps the real circuit and this model in lockstep; any
// divergence is a bug.
type circuitModel struct {
	isOpen    bool
	allow     bool
	wantOpen  bool
	wantClose bool
	executeN  int64
	fallbackN int64
	openedN   int64
	closedN   int64
}

func (m *circuitModel) apply(op circuitOp) {
	switch op {
	case opExecSuccess:
		m.executeN++
		if m.isOpen && !m.allow {
			return // short-circuited, run not attempted
		}
		// Success while open can close the circuit if wantClose.
		if m.isOpen && m.wantClose {
			m.isOpen = false
			m.closedN++
		}
	case opExecFailure:
		m.executeN++
		if m.isOpen && !m.allow {
			return
		}
		// Failure while closed can open the circuit if wantOpen.
		if !m.isOpen && m.wantOpen {
			m.isOpen = true
			m.openedN++
		}
	case opExecBadRequest:
		m.executeN++
		// Bad requests never change circuit state and never hit fallback.
		// They DO still get short-circuited if the circuit is open though.
	case opExecFallbackSuccess, opExecFallbackFailure:
		m.executeN++
		if m.isOpen && !m.allow {
			// Short-circuited: fallback IS called on short-circuit.
			m.fallbackN++
			return
		}
		// runFunc fails → fallback fires.
		m.fallbackN++
		// Failure may also open the circuit.
		if !m.isOpen && m.wantOpen {
			m.isOpen = true
			m.openedN++
		}
	case opOpenCircuit:
		if !m.isOpen {
			m.isOpen = true
			m.openedN++
		}
	case opCloseCircuit:
		if m.isOpen {
			m.isOpen = false
			m.closedN++
		}
	case opSetWantOpenTrue:
		m.wantOpen = true
	case opSetWantOpenFalse:
		m.wantOpen = false
	case opSetAllowTrue:
		m.allow = true
	case opSetAllowFalse:
		m.allow = false
	case opSetWantCloseTrue:
		m.wantClose = true
	case opSetWantCloseFalse:
		m.wantClose = false
	case opAdvanceClock:
		// no state change in model
	}
}

// checkInvariants returns a non-empty error string if any invariant is
// violated. These are the properties that should hold at every step.
func checkInvariants(
	rm *countingRunMetrics, fm *countingFallbackMetrics, tc *transitionCounter,
	c *Circuit, m *circuitModel, step int, op circuitOp,
) string {
	// 1. Exactly one RunMetrics callback per Execute.
	if got, want := rm.total(), m.executeN; got != want {
		return reportf(step, op,
			"RunMetrics total=%d ≠ executeN=%d (success=%d fail=%d timeout=%d badReq=%d int=%d concRej=%d short=%d)",
			got, want,
			rm.success.Get(), rm.errFailure.Get(), rm.errTimeout.Get(),
			rm.errBadRequest.Get(), rm.errInterrupt.Get(),
			rm.errConcurrencyLimitReject.Get(), rm.errShortCircuit.Get())
	}

	// 2. At most one FallbackMetrics callback per fallback invocation.
	if got, want := fm.total(), m.fallbackN; got != want {
		return reportf(step, op, "FallbackMetrics total=%d ≠ model fallbackN=%d", got, want)
	}

	// 3. Opened/Closed counts match the model exactly (deterministic).
	if tc.opened.Get() != m.openedN {
		return reportf(step, op, "Opened count=%d ≠ model=%d", tc.opened.Get(), m.openedN)
	}
	if tc.closed.Get() != m.closedN {
		return reportf(step, op, "Closed count=%d ≠ model=%d", tc.closed.Get(), m.closedN)
	}

	// 4. Transition alternation: never more Closed than Opened, and the gap
	//    is at most 1.
	if m.closedN > m.openedN {
		return reportf(step, op, "alternation violated: closed=%d > opened=%d", m.closedN, m.openedN)
	}
	if m.openedN > m.closedN+1 {
		return reportf(step, op, "alternation violated: opened=%d > closed+1=%d", m.openedN, m.closedN+1)
	}

	// 5. IsOpen matches model.
	if c.IsOpen() != m.isOpen {
		return reportf(step, op, "IsOpen()=%v ≠ model.isOpen=%v", c.IsOpen(), m.isOpen)
	}

	// 6. Concurrent counters return to 0 after serial execution.
	if cc := c.ConcurrentCommands(); cc != 0 {
		return reportf(step, op, "ConcurrentCommands=%d after serial Execute", cc)
	}
	if cf := c.ConcurrentFallbacks(); cf != 0 {
		return reportf(step, op, "ConcurrentFallbacks=%d after serial Execute", cf)
	}

	return ""
}

func reportf(step int, op circuitOp, format string, args ...interface{}) string {
	all := make([]interface{}, 0, len(args)+2)
	all = append(all, step, opName(op))
	all = append(all, args...)
	return fmt.Sprintf("[step %d op=%s] "+format, all...)
}

func opName(op circuitOp) string {
	names := [...]string{
		"ExecSuccess", "ExecFailure", "ExecBadRequest",
		"ExecFallbackSuccess", "ExecFallbackFailure",
		"OpenCircuit", "CloseCircuit",
		"WantOpen=true", "WantOpen=false",
		"Allow=true", "Allow=false",
		"WantClose=true", "WantClose=false",
		"AdvanceClock",
	}
	if int(op) >= 0 && int(op) < len(names) {
		return names[op]
	}
	return "?"
}

// replayOps builds a fresh circuit + model and replays the op sequence,
// checking invariants after every step. Returns (failure msg, false) on
// violation, ("", true) on success.
func replayOps(ops []circuitOp) (string, bool) {
	mc := &clock.MockClock{}
	mc.Set(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))

	opener := &commandableOpener{}
	closer := &commandableCloser{}

	rm := &countingRunMetrics{}
	fm := &countingFallbackMetrics{}
	tc := &transitionCounter{}

	c := NewCircuitFromConfig("prop", Config{
		General: GeneralConfig{
			TimeKeeper: TimeKeeper{
				Now:       mc.Now,
				AfterFunc: mc.AfterFunc,
			},
			ClosedToOpenFactory: func() ClosedToOpen { return opener },
			OpenToClosedFactory: func() OpenToClosed { return closer },
		},
		Execution: ExecutionConfig{
			MaxConcurrentRequests: -1,
			Timeout:               time.Hour,
		},
		Fallback: FallbackConfig{
			MaxConcurrentRequests: -1,
		},
		Metrics: MetricsCollectors{
			Run:      []RunMetrics{rm},
			Fallback: []FallbackMetrics{fm},
			Circuit:  []Metrics{tc},
		},
	})

	model := &circuitModel{}
	ctx := context.Background()
	errFail := errors.New("fail")

	for i, op := range ops {
		switch op {
		case opExecSuccess:
			_ = c.Execute(ctx, func(context.Context) error { return nil }, nil)
		case opExecFailure:
			_ = c.Execute(ctx, func(context.Context) error { return errFail }, nil)
		case opExecBadRequest:
			_ = c.Execute(ctx, func(context.Context) error {
				return SimpleBadRequest{Err: errFail}
			}, nil)
		case opExecFallbackSuccess:
			_ = c.Execute(ctx,
				func(context.Context) error { return errFail },
				func(context.Context, error) error { return nil })
		case opExecFallbackFailure:
			_ = c.Execute(ctx,
				func(context.Context) error { return errFail },
				func(context.Context, error) error { return errFail })
		case opOpenCircuit:
			c.OpenCircuit(ctx)
		case opCloseCircuit:
			c.CloseCircuit(ctx)
		case opSetWantOpenTrue:
			opener.wantOpen.Set(true)
		case opSetWantOpenFalse:
			opener.wantOpen.Set(false)
		case opSetAllowTrue:
			closer.allow.Set(true)
		case opSetAllowFalse:
			closer.allow.Set(false)
		case opSetWantCloseTrue:
			closer.wantClose.Set(true)
		case opSetWantCloseFalse:
			closer.wantClose.Set(false)
		case opAdvanceClock:
			mc.Add(time.Millisecond)
		}

		model.apply(op)

		if msg := checkInvariants(rm, fm, tc, c, model, i, op); msg != "" {
			return msg, false
		}
	}
	return "", true
}

// TestCircuit_StateMachineProperty replays random sequences of operations
// against both the real Circuit and a trivial reference model, asserting they
// agree at every step. Fully deterministic (MockClock, single goroutine) — any
// failure is reproducible from the printed op list.
func TestCircuit_StateMachineProperty(t *testing.T) {
	prop := func(raw []uint8) bool {
		if len(raw) == 0 {
			return true
		}
		ops := make([]circuitOp, len(raw))
		for i, b := range raw {
			// b%numOps is 0..13, safely fits int8.
			ops[i] = circuitOp(b % uint8(numOps)) //nolint:gosec // bounded modulo result
		}
		msg, ok := replayOps(ops)
		if !ok {
			t.Logf("FAIL: %s\nops (len=%d): %v", msg, len(ops), opNames(ops))
		}
		return ok
	}

	cfg := &quick.Config{
		MaxCount: 500,
		Rand:     rand.New(rand.NewSource(0x5EED)),
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Fatal(err)
	}
}

// TestCircuit_StateMachineProperty_Handwritten runs a few curated sequences
// that encode past bug reproductions. Keeps them green without relying solely
// on random search to rediscover them.
func TestCircuit_StateMachineProperty_Handwritten(t *testing.T) {
	cases := []struct {
		name string
		ops  []circuitOp
	}{
		{
			name: "open-then-execute-shortcircuits",
			ops:  []circuitOp{opOpenCircuit, opExecSuccess, opExecFailure},
		},
		{
			name: "wantOpen-failure-opens",
			ops:  []circuitOp{opSetWantOpenTrue, opExecFailure, opExecSuccess},
		},
		{
			name: "half-open-success-closes",
			ops: []circuitOp{
				opOpenCircuit, opSetAllowTrue, opSetWantCloseTrue,
				opExecSuccess, opExecSuccess,
			},
		},
		{
			name: "repeated-OpenCircuit-is-idempotent",
			ops: []circuitOp{
				opOpenCircuit, opOpenCircuit, opOpenCircuit,
				opCloseCircuit, opCloseCircuit,
			},
		},
		{
			name: "fallback-fires-on-short-circuit",
			ops: []circuitOp{
				opOpenCircuit, opExecFallbackSuccess, opExecFallbackFailure,
			},
		},
		{
			name: "bad-request-never-opens",
			ops: []circuitOp{
				opSetWantOpenTrue,
				opExecBadRequest, opExecBadRequest, opExecBadRequest,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if msg, ok := replayOps(tc.ops); !ok {
				t.Fatalf("%s\nops: %v", msg, opNames(tc.ops))
			}
		})
	}
}

func opNames(ops []circuitOp) []string {
	names := make([]string, len(ops))
	for i, op := range ops {
		names[i] = opName(op)
	}
	return names
}
