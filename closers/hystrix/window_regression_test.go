package hystrix

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cep21/circuit/v4"
	"github.com/cep21/circuit/v4/internal/clock"
)

// The README's live-config example passes only SleepWindow to Closer.SetConfigThreadSafe, which leaves
// RequiredConcurrentSuccessful at zero.  A request that was already in flight when the circuit opened must still not
// close it: at least one genuine half-open probe has to succeed first.
func TestCloserPartialLiveConfigStillNeedsAProbe(t *testing.T) {
	mc := &clock.MockClock{}
	mc.Set(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	c := newMockClockCircuit(t, "partial", mc, ConfigureCloser{})
	c.OpenToClose.(*Closer).SetConfigThreadSafe(ConfigureCloser{SleepWindow: 3 * time.Second})
	ctx := context.Background()

	release, started, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- c.Execute(ctx, func(context.Context) error { close(started); <-release; return nil }, nil)
	}()
	<-started
	mc.Add(time.Millisecond)
	_ = c.Execute(ctx, func(context.Context) error { return errors.New("boom") }, nil)
	if !c.IsOpen() {
		t.Fatal("precondition: circuit should have opened")
	}
	mc.Add(time.Millisecond)
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("in-flight request should still succeed: %v", err)
	}
	if !c.IsOpen() {
		t.Fatal("a pre-open in-flight success closed the circuit 1ms after it opened; SleepWindow (3s) never honored")
	}

	// One successful probe still closes it
	mc.Add(3 * time.Second)
	if err := c.Execute(ctx, func(context.Context) error { return nil }, nil); err != nil {
		t.Fatalf("probe after the sleep window should run and pass: %v", err)
	}
	if c.IsOpen() {
		t.Fatal("successful probe should close the circuit")
	}
}

// The Opener keeps attempts and errors in two rolling counters that roll forward independently: a success only rolls
// attempts.  A failure that finishes just before a bucket boundary but asks ShouldOpen just after a concurrent success
// crossed it must not compare the errors of the old window with the attempts of the new one.
func TestOpenerWindowsSkewedByConcurrentSuccess(t *testing.T) {
	mc := &clock.MockClock{}
	t0 := mc.Set(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	hook := &hookRunMetrics{RunMetrics: circuit.RunMetricsCollection{}}
	c := circuit.NewCircuitFromConfig("skew", circuit.Config{
		General: circuit.GeneralConfig{
			TimeKeeper:          circuit.TimeKeeper{Now: mc.Now, AfterFunc: mc.AfterFunc},
			OpenToClosedFactory: CloserFactory(ConfigureCloser{AfterFunc: mc.AfterFunc}),
			// Defaults: 50% of at least 20 requests over 10 buckets of 1s
			ClosedToOpenFactory: OpenerFactory(ConfigureOpener{Now: mc.Now}),
		},
		Execution: circuit.ExecutionConfig{Timeout: -1, MaxConcurrentRequests: -1},
		Metrics:   circuit.MetricsCollectors{Run: []circuit.RunMetrics{hook}},
	})
	ctx := context.Background()
	pass := func(context.Context) error { return nil }
	fail := func(context.Context) error { return errors.New("boom") }

	// Second 0: a burst with a 20% error rate.  Seconds 1-9: light, fully healthy traffic.
	for i := 0; i < 100; i++ {
		if i%5 == 4 {
			_ = c.Run(ctx, fail)
		} else {
			_ = c.Run(ctx, pass)
		}
	}
	for s := 1; s <= 9; s++ {
		mc.Set(t0.Add(time.Duration(s) * time.Second))
		for i := 0; i < 3; i++ {
			_ = c.Run(ctx, pass)
		}
	}
	if c.IsOpen() {
		t.Fatal("precondition: 20 errors out of 127 must not open the circuit")
	}

	// One failure finishes at 9.999s.  While its metrics are being reported the clock crosses the bucket boundary and
	// another request succeeds.
	mc.Set(t0.Add(10*time.Second - time.Millisecond))
	hook.onErrFailure = func() {
		mc.Set(t0.Add(10 * time.Second))
		if err := c.Run(ctx, pass); err != nil {
			t.Errorf("concurrent request should pass: %v", err)
		}
	}
	_ = c.Run(ctx, fail)
	if c.IsOpen() {
		t.Fatal("circuit opened although the error rate is 21/128 (16%) in the old window and 1/29 (3%) in the new one")
	}
}

// err_% on expvar is a ratio: it must not exceed 1 just because failures landed between reading the two counters.
func TestOpenerJSONErrPercentageNeverAboveOne(t *testing.T) {
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	o := OpenerFactory(ConfigureOpener{Now: func() time.Time { return now }})().(*Opener)
	var stop atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			o.ErrFailure(context.Background(), now, time.Millisecond)
		}
	}()
	defer wg.Wait()
	defer stop.Store(true)
	for o.errorsCount.TotalSum() < 200_000 {
		b, err := json.Marshal(o)
		if err != nil {
			t.Fatal(err)
		}
		var out struct {
			Pct float64 `json:"err_%"`
		}
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		if out.Pct > 1 {
			t.Fatalf("err_%% = %v", out.Pct)
		}
	}
}
