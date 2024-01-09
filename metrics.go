package circuit

import (
	"context"
	"expvar"
	"time"
)

// RunMetricsCollection send metrics to multiple RunMetrics
type RunMetricsCollection []RunMetrics

var _ RunMetrics = &RunMetricsCollection{}

type varable interface {
	Var() expvar.Var
}

func expvarToVal(in expvar.Var) interface{} {
	type iv interface {
		Value() interface{}
	}
	if rawVal, ok := in.(iv); ok {
		return rawVal.Value()
	}
	return nil
}

// Var exposes run collectors as expvar
func (r RunMetricsCollection) Var() expvar.Var {
	return expvar.Func(func() interface{} {
		ret := make([]interface{}, 0, len(r))
		for _, c := range r {
			if v, ok := c.(varable); ok {
				asVal := expvarToVal(v.Var())
				if asVal != nil {
					ret = append(ret, asVal)
				}
			}
		}
		return ret
	})
}

// Success sends Success to all collectors
func (r RunMetricsCollection) Success(ctx context.Context, now time.Time, duration time.Duration) {
	for _, c := range r {
		c.Success(ctx, now, duration)
	}
}

// ErrConcurrencyLimitReject sends ErrConcurrencyLimitReject to all collectors
func (r RunMetricsCollection) ErrConcurrencyLimitReject(ctx context.Context, now time.Time) {
	for _, c := range r {
		c.ErrConcurrencyLimitReject(ctx, now)
	}
}

// ErrFailure sends ErrFailure to all collectors
func (r RunMetricsCollection) ErrFailure(ctx context.Context, now time.Time, duration time.Duration) {
	for _, c := range r {
		c.ErrFailure(ctx, now, duration)
	}
}

// ErrShortCircuit sends ErrShortCircuit to all collectors
func (r RunMetricsCollection) ErrShortCircuit(ctx context.Context, now time.Time) {
	for _, c := range r {
		c.ErrShortCircuit(ctx, now)
	}
}

// ErrTimeout sends ErrTimeout to all collectors
func (r RunMetricsCollection) ErrTimeout(ctx context.Context, now time.Time, duration time.Duration) {
	for _, c := range r {
		c.ErrTimeout(ctx, now, duration)
	}
}

// ErrBadRequest sends ErrBadRequest to all collectors
func (r RunMetricsCollection) ErrBadRequest(ctx context.Context, now time.Time, duration time.Duration) {
	for _, c := range r {
		c.ErrBadRequest(ctx, now, duration)
	}
}

// ErrInterrupt sends ErrInterrupt to all collectors
func (r RunMetricsCollection) ErrInterrupt(ctx context.Context, now time.Time, duration time.Duration) {
	for _, c := range r {
		c.ErrInterrupt(ctx, now, duration)
	}
}

// FallbackMetricsCollection sends fallback metrics to all collectors
type FallbackMetricsCollection []FallbackMetrics

var _ FallbackMetrics = &FallbackMetricsCollection{}

// Success sends Success to all collectors
func (r FallbackMetricsCollection) Success(ctx context.Context, now time.Time, duration time.Duration) {
	for _, c := range r {
		c.Success(ctx, now, duration)
	}
}

// ErrConcurrencyLimitReject sends ErrConcurrencyLimitReject to all collectors
func (r FallbackMetricsCollection) ErrConcurrencyLimitReject(ctx context.Context, now time.Time) {
	for _, c := range r {
		c.ErrConcurrencyLimitReject(ctx, now)
	}
}

// ErrFailure sends ErrFailure to all collectors
func (r FallbackMetricsCollection) ErrFailure(ctx context.Context, now time.Time, duration time.Duration) {
	for _, c := range r {
		c.ErrFailure(ctx, now, duration)
	}
}

// Var exposes run collectors as expvar
func (r FallbackMetricsCollection) Var() expvar.Var {
	return expvar.Func(func() interface{} {
		ret := make([]interface{}, 0, len(r))
		for _, c := range r {
			if v, ok := c.(varable); ok {
				asVal := expvarToVal(v.Var())
				if asVal != nil {
					ret = append(ret, asVal)
				}
			}
		}
		return ret
	})
}

// MetricsCollection allows reporting multiple circuit metrics at once
type MetricsCollection []Metrics

var _ Metrics = &MetricsCollection{}

// Closed sends Closed to all collectors
func (r MetricsCollection) Closed(ctx context.Context, now time.Time) {
	for _, c := range r {
		c.Closed(ctx, now)
	}
}

// Opened sends Opened to all collectors
func (r MetricsCollection) Opened(ctx context.Context, now time.Time) {
	for _, c := range r {
		c.Opened(ctx, now)
	}
}

// Metrics reports internal circuit metric events
type Metrics interface {
	// Closed is called when the circuit transitions from Open to Closed.
	Closed(ctx context.Context, now time.Time)
	// Opened is called when the circuit transitions from Closed to Opened.
	Opened(ctx context.Context, now time.Time)
}

// RunMetrics is guaranteed to execute one (and only one) of the following functions each time the circuit
// attempts to call a run function. Methods with durations are when run was actually executed.  Methods without
// durations never called run, probably because of the circuit.
type RunMetrics interface {
	// Success each time `Execute` does not return an error
	Success(ctx context.Context, now time.Time, duration time.Duration)
	// ErrFailure each time a runFunc (the circuit part) ran, but failed
	ErrFailure(ctx context.Context, now time.Time, duration time.Duration)
	// ErrTimeout increments the number of timeouts that occurred in the circuit breaker.
	ErrTimeout(ctx context.Context, now time.Time, duration time.Duration)
	// ErrBadRequest is counts of http://netflix.github.io/Hystrix/javadoc/com/netflix/hystrix/exception/HystrixBadRequestException.html
	// See https://github.com/Netflix/Hystrix/wiki/How-To-Use#error-propagation
	ErrBadRequest(ctx context.Context, now time.Time, duration time.Duration)
	// ErrInterrupt means the request ended, not because the runFunc failed, but probably because the original
	// context canceled.  Your circuit returned an error, but it's probably because someone else killed the context,
	// and not that your circuit is broken.  Java Manager doesn't have an equivalent for this, but it would be like if
	// an interrupt was called on the thread.
	//
	// A note on stat tracking: you may or may not consider this duration valid.  Yes, that's how long it executed,
	// but the circuit never finished correctly since it was asked to end early, so the value is smaller than the
	// circuit would have otherwise taken.
	ErrInterrupt(ctx context.Context, now time.Time, duration time.Duration)
	// If `Execute` returns an error, it will increment one of the following metrics

	// ErrConcurrencyLimitReject each time a circuit is rejected due to concurrency limits
	ErrConcurrencyLimitReject(ctx context.Context, now time.Time)
	// ErrShortCircuit each time runFunc is not called because the circuit was open.
	ErrShortCircuit(ctx context.Context, now time.Time)
}

// FallbackMetrics is guaranteed to execute one (and only one) of the following functions each time a fallback is executed.
// Methods with durations are when the fallback is actually executed.  Methods without durations are when the fallback was
// never called, probably because of some circuit condition.
type FallbackMetrics interface {
	// All `fallback` calls will implement one of the following metrics

	// Success each time fallback is called and succeeds.
	Success(ctx context.Context, now time.Time, duration time.Duration)
	// ErrFailure each time fallback callback fails.
	ErrFailure(ctx context.Context, now time.Time, duration time.Duration)
	// ErrConcurrencyLimitReject each time fallback fails due to concurrency limit
	ErrConcurrencyLimitReject(ctx context.Context, now time.Time)
}

var _ FallbackMetrics = RunMetrics(nil)
