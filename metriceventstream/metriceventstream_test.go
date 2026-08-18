package metriceventstream

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cep21/circuit/v4"
	"github.com/cep21/circuit/v4/closers/hystrix"
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

func TestMetricEventStream_HystrixProperties(t *testing.T) {
	h := &circuit.Manager{}
	h.MustCreateCircuit("plain", circuit.Config{})
	h.MustCreateCircuit("hystrix", circuit.Config{
		General: circuit.GeneralConfig{
			ClosedToOpenFactory: hystrix.OpenerFactory(hystrix.ConfigureOpener{
				ErrorThresholdPercentage: 37,
				RequestVolumeThreshold:   11,
			}),
			OpenToClosedFactory: hystrix.CloserFactory(hystrix.ConfigureCloser{
				SleepWindow: 1234 * time.Millisecond,
			}),
		},
	})
	eventStream := MetricEventStream{Manager: h, TickDuration: time.Millisecond}
	startResult := make(chan error)
	go func() { startResult <- eventStream.Start() }()

	recorder := httptest.NewRecorder()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	eventStream.ServeHTTP(recorder, httptest.NewRequest("GET", "/hystrix.stream", nil).WithContext(ctx))
	if err := eventStream.Close(); err != nil {
		t.Fatal(err)
	}
	<-startResult

	seen := map[string]streamCmdMetric{}
	for _, line := range strings.Split(recorder.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var m streamCmdMetric
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data:")), &m); err != nil {
			t.Fatalf("%q: %v", line, err)
		}
		seen[m.Name] = m
	}
	hm, ok := seen["hystrix"]
	if !ok {
		t.Fatalf("never saw the hystrix circuit in %q", recorder.Body.String())
	}
	if hm.CircuitBreakerErrorThresholdPercent != 37 || hm.CircuitBreakerRequestVolumeThreshold != 11 || hm.CircuitBreakerSleepWindow != 1234 {
		t.Errorf("hystrix properties not reported: %+v", hm)
	}
	pm, ok := seen["plain"]
	if !ok {
		t.Fatalf("never saw the plain circuit in %q", recorder.Body.String())
	}
	if pm.CircuitBreakerErrorThresholdPercent != 0 || pm.CircuitBreakerRequestVolumeThreshold != 0 || pm.CircuitBreakerSleepWindow != 0 {
		t.Errorf("plain circuit should not report hystrix properties: %+v", pm)
	}
}

// nonFlushingWriter hides the Flush method of the underlying ResponseWriter
type nonFlushingWriter struct {
	header http.Header
	code   int
	body   strings.Builder
}

func (n *nonFlushingWriter) Header() http.Header         { return n.header }
func (n *nonFlushingWriter) Write(b []byte) (int, error) { return n.body.Write(b) }
func (n *nonFlushingWriter) WriteHeader(statusCode int)  { n.code = statusCode }

func TestMetricEventStream_RequiresFlusher(t *testing.T) {
	eventStream := MetricEventStream{Manager: &circuit.Manager{}}
	defer func() { _ = eventStream.Close() }()
	rw := &nonFlushingWriter{header: http.Header{}}
	eventStream.ServeHTTP(rw, httptest.NewRequest("GET", "/hystrix.stream", nil))
	if rw.code != http.StatusInternalServerError {
		t.Fatalf("expected a 500 for a writer that cannot flush, got %d: %s", rw.code, rw.body.String())
	}
}
