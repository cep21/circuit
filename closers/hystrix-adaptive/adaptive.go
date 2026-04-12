package hystrixadaptive

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/cep21/circuit/v4"
	"github.com/cep21/circuit/v4/closers/hystrix"
	"github.com/cep21/circuit/v4/faststats"
)

// AdaptiveOpener composes *hystrix.Opener and overrides ShouldOpen to avoid opening when
// recent failures are mostly timeouts during elevated latency headroom
type AdaptiveOpener struct {
	Opener *hystrix.Opener

	mu     sync.Mutex
	config ConfigureAdaptive

	// extra is added to BaselineLatency when deciding if a success was "slow" and for ShouldOpen
	extra time.Duration

	timeoutCount faststats.RollingCounter
	failureCount faststats.RollingCounter
}

// Compile-time assertions that AdaptiveOpener implements circuit.ClosedToOpen and json.Marshaler
var (
	_ circuit.ClosedToOpen = (*AdaptiveOpener)(nil)
	_ json.Marshaler       = (*AdaptiveOpener)(nil)
)

// OpenerFactory returns a ClosedToOpen factory that wraps hystrix.OpenerFactory
func OpenerFactory(config ConfigureAdaptive) func() circuit.ClosedToOpen {
	return func() circuit.ClosedToOpen {
		cfg := config
		cfg.Merge(defaultConfigureAdaptive)
		opener := hystrix.OpenerFactory(cfg.ConfigureOpener)().(*hystrix.Opener)
		a := &AdaptiveOpener{Opener: opener}
		a.setConfigNotThreadSafeLocked(cfg)
		return a
	}
}

// ShouldOpen delegates to the Hystrix opener, then may suppress opening when headroom is
// non-zero and rolling failures are predominantly timeouts (ambient slowness)
func (a *AdaptiveOpener) ShouldOpen(ctx context.Context, now time.Time) bool {
	if !a.Opener.ShouldOpen(ctx, now) {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.extra <= 0 {
		return true
	}
	t := a.timeoutCount.RollingSumAt(now)
	f := a.failureCount.RollingSumAt(now)
	if t+f == 0 {
		return true
	}
	ratio := float64(t) / float64(t+f)
	if ratio >= a.config.MinTimeoutRatioToDefer {
		return false
	}
	return true
}

// Prevent delegates to the Hystrix opener
func (a *AdaptiveOpener) Prevent(ctx context.Context, now time.Time) bool {
	return a.Opener.Prevent(ctx, now)
}

// Closed resets the adaptive state and delegates to the Hystrix opener
func (a *AdaptiveOpener) Closed(ctx context.Context, now time.Time) {
	a.Opener.Closed(ctx, now)
	a.resetAdaptive(now)
}

// Opened resets the adaptive state and delegates to the Hystrix opener
func (a *AdaptiveOpener) Opened(ctx context.Context, now time.Time) {
	a.Opener.Opened(ctx, now)
	a.resetAdaptive(now)
}

// Success adjusts the adaptive headroom and delegates to the Hystrix opener
func (a *AdaptiveOpener) Success(ctx context.Context, now time.Time, d time.Duration) {
	a.Opener.Success(ctx, now, d)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.adjustExtraOnSuccessLocked(d)
}

// ErrBadRequest delegates to the Hystrix opener
func (a *AdaptiveOpener) ErrBadRequest(ctx context.Context, now time.Time, d time.Duration) {
	a.Opener.ErrBadRequest(ctx, now, d)
}

// ErrInterrupt delegates to the Hystrix opener
func (a *AdaptiveOpener) ErrInterrupt(ctx context.Context, now time.Time, d time.Duration) {
	a.Opener.ErrInterrupt(ctx, now, d)
}

// ErrFailure increases the failure count and delegates to the Hystrix opener
func (a *AdaptiveOpener) ErrFailure(ctx context.Context, now time.Time, d time.Duration) {
	a.Opener.ErrFailure(ctx, now, d)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failureCount.Inc(now)
}

// ErrTimeout increases the timeout count and delegates to the Hystrix opener
func (a *AdaptiveOpener) ErrTimeout(ctx context.Context, now time.Time, d time.Duration) {
	a.Opener.ErrTimeout(ctx, now, d)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.timeoutCount.Inc(now)
	a.bumpExtraLocked()
}

// ErrConcurrencyLimitReject delegates to the Hystrix opener
func (a *AdaptiveOpener) ErrConcurrencyLimitReject(ctx context.Context, now time.Time) {
	a.Opener.ErrConcurrencyLimitReject(ctx, now)
}

// ErrShortCircuit delegates to the Hystrix opener
func (a *AdaptiveOpener) ErrShortCircuit(ctx context.Context, now time.Time) {
	a.Opener.ErrShortCircuit(ctx, now)
}

// adjustExtraOnSuccessLocked adjusts the adaptive headroom based on the success duration
func (a *AdaptiveOpener) adjustExtraOnSuccessLocked(d time.Duration) {
	base := a.config.BaselineLatency
	maxE := a.config.MaxExtraLatency
	effectiveSlow := base + a.extra
	switch {
	case d > effectiveSlow:
		a.extra += a.config.IncreaseExtra
		if a.extra > maxE {
			a.extra = maxE
		}
	case d < base:
		a.extra -= a.config.DecreaseExtra
		if a.extra < 0 {
			a.extra = 0
		}
	}
}

// bumpExtraLocked increases the adaptive headroom based on the increase extra
func (a *AdaptiveOpener) bumpExtraLocked() {
	maxE := a.config.MaxExtraLatency
	a.extra += a.config.IncreaseExtra
	if a.extra > maxE {
		a.extra = maxE
	}
}

// resetAdaptive resets the adaptive state
func (a *AdaptiveOpener) resetAdaptive(now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.extra = 0
	a.timeoutCount.Reset(now)
	a.failureCount.Reset(now)
}

// SetConfigThreadSafe updates hystrix opener fields from ConfigureOpener
func (a *AdaptiveOpener) SetConfigThreadSafe(props ConfigureAdaptive) {
	props.Merge(defaultConfigureAdaptive)
	a.mu.Lock()
	a.config = props
	a.mu.Unlock()
	a.Opener.SetConfigThreadSafe(props.ConfigureOpener)
}

// SetConfigNotThreadSafe reinitializes rolling windows for the adaptive split counters
func (a *AdaptiveOpener) SetConfigNotThreadSafe(props ConfigureAdaptive) {
	a.setConfigNotThreadSafeLocked(props)
}

// setConfigNotThreadSafeLocked sets the adaptive configuration and reinitializes rolling windows
func (a *AdaptiveOpener) setConfigNotThreadSafeLocked(props ConfigureAdaptive) {
	props.Merge(defaultConfigureAdaptive)
	a.mu.Lock()
	a.config = props
	a.extra = 0
	a.mu.Unlock()
	a.Opener.SetConfigNotThreadSafe(props.ConfigureOpener)
	ho := props.ConfigureOpener
	nowFn := ho.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	t := nowFn()
	rollingCounterBucketWidth := time.Duration(ho.RollingDuration.Nanoseconds() / int64(ho.NumBuckets))
	a.mu.Lock()
	a.timeoutCount = faststats.NewRollingCounter(rollingCounterBucketWidth, ho.NumBuckets, t)
	a.failureCount = faststats.NewRollingCounter(rollingCounterBucketWidth, ho.NumBuckets, t)
	a.mu.Unlock()
}

// Config returns the merged adaptive configuration (including embedded hystrix opener config)
func (a *AdaptiveOpener) Config() ConfigureAdaptive {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.config
}

// ExtraLatency returns the current adaptive headroom on top of BaselineLatency
func (a *AdaptiveOpener) ExtraLatency() time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.extra
}

// MarshalJSON exposes opener state for debugging
func (a *AdaptiveOpener) MarshalJSON() ([]byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return json.Marshal(map[string]interface{}{
		"hystrix":          a.Opener,
		"config":           a.config,
		"extra_latency_ns": int64(a.extra),
		"timeouts":         &a.timeoutCount,
		"failures":         &a.failureCount,
	})
}
