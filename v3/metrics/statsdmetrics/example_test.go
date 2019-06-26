package statsdmetrics_test

import (
	"github.com/cactus/go-statsd-client/statsd"
	"github.com/cep21/circuit/v3"
	"github.com/cep21/circuit/v3/metrics/statsdmetrics"
)

// Our interface should be satisfied by go-statsd-client
var _ statsdmetrics.StatSender = statsd.StatSender(nil)

// This example shows how to inject a statsd metric collector into a circuit
func ExampleCommandFactory_CommandProperties() {
	// This factory allows us to report statsd metrics from the circuit
	f := statsdmetrics.CommandFactory{
		StatSender: &statsd.NoopClient{},
	}

	// Wire the statsd factory into the circuit manager
	h := circuit.Manager{
		DefaultCircuitProperties: []circuit.CommandPropertiesConstructor{f.CommandProperties},
	}
	// This created circuit will now use statsd
	h.MustCreateCircuit("using-statsd")
	// Output:
}
