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

// Opener wraps hystrix.Opener and may defer ShouldOpen when failures are timeout-heavy and extra is under the cap
type Opener struct {
	Opener *hystrix.Opener

	mu     sync.Mutex
	config ConfigureAdaptive

	// extra is added to BaselineLatency for slow-success detection and ShouldOpen
	extra time.Duration

	timeoutCount faststats.RollingCounter
	failureCount faststats.RollingCounter
}

var (
	_ circuit.ClosedToOpen = (*Opener)(nil)
	_ json.Marshaler       = (*Opener)(nil)
)

// NewOpener merges defaults into config and returns a new Opener (same as OpenerFactory(config)())
func NewOpener(config ConfigureAdaptive) *Opener {
	cfg := config
	cfg.Merge(defaultConfigureAdaptive)
	inner := hystrix.OpenerFactory(cfg.ConfigureOpener)().(*hystrix.Opener)
	a := &Opener{Opener: inner}
	a.setConfigNotThreadSafeLocked(cfg)
	return a
}

// OpenerFactory wraps hystrix.OpenerFactory with adaptive behavior
func OpenerFactory(config ConfigureAdaptive) func() circuit.ClosedToOpen {
	return func() circuit.ClosedToOpen {
		return NewOpener(config)
	}
}

// ShouldOpen defers only if inner wants open, extra is in (0, MaxExtraLatency), and timeout ratio is high enough
func (a *Opener) ShouldOpen(ctx context.Context, now time.Time) bool {
	if !a.Opener.ShouldOpen(ctx, now) {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.extra <= 0 {
		return true
	}
	if a.config.MaxExtraLatency > 0 && a.extra >= a.config.MaxExtraLatency {
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

// Prevent forwards to the inner opener
func (a *Opener) Prevent(ctx context.Context, now time.Time) bool {
	return a.Opener.Prevent(ctx, now)
}

// Closed resets adaptive state and forwards to the inner opener
func (a *Opener) Closed(ctx context.Context, now time.Time) {
	a.Opener.Closed(ctx, now)
	a.resetAdaptive(now)
}

// Opened resets adaptive state and forwards to the inner opener
func (a *Opener) Opened(ctx context.Context, now time.Time) {
	a.Opener.Opened(ctx, now)
	a.resetAdaptive(now)
}

// Success adjusts extra and forwards to the inner opener
func (a *Opener) Success(ctx context.Context, now time.Time, d time.Duration) {
	a.Opener.Success(ctx, now, d)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.adjustExtraOnSuccessLocked(d)
}

// ErrBadRequest forwards to the inner opener
func (a *Opener) ErrBadRequest(ctx context.Context, now time.Time, d time.Duration) {
	a.Opener.ErrBadRequest(ctx, now, d)
}

// ErrInterrupt forwards to the inner opener
func (a *Opener) ErrInterrupt(ctx context.Context, now time.Time, d time.Duration) {
	a.Opener.ErrInterrupt(ctx, now, d)
}

// ErrFailure increments adaptive failure tally and forwards to the inner opener
func (a *Opener) ErrFailure(ctx context.Context, now time.Time, d time.Duration) {
	a.Opener.ErrFailure(ctx, now, d)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failureCount.Inc(now)
}

// ErrTimeout increments adaptive timeout tally, bumps extra, and forwards to the inner opener
func (a *Opener) ErrTimeout(ctx context.Context, now time.Time, d time.Duration) {
	a.Opener.ErrTimeout(ctx, now, d)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.timeoutCount.Inc(now)
	a.bumpExtraLocked()
}

// ErrConcurrencyLimitReject forwards to the inner opener
func (a *Opener) ErrConcurrencyLimitReject(ctx context.Context, now time.Time) {
	a.Opener.ErrConcurrencyLimitReject(ctx, now)
}

// ErrShortCircuit forwards to the inner opener
func (a *Opener) ErrShortCircuit(ctx context.Context, now time.Time) {
	a.Opener.ErrShortCircuit(ctx, now)
}

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

func (a *Opener) bumpExtraLocked() {
	maxE := a.config.MaxExtraLatency
	a.extra += a.config.IncreaseExtra
	if a.extra > maxE {
		a.extra = maxE
	}
}

func (a *Opener) resetAdaptive(now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.extra = 0
	a.timeoutCount.Reset(now)
	a.failureCount.Reset(now)
}

// SetConfigThreadSafe updates adaptive and Hystrix fields without rebuilding adaptive rolling counters
func (a *Opener) SetConfigThreadSafe(props ConfigureAdaptive) {
	props.Merge(defaultConfigureAdaptive)
	a.mu.Lock()
	a.config = props
	a.mu.Unlock()
	a.Opener.SetConfigThreadSafe(props.ConfigureOpener)
}

// SetConfigNotThreadSafe rebuilds rolling windows and resets extra; prefer when rolling parameters change
func (a *Opener) SetConfigNotThreadSafe(props ConfigureAdaptive) {
	a.setConfigNotThreadSafeLocked(props)
}

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

// Config returns the merged ConfigureAdaptive (including embedded hystrix config)
func (a *Opener) Config() ConfigureAdaptive {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.config
}

// ExtraLatency returns current extra headroom above BaselineLatency
func (a *Opener) ExtraLatency() time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.extra
}

// MarshalJSON is for debugging
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
