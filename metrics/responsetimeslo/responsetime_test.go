package responsetimeslo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cep21/circuit/v4"
)

func checkSLO(t *testing.T, r *Tracker, expectFail int64, expectPass int64) {
	if r.FailsSLOCount.Get() != expectFail {
		t.Error("Unexpected failing count", r.FailsSLOCount.Get(), expectFail)
	}
	if r.MeetsSLOCount.Get() != expectPass {
		t.Error("Unexpected meets count", r.MeetsSLOCount.Get(), expectPass)
	}
}

func TestTracker(t *testing.T) {
	r := &Tracker{}
	ctx := context.Background()
	r.MaximumHealthyTime.Set(time.Second.Nanoseconds())
	r.ErrInterrupt(ctx, time.Now(), time.Second)
	checkSLO(t, r, 0, 0)
	r.ErrInterrupt(ctx, time.Now(), time.Second*2)
	checkSLO(t, r, 1, 0)
	r.ErrBadRequest(ctx, time.Now(), time.Second*2)
	checkSLO(t, r, 1, 0)
	r.ErrConcurrencyLimitReject(ctx, time.Now())
	checkSLO(t, r, 2, 0)
	r.ErrFailure(ctx, time.Now(), time.Nanosecond)
	checkSLO(t, r, 3, 0)
	r.ErrShortCircuit(ctx, time.Now())
	checkSLO(t, r, 4, 0)
	r.ErrTimeout(ctx, time.Now(), time.Second)
	checkSLO(t, r, 5, 0)
	r.Success(ctx, time.Now(), time.Second)
	checkSLO(t, r, 5, 1)
	r.Success(ctx, time.Now(), time.Second*2)
	checkSLO(t, r, 6, 1)

	if r.Var().String() == "" {
		t.Error("Expect something out of Var")
	}

}

type countingCollector struct {
	passed, failed int
}

func (c *countingCollector) Failed() { c.failed++ }
func (c *countingCollector) Passed() { c.passed++ }

func TestFactory(t *testing.T) {
	collectors := map[string]*countingCollector{}
	f := Factory{
		Config: Config{MaximumHealthyTime: time.Hour},
		ConfigConstructor: []func(string) Config{
			func(name string) Config {
				if name == "strict" {
					return Config{MaximumHealthyTime: time.Nanosecond}
				}
				return Config{}
			},
		},
		CollectorConstructors: []func(string) Collector{
			func(name string) Collector {
				c := &countingCollector{}
				collectors[name] = c
				return c
			},
		},
	}
	m := circuit.Manager{DefaultCircuitProperties: []circuit.CommandPropertiesConstructor{f.CommandProperties}}
	relaxed := m.MustCreateCircuit("relaxed")
	strict := m.MustCreateCircuit("strict")
	ctx := context.Background()
	slow := func(context.Context) error {
		time.Sleep(time.Millisecond)
		return nil
	}
	if err := relaxed.Execute(ctx, slow, nil); err != nil {
		t.Fatal(err)
	}
	if err := strict.Execute(ctx, slow, nil); err != nil {
		t.Fatal(err)
	}
	if err := relaxed.Execute(ctx, func(context.Context) error { return errors.New("boom") }, nil); err == nil {
		t.Fatal("expected an error")
	}
	if collectors["relaxed"].passed != 1 || collectors["relaxed"].failed != 1 {
		t.Errorf("relaxed collector: %+v", collectors["relaxed"])
	}
	if collectors["strict"].passed != 0 || collectors["strict"].failed != 1 {
		t.Errorf("strict collector: %+v", collectors["strict"])
	}
	// The per-circuit constructor beats Factory.Config, which beats the package default
	var strictTracker, relaxedTracker *Tracker
	for _, rm := range strict.CmdMetricCollector {
		if tr, ok := rm.(*Tracker); ok {
			strictTracker = tr
		}
	}
	for _, rm := range relaxed.CmdMetricCollector {
		if tr, ok := rm.(*Tracker); ok {
			relaxedTracker = tr
		}
	}
	if strictTracker == nil || relaxedTracker == nil {
		t.Fatal("expected a Tracker on each circuit")
	}
	if strictTracker.Config().MaximumHealthyTime != time.Nanosecond {
		t.Errorf("strict config: %v", strictTracker.Config())
	}
	if relaxedTracker.Config().MaximumHealthyTime != time.Hour {
		t.Errorf("relaxed config: %v", relaxedTracker.Config())
	}
}
