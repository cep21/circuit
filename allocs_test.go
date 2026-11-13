package circuit_test

import (
	"context"
	"testing"
	"time"

	"github.com/cep21/circuit/v4"
	"github.com/stretchr/testify/require"
)

// The steady state must not feed the garbage collector.  The only allocations a request may cost are the ones of
// context.WithDeadline (when there is a Timeout) and, for Go, its goroutine plumbing.
func TestSteadyStateAllocations(t *testing.T) {
	ctx := context.Background()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	const withDeadline = 4
	interrupts := noTimeout
	interrupts.Execution.IsErrInterrupt = func(error) bool { return true }
	interrupts.General.CustomConfig = map[interface{}]interface{}{"k": "v"}
	execute := func(ctx context.Context, err error) func(*circuit.Circuit) {
		run := returns(err)
		return func(c *circuit.Circuit) { _ = c.Execute(ctx, run, fallbackPasses) }
	}
	for _, tc := range []struct {
		name    string
		cfg     circuit.Config
		prepare func(*circuit.Circuit)
		op      func(*circuit.Circuit)
		max     float64
	}{
		{name: "success", cfg: noTimeout, op: execute(ctx, nil)},
		{name: "success with timeout", op: execute(ctx, nil), max: withDeadline},
		{name: "plain failure", cfg: noTimeout, op: execute(ctx, errPlain)},
		{name: "wrapped failure", cfg: noTimeout, op: execute(ctx, errWrapped)},
		{name: "joined failure", cfg: noTimeout, op: execute(ctx, errJoined)},
		{name: "network failure", cfg: noTimeout, op: execute(ctx, errNet)},
		{name: "network failure with timeout", op: execute(ctx, errNet), max: withDeadline},
		{name: "bad request", cfg: noTimeout, op: execute(ctx, errWrappedBad)},
		{name: "interrupt", cfg: interrupts, op: execute(canceled, errPlain)},
		{name: "open", prepare: openCircuit, op: execute(ctx, nil)},
		{name: "throttled", op: execute(ctx, nil), prepare: func(c *circuit.Circuit) {
			cfg := c.Config()
			cfg.Execution = circuit.ExecutionConfig{Timeout: time.Hour}
			c.SetConfigThreadSafe(cfg)
		}},
		{name: "Go", cfg: noTimeout, op: func(c *circuit.Circuit) { _ = c.Go(ctx, runIgnoresCtx, nil) }, max: 3},
		{name: "Config", cfg: interrupts, op: func(c *circuit.Circuit) { _ = c.Config() }},
		{name: "SetConfigThreadSafe", cfg: interrupts, op: func(c *circuit.Circuit) { c.SetConfigThreadSafe(c.Config()) }},
	} {
		for _, stack := range benchStacks {
			t.Run(tc.name+"/"+stack.name, func(t *testing.T) {
				c := stack.make(tc.cfg)
				if tc.prepare != nil {
					tc.prepare(c)
				}
				require.LessOrEqual(t, testing.AllocsPerRun(200, func() { tc.op(c) }), tc.max)
			})
		}
	}
}
