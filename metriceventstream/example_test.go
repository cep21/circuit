package metriceventstream_test

import (
	"net/http"

	"github.com/cep21/hystrix"
	"github.com/cep21/hystrix/metriceventstream"
)

// This example creates an event stream handler, starts it, then later closes the handler
func ExampleMetricEventStream() {
	h := hystrix.Hystrix{}
	es := metriceventstream.MetricEventStream{
		Hystrix: &h,
	}
	go es.Start()
	http.Handle("/hystrix.stream", &es)
	// ...
	es.Close()
	// Output:
}
