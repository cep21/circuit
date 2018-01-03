package rolling

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cep21/hystrix"
	"github.com/cep21/hystrix/internal/testhelp"
)

func TestHappyCircuit(t *testing.T) {
	c := hystrix.NewCircuitFromConfig("TestHappyCircuit", CollectRollingStats(RunStatsConfig{}, FallbackStatsConfig{})(""))
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
	c := hystrix.NewCircuitFromConfig("TestBadRequest", CollectRollingStats(RunStatsConfig{}, FallbackStatsConfig{})(""))
	err := c.Execute(context.Background(), func(_ context.Context) error {
		return hystrix.SimpleBadRequest{
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
	c := hystrix.NewCircuitFromConfig("TestFallbackCircuit", CollectRollingStats(RunStatsConfig{}, FallbackStatsConfig{})(""))
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
	h := hystrix.Manager{
		DefaultCircuitProperties: []hystrix.CommandPropertiesConstructor{CollectRollingStats(RunStatsConfig{}, FallbackStatsConfig{})},
	}
	c := h.MustCreateCircuit("TestFailingCircuit", hystrix.CircuitConfig{
		Execution: hystrix.ExecutionConfig{
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
