package statsdmetrics

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cep21/circuit/v3"
	"github.com/cep21/circuit/v3/internal/clock"
	"github.com/cep21/circuit/v3/metrics/responsetimeslo"
	"github.com/stretchr/testify/require"
)

type rememberStats struct {
	incs    map[string][]int64
	gauges  map[string][]int64
	timings map[string][]time.Duration

	mu sync.Mutex
}

func (n *rememberStats) Inc(stat string, val int64, sampleRate float32) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.incs == nil {
		n.incs = make(map[string][]int64)
	}
	n.incs[stat] = append(n.incs[stat], val)
	return nil
}

func (n *rememberStats) EachGauge(f func(s string, vals []int64)) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for k, v := range n.gauges {
		f(k, v)
	}
}

func (n *rememberStats) EachCounter(f func(s string, vals []int64)) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for k, v := range n.incs {
		f(k, v)
	}
}

func (n *rememberStats) NumGauges() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.gauges)
}

func (n *rememberStats) Gauge(stat string, val int64, sampleRate float32) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.gauges == nil {
		n.gauges = make(map[string][]int64)
	}
	n.gauges[stat] = append(n.gauges[stat], val)
	return nil
}

func (n *rememberStats) TimingDuration(stat string, val time.Duration, sampleRate float32) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.timings == nil {
		n.timings = make(map[string][]time.Duration)
	}
	n.timings[stat] = append(n.timings[stat], val)
	return nil
}

var _ StatSender = &rememberStats{}

func TestCommandFactory_config(t *testing.T) {
	x := CommandFactory{
		StatSender: &rememberStats{},
		SampleRate: .5,
		SanitizeStatsdFunction: func(_ string) string {
			return "_bob_"
		},
	}
	if sr := x.sampleRate(); sr != .5 {
		t.Fatalf("expected .5 sample rate, not %f", sr)
	}
	if s := x.sanitizeStatsdFunction()(""); s != "_bob_" {
		t.Fatalf("expected _bob_, not %s", s)
	}
}

func TestSanitizeStatsd(t *testing.T) {
	if s := sanitizeStatsd("aA0"); s != "aA0" {
		t.Fatalf("expect aA0 back")
	}
	if s := sanitizeStatsd("abc.123.*&#"); s != "abc_123____" {
		t.Fatalf("expect abc_123____ back, got %s", s)
	}
}

func TestAppendStatsdParts(t *testing.T) {
	if x := appendStatsdParts(sanitizeStatsd, "hello", "", "world"); x != "hello.world" {
		t.Fatalf("expect hello.world, got %s", x)
	}
}

func Test_sanitizeStatsd(t *testing.T) {
	type args struct {
		s string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "normal",
			args: args{
				s: "hello",
			},
			want: "hello",
		},
		{
			name: "empty",
			args: args{
				s: "",
			},
			want: "",
		},
		{
			name: "badchars",
			args: args{
				s: "aAzZ09_,",
			},
			want: "aAzZ09__",
		},
		{
			name: "long",
			args: args{
				s: strings.Repeat("a", 65),
			},
			want: strings.Repeat("a", 64),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeStatsd(tt.args.s); got != tt.want {
				t.Errorf("sanitizeStatsd() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConcurrencyCollector_delay(t *testing.T) {
	x := ConcurrencyCollector{}
	require.Equal(t, x.delay(), time.Second*10)
	x.Delay.Set(time.Second.Nanoseconds() * 3)
	require.Equal(t, x.delay(), time.Second*3)
}

func waitForGauge(name string, ss *rememberStats, clk *clock.MockClock) {
	hasGauge := false
	for !hasGauge {
		time.Sleep(time.Millisecond)
		ss.EachGauge(func(s string, vals []int64) {
			if s == name {
				hasGauge = true
			}
		})
	}
}

func waitForCounter(name string, ss *rememberStats, clk *clock.MockClock) {
	hasCounter := false
	for !hasCounter {
		time.Sleep(time.Millisecond)
		ss.EachCounter(func(s string, vals []int64) {
			if s == name {
				hasCounter = true
			}
		})
	}
}

func TestConcurrencyCollector_Start(t *testing.T) {
	clk := clock.MockClock{}
	now := time.Now()
	clk.Set(now)
	ss := rememberStats{}
	c := CommandFactory{
		StatSender: &ss,
	}
	tf := responsetimeslo.Factory{
		CollectorConstructors: []func(circuitName string) responsetimeslo.Collector{
			c.SLOCollector,
		},
	}
	m := &circuit.Manager{
		DefaultCircuitProperties: []circuit.CommandPropertiesConstructor{
			c.CommandProperties,
			tf.CommandProperties,
		},
	}
	exampleCircuit := m.MustCreateCircuit("example")
	require.NotNil(t, exampleCircuit)
	cc := c.ConcurrencyCollector(m)
	cc.timeAfter = clk.After
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		cc.Start()
	}()
	expectedGauges := []string{
		"example.circuit.is_open",
		"example.circuit.concurrent",
		"example.circuit.max_concurrent",
	}
	expectedCounters := []string{
		"example.run.success",
		"example.run.err_failure",
		"example.slo.passed",
		"example.slo.failed",
	}

	foundExpected := int64(0)
	for _, e := range expectedGauges {
		wg.Add(1)
		e := e
		go func() {
			defer wg.Done()
			defer atomic.AddInt64(&foundExpected, 1)
			waitForGauge(e, &ss, &clk)
		}()
	}
	for _, e := range expectedCounters {
		wg.Add(1)
		e := e
		go func() {
			defer wg.Done()
			defer atomic.AddInt64(&foundExpected, 1)
			waitForCounter(e, &ss, &clk)
		}()
	}

	// Do 1 of most types to make sure they trigger
	require.Error(t, exampleCircuit.Run(context.Background(), func(ctx context.Context) error {
		return errors.New("bad")
	}))
	require.NoError(t, exampleCircuit.Run(context.Background(), func(ctx context.Context) error {
		return nil
	}))

	// Keep ticking until we find the 3 metrics we expect
	clock.TickUntil(&clk, func() bool {
		return atomic.LoadInt64(&foundExpected) == int64(len(expectedCounters)+len(expectedGauges))
	}, time.Millisecond, time.Second)
	require.NoError(t, cc.Close())
	wg.Wait()
}
