package faststats

import (
	"testing"
	"time"
)

func BenchmarkRollingPercentileSnapshot(b *testing.B) {
	now := time.Now()
	x := NewRollingPercentile(10*time.Second, 6, 100, now)
	for i := 0; i < 6; i++ {
		for j := 0; j < 100; j++ {
			x.AddDuration(time.Duration((j*7919)%1000)*time.Microsecond, now.Add(time.Duration(i)*10*time.Second))
		}
	}
	at := now.Add(50 * time.Second)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = x.SnapshotAt(at)
	}
}

func BenchmarkRollingCounterIncParallel(b *testing.B) {
	start := time.Now()
	x := NewRollingCounter(time.Second, 10, start)
	now := start.Add(time.Second)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			x.Inc(now)
		}
	})
}

func BenchmarkRollingCounterMixedParallel(b *testing.B) {
	start := time.Now()
	x := NewRollingCounter(time.Second, 10, start)
	now := start.Add(time.Second)
	var ctr AtomicInt64
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if ctr.Add(1)%10 == 0 {
				_ = x.RollingSumAt(now)
			} else {
				x.Inc(now)
			}
		}
	})
}

func BenchmarkRollingBucketsAdvance(b *testing.B) {
	start := time.Now()
	b.Run("steady", func(b *testing.B) {
		rb := RollingBuckets{NumBuckets: 10, BucketWidth: 100 * time.Millisecond, StartTime: start}
		now := start.Add(time.Second)
		for i := 0; i < b.N; i++ {
			rb.Advance(now, func(int) {})
		}
	})
	b.Run("realtime-1s-buckets/parallel", func(b *testing.B) {
		rb := RollingBuckets{NumBuckets: 10, BucketWidth: time.Second, StartTime: start}
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				rb.Advance(time.Now(), func(int) {})
			}
		})
	})
}
