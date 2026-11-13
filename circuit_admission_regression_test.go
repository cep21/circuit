package circuit

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cep21/circuit/v4/faststats"
	"github.com/cep21/circuit/v4/internal/clock"
	"github.com/stretchr/testify/require"
)

// parkingRejects parks the first ErrConcurrencyLimitReject callback until released
type parkingRejects struct {
	countingRunMetrics
	entered, release chan struct{}
}

func (p *parkingRejects) ErrConcurrencyLimitReject(ctx context.Context, now time.Time) {
	p.countingRunMetrics.ErrConcurrencyLimitReject(ctx, now)
	if p.errConcurrencyLimitReject.Get() == 1 {
		close(p.entered)
		<-p.release
	}
}

// A rejected request is not running, so it must not occupy a slot while it reports its rejection: otherwise, under
// overload, the rejections themselves keep the limit saturated and starve requests while nothing is running.
func TestRejectedRunDoesNotHoldConcurrencySlot(t *testing.T) {
	ctx := context.Background()
	m := &parkingRejects{entered: make(chan struct{}), release: make(chan struct{})}
	c := NewCircuitFromConfig("throttle", Config{
		Execution: ExecutionConfig{MaxConcurrentRequests: 1, Timeout: -1},
		Metrics:   MetricsCollectors{Run: []RunMetrics{m}},
	})
	started, finish, aDone, bDone := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(aDone)
		_ = c.Run(ctx, func(context.Context) error { close(started); <-finish; return nil })
	}()
	regWait(t, started, "A to start")
	go func() {
		defer close(bDone)
		_ = c.Run(ctx, func(context.Context) error { return nil })
	}()
	regWait(t, m.entered, "B to be rejected")
	defer func() { close(m.release); regWait(t, bDone, "B to return") }()
	running := c.ConcurrentCommands()
	close(finish)
	regWait(t, aDone, "A to finish")

	require.NoError(t, c.Run(ctx, func(context.Context) error { return nil }), "rejected although nothing is running")
	require.Equal(t, int64(1), running, "only A was running")
	require.Zero(t, c.ConcurrentCommands())
}

func TestRejectedFallbackDoesNotHoldConcurrencySlot(t *testing.T) {
	ctx := context.Background()
	m := &parkingRejects{entered: make(chan struct{}), release: make(chan struct{})}
	c := NewCircuitFromConfig("throttle", Config{
		Execution: ExecutionConfig{MaxConcurrentRequests: -1, Timeout: -1},
		Fallback:  FallbackConfig{MaxConcurrentRequests: 1},
		Metrics:   MetricsCollectors{Fallback: []FallbackMetrics{m}},
	})
	fails := func(context.Context) error { return errors.New("boom") }
	recovers := func(context.Context, error) error { return nil }
	started, finish, aDone, bDone := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(aDone)
		_ = c.Execute(ctx, fails, func(context.Context, error) error { close(started); <-finish; return nil })
	}()
	regWait(t, started, "A's fallback to start")
	go func() {
		defer close(bDone)
		_ = c.Execute(ctx, fails, recovers)
	}()
	regWait(t, m.entered, "B's fallback to be rejected")
	defer func() { close(m.release); regWait(t, bDone, "B to return") }()
	running := c.ConcurrentFallbacks()
	close(finish)
	regWait(t, aDone, "A to finish")

	require.NoError(t, c.Execute(ctx, fails, recovers), "fallback rejected although no fallback is running")
	require.Equal(t, int64(1), running, "only A's fallback was running")
	require.Zero(t, c.ConcurrentFallbacks())
}

// Prevent stops requests on a closed circuit, so they must not be counted as short circuits (which mean "open" and
// fail SLO tracking).  Reporting them properly needs a metric of their own.
func TestPreventReportsNoRunMetrics(t *testing.T) {
	rm := &countingRunMetrics{}
	c := NewCircuitFromConfig("prevent", Config{
		General: GeneralConfig{ClosedToOpenFactory: func() ClosedToOpen { return &configurableOpener{prevent: true} }},
		Metrics: MetricsCollectors{Run: []RunMetrics{rm}},
	})
	require.Error(t, c.Run(context.Background(), func(context.Context) error { return nil }))
	require.Zero(t, rm.total(), "RunMetrics callbacks for a prevented Execute")
}

// ForcedClosed: "every Execute is allowed to run"
func TestForcedClosedOverridesPrevent(t *testing.T) {
	c := NewCircuitFromConfig("prevent", Config{
		General: GeneralConfig{
			ForcedClosed:        true,
			ClosedToOpenFactory: func() ClosedToOpen { return &configurableOpener{prevent: true} },
		},
	})
	ran := false
	require.NoError(t, c.Run(context.Background(), func(context.Context) error { ran = true; return nil }))
	require.True(t, ran)
}

// The context deadline fires *at* start+Timeout.  When the clock still reads exactly that instant once runFunc
// returns (always under testing/synctest, and on platforms with a coarse clock) it is still a timeout.
func TestTimeoutAtExactDeadline(t *testing.T) {
	const timeout = time.Millisecond
	mc := &clock.MockClock{}
	mc.Set(time.Now())
	rm := &countingRunMetrics{}
	c := NewCircuitFromConfig("boundary", Config{
		General:   GeneralConfig{TimeKeeper: TimeKeeper{Now: mc.Now}},
		Execution: ExecutionConfig{Timeout: timeout},
		Metrics:   MetricsCollectors{Run: []RunMetrics{rm}},
	})
	err := c.Run(context.Background(), func(ctx context.Context) error {
		<-ctx.Done()
		mc.Add(timeout)
		return ctx.Err()
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, int64(1), rm.errTimeout.Get(), "ErrTimeout (ErrFailure=%d)", rm.errFailure.Get())
	require.Equal(t, int64(1), rm.total())
}

// While the limit is saturated, a storm of rejected callers must not show up in the counter at all: one that is
// descheduled holding an increment blocks a slot although nothing is running in it.
func TestRejectStormDoesNotInflateConcurrentCommands(t *testing.T) {
	ctx := context.Background()
	c := NewCircuitFromConfig("storm", Config{Execution: ExecutionConfig{MaxConcurrentRequests: 1, Timeout: -1}})
	passes := func(context.Context) error { return nil }
	require.NoError(t, c.Run(ctx, func(context.Context) error {
		require.Error(t, c.Run(ctx, passes))
		var stop atomic.Bool
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for !stop.Load() {
					_ = c.Run(ctx, passes)
				}
			}()
		}
		defer wg.Wait()
		defer stop.Store(true)
		for deadline := time.Now().Add(50 * time.Millisecond); time.Now().Before(deadline); {
			require.Equal(t, int64(1), c.ConcurrentCommands())
		}
		return nil
	}))
}

func TestAcquireSlot(t *testing.T) {
	var running faststats.AtomicInt64
	const limit = 4
	for i := 0; i < limit; i++ {
		require.True(t, acquireSlot(&running, limit))
	}
	require.False(t, acquireSlot(&running, limit))
	require.Equal(t, int64(limit), running.Get())
	running.Add(-1)
	require.True(t, acquireSlot(&running, limit), "a freed slot is usable again")
	require.False(t, acquireSlot(&running, limit-2), "limit lowered below what is running")
	require.Equal(t, int64(limit), running.Get())
	require.True(t, acquireSlot(&running, -1), "negative is unlimited")
	require.Equal(t, int64(limit+1), running.Get())
	running.Set(0)
	require.False(t, acquireSlot(&running, 0), "zero rejects everything")
	require.Zero(t, running.Get())
}

// Callers racing for the last slot: the limit is never exceeded, and losers leave nothing behind.
func TestAcquireSlotConcurrent(t *testing.T) {
	const limit = 2
	var running faststats.AtomicInt64
	var holders, admitted atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50000; i++ {
				if !acquireSlot(&running, limit) {
					continue
				}
				admitted.Add(1)
				if n := holders.Add(1); n > limit {
					t.Errorf("%d holders with a limit of %d", n, limit)
				}
				holders.Add(-1)
				running.Add(-1)
			}
		}()
	}
	wg.Wait()
	require.Zero(t, running.Get())
	require.Positive(t, admitted.Load())
}

// Every way of turning a request away before it runs hands the circuit's error to the fallback.
func TestRejectedRequestsReachFallback(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg     Config
		prepare func(c *Circuit)
		want    error
	}{
		"open":      {prepare: func(c *Circuit) { c.OpenCircuit(context.Background()) }, want: errCircuitOpen},
		"throttled": {prepare: func(c *Circuit) { c.concurrentCommands.Add(10) }, want: errThrottledConcurrentCommands},
		"prevented": {want: errCircuitOpen, cfg: Config{General: GeneralConfig{
			ClosedToOpenFactory: func() ClosedToOpen { return &configurableOpener{prevent: true} },
		}}},
	} {
		t.Run(name, func(t *testing.T) {
			c := NewCircuitFromConfig(name, tc.cfg)
			if tc.prepare != nil {
				tc.prepare(c)
			}
			var got error
			require.NoError(t, c.Execute(context.Background(), func(context.Context) error {
				t.Error("must not run")
				return nil
			}, func(_ context.Context, err error) error {
				got = err
				return nil
			}))
			require.Equal(t, tc.want, got)
		})
	}
}
