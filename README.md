<!-- Image designed by Jack Lindamood, Licensed under the Creative Commons 3.0 Attributions license, originate from https://github.com/golang-samples/gopher-vector design by Takuya Ueda -->
![Mascot](https://cep21.github.io/circuit/imgs/hystrix-gopher_100px.png)
# Circuit
[![Build Status](https://travis-ci.org/cep21/circuit.svg?branch=master)](https://travis-ci.org/cep21/circuit)
[![GoDoc](https://godoc.org/github.com/cep21/circuit?status.svg)](https://godoc.org/github.com/cep21/circuit)

Circuit is an efficient and feature complete [Hystrix](https://github.com/Netflix/Hystrix) like Go implementation of the [circuit
breaker pattern](https://docs.microsoft.com/en-us/azure/architecture/patterns/circuit-breaker).

Learn more about the problems Hystrix and other circuit breakers solve on the [Hystrix Wiki](https://github.com/Netflix/Hystrix/wiki).

There are a large number of examples on the [godoc](https://godoc.org/github.com/cep21/circuit#pkg-examples) that are worth looking at.  They tend to be more up to date than the README doc.

# Feature set

* No forced goroutines
* recoverable panic()
* Integrated with context.Context
* Comprehensive metric tracking
* Efficient implementation with Benchmarks
* Low/zero memory allocation costs
* Support for [Netflix Hystrix dashboards](https://github.com/Netflix/Hystrix/wiki/Dashboard), even with custom circuit transition logic
* Multiple error handling features
* Expose circuit health and configuration on expvar
* SLO tracking
* Customizable state transition logic, allowing complex circuit state changes
* Live configuration changes
* Many tests and examples
* Good inline documentation

# Usage

## Hello world circuit

```go
	// Manages all our circuits
	h := circuit.Manager{}
	// Create a circuit with a unique name
	c:= h.MustCreateCircuit("hello-world", circuit.Config{})
	// Call the circuit
	errResult := c.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	}, nil)
	fmt.Println("Result of execution:", errResult)
	// Output: Result of execution: <nil>
```

## Hello world fallback

```go
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
```

## Ending early for functions that don't respect context.Context.Done()

I strongly recommend using `circuit.Execute` and implementing a context aware function.  If, however, you want to exit
your run function early and leave it hanging (possibly forever), then you can call `circuit.Go`.

```go
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
```

## Configuration

All configuration parameters are documented in config.go.  Your circuit open/close logic configuration is documented
with the logic.  For hystrix, this configuration is in closers/hystrix and well documented on [the Hystrix wiki](https://github.com/Netflix/Hystrix/wiki/Configuration).

This example configures the circuit to use Hystrix open/close logic with the default Hystrix parameters
```go
	configuration := hystrix.ConfigFactory{
		// Hystrix open logic is to open the circuit after an % of errors
		ConfigureOpenOnErrPercentage: hystrix.ConfigureOpenOnErrPercentage{
			// We change the default to wait for 10 requests, not 20, before checking to close
			RequestVolumeThreshold: 10,
			// The default values match what hystrix does by default
		},
		// Hystrix close logic is to sleep then check
		ConfigureSleepyCloseCheck: hystrix.ConfigureSleepyCloseCheck{
			// The default values match what hystrix does by default
		},
	}
	h := circuit.Manager{
		// Tell the manager to use this configuration factory whenever it makes a new circuit
		DefaultCircuitProperties: []circuit.CommandPropertiesConstructor{configuration.Configure},
	}
	// This circuit will inherit the configuration from the example
	c := h.MustCreateCircuit("hystrix-circuit")
	fmt.Println("This is a hystrix configured circuit", c.Name())
	// Output: This is a hystrix configured circuit hystrix-circuit
```

## Enable dashboard metrics

Dashboard metrics can be enabled with the MetricEventStream object.
```go
	h := circuit.Manager{}
	es := metriceventstream.MetricEventStream{
		Hystrix: &h,
	}
	go func() {
		if err := es.Start(); err != nil {
			log.Fatal(err)
		}
	}()
	http.Handle("/hystrix.stream", &es)
	// ...
	if err := es.Close(); err != nil {
		log.Fatal(err)
	}
	// Output:	
```

## Enable expvar

Expvar variables can be exported via the Var function

```go
	h := circuit.Manager{}
	expvar.Publish("hystrix", h.Var())
```

## Custom metrics

Implement interfaces CmdMetricCollector or FallbackMetricCollector to know what happens with commands or fallbacks.
Then pass those implementations to configure.

```go
	config := circuit.Config{
		Metrics: circuit.MetricsCollectors{
			Run: []circuit.RunMetrics{
				// Here is where I would insert my custom metric collector
			},
		},
	}
	circuit.NewCircuitFromConfig("custom-metrics", config)
```

## Panics

Code executed with `Execute` does not spawn a goroutine and panics naturally go up the call stack to the caller.
This is also true for `Go`, where we attempt to recover and throw panics on the same stack that
calls Go.
 ```go
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
```

## Runtime configuration changes

Most configuration properties on [the Hystrix Configuration page](https://github.com/Netflix/Hystrix/wiki/Configuration) that say
they are modifyable at runtime can be changed on the Circuit in a thread safe way.  Most of the ones that cannot are
related to stat collection.  A comprehensive list is is all the fields duplicated on the `atomicCircuitConfig` struct
internal to this project.

```go
	// Start off using the defaults
	configuration := hystrix.ConfigFactory{}
	h := circuit.Manager{
		// Tell the manager to use this configuration factory whenever it makes a new circuit
		DefaultCircuitProperties: []circuit.CommandPropertiesConstructor{configuration.Configure},
	}
	c := h.MustCreateCircuit("hystrix-circuit")
	fmt.Println("The default sleep window", c.OpenToClose.(*hystrix.SleepyCloseCheck).Config().SleepWindow)
	// This configuration update function is thread safe.  We can modify this at runtime while the circuit is active
	c.OpenToClose.(*hystrix.SleepyCloseCheck).SetConfigThreadSafe(hystrix.ConfigureSleepyCloseCheck{
		SleepWindow: time.Second * 3,
	})
	fmt.Println("The new sleep window", c.OpenToClose.(*hystrix.SleepyCloseCheck).Config().SleepWindow)
	// Output:
	// The default sleep window 5s
	// The new sleep window 3s
```

## Not counting early terminations as failures

If the context passed into a circuit function ends, before the circuit can
finish, it does not count the circuit as unhealthy.  You can disable this
behavior with the `IgnoreInterrputs` flag.

This example proves that terminating a circuit call early because the passed in context died does not, by default,
count as an error on the circuit.  It also demonstrates setting up internal stat collection by default for all
circuits

```go
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
		case <- ctx.Done():
			// This will return early, with an error, since the parent context was canceled after 1 ms
			return ctx.Err()
		case <- time.After(time.Hour):
			panic("We never actually get this far")
		}
	}, nil)
	rs := f.RunStats("don't fail me bro")
	fmt.Println("errResult is", errResult)
	fmt.Println("The error and timeout count is", rs.ErrTimeouts.TotalSum() + rs.ErrFailures.TotalSum())
	// Output: errResult is context deadline exceeded
	// The error and timeout count is 0
```

## Configuration factories

Configuration factories are supported on the root hystrix object.  This allows you to create dynamic configuration per
circuit name.

```go
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
```

## StatsD configuration factory

A configuration factory for statsd is provided inside ./metrics/statsdmetrics

```go
	// This factory allows us to report statsd metrics from the circuit
	f := statsdmetrics.CommandFactory{
		SubStatter: &statsd.NoopClient{},
	}

	// Wire the statsd factory into the circuit manager
	h := circuit.Manager{
		DefaultCircuitProperties: []circuit.CommandPropertiesConstructor{f.CommandProperties},
	}
	// This created circuit will now use statsd
	h.MustCreateCircuit("using-statsd")
	// Output:
```

## Service health tracking

Most services have the concept of an SLA, or service level agreement.  Unfortunantly,
this is usually tracked by the service owners, which creates incentives for people to
inflate the health of their service.

This Circuit implementation formalizes an SLO of the template
"X% of requests will return faster than Y ms".  This is a value that canont be calculated
just by looking at the p90 or p99 of requests in aggregate, but must be tracked per
request.  You can define a SLO for your service, which is a time **less** than the timeout
time of a request, that works as a promise of health for the service.  You can then
report per circuit not just fail/pass but an extra "healthy" % over time that counts only
requests that resopnd _quickly enough_.

```go
	sloTrackerFactory := responsetimeslo.Factory{
		Config: responsetimeslo.Config{
			// Consider requests faster than 20 ms as passing
			MaximumHealthyTime: time.Millisecond * 20,
		},
		// Pass in your collector here: for example, statsd
		CollectorConstructors: nil,
	}
	h := circuit.Manager{
		DefaultCircuitProperties: []circuit.CommandPropertiesConstructor{sloTrackerFactory.CommandProperties},
	}
	h.CreateCircuit("circuit-with-slo")
  
```

## Not counting user error as a fault

Sometimes users pass invalid functions to the input of your circuit.  You want to return
an error in that case, but not count the error as a failure of the circuit.  Use `SimpleBadRequest`
in this case.

This example shows how to return errors in a circuit without considering the circuit at fault.
Here, even if someone tries to divide by zero, the circuit will not consider it a failure even if the
function returns non nil error.

```go
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
```

# Benchmarking

This implementation is more efficient than go-hystrix in every configuration.  It has comparable efficiency
to other implementations, in most faster when running with high concurrency. Run benchmarks with `make bench`.

I benchmark the following alternative circuit implementations.  I try to be fair and if
there is a better way to benchmark one of these circuits, please let me know!
* [hystrix-go](https://github.com/afex/hystrix-go)
* [rubyist](https://github.com/rubyist/circuitbreaker)
* [sony/gobreaker](https://github.com/sony/gobreaker)
* [handy/breaker](https://github.com/streadway/handy/tree/master/breaker)
* [iand-circuit]("github.com/iand/circuit")

```
> make bench
cd benchmarking && go test -v -benchmem -run=^$ -bench=. . 2> /dev/null
goos: darwin
goarch: amd64
pkg: github.com/cep21/circuit/benchmarking
BenchmarkCiruits/Hystrix/Metrics/passing/1-8       	 5000000	       253 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/Hystrix/Metrics/passing/75-8      	10000000	       108 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/Hystrix/Metrics/failing/1-8       	 5000000	       297 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/Hystrix/Metrics/failing/75-8      	20000000	       115 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/Hystrix/Minimal/passing/1-8       	10000000	       177 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/Hystrix/Minimal/passing/75-8      	20000000	        92.1 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/Hystrix/Minimal/failing/1-8       	20000000	        60.4 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/Hystrix/Minimal/failing/75-8      	100000000	        17.6 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/Hystrix/UseGo/passing/1-8         	 1000000	      1242 ns/op	     256 B/op	       5 allocs/op
BenchmarkCiruits/Hystrix/UseGo/passing/75-8        	 5000000	       335 ns/op	     256 B/op	       5 allocs/op
BenchmarkCiruits/Hystrix/UseGo/failing/1-8         	 1000000	      1294 ns/op	     256 B/op	       5 allocs/op
BenchmarkCiruits/Hystrix/UseGo/failing/75-8        	 5000000	       353 ns/op	     256 B/op	       5 allocs/op
BenchmarkCiruits/GoHystrix/DefaultConfig/passing/1-8         	  200000	      7880 ns/op	    1007 B/op	      18 allocs/op
BenchmarkCiruits/GoHystrix/DefaultConfig/passing/75-8        	  500000	      2832 ns/op	     990 B/op	      20 allocs/op
BenchmarkCiruits/GoHystrix/DefaultConfig/failing/1-8         	  200000	      7338 ns/op	    1022 B/op	      19 allocs/op
BenchmarkCiruits/GoHystrix/DefaultConfig/failing/75-8        	  500000	      2088 ns/op	    1004 B/op	      20 allocs/op
BenchmarkCiruits/rubyist/Threshold-10/passing/1-8            	 1000000	      1699 ns/op	     332 B/op	       5 allocs/op
BenchmarkCiruits/rubyist/Threshold-10/passing/75-8           	 2000000	       891 ns/op	     309 B/op	       4 allocs/op
BenchmarkCiruits/rubyist/Threshold-10/failing/1-8            	20000000	       125 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/rubyist/Threshold-10/failing/75-8           	 5000000	       242 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/gobreaker/Default/passing/1-8               	10000000	       196 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/gobreaker/Default/passing/75-8              	 2000000	       654 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/gobreaker/Default/failing/1-8               	20000000	        94.4 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/gobreaker/Default/failing/75-8              	 5000000	       345 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/handy/Default/passing/1-8                   	 1000000	      1070 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/handy/Default/passing/75-8                  	 1000000	      1915 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/handy/Default/failing/1-8                   	 1000000	      1293 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/handy/Default/failing/75-8                  	 1000000	      1787 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/iand_circuit/Default/passing/1-8            	10000000	       116 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/iand_circuit/Default/passing/75-8           	 3000000	       351 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/iand_circuit/Default/failing/1-8            	100000000	        20.9 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/iand_circuit/Default/failing/75-8           	300000000	         5.36 ns/op	       0 B/op	       0 allocs/op

PASS
ok  	github.com/cep21/circuit/benchmarking	59.518s
``
```

I feel the most important benchmarks are the ones with high concurrency on a passing circuit, since
that is the common case for heavily loaded systems.

# Development

Make sure your tests pass with `make test` and your lints pass with `make lint`.

# Example

You can run an example set of circuits inside the /example directory

```bash
make run
```

The output looks something like this:
```bash
< make run
go run example/main.go
2017/12/19 15:24:42 Serving on socket :8123
2017/12/19 15:24:42 To view the stream, execute: 
2017/12/19 15:24:42   curl http://localhost:8123/hystrix.stream
2017/12/19 15:24:42 
2017/12/19 15:24:42 To view expvar metrics, visit expvar in your browser
2017/12/19 15:24:42   http://localhost:8123/debug/vars
2017/12/19 15:24:42 
2017/12/19 15:24:42 To view a dashboard, follow the instructions at https://github.com/Netflix/Hystrix/wiki/Dashboard#run-via-gradle
2017/12/19 15:24:42   git clone git@github.com:Netflix/Hystrix.git
2017/12/19 15:24:42   cd Hystrix/hystrix-dashboard
2017/12/19 15:24:42   ../gradlew jettyRun
2017/12/19 15:24:42 
2017/12/19 15:24:42 Then, add the stream http://localhost:8123/hystrix.stream
```

If you load the Hystrix dasbhoard (following the above instructions), you should see metrics for all the example circuits.

[![dashboard](https://cep21.github.io/hystrix/imgs/hystrix_ui.png)](https://cep21.github.io/hystrix/imgs/hystrix_ui.png)
