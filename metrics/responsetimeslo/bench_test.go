package responsetimeslo

import (
	"context"
	"testing"
	"time"
)

func BenchmarkTrackerSuccess(b *testing.B) {
	ctx := context.Background()
	now := time.Now()
	newTracker := func() *Tracker {
		return (&Factory{}).CommandProperties("b").Metrics.Run[0].(*Tracker)
	}
	b.Run("serial", func(b *testing.B) {
		tr := newTracker()
		for i := 0; i < b.N; i++ {
			tr.Success(ctx, now, time.Millisecond)
		}
	})
	b.Run("parallel", func(b *testing.B) {
		tr := newTracker()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				tr.Success(ctx, now, time.Millisecond)
			}
		})
	})
}
