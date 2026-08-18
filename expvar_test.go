package circuit_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/cep21/circuit/v4"
	"github.com/cep21/circuit/v4/metrics/rolling"
	"github.com/stretchr/testify/require"
)

// TestCircuit_VarWithRollingStats drives a circuit that has rolling run and fallback stats through each fallback
// outcome and checks the expvar output includes those collectors with the right counts.
func TestCircuit_VarWithRollingStats(t *testing.T) {
	ctx := context.Background()
	f := rolling.StatFactory{}
	m := circuit.Manager{DefaultCircuitProperties: []circuit.CommandPropertiesConstructor{f.CreateConfig}}
	c := m.MustCreateCircuit("var-rolling", circuit.Config{
		Fallback: circuit.FallbackConfig{MaxConcurrentRequests: 1},
	})
	fails := func(context.Context) error { return errors.New("boom") }
	recovers := func(context.Context, error) error { return nil }

	require.NoError(t, c.Execute(ctx, func(context.Context) error { return nil }, nil))
	require.NoError(t, c.Execute(ctx, fails, recovers))
	require.Error(t, c.Execute(ctx, fails, func(_ context.Context, err error) error { return err }))

	// With one fallback parked, a second concurrent fallback is rejected
	inFallback := make(chan struct{})
	release := make(chan struct{})
	parked := make(chan error, 1)
	go func() {
		parked <- c.Execute(ctx, fails, func(context.Context, error) error {
			close(inFallback)
			<-release
			return nil
		})
	}()
	<-inFallback
	require.Error(t, c.Execute(ctx, fails, recovers))
	close(release)
	require.NoError(t, <-parked)

	var out struct {
		Name            string                       `json:"name"`
		IsOpen          bool                         `json:"is_open"`
		RunMetrics      []map[string]json.RawMessage `json:"run_metrics"`
		FallbackMetrics []map[string]int64           `json:"fallback_metrics"`
	}
	require.NoError(t, json.Unmarshal([]byte(c.Var().String()), &out), c.Var().String())
	require.Equal(t, "var-rolling", out.Name)
	require.False(t, out.IsOpen)
	require.Len(t, out.RunMetrics, 1)
	for _, k := range []string{"Successes", "ErrFailures", "Latencies"} {
		require.Contains(t, out.RunMetrics[0], k)
	}
	require.Equal(t, []map[string]int64{{
		"Successes":                  2,
		"ErrFailures":                1,
		"ErrConcurrencyLimitRejects": 1,
	}}, out.FallbackMetrics)

	// The manager's Var nests the same document under the circuit name
	var all map[string]struct {
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal([]byte(m.Var().String()), &all))
	require.Equal(t, "var-rolling", all["var-rolling"].Name)
}
