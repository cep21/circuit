package hystrix

import (
	"time"

	"github.com/cep21/hystrix/fastmath"
)

// MultiCmdMetricCollector send metrics to multiple RunMetrics
type MultiCmdMetricCollector struct {
	CmdMetricCollectors []RunMetrics
}

var _ RunMetrics = &MultiCmdMetricCollector{}

// Success sends Success to all collectors
func (r *MultiCmdMetricCollector) Success(duration time.Duration) {
	for _, c := range r.CmdMetricCollectors {
		c.Success(duration)
	}
}

// ErrConcurrencyLimitReject sends ErrConcurrencyLimitReject to all collectors
func (r *MultiCmdMetricCollector) ErrConcurrencyLimitReject() {
	for _, c := range r.CmdMetricCollectors {
		c.ErrConcurrencyLimitReject()
	}
}

// ErrFailure sends ErrFailure to all collectors
func (r *MultiCmdMetricCollector) ErrFailure(duration time.Duration) {
	for _, c := range r.CmdMetricCollectors {
		c.ErrFailure(duration)
	}
}

// ErrShortCircuit sends ErrShortCircuit to all collectors
func (r *MultiCmdMetricCollector) ErrShortCircuit() {
	for _, c := range r.CmdMetricCollectors {
		c.ErrShortCircuit()
	}
}

// ErrTimeout sends ErrTimeout to all collectors
func (r *MultiCmdMetricCollector) ErrTimeout(duration time.Duration) {
	for _, c := range r.CmdMetricCollectors {
		c.ErrTimeout(duration)
	}
}

// ErrBadRequest sends ErrBadRequest to all collectors
func (r *MultiCmdMetricCollector) ErrBadRequest(duration time.Duration) {
	for _, c := range r.CmdMetricCollectors {
		c.ErrBadRequest(duration)
	}
}

// ErrInterrupt sends ErrInterrupt to all collectors
func (r *MultiCmdMetricCollector) ErrInterrupt(duration time.Duration) {
	for _, c := range r.CmdMetricCollectors {
		c.ErrInterrupt(duration)
	}
}

var _ FallbackMetric = &MultiFallbackMetricCollectors{}

// MultiFallbackMetricCollectors sends fallback metrics to all collectors
type MultiFallbackMetricCollectors struct {
	FallbackMetricCollectors []FallbackMetric
}

// Success sends Success to all collectors
func (r *MultiFallbackMetricCollectors) Success(duration time.Duration) {
	for _, c := range r.FallbackMetricCollectors {
		c.Success(duration)
	}
}

// ErrConcurrencyLimitReject sends ErrConcurrencyLimitReject to all collectors
func (r *MultiFallbackMetricCollectors) ErrConcurrencyLimitReject() {
	for _, c := range r.FallbackMetricCollectors {
		c.ErrConcurrencyLimitReject()
	}
}

// ErrFailure sends ErrFailure to all collectors
func (r *MultiFallbackMetricCollectors) ErrFailure(duration time.Duration) {
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

type rollingCmdMetrics struct {
	successCount              fastmath.RollingCounter
	errConcurrencyLimitReject fastmath.RollingCounter
	errFailure                fastmath.RollingCounter
	errShortCircuit           fastmath.RollingCounter
	errTimeout                fastmath.RollingCounter
	errBadRequest             fastmath.RollingCounter
	errInterrupt              fastmath.RollingCounter

	// It is analogous to https://github.com/Netflix/Hystrix/wiki/Metrics-and-Monitoring#latency-percentiles-end-to-end-execution-gauge
	rollingLatencyPercentile fastmath.RollingPercentile
}

func (r *rollingCmdMetrics) Success(duration time.Duration) {
	now := time.Now()
	r.successCount.Inc(now)
	r.rollingLatencyPercentile.AddDuration(duration, now)
}

func (r *rollingCmdMetrics) ErrInterrupt(duration time.Duration) {
	now := time.Now()
	r.errInterrupt.Inc(now)
	r.rollingLatencyPercentile.AddDuration(duration, now)
}

func (r *rollingCmdMetrics) ErrConcurrencyLimitReject() {
	r.errConcurrencyLimitReject.Inc(time.Now())
}

func (r *rollingCmdMetrics) ErrFailure(duration time.Duration) {
	now := time.Now()
	r.errFailure.Inc(now)
	r.rollingLatencyPercentile.AddDuration(duration, now)
}

func (r *rollingCmdMetrics) ErrShortCircuit() {
	r.errShortCircuit.Inc(time.Now())
}

func (r *rollingCmdMetrics) ErrTimeout(duration time.Duration) {
	now := time.Now()
	r.errTimeout.Inc(now)
	r.rollingLatencyPercentile.AddDuration(duration, now)
}

func (r *rollingCmdMetrics) ErrBadRequest(duration time.Duration) {
	now := time.Now()
	r.errBadRequest.Inc(now)
	r.rollingLatencyPercentile.AddDuration(duration, now)
}

var _ RunMetrics = &rollingCmdMetrics{}

func newRollingCmdMetrics(bucketWidth time.Duration, numBuckets int, rollingPercentileBucketWidth time.Duration, rollingPercentileNumBuckets int, rollingPercentileBucketSize int, now time.Time) rollingCmdMetrics {
	return rollingCmdMetrics{
		successCount:              fastmath.NewRollingCounter(bucketWidth, numBuckets, now),
		errConcurrencyLimitReject: fastmath.NewRollingCounter(bucketWidth, numBuckets, now),
		errFailure:                fastmath.NewRollingCounter(bucketWidth, numBuckets, now),
		errShortCircuit:           fastmath.NewRollingCounter(bucketWidth, numBuckets, now),
		errTimeout:                fastmath.NewRollingCounter(bucketWidth, numBuckets, now),
		errBadRequest:             fastmath.NewRollingCounter(bucketWidth, numBuckets, now),
		errInterrupt:              fastmath.NewRollingCounter(bucketWidth, numBuckets, now),
		rollingLatencyPercentile:  fastmath.NewRollingPercentile(rollingPercentileBucketWidth, rollingPercentileNumBuckets, rollingPercentileBucketSize, now),
	}
}

type rollingFallbackMetrics struct {
	successCount              fastmath.RollingCounter
	errConcurrencyLimitReject fastmath.RollingCounter
	errFailure                fastmath.RollingCounter
}

func (r *rollingFallbackMetrics) Success(duration time.Duration) {
	r.successCount.Inc(time.Now())
}

func (r *rollingFallbackMetrics) ErrConcurrencyLimitReject() {
	r.errConcurrencyLimitReject.Inc(time.Now())
}

func (r *rollingFallbackMetrics) ErrFailure(duration time.Duration) {
	r.errFailure.Inc(time.Now())
}

var _ FallbackMetric = &rollingFallbackMetrics{}

func newRollingFallbackMetricCollector(bucketWidth time.Duration, numBuckets int, now time.Time) rollingFallbackMetrics {
	return rollingFallbackMetrics{
		successCount:              fastmath.NewRollingCounter(bucketWidth, numBuckets, now),
		errConcurrencyLimitReject: fastmath.NewRollingCounter(bucketWidth, numBuckets, now),
		errFailure:                fastmath.NewRollingCounter(bucketWidth, numBuckets, now),
	}
}

type circuitStats struct {
	// Tracks how many errors we've seen in a time window
	errorsCount fastmath.RollingCounter
	// Tracks how many attempts we've had that were real attempts to run the circuit, but failed
	legitimateAttemptsCount fastmath.RollingCounter
	// Tracks how many attempts we've had that were backed out of because the request was invalid, or because the
	// root context canceled.  These attempts are useful for stat tracking, but are *NOT* counted as real attempts that
	// change the error percentage.
	backedOutAttemptsCount fastmath.RollingCounter
	// All cmd and fallback metrics go here
	cmdMetricCollector      MultiCmdMetricCollector
	fallbackMetricCollector MultiFallbackMetricCollectors

	responseTimeSLO ResponseTimeSLO

	// These are Circuit specific stats that are always tracked.
	builtInRollingCmdMetricCollector      rollingCmdMetrics
	builtInRollingFallbackMetricCollector rollingFallbackMetrics
}

func (c *circuitStats) SetConfigThreadSafe(config CommandProperties) {
	c.responseTimeSLO.MaximumHealthyTime.Set(config.GoSpecific.ResponseTimeSLO.Nanoseconds())
}

// SetConfigNotThreadSafe is only useful during construction before a circuit is being used.  It is not thread safe,
// but will modify all the circuit's internal structs to match what the config wants.  It also doe *NOT* use the
// default configuration parameters.
func (c *circuitStats) SetConfigNotThreadSafe(config CommandProperties) {
	c.SetConfigThreadSafe(config)
	now := config.GoSpecific.Now()
	rollingCounterBucketWidth := time.Duration(config.Metrics.RollingStatsDuration.Nanoseconds() / int64(config.Metrics.RollingStatsNumBuckets))
	c.errorsCount = fastmath.NewRollingCounter(rollingCounterBucketWidth, config.Metrics.RollingStatsNumBuckets, now)
	c.legitimateAttemptsCount = fastmath.NewRollingCounter(rollingCounterBucketWidth, config.Metrics.RollingStatsNumBuckets, now)
	c.backedOutAttemptsCount = fastmath.NewRollingCounter(rollingCounterBucketWidth, config.Metrics.RollingStatsNumBuckets, now)
	c.builtInRollingFallbackMetricCollector = newRollingFallbackMetricCollector(rollingCounterBucketWidth, config.Metrics.RollingStatsNumBuckets, now)

	rollingPercentileBucketWidth := time.Duration(config.Metrics.RollingPercentileDuration.Nanoseconds() / int64(config.Metrics.RollingPercentileNumBuckets))
	c.builtInRollingCmdMetricCollector = newRollingCmdMetrics(rollingCounterBucketWidth, config.Metrics.RollingStatsNumBuckets, rollingPercentileBucketWidth, config.Metrics.RollingPercentileNumBuckets, config.Metrics.RollingPercentileBucketSize, now)

	c.cmdMetricCollector = MultiCmdMetricCollector{
		CmdMetricCollectors: append([]RunMetrics{
			&c.builtInRollingCmdMetricCollector, &c.responseTimeSLO,
		}, config.MetricsCollectors.Run...),
	}
	c.fallbackMetricCollector = MultiFallbackMetricCollectors{
		FallbackMetricCollectors: append([]FallbackMetric{
			&c.builtInRollingFallbackMetricCollector,
		}, config.MetricsCollectors.Fallback...),
	}
}

func (c *circuitStats) errorPercentage(now time.Time) float64 {
	attemptCount := c.legitimateAttemptsCount.RollingSum(now)
	if attemptCount == 0 {
		return 0
	}
	errCount := c.errorsCount.RollingSum(now)
	return float64(errCount) / float64(attemptCount)
}
