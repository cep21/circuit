package hystrixadaptive

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cep21/circuit/v4"
	"github.com/cep21/circuit/v4/closers/hystrix"
)

func TestAdaptiveOpener_TimeoutHeavyDefersOpen(t *testing.T) {
	ctx := context.Background()
	o := OpenerFactory(ConfigureAdaptive{
		ConfigureOpener: hystrix.ConfigureOpener{
			RequestVolumeThreshold:   3,
			ErrorThresholdPercentage: 50,
			NumBuckets:               10,
			RollingDuration:          10 * time.Second,
		},
		MinTimeoutRatioToDefer: 0.85,
	})().(*AdaptiveOpener)
	// Timestamps must be >= rolling window StartTime (set when the opener is constructed)
	now := time.Now()

	if o.ShouldOpen(ctx, now) {
		t.Fatal("should not open with no traffic")
	}
	for i := 0; i < 3; i++ {
		o.ErrTimeout(ctx, now, 100*time.Millisecond)
	}
	if o.ExtraLatency() <= 0 {
		t.Fatal("expected non-zero extra headroom after timeouts")
	}
	if !o.Opener.ShouldOpen(ctx, now) {
		t.Fatal("inner hystrix opener should want to open")
	}
	if o.ShouldOpen(ctx, now) {
		t.Fatal("adaptive layer should defer open when failures are mostly timeouts with headroom")
	}
}

func TestAdaptiveOpener_FailuresStillOpen(t *testing.T) {
	ctx := context.Background()
	o := OpenerFactory(ConfigureAdaptive{
		ConfigureOpener: hystrix.ConfigureOpener{
			RequestVolumeThreshold:   3,
			ErrorThresholdPercentage: 50,
			NumBuckets:               10,
			RollingDuration:          10 * time.Second,
		},
	})().(*AdaptiveOpener)
	now := time.Now()

	for i := 0; i < 3; i++ {
		o.ErrFailure(ctx, now, time.Millisecond)
	}
	if !o.ShouldOpen(ctx, now) {
		t.Fatal("expected open when rolling window is failure-dominated")
	}
}

func TestAdaptiveOpener_MarshalJSON(t *testing.T) {
	o := OpenerFactory(ConfigureAdaptive{})().(*AdaptiveOpener)
	ctx := context.Background()
	now := time.Now()
	o.ErrTimeout(ctx, now, time.Second)
	b, err := o.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "timeouts") {
		t.Fatalf("expected timeouts in json: %s", b)
	}
}

func TestFactoryConfigure(t *testing.T) {
	f := Factory{
		ConfigureAdaptive: ConfigureAdaptive{
			ConfigureOpener: hystrix.ConfigureOpener{
				RequestVolumeThreshold: 7,
			},
		},
	}
	cfg := f.Configure("x")
	ao := cfg.General.ClosedToOpenFactory().(*AdaptiveOpener)
	if ao.Config().ConfigureOpener.RequestVolumeThreshold != 7 {
		t.Fatalf("got threshold %d", ao.Config().ConfigureOpener.RequestVolumeThreshold)
	}
}

func TestAdaptiveOpener_FastSuccessClearsExtraHeadroom(t *testing.T) {
	ctx := context.Background()
	o := OpenerFactory(ConfigureAdaptive{
		ConfigureOpener: hystrix.ConfigureOpener{
			RequestVolumeThreshold:   3,
			ErrorThresholdPercentage: 50,
			NumBuckets:               10,
			RollingDuration:          10 * time.Second,
		},
		BaselineLatency: 100 * time.Millisecond,
		IncreaseExtra:   10 * time.Millisecond,
		DecreaseExtra:   10 * time.Millisecond,
		MaxExtraLatency: 200 * time.Millisecond,
	})().(*AdaptiveOpener)
	now := time.Now()

	o.ErrTimeout(ctx, now, 100*time.Millisecond)
	if got := o.ExtraLatency(); got != 10*time.Millisecond {
		t.Fatalf("after one timeout, extra = %v, want 10ms", got)
	}
	o.Success(ctx, now, 5*time.Millisecond)
	if got := o.ExtraLatency(); got != 0 {
		t.Fatalf("after fast success below baseline, extra = %v, want 0", got)
	}
}

func TestAdaptiveOpener_ClosedResetsAdaptiveState(t *testing.T) {
	ctx := context.Background()
	o := OpenerFactory(ConfigureAdaptive{
		ConfigureOpener: hystrix.ConfigureOpener{
			RequestVolumeThreshold:   3,
			ErrorThresholdPercentage: 50,
			NumBuckets:               10,
			RollingDuration:          10 * time.Second,
		},
	})().(*AdaptiveOpener)
	now := time.Now()
	o.ErrTimeout(ctx, now, time.Millisecond)
	if o.ExtraLatency() <= 0 {
		t.Fatal("expected extra after timeout")
	}
	o.Closed(ctx, now)
	if o.ExtraLatency() != 0 {
		t.Fatalf("Closed should reset extra, got %v", o.ExtraLatency())
	}
}

// TestCircuit_AdaptiveVsPlainHystrix_OpenerBehavior drives the real circuit Execute path and
// checks that plain Hystrix opens on a timeout-only burst while the adaptive opener stays closed
func TestCircuit_AdaptiveVsPlainHystrix_OpenerBehavior(t *testing.T) {
	ctx := context.Background()
	opener := hystrix.ConfigureOpener{
		RequestVolumeThreshold:   3,
		ErrorThresholdPercentage: 50,
		NumBuckets:               10,
		RollingDuration:          10 * time.Second,
	}

	runTimeoutBurst := func(factory func() circuit.ClosedToOpen) bool {
		cfg := circuit.Config{
			General: circuit.GeneralConfig{
				ClosedToOpenFactory: factory,
				OpenToClosedFactory: hystrix.CloserFactory(hystrix.ConfigureCloser{
					SleepWindow: time.Hour,
				}),
			},
			Execution: circuit.ExecutionConfig{
				Timeout: 5 * time.Millisecond,
			},
		}
		c := circuit.NewCircuitFromConfig("opener-behavior", cfg)
		for i := 0; i < 3; i++ {
			_ = c.Execute(ctx, func(ctx context.Context) error {
				select {
				case <-time.After(50 * time.Millisecond):
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}, nil)
		}
		return c.IsOpen()
	}

	t.Run("plainHystrixOpens", func(t *testing.T) {
		opened := runTimeoutBurst(hystrix.OpenerFactory(opener))
		if !opened {
			t.Fatal("expected plain Hystrix opener to open the circuit after three timeouts with volume=3 and 100% errors")
		}
	})

	t.Run("adaptiveStaysClosed", func(t *testing.T) {
		opened := runTimeoutBurst(OpenerFactory(ConfigureAdaptive{
			ConfigureOpener:        opener,
			MinTimeoutRatioToDefer: 0.85,
		}))
		if opened {
			t.Fatal("expected adaptive opener to defer opening while failures are timeout-heavy and headroom is non-zero")
		}
	})
}
