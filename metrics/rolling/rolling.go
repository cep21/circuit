package rolling

import (
	"context"
	"expvar"
	"time"

	"sync"

	"github.com/cep21/circuit/v4"
	"github.com/cep21/circuit/v4/faststats"
	"github.com/cep21/circuit/v4/internal/evar"
)

// StatFactory helps the process of making stat collectors for circuit breakers
type StatFactory struct {
	RunConfig      RunStatsConfig
	FallbackConfig FallbackStatsConfig

	runStatsByCircuit      map[string]*RunStats
	fallbackStatsByCircuit map[string]*FallbackStats
	mu                     sync.Mutex
}

// CreateConfig is a config factory that associates stat collection with the circuit
func (s *StatFactory) CreateConfig(circuitName string) circuit.Config {
	rs := RunStats{}
	cfg := RunStatsConfig{}
	cfg.Merge(s.RunConfig)
	cfg.Merge(defaultRunStatsConfig)
	rs.SetConfigNotThreadSafe(cfg)

	fs := FallbackStats{}
	fcfg := FallbackStatsConfig{}
	fcfg.Merge(s.FallbackConfig)
	fcfg.Merge(defaultFallbackStatsConfig)
	fs.SetConfigNotThreadSafe(fcfg)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runStatsByCircuit == nil {
		s.runStatsByCircuit = make(map[string]*RunStats, 1)
	}
	if s.fallbackStatsByCircuit == nil {
		s.fallbackStatsByCircuit = make(map[string]*FallbackStats, 1)
	}
	s.runStatsByCircuit[circuitName] = &rs
	s.fallbackStatsByCircuit[circuitName] = &fs
	return circuit.Config{
		Metrics: circuit.MetricsCollectors{
			Run:      []circuit.RunMetrics{&rs},
			Fallback: []circuit.FallbackMetrics{&fs},
		},
	}
}

// RunStats returns the run stats for a circuit, or nil if there is none
func (s *StatFactory) RunStats(circuitName string) *RunStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runStatsByCircuit[circuitName]
}

// FallbackStats returns the fallback stats for a circuit, or nil if there is none
func (s *StatFactory) FallbackStats(circuitName string) *FallbackStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fallbackStatsByCircuit[circuitName]
}

// FindCommandMetrics searches a circuit for the previously stored run stats.  Returns nil if never set.
func FindCommandMetrics(c *circuit.Circuit) *RunStats {
	for _, r := range c.CmdMetricCollector {
		if ret, ok := r.(*RunStats); ok {
			return ret
		}
	}
	return nil
}

// FindFallbackMetrics searches a circuit for the previously stored fallback stats.  Returns nil if never set.
func FindFallbackMetrics(c *circuit.Circuit) *FallbackStats {
	for _, r := range c.FallbackMetricCollector {
		if ret, ok := r.(*FallbackStats); ok {
			return ret
		}
	}
	return nil
}

// RunStats tracks rolling windows of callback counts
type RunStats struct {
	Successes                  faststats.RollingCounter
	ErrConcurrencyLimitRejects faststats.RollingCounter
	ErrFailures                faststats.RollingCounter
	ErrShortCircuits           faststats.RollingCounter
	ErrTimeouts                faststats.RollingCounter
	ErrBadRequests             faststats.RollingCounter
	ErrInterrupts              faststats.RollingCounter

	// It is analogous to https://github.com/Netflix/Hystrix/wiki/Metrics-and-Monitoring#latency-percentiles-hystrixcommandrun-execution-gauge
	Latencies faststats.RollingPercentile

	mu     sync.Mutex
	config RunStatsConfig
}

// Var allows exposing RunStats on expvar
func (r *RunStats) Var() expvar.Var {
	return expvar.Func(func() interface{} {
		ret := map[string]interface{}{
			"Successes":                  evar.ForExpvar(&r.Successes),
			"ErrConcurrencyLimitRejects": evar.ForExpvar(&r.ErrConcurrencyLimitRejects),
			"ErrFailures":                evar.ForExpvar(&r.ErrFailures),
			"ErrShortCircuits":           evar.ForExpvar(&r.ErrShortCircuits),
			"ErrTimeouts":                evar.ForExpvar(&r.ErrTimeouts),
			"ErrBadRequests":             evar.ForExpvar(&r.ErrBadRequests),
			"ErrInterrupts":              evar.ForExpvar(&r.ErrInterrupts),
			"Latencies":                  evar.ForExpvar(&r.Latencies),
		}
		return ret
	})
}

// RunStatsConfig configures a RunStats
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

// Merge this config with another
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
	Now:                         time.Now,
	RollingStatsDuration:        10 * time.Second,
	RollingStatsNumBuckets:      10,
	RollingPercentileDuration:   60 * time.Second,
	RollingPercentileNumBuckets: 6,
	RollingPercentileBucketSize: 100,
}

// Config returns the current configuration
func (r *RunStats) Config() RunStatsConfig {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.config
}

// SetConfigNotThreadSafe updates the RunStats buckets
func (r *RunStats) SetConfigNotThreadSafe(config RunStatsConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.config = config
	now := config.Now()
	bucketWidth := time.Duration(config.RollingStatsDuration.Nanoseconds() / int64(config.RollingStatsNumBuckets))
	numBuckets := config.RollingStatsNumBuckets
	rollingPercentileBucketWidth := time.Duration(config.RollingPercentileDuration.Nanoseconds() / int64(config.RollingPercentileNumBuckets))
	rollingPercentileNumBuckets := config.RollingPercentileNumBuckets
	rollingPercentileBucketSize := config.RollingPercentileBucketSize

	r.Successes = faststats.NewRollingCounter(bucketWidth, numBuckets, now)
	r.ErrConcurrencyLimitRejects = faststats.NewRollingCounter(bucketWidth, numBuckets, now)
	r.ErrFailures = faststats.NewRollingCounter(bucketWidth, numBuckets, now)
	r.ErrShortCircuits = faststats.NewRollingCounter(bucketWidth, numBuckets, now)
	r.ErrTimeouts = faststats.NewRollingCounter(bucketWidth, numBuckets, now)
	r.ErrBadRequests = faststats.NewRollingCounter(bucketWidth, numBuckets, now)
	r.ErrInterrupts = faststats.NewRollingCounter(bucketWidth, numBuckets, now)
	r.Latencies = faststats.NewRollingPercentile(rollingPercentileBucketWidth, rollingPercentileNumBuckets, rollingPercentileBucketSize, now)
}

// Success increments the Successes bucket
func (r *RunStats) Success(_ context.Context, now time.Time, duration time.Duration) {
	r.Successes.Inc(now)
	r.Latencies.AddDuration(duration, now)
}

// ErrInterrupt increments the ErrInterrupts bucket
func (r *RunStats) ErrInterrupt(_ context.Context, now time.Time, duration time.Duration) {
	r.ErrInterrupts.Inc(now)
	r.Latencies.AddDuration(duration, now)
}

// ErrConcurrencyLimitReject increments the ErrConcurrencyLimitReject bucket
func (r *RunStats) ErrConcurrencyLimitReject(_ context.Context, now time.Time) {
	r.ErrConcurrencyLimitRejects.Inc(now)
}

// ErrFailure increments the ErrFailure bucket
func (r *RunStats) ErrFailure(_ context.Context, now time.Time, duration time.Duration) {
	r.ErrFailures.Inc(now)
	r.Latencies.AddDuration(duration, now)
}

// ErrShortCircuit increments the ErrShortCircuit bucket
func (r *RunStats) ErrShortCircuit(_ context.Context, now time.Time) {
	r.ErrShortCircuits.Inc(now)
}

// ErrTimeout increments the ErrTimeout bucket
func (r *RunStats) ErrTimeout(_ context.Context, now time.Time, duration time.Duration) {
	r.ErrTimeouts.Inc(now)
	r.Latencies.AddDuration(duration, now)
}

// ErrBadRequest increments the ErrBadRequest bucket
func (r *RunStats) ErrBadRequest(_ context.Context, now time.Time, duration time.Duration) {
	r.ErrBadRequests.Inc(now)
	r.Latencies.AddDuration(duration, now)
}

// ErrorPercentage returns [0.0 - 1.0] what % of request are considered failing in the rolling window.
func (r *RunStats) ErrorPercentage() float64 {
	return r.ErrorPercentageAt(time.Now())
}

// LegitimateAttemptsAt returns the sum of errors and successes
func (r *RunStats) LegitimateAttemptsAt(now time.Time) int64 {
	return r.Successes.RollingSumAt(now) + r.ErrorsAt(now)
}

// ErrorsAt returns the # of errors at a moment in time (errors are timeouts and failures)
func (r *RunStats) ErrorsAt(now time.Time) int64 {
	return r.ErrFailures.RollingSumAt(now) + r.ErrTimeouts.RollingSumAt(now)
}

// ErrorPercentageAt is [0.0 - 1.0] errors/legitimate
func (r *RunStats) ErrorPercentageAt(now time.Time) float64 {
	attemptCount := r.LegitimateAttemptsAt(now)
	if attemptCount == 0 {
		return 0
	}
	errCount := r.ErrorsAt(now)
	return float64(errCount) / float64(attemptCount)
}

// FallbackStats tracks fallback metrics in rolling buckets
type FallbackStats struct {
	Successes                  faststats.RollingCounter
	ErrConcurrencyLimitRejects faststats.RollingCounter
	ErrFailures                faststats.RollingCounter
}

// Var allows FallbackStats on expvar
func (r *FallbackStats) Var() expvar.Var {
	return expvar.Func(func() interface{} {
		return map[string]interface{}{
			"Successes":                  r.Successes.TotalSum(),
			"ErrConcurrencyLimitRejects": r.ErrConcurrencyLimitRejects.TotalSum(),
			"ErrFailures":                r.ErrFailures.TotalSum(),
		}
	})
}

// Success increments the Success bucket
func (r *FallbackStats) Success(_ context.Context, now time.Time, _ time.Duration) {
	r.Successes.Inc(now)
}

// ErrConcurrencyLimitReject increments the ErrConcurrencyLimitReject bucket
func (r *FallbackStats) ErrConcurrencyLimitReject(_ context.Context, now time.Time) {
	r.ErrConcurrencyLimitRejects.Inc(now)
}

// ErrFailure increments the ErrFailure bucket
func (r *FallbackStats) ErrFailure(_ context.Context, now time.Time, _ time.Duration) {
	r.ErrFailures.Inc(now)
}

// FallbackStatsConfig configures how to track fallback stats
type FallbackStatsConfig struct {
	// Rolling Stats size is https://github.com/Netflix/Hystrix/wiki/Configuration#metricsrollingstatstimeinmilliseconds
	RollingStatsDuration time.Duration
	// Now should simulate time.Now
	Now func() time.Time
	// RollingStatsNumBuckets is https://github.com/Netflix/Hystrix/wiki/Configuration#metricsrollingstatsnumbuckets
	RollingStatsNumBuckets int
}

// Merge this config with another
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
	Now:                    time.Now,
	RollingStatsDuration:   10 * time.Second,
	RollingStatsNumBuckets: 10,
}

var _ circuit.FallbackMetrics = &FallbackStats{}

// SetConfigNotThreadSafe sets the configuration for fallback stats
func (r *FallbackStats) SetConfigNotThreadSafe(config FallbackStatsConfig) {
	now := config.Now()
	bucketWidth := time.Duration(config.RollingStatsDuration.Nanoseconds() / int64(config.RollingStatsNumBuckets))
	numBuckets := config.RollingStatsNumBuckets

	r.Successes = faststats.NewRollingCounter(bucketWidth, numBuckets, now)
	r.ErrConcurrencyLimitRejects = faststats.NewRollingCounter(bucketWidth, numBuckets, now)
	r.ErrFailures = faststats.NewRollingCounter(bucketWidth, numBuckets, now)
}
