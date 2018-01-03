package metriceventstream_test

import (
	"net/http"

	"log"

	"github.com/cep21/circuit"
	"github.com/cep21/circuit/metriceventstream"
	"github.com/cep21/circuit/metrics/rolling"
)

// This example creates an event stream handler, starts it, then later closes the handler
func ExampleMetricEventStream() {
	// metriceventstream uses rolling stats to report circuit information
	sf := rolling.StatFactory{}
	h := circuit.Manager{
		DefaultCircuitProperties: []circuit.CommandPropertiesConstructor{sf.CreateConfig},
	}
	es := metriceventstream.MetricEventStream{
		Manager: &h,
	}
	go func() {
		if err := es.Start(); err != nil {
			log.Fatal(err)
		}
	}()
	// ES is a http.Handler, so you can pass it directly to your mux
	http.Handle("/hystrix.stream", &es)
	// ...
	if err := es.Close(); err != nil {
		log.Fatal(err)
	}
	// Output:
}
