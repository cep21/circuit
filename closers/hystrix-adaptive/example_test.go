package hystrixadaptive_test

import (
	"fmt"
	"time"

	"github.com/cep21/circuit/v4"
	"github.com/cep21/circuit/v4/closers/hystrix"
	hystrixadaptive "github.com/cep21/circuit/v4/closers/hystrix-adaptive"
)

// This example wires a manager with the adaptive Hystrix opener: same rolling error logic as
// closers/hystrix, plus adaptive ShouldOpen behavior (see package doc)
func ExampleFactory() {
	configuration := hystrixadaptive.Factory{
		Factory: hystrix.Factory{
			ConfigureOpener: hystrix.ConfigureOpener{
				RequestVolumeThreshold: 10,
			},
			ConfigureCloser: hystrix.ConfigureCloser{},
		},
		ConfigureAdaptive: hystrixadaptive.ConfigureAdaptive{
			BaselineLatency:        100 * time.Millisecond,
			MaxExtraLatency:        200 * time.Millisecond,
			IncreaseExtra:          10 * time.Millisecond,
			DecreaseExtra:          10 * time.Millisecond,
			MinTimeoutRatioToDefer: 0.85,
		},
	}
	h := circuit.Manager{
		DefaultCircuitProperties: []circuit.CommandPropertiesConstructor{configuration.Configure},
	}
	c := h.MustCreateCircuit("adaptive-hystrix")
	fmt.Println("circuit:", c.Name())
	// Output:
	// circuit: adaptive-hystrix
}

// You can use OpenerFactory directly when you build a [circuit.Config] yourself and pair it
// with [hystrix.CloserFactory] (or another OpenToClosed implementation)
func ExampleOpenerFactory() {
	cfg := circuit.Config{
		General: circuit.GeneralConfig{
			ClosedToOpenFactory: hystrixadaptive.OpenerFactory(hystrixadaptive.ConfigureAdaptive{
				ConfigureOpener: hystrix.ConfigureOpener{
					RequestVolumeThreshold: 10,
				},
			}),
			OpenToClosedFactory: hystrix.CloserFactory(hystrix.ConfigureCloser{}),
		},
	}
	c := circuit.NewCircuitFromConfig("custom-opener", cfg)
	fmt.Println("circuit:", c.Name())
	// Output:
	// circuit: custom-opener
}

// Baseline latency and other adaptive fields can be updated at runtime together with the
// embedded Hystrix opener thresholds via [hystrixadaptive.Opener.SetConfigThreadSafe]
// Use [hystrixadaptive.NewOpener] when you need the concrete type without a type assertion
func ExampleOpener_SetConfigThreadSafe() {
	ao := hystrixadaptive.NewOpener(hystrixadaptive.ConfigureAdaptive{})
	fmt.Println("default baseline:", ao.Config().BaselineLatency)
	ao.SetConfigThreadSafe(hystrixadaptive.ConfigureAdaptive{
		BaselineLatency: 50 * time.Millisecond,
	})
	fmt.Println("new baseline:", ao.Config().BaselineLatency)
	// Output:
	// default baseline: 100ms
	// new baseline: 50ms
}
