package metriceventstream

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cep21/circuit/v4"
)


func TestMetricEventStream_DoubleClose(t *testing.T) {
	eventStream := MetricEventStream{}
	if err := eventStream.Close(); err != nil {
		t.Fatal("first close should not error:", err)
	}
	// Second close should not panic
	if err := eventStream.Close(); err != nil {
		t.Fatal("second close should not error:", err)
	}
}

func TestMetricEventStream(t *testing.T) {
	h := &circuit.Manager{}
	c := h.MustCreateCircuit("hello-world", circuit.Config{})
	if err := c.Execute(context.Background(), func(_ context.Context) error {
		return nil
	}, nil); err != nil {
		t.Error("no error expected from always passes")
	}

	eventStream := MetricEventStream{
		Manager:      h,
		TickDuration: time.Millisecond * 10,
	}
	eventStreamStartResult := make(chan error)
	go func() {
		eventStreamStartResult <- eventStream.Start()
	}()

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "http://localhost:8080/hystrix.stream", nil)
	// Just get 500 ms of data
	reqContext, cancelData := context.WithTimeout(context.Background(), time.Millisecond*100)
	defer cancelData()
	req = req.WithContext(reqContext)
	eventStream.ServeHTTP(recorder, req)

	bodyOfRequest := recorder.Body.String()
	if !strings.Contains(bodyOfRequest, "hello-world") {
		t.Error("Did not see my hello world circuit in the body")
	}
	if err := eventStream.Close(); err != nil {
		t.Error("no error expected from closing event stream")
	}
	// And finally wait for start to end
	<-eventStreamStartResult
}
