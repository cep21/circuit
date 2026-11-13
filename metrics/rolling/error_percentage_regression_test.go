package rolling

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ErrorPercentageAt is documented as [0.0 - 1.0].  It read the error counters twice (once for the denominator, again
// for the numerator), so failures landing between the reads pushed it above 1.
func TestErrorPercentageNeverAboveOne(t *testing.T) {
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	var rs RunStats
	rs.SetConfigNotThreadSafe(RunStatsConfig{Now: func() time.Time { return now }})
	var stop atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			rs.ErrFailure(context.Background(), now, time.Millisecond)
		}
	}()
	defer wg.Wait()
	defer stop.Store(true)
	// Keep reading until plenty of failures have raced the reads
	for rs.ErrFailures.TotalSum() < 200_000 {
		if pct := rs.ErrorPercentageAt(now); pct > 1 {
			t.Fatalf("ErrorPercentageAt = %v, documented range is [0.0 - 1.0]", pct)
		}
	}
}
