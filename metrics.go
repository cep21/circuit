package hystrix

import (
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
func (r RunMetricsCollection) Success(now time.Time, duration time.Duration) {
	for _, c := range r {
		c.Success(now, duration)
	}
}

// ErrConcurrencyLimitReject sends ErrConcurrencyLimitReject to all collectors
func (r RunMetricsCollection) ErrConcurrencyLimitReject(now time.Time) {
	for _, c := range r {
		c.ErrConcurrencyLimitReject(now)
	}
}

// ErrFailure sends ErrFailure to all collectors
func (r RunMetricsCollection) ErrFailure(now time.Time, duration time.Duration) {
	for _, c := range r {
		c.ErrFailure(now, duration)
	}
}

// ErrShortCircuit sends ErrShortCircuit to all collectors
func (r RunMetricsCollection) ErrShortCircuit(now time.Time) {
	for _, c := range r {
		c.ErrShortCircuit(now)
	}
}

// ErrTimeout sends ErrTimeout to all collectors
func (r RunMetricsCollection) ErrTimeout(now time.Time, duration time.Duration) {
	for _, c := range r {
		c.ErrTimeout(now, duration)
	}
}

// ErrBadRequest sends ErrBadRequest to all collectors
func (r RunMetricsCollection) ErrBadRequest(now time.Time, duration time.Duration) {
	for _, c := range r {
		c.ErrBadRequest(now, duration)
	}
}

// ErrInterrupt sends ErrInterrupt to all collectors
func (r RunMetricsCollection) ErrInterrupt(now time.Time, duration time.Duration) {
	for _, c := range r {
		c.ErrInterrupt(now, duration)
	}
}

// FallbackMetricsCollection sends fallback metrics to all collectors
type FallbackMetricsCollection []FallbackMetric

var _ FallbackMetric = &FallbackMetricsCollection{}

// Success sends Success to all collectors
func (r FallbackMetricsCollection) Success(now time.Time, duration time.Duration) {
	for _, c := range r {
		c.Success(now, duration)
	}
}

// ErrConcurrencyLimitReject sends ErrConcurrencyLimitReject to all collectors
func (r FallbackMetricsCollection) ErrConcurrencyLimitReject(now time.Time) {
	for _, c := range r {
		c.ErrConcurrencyLimitReject(now)
	}
}

// ErrFailure sends ErrFailure to all collectors
func (r FallbackMetricsCollection) ErrFailure(now time.Time, duration time.Duration) {
	for _, c := range r {
		c.ErrFailure(now, duration)
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

type CircuitMetricsCollection []CircuitMetrics
var _ CircuitMetrics = &CircuitMetricsCollection{}

// Closed sends Closed to all collectors
func (r CircuitMetricsCollection) Closed(now time.Time) {
	for _, c := range r {
		c.Closed(now)
	}
}

// Opened sends Opened to all collectors
func (r CircuitMetricsCollection) Opened(now time.Time) {
	for _, c := range r {
		c.Opened(now)
	}
}

// CircuitMetrics reports internal circuit metric events
type CircuitMetrics interface {
	// Closed is called when the circuit transitions from Open to Closed.
	Closed(now time.Time)
	// Opened is called when the circuit transitions from Closed to Opened.
	Opened(now time.Time)
}

// RunMetrics is guaranteed to execute one (and only one) of the following functions each time the circuit
// attempts to call a run function. Methods with durations are when run was actually executed.  Methods without
// durations never called run, probably because of the circuit.
type RunMetrics interface {
	// Success each time `Execute` does not return an error
	Success(now time.Time, duration time.Duration)
	// ErrFailure each time a runFunc (the circuit part) ran, but failed
	ErrFailure(now time.Time, duration time.Duration)
	// ErrTimeout increments the number of timeouts that occurred in the circuit breaker.
	ErrTimeout(now time.Time, duration time.Duration)
	// ErrBadRequest is counts of http://netflix.github.io/Hystrix/javadoc/com/netflix/hystrix/exception/HystrixBadRequestException.html
	// See https://github.com/Netflix/Hystrix/wiki/How-To-Use#error-propagation
	ErrBadRequest(now time.Time, duration time.Duration)
	// ErrInterrupt means the request ended, not because the runFunc failed, but probably because the original
	// context canceled.  Your circuit returned an error, but it's probably because someone else killed the context,
	// and not that your circuit is broken.  Java Hystrix doesn't have an equivalent for this, but it would be like if
	// an interrupt was called on the thread.
	//
	// A note on stat tracking: you may or may not consider this duration valid.  Yes, that's how long it executed,
	// but the circuit never finished correctly since it was asked to end early, so the value is smaller than the
	// circuit would have otherwise taken.
	ErrInterrupt(now time.Time, duration time.Duration)

	// If `Execute` returns an error, it will increment one of the following metrics
	// ErrConcurrencyLimitReject each time a circuit is rejected due to concurrency limits
	ErrConcurrencyLimitReject(now time.Time)
	// ErrShortCircuit each time runFunc is not called because the circuit was open.
	ErrShortCircuit(now time.Time)
}

// FallbackMetric is guaranteed to execute one (and only one) of the following functions each time a fallback is executed.
// Methods with durations are when the fallback is actually executed.  Methods without durations are when the fallback was
// never called, probably because of some circuit condition.
type FallbackMetric interface {
	// All `fallback` calls will implement one of the following metrics
	// Success each time fallback is called and succeeds.
	Success(now time.Time, duration time.Duration)
	// ErrFailure each time fallback callback fails.
	ErrFailure(now time.Time, duration time.Duration)
	// ErrConcurrencyLimitReject each time fallback fails due to concurrency limit
	ErrConcurrencyLimitReject(now time.Time)
}

var _ FallbackMetric = RunMetrics(nil)