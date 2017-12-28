package hystrix

import (
	"time"

	"github.com/cep21/hystrix/internal/fastmath"
)

// ExecutionConfig is https://github.com/Netflix/Hystrix/wiki/Configuration#execution
type ExecutionConfig struct {
	// ExecutionTimeout is https://github.com/Netflix/Hystrix/wiki/Configuration#execution.isolation.thread.timeoutInMilliseconds
	Timeout time.Duration
	// MaxConcurrentRequests is https://github.com/Netflix/Hystrix/wiki/Configuration#executionisolationsemaphoremaxconcurrentrequests
	MaxConcurrentRequests int64
}

func (c *ExecutionConfig) merge(other ExecutionConfig) {
	if c.MaxConcurrentRequests == 0 {
		c.MaxConcurrentRequests = other.MaxConcurrentRequests
	}
	if c.Timeout == 0 {
		c.Timeout = other.Timeout
	}
}

// FallbackConfig is https://github.com/Netflix/Hystrix/wiki/Configuration#fallback
type FallbackConfig struct {
	// Enabled is opposite of https://github.com/Netflix/Hystrix/wiki/Configuration#circuitbreakerenabled
	// Note: Java Hystrix calls this "Enabled".  I call it "Disabled" so the zero struct can fill defaults
	Disabled bool
	// MaxConcurrentRequests is https://github.com/Netflix/Hystrix/wiki/Configuration#fallback.isolation.semaphore.maxConcurrentRequests
	MaxConcurrentRequests int64
}

func (c *FallbackConfig) merge(other FallbackConfig) {
	if c.MaxConcurrentRequests == 0 {
		c.MaxConcurrentRequests = other.MaxConcurrentRequests
	}
	if !c.Disabled {
		c.Disabled = other.Disabled
	}
}

func (c *CircuitBreakerConfig) merge(other CircuitBreakerConfig) {
	if !c.Disabled {
		c.Disabled = other.Disabled
	}
	if c.RequestVolumeThreshold == 0 {
		c.RequestVolumeThreshold = other.RequestVolumeThreshold
	}
	if c.SleepWindow == 0 {
		c.SleepWindow = other.SleepWindow
	}
	if c.ErrorThresholdPercentage == 0 {
		c.ErrorThresholdPercentage = other.ErrorThresholdPercentage
	}
	if !c.ForcedClosed {
		c.ForcedClosed = other.ForcedClosed
	}
	if !c.ForceOpen {
		c.ForceOpen = other.ForceOpen
	}
}

// MetricsConfig is https://github.com/Netflix/Hystrix/wiki/Configuration#metrics
type MetricsConfig struct {
	// Rolling Stats size is https://github.com/Netflix/Hystrix/wiki/Configuration#metricsrollingstatstimeinmilliseconds
	RollingStatsDuration time.Duration
	// RollingStatsNumBuckets is https://github.com/Netflix/Hystrix/wiki/Configuration#metricsrollingstatsnumbuckets
	RollingStatsNumBuckets int

	// RollingPercentileEnabled is opposite of https://github.com/Netflix/Hystrix/wiki/Configuration#metricsrollingpercentileenabled
	RollingPercentileDisabled bool
	// RollingPercentileDuration is https://github.com/Netflix/Hystrix/wiki/Configuration#metricsrollingpercentiletimeinmilliseconds
	RollingPercentileDuration time.Duration
	// RollingPercentileNumBuckets is https://github.com/Netflix/Hystrix/wiki/Configuration#metricsrollingpercentilenumbuckets
	RollingPercentileNumBuckets int
	// RollingPercentileBucketSize is https://github.com/Netflix/Hystrix/wiki/Configuration#metricsrollingpercentilebucketsize
	RollingPercentileBucketSize int
}

func (c *MetricsConfig) merge(other MetricsConfig) {
	if c.RollingStatsDuration == 0 {
		c.RollingStatsDuration = other.RollingStatsDuration
	}
	if c.RollingStatsNumBuckets == 0 {
		c.RollingStatsNumBuckets = other.RollingStatsNumBuckets
	}
	if !c.RollingPercentileDisabled {
		c.RollingPercentileDisabled = other.RollingPercentileDisabled
	}
	if c.RollingPercentileDuration == 0 {
		c.RollingPercentileDuration = other.RollingPercentileDuration
	}
	if c.RollingPercentileNumBuckets == 0 {
		c.RollingPercentileNumBuckets = other.RollingPercentileNumBuckets
	}
	if c.RollingPercentileBucketSize == 0 {
		c.RollingPercentileBucketSize = other.RollingPercentileBucketSize
	}
}

// CircuitBreakerConfig is https://github.com/Netflix/Hystrix/wiki/Configuration#circuit-breaker
type CircuitBreakerConfig struct {
	// RequestVolumeThreshold is https://github.com/Netflix/Hystrix/wiki/Configuration#circuitbreakerrequestvolumethreshold
	RequestVolumeThreshold int64
	// SleepWindow is https://github.com/Netflix/Hystrix/wiki/Configuration#circuitbreakersleepwindowinmilliseconds
	SleepWindow time.Duration
	// ErrorThresholdPercentage is https://github.com/Netflix/Hystrix/wiki/Configuration#circuitbreakererrorthresholdpercentage
	ErrorThresholdPercentage int64
	// if disabled, Execute functions pass to just calling runFunc and do no tracking or fallbacks
	// Note: Java Hystrix calls this "Enabled".  I call it "Disabled" so the zero struct can fill defaults
	Disabled bool
	// ForceOpen is https://github.com/Netflix/Hystrix/wiki/Configuration#circuitbreakerforceopen
	ForceOpen bool
	// ForcedClosed is https://github.com/Netflix/Hystrix/wiki/Configuration#circuitbreakerforceclosed
	ForcedClosed bool
}

// CommandProperties is https://github.com/Netflix/Hystrix/wiki/Configuration#command-properties
type CommandProperties struct {
	Execution         ExecutionConfig
	Fallback          FallbackConfig
	CircuitBreaker    CircuitBreakerConfig
	Metrics           MetricsConfig
	MetricsCollectors MetricsCollectors
	GoSpecific        GoSpecificConfig
}

// GoSpecificConfig is settings that aren't in the Java Hystrix implementation.
type GoSpecificConfig struct {
	// Normally if the parent context is canceled before a timeout is reached, we don't consider the circuit
	// unhealth.  Set this to true to consider those circuits unhealthy.
	IgnoreInterrputs bool
	// If true, *all* internal stat tracking will not be enabled.  You cannot change this property at runtime, since it
	// takes optimization steps that aren't allowed.  Only use this if you really need the extra ns
	DisableAllStats bool
	// Track to report an SLO similar to "99% of requests should respond correctly within 300 ms"
	// This is the duration part.  Will allow metric reporting and gathering of the number of good requests <= that
	// amount, compared to the number of requests not.
	// This value should be much smaller than the timeout of the circuit, which is an upper bound on how long to wait.
	// This metric is more around "how long to -should- you have to wait", not "what is the longest you will wait"
	ResponseTimeSLO time.Duration

	// ClosedToOpenFactory creates logic that determines if the circuit should go from Closed to Open state.
	// By default, it uses the Hystrix model of opening a circuit after a threshold and % as reached.
	ClosedToOpenFactory func() ClosedToOpen `json:"-"`
	// OpenToClosedFactory creates logic that determines if the circuit should go from Open to Closed state.
	// By default, it uses the Hystrix model of allowing a single connection and switching if the connection is
	// Successful
	OpenToClosedFactory func() OpenToClosed `json:"-"`
	// CustomConfig is anything you want.  It is passed along the circuit to create logic for ClosedToOpenFactory
	// and OpenToClosedFactory configuration.  These maps are merged.
	CustomConfig map[interface{}]interface{} `json:"-"`
	// TimeKeeper returns the current way to keep time.  You only want to modify this for testing.
	TimeKeeper TimeKeeper `json:"-"`
	// GoLostErrors can receive errors that would otherwise be lost by `Go` executions.  For example, if Go returns
	// early but some long time later an error or panic eventually happens.
	GoLostErrors func(err error, panics interface{}) `json:"-"`
}

// TimeKeeper allows overriding time to test the circuit
type TimeKeeper struct {
	// Now should simulate time.Now
	Now func() time.Time
	// AfterFunc should simulate time.AfterFunc
	AfterFunc func(time.Duration, func()) *time.Timer
}

func (t *TimeKeeper) merge(other TimeKeeper) {
	if t.Now == nil {
		t.Now = other.Now
	}
	if t.AfterFunc == nil {
		t.AfterFunc = other.AfterFunc
	}
}

func (g *GoSpecificConfig) mergeCustomConfig(other GoSpecificConfig) {
	if len(other.CustomConfig) != 0 {
		if g.CustomConfig == nil {
			g.CustomConfig = make(map[interface{}]interface{}, len(other.CustomConfig))
		}
		for k, v := range other.CustomConfig {
			if _, exists := g.CustomConfig[k]; !exists {
				g.CustomConfig[k] = v
			}
		}
	}
}

func (g *GoSpecificConfig) merge(other GoSpecificConfig) {
	if !g.IgnoreInterrputs {
		g.IgnoreInterrputs = other.IgnoreInterrputs
	}
	if g.ResponseTimeSLO == 0 {
		g.ResponseTimeSLO = other.ResponseTimeSLO
	}
	if g.ClosedToOpenFactory == nil {
		g.ClosedToOpenFactory = other.ClosedToOpenFactory
	}
	if g.OpenToClosedFactory == nil {
		g.OpenToClosedFactory = other.OpenToClosedFactory
	}
	g.mergeCustomConfig(other)

	if !g.DisableAllStats {
		g.DisableAllStats = other.DisableAllStats
	}
	if g.GoLostErrors == nil {
		g.GoLostErrors = other.GoLostErrors
	}
	g.TimeKeeper.merge(other.TimeKeeper)
}

// MetricsCollectors can receive metrics during a circuit.  They should be fast, as they will
// block circuit operation during function calls.
type MetricsCollectors struct {
	Run             []RunMetrics               `json:"-"`
	Fallback        []FallbackMetric           `json:"-"`
	ResponseTimeSLO []ResponseTimeSLOCollector `json:"-"`
}

func (m *MetricsCollectors) merge(other MetricsCollectors) {
	m.Run = append(m.Run, other.Run...)
	m.Fallback = append(m.Fallback, other.Fallback...)
}

// merge these properties with another command's properties.  Anything set to the zero value, will takes values from
// other.
func (c *CommandProperties) merge(other CommandProperties) {
	c.Execution.merge(other.Execution)
	c.Fallback.merge(other.Fallback)
	c.CircuitBreaker.merge(other.CircuitBreaker)
	c.Metrics.merge(other.Metrics)
	c.MetricsCollectors.merge(other.MetricsCollectors)
	c.GoSpecific.merge(other.GoSpecific)
}

// atomicCircuitConfig is used during circuit operations and allows atomic read/write operations.  This lets users
// change config at runtime without requiring locks on common operations
type atomicCircuitConfig struct {
	CircuitBreaker struct {
		ForceOpen                fastmath.AtomicBoolean
		ForcedClosed             fastmath.AtomicBoolean
		Disabled                 fastmath.AtomicBoolean
		RequestVolumeThreshold   fastmath.AtomicInt64
		ErrorThresholdPercentage fastmath.AtomicInt64
	}
	Execution struct {
		ExecutionTimeout      fastmath.AtomicInt64
		MaxConcurrentRequests fastmath.AtomicInt64
	}
	GoSpecific struct {
		IgnoreInterrputs fastmath.AtomicBoolean
		ResponseTimeSLO  fastmath.AtomicInt64
	}
	Fallback struct {
		Disabled              fastmath.AtomicBoolean
		MaxConcurrentRequests fastmath.AtomicInt64
	}
}

func (a *atomicCircuitConfig) reset(config CommandProperties) {
	a.CircuitBreaker.ForcedClosed.Set(config.CircuitBreaker.ForcedClosed)
	a.CircuitBreaker.ForceOpen.Set(config.CircuitBreaker.ForceOpen)
	a.CircuitBreaker.Disabled.Set(config.CircuitBreaker.Disabled)
	a.CircuitBreaker.RequestVolumeThreshold.Set(config.CircuitBreaker.RequestVolumeThreshold)
	a.CircuitBreaker.ErrorThresholdPercentage.Set(config.CircuitBreaker.ErrorThresholdPercentage)

	a.Execution.ExecutionTimeout.Set(config.Execution.Timeout.Nanoseconds())
	a.Execution.MaxConcurrentRequests.Set(config.Execution.MaxConcurrentRequests)

	a.GoSpecific.IgnoreInterrputs.Set(config.GoSpecific.IgnoreInterrputs)
	a.GoSpecific.ResponseTimeSLO.Set(config.GoSpecific.ResponseTimeSLO.Nanoseconds())

	a.Fallback.Disabled.Set(config.Fallback.Disabled)
	a.Fallback.MaxConcurrentRequests.Set(config.Fallback.MaxConcurrentRequests)
}

var defaultExecutionConfig = ExecutionConfig{
	Timeout:               time.Second,
	MaxConcurrentRequests: 10,
}

var defaultFallbackConfig = FallbackConfig{
	MaxConcurrentRequests: 10,
}

var defaultCircuitBreakerConfig = CircuitBreakerConfig{
	RequestVolumeThreshold:   20,
	SleepWindow:              5 * time.Second,
	ErrorThresholdPercentage: 50,
}

var defaultMetricsConfig = MetricsConfig{
	RollingStatsDuration:        10 * time.Second,
	RollingStatsNumBuckets:      10,
	RollingPercentileDisabled:   true,
	RollingPercentileDuration:   60 * time.Second,
	RollingPercentileNumBuckets: 6,
	RollingPercentileBucketSize: 100,
}

var defaultGoSpecificConfig = GoSpecificConfig{
	ResponseTimeSLO:     time.Millisecond * 300,
	ClosedToOpenFactory: newErrorPercentageCheck,
	OpenToClosedFactory: newSleepyOpenToClose,
	TimeKeeper: TimeKeeper{
		Now:       time.Now,
		AfterFunc: time.AfterFunc,
	},
}

var defaultCommandProperties = CommandProperties{
	Execution:      defaultExecutionConfig,
	Fallback:       defaultFallbackConfig,
	CircuitBreaker: defaultCircuitBreakerConfig,
	Metrics:        defaultMetricsConfig,
	GoSpecific:     defaultGoSpecificConfig,
}
