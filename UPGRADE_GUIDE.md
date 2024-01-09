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