package hystrix_test

import (
	"bytes"
	"context"
	"errors"
	"expvar"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/cep21/hystrix"
)

func Example_http() {
	h := hystrix.Hystrix{}
	c := h.MustCreateCircuit("hello-http", hystrix.CommandProperties{
		Execution: hystrix.ExecutionConfig{
			// Timeout after 30 seconds
			Timeout: time.Second * 30,
		},
	})

	var body bytes.Buffer
	runErr := c.Run(context.Background(), func(ctx context.Context) error {
		req, err := http.NewRequest("GET", "http://www.google.com", nil)
		if err != nil {
			return hystrix.SimpleBadRequest{Err: err}
		}
		req = req.WithContext(ctx)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode >= 400 && resp.StatusCode <= 499 {
			return hystrix.SimpleBadRequest{Err: errors.New("server found your request invalid")}
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return fmt.Errorf("invalid status code: %d", resp.StatusCode)
		}
		if _, err := io.Copy(&body, resp.Body); err != nil {
			return err
		}
		return resp.Body.Close()
	})
	if runErr == nil {
		fmt.Printf("We saw a body\n")
		return
	}
	fmt.Printf("There was an error with the request: %s\n", runErr)
	// Output: We saw a body
}

// This example shows execute failing (marking the circuit with a failure), but not returning an error
// back to the user since the fallback was able to execute.  For this case, we try to load the size of the
// largest message a user can send, but fall back to 140 if the load fails.
func ExampleCircuit_Execute() {
	c := hystrix.NewCircuitFromConfig("divider", hystrix.CommandProperties{})
	var maximumMessageSize int
	err := c.Execute(context.Background(), func(_ context.Context) error {
		return errors.New("your circuit failed")
	}, func(ctx context.Context, err2 error) error {
		maximumMessageSize = 140
		return nil
	})
	fmt.Printf("value=%d err=%v", maximumMessageSize, err)
	// Output: value=140 err=<nil>
}

// ExampleBadRequest shows how to return errors in a circuit without considering the circuit at fault.
// Here, even if someone tries to divide by zero, the circuit will not consider it a failure even if the
// function returns non nil error.
func ExampleBadRequest() {
	c := hystrix.NewCircuitFromConfig("divider", hystrix.CommandProperties{})
	divideInCircuit := func(a, b int) (int, error) {
		var result int
		err := c.Run(context.Background(), func(ctx context.Context) error {
			if b == 0 {
				return &hystrix.SimpleBadRequest{
					Err: errors.New("someone tried to divide by zero"),
				}
			}
			result = a / b
			return nil
		})
		return result, err
	}
	_, err := divideInCircuit(10, 0)
	fmt.Println("Result of 10/0 is", err)
	// Output: Result of 10/0 is someone tried to divide by zero
}

// If you wanted to publish hystrix information on Expvar, you can register your instance.
func ExampleHystrix_Var() {
	h := hystrix.Hystrix{}
	expvar.Publish("hystrix", h.Var())
	// Output:
}

func ExampleCommandProperties() {
	h := hystrix.Hystrix{}

	circuitConfig := hystrix.CommandProperties{
		CircuitBreaker: hystrix.CircuitBreakerConfig{
			// This should allow a new request every 10 milliseconds
			SleepWindow: time.Millisecond * 5,
			// The first failure should open the circuit
			ErrorThresholdPercentage: 1,
			// Only one request is required to fail the circuit
			RequestVolumeThreshold: 1,
		},
		Execution: hystrix.ExecutionConfig{
			// Allow at most 2 requests at a time
			MaxConcurrentRequests: 2,
			// Time out the context after one second
			Timeout: time.Second,
		},
	}
	h.MustCreateCircuit("configured-circuit", circuitConfig)
	// Output:
}

// This example creates an event stream handler, starts it, then later closes the handler
func ExampleMetricEventStream() {
	h := hystrix.Hystrix{}
	es := hystrix.MetricEventStream{
		Hystrix: &h,
	}
	go es.Start()
	http.Handle("/hystrix.stream", &es)
	// ...
	es.Close()
	// Output:
}
