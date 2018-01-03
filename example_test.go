package circuit_test

import (
	"bytes"
	"context"
	"errors"
	"expvar"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/cep21/circuit"
)

// This is a full example of using a circuit around HTTP requests.
func Example_http() {
	h := circuit.Manager{}
	c := h.MustCreateCircuit("hello-http", circuit.Config{
		Execution: circuit.ExecutionConfig{
			// Timeout after 3 seconds
			Timeout: time.Second * 3,
		},
	})

	testServer := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		_, _ = io.WriteString(rw, "hello world")
	}))
	defer testServer.Close()

	var body bytes.Buffer
	runErr := c.Run(context.Background(), func(ctx context.Context) error {
		req, err := http.NewRequest("GET", testServer.URL, nil)
		if err != nil {
			return circuit.SimpleBadRequest{Err: err}
		}
		req = req.WithContext(ctx)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode >= 400 && resp.StatusCode <= 499 {
			return circuit.SimpleBadRequest{Err: errors.New("server found your request invalid")}
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
func ExampleCircuit_Execute_fallback() {
	c := circuit.NewCircuitFromConfig("divider", circuit.Config{})
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

// This example shows execute failing (marking the circuit with a failure), but not returning an error
// back to the user since the fallback was able to execute.  For this case, we try to load the size of the
// largest message a user can send, but fall back to 140 if the load fails.
func ExampleCircuit_Execute_helloworld() {
	c := circuit.NewCircuitFromConfig("hello-world", circuit.Config{})
	err := c.Execute(context.Background(), func(_ context.Context) error {
		return nil
	}, nil)
	fmt.Printf("err=%v", err)
	// Output: err=<nil>
}

func ExampleCircuit_Go() {
	h := circuit.Manager{}
	c := h.MustCreateCircuit("untrusting-circuit", circuit.Config{
		Execution: circuit.ExecutionConfig{
			// Time out the context after one second
			Timeout: time.Second,
		},
	})

	neverEnds := make(chan struct{})
	defer close(neverEnds) // Just to clean up our testing goroutine

	errResult := c.Go(context.Background(), func(ctx context.Context) error {
		// This will be left hanging because we never send a signal to neverEnds
		<-neverEnds
		return nil
	}, nil)
	fmt.Printf("err=%v", errResult)
	// Output: err=context deadline exceeded
}

// This example will panic, and the panic can be caught up the stack
func ExampleCircuit_Execute_panics() {
	h := circuit.Manager{}
	c := h.MustCreateCircuit("panic_up", circuit.Config{})

	defer func() {
		r := recover()
		if r != nil {
			fmt.Println("I recovered from a panic", r)
		}
	}()
	c.Execute(context.Background(), func(ctx context.Context) error {
		panic("oh no")
	}, nil)
	// Output: I recovered from a panic oh no
}

// You can use DefaultCircuitProperties to set configuration dynamically for any circuit
func ExampleManager_DefaultCircuitProperties() {
	myFactory := func(circuitName string) circuit.Config {
		timeoutsByName := map[string]time.Duration{
			"v1": time.Second,
			"v2": time.Second * 2,
		}
		customTimeout := timeoutsByName[circuitName]
		if customTimeout == 0 {
			// Just return empty if you don't want to set any config
			return circuit.Config{}
		}
		return circuit.Config{
			Execution: circuit.ExecutionConfig{
				Timeout: customTimeout,
			},
		}
	}

	// Hystrix manages circuits with unique names
	h := circuit.Manager{
		DefaultCircuitProperties: []circuit.CommandPropertiesConstructor{myFactory},
	}
	h.MustCreateCircuit("v1", circuit.Config{})
	fmt.Println("The timeout of v1 is", h.GetCircuit("v1").Config().Execution.Timeout)
	// Output: The timeout of v1 is 1s
}

// Many configuration variables can be set at runtime in a thread safe way
func ExampleCircuit_SetConfigThreadSafe() {
	h := circuit.Manager{}
	c := h.MustCreateCircuit("changes-at-runtime", circuit.Config{})
	// ... later on (during live)
	c.SetConfigThreadSafe(circuit.Config{
		Execution: circuit.ExecutionConfig{
			MaxConcurrentRequests: int64(12),
		},
	})
}

// Even though Go executes inside a goroutine, we catch that panic and bubble it up the same
// call stack that called Go
func ExampleCircuit_Go_panics() {
	h := circuit.Manager{}
	c := h.MustCreateCircuit("panic_up", circuit.Config{})

	defer func() {
		r := recover()
		if r != nil {
			fmt.Println("I recovered from a panic", r)
		}
	}()
	_ = c.Go(context.Background(), func(ctx context.Context) error {
		panic("oh no")
	}, nil)
	// Output: I recovered from a panic oh no
}

// ExampleBadRequest shows how to return errors in a circuit without considering the circuit at fault.
// Here, even if someone tries to divide by zero, the circuit will not consider it a failure even if the
// function returns non nil error.
func ExampleBadRequest() {
	c := circuit.NewCircuitFromConfig("divider", circuit.Config{})
	divideInCircuit := func(a, b int) (int, error) {
		var result int
		err := c.Run(context.Background(), func(ctx context.Context) error {
			if b == 0 {
				return &circuit.SimpleBadRequest{
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
func ExampleManager_Var() {
	h := circuit.Manager{}
	expvar.Publish("hystrix", h.Var())
	// Output:
}

//func ExampleCommandProperties() {
//	h := circuit.Manager{}
//
//	circuitConfig := circuit.Config{
//		CircuitBreaker: circuit.CircuitBreakerConfig{
//			// This should allow a new request every 10 milliseconds
//			SleepWindow: time.Millisecond * 5,
//			// The first failure should open the circuit
//			ErrorThresholdPercentage: 1,
//			// Only one request is required to fail the circuit
//			RequestVolumeThreshold: 1,
//		},
//		Execution: circuit.ExecutionConfig{
//			// Allow at most 2 requests at a time
//			MaxConcurrentRequests: 2,
//			// Time out the context after one second
//			Timeout: time.Second,
//		},
//	}
//	h.MustCreateCircuit("configured-circuit", circuitConfig)
//	// Output:
//}
