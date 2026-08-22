package hystrixadaptive

import (
	"time"

	"github.com/cep21/circuit/v4"
	"github.com/cep21/circuit/v4/closers/hystrix"
)

// ConfigureAdaptive holds adaptive policy and embeds hystrix.ConfigureOpener; it does not set circuit execution timeouts
type ConfigureAdaptive struct {
	hystrix.ConfigureOpener

	// BaselineLatency is the nominal fast path; successes below it decrease extra
	BaselineLatency time.Duration
	// MaxExtraLatency caps extra; slow-success threshold is roughly baseline+extra; at the cap, timeout deferral ends if inner ShouldOpen
	MaxExtraLatency time.Duration
	// IncreaseExtra added to extra on each timeout and on each success slower than baseline+current extra
	IncreaseExtra time.Duration
	// DecreaseExtra subtracted from extra when a success finishes faster than BaselineLatency
	DecreaseExtra time.Duration
	// MinTimeoutRatioToDefer is the rolling timeouts/(timeouts+failures) above which ShouldOpen may defer while 0 < extra < MaxExtraLatency
	MinTimeoutRatioToDefer float64
}

// Merge copies missing fields from other
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

// defaultConfigureAdaptive is the default adaptive configuration
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

// Factory merges hystrix.Factory with adaptive configuration
type Factory struct {
	hystrix.Factory

	ConfigureAdaptive       ConfigureAdaptive
	CreateConfigureAdaptive []func(circuitName string) ConfigureAdaptive
}

// Configure returns circuit.Config with adaptive ClosedToOpen from this factory
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
