package hystrixadaptive

import (
	"time"

	"github.com/cep21/circuit/v4"
	"github.com/cep21/circuit/v4/closers/hystrix"
)

// ConfigureAdaptive configures the adaptive opener and embeds hystrix.ConfigureOpener
type ConfigureAdaptive struct {
	hystrix.ConfigureOpener

	// BaselineLatency is the expected healthy latency (e.g. matches a strict timeout budget
	// before adaptive headroom is applied)
	BaselineLatency time.Duration
	// MaxExtraLatency caps how much additional headroom can accumulate (e.g. 200ms above baseline)
	MaxExtraLatency time.Duration
	// IncreaseExtra is added to the headroom when a run is slower than baseline+headroom or on timeout
	IncreaseExtra time.Duration
	// DecreaseExtra is subtracted from headroom on fast successes (duration below BaselineLatency)
	DecreaseExtra time.Duration
	// MinTimeoutRatioToDefer is the minimum rolling ratio of timeouts to (timeouts+failures)
	// required before ShouldOpen defers to the inner opener when headroom is non-zero
	MinTimeoutRatioToDefer float64
}

// Merge fills zero values from other
func (c *ConfigureAdaptive) Merge(other ConfigureAdaptive) {
	c.ConfigureOpener.Merge(other.ConfigureOpener)
	if c.BaselineLatency == 0 {
		c.BaselineLatency = other.BaselineLatency
	}
	if c.MaxExtraLatency == 0 {
		c.MaxExtraLatency = other.MaxExtraLatency
	}
	if c.IncreaseExtra == 0 {
		c.IncreaseExtra = other.IncreaseExtra
	}
	if c.DecreaseExtra == 0 {
		c.DecreaseExtra = other.DecreaseExtra
	}
	if c.MinTimeoutRatioToDefer == 0 {
		c.MinTimeoutRatioToDefer = other.MinTimeoutRatioToDefer
	}
}

// defaultConfigureAdaptive is the default configuration for the adaptive opener
var defaultConfigureAdaptive = ConfigureAdaptive{
	ConfigureOpener: hystrix.ConfigureOpener{
		RequestVolumeThreshold:   20,
		ErrorThresholdPercentage: 50,
		Now:                      time.Now,
		NumBuckets:               10,
		RollingDuration:          10 * time.Second,
	},
	BaselineLatency:        100 * time.Millisecond,
	MaxExtraLatency:        200 * time.Millisecond,
	IncreaseExtra:          10 * time.Millisecond,
	DecreaseExtra:          10 * time.Millisecond,
	MinTimeoutRatioToDefer: 0.85,
}

// Factory builds circuit configs that use the adaptive opener with optional hystrix closer wiring
type Factory struct {
	hystrix.Factory

	ConfigureAdaptive       ConfigureAdaptive
	CreateConfigureAdaptive []func(circuitName string) ConfigureAdaptive
}

// Configure returns a circuit.Config with adaptive ClosedToOpen and hystrix OpenToClosed
func (f *Factory) Configure(circuitName string) circuit.Config {
	cfg := f.Factory.Configure(circuitName)
	adaptiveCfg := ConfigureAdaptive{}
	for i := len(f.CreateConfigureAdaptive) - 1; i >= 0; i-- {
		adaptiveCfg.Merge(f.CreateConfigureAdaptive[i](circuitName))
	}
	adaptiveCfg.Merge(f.ConfigureAdaptive)
	cfg.General.ClosedToOpenFactory = OpenerFactory(adaptiveCfg)
	return cfg
}
