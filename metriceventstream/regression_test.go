package metriceventstream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cep21/circuit/v4"
)

// The SSE endpoint must send its status line/headers immediately, even before the first event (or with zero
// circuits registered), or clients see no response at all.
func TestRegression_HeadersFlushedImmediately(t *testing.T) {
	es := &MetricEventStream{Manager: &circuit.Manager{}, TickDuration: time.Hour}
	defer func() { _ = es.Close() }()
	srv := httptest.NewServer(es)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("client never received response headers from the SSE endpoint: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
}
