package metriceventstream

import (
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/cep21/circuit/v4"
	"github.com/cep21/circuit/v4/closers/hystrix"
	"github.com/cep21/circuit/v4/metrics/rolling"
)

// benchManager creates numCircuits circuits whose latency windows hold perBucket samples in each of their 6 buckets
func benchManager(numCircuits int, perBucket int) *circuit.Manager {
	now := time.Now()
	start := now.Add(-72 * time.Hour)
	createdAt := func() time.Time { return start }
	sf := &rolling.StatFactory{
		RunConfig:      rolling.RunStatsConfig{Now: createdAt},
		FallbackConfig: rolling.FallbackStatsConfig{Now: createdAt},
	}
	hf := &hystrix.Factory{ConfigureOpener: hystrix.ConfigureOpener{Now: createdAt}}
	m := &circuit.Manager{DefaultCircuitProperties: []circuit.CommandPropertiesConstructor{
		func(string) circuit.Config {
			// Frozen clock so the windows do not drain while the benchmark runs
			return circuit.Config{General: circuit.GeneralConfig{TimeKeeper: circuit.TimeKeeper{Now: func() time.Time { return now }}}}
		},
		sf.CreateConfig, hf.Configure,
	}}
	r := rand.New(rand.NewSource(1))
	for i := 0; i < numCircuits; i++ {
		name := "service.endpoint." + strconv.Itoa(i)
		m.MustCreateCircuit(name)
		rs := sf.RunStats(name)
		for bucket := 5; bucket >= 0; bucket-- {
			at := now.Add(-time.Duration(bucket) * 10 * time.Second)
			for j := 0; j < perBucket; j++ {
				rs.Latencies.AddDuration(time.Duration(5e6*math.Exp(r.NormFloat64())), at)
				rs.Successes.Inc(at)
			}
		}
	}
	return m
}

// BenchmarkTick drives the real Start loop back to back.  One op is one tick over 500 circuits.
func BenchmarkTick(b *testing.B) {
	const numCircuits = 500
	for _, tc := range []struct {
		name      string
		perBucket int
	}{{"busy", 100}, {"light", 5}, {"idle", 0}} {
		b.Run(tc.name, func(b *testing.B) {
			es := &MetricEventStream{Manager: benchManager(numCircuits, tc.perBucket), TickDuration: time.Nanosecond}
			es.once.Do(es.doOnce)
			events := make(chan []byte, 8*numCircuits)
			es.eventStreams[&http.Request{}] = events
			done := make(chan error)
			b.ReportAllocs()
			b.ResetTimer()
			go func() { done <- es.Start() }()
			for i := 0; i < b.N*numCircuits; i++ {
				<-events
			}
			b.StopTimer()
			_ = es.Close()
			if err := <-done; err != nil {
				b.Fatal(err)
			}
		})
	}
}
