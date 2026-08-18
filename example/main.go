/*
Run this simple Go program to see what circuits look like.  You can explore their hystrix stream and expvar values.
*/
package main

import (
	"context"
	"errors"
	"expvar"
	"flag"
	"log"
	"math/rand"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/cep21/circuit/v4"
	"github.com/cep21/circuit/v4/closers/hystrix"
	"github.com/cep21/circuit/v4/metriceventstream"
	"github.com/cep21/circuit/v4/metrics/rolling"
)

// nolint:lll
const exampleURL = "http://localhost:7979/hystrix-dashboard/monitor/monitor.html?streams=%5B%7B%22name%22%3A%22%22%2C%22stream%22%3A%22http%3A%2F%2Flocalhost%3A8123%2Fhystrix.stream%22%2C%22auth%22%3A%22%22%2C%22delay%22%3A%22%22%7D%5D"

func main() {
	interval := flag.Duration("interval", time.Millisecond*100, "Setup duration between metric ticks")
	addr := flag.String("addr", "127.0.0.1:8123", "Address to listen on")
	flag.Parse()
	sock, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	printInstructions(sock.Addr().String())
	handler, es := newHandler(*interval)
	go func() {
		mustPass(es.Start())
	}()
	srv := http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	log.Fatal(srv.Serve(sock))
}

// newHandler creates the example circuits and returns a handler serving /hystrix.stream and /debug/vars, plus the
// event stream backing /hystrix.stream.  The caller must Start and eventually Close the stream.
func newHandler(interval time.Duration) (http.Handler, *metriceventstream.MetricEventStream) {
	f := rolling.StatFactory{}
	h := &circuit.Manager{
		DefaultCircuitProperties: []circuit.CommandPropertiesConstructor{f.CreateConfig},
	}
	if expvar.Get("hystrix") == nil {
		expvar.Publish("hystrix", h.Var())
	}
	es := &metriceventstream.MetricEventStream{
		Manager: h,
	}
	createBackgroundCircuits(h, interval)
	mux := http.NewServeMux()
	mux.Handle("/hystrix.stream", es)
	mux.Handle("/debug/vars", expvar.Handler())
	return mux, es
}

func printInstructions(addr string) {
	log.Printf("Serving on socket %s\n", addr)
	log.Println("To view the stream, execute: ")
	log.Printf("  curl http://%s/hystrix.stream\n", addr)
	log.Println()
	log.Println("To view expvar metrics, visit expvar in your browser")
	log.Printf("  http://%s/debug/vars\n", addr)
	log.Println()
	log.Println("To view a dashboard, follow the instructions at https://github.com/Netflix-Skunkworks/hystrix-dashboard#run-via-gradle")
	log.Println("  git clone git@github.com:Netflix-Skunkworks/hystrix-dashboard.git")
	log.Println("  cd hystrix-dashboard")
	log.Println("  ./gradlew jettyRun")
	log.Println()
	log.Printf("Then, add the stream http://%s/hystrix.stream\n", addr)
	log.Println()
	log.Println("A URL directly to the page usually looks something like this (adjust the port if you changed -addr)")
	log.Printf("   %s\n", exampleURL)
}

func mustFail(err error) {
	if err == nil {
		panic("Expected a failure")
	}
}

func mustPass(err error) {
	if err != nil {
		panic(err)
	}
}

func setupAlwaysFails(h *circuit.Manager, tickInterval time.Duration) {
	failureCircuit := h.MustCreateCircuit("always-fails", circuit.Config{
		General: circuit.GeneralConfig{
			OpenToClosedFactory: hystrix.CloserFactory(hystrix.ConfigureCloser{}),
			ClosedToOpenFactory: hystrix.OpenerFactory(hystrix.ConfigureOpener{}),
		},
	})
	go func() {
		for range time.Tick(tickInterval) {
			mustFail(failureCircuit.Execute(context.Background(), func(ctx context.Context) error {
				return errors.New("a failure")
			}, nil))
		}
	}()
}

func setupBadRequest(h *circuit.Manager, tickInterval time.Duration) {
	failingBadRequest := h.MustCreateCircuit("always-fails-bad-request", circuit.Config{
		General: circuit.GeneralConfig{
			OpenToClosedFactory: hystrix.CloserFactory(hystrix.ConfigureCloser{}),
			ClosedToOpenFactory: hystrix.OpenerFactory(hystrix.ConfigureOpener{}),
		},
	})
	go func() {
		for range time.Tick(tickInterval) {
			mustFail(failingBadRequest.Execute(context.Background(), func(ctx context.Context) error {
				return circuit.SimpleBadRequest{Err: errors.New("bad user input")}
			}, nil))
		}
	}()
}

func setupFailsOriginalContext(h *circuit.Manager, tickInterval time.Duration) {
	failingOriginalContextCanceled := h.MustCreateCircuit("always-fails-original-context", circuit.Config{
		General: circuit.GeneralConfig{
			OpenToClosedFactory: hystrix.CloserFactory(hystrix.ConfigureCloser{}),
			ClosedToOpenFactory: hystrix.OpenerFactory(hystrix.ConfigureOpener{}),
		},
	})
	go func() {
		for range time.Tick(tickInterval) {
			endedContext, cancel := context.WithCancel(context.Background())
			cancel()
			mustFail(failingOriginalContextCanceled.Execute(endedContext, func(ctx context.Context) error {
				return errors.New("a failure, but it's not my fault")
			}, nil))
		}
	}()
}

func setupAlwaysPasses(h *circuit.Manager, tickInterval time.Duration) {
	passingCircuit := h.MustCreateCircuit("always-passes", circuit.Config{})
	go func() {
		for range time.Tick(tickInterval) {
			mustPass(passingCircuit.Execute(context.Background(), func(ctx context.Context) error {
				return nil
			}, nil))
		}
	}()
}

func setupTimesOut(h *circuit.Manager, tickInterval time.Duration) {
	timeOutCircuit := h.MustCreateCircuit("always-times-out", circuit.Config{
		Execution: circuit.ExecutionConfig{
			Timeout: time.Millisecond,
		},
	})
	go func() {
		for range time.Tick(tickInterval) {
			mustFail(timeOutCircuit.Execute(context.Background(), func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			}, nil))
		}
	}()
}

func setupFallsBack(h *circuit.Manager, tickInterval time.Duration) {
	fallbackCircuit := h.MustCreateCircuit("always-falls-back", circuit.Config{
		Execution: circuit.ExecutionConfig{
			Timeout: time.Millisecond,
		},
	})
	go func() {
		for range time.Tick(tickInterval) {
			mustPass(fallbackCircuit.Execute(context.Background(), func(ctx context.Context) error {
				return errors.New("a failure")
			}, func(ctx context.Context, err error) error {
				return nil
			}))
		}
	}()
}

func setupRandomExecutionTime(h *circuit.Manager, tickInterval time.Duration) {
	randomExecutionTime := h.MustCreateCircuit("random-execution-time", circuit.Config{
		Execution: circuit.ExecutionConfig{},
	})
	go func() {
		for range time.Tick(tickInterval) {
			mustPass(randomExecutionTime.Execute(context.Background(), func(ctx context.Context) error {
				select {
				// Some time between 0 and 50ms
				case <-time.After(time.Duration(int64(float64(time.Millisecond.Nanoseconds()*50) * rand.Float64()))):
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}, func(ctx context.Context, err error) error {
				return nil
			}))
		}
	}()
}

func setupFloppyCircuit(h *circuit.Manager, tickInterval time.Duration) {
	// Flop every 3 seconds, try to recover very quickly
	floppyCircuit := h.MustCreateCircuit("floppy-circuit", circuit.Config{
		General: circuit.GeneralConfig{
			OpenToClosedFactory: hystrix.CloserFactory(hystrix.ConfigureCloser{
				//		// This should allow a new request every 10 milliseconds
				SleepWindow: time.Millisecond * 10,
			}),
			ClosedToOpenFactory: hystrix.OpenerFactory(hystrix.ConfigureOpener{
				RequestVolumeThreshold: 2,
			}),
		},
	})
	floppyCircuitPasses := int64(1)
	go func() {
		isPassing := true
		for range time.Tick(time.Second * 3) {
			if isPassing {
				atomic.StoreInt64(&floppyCircuitPasses, 0)
			} else {
				atomic.StoreInt64(&floppyCircuitPasses, 1)
			}
			isPassing = !isPassing
		}
	}()
	for i := 0; i < 10; i++ {
		go func() {
			totalErrors := 0
			for range time.Tick(tickInterval) {
				// Errors flop back and forth
				err := floppyCircuit.Execute(context.Background(), func(ctx context.Context) error {
					if atomic.LoadInt64(&floppyCircuitPasses) == 1 {
						return nil
					}
					return errors.New("i'm failing now")
				}, func(ctx context.Context, err error) error {
					return nil
				})
				if err != nil {
					totalErrors++
				}
			}
		}()
	}
}

func setupThrottledCircuit(h *circuit.Manager, tickInterval time.Duration) {
	throttledCircuit := h.MustCreateCircuit("throttled-circuit", circuit.Config{
		Execution: circuit.ExecutionConfig{
			MaxConcurrentRequests: 2,
		},
	})
	// 100 threads, every 100ms, someone will get throttled
	for i := 0; i < 100; i++ {
		go func() {
			totalErrors := 0
			for range time.Tick(tickInterval) {
				// Some pass (not throttled) and some don't (throttled)
				err := throttledCircuit.Execute(context.Background(), func(ctx context.Context) error {
					select {
					// Some time between 0 and 50ms
					case <-time.After(time.Duration(int64(float64(time.Millisecond.Nanoseconds()*50) * rand.Float64()))):
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}, nil)
				if err != nil {
					totalErrors++
				}
			}
		}()
	}
}

func createBackgroundCircuits(h *circuit.Manager, tickInterval time.Duration) {
	setupAlwaysFails(h, tickInterval)
	setupBadRequest(h, tickInterval)
	setupFailsOriginalContext(h, tickInterval)
	setupAlwaysPasses(h, tickInterval)
	setupTimesOut(h, tickInterval)
	setupFallsBack(h, tickInterval)
	setupRandomExecutionTime(h, tickInterval)
	setupFloppyCircuit(h, tickInterval)
	setupThrottledCircuit(h, tickInterval)
}
