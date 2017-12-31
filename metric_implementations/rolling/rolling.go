package rolling

import (
	"time"

	"expvar"

	"github.com/cep21/hystrix"
	"github.com/cep21/hystrix/internal/fastmath"
)

// CollectRollingStats enables stats needed to display metric event streams on a hystrix dashboard, as well as it
// gives easy access to rolling and total latency stats
func CollectRollingStats(_ string) hystrix.CommandProperties {
	return hystrix.CommandProperties{
		MetricsCollectors: hystrix.MetricsCollectors{
			Run:      []hystrix.RunMetrics{&RunStats{}},
			Fallback: []hystrix.FallbackMetric{&FallbackStats{}},
		},
	}
}

func FindCommandMetrics(c *hystrix.Circuit) *RunStats {
	for _, r := range c.CmdMetricCollector {
		if ret, ok := r.(*RunStats); ok {
			return ret
		}
	}
	return nil
}

func FindFallbackMetrics(c *hystrix.Circuit) *FallbackStats {
	for _, r := range c.FallbackMetricCollector {
		if ret, ok := r.(*FallbackStats); ok {
			return ret
		}
	}
	return nil
}

type RunStats struct {
	Successes                  fastmath.RollingCounter
	ErrConcurrencyLimitRejects fastmath.RollingCounter
	ErrFailures                fastmath.RollingCounter
	ErrShortCircuits           fastmath.RollingCounter
	ErrTimeouts                fastmath.RollingCounter
	ErrBadRequests             fastmath.RollingCounter
	ErrInterrupts              fastmath.RollingCounter

	// It is analogous to https://github.com/Netflix/Hystrix/wiki/Metrics-and-Monitoring#latency-percentiles-hystrixcommandrun-execution-gauge
	Latencies fastmath.RollingPercentile
}

func (r *RunStats) Var() expvar.Var {
	return expvar.Func(func() interface{} {
		snap := r.Latencies.Snapshot()
		return map[string]interface{}{
			"Successes":                  r.Successes.TotalSum(),
			"ErrConcurrencyLimitRejects": r.ErrConcurrencyLimitRejects.TotalSum(),
			"ErrFailures":                r.ErrFailures.TotalSum(),
			"ErrShortCircuits":           r.ErrShortCircuits.TotalSum(),
			"ErrTimeouts":                r.ErrTimeouts.TotalSum(),
			"ErrBadRequests":             r.ErrBadRequests.TotalSum(),
			"ErrInterrupts":              r.ErrInterrupts.TotalSum(),
			"Latencies": map[string]string{
				"p25":  snap.Percentile(.25).String(),
				"p50":  snap.Percentile(.5).String(),
				"p90":  snap.Percentile(.9).String(),
				"p99":  snap.Percentile(.99).String(),
				"mean": snap.Mean().String(),
			},
		}
	})
}

func (r *RunStats) SetConfigThreadSafe(config hystrix.CommandProperties) {

}

func (r *RunStats) SetConfigNotThreadSafe(config hystrix.CommandProperties) {
	now := config.GoSpecific.TimeKeeper.Now()
	bucketWidth := time.Duration(config.Metrics.RollingStatsDuration.Nanoseconds() / int64(config.Metrics.RollingStatsNumBuckets))
	numBuckets := config.Metrics.RollingStatsNumBuckets
	rollingPercentileBucketWidth := time.Duration(config.Metrics.RollingPercentileDuration.Nanoseconds() / int64(config.Metrics.RollingPercentileNumBuckets))
	rollingPercentileNumBuckets := config.Metrics.RollingPercentileNumBuckets
	rollingPercentileBucketSize := config.Metrics.RollingPercentileBucketSize

	r.Successes = fastmath.NewRollingCounter(bucketWidth, numBuckets, now)
	r.ErrConcurrencyLimitRejects = fastmath.NewRollingCounter(bucketWidth, numBuckets, now)
	r.ErrFailures = fastmath.NewRollingCounter(bucketWidth, numBuckets, now)
	r.ErrShortCircuits = fastmath.NewRollingCounter(bucketWidth, numBuckets, now)
	r.ErrTimeouts = fastmath.NewRollingCounter(bucketWidth, numBuckets, now)
	r.ErrBadRequests = fastmath.NewRollingCounter(bucketWidth, numBuckets, now)
	r.ErrInterrupts = fastmath.NewRollingCounter(bucketWidth, numBuckets, now)
	r.Latencies = fastmath.NewRollingPercentile(rollingPercentileBucketWidth, rollingPercentileNumBuckets, rollingPercentileBucketSize, now)
}

func (r *RunStats) Success(duration time.Duration) {
	now := time.Now()
	r.Successes.Inc(now)
	r.Latencies.AddDuration(duration, now)
}

func (r *RunStats) ErrInterrupt(duration time.Duration) {
	now := time.Now()
	r.ErrInterrupts.Inc(now)
	r.Latencies.AddDuration(duration, now)
}

func (r *RunStats) ErrConcurrencyLimitReject() {
	r.ErrConcurrencyLimitRejects.Inc(time.Now())
}

func (r *RunStats) ErrFailure(duration time.Duration) {
	now := time.Now()
	r.ErrFailures.Inc(now)
	r.Latencies.AddDuration(duration, now)
}

func (r *RunStats) ErrShortCircuit() {
	r.ErrShortCircuits.Inc(time.Now())
}

func (r *RunStats) ErrTimeout(duration time.Duration) {
	now := time.Now()
	r.ErrTimeouts.Inc(now)
	r.Latencies.AddDuration(duration, now)
}

func (r *RunStats) ErrBadRequest(duration time.Duration) {
	now := time.Now()
	r.ErrBadRequests.Inc(now)
	r.Latencies.AddDuration(duration, now)
}

// ErrorPercentage returns [0.0 - 1.0] what % of request are considered failing in the rolling window.
func (r *RunStats) ErrorPercentage() float64 {
	return r.ErrorPercentageAt(time.Now())
}

func (r *RunStats) LegitimateAttemptsAt(now time.Time) int64 {
	return r.Successes.RollingSumAt(now) + r.ErrorsAt(now)
}

func (r *RunStats) ErrorsAt(now time.Time) int64 {
	return r.ErrFailures.RollingSumAt(now) + r.ErrTimeouts.RollingSumAt(now)
}

func (r *RunStats) ErrorPercentageAt(now time.Time) float64 {
	attemptCount := r.LegitimateAttemptsAt(now)
	if attemptCount == 0 {
		return 0
	}
	errCount := r.ErrorsAt(now)
	return float64(errCount) / float64(attemptCount)
}

type FallbackStats struct {
	Successes                  fastmath.RollingCounter
	ErrConcurrencyLimitRejects fastmath.RollingCounter
	ErrFailures                fastmath.RollingCounter
}

func (r *FallbackStats) Var() expvar.Var {
	return expvar.Func(func() interface{} {
		return map[string]interface{}{
			"Successes":                  r.Successes.TotalSum(),
			"ErrConcurrencyLimitRejects": r.ErrConcurrencyLimitRejects.TotalSum(),
			"ErrFailures":                r.ErrFailures.TotalSum(),
		}
	})
}

func (r *FallbackStats) Success(duration time.Duration) {
	r.Successes.Inc(time.Now())
}

func (r *FallbackStats) ErrConcurrencyLimitReject() {
	r.ErrConcurrencyLimitRejects.Inc(time.Now())
}

func (r *FallbackStats) ErrFailure(duration time.Duration) {
	r.ErrFailures.Inc(time.Now())
}

var _ hystrix.FallbackMetric = &FallbackStats{}

func (r *FallbackStats) SetConfigThreadSafe(config hystrix.CommandProperties) {
}

func (r *FallbackStats) SetConfigNotThreadSafe(config hystrix.CommandProperties) {
	now := config.GoSpecific.TimeKeeper.Now()
	bucketWidth := time.Duration(config.Metrics.RollingStatsDuration.Nanoseconds() / int64(config.Metrics.RollingStatsNumBuckets))
	numBuckets := config.Metrics.RollingStatsNumBuckets

	r.Successes = fastmath.NewRollingCounter(bucketWidth, numBuckets, now)
	r.ErrConcurrencyLimitRejects = fastmath.NewRollingCounter(bucketWidth, numBuckets, now)
	r.ErrFailures = fastmath.NewRollingCounter(bucketWidth, numBuckets, now)
}
