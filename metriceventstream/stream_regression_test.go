package metriceventstream

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cep21/circuit/v4"
	"github.com/cep21/circuit/v4/internal/clock"
	"github.com/cep21/circuit/v4/metrics/rolling"
)

// firstEvent starts es, connects a real HTTP client and returns the first event on the stream.  A panic inside Start
// (which in production runs on a bare goroutine and takes the process down) fails the test.
func firstEvent(t *testing.T, es *MetricEventStream) streamCmdMetric {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	startDone := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				startDone <- fmt.Errorf("Start panicked: %v", r)
				cancel()
			}
		}()
		startDone <- es.Start()
	}()
	srv := httptest.NewServer(es)
	defer srv.Close()
	defer func() {
		_ = es.Close()
		if err := <-startDone; err != nil {
			t.Fatal(err)
		}
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Errorf("request failed: %v", err)
		return streamCmdMetric{}
	}
	defer func() { _ = resp.Body.Close() }()
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if data, ok := strings.CutPrefix(scanner.Text(), "data:"); ok {
			var m streamCmdMetric
			if err := json.Unmarshal([]byte(data), &m); err != nil {
				t.Fatal(err)
			}
			return m
		}
	}
	t.Errorf("no event received: %v", scanner.Err())
	return streamCmdMetric{}
}

// SetConfigThreadSafe does not merge defaults, so after a live update written like ExampleCircuit_SetConfigThreadSafe
// Config().General.TimeKeeper.Now is nil (the circuit itself keeps using the clock it was built with).
func TestStreamSurvivesLiveConfigWithoutTimeKeeper(t *testing.T) {
	liveUpdate := circuit.Config{
		Execution: circuit.ExecutionConfig{MaxConcurrentRequests: 12},
	}
	t.Run("no stats", func(t *testing.T) {
		h := &circuit.Manager{}
		h.MustCreateCircuit("changes-at-runtime").SetConfigThreadSafe(liveUpdate)
		m := firstEvent(t, &MetricEventStream{Manager: h, TickDuration: time.Millisecond})
		if m.Name != "changes-at-runtime" || m.ExecutionIsolationSemaphoreMaxConcurrentRequests != 12 {
			t.Fatalf("unexpected event %+v", m)
		}
	})
	t.Run("custom clock", func(t *testing.T) {
		mc := &clock.MockClock{}
		start := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
		mc.Set(start)
		sf := rolling.StatFactory{
			RunConfig:      rolling.RunStatsConfig{Now: mc.Now},
			FallbackConfig: rolling.FallbackStatsConfig{Now: mc.Now},
		}
		h := &circuit.Manager{DefaultCircuitProperties: []circuit.CommandPropertiesConstructor{sf.CreateConfig}}
		c := h.MustCreateCircuit("mock", circuit.Config{
			General: circuit.GeneralConfig{TimeKeeper: circuit.TimeKeeper{Now: mc.Now, AfterFunc: mc.AfterFunc}},
		})
		c.SetConfigThreadSafe(liveUpdate)
		if err := c.Run(context.Background(), func(context.Context) error { return nil }); err != nil {
			t.Fatal(err)
		}
		m := firstEvent(t, &MetricEventStream{Manager: h, TickDuration: time.Millisecond})
		// Reading with the wall clock would roll the windows 20+ years ahead of the clock events are recorded with
		if m.RollingCountSuccess != 1 || m.Time != start.UnixMilli() {
			t.Fatalf("stream did not use the stats' clock: success=%d time=%d", m.RollingCountSuccess, m.Time)
		}
	})
}

func circuitWithFixedClock(now time.Time) (*circuit.Circuit, *rolling.RunStats) {
	nowFunc := func() time.Time { return now }
	sf := rolling.StatFactory{
		RunConfig:      rolling.RunStatsConfig{Now: nowFunc},
		FallbackConfig: rolling.FallbackStatsConfig{Now: nowFunc},
	}
	cfg := sf.CreateConfig("c")
	cfg.General.TimeKeeper.Now = nowFunc
	return circuit.NewCircuitFromConfig("c", cfg), sf.RunStats("c")
}

// int64(100 * float) truncates values like 56.99999: the dashboard showed 56% for 57 errors out of 100, i.e. below a
// 57% threshold the hystrix Opener had just tripped on.
func TestErrorPercentageIsExact(t *testing.T) {
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for errs := int64(0); errs <= 100; errs++ {
		c, rs := circuitWithFixedClock(now)
		for i := int64(0); i < 100; i++ {
			if i < errs {
				rs.ErrFailure(context.Background(), now, time.Millisecond)
			} else {
				rs.Success(context.Background(), now, time.Millisecond)
			}
		}
		if got := collectCommandMetrics(c).ErrorPct; got != errs {
			t.Errorf("%d errors out of 100 requests reported as errorPercentage=%d", errs, got)
		}
	}
}

// The health numbers of one event have to agree with each other even while requests are completing.
func TestHealthCountsAreConsistent(t *testing.T) {
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	c, rs := circuitWithFixedClock(now)
	var stop atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			rs.ErrFailure(context.Background(), now, time.Millisecond)
		}
	}()
	defer wg.Wait()
	defer stop.Store(true)
	for rs.ErrFailures.TotalSum() < 200_000 {
		m := collectCommandMetrics(c)
		if m.ErrorCount > m.RequestCount || m.ErrorPct > 100 || m.ErrorCount != m.RollingCountFailure+m.RollingCountTimeout {
			t.Fatalf("inconsistent event: requestCount=%d errorCount=%d errorPercentage=%d rollingCountFailure=%d rollingCountTimeout=%d",
				m.RequestCount, m.ErrorCount, m.ErrorPct, m.RollingCountFailure, m.RollingCountTimeout)
		}
	}
}

// A HEAD response has no body, so the client considers it complete after the headers and reuses the connection.  If
// the handler keeps "streaming", the next request on that connection is never served.
func TestHeadRequestDoesNotWedgeConnection(t *testing.T) {
	es := &MetricEventStream{Manager: &circuit.Manager{}, TickDuration: time.Hour}
	mux := http.NewServeMux()
	mux.Handle("/hystrix.stream", es)
	mux.HandleFunc("/health", func(http.ResponseWriter, *http.Request) {})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	defer func() { _ = es.Close() }()

	do := func(method string, path string) *http.Response {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, method, srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		_ = resp.Body.Close()
		return resp
	}
	if resp := do(http.MethodHead, "/hystrix.stream"); resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("unexpected HEAD response %v", resp)
	}
	do(http.MethodGet, "/health")
}

func TestEveryRunCountIsReported(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	c, rs := circuitWithFixedClock(now)
	for count, record := range map[int]func(){
		1:  func() { rs.Success(ctx, now, 0) },
		2:  func() { rs.ErrFailure(ctx, now, 0) },
		4:  func() { rs.ErrTimeout(ctx, now, 0) },
		8:  func() { rs.ErrInterrupt(ctx, now, 0) },
		16: func() { rs.ErrBadRequest(ctx, now, 0) },
		32: func() { rs.ErrShortCircuit(ctx, now) },
		64: func() { rs.ErrConcurrencyLimitReject(ctx, now) },
	} {
		for i := 0; i < count; i++ {
			record()
		}
	}
	m := collectCommandMetrics(c)
	got := []int64{
		m.RequestCount, m.ErrorCount, m.ErrorPct,
		m.RollingCountSuccess, m.RollingCountFailure, m.RollingCountTimeout, m.RollingCountBadRequests,
		m.RollingCountShortCircuited, m.RollingCountSemaphoreRejected,
		m.TotalCountSuccess, m.TotalCountFailure, m.TotalCountTimeout, m.TotalCountBadRequests,
		m.TotalCountShortCircuited, m.TotalCountSemaphoreRejected,
	}
	want := []int64{15, 6, 85, 1, 2, 4, 24, 32, 64, 1, 2, 4, 24, 32, 64}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
