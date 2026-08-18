package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// TestExample runs the example's circuits against an in-process server and checks that both the expvar and the
// hystrix event stream endpoints describe them.  The background goroutines panic (via mustPass/mustFail) if a
// circuit stops behaving the way the example expects, so this doubles as a smoke test of the library.
func TestExample(t *testing.T) {
	log.SetOutput(io.Discard)
	defer log.SetOutput(os.Stderr)
	handler, es := newHandler(time.Millisecond)
	startErr := make(chan error, 1)
	go func() { startErr <- es.Start() }()
	srv := httptest.NewServer(handler)
	defer srv.Close()

	printInstructions(srv.Listener.Addr().String())

	t.Run("expvar", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/debug/vars")
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := resp.Body.Close(); err != nil {
				t.Error(err)
			}
		}()
		var vars struct {
			Hystrix map[string]struct {
				Name string `json:"name"`
			} `json:"hystrix"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&vars); err != nil {
			t.Fatal(err)
		}
		for _, expected := range []string{"always-fails", "always-passes", "floppy-circuit", "throttled-circuit"} {
			if c, ok := vars.Hystrix[expected]; !ok || c.Name != expected {
				t.Errorf("circuit %q missing from expvar output: %v", expected, vars.Hystrix)
			}
		}
	})

	t.Run("hystrix.stream", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/hystrix.stream", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := resp.Body.Close(); err != nil {
				t.Error(err)
			}
		}()
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
			t.Errorf("unexpected content type %q", ct)
		}
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), `"always-times-out"`) {
				return
			}
		}
		t.Fatalf("never saw a circuit on the event stream: %v", scanner.Err())
	})

	if err := es.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-startErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("event stream did not stop after Close")
	}
}
