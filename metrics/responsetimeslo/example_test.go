package responsetimeslo_test

import (
	"time"

	"github.com/cep21/circuit"
	"github.com/cep21/circuit/metrics/responsetimeslo"
)

func ExampleFactory() {
	sloTrackerFactory := responsetimeslo.Factory{
		Config: responsetimeslo.Config{
			// Consider requests faster than 20 ms as passing
			MaximumHealthyTime: time.Millisecond * 20,
		},
		// Pass in your collector here: for example, statsd
		CollectorConstructors: nil,
	}
	h := circuit.Manager{
		DefaultCircuitProperties: []circuit.CommandPropertiesConstructor{sloTrackerFactory.CommandProperties},
	}
	h.CreateCircuit("circuit-with-slo")
	// Output:
}
