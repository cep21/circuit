[![Build Status](https://travis-ci.org/cep21/hystrix.svg?branch=master)](https://travis-ci.org/cep21/hystrix)
# Hystrix

<!-- Image designed by Jack Lindamood, Licensed under the Creative Commons 3.0 Attributions license, originate from https://github.com/golang-samples/gopher-vector design by Takuya Ueda -->
<img align="left" width="100" src="https://cep21.github.io/hystrix/imgs/hystrix-gopher.png"/>

Hystrix is a Go implementation of [Hystrix](https://github.com/Netflix/Hystrix) from Netflix.

It is designed to support context.Context and supports the feature set of Netflix's Java library.

The names of functions and configuration attempts to mirror the Java implementation's when appropriate.

# Feature set

* No global mutable state
* No singletons
* No forced goroutines
* recoverable panic()
* Integrated with context.Context
* Comprehensive metric tracking
* Efficient implementation
* Low/zero memory allocation costs
* Support for Netflix Hystrix dashboards
* Multiple error handling features
* Expose circuit health and configuration on expvar
* Built in SLO tracking
* Customizable state transition logic, allowing complex circuit state changes
* Live configuration changes
* Many tests
* Benchmarks
* Good inline documentation


# Comparison to go-hystrix

This library is most directly comparable to [go-hystrix](https://github.com/afex/hystrix-go),
but differs in many ways including performance, accuracy (more accurately does what it advertises),
feature set, context support, panic behavior, and metric tracking.

# Usage

## Hello world circuit

```go
  // Make one of these to manage all your circuits
  h := hystrix.Hystrix{}
  
  // Create a named circuit from your hystrix manager
  circuit := h.MustCreateCircuit("hello-world", hystrix.CommandProperties{})
  
  // Call a function on your circuit
  errResult := circuit.Execute(ctx, func(ctx context.Context) error {
  	return nil
  }, nil)
  // errResult == nil
```

## Hello world fallback

```go
  h := hystrix.Hystrix{}
  
  circuit := h.MustCreateCircuit("fallback-circuit", hystrix.CommandProperties{})
  
  errResult := circuit.Execute(ctx, func(ctx context.Context) error {
  	return errors.New("This will fail")
  }, func(ctx context.Context, err error) error {
  	fmt.Println("Circuit failed with error ", err)
  	fmt.Println("But I will not")
  	return nil
  })
  // errResult == nil
```

## Ending early for functions that don't respect context.Context.Done()

I strongly recommend using `circuit.Execute` and implementing a context aware function.  If, however, you want to exit
your run function early and leave it hanging (possibly forever), then you can call `circuit.Go`.

```go
  h := hystrix.Hystrix{}
  circuit := h.MustCreateCircuit("untrusting-circuit", hystrix.CommandProperties{
    Execution: hystrix.ExecutionConfig{
      // Time out the context after one second
      ExecutionTimeout: time.Second,
    },
  })
  
  errResult := circuit.Go(ctx, func(ctx context.Context) error {
  	// This will be left hanging because time.Sleep continues to run even if the context is dead
  	time.Sleep(time.Hour)
  }, nil)
  // errResult != nil
  //  Will return after 1 second.  Go spins time.Sleep inside a goroutine, which will hang around for 1 hour, while
  //  Go ends when the context is canceled (in this case, after a timeout of 1 second).
```

## Configuration

All configuration parameters are documented in config.go and mirror the configuration better documented on [the Hystrix wiki](https://github.com/Netflix/Hystrix/wiki/Configuration).
```go
  // Make one of these to manage all your circuits
  h := hystrix.Hystrix{}
  
  circuitConfig := hystrix.CommandProperties {
  	CircuitBreaker: hystrix.CircuitBreakerConfig{
      // This should allow a new request every 10 milliseconds
      SleepWindow: time.Millisecond * 5,
      // The first failure should open the circuit
      ErrorThresholdPercentage: 1,
      // Only one request is required to fail the circuit
      RequestVolumeThreshold:   1,
    },
    Execution: hystrix.ExecutionConfig{
      // Allow at most 2 requests at a time
      MaxConcurrentRequests: 2,
      // Time out the context after one second
      ExecutionTimeout: time.Second,
    },
  }
  circuit := h.MustCreateCircuit("configured-circuit", circuitConfig) 
```

## Enable dashboard metrics

Dashboard metrics can be enabled with the MetricEventStream object.
```go
	h := hystrix.Hystrix{}
	eventStream := hystrix.MetricEventStream{
		Hystrix: &h,
	}
	go eventStream.Start()
	http.Handle("/hystrix.stream", &eventStream)
	// ...
	go http.ListenAndServe(net.JoinHostPort("", "8181"), hystrixStreamHandler)
	
```

## Enable expvar

Expvar variables can be exported via the Var function

```go
	h := hystrix.Hystrix{}
	expvar.Publish("hystrix", h.Var())
```

## Custom metrics

Implement interfaces CmdMetricCollector or FallbackMetricCollector to know what happens with commands or fallbacks.
Then pass those implementations to configure.

```go
  // Make one of these to manage all your circuits
  h := hystrix.Hystrix{}
  
  circuitConfig := CommandProperties {
  	MetricsConfig: hystrix.MetricsConfig{
  		CmdMetricCollector: &myImplementation{},
  	},
  }
  circuit := h.MustCreateCircuit("configured-circuit", circuitConfig) 
```

## Panics

Code executed with `Execute` does not spawn a goroutine and panics naturally go up the call stack to the caller.  This is not
true for `Go`.
 ```go
  h := hystrix.Hystrix{}
  circuit := h.MustCreateCircuit("panic_up", hystrix.CommandProperties{})
  
  defer func() {
  	r := recover()
  	fmt.Println("I recovered from a panic")
  }()
  errResult := circuit.Execute(ctx, func(ctx context.Context) error {
  	panic("oh no")
  }, nil)
  // errResult never happens (panics first)
```

## Runtime configuration changes

Most configuration properties on [the Hystrix Configuration page](https://github.com/Netflix/Hystrix/wiki/Configuration) that say
they are modifyable at runtime can be changed on the Circuit in a thread safe way.  Most of the ones that cannot are
related to stat collection.  A comprehensive list is is all the fields duplicated on the `atomicCircuitConfig` struct
internal to this project.

```go
  h := hystrix.Hystrix{}
  circuit := h.MustCreateCircuit("changes-at-runtime", hystrix.CommandProperties{})
  // ... later on (during live)
  circuit.SetConfigThreadSafe(hystrix.CommandProperties{
		Execution: hystrix.ExecutionConfig{
			MaxConcurrentRequests: int64(12),
		},
  })
```

## Not counting early terminations as failures

If you're passing a context to your circuit, and the passed context fails, you may not want to count the circuit as
behaving badly. In that case, you can use the custom `IgnoreContextFailures` field to ignore those failures.  I strongly
recommend using this.

```go
  h := hystrix.Hystrix{}
  circuit := h.MustCreateCircuit("dont-fail-me-bro", hystrix.CommandProperties{
    Execution: hystrix.ExecutionConfig{
      // healthy is allowing a full second
      ExecutionTimeout: time.Second,
    },
    GoSpecific: hystrix.ExecutionConfig{
    	// Do not count parent context failures as the circuit's fault
      IgnoreContextFailures: true,
    },
  })
  // The passed in context will time out in a millisecond
  ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
  defer cancel()
  
  // Call a function on your circuit
  errResult := circuit.Execute(ctx, func(ctx context.Context) error {
  	select {
  	case <- ctx.Done():
  		// This will return early, with an error, since the parent context was canceled after 1 ms
  		return ctx.Err()
  	case <- time.After(time.Millisecond * 100):
  		//  100ms is a healthy execute
  		return nil
  	}
  }, nil)
  
  // errResult != nil
  // The circuit failed, but not because it's misbehaving: it failed because the passed in context was canceled.
  // Because you set IgnoreContextFailures, we will not consider this failure state invalid and the circuit will not
  // try to open up.
```

## Configuration factories

Configuration factories are supported on the root hystrix object.  This allows you to create dynamic configuration per
circuit name.

```go
  myFactory := func(circuitName string) hysrix.CommandProperties {
    customTimeout := lookup_timeout(circuitName)
    return hysrix.CommandProperties {
      Execution: hystrix.ExecutionConfig{
        ExecutionTimeout: customTimeout,
      }
    },
  }
  
  // Hystrix manages circuits with unique names
  h := hystrix.Hystrix {
    DefaultCircuitProperties: []func(circuitName string) CommandProperties {myFactory},
  }
  h.MustCreateCircuit("...", hystrix.CommandProperties{})
```

## StatsD configuration factory

A configuration factory for statsd is provided inside metric_implementations

```go
  statsdFactory := statsdmetrics.CommandFactory {
  	SubStatter: myStatter,
  }

  // Hystrix manages circuits with unique names
  h := hystrix.Hystrix {
    DefaultCircuitProperties: []func(circuitName string) CommandProperties {statsdFactory.CommandProperties},
  }
  h.MustCreateCircuit("...", hystrix.CommandProperties{})

```

## Service health tracking

Most services have the concept of an SLA, or service level agreement.  Unfortunantly,
this is usually tracked by the service owners, which creates incentives for people to
inflate the health of their service.

This Hystrix implementation formalizes an SLO of the template
"X% of requests will return faster than Y ms".  This is a value that canont be calculated
just by looking at the p90 or p99 of requests in aggregate, but must be tracked per
request.  You can define a SLO for your service, which is a time **less** than the timeout
time of a request, that works as a promise of health for the service.  You can then
report per circuit not just fail/pass but an extra "healthy" % over time that counts only
requests that resopnd _quickly enough_.

```go
  h := hystrix.Hystrix{}
  circuit := h.MustCreateCircuit("track-my-slo", hystrix.CommandProperties{
    Execution: hystrix.ExecutionConfig{
      // healthy is allowing a full second
      ExecutionTimeout: time.Second,
    },
    GoSpecific: hystrix.ExecutionConfig{
    	// But healthy requests should respond in < 100 ms
      ResponseTimeSLO: time.Millisecond * 100,
      MetricsCollectors:  {
      	ResponseTimeSLO: []hystrix.ResponseTimeSLOCollector {
      		myCustomCollector{},
      	},
      },
    },
  })
  err := c.Execute(func(_ context.Context) error {
  	time.Sleep(time.Millisecond * 250)
  	return nil
  }, nil)
  
  // err will be nil, because the circuit returned quickly enough
  // But a SLO metric of 'fail' will be incremented to indicate the
  // response did not meet service SLO
```

# Why fork go-hystrix

I wanted to change the API to confirm to other design principals (forced asyncronousness, global mutable state, etc),
as well as work with newer Go features (context).
As I worked with go-hystrix, the internal code felt slightly complicated, and as I pulled chunks out, the API turned
into something entirely different.  At that point, I felt a fork was best.  However, I freely admit to borrowing many
ideas from go-hystrix! 

# Benchmarking

This implementation is more efficient than go-hystrix in every configuration.  It has comparable efficiency
to other implementations, in most faster when running with high concurrency. Run benchmarks with `make bench`.

I benchmark the following alternative circuit implementations.  I try to be fair and if
there is a better way to benchmark one of these circuits, please let me know!
* [hystrix-go](https://github.com/afex/hystrix-go)
* [rubyist](https://github.com/rubyist/circuitbreaker)
* [sony/gobreaker](https://github.com/sony/gobreaker)
* [handy/breaker](https://github.com/streadway/handy/tree/master/breaker)

```
> make bench
cd benchmarking && go test -v -benchmem -run=^$ -bench=. . 2> /dev/null
goos: darwin
goarch: amd64
pkg: github.com/cep21/hystrix/benchmarking
BenchmarkCiruits/Hystrix/passing/DefaultConfig/1-8       	 2000000	       801 ns/op	     192 B/op	       4 allocs/op
BenchmarkCiruits/Hystrix/passing/DefaultConfig/75-8      	 5000000	       396 ns/op	     192 B/op	       4 allocs/op
BenchmarkCiruits/Hystrix/passing/NoTimeout/1-8           	 5000000	       315 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/Hystrix/passing/NoTimeout/75-8          	 5000000	       309 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/Hystrix/failing/DefaultConfig/1-8       	10000000	       189 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/Hystrix/failing/DefaultConfig/75-8      	10000000	       123 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/Hystrix/failing/NoTimeout/1-8           	10000000	       196 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/Hystrix/failing/NoTimeout/75-8          	10000000	       122 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/GoHystrix/passing/DefaultConfig/1-8     	  200000	      6779 ns/op	    1000 B/op	      18 allocs/op
BenchmarkCiruits/GoHystrix/passing/DefaultConfig/75-8    	  500000	      7940 ns/op	    1740 B/op	      25 allocs/op
BenchmarkCiruits/GoHystrix/failing/DefaultConfig/1-8     	  300000	      5910 ns/op	    1024 B/op	      19 allocs/op
BenchmarkCiruits/GoHystrix/failing/DefaultConfig/75-8    	 1000000	      2995 ns/op	    1224 B/op	      22 allocs/op
BenchmarkCiruits/rubyist/passing/Threshold-10/1-8        	 1000000	      1434 ns/op	     334 B/op	       5 allocs/op
BenchmarkCiruits/rubyist/passing/Threshold-10/75-8       	 2000000	       782 ns/op	     309 B/op	       4 allocs/op
BenchmarkCiruits/rubyist/failing/Threshold-10/1-8        	10000000	       114 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/rubyist/failing/Threshold-10/75-8       	 1000000	      1023 ns/op	     304 B/op	       4 allocs/op
BenchmarkCiruits/gobreaker/passing/Default/1-8           	 5000000	       203 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/gobreaker/passing/Default/75-8          	 2000000	       748 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/gobreaker/failing/Default/1-8           	20000000	        93.5 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/gobreaker/failing/Default/75-8          	 5000000	       296 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/handy/passing/Default/1-8               	 1000000	      1050 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/handy/passing/Default/75-8              	 1000000	      1755 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/handy/failing/Default/1-8               	 1000000	      1265 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/handy/failing/Default/75-8              	 1000000	      1713 ns/op	       0 B/op	       0 allocs/op
PASS
ok  	github.com/cep21/hystrix/benchmarking	59.518s
``
```

I feel the most important benchmarks are the ones with high concurrency on a passing circuit, since
that is the common case for heavily loaded systems.

```
BenchmarkCiruits/Hystrix/passing/NoTimeout/75-8          	 5000000	       309 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/GoHystrix/passing/DefaultConfig/75-8    	  500000	      7940 ns/op	    1740 B/op	      25 allocs/op
BenchmarkCiruits/rubyist/passing/Threshold-10/75-8       	 2000000	       782 ns/op	     309 B/op	       4 allocs/op
BenchmarkCiruits/gobreaker/passing/Default/75-8          	 2000000	       748 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/handy/passing/Default/75-8              	 1000000	      1755 ns/op	       0 B/op	       0 allocs/op
```

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
