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

// Opener is the adaptive ClosedToOpen implementation: it wraps an inner *hystrix.Opener
// (field Opener) and overrides ShouldOpen to avoid opening when recent failures are mostly
// timeouts during elevated latency headroom
type Opener struct {
	Opener *hystrix.Opener

	mu     sync.Mutex
	config ConfigureAdaptive

	// extra is added to BaselineLatency when deciding if a success was "slow" and for ShouldOpen
	extra time.Duration

	timeoutCount faststats.RollingCounter
	failureCount faststats.RollingCounter
}

// Compile-time assertions that Opener implements circuit.ClosedToOpen and json.Marshaler
var (
	_ circuit.ClosedToOpen = (*Opener)(nil)
	_ json.Marshaler       = (*Opener)(nil)
)

// NewOpener returns a new Opener with defaults merged into config.
// It is the same implementation as the value returned from OpenerFactory(config)().
func NewOpener(config ConfigureAdaptive) *Opener {
	cfg := config
	cfg.Merge(defaultConfigureAdaptive)
	inner := hystrix.OpenerFactory(cfg.ConfigureOpener)().(*hystrix.Opener)
	a := &Opener{Opener: inner}
	a.setConfigNotThreadSafeLocked(cfg)
	return a
}

// OpenerFactory returns a ClosedToOpen factory that wraps hystrix.OpenerFactory
func OpenerFactory(config ConfigureAdaptive) func() circuit.ClosedToOpen {
	return func() circuit.ClosedToOpen {
		return NewOpener(config)
	}
}

// ShouldOpen delegates to the Hystrix opener, then may suppress opening when headroom is
// non-zero and rolling failures are predominantly timeouts (ambient slowness)
func (a *Opener) ShouldOpen(ctx context.Context, now time.Time) bool {
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
func (a *Opener) Prevent(ctx context.Context, now time.Time) bool {
	return a.Opener.Prevent(ctx, now)
}

// Closed resets the adaptive state and delegates to the Hystrix opener
func (a *Opener) Closed(ctx context.Context, now time.Time) {
	a.Opener.Closed(ctx, now)
	a.resetAdaptive(now)
}

// Opened resets the adaptive state and delegates to the Hystrix opener
func (a *Opener) Opened(ctx context.Context, now time.Time) {
	a.Opener.Opened(ctx, now)
	a.resetAdaptive(now)
}

// Success adjusts the adaptive headroom and delegates to the Hystrix opener
func (a *Opener) Success(ctx context.Context, now time.Time, d time.Duration) {
	a.Opener.Success(ctx, now, d)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.adjustExtraOnSuccessLocked(d)
}

// ErrBadRequest delegates to the Hystrix opener
func (a *Opener) ErrBadRequest(ctx context.Context, now time.Time, d time.Duration) {
	a.Opener.ErrBadRequest(ctx, now, d)
}

// ErrInterrupt delegates to the Hystrix opener
func (a *Opener) ErrInterrupt(ctx context.Context, now time.Time, d time.Duration) {
	a.Opener.ErrInterrupt(ctx, now, d)
}

// ErrFailure increases the failure count and delegates to the Hystrix opener
func (a *Opener) ErrFailure(ctx context.Context, now time.Time, d time.Duration) {
	a.Opener.ErrFailure(ctx, now, d)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failureCount.Inc(now)
}

// ErrTimeout increases the timeout count and delegates to the Hystrix opener
func (a *Opener) ErrTimeout(ctx context.Context, now time.Time, d time.Duration) {
	a.Opener.ErrTimeout(ctx, now, d)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.timeoutCount.Inc(now)
	a.bumpExtraLocked()
}

// ErrConcurrencyLimitReject delegates to the Hystrix opener
func (a *Opener) ErrConcurrencyLimitReject(ctx context.Context, now time.Time) {
	a.Opener.ErrConcurrencyLimitReject(ctx, now)
}

// ErrShortCircuit delegates to the Hystrix opener
func (a *Opener) ErrShortCircuit(ctx context.Context, now time.Time) {
	a.Opener.ErrShortCircuit(ctx, now)
}

// adjustExtraOnSuccessLocked adjusts the adaptive headroom based on the success duration
func (a *Opener) adjustExtraOnSuccessLocked(d time.Duration) {
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
func (a *Opener) bumpExtraLocked() {
	maxE := a.config.MaxExtraLatency
	a.extra += a.config.IncreaseExtra
	if a.extra > maxE {
		a.extra = maxE
	}
}

// resetAdaptive resets the adaptive state
func (a *Opener) resetAdaptive(now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.extra = 0
	a.timeoutCount.Reset(now)
	a.failureCount.Reset(now)
}

// SetConfigThreadSafe updates hystrix opener fields from ConfigureOpener
func (a *Opener) SetConfigThreadSafe(props ConfigureAdaptive) {
	props.Merge(defaultConfigureAdaptive)
	a.mu.Lock()
	a.config = props
	a.mu.Unlock()
	a.Opener.SetConfigThreadSafe(props.ConfigureOpener)
}

// SetConfigNotThreadSafe reinitializes rolling windows for the adaptive split counters
func (a *Opener) SetConfigNotThreadSafe(props ConfigureAdaptive) {
	a.setConfigNotThreadSafeLocked(props)
}

// setConfigNotThreadSafeLocked sets the adaptive configuration and reinitializes rolling windows
func (a *Opener) setConfigNotThreadSafeLocked(props ConfigureAdaptive) {
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
func (a *Opener) Config() ConfigureAdaptive {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.config
}

// ExtraLatency returns the current adaptive headroom on top of BaselineLatency
func (a *Opener) ExtraLatency() time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.extra
}

// MarshalJSON exposes opener state for debugging
func (a *Opener) MarshalJSON() ([]byte, error) {
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
