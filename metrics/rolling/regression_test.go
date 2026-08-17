package rolling

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cep21/circuit/v4"
	"github.com/cep21/circuit/v4/internal/clock"
	"github.com/cep21/circuit/v4/internal/testhelp"
)

// ErrorPercentage() and Var() used wall-clock time even when a different clock was configured, which both gave the
// wrong answer and advanced the rolling windows so far ahead that every later event was dropped.
func TestRegression_ConfiguredClockUsedEverywhere(t *testing.T) {
	mc := &clock.MockClock{}
	mockNow := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	mc.Set(mockNow)
	s := StatFactory{RunConfig: RunStatsConfig{Now: mc.Now}}
	cfg := s.CreateConfig("x")
	cfg.General.TimeKeeper = circuit.TimeKeeper{Now: mc.Now, AfterFunc: mc.AfterFunc}
	c := circuit.NewCircuitFromConfig("x", cfg)
	ctx := context.Background()
	_ = c.Execute(ctx, testhelp.AlwaysFails, nil)
	rs := FindCommandMetrics(c)
	if got := rs.ErrorPercentage(); got != 1.0 {
		t.Fatalf("ErrorPercentage() with mocked clock returned %v, want 1.0", got)
	}
	_ = c.Execute(ctx, testhelp.AlwaysPasses, nil)
	_ = rs.Var().String()
	_ = c.Execute(ctx, testhelp.AlwaysPasses, nil)
	if got := rs.Successes.RollingSumAt(mockNow); got != 2 {
		t.Fatalf("after ErrorPercentage()/Var() the rolling window is corrupted: successes=%d want 2", got)
	}
	if got := rs.LegitimateAttemptsAt(mockNow); got != 3 {
		t.Fatalf("LegitimateAttemptsAt=%d want 3", got)
	}
	var asMap map[string]map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rs.Var().String()), &asMap); err != nil {
		t.Fatal(err)
	}
	if snap, ok := asMap["Latencies"]["snap"]; !ok || !strings.Contains(string(snap), `"p50"`) {
		t.Fatalf("expected Latencies.snap latency snapshot in Var (same shape as before): %s", rs.Var().String())
	}
	mc.Add(time.Hour)
	if strings.Contains(rs.Var().String(), `"RollingSum":2`) {
		t.Fatalf("Var should roll idle windows forward: %s", rs.Var().String())
	}
}

func TestRegression_SetConfigNotThreadSafePartialConfig(t *testing.T) {
	var rs RunStats
	rs.SetConfigNotThreadSafe(RunStatsConfig{Now: time.Now, RollingStatsDuration: 10 * time.Second})
	rs.Success(context.Background(), time.Now(), time.Millisecond)
	if rs.Successes.RollingSumAt(time.Now()) != 1 {
		t.Fatal("expected default buckets to be usable")
	}
	var fs FallbackStats
	fs.SetConfigNotThreadSafe(FallbackStatsConfig{RollingStatsDuration: 10 * time.Second, RollingStatsNumBuckets: 10})
	fs.Success(context.Background(), time.Now(), time.Millisecond)

	s := StatFactory{RunConfig: RunStatsConfig{RollingStatsNumBuckets: -1, RollingPercentileBucketSize: -1}}
	var m circuit.Manager
	m.DefaultCircuitProperties = append(m.DefaultCircuitProperties, s.CreateConfig)
	c := m.MustCreateCircuit("neg")
	if err := c.Execute(context.Background(), testhelp.AlwaysPasses, nil); err != nil {
		t.Fatal(err)
	}
}

// A failed duplicate CreateCircuit must not orphan the StatFactory entry for the live circuit.
func TestRegression_DuplicateCreateKeepsStats(t *testing.T) {
	s := StatFactory{}
	var m circuit.Manager
	m.DefaultCircuitProperties = append(m.DefaultCircuitProperties, s.CreateConfig)
	c := m.MustCreateCircuit("dup")
	_ = c.Execute(context.Background(), testhelp.AlwaysPasses, nil)
	if _, err := m.CreateCircuit("dup"); err == nil {
		t.Fatal("expected duplicate error")
	}
	if s.RunStats("dup") != FindCommandMetrics(c) {
		t.Fatal("StatFactory.RunStats(dup) points at an orphaned RunStats after a failed duplicate create")
	}
	if s.FallbackStats("dup") != FindFallbackMetrics(c) {
		t.Fatal("StatFactory.FallbackStats(dup) is orphaned")
	}
}
