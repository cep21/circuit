package rolling

import (
	"time"

	"expvar"

	"github.com/cep21/hystrix"
	"github.com/cep21/hystrix/internal/fastmath"
)

// CollectRollingStats enables stats needed to display metric event streams on a hystrix dashboard, as well as it
// gives easy access to rolling and total latency stats
func CollectRollingStats(runConfig RunStatsConfig, fallbackConfig FallbackStatsConfig) func (string) hystrix.CommandProperties {
	return func(_ string) hystrix.CommandProperties {
		rs := RunStats{}
		runConfig.Merge(defaultRunStatsConfig)
		rs.SetConfigNotThreadSafe(runConfig)
		fs := FallbackStats{}
		fallbackConfig.Merge(defaultFallbackStatsConfig)
		fs.SetConfigNotThreadSafe(fallbackConfig)
		return hystrix.CommandProperties{
			MetricsCollectors: hystrix.MetricsCollectors{
				Run:      []hystrix.RunMetrics{&rs},
				Fallback: []hystrix.FallbackMetric{&fs},
			},
		}
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

func (r *RunStats) Var() expvar.Var {
	return expvar.Func(func() interface{} {
		snap := r.Latencies.Snapshot()
		lats := expvarToVal(snap.Var())
		ret := map[string]interface{}{
			"Successes":                  r.Successes.TotalSum(),
			"ErrConcurrencyLimitRejects": r.ErrConcurrencyLimitRejects.TotalSum(),
			"ErrFailures":                r.ErrFailures.TotalSum(),
			"ErrShortCircuits":           r.ErrShortCircuits.TotalSum(),
			"ErrTimeouts":                r.ErrTimeouts.TotalSum(),
			"ErrBadRequests":             r.ErrBadRequests.TotalSum(),
			"ErrInterrupts":              r.ErrInterrupts.TotalSum(),
		}
		if lats != nil {
			ret["Latencies"] = lats
		}
		return ret
	})
}

func (r *RunStats) SetConfigThreadSafe(config hystrix.CommandProperties) {
}

type RunStatsConfig struct {
	// Now should simulate time.Now
	Now func() time.Time
	// Rolling Stats size is https://github.com/Netflix/Hystrix/wiki/Configuration#metricsrollingstatstimeinmilliseconds
	RollingStatsDuration time.Duration
	// RollingStatsNumBuckets is https://github.com/Netflix/Hystrix/wiki/Configuration#metricsrollingstatsnumbuckets
	RollingStatsNumBuckets int
	// RollingPercentileDuration is https://github.com/Netflix/Hystrix/wiki/Configuration#metricsrollingpercentiletimeinmilliseconds
	RollingPercentileDuration time.Duration
	// RollingPercentileNumBuckets is https://github.com/Netflix/Hystrix/wiki/Configuration#metricsrollingpercentilenumbuckets
	RollingPercentileNumBuckets int
	// RollingPercentileBucketSize is https://github.com/Netflix/Hystrix/wiki/Configuration#metricsrollingpercentilebucketsize
	RollingPercentileBucketSize int
}

func (r *RunStatsConfig) Merge(other RunStatsConfig) {
	if r.Now == nil {
		r.Now = other.Now
	}
	if r.RollingStatsDuration == 0 {
		r.RollingStatsDuration = other.RollingStatsDuration
	}
	if r.RollingStatsNumBuckets == 0 {
		r.RollingStatsNumBuckets = other.RollingStatsNumBuckets
	}
	if r.RollingPercentileDuration == 0 {
		r.RollingPercentileDuration = other.RollingPercentileDuration
	}
	if r.RollingPercentileNumBuckets == 0 {
		r.RollingPercentileNumBuckets = other.RollingPercentileNumBuckets
	}
	if r.RollingPercentileBucketSize == 0 {
		r.RollingPercentileBucketSize = other.RollingPercentileBucketSize
	}
}

var defaultRunStatsConfig = RunStatsConfig{
	Now: time.Now,
	RollingStatsDuration:10 * time.Second,
	RollingStatsNumBuckets:10,
	RollingPercentileDuration:60 * time.Second,
	RollingPercentileNumBuckets:6,
	RollingPercentileBucketSize:100,
}

func (r *RunStats) SetConfigNotThreadSafe(config RunStatsConfig) {
	now := config.Now()
	bucketWidth := time.Duration(config.RollingStatsDuration.Nanoseconds() / int64(config.RollingStatsNumBuckets))
	numBuckets := config.RollingStatsNumBuckets
	rollingPercentileBucketWidth := time.Duration(config.RollingPercentileDuration.Nanoseconds() / int64(config.RollingPercentileNumBuckets))
	rollingPercentileNumBuckets := config.RollingPercentileNumBuckets
	rollingPercentileBucketSize := config.RollingPercentileBucketSize

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

type FallbackStatsConfig struct {
	// Rolling Stats size is https://github.com/Netflix/Hystrix/wiki/Configuration#metricsrollingstatstimeinmilliseconds
	RollingStatsDuration time.Duration
	// Now should simulate time.Now
	Now func() time.Time
	// RollingStatsNumBuckets is https://github.com/Netflix/Hystrix/wiki/Configuration#metricsrollingstatsnumbuckets
	RollingStatsNumBuckets int
}


func (r *FallbackStatsConfig) Merge(other FallbackStatsConfig) {
	if r.Now == nil {
		r.Now = other.Now
	}
	if r.RollingStatsDuration == 0 {
		r.RollingStatsDuration = other.RollingStatsDuration
	}
	if r.RollingStatsNumBuckets == 0 {
		r.RollingStatsNumBuckets = other.RollingStatsNumBuckets
	}
}

var defaultFallbackStatsConfig = FallbackStatsConfig{
	Now: time.Now,
	RollingStatsDuration:10 * time.Second,
	RollingStatsNumBuckets:10,
}

var _ hystrix.FallbackMetric = &FallbackStats{}

func (r *FallbackStats) SetConfigNotThreadSafe(config FallbackStatsConfig) {
	now := config.Now()
	bucketWidth := time.Duration(config.RollingStatsDuration.Nanoseconds() / int64(config.RollingStatsNumBuckets))
	numBuckets := config.RollingStatsNumBuckets

	r.Successes = fastmath.NewRollingCounter(bucketWidth, numBuckets, now)
	r.ErrConcurrencyLimitRejects = fastmath.NewRollingCounter(bucketWidth, numBuckets, now)
	r.ErrFailures = fastmath.NewRollingCounter(bucketWidth, numBuckets, now)
}
