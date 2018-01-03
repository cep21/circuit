package statsdmetrics_test

import (
	"github.com/cep21/circuit/metrics/statsdmetrics"
	"github.com/cactus/go-statsd-client/statsd"
	"github.com/cep21/circuit"
)

func ExampleCommandFactory_CommandProperties() {
	// This factory allows us to report statsd metrics from the circuit
	f := statsdmetrics.CommandFactory{
		SubStatter: &statsd.NoopClient{},
	}

	// Wire the statsd factory into the circuit manager
	h := circuit.Manager{
		DefaultCircuitProperties: []circuit.CommandPropertiesConstructor{f.CommandProperties},
	}
	// This created circuit will now use statsd
	h.MustCreateCircuit("using-statsd")
	// Output:
}