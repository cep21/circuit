//go:build go1.25

package circuit

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTimeoutAtExactDeadlineSynctest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rm := &countingRunMetrics{}
		c := NewCircuitFromConfig("boundary", Config{Metrics: MetricsCollectors{Run: []RunMetrics{rm}}})
		err := c.Run(context.Background(), func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		})
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Equal(t, int64(1), rm.errTimeout.Get(), "ErrTimeout (ErrFailure=%d)", rm.errFailure.Get())
	})
}

// A timeout beats an interrupt, as it does on the real clock, when the caller's deadline is the very same instant.
func TestTimeoutTiedWithParentDeadlineSynctest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const runs = 50
		rm := &countingRunMetrics{}
		c := NewCircuitFromConfig("boundary", Config{
			Execution: ExecutionConfig{Timeout: time.Second},
			Metrics:   MetricsCollectors{Run: []RunMetrics{rm}},
		})
		for range runs {
			parent, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = c.Run(parent, func(context.Context) error {
				<-parent.Done()
				return parent.Err()
			})
			cancel()
		}
		require.Equal(t, int64(runs), rm.errTimeout.Get())
	})
}
