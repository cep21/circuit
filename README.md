<!-- Image designed by Jack Lindamood, Licensed under the Creative Commons 3.0 Attributions license, originate from
https://github.com/golang-samples/gopher-vector design by Takuya Ueda -->
![Mascot](https://cep21.github.io/circuit/imgs/hystrix-gopher_100px.png)
# Circuit
[![Build Status](https://travis-ci.org/cep21/circuit.svg?branch=master)](https://travis-ci.org/cep21/circuit)
[![GoDoc](https://godoc.org/github.com/cep21/circuit?status.svg)](https://godoc.org/github.com/cep21/circuit)
[![Coverage Status](https://coveralls.io/repos/github/cep21/circuit/badge.svg)](https://coveralls.io/github/cep21/circuit)

Circuit is an efficient and feature complete [Hystrix](https://github.com/Netflix/Hystrix) like Go implementation of
the [circuit breaker pattern](https://docs.microsoft.com/en-us/azure/architecture/patterns/circuit-breaker).
Learn more about the problems Hystrix and other circuit breakers solve on the
[Hystrix Wiki](https://github.com/Netflix/Hystrix/wiki).  A short summary of advantages are:

* A downstream service failed and all requests hang forever.  Without a circuit, your service would also hang forever.
  Because you have a circuit, you detect this failure quickly and can return errors quickly while waiting for the
  downstream service to recover.
* Circuits make great monitoring and metrics boundaries, creating common metric names for the common downstream failure
  types.  This package goes further to formalize this in a SLO tracking pattern.
* Circuits create a common place for downstream failure fallback logic.
* Downstream services sometimes fail entirely when overloaded.  While in a degraded state, circuits allow you to push
  downstream services to the edge between absolute failure and mostly working.
* Open/Close state of a circuit is a clear early warning sign of downstream failures.
* Circuits allow you to protect your dependencies from abnormal rushes of traffic.

There are a large number of examples on the [godoc](https://godoc.org/github.com/cep21/circuit#pkg-examples) that are
worth looking at.  They tend to be more up to date than the README doc.

# Feature set

* No forced goroutines
* recoverable panic()
* Integrated with context.Context
* Comprehensive metric tracking
* Efficient implementation with Benchmarks
* Low/zero memory allocation costs
* Support for [Netflix Hystrix dashboards](https://github.com/Netflix/Hystrix/wiki/Dashboard), even with custom
  circuit transition logic
* Multiple error handling features
* Expose circuit health and configuration on expvar
* SLO tracking
* Customizable state transition logic, allowing complex circuit state changes
* Live configuration changes
* Many tests and examples
* Good inline documentation
* Generatable interface wrapping support with https://github.com/twitchtv/circuitgen
* Support for [Additive increase/multiplicative decrease](https://github.com/cep21/aimdcloser)

# Usage

## [Hello world circuit](https://godoc.org/github.com/cep21/circuit#example-Manager-MustCreateCircuit-Helloworld)

This example shows how to create a hello-world circuit from the circuit manager

```go
// Manages all our circuits
h := circuit.Manager{}
// Create a circuit with a unique name
c := h.MustCreateCircuit("hello-world")
// Call the circuit
errResult := c.Execute(context.Background(), func(ctx context.Context) error {
  return nil
}, nil)
fmt.Println("Result of execution:", errResult)
// Output: Result of execution: <nil>
```

## [Hello world fallback](https://godoc.org/github.com/cep21/circuit#example-Circuit-Execute-Fallbackhelloworld)

This example shows how fallbacks execute to return alternate errors or provide
logic when the circuit is open.

```go
// You can create circuits without using the manager
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

## [Running inside a Goroutine](https://godoc.org/github.com/cep21/circuit#example-Circuit-Go)

It is recommended to use `circuit.Execute` and a context aware function.  If, however, you want to exit
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

## [Hystrix Configuration](https://godoc.org/github.com/cep21/circuit/closers/hystrix#example-ConfigFactory-Configure)

All configuration parameters are documented in config.go.  Your circuit open/close logic configuration is documented
with the logic.  For hystrix, this configuration is in closers/hystrix and well documented on
[the Hystrix wiki](https://github.com/Netflix/Hystrix/wiki/Configuration).

This example configures the circuit to use Hystrix open/close logic with the default Hystrix parameters

```go
configuration := hystrix.ConfigFactory{
  // Hystrix open logic is to open the circuit after an % of errors
  ConfigureOpener: hystrix.ConfigureOpener{
    // We change the default to wait for 10 requests, not 20, before checking to close
    RequestVolumeThreshold: 10,
    // The default values match what hystrix does by default
  },
  // Hystrix close logic is to sleep then check
  ConfigureCloser: hystrix.ConfigureCloser{
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

## [Enable dashboard metrics](https://godoc.org/github.com/cep21/circuit/metriceventstream#example-MetricEventStream)

Dashboard metrics can be enabled with the MetricEventStream object. This example creates an event stream handler,
starts it, then later closes the handler

```go
// metriceventstream uses rolling stats to report circuit information
sf := rolling.StatFactory{}
h := circuit.Manager{
  DefaultCircuitProperties: []circuit.CommandPropertiesConstructor{sf.CreateConfig},
}
es := metriceventstream.MetricEventStream{
  Manager: &h,
}
go func() {
  if err := es.Start(); err != nil {
    log.Fatal(err)
  }
}()
// ES is a http.Handler, so you can pass it directly to your mux
http.Handle("/hystrix.stream", &es)
// ...
if err := es.Close(); err != nil {
  log.Fatal(err)
}
// Output:
```

## [Enable expvar](https://godoc.org/github.com/cep21/circuit#example-Manager-Var)

If you wanted to publish hystrix information on Expvar, you can register your manager.

```go
h := circuit.Manager{}
expvar.Publish("hystrix", h.Var())
```

## [Custom metrics](https://godoc.org/github.com/cep21/circuit#example-Config--Custommetrics)

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

## [Panics](https://godoc.org/github.com/cep21/circuit#example-Circuit-Execute-Panics)

Code executed with `Execute` does not spawn a goroutine and panics naturally go up the call stack to the caller.
This is also true for `Go`, where we attempt to recover and throw panics on the same stack that
calls Go.  This example will panic, and the panic can be caught up the stack.

 ```go
h := circuit.Manager{}
c := h.MustCreateCircuit("panic_up")

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

## [Runtime configuration changes](https://godoc.org/github.com/cep21/circuit/closers/hystrix#example-Closer-SetConfigThreadSafe)

Most configuration properties on
[the Hystrix Configuration page](https://github.com/Netflix/Hystrix/wiki/Configuration) that say they are modifyable at
runtime can be changed on the Circuit in a thread safe way.  Most of the ones that cannot are related to stat
collection.

This example shows how to update hystrix configuration at runtime.

```go
// Start off using the defaults
configuration := hystrix.ConfigFactory{}
h := circuit.Manager{
  // Tell the manager to use this configuration factory whenever it makes a new circuit
  DefaultCircuitProperties: []circuit.CommandPropertiesConstructor{configuration.Configure},
}
c := h.MustCreateCircuit("hystrix-circuit")
fmt.Println("The default sleep window", c.OpenToClose.(*hystrix.Closer).Config().SleepWindow)
// This configuration update function is thread safe.  We can modify this at runtime while the circuit is active
c.OpenToClose.(*hystrix.Closer).SetConfigThreadSafe(hystrix.ConfigureCloser{
  SleepWindow: time.Second * 3,
})
fmt.Println("The new sleep window", c.OpenToClose.(*hystrix.Closer).Config().SleepWindow)
// Output:
// The default sleep window 5s
// The new sleep window 3s
```

## [Not counting early terminations as failures](https://godoc.org/github.com/cep21/circuit#example-Circuit--Noearlyterminate)

If the context passed into a circuit function ends, before the circuit can
finish, it does not count the circuit as unhealthy.  You can disable this
behavior with the `IgnoreInterrupts` flag.

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

## [Configuration factories](https://godoc.org/github.com/cep21/circuit#example-CommandPropertiesConstructor)

Configuration factories are supported on the root manager object.  This allows you to create dynamic configuration per
circuit name.

You can use DefaultCircuitProperties to set configuration dynamically for any circuit

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
h.MustCreateCircuit("v1")
fmt.Println("The timeout of v1 is", h.GetCircuit("v1").Config().Execution.Timeout)
// Output: The timeout of v1 is 1s
```

## [StatsD configuration factory](https://godoc.org/github.com/cep21/circuit/metrics/statsdmetrics#example-CommandFactory-CommandProperties)

A configuration factory for statsd is provided inside ./metrics/statsdmetrics

This example shows how to inject a statsd metric collector into a circuit.

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

## [Service health tracking](https://godoc.org/github.com/cep21/circuit/metrics/responsetimeslo#example-Factory)

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

This example creates a SLO tracker that counts failures at less than 20 ms.  You
will need to provide your own Collectors.

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

## [Not counting user error as a fault](https://godoc.org/github.com/cep21/circuit#example-BadRequest)

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

# [Benchmarking](https://github.com/cep21/circuit/blob/master/benchmarking/benchmark_hystrix_test.go)

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
BenchmarkCiruits/cep21-circuit/Hystrix/passing/1-8       	 2000000	       896 ns/op	     192 B/op	       4 allocs/op
BenchmarkCiruits/cep21-circuit/Hystrix/passing/75-8      	 3000000	       500 ns/op	     192 B/op	       4 allocs/op
BenchmarkCiruits/cep21-circuit/Hystrix/failing/1-8       	10000000	       108 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/cep21-circuit/Hystrix/failing/75-8      	20000000	        82.5 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/cep21-circuit/Minimal/passing/1-8       	10000000	       165 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/cep21-circuit/Minimal/passing/75-8      	20000000	        87.7 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/cep21-circuit/Minimal/failing/1-8       	20000000	        64.4 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/cep21-circuit/Minimal/failing/75-8      	100000000	        19.6 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/cep21-circuit/UseGo/passing/1-8         	 1000000	      1300 ns/op	     256 B/op	       5 allocs/op
BenchmarkCiruits/cep21-circuit/UseGo/passing/75-8        	 5000000	       374 ns/op	     256 B/op	       5 allocs/op
BenchmarkCiruits/cep21-circuit/UseGo/failing/1-8         	 1000000	      1348 ns/op	     256 B/op	       5 allocs/op
BenchmarkCiruits/cep21-circuit/UseGo/failing/75-8        	 5000000	       372 ns/op	     256 B/op	       5 allocs/op
BenchmarkCiruits/GoHystrix/DefaultConfig/passing/1-8     	  200000	      8146 ns/op	    1001 B/op	      18 allocs/op
BenchmarkCiruits/GoHystrix/DefaultConfig/passing/75-8    	  500000	      2498 ns/op	     990 B/op	      20 allocs/op
BenchmarkCiruits/GoHystrix/DefaultConfig/failing/1-8     	  200000	      6299 ns/op	    1020 B/op	      19 allocs/op
BenchmarkCiruits/GoHystrix/DefaultConfig/failing/75-8    	 1000000	      1582 ns/op	    1003 B/op	      20 allocs/op
BenchmarkCiruits/rubyist/Threshold-10/passing/1-8        	 1000000	      1834 ns/op	     332 B/op	       5 allocs/op
BenchmarkCiruits/rubyist/Threshold-10/passing/75-8       	 2000000	       849 ns/op	     309 B/op	       4 allocs/op
BenchmarkCiruits/rubyist/Threshold-10/failing/1-8        	20000000	       114 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/rubyist/Threshold-10/failing/75-8       	 5000000	       302 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/gobreaker/Default/passing/1-8           	10000000	       202 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/gobreaker/Default/passing/75-8          	 2000000	       698 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/gobreaker/Default/failing/1-8           	20000000	        90.6 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/gobreaker/Default/failing/75-8          	 5000000	       346 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/handy/Default/passing/1-8               	 2000000	      1075 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/handy/Default/passing/75-8              	 1000000	      1795 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/handy/Default/failing/1-8               	 1000000	      1272 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/handy/Default/failing/75-8              	 1000000	      1686 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/iand_circuit/Default/passing/1-8        	10000000	       119 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/iand_circuit/Default/passing/75-8       	 5000000	       349 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/iand_circuit/Default/failing/1-8        	100000000	        20.4 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/iand_circuit/Default/failing/75-8       	300000000	         5.46 ns/op	       0 B/op	       0 allocs/op
PASS
ok      github.com/cep21/circuit/benchmarking   59.518s
```

Limiting to just high concurrency passing circuits (the common case).

```
BenchmarkCiruits/cep21-circuit/Minimal/passing/75-8      	20000000	        87.7 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/GoHystrix/DefaultConfig/passing/75-8    	  500000	      2498 ns/op	     990 B/op	      20 allocs/op
BenchmarkCiruits/rubyist/Threshold-10/passing/75-8       	 2000000	       849 ns/op	     309 B/op	       4 allocs/op
BenchmarkCiruits/gobreaker/Default/passing/75-8          	 2000000	       698 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/handy/Default/passing/75-8              	 1000000	      1795 ns/op	       0 B/op	       0 allocs/op
BenchmarkCiruits/iand_circuit/Default/passing/75-8       	 5000000	       349 ns/op	       0 B/op	       0 allocs/op
```

# [Development](https://github.com/cep21/circuit/blob/master/Makefile)

Make sure your tests pass with `make test` and your lints pass with `make lint`.

# [Example](https://github.com/cep21/circuit/blob/master/example/main.go)

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

[![dashboard](https://cep21.github.io/circuit/imgs/hystrix_ui.png)](https://cep21.github.io/hystrix/imgs/hystrix_ui.png)
