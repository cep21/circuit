package hystrix

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetricEventStream(t *testing.T) {
	h := &Hystrix{}
	c := h.MustCreateCircuit("hello-world", CommandProperties{})
	if err := c.Execute(context.Background(), alwaysPasses, nil); err != nil {
		t.Error("no error expected from always passes")
	}

	eventStream := MetricEventStream{
		Hystrix:      h,
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
