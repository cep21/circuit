package hystrixadaptive

import (
	"context"
	"strings"
	"testing"
	"time"

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
	// Timestamps must be >= rolling window StartTime (set when the opener is constructed).
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
