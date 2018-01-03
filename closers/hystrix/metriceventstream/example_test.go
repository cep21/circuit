package metriceventstream_test

import (
	"net/http"

	"log"

	"github.com/cep21/circuit"
	"github.com/cep21/circuit/closers/hystrix/metriceventstream"
)

// This example creates an event stream handler, starts it, then later closes the handler
func ExampleMetricEventStream() {
	h := circuit.Manager{}
	es := metriceventstream.MetricEventStream{
		Hystrix: &h,
	}
	go func() {
		if err := es.Start(); err != nil {
			log.Fatal(err)
		}
	}()
	http.Handle("/hystrix.stream", &es)
	// ...
	if err := es.Close(); err != nil {
		log.Fatal(err)
	}
	// Output:
}
