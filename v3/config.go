package circuit

import (
	"time"

	"github.com/cep21/circuit/v3/faststats"
)

// Config controls how a circuit operates
type Config struct {
	General   GeneralConfig
	Execution ExecutionConfig
	Fallback  FallbackConfig
	Metrics   MetricsCollectors
}

// GeneralConfig controls the non general logic of the circuit.  Things specific to metrics, execution, or fallback are
// in their own configs
type GeneralConfig struct {
	// if disabled, Execute functions pass to just calling runFunc and do no tracking or fallbacks
	// Note: Java Manager calls this "Enabled".  I call it "Disabled" so the zero struct can fill defaults
	Disabled bool `json:",omitempty"`
	// ForceOpen is https://github.com/Netflix/Hystrix/wiki/Configuration#circuitbreakerforceopen
	ForceOpen bool `json:",omitempty"`
	// ForcedClosed is https://github.com/Netflix/Hystrix/wiki/Configuration#circuitbreakerforceclosed
	ForcedClosed bool `json:",omitempty"`
	// GoLostErrors can receive errors that would otherwise be lost by `Go` executions.  For example, if Go returns
	// early but some long time later an error or panic eventually happens.
	GoLostErrors func(err error, panics interface{}) `json:"-"`
	// ClosedToOpenFactory creates logic that determines if the circuit should go from Closed to Open state.
	// By default, it never opens
	ClosedToOpenFactory func() ClosedToOpen `json:"-"`
	// OpenToClosedFactory creates logic that determines if the circuit should go from Open to Closed state.
	// By default, it never closes
	OpenToClosedFactory func() OpenToClosed `json:"-"`
	// CustomConfig is anything you want.
	CustomConfig map[interface{}]interface{} `json:"-"`
	// TimeKeeper returns the current way to keep time.  You only want to modify this for testing.
	TimeKeeper TimeKeeper `json:"-"`
}

// ExecutionConfig is https://github.com/Netflix/Hystrix/wiki/Configuration#execution
type ExecutionConfig struct {
	// ExecutionTimeout is https://github.com/Netflix/Hystrix/wiki/Configuration#execution.isolation.thread.timeoutInMilliseconds
	Timeout time.Duration
	// MaxConcurrentRequests is https://github.com/Netflix/Hystrix/wiki/Configuration#executionisolationsemaphoremaxconcurrentrequests
	MaxConcurrentRequests int64
	// Normally if the parent context is canceled before a timeout is reached, we don't consider the circuit
	// unhealthy.  Set this to true to consider those circuits unhealthy.
	IgnoreInterrupts bool `json:",omitempty"`
	// IsErrInterrupt should return true if the error from the original context should be considered an interrupt error.
	// The error passed in will be a non-nil error returned by calling `Err()` on the context passed into Run.
	// The default behavior is to consider all errors from the original context interrupt caused errors.
	// Default behaviour:
	// 		IsErrInterrupt: function(e err) bool { return true }
	IsErrInterrupt func(originalContextError error) bool `json:"-"`
}

// FallbackConfig is https://github.com/Netflix/Hystrix/wiki/Configuration#fallback
type FallbackConfig struct {
	// Enabled is opposite of https://github.com/Netflix/Hystrix/wiki/Configuration#circuitbreakerenabled
	// Note: Java Manager calls this "Enabled".  I call it "Disabled" so the zero struct can fill defaults
	Disabled bool `json:",omitempty"`
	// MaxConcurrentRequests is https://github.com/Netflix/Hystrix/wiki/Configuration#fallback.isolation.semaphore.maxConcurrentRequests
	MaxConcurrentRequests int64
}

// MetricsCollectors can receive metrics during a circuit.  They should be fast, as they will
// block circuit operation during function calls.
type MetricsCollectors struct {
	Run      []RunMetrics      `json:"-"`
	Fallback []FallbackMetrics `json:"-"`
	Circuit  []Metrics         `json:"-"`
}

// TimeKeeper allows overriding time to test the circuit
type TimeKeeper struct {
	// Now should simulate time.Now
	Now func() time.Time
	// AfterFunc should simulate time.AfterFunc
	AfterFunc func(time.Duration, func()) *time.Timer
}

// Configurable is anything that can receive configuration changes while live
type Configurable interface {
	// SetConfigThreadSafe can be called while the circuit is currently being used and will modify things that are
	// safe to change live.
	SetConfigThreadSafe(props Config)
	// SetConfigNotThreadSafe should only be called when the circuit is not in use: otherwise it will fail -race
	// detection
	SetConfigNotThreadSafe(props Config)
}

func (t *TimeKeeper) merge(other TimeKeeper) {
	if t.Now == nil {
		t.Now = other.Now
	}
	if t.AfterFunc == nil {
		t.AfterFunc = other.AfterFunc
	}
}

func (c *ExecutionConfig) merge(other ExecutionConfig) {
	if !c.IgnoreInterrupts {
		c.IgnoreInterrupts = other.IgnoreInterrupts
	}
	if c.IsErrInterrupt == nil {
		c.IsErrInterrupt = other.IsErrInterrupt
	}
	if c.MaxConcurrentRequests == 0 {
		c.MaxConcurrentRequests = other.MaxConcurrentRequests
	}
	if c.Timeout == 0 {
		c.Timeout = other.Timeout
	}
}

func (c *FallbackConfig) merge(other FallbackConfig) {
	if c.MaxConcurrentRequests == 0 {
		c.MaxConcurrentRequests = other.MaxConcurrentRequests
	}
	if !c.Disabled {
		c.Disabled = other.Disabled
	}
}

func (g *GeneralConfig) mergeCustomConfig(other GeneralConfig) {
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

func (g *GeneralConfig) merge(other GeneralConfig) {
	if g.ClosedToOpenFactory == nil {
		g.ClosedToOpenFactory = other.ClosedToOpenFactory
	}
	if g.OpenToClosedFactory == nil {
		g.OpenToClosedFactory = other.OpenToClosedFactory
	}
	g.mergeCustomConfig(other)

	if !g.Disabled {
		g.Disabled = other.Disabled
	}

	if !g.ForceOpen {
		g.ForceOpen = other.ForceOpen
	}

	if !g.ForcedClosed {
		g.ForcedClosed = other.ForcedClosed
	}

	if g.GoLostErrors == nil {
		g.GoLostErrors = other.GoLostErrors
	}
	g.TimeKeeper.merge(other.TimeKeeper)
}

func (m *MetricsCollectors) merge(other MetricsCollectors) {
	m.Run = append(m.Run, other.Run...)
	m.Fallback = append(m.Fallback, other.Fallback...)
	m.Circuit = append(m.Circuit, other.Circuit...)
}

// Merge these properties with another command's properties.  Anything set to the zero value, will takes values from
// other.
func (c *Config) Merge(other Config) *Config {
	c.Execution.merge(other.Execution)
	c.Fallback.merge(other.Fallback)
	c.Metrics.merge(other.Metrics)
	c.General.merge(other.General)
	return c
}

// atomicCircuitConfig is used during circuit operations and allows atomic read/write operations.  This lets users
// change config at runtime without requiring locks on common operations
type atomicCircuitConfig struct {
	Execution struct {
		ExecutionTimeout      faststats.AtomicInt64
		MaxConcurrentRequests faststats.AtomicInt64
	}
	Fallback struct {
		Disabled              faststats.AtomicBoolean
		MaxConcurrentRequests faststats.AtomicInt64
	}
	CircuitBreaker struct {
		ForceOpen    faststats.AtomicBoolean
		ForcedClosed faststats.AtomicBoolean
		Disabled     faststats.AtomicBoolean
	}
	GoSpecific struct {
		IgnoreInterrupts faststats.AtomicBoolean
	}
}

func (a *atomicCircuitConfig) reset(config Config) {
	a.CircuitBreaker.ForcedClosed.Set(config.General.ForcedClosed)
	a.CircuitBreaker.ForceOpen.Set(config.General.ForceOpen)
	a.CircuitBreaker.Disabled.Set(config.General.Disabled)

	a.Execution.ExecutionTimeout.Set(config.Execution.Timeout.Nanoseconds())
	a.Execution.MaxConcurrentRequests.Set(config.Execution.MaxConcurrentRequests)

	a.GoSpecific.IgnoreInterrupts.Set(config.Execution.IgnoreInterrupts)

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

var defaultGoSpecificConfig = GeneralConfig{
	ClosedToOpenFactory: neverOpensFactory,
	OpenToClosedFactory: neverClosesFactory,
	TimeKeeper: TimeKeeper{
		Now:       time.Now,
		AfterFunc: time.AfterFunc,
	},
}

var defaultCommandProperties = Config{
	Execution: defaultExecutionConfig,
	Fallback:  defaultFallbackConfig,
	General:   defaultGoSpecificConfig,
}
