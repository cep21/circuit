package hystrix

import (
	"time"
)

// multiCmdMetricCollector send metrics to multiple RunMetrics
type multiCmdMetricCollector struct {
	CmdMetricCollectors []RunMetrics
}

func (r *multiCmdMetricCollector) SetConfigNotThreadSafe(config CommandProperties) {
	for _, c := range r.CmdMetricCollectors {
		if cfg, ok := c.(Configurable); ok {
			cfg.SetConfigNotThreadSafe(config)
		}
	}
}

func (r *multiCmdMetricCollector) SetConfigThreadSafe(config CommandProperties) {
	for _, c := range r.CmdMetricCollectors {
		if cfg, ok := c.(Configurable); ok {
			cfg.SetConfigThreadSafe(config)
		}
	}
}

var _ RunMetrics = &multiCmdMetricCollector{}
var _ Configurable = &multiCmdMetricCollector{}

// Success sends Success to all collectors
func (r *multiCmdMetricCollector) Success(duration time.Duration) {
	for _, c := range r.CmdMetricCollectors {
		c.Success(duration)
	}
}

// ErrConcurrencyLimitReject sends ErrConcurrencyLimitReject to all collectors
func (r *multiCmdMetricCollector) ErrConcurrencyLimitReject() {
	for _, c := range r.CmdMetricCollectors {
		c.ErrConcurrencyLimitReject()
	}
}

// ErrFailure sends ErrFailure to all collectors
func (r *multiCmdMetricCollector) ErrFailure(duration time.Duration) {
	for _, c := range r.CmdMetricCollectors {
		c.ErrFailure(duration)
	}
}

// ErrShortCircuit sends ErrShortCircuit to all collectors
func (r *multiCmdMetricCollector) ErrShortCircuit() {
	for _, c := range r.CmdMetricCollectors {
		c.ErrShortCircuit()
	}
}

// ErrTimeout sends ErrTimeout to all collectors
func (r *multiCmdMetricCollector) ErrTimeout(duration time.Duration) {
	for _, c := range r.CmdMetricCollectors {
		c.ErrTimeout(duration)
	}
}

// ErrBadRequest sends ErrBadRequest to all collectors
func (r *multiCmdMetricCollector) ErrBadRequest(duration time.Duration) {
	for _, c := range r.CmdMetricCollectors {
		c.ErrBadRequest(duration)
	}
}

// ErrInterrupt sends ErrInterrupt to all collectors
func (r *multiCmdMetricCollector) ErrInterrupt(duration time.Duration) {
	for _, c := range r.CmdMetricCollectors {
		c.ErrInterrupt(duration)
	}
}

var _ FallbackMetric = &multiFallbackMetricCollectors{}
var _ Configurable = &multiFallbackMetricCollectors{}

// multiFallbackMetricCollectors sends fallback metrics to all collectors
type multiFallbackMetricCollectors struct {
	FallbackMetricCollectors []FallbackMetric
}

func (r *multiFallbackMetricCollectors) SetConfigNotThreadSafe(config CommandProperties) {
	for _, c := range r.FallbackMetricCollectors {
		if cfg, ok := c.(Configurable); ok {
			cfg.SetConfigNotThreadSafe(config)
		}
	}
}

func (r *multiFallbackMetricCollectors) SetConfigThreadSafe(config CommandProperties) {
	for _, c := range r.FallbackMetricCollectors {
		if cfg, ok := c.(Configurable); ok {
			cfg.SetConfigThreadSafe(config)
		}
	}
}


// Success sends Success to all collectors
func (r *multiFallbackMetricCollectors) Success(duration time.Duration) {
	for _, c := range r.FallbackMetricCollectors {
		c.Success(duration)
	}
}

// ErrConcurrencyLimitReject sends ErrConcurrencyLimitReject to all collectors
func (r *multiFallbackMetricCollectors) ErrConcurrencyLimitReject() {
	for _, c := range r.FallbackMetricCollectors {
		c.ErrConcurrencyLimitReject()
	}
}

// ErrFailure sends ErrFailure to all collectors
func (r *multiFallbackMetricCollectors) ErrFailure(duration time.Duration) {
	for _, c := range r.FallbackMetricCollectors {
		c.ErrFailure(duration)
	}
}

// RunMetrics is guaranteed to execute one (and only one) of the following functions each time the circuit
// attempts to call a run function. Methods with durations are when run was actually executed.  Methods without
// durations never called run, probably because of the circuit.
type RunMetrics interface {
	// Success each time `Execute` does not return an error
	Success(duration time.Duration)
	// ErrFailure each time a runFunc (the circuit part) ran, but failed
	ErrFailure(duration time.Duration)
	// ErrTimeout increments the number of timeouts that occurred in the circuit breaker.
	ErrTimeout(duration time.Duration)
	// ErrBadRequest is counts of http://netflix.github.io/Hystrix/javadoc/com/netflix/hystrix/exception/HystrixBadRequestException.html
	// See https://github.com/Netflix/Hystrix/wiki/How-To-Use#error-propagation
	ErrBadRequest(duration time.Duration)
	// ErrInterrupt means the request ended, not because the runFunc failed, but probably because the original
	// context canceled.  Your circuit returned an error, but it's probably because someone else killed the context,
	// and not that your circuit is broken.  Java Hystrix doesn't have an equivalent for this, but it would be like if
	// an interrupt was called on the thread.
	//
	// A note on stat tracking: you may or may not consider this duration valid.  Yes, that's how long it executed,
	// but the circuit never finished correctly since it was asked to end early, so the value is smaller than the
	// circuit would have otherwise taken.
	ErrInterrupt(duration time.Duration)

	// If `Execute` returns an error, it will increment one of the following metrics
	// ErrConcurrencyLimitReject each time a circuit is rejected due to concurrency limits
	ErrConcurrencyLimitReject()
	// ErrShortCircuit each time runFunc is not called because the circuit was open.
	ErrShortCircuit()
}

// FallbackMetric is guaranteed to execute one (and only one) of the following functions each time a fallback is executed.
// Methods with durations are when the fallback is actually executed.  Methods without durations are when the fallback was
// never called, probably because of some circuit condition.
type FallbackMetric interface {
	// All `fallback` calls will implement one of the following metrics
	// Success each time fallback is called and succeeds.
	Success(duration time.Duration)
	// ErrFailure each time fallback callback fails.
	ErrFailure(duration time.Duration)
	// ErrConcurrencyLimitReject each time fallback fails due to concurrency limit
	ErrConcurrencyLimitReject()
}
