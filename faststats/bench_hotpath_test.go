package faststats

import (
	"encoding/json"
	"math"
	"math/rand"
	"testing"
	"time"
)

// benchUptime is how long ago the benchmarked structures were "created": production counters live for days, which
// makes the absolute bucket index (and the operands of any division) realistically large.
const benchUptime = 72 * time.Hour

var (
	benchSinkInt   int
	benchSinkInt64 int64
	benchSinkDurs  SortedDurations
	benchSinkBytes []byte
	benchSinkBool  bool
)

// benchClock returns a start time benchUptime in the past and a current time.  Both carry a monotonic reading.
func benchClock() (start time.Time, now time.Time) {
	now = time.Now()
	return now.Add(-benchUptime), now
}

// benchLatencies returns n log-normally distributed latencies (median ~5ms, ns resolution)
func benchLatencies(seed int64, n int) []time.Duration {
	r := rand.New(rand.NewSource(seed))
	ret := make([]time.Duration, n)
	for i := range ret {
		ret[i] = time.Duration(5e6 * math.Exp(r.NormFloat64()))
	}
	return ret
}

func BenchmarkHotAdvance(b *testing.B) {
	noop := func(int) {}
	b.Run("current", func(b *testing.B) {
		start, now := benchClock()
		rb := RollingBuckets{NumBuckets: 10, BucketWidth: time.Second, StartTime: start}
		rb.Advance(now, noop)
		for i := 0; i < b.N; i++ {
			benchSinkInt = rb.Advance(now, noop)
		}
	})
	b.Run("behind", func(b *testing.B) {
		start, now := benchClock()
		rb := RollingBuckets{NumBuckets: 10, BucketWidth: time.Second, StartTime: start}
		rb.Advance(now, noop)
		now = now.Add(-2 * time.Second)
		for i := 0; i < b.N; i++ {
			benchSinkInt = rb.Advance(now, noop)
		}
	})
	b.Run("wallclock", func(b *testing.B) {
		start, now := benchClock()
		start, now = start.Round(0), now.Round(0)
		rb := RollingBuckets{NumBuckets: 10, BucketWidth: time.Second, StartTime: start}
		rb.Advance(now, noop)
		for i := 0; i < b.N; i++ {
			benchSinkInt = rb.Advance(now, noop)
		}
	})
	b.Run("roll-every-call", func(b *testing.B) {
		start, now := benchClock()
		rb := RollingBuckets{NumBuckets: 10, BucketWidth: time.Second, StartTime: start}
		for i := 0; i < b.N; i++ {
			now = now.Add(time.Second)
			benchSinkInt = rb.Advance(now, noop)
		}
	})
}

func BenchmarkHotCounter(b *testing.B) {
	b.Run("Inc", func(b *testing.B) {
		start, now := benchClock()
		x := NewRollingCounter(time.Second, 10, start)
		for i := 0; i < b.N; i++ {
			x.Inc(now)
		}
	})
	b.Run("Inc/parallel", func(b *testing.B) {
		start, now := benchClock()
		x := NewRollingCounter(time.Second, 10, start)
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				x.Inc(now)
			}
		})
	})
	b.Run("Inc-timeNow/parallel", func(b *testing.B) {
		start, _ := benchClock()
		x := NewRollingCounter(time.Second, 10, start)
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				x.Inc(time.Now())
			}
		})
	})
	b.Run("Inc-idle-gap", func(b *testing.B) {
		// A rarely used circuit: every Inc has to roll the whole window
		start, now := benchClock()
		x := NewRollingCounter(time.Second, 10, start)
		for i := 0; i < b.N; i++ {
			now = now.Add(time.Minute)
			x.Inc(now)
		}
	})
	b.Run("Inc-next-bucket", func(b *testing.B) {
		// ~1 request per second: every Inc rolls one bucket
		start, now := benchClock()
		x := NewRollingCounter(time.Second, 10, start)
		for i := 0; i < b.N; i++ {
			now = now.Add(time.Second)
			x.Inc(now)
		}
	})
	b.Run("RollingSumAt", func(b *testing.B) {
		start, now := benchClock()
		x := NewRollingCounter(time.Second, 10, start)
		x.Inc(now)
		for i := 0; i < b.N; i++ {
			benchSinkInt64 = x.RollingSumAt(now)
		}
	})
	b.Run("TotalSum", func(b *testing.B) {
		start, now := benchClock()
		x := NewRollingCounter(time.Second, 10, start)
		x.Inc(now)
		for i := 0; i < b.N; i++ {
			benchSinkInt64 = x.TotalSum()
		}
	})
	b.Run("TotalSum/parallel", func(b *testing.B) {
		start, now := benchClock()
		x := NewRollingCounter(time.Second, 10, start)
		x.Inc(now)
		b.RunParallel(func(pb *testing.PB) {
			var sum int64
			for pb.Next() {
				sum += x.TotalSum()
			}
			if sum < 0 {
				b.Fatal("negative")
			}
		})
	})
	b.Run("MarshalJSON", func(b *testing.B) {
		start, now := benchClock()
		x := NewRollingCounter(time.Second, 10, start)
		for i := 0; i < 12345; i++ {
			x.Inc(now.Add(time.Duration(i) * time.Millisecond))
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			benchSinkBytes, _ = json.Marshal(&x)
		}
	})
}

func BenchmarkHotPercentile(b *testing.B) {
	b.Run("AddDuration", func(b *testing.B) {
		start, now := benchClock()
		x := NewRollingPercentile(10*time.Second, 6, 100, start)
		for i := 0; i < b.N; i++ {
			x.AddDuration(time.Millisecond, now)
		}
	})
	b.Run("AddDuration/parallel", func(b *testing.B) {
		start, now := benchClock()
		x := NewRollingPercentile(10*time.Second, 6, 100, start)
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				x.AddDuration(time.Millisecond, now)
			}
		})
	})
	for _, tc := range []struct {
		name      string
		perBucket int
	}{{"n=600", 100}, {"n=120", 20}, {"n=30", 5}, {"n=6", 1}} {
		b.Run("Snapshot/"+tc.name, func(b *testing.B) {
			// Many distinct data sets so the branch predictor cannot memorise one sort
			const sets = 256
			start, now := benchClock()
			all := make([]RollingPercentile, sets)
			for s := range all {
				all[s] = NewRollingPercentile(10*time.Second, 6, 100, start)
				lat := benchLatencies(int64(s), 6*tc.perBucket)
				for i, d := range lat {
					all[s].AddDuration(d, now.Add(time.Duration(i/tc.perBucket)*10*time.Second))
				}
			}
			at := now.Add(50 * time.Second)
			if got := len(all[0].SnapshotAt(at)); got != 6*tc.perBucket {
				b.Fatalf("unexpected snapshot size %d", got)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchSinkDurs = all[i%sets].SnapshotAt(at)
			}
		})
	}
}

func BenchmarkHotTimedCheck(b *testing.B) {
	b.Run("sleeping/parallel", func(b *testing.B) {
		var x TimedCheck
		now := time.Now()
		x.SetSleepDuration(time.Hour)
		x.SetEventCountToAllow(1)
		x.SleepStart(now)
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				if x.Check(now) {
					b.Fatal("should be sleeping")
				}
			}
		})
	})
	b.Run("SleepStart", func(b *testing.B) {
		var x TimedCheck
		now := time.Now()
		x.SetSleepDuration(time.Hour)
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			x.SleepStart(now)
		}
		benchSinkBool = x.Check(now)
	})
}
