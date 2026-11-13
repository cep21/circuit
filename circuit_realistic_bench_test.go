package circuit_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/cep21/circuit/v4"
	"github.com/cep21/circuit/v4/closers/hystrix"
	"github.com/cep21/circuit/v4/metrics/rolling"
)

// These benchmarks live in the external test package so they can attach the hystrix and rolling packages.  They use
// the default Timeout and concurrency limits unless the case says otherwise.

var (
	errPlain   = errors.New("plain failure")
	errWrapped = fmt.Errorf("query users: %w", fmt.Errorf("conn: %w", errPlain))
	errJoined  = fmt.Errorf("fanout: %w", errors.Join(errPlain, io.EOF, errWrapped))
	// errNet is shaped like what net/http returns when a dial is refused
	errNet error = &url.Error{Op: "Get", URL: "http://localhost", Err: &net.OpError{
		Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED),
	}}
	errWrappedBad = fmt.Errorf("handler: %w", circuit.SimpleBadRequest{Err: errPlain})
)

type benchCtxKey struct{}

func runIgnoresCtx(context.Context) error { return nil }

func runChecksErr(ctx context.Context) error { return ctx.Err() }

func runReadsValue(ctx context.Context) error {
	_ = ctx.Value(benchCtxKey{})
	return nil
}

func runSelectsDone(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// runDerivesChild does what an HTTP or database client does with the context it is handed
func runDerivesChild(ctx context.Context) error {
	_ = ctx.Value(benchCtxKey{})
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	return runSelectsDone(child)
}

func returns(err error) func(context.Context) error {
	return func(context.Context) error { return err }
}

func fallbackPasses(context.Context, error) error { return nil }

type benchStack struct {
	name string
	make func(cfg circuit.Config) *circuit.Circuit
}

var benchStacks = []benchStack{
	{name: "bare", make: func(cfg circuit.Config) *circuit.Circuit {
		return circuit.NewCircuitFromConfig("b", cfg)
	}},
	{name: "hystrix", make: func(cfg circuit.Config) *circuit.Circuit {
		// The opener does all of its usual work but never trips, and an open circuit stays open
		h := hystrix.Factory{
			ConfigureOpener: hystrix.ConfigureOpener{ErrorThresholdPercentage: 1000},
			ConfigureCloser: hystrix.ConfigureCloser{SleepWindow: time.Hour},
		}
		m := circuit.Manager{DefaultCircuitProperties: []circuit.CommandPropertiesConstructor{h.Configure}}
		return m.MustCreateCircuit("b", cfg)
	}},
	{name: "hystrix+rolling", make: func(cfg circuit.Config) *circuit.Circuit {
		h := hystrix.Factory{
			ConfigureOpener: hystrix.ConfigureOpener{ErrorThresholdPercentage: 1000},
			ConfigureCloser: hystrix.ConfigureCloser{SleepWindow: time.Hour},
		}
		s := rolling.StatFactory{}
		m := circuit.Manager{DefaultCircuitProperties: []circuit.CommandPropertiesConstructor{h.Configure, s.CreateConfig}}
		return m.MustCreateCircuit("b", cfg)
	}},
}

// benchBoth runs op serially and from GOMAXPROCS goroutines, against a new circuit of every stack.  setup is called
// once per goroutine and returns the operation to time, like one server goroutine handling many requests.
func benchBoth(b *testing.B, name string, cfg circuit.Config, prepare func(c *circuit.Circuit), setup func(b *testing.B, c *circuit.Circuit) func()) {
	for _, stack := range benchStacks {
		newCircuit := func() *circuit.Circuit {
			c := stack.make(cfg)
			if prepare != nil {
				prepare(c)
			}
			return c
		}
		b.Run(name+"/"+stack.name+"/serial", func(b *testing.B) {
			op := setup(b, newCircuit())
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				op()
			}
		})
		b.Run(name+"/"+stack.name+"/parallel", func(b *testing.B) {
			c := newCircuit()
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				op := setup(b, c)
				for pb.Next() {
					op()
				}
			})
		})
	}
}

type benchParent struct {
	name string
	make func(b *testing.B) context.Context
}

var benchParents = []benchParent{
	{name: "background", make: func(*testing.B) context.Context { return context.Background() }},
	{name: "cancellable", make: func(b *testing.B) context.Context {
		// Shaped like an http.Request context: cancellable, with values layered on top
		ctx, cancel := context.WithCancel(context.Background())
		b.Cleanup(cancel)
		return context.WithValue(ctx, benchCtxKey{}, "value")
	}},
}

func openCircuit(c *circuit.Circuit) { c.OpenCircuit(context.Background()) }

func executes(parent benchParent, run func(context.Context) error, fallback func(context.Context, error) error) func(b *testing.B, c *circuit.Circuit) func() {
	return func(b *testing.B, c *circuit.Circuit) func() {
		ctx := parent.make(b)
		return func() { _ = c.Execute(ctx, run, fallback) }
	}
}

func goes(parent benchParent, run func(context.Context) error, fallback func(context.Context, error) error) func(b *testing.B, c *circuit.Circuit) func() {
	return func(b *testing.B, c *circuit.Circuit) func() {
		ctx := parent.make(b)
		return func() { _ = c.Go(ctx, run, fallback) }
	}
}

var noTimeout = circuit.Config{Execution: circuit.ExecutionConfig{Timeout: -1}}

func BenchmarkSuccess(b *testing.B) {
	runs := []struct {
		name string
		run  func(context.Context) error
	}{
		{"ignores-ctx", runIgnoresCtx},
		{"ctx-err", runChecksErr},
		{"ctx-value", runReadsValue},
		{"ctx-done", runSelectsDone},
		{"ctx-child", runDerivesChild},
	}
	for _, parent := range benchParents {
		for _, r := range runs {
			benchBoth(b, parent.name+"/"+r.name, circuit.Config{}, nil, executes(parent, r.run, fallbackPasses))
		}
	}
	benchBoth(b, "no-timeout/ignores-ctx", noTimeout, nil, executes(benchParents[1], runIgnoresCtx, fallbackPasses))
	benchBoth(b, "no-timeout/ctx-child", noTimeout, nil, executes(benchParents[1], runDerivesChild, fallbackPasses))
}

func BenchmarkFailure(b *testing.B) {
	errs := []struct {
		name string
		err  error
	}{
		{"plain", errPlain},
		{"wrapped", errWrapped},
		{"joined", errJoined},
		{"net", errNet},
		{"wrapped-bad-request", errWrappedBad},
	}
	for _, e := range errs {
		benchBoth(b, e.name+"/no-fallback", circuit.Config{}, nil, executes(benchParents[1], returns(e.err), nil))
		benchBoth(b, e.name+"/fallback", circuit.Config{}, nil, executes(benchParents[1], returns(e.err), fallbackPasses))
	}
	benchBoth(b, "no-timeout/net/fallback", noTimeout, nil, executes(benchParents[1], returns(errNet), fallbackPasses))
}

func BenchmarkInterrupt(b *testing.B) {
	alreadyCancelled := benchParent{make: func(*testing.B) context.Context {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx
	}}
	cancelsMidRun := func(_ *testing.B, c *circuit.Circuit) func() {
		return func() {
			ctx, cancel := context.WithCancel(context.Background())
			_ = c.Execute(ctx, func(ctx context.Context) error {
				cancel()
				<-ctx.Done()
				return ctx.Err()
			}, nil)
		}
	}
	benchBoth(b, "already-cancelled", circuit.Config{}, nil, executes(alreadyCancelled, runChecksErr, nil))
	benchBoth(b, "already-cancelled/no-timeout", noTimeout, nil, executes(alreadyCancelled, runChecksErr, nil))
	benchBoth(b, "mid-run", circuit.Config{}, nil, cancelsMidRun)
}

func BenchmarkOpen(b *testing.B) {
	benchBoth(b, "no-fallback", circuit.Config{}, openCircuit, executes(benchParents[1], runIgnoresCtx, nil))
	benchBoth(b, "fallback", circuit.Config{}, openCircuit, executes(benchParents[1], runIgnoresCtx, fallbackPasses))
}

// BenchmarkThrottled holds the circuit's only slot so that every timed request is shed
func BenchmarkThrottled(b *testing.B) {
	cfg := circuit.Config{Execution: circuit.ExecutionConfig{MaxConcurrentRequests: 1, Timeout: time.Hour}}
	release := make(chan struct{})
	defer close(release)
	saturate := func(c *circuit.Circuit) {
		started := make(chan struct{})
		go func() {
			_ = c.Run(context.Background(), func(context.Context) error {
				close(started)
				<-release
				return nil
			})
		}()
		<-started
	}
	benchBoth(b, "no-fallback", cfg, saturate, executes(benchParents[1], runIgnoresCtx, nil))
	benchBoth(b, "fallback", cfg, saturate, executes(benchParents[1], runIgnoresCtx, fallbackPasses))
}

func BenchmarkGo(b *testing.B) {
	lost := circuit.Config{General: circuit.GeneralConfig{GoLostErrors: func(error, interface{}) {}}}
	benchBoth(b, "success", lost, nil, goes(benchParents[1], runDerivesChild, fallbackPasses))
	benchBoth(b, "success/no-timeout", noTimeout, nil, goes(benchParents[0], runIgnoresCtx, nil))
	benchBoth(b, "open/no-fallback", lost, openCircuit, goes(benchParents[1], runIgnoresCtx, nil))
	benchBoth(b, "open/fallback", lost, openCircuit, goes(benchParents[1], runIgnoresCtx, fallbackPasses))
	benchBoth(b, "failure/fallback", lost, nil, goes(benchParents[1], returns(errNet), fallbackPasses))
	benchBoth(b, "abandoned", lost, nil, func(_ *testing.B, c *circuit.Circuit) func() {
		return func() {
			ctx, cancel := context.WithCancel(context.Background())
			release := make(chan struct{})
			_ = c.Go(ctx, func(context.Context) error {
				cancel()
				<-release
				return errPlain
			}, nil)
			close(release)
		}
	})
}

func BenchmarkManagerGetCircuit(b *testing.B) {
	m := circuit.Manager{}
	names := make([]string, 64)
	for i := range names {
		names[i] = "dependency-" + strconv.Itoa(i)
		m.MustCreateCircuit(names[i])
	}
	b.Run("serial", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = m.GetCircuit(names[i%len(names)])
		}
	})
	b.Run("parallel", func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for i := 0; pb.Next(); i++ {
				_ = m.GetCircuit(names[i%len(names)])
			}
		})
	})
	b.Run("parallel/get-and-run", func(b *testing.B) {
		ctx := benchParents[1].make(b)
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			for i := 0; pb.Next(); i++ {
				_ = m.GetCircuit(names[i%len(names)]).Run(ctx, runSelectsDone)
			}
		})
	})
}

func BenchmarkManagerCreateCircuit(b *testing.B) {
	for _, size := range []int{10, 1000} {
		names := make([]string, size)
		for i := range names {
			names[i] = "dependency-" + strconv.Itoa(i)
		}
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				m := circuit.Manager{}
				for _, name := range names {
					m.MustCreateCircuit(name)
				}
			}
		})
	}
}
