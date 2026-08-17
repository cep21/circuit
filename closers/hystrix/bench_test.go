package hystrix

import (
	"context"
	"testing"
	"time"
)

func BenchmarkCloserSuccessWhileClosedParallel(b *testing.B) {
	c := CloserFactory(ConfigureCloser{})().(*Closer)
	ctx := context.Background()
	now := time.Now()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.Success(ctx, now, time.Millisecond)
		}
	})
}

func BenchmarkOpenerShouldOpen(b *testing.B) {
	now := time.Now()
	o := OpenerFactory(ConfigureOpener{RequestVolumeThreshold: 10, Now: func() time.Time { return now }})().(*Opener)
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		o.ErrFailure(ctx, now, time.Millisecond)
		o.Success(ctx, now, time.Millisecond)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = o.ShouldOpen(ctx, now)
	}
}
