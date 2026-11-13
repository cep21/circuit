package rolling_test

import (
	"context"
	"errors"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cep21/circuit/v4"
	"github.com/cep21/circuit/v4/closers/hystrix"
	"github.com/cep21/circuit/v4/metrics/responsetimeslo"
	"github.com/cep21/circuit/v4/metrics/rolling"
)

var errBench = errors.New("bench failure")

func benchPasses(context.Context) error          { return nil }
func benchFails(context.Context) error           { return errBench }
func benchFallback(context.Context, error) error { return nil }

var benchSpinSink atomic.Uint64

// benchWorks burns roughly a microsecond of core-local CPU, so that contended cache lines are hit at a rate closer
// to a (very) busy real service than to a tight loop.
func benchWorks(context.Context) error {
	x := uint64(88172645463325252)
	for i := 0; i < 400; i++ {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
	}
	if x == 0 {
		benchSpinSink.Add(1)
	}
	return nil
}

// benchManager is the full production stack: hystrix opener+closer, rolling stats and an SLO tracker, with every
// rolling window created three days ago (monotonic clock preserved).
func benchManager(timeout time.Duration) *circuit.Manager {
	longAgo := time.Now().Add(-72 * time.Hour)
	createdAt := func() time.Time { return longAgo }
	sf := &rolling.StatFactory{
		RunConfig:      rolling.RunStatsConfig{Now: createdAt},
		FallbackConfig: rolling.FallbackStatsConfig{Now: createdAt},
	}
	hf := &hystrix.Factory{
		// Far > 100%: ShouldOpen does all of its work on every failure, but the circuit never opens on its own
		ConfigureOpener: hystrix.ConfigureOpener{ErrorThresholdPercentage: 1 << 24, Now: createdAt},
		ConfigureCloser: hystrix.ConfigureCloser{SleepWindow: time.Hour},
	}
	slo := &responsetimeslo.Factory{}
	return &circuit.Manager{DefaultCircuitProperties: []circuit.CommandPropertiesConstructor{
		func(string) circuit.Config {
			return circuit.Config{
				Execution: circuit.ExecutionConfig{MaxConcurrentRequests: -1, Timeout: timeout},
				Fallback:  circuit.FallbackConfig{MaxConcurrentRequests: -1},
			}
		},
		slo.CommandProperties, sf.CreateConfig, hf.Configure,
	}}
}

func BenchmarkE2E(b *testing.B) {
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		timeout  time.Duration
		open     bool
		run      func(context.Context) error
		fallback func(context.Context, error) error
	}{
		{name: "success", timeout: -1, run: benchPasses},
		{name: "success-timeout", timeout: time.Second, run: benchPasses},
		{name: "success-1us-work", timeout: -1, run: benchWorks},
		{name: "failure", timeout: -1, run: benchFails},
		{name: "failure-fallback", timeout: -1, run: benchFails, fallback: benchFallback},
		{name: "open", timeout: -1, open: true, run: benchPasses},
		{name: "open-fallback", timeout: -1, open: true, run: benchPasses, fallback: benchFallback},
	} {
		setup := func() *circuit.Circuit {
			c := benchManager(tc.timeout).MustCreateCircuit("bench")
			if tc.open {
				c.OpenCircuit(ctx)
			}
			return c
		}
		b.Run(tc.name+"/serial", func(b *testing.B) {
			c := setup()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = c.Execute(ctx, tc.run, tc.fallback)
			}
			if c.IsOpen() != tc.open {
				b.Fatal("circuit changed state")
			}
		})
		b.Run(tc.name+"/parallel", func(b *testing.B) {
			c := setup()
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					_ = c.Execute(ctx, tc.run, tc.fallback)
				}
			})
			if c.IsOpen() != tc.open {
				b.Fatal("circuit changed state")
			}
		})
	}
}

// BenchmarkE2EManyCircuits spreads traffic over many circuits: little true sharing, but cache-cold state, so it is
// the benchmark that pays for a larger memory footprint.
func BenchmarkE2EManyCircuits(b *testing.B) {
	ctx := context.Background()
	const n = 4096
	m := benchManager(-1)
	all := make([]*circuit.Circuit, n)
	for i := range all {
		all[i] = m.MustCreateCircuit(strconv.Itoa(i))
	}
	// Independent pseudo random walks, so goroutines do not convoy through the same circuits
	next := func(x uint32) uint32 {
		x ^= x << 13
		x ^= x >> 17
		return x ^ x<<5
	}
	b.Run("serial", func(b *testing.B) {
		x := uint32(1)
		for i := 0; i < b.N; i++ {
			x = next(x)
			_ = all[x%n].Execute(ctx, benchPasses, nil)
		}
	})
	b.Run("parallel", func(b *testing.B) {
		var seed atomic.Uint32
		b.RunParallel(func(pb *testing.PB) {
			x := seed.Add(0x9e3779b9)
			for pb.Next() {
				x = next(x)
				_ = all[x%n].Execute(ctx, benchPasses, nil)
			}
		})
	})
}

// BenchmarkCircuitFootprint reports the retained heap per fully configured circuit
func BenchmarkCircuitFootprint(b *testing.B) {
	const n = 2000
	var perCircuit float64
	for i := 0; i < b.N; i++ {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		m := benchManager(-1)
		for j := 0; j < n; j++ {
			m.MustCreateCircuit(strconv.Itoa(j))
		}
		runtime.GC()
		runtime.ReadMemStats(&after)
		perCircuit = float64(after.HeapAlloc-before.HeapAlloc) / n
		runtime.KeepAlive(m)
	}
	b.ReportMetric(perCircuit, "B/circuit")
}

// BenchmarkExpvar renders the expvar JSON of one busy circuit
func BenchmarkExpvar(b *testing.B) {
	ctx := context.Background()
	m := &circuit.Manager{DefaultCircuitProperties: []circuit.CommandPropertiesConstructor{
		(&responsetimeslo.Factory{}).CommandProperties,
		(&rolling.StatFactory{}).CreateConfig,
		(&hystrix.Factory{}).Configure,
	}}
	c := m.MustCreateCircuit("bench")
	for i := 0; i < 1000; i++ {
		_ = c.Execute(ctx, benchPasses, nil)
	}
	v := m.Var()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if len(v.String()) < 100 {
			b.Fatal("short output")
		}
	}
}
