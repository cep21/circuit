package rolling

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cep21/circuit/v4"
	"github.com/cep21/circuit/v4/internal/testhelp"
)

func TestHappyCircuit(t *testing.T) {
	s := StatFactory{}
	c := circuit.NewCircuitFromConfig("TestHappyCircuit", s.CreateConfig(""))
	err := c.Execute(context.Background(), testhelp.AlwaysPasses, nil)
	if err != nil {
		t.Error("saw error from circuit that always passes")
	}
	cmdMetrics := FindCommandMetrics(c)
	errCount := cmdMetrics.ErrorsAt(time.Now())
	if errCount != 0 {
		t.Error("Happy circuit shouldn't make errors")
	}
	if cmdMetrics.Successes.TotalSum() != 1 {
		t.Error("Should see a success total")
	}
	if cmdMetrics.Successes.RollingSumAt(time.Now()) != 1 {
		t.Error("Should see a success rolling")
	}
	requestCount := cmdMetrics.LegitimateAttemptsAt(time.Now())
	if requestCount != 1 {
		t.Error("happy circuit should still count as a request")
	}
}

func TestBadRequest(t *testing.T) {
	s := StatFactory{}
	c := circuit.NewCircuitFromConfig("TestBadRequest", s.CreateConfig(""))
	err := c.Execute(context.Background(), func(_ context.Context) error {
		return circuit.SimpleBadRequest{
			Err: errors.New("this request is bad"),
		}
	}, nil)
	if err == nil {
		t.Error("I really expected an error here!")
	}
	cmdMetrics := FindCommandMetrics(c)
	errCount := cmdMetrics.ErrorsAt(time.Now())
	if errCount != 0 {
		t.Error("bad requests shouldn't be errors!")
	}
	requestCount := cmdMetrics.LegitimateAttemptsAt(time.Now())
	if requestCount != 0 {
		t.Error("bad requests should not count as legit requests!")
	}
	requestCount = cmdMetrics.ErrBadRequests.RollingSumAt(time.Now())
	if requestCount != 1 {
		t.Error("bad requests should count as backed out requests!")
	}
}

func TestFallbackCircuit(t *testing.T) {
	s := StatFactory{}
	c := circuit.NewCircuitFromConfig("TestFallbackCircuit", s.CreateConfig(""))
	err := c.Execute(context.Background(), testhelp.AlwaysFails, testhelp.AlwaysPassesFallback)
	if err != nil {
		t.Error("saw error from circuit that has happy fallback", err)
	}
	cmdMetrics := FindCommandMetrics(c)
	fallbackMetrics := FindFallbackMetrics(c)
	if cmdMetrics.ErrorsAt(time.Now()) != 1 {
		t.Error("Even if fallback happens, and works ok, we should still count an error in the circuit")
	}
	if cmdMetrics.ErrFailures.RollingSumAt(time.Now()) != 1 {
		t.Error("Even if fallback happens, and works ok, we should still increment an error in stats")
	}
	if fallbackMetrics.ErrFailures.TotalSum() != 0 {
		t.Error("expected no fallback error")
	}
	if fallbackMetrics.Successes.TotalSum() != 1 {
		t.Error("expected fallback success")
	}
	if fallbackMetrics.Successes.RollingSumAt(time.Now()) != 1 {
		t.Error("expected fallback success")
	}
}

func TestCircuitIgnoreContextFailures(t *testing.T) {
	s := StatFactory{}
	h := circuit.Manager{
		DefaultCircuitProperties: []circuit.CommandPropertiesConstructor{s.CreateConfig},
	}
	c := h.MustCreateCircuit("TestFailingCircuit", circuit.Config{
		Execution: circuit.ExecutionConfig{
			Timeout: time.Hour,
		},
	})
	rootCtx, cancel := context.WithTimeout(context.Background(), time.Millisecond*3)
	defer cancel()
	err := c.Execute(rootCtx, testhelp.SleepsForX(time.Second), nil)
	if err == nil {
		t.Error("saw no error from circuit that should end in an error")
	}
	cmdMetrics := FindCommandMetrics(c)
	if cmdMetrics.ErrorsAt(time.Now()) != 0 {
		t.Error("if the root context dies, it shouldn't be an error")
	}
	if cmdMetrics.ErrInterrupts.TotalSum() != 1 {
		t.Error("Total sum should count the interrupt")
	}
	if cmdMetrics.ErrInterrupts.RollingSumAt(time.Now()) != 1 {
		t.Error("rolling sum should count the interrupt")
	}
}

func TestStatFactory_RunStats(t *testing.T) {
	s := StatFactory{}
	if s.RunStats("hello") != nil {
		t.Error("expected nil stats")
	}
	s.CreateConfig("hello")
	if s.RunStats("hello") == nil {
		t.Error("expected non nil stats")
	}
}

func TestStatFactory_FallbackStats(t *testing.T) {
	s := StatFactory{}
	if s.FallbackStats("hello") != nil {
		t.Error("expected nil stats")
	}
	s.CreateConfig("hello")
	if s.FallbackStats("hello") == nil {
		t.Error("expected non nil stats")
	}
}

func TestFindCommandMetrics(t *testing.T) {
	var c circuit.Circuit
	if stats := FindCommandMetrics(&c); stats != nil {
		t.Error("expect no stats on empty circuit")
	}
}

func TestFindFallbackMetrics(t *testing.T) {
	var c circuit.Circuit
	if stats := FindFallbackMetrics(&c); stats != nil {
		t.Error("expect no stats on empty circuit")
	}
}

func TestRunStats_Var(t *testing.T) {
	r := RunStats{}
	varOut := r.Var().String()
	if !strings.Contains(varOut, "ErrFailures") {
		t.Fatal("expect to see failures in var stats")
	}
}

func TestRunStats_Config(t *testing.T) {
	var r RunStats
	c := RunStatsConfig{
		RollingStatsNumBuckets: 10,
	}
	c.Merge(defaultRunStatsConfig)
	r.SetConfigNotThreadSafe(c)
	if r.Config().RollingStatsNumBuckets != 10 {
		t.Fatal("expect 10 rolling stats buckets")
	}
}

func TestRunStats_ErrConcurrencyLimitReject(t *testing.T) {
	var r RunStats
	r.SetConfigNotThreadSafe(defaultRunStatsConfig)
	now := time.Now()
	r.ErrConcurrencyLimitReject(now)
	if r.ErrConcurrencyLimitRejects.TotalSum() != 1 {
		t.Errorf("expect a limit reject")
	}
}

func TestRunStats_ErrShortCircuit(t *testing.T) {
	var r RunStats
	r.SetConfigNotThreadSafe(defaultRunStatsConfig)
	now := time.Now()
	r.ErrShortCircuit(now)
	if r.ErrShortCircuits.TotalSum() != 1 {
		t.Errorf("expect a short circuit")
	}
}

func TestRunStats_ErrTimeout(t *testing.T) {
	var r RunStats
	r.SetConfigNotThreadSafe(defaultRunStatsConfig)
	now := time.Now()
	r.ErrTimeout(now, time.Second)
	if r.ErrTimeouts.TotalSum() != 1 {
		t.Errorf("expect a error timeout")
	}
	if r.Latencies.Snapshot().Max() != time.Second {
		t.Errorf("expect 1 sec latency")
	}
}

func TestRunStats_ErrorPercentage(t *testing.T) {
	var r RunStats
	if r.ErrorPercentage() != 0.0 {
		t.Errorf("Expect no errors")
	}
	r.SetConfigNotThreadSafe(defaultRunStatsConfig)
	now := time.Now()
	r.ErrTimeout(now, time.Second)
	if r.ErrorPercentage() != 1.0 {
		t.Errorf("Expect all errors")
	}
}
