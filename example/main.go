package main

import (
	"context"
	"errors"
	"expvar"
	"log"
	"math/rand"
	"net"
	"net/http"
	"time"

	"sync/atomic"

	"github.com/cep21/hystrix"
)

const exampleURL = "http://localhost:7979/hystrix-dashboard/monitor/monitor.html?streams=%5B%7B%22name%22%3A%22%22%2C%22stream%22%3A%22http%3A%2F%2Flocalhost%3A8123%2Fhystrix.stream%22%2C%22auth%22%3A%22%22%2C%22delay%22%3A%22%22%7D%5D"

func main() {
	h := hystrix.Hystrix{}
	expvar.Publish("hystrix", h.Var())
	es := hystrix.MetricEventStream{
		Hystrix: &h,
	}
	go es.Start()
	createBackgroundCircuits(&h)
	http.Handle("/hystrix.stream", &es)
	sock, err := net.Listen("tcp", ":8123")
	if err != nil {
		panic(err)
	}
	log.Println("Serving on socket :8123")
	log.Println("To view the stream, execute: ")
	log.Println("  curl http://localhost:8123/hystrix.stream")
	log.Println()
	log.Println("To view expvar metrics, visit expvar in your browser")
	log.Println("  http://localhost:8123/debug/vars")
	log.Println()
	log.Println("To view a dashboard, follow the instructions at https://github.com/Netflix/Hystrix/wiki/Dashboard#run-via-gradle")
	log.Println("  git clone git@github.com:Netflix/Hystrix.git")
	log.Println("  cd Hystrix/hystrix-dashboard")
	log.Println("  ../gradlew jettyRun")
	log.Println()
	log.Println("Then, add the stream http://localhost:8123/hystrix.stream")
	log.Println()
	log.Println("A URL directly to the page usually looks something like this")
	log.Printf("   %s\n", exampleURL)
	log.Fatal(http.Serve(sock, nil))
}

func createBackgroundCircuits(h *hystrix.Hystrix) {
	tickInterval := time.Millisecond * 100
	failureCircuit := h.MustCreateCircuit("always-fails", hystrix.CommandProperties{})
	go func() {
		for range time.Tick(tickInterval) {
			failureCircuit.Execute(context.Background(), func(ctx context.Context) error {
				return errors.New("a failure")
			}, nil)
		}
	}()

	failingBadRequest := h.MustCreateCircuit("always-fails-bad-request", hystrix.CommandProperties{})
	go func() {
		for range time.Tick(tickInterval) {
			failingBadRequest.Execute(context.Background(), func(ctx context.Context) error {
				return hystrix.SimpleBadRequest{Err: errors.New("bad user input")}
			}, nil)
		}
	}()

	failingOriginalContextCanceled := h.MustCreateCircuit("always-fails-original-context", hystrix.CommandProperties{})
	go func() {
		for range time.Tick(tickInterval) {
			endedContext, cancel := context.WithCancel(context.Background())
			cancel()
			failingOriginalContextCanceled.Execute(endedContext, func(ctx context.Context) error {
				return errors.New("a failure, but it's not my fault")
			}, nil)
		}
	}()

	passingCircuit := h.MustCreateCircuit("always-passes", hystrix.CommandProperties{})
	go func() {
		for range time.Tick(tickInterval) {
			passingCircuit.Execute(context.Background(), func(ctx context.Context) error {
				return nil
			}, nil)
		}
	}()

	timeOutCircuit := h.MustCreateCircuit("always-times-out", hystrix.CommandProperties{
		Execution: hystrix.ExecutionConfig{
			Timeout: time.Millisecond,
		},
	})
	go func() {
		for range time.Tick(tickInterval) {
			timeOutCircuit.Execute(context.Background(), func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			}, nil)
		}
	}()

	fallbackCircuit := h.MustCreateCircuit("always-falls-back", hystrix.CommandProperties{
		Execution: hystrix.ExecutionConfig{
			Timeout: time.Millisecond,
		},
	})
	go func() {
		for range time.Tick(tickInterval) {
			fallbackCircuit.Execute(context.Background(), func(ctx context.Context) error {
				return errors.New("a failure")
			}, func(ctx context.Context, err error) error {
				return nil
			})
		}
	}()

	randomExecutionTime := h.MustCreateCircuit("random-execution-time", hystrix.CommandProperties{
		Execution: hystrix.ExecutionConfig{},
	})
	go func() {
		for range time.Tick(tickInterval) {
			randomExecutionTime.Execute(context.Background(), func(ctx context.Context) error {
				select {
				// Some time between 0 and 50ms
				case <-time.After(time.Duration(int64(float64(time.Millisecond.Nanoseconds()*50) * rand.Float64()))):
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}, func(ctx context.Context, err error) error {
				return nil
			})
		}
	}()

	// Flop every 3 seconds, try to recover very quickly
	floppyCircuit := h.MustCreateCircuit("floppy-circuit", hystrix.CommandProperties{
		CircuitBreaker: hystrix.CircuitBreakerConfig{
			SleepWindow:            time.Millisecond * 10,
			RequestVolumeThreshold: 2,
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
			for range time.Tick(tickInterval) {
				floppyCircuit.Execute(context.Background(), func(ctx context.Context) error {
					if atomic.LoadInt64(&floppyCircuitPasses) == 1 {
						return nil
					}
					return errors.New("i'm failing now")
				}, func(ctx context.Context, err error) error {
					return nil
				})
			}
		}()
	}

	throttledCircuit := h.MustCreateCircuit("throttled-circuit", hystrix.CommandProperties{
		Execution: hystrix.ExecutionConfig{
			MaxConcurrentRequests: 2,
		},
	})
	// 100 threads, every 100ms, someone will get throttled
	for i := 0; i < 100; i++ {
		go func() {
			for range time.Tick(tickInterval) {
				throttledCircuit.Execute(context.Background(), func(ctx context.Context) error {
					select {
					// Some time between 0 and 50ms
					case <-time.After(time.Duration(int64(float64(time.Millisecond.Nanoseconds()*50) * rand.Float64()))):
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}, nil)
			}
		}()
	}
}
