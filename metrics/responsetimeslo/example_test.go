package responsetimeslo_test

import (
	"time"

	"github.com/cep21/circuit"
	"github.com/cep21/circuit/metrics/responsetimeslo"
)

// This example creates a SLO tracker that counts failures at less than 20 ms.  You
// will need to provide your own Collectors.
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
