package hystrix

import (
	"time"

	"github.com/cep21/hystrix/internal/fastmath"
)

// CommandProperties is https://github.com/Netflix/Hystrix/wiki/Configuration#command-properties
type CommandProperties struct {
	Execution         ExecutionConfig
	Fallback          FallbackConfig
	MetricsCollectors MetricsCollectors
	GoSpecific        GoSpecificConfig
}

// ExecutionConfig is https://github.com/Netflix/Hystrix/wiki/Configuration#execution
type ExecutionConfig struct {
	// ExecutionTimeout is https://github.com/Netflix/Hystrix/wiki/Configuration#execution.isolation.thread.timeoutInMilliseconds
	Timeout time.Duration
	// MaxConcurrentRequests is https://github.com/Netflix/Hystrix/wiki/Configuration#executionisolationsemaphoremaxconcurrentrequests
	MaxConcurrentRequests int64
	// if disabled, Execute functions pass to just calling runFunc and do no tracking or fallbacks
	// Note: Java Hystrix calls this "Enabled".  I call it "Disabled" so the zero struct can fill defaults
	Disabled bool `json:",omitempty"`
	// ForceOpen is https://github.com/Netflix/Hystrix/wiki/Configuration#circuitbreakerforceopen
	ForceOpen bool `json:",omitempty"`
	// ForcedClosed is https://github.com/Netflix/Hystrix/wiki/Configuration#circuitbreakerforceclosed
	ForcedClosed bool `json:",omitempty"`
}

// FallbackConfig is https://github.com/Netflix/Hystrix/wiki/Configuration#fallback
type FallbackConfig struct {
	// Enabled is opposite of https://github.com/Netflix/Hystrix/wiki/Configuration#circuitbreakerenabled
	// Note: Java Hystrix calls this "Enabled".  I call it "Disabled" so the zero struct can fill defaults
	Disabled bool `json:",omitempty"`
	// MaxConcurrentRequests is https://github.com/Netflix/Hystrix/wiki/Configuration#fallback.isolation.semaphore.maxConcurrentRequests
	MaxConcurrentRequests int64
}

// MetricsCollectors can receive metrics during a circuit.  They should be fast, as they will
// block circuit operation during function calls.
type MetricsCollectors struct {
	Run      []RunMetrics     `json:"-"`
	Fallback []FallbackMetric `json:"-"`
	Circuit []CircuitMetrics `json:"-"`
}

// GoSpecificConfig is settings that aren't in the Java Hystrix implementation.
type GoSpecificConfig struct {
	// Normally if the parent context is canceled before a timeout is reached, we don't consider the circuit
	// unhealth.  Set this to true to consider those circuits unhealthy.
	IgnoreInterrputs bool `json:",omitempty"`
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

// Configurable is anything that can receive configuration changes while live
type Configurable interface {
	// SetConfigThreadSafe can be called while the circuit is currently being used and will modify things that are
	// safe to change live.
	SetConfigThreadSafe(props CommandProperties)
	// SetConfigNotThreadSafe should only be called when the circuit is not in use: otherwise it will fail -race
	// detection
	SetConfigNotThreadSafe(props CommandProperties)
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
	if g.ClosedToOpenFactory == nil {
		g.ClosedToOpenFactory = other.ClosedToOpenFactory
	}
	if g.OpenToClosedFactory == nil {
		g.OpenToClosedFactory = other.OpenToClosedFactory
	}
	g.mergeCustomConfig(other)

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
func (c *CommandProperties) Merge(other CommandProperties) *CommandProperties {
	c.Execution.merge(other.Execution)
	c.Fallback.merge(other.Fallback)
	c.MetricsCollectors.merge(other.MetricsCollectors)
	c.GoSpecific.merge(other.GoSpecific)
	return c
}

// atomicCircuitConfig is used during circuit operations and allows atomic read/write operations.  This lets users
// change config at runtime without requiring locks on common operations
type atomicCircuitConfig struct {
	Execution struct {
		ExecutionTimeout      fastmath.AtomicInt64
		MaxConcurrentRequests fastmath.AtomicInt64
	}
	Fallback struct {
		Disabled              fastmath.AtomicBoolean
		MaxConcurrentRequests fastmath.AtomicInt64
	}
	CircuitBreaker struct {
		ForceOpen    fastmath.AtomicBoolean
		ForcedClosed fastmath.AtomicBoolean
		Disabled     fastmath.AtomicBoolean
	}
	GoSpecific struct {
		IgnoreInterrputs fastmath.AtomicBoolean
	}
}

func (a *atomicCircuitConfig) reset(config CommandProperties) {
	a.CircuitBreaker.ForcedClosed.Set(config.Execution.ForcedClosed)
	a.CircuitBreaker.ForceOpen.Set(config.Execution.ForceOpen)
	a.CircuitBreaker.Disabled.Set(config.Execution.Disabled)

	a.Execution.ExecutionTimeout.Set(config.Execution.Timeout.Nanoseconds())
	a.Execution.MaxConcurrentRequests.Set(config.Execution.MaxConcurrentRequests)

	a.GoSpecific.IgnoreInterrputs.Set(config.GoSpecific.IgnoreInterrputs)

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

var defaultGoSpecificConfig = GoSpecificConfig{
	ClosedToOpenFactory: neverOpensFactory,
	OpenToClosedFactory: neverClosesFactory,
	TimeKeeper: TimeKeeper{
		Now:       time.Now,
		AfterFunc: time.AfterFunc,
	},
}

var defaultCommandProperties = CommandProperties{
	Execution:  defaultExecutionConfig,
	Fallback:   defaultFallbackConfig,
	GoSpecific: defaultGoSpecificConfig,
}
