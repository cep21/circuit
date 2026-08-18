# Upgrading from v3 -> v4

## Gopkg.toml removed

The `Gopkg.toml` file and support for [dep](https://github.com/golang/dep) has been
removed. Please use `go.mod` instead.

## Remove the "/v3" root directory

The `/v3` directory has been removed and things have moved to the root directory.  This should not
be a problem if you are using the `go.mod` file.

##  Move statsd implementation to another library

The statsd implementation has been moved to a separate library since the statsd interface was not stable.
If you need statsd metrics, use the implementation [here](https://github.com/cep21/circuit-statsd).

##  Add ctx to the stats interfaces

All metric and circuit interfaces now take a context as the first parameter.  For example, the call
`Success(now time.Time, duration time.Duration)` is now `Success(ctx context.Context, now time.Time, duration time.Duration)`
and the call `Closed(now time.Time)` is now `Closed(ctx context.Context, now time.Time)`.

If you have a custom metric implementation, you will need to add a context to your interface.

##  Move benchmarks to their own repo

The benchmarks have been moved to their own repo.  You can find them [here](https://github.com/cep21/circuit-benchmarks).

## Use Go's builtin atomic package

The atomics package previously implemented atomics manually. This is now using go 1.19's builtin atomics package.

## External API changes to `Circuit`

The following APIs have changed:

* `func (c *Circuit) CloseCircuit()` is now `func (c *Circuit) CloseCircuit(ctx context.Context)`
* `func (c *Circuit) OpenCircuit()` is now `func (c *Circuit) OpenCircuit(ctx context.Context)`

# Behavior changes in v4.2

v4.2 has no breaking API changes, but several bug fixes change observable behavior:

* `ForceOpen` now rejects every request.  Previously the half-open logic was still consulted, so a
  forced-open circuit could still run probe requests.
* `OpenCircuit`/`CloseCircuit` act on the circuit's underlying state even while
  `ForceOpen`/`ForcedClosed` is set (that state applies once the override is cleared), and `Metrics`
  listeners are notified accordingly.  `IsOpen` still reflects the override while it is set.
* `hystrix.Closer`: half-open probes are only admitted after the `SleepWindow` that starts when the
  circuit opens, and only requests that started after the circuit opened count toward closing it.  A
  request already in flight when the circuit opened no longer closes it on success, nor does its late
  failure reset the probe streak.  The minimum open duration is therefore one `SleepWindow` (default
  5s); tune `SleepWindow` if you relied on faster recovery.  Unit tests that drive a `Closer` directly
  must call `Opened()` first and use a start time after it (e.g. `Opened(t0); Success(t0+window, d)`).
* A `hystrix.Closer` must not be shared between circuits (always use the factory).  A closer that is
  asked to `Allow` without having seen `Opened` denies and (unless it was told `Closed` less than a
  `SleepWindow` ago) arms a fresh `SleepWindow` instead of allowing immediately.
* `hystrix.Closer.SetConfigThreadSafe` no longer applies `AfterFunc` (use `SetConfigNotThreadSafe`);
  this removed a data race.
* `Metrics.Opened`/`Closed` are delivered strictly alternating and in transition order, without locks
  held.  Under contention or re-entrancy a notification may be delivered on a different goroutine,
  after the `OpenCircuit`/`CloseCircuit` call that caused it returned.  The `now` passed to `Opened`
  is taken at the moment of transition.
* `Circuit.CircuitMetricsCollector` now holds only the configured `Metrics.Circuit` listeners.  The
  circuit's own `OpenToClose`/`ClosedToOpen` logic is notified of transitions directly (still before
  any of those listeners) and is no longer an element of that slice.
* Custom `ClosedToOpen`/`OpenToClosed` implementations are told `Opened`/`Closed` (and re-asked
  `ShouldOpen`/`ShouldClose` right before the state flips) inside the circuit's transition critical
  section.  Those methods must be quick and must not call `OpenCircuit`/`CloseCircuit` on the same
  circuit: that now deadlocks (it only happened to work before).
* `hystrix.Opener` opens at exactly `ErrorThresholdPercentage` (integer math; previously e.g. 57/100
  errors did not trip a 57% threshold).
* `Manager.CreateCircuit` no longer runs `DefaultCircuitProperties` for a duplicate name and no longer
  holds its lock while constructors run.
* `Execute`/`Run`/`Go` with a nil `runFunc` return nil instead of panicking on nil/`Disabled` circuits.
* `faststats.RollingBuckets` gained an unexported field: construct it with keyed fields
  (`RollingBuckets{NumBuckets: n, ...}`); unkeyed composite literals no longer compile (permitted by
  the Go 1 compatibility rules, but worth knowing).  It also must not be copied after first use.
* expvar output now includes collectors whose `Var` lacks a `Value()` method (rendered from
  `String()`); JSON shapes of existing keys are unchanged.
