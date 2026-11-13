package circuit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// SetConfigThreadSafe is commonly called with a config that only names the live setting being changed.  The
// parts it cannot change stay in effect, so Config() must keep reporting them.
func TestSetConfigThreadSafePartialKeepsStaticConfig(t *testing.T) {
	fixed := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	rm := &countingRunMetrics{}
	fm := &countingFallbackMetrics{}
	tc := &transitionCounter{}
	c := NewCircuitFromConfig("partial", Config{
		General: GeneralConfig{
			TimeKeeper:   TimeKeeper{Now: func() time.Time { return fixed }},
			GoLostErrors: func(error, interface{}) {},
		},
		Metrics: MetricsCollectors{Run: []RunMetrics{rm}, Fallback: []FallbackMetrics{fm}, Circuit: []Metrics{tc}},
	})
	c.SetConfigThreadSafe(Config{Execution: ExecutionConfig{MaxConcurrentRequests: 12}})

	got := c.Config()
	require.Equal(t, int64(12), got.Execution.MaxConcurrentRequests)
	require.NotNil(t, got.General.TimeKeeper.Now, "Config() lost the clock the circuit is still using")
	require.Equal(t, fixed, got.General.TimeKeeper.Now())
	require.NotNil(t, got.General.TimeKeeper.AfterFunc)
	require.NotNil(t, got.General.ClosedToOpenFactory)
	require.NotNil(t, got.General.OpenToClosedFactory)
	require.NotNil(t, got.General.GoLostErrors)
	require.Equal(t, []RunMetrics{rm}, got.Metrics.Run)
	require.Equal(t, []FallbackMetrics{fm}, got.Metrics.Fallback)
	require.Equal(t, []Metrics{tc}, got.Metrics.Circuit)
}

// Config() is the natural input for SetConfigNotThreadSafe, which does not merge defaults.
func TestSetConfigThreadSafePartialThenRebuild(t *testing.T) {
	rm := &countingRunMetrics{}
	c := NewCircuitFromConfig("partial", Config{Metrics: MetricsCollectors{Run: []RunMetrics{rm}}})
	c.SetConfigThreadSafe(Config{Execution: ExecutionConfig{MaxConcurrentRequests: 12}})
	require.NotPanics(t, func() { c.SetConfigNotThreadSafe(c.Config()) })
	require.NoError(t, c.Run(context.Background(), func(context.Context) error { return nil }))
	require.Equal(t, int64(1), rm.success.Get())
}

// Live settings are still replaced wholesale, and SetConfigNotThreadSafe still replaces everything.
func TestSetConfigPartialStillReplacesWhatItApplies(t *testing.T) {
	c := NewCircuitFromConfig("partial", Config{
		General: GeneralConfig{
			ForceOpen:    true,
			GoLostErrors: func(error, interface{}) {},
			CustomConfig: map[interface{}]interface{}{"a": 1},
		},
		Execution: ExecutionConfig{IsErrInterrupt: func(error) bool { return false }},
		Metrics:   MetricsCollectors{Run: []RunMetrics{&countingRunMetrics{}}},
	})
	c.SetConfigThreadSafe(Config{})
	got := c.Config()
	require.False(t, got.General.ForceOpen)
	require.Nil(t, got.General.CustomConfig)
	require.Nil(t, got.Execution.IsErrInterrupt)
	require.Zero(t, got.Execution.Timeout)
	require.Zero(t, got.Execution.MaxConcurrentRequests)

	c.SetConfigNotThreadSafe(defaultCommandProperties)
	got = c.Config()
	require.Nil(t, got.General.GoLostErrors)
	require.Empty(t, got.Metrics.Run)
}

// Two configs derived from one base value must not write into each other (or the base) when merged.
func TestMergeDoesNotAliasCopiedConfig(t *testing.T) {
	// Three Merge calls are enough to leave spare capacity (append grows 1, 2, 4)
	var base Config
	for i := 0; i < 3; i++ {
		base.Merge(Config{Metrics: MetricsCollectors{
			Run:      []RunMetrics{&countingRunMetrics{}},
			Fallback: []FallbackMetrics{&countingFallbackMetrics{}},
			Circuit:  []Metrics{&transitionCounter{}},
		}})
	}
	base.General.CustomConfig = map[interface{}]interface{}{"base": true}

	derive := func(owner string) (Config, *countingRunMetrics, *countingFallbackMetrics, *transitionCounter) {
		rm, fm, tc := &countingRunMetrics{}, &countingFallbackMetrics{}, &transitionCounter{}
		cfg := base
		cfg.Merge(Config{
			General: GeneralConfig{CustomConfig: map[interface{}]interface{}{"owner": owner}},
			Metrics: MetricsCollectors{Run: []RunMetrics{rm}, Fallback: []FallbackMetrics{fm}, Circuit: []Metrics{tc}},
		})
		return cfg, rm, fm, tc
	}
	cfgA, runA, fallbackA, circuitA := derive("a")
	cfgB, runB, _, _ := derive("b")

	t.Run("metrics", func(t *testing.T) {
		require.NoError(t, NewCircuitFromConfig("a", cfgA).Run(context.Background(), func(context.Context) error { return nil }))
		require.Equal(t, int64(0), runB.success.Get(), "circuit a reported to b's collector")
		require.Equal(t, int64(1), runA.success.Get())
		require.Same(t, fallbackA, cfgA.Metrics.Fallback[3])
		require.Same(t, circuitA, cfgA.Metrics.Circuit[3])
	})
	t.Run("custom config", func(t *testing.T) {
		require.Equal(t, "b", cfgB.General.CustomConfig["owner"], "b inherited a's value through the shared base map")
		require.Equal(t, "a", cfgA.General.CustomConfig["owner"])
		require.Equal(t, map[interface{}]interface{}{"base": true}, base.General.CustomConfig)
	})
}
