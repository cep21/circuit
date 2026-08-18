package circuit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNeverOpen(t *testing.T) {
	ctx := context.Background()
	c := neverOpensFactory()
	require.False(t, c.ShouldOpen(ctx, time.Now()))
	require.False(t, c.Prevent(ctx, time.Now()))
}

func TestNeverClose(t *testing.T) {
	ctx := context.Background()
	c := neverClosesFactory()
	require.False(t, c.Allow(ctx, time.Now()))
	require.False(t, c.ShouldClose(ctx, time.Now()))
}

// TestNeverOpensNeverClosesIgnoreEvents runs every RunMetrics/Metrics event through the default no-op logic and
// checks none of them change its answer.
func TestNeverOpensNeverClosesIgnoreEvents(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	opener := neverOpensFactory()
	closer := neverClosesFactory()
	for _, m := range []interface {
		RunMetrics
		Metrics
	}{opener, closer} {
		m.Success(ctx, now, time.Millisecond)
		m.ErrFailure(ctx, now, time.Millisecond)
		m.ErrTimeout(ctx, now, time.Millisecond)
		m.ErrBadRequest(ctx, now, time.Millisecond)
		m.ErrInterrupt(ctx, now, time.Millisecond)
		m.ErrConcurrencyLimitReject(ctx, now)
		m.ErrShortCircuit(ctx, now)
		m.Opened(ctx, now)
		m.Closed(ctx, now)
	}
	require.False(t, opener.ShouldOpen(ctx, now))
	require.False(t, opener.Prevent(ctx, now))
	require.False(t, closer.ShouldClose(ctx, now))
	require.False(t, closer.Allow(ctx, now))
}

type configurableOpener struct {
	neverOpens
	prevent               bool
	threadSafe, notThread []Config
}

func (c *configurableOpener) Prevent(context.Context, time.Time) bool { return c.prevent }
func (c *configurableOpener) SetConfigThreadSafe(props Config) {
	c.threadSafe = append(c.threadSafe, props)
}
func (c *configurableOpener) SetConfigNotThreadSafe(props Config) {
	c.notThread = append(c.notThread, props)
}

type configurableCloser struct {
	neverCloses
	threadSafe, notThread []Config
}

func (c *configurableCloser) SetConfigThreadSafe(props Config) {
	c.threadSafe = append(c.threadSafe, props)
}
func (c *configurableCloser) SetConfigNotThreadSafe(props Config) {
	c.notThread = append(c.notThread, props)
}

// Open/close logic that implements Configurable is handed the circuit's config on construction and on live updates.
func TestConfigurableLogicReceivesConfig(t *testing.T) {
	opener := &configurableOpener{}
	closer := &configurableCloser{}
	c := NewCircuitFromConfig("configurable", Config{
		General: GeneralConfig{
			ClosedToOpenFactory: func() ClosedToOpen { return opener },
			OpenToClosedFactory: func() OpenToClosed { return closer },
		},
		Execution: ExecutionConfig{MaxConcurrentRequests: 3},
	})
	require.Len(t, opener.notThread, 1)
	require.Len(t, closer.notThread, 1)
	require.Equal(t, int64(3), opener.notThread[0].Execution.MaxConcurrentRequests)
	liveUpdates := len(opener.threadSafe)

	cfg := c.Config()
	cfg.Execution.MaxConcurrentRequests = 9
	c.SetConfigThreadSafe(cfg)
	require.Len(t, opener.notThread, 1)
	require.Len(t, opener.threadSafe, liveUpdates+1)
	require.Len(t, closer.threadSafe, liveUpdates+1)
	require.Equal(t, int64(9), opener.threadSafe[liveUpdates].Execution.MaxConcurrentRequests)
	require.Equal(t, int64(9), closer.threadSafe[liveUpdates].Execution.MaxConcurrentRequests)
}

// ClosedToOpen.Prevent short-circuits a request on a closed circuit without opening it.
func TestPreventShortCircuits(t *testing.T) {
	opener := &configurableOpener{prevent: true}
	c := NewCircuitFromConfig("prevent", Config{
		General: GeneralConfig{ClosedToOpenFactory: func() ClosedToOpen { return opener }},
	})
	ran := false
	err := c.Execute(context.Background(), func(context.Context) error {
		ran = true
		return nil
	}, nil)
	require.Error(t, err)
	require.False(t, ran)
	require.False(t, c.IsOpen())
	var ce Error
	require.ErrorAs(t, err, &ce)
	require.True(t, ce.CircuitOpen())

	opener.prevent = false
	require.NoError(t, c.Execute(context.Background(), func(context.Context) error { return nil }, nil))
}
