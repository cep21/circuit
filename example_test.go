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
	"github.com/cep21/circuit/metrics/rolling"
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

// This example shows how to create a hello-world circuit from the circuit manager
func ExampleManager_MustCreateCircuit_helloworld() {
	// Manages all our circuits
	h := circuit.Manager{}
	// Create a circuit with a unique name
	c := h.MustCreateCircuit("hello-world", circuit.Config{})
	// Call the circuit
	errResult := c.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	}, nil)
	fmt.Println("Result of execution:", errResult)
	// Output: Result of execution: <nil>
}

// This example proves that terminating a circuit call early because the passed in context died does not, by default,
// count as an error on the circuit.  It also demonstrates setting up internal stat collection by default for all
// circuits
func ExampleCircuit_noearlyterminate() {
	// Inject stat collection to prove these failures don't count
	f := rolling.StatFactory{}
	manager := circuit.Manager{
		DefaultCircuitProperties: []circuit.CommandPropertiesConstructor{
			f.CreateConfig,
		},
	}
	c := manager.MustCreateCircuit("don't fail me bro")
	// The passed in context times out in one millisecond
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	errResult := c.Execute(ctx, func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			// This will return early, with an error, since the parent context was canceled after 1 ms
			return ctx.Err()
		case <-time.After(time.Hour):
			panic("We never actually get this far")
		}
	}, nil)
	rs := f.RunStats("don't fail me bro")
	fmt.Println("errResult is", errResult)
	fmt.Println("The error and timeout count is", rs.ErrTimeouts.TotalSum()+rs.ErrFailures.TotalSum())
	// Output: errResult is context deadline exceeded
	// The error and timeout count is 0
}

// This example shows how fallbacks execute to return alternate errors
func ExampleCircuit_Execute_fallbackhelloworld() {
	c := circuit.NewCircuitFromConfig("hello-world-fallback", circuit.Config{})
	errResult := c.Execute(context.Background(), func(ctx context.Context) error {
		return errors.New("this will fail")
	}, func(ctx context.Context, err error) error {
		fmt.Println("Circuit failed with error, but fallback returns nil")
		return nil
	})
	fmt.Println("Execution result:", errResult)
	// Output: Circuit failed with error, but fallback returns nil
	// Execution result: <nil>
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
			// Time out the context after a few ms
			Timeout: time.Millisecond * 30,
		},
	})

	errResult := c.Go(context.Background(), func(ctx context.Context) error {
		// Sleep 30 seconds, way longer than our timeout
		time.Sleep(time.Second * 30)
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

// This example shows how to return errors in a circuit without considering the circuit at fault.
// Here, even if someone tries to divide by zero, the circuit will not consider it a failure even if the
// function returns non nil error.
func ExampleBadRequest() {
	c := circuit.NewCircuitFromConfig("divider", circuit.Config{})
	divideInCircuit := func(numerator, denominator int) (int, error) {
		var result int
		err := c.Run(context.Background(), func(ctx context.Context) error {
			if denominator == 0 {
				// This error type is not counted as a failure of the circuit
				return &circuit.SimpleBadRequest{
					Err: errors.New("someone tried to divide by zero"),
				}
			}
			result = numerator / denominator
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

func ExampleConfig_custommetrics() {
	config := circuit.Config{
		Metrics: circuit.MetricsCollectors{
			Run: []circuit.RunMetrics{
				// Here is where I would insert my custom metric collector
			},
		},
	}
	circuit.NewCircuitFromConfig("custom-metrics", config)
	// Output:
}