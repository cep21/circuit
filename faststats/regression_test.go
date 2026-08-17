package faststats

import (
	"encoding/json"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// burstAfterIdle drives `workers` goroutines that, once per round, all record an event with the *same* timestamp
// after the window has been idle for far longer than its length.  It returns how many rounds observed a wrong count.
func burstAfterIdle(t *testing.T, workers int, rounds int, record func(now time.Time), check func(now time.Time) bool, start time.Time, gap time.Duration) int {
	t.Helper()
	var round, nowNanos, done atomic.Int64
	var stop atomic.Bool
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			seen := int64(0)
			for !stop.Load() {
				r := round.Load()
				if r == seen {
					runtime.Gosched()
					continue
				}
				seen = r
				record(time.Unix(0, nowNanos.Load()))
				done.Add(1)
			}
		}()
	}
	wrong := 0
	now := start
	for i := 1; i <= rounds; i++ {
		now = now.Add(gap)
		nowNanos.Store(now.UnixNano())
		done.Store(0)
		round.Store(int64(i))
		for done.Load() != int64(workers) {
			runtime.Gosched()
		}
		if !check(now) {
			wrong++
		}
	}
	stop.Store(true)
	wg.Wait()
	return wrong
}

// Advance used to publish the new LastAbsIndex (via CAS) *before* clearing the bucket, and to return a bucket even
// when its final CAS lost, so concurrent writers arriving together after an idle gap had their writes wiped.
func TestRegression_NoLostEventsAfterIdleGap(t *testing.T) {
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	t.Run("counter/small", func(t *testing.T) {
		x := NewRollingCounter(time.Millisecond, 4, start)
		const workers = 8
		wrong := burstAfterIdle(t, workers, 3000,
			func(now time.Time) { x.Inc(now) },
			func(now time.Time) bool { return x.RollingSumAt(now) == workers },
			start, time.Second)
		if wrong > 0 {
			t.Fatalf("lost events in %d rounds (TotalSum=%d)", wrong, x.TotalSum())
		}
	})
	t.Run("defaults", func(t *testing.T) {
		x := NewRollingCounter(time.Second, 10, start)
		p := NewRollingPercentile(10*time.Second, 6, 100, start)
		const workers = 4
		wrong := burstAfterIdle(t, workers, 3000,
			func(now time.Time) { x.Inc(now); p.AddDuration(time.Millisecond, now) },
			func(now time.Time) bool { return x.RollingSumAt(now) == workers && len(p.SnapshotAt(now)) == workers },
			start, 2*time.Minute)
		if wrong > 0 {
			t.Fatalf("counter/percentile wrong in %d rounds", wrong)
		}
	})
}

func TestRegression_RollingSumNeverNegative(t *testing.T) {
	x := NewRollingCounter(50*time.Microsecond, 2, time.Now())
	var stop atomic.Bool
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				x.Inc(time.Now())
			}
		}()
	}
	var negatives, minSeen int64
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		if v := x.RollingSumAt(time.Now()); v < 0 {
			negatives++
			if v < minSeen {
				minSeen = v
			}
		}
	}
	stop.Store(true)
	wg.Wait()
	if negatives > 0 {
		t.Fatalf("RollingSumAt returned a negative value %d times (min=%d)", negatives, minSeen)
	}
}

func TestRegression_UnmarshalRejectsNegativeBucketState(t *testing.T) {
	for _, js := range []string{
		`{"Buckets":[0,0,0],"RollingSum":0,"TotalSum":0,"RollingBucket":{"NumBuckets":3,"StartTime":"2020-01-01T00:00:00Z","BucketWidth":1000000,"LastAbsIndex":-5}}`,
		`{"Buckets":[0,0,0],"RollingSum":0,"TotalSum":0,"RollingBucket":{"NumBuckets":3,"StartTime":"2020-01-01T00:00:00Z","BucketWidth":-1000000,"LastAbsIndex":0}}`,
	} {
		var x RollingCounter
		if err := json.Unmarshal([]byte(js), &x); err == nil {
			t.Fatalf("expected corrupt JSON to be rejected: %s", js)
		}
		// and nothing panics afterwards
		now := time.Date(2020, 1, 1, 0, 0, 1, 0, time.UTC)
		x.Inc(now)
		_ = x.RollingSumAt(now)
		_ = x.String()
	}
	// RollingSum is derived; JSON without it (but otherwise complete) is accepted
	var y RollingCounter
	if err := json.Unmarshal([]byte(`{"Buckets":[1,2],"TotalSum":3,"RollingBucket":{"NumBuckets":2,"StartTime":"2020-01-01T00:00:00Z","BucketWidth":1000000000,"LastAbsIndex":1}}`), &y); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got := y.RollingSumAt(time.Date(2020, 1, 1, 0, 0, 1, 0, time.UTC)); got != 3 {
		t.Fatalf("rolling sum should be derived from buckets, got %d", got)
	}
}

func TestRegression_RollingCounterJSONShape(t *testing.T) {
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	x := NewRollingCounter(time.Second, 3, now)
	x.Inc(now)
	x.Inc(now.Add(time.Second))
	b, err := json.Marshal(&x)
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(b, &generic); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"Buckets", "RollingSum", "TotalSum", "RollingBucket"} {
		if _, ok := generic[k]; !ok {
			t.Fatalf("missing %s in %s", k, b)
		}
	}
	if string(generic["RollingSum"]) != "2" || string(generic["TotalSum"]) != "2" {
		t.Fatalf("unexpected sums in %s", b)
	}
	var y RollingCounter
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatal(err)
	}
	if y.RollingSumAt(now.Add(time.Second)) != 2 {
		t.Fatal("round trip lost data")
	}
}

// Snapshots must never surface a duration from an expired window through a reserved-but-not-yet-written slot.
func TestRegression_NoStaleDurationsInSnapshot(t *testing.T) {
	const rounds = 1500
	const writers = 4
	const addsPerWriter = 25
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	x := NewRollingPercentile(time.Millisecond, 2, 100, start)
	var round, nowNanos, done atomic.Int64
	var stop atomic.Bool
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			seen := int64(0)
			for !stop.Load() {
				r := round.Load()
				if r == seen {
					runtime.Gosched()
					continue
				}
				seen = r
				now := time.Unix(0, nowNanos.Load())
				for i := 0; i < addsPerWriter; i++ {
					x.AddDuration(time.Duration(r)*time.Hour, now)
				}
				done.Add(1)
			}
		}()
	}
	stale := 0
	now := start
	for r := int64(1); r <= rounds; r++ {
		now = now.Add(time.Second)
		nowNanos.Store(now.UnixNano())
		done.Store(0)
		round.Store(r)
		want := time.Duration(r) * time.Hour
		for done.Load() != writers {
			for _, d := range x.SnapshotAt(now) {
				if d != want {
					stale++
				}
			}
		}
		if got := len(x.SnapshotAt(now)); got != writers*addsPerWriter {
			t.Fatalf("round %d: snapshot has %d entries, want %d", r, got, writers*addsPerWriter)
		}
	}
	stop.Store(true)
	wg.Wait()
	if stale > 0 {
		t.Fatalf("observed %d stale/unset durations in snapshots", stale)
	}
}

func TestRegression_ZeroDurationRoundTrips(t *testing.T) {
	now := time.Now()
	x := NewRollingPercentile(time.Second, 2, 10, now)
	x.AddDuration(0, now)
	x.AddDuration(time.Nanosecond, now)
	snap := x.SnapshotAt(now)
	if len(snap) != 2 || snap[0] != 0 || snap[1] != time.Nanosecond {
		t.Fatalf("unexpected snapshot %v", snap)
	}
	b := newDurationsBucket(3)
	b.addDuration(0)
	b.addDuration(5)
	js, err := b.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(js) != `{"DurationsSomeInvalid":[0,5,0],"CurrentIndex":2}` {
		t.Fatalf("unexpected JSON %s", js)
	}
	var b2 durationsBucket
	if err := b2.UnmarshalJSON(js); err != nil {
		t.Fatal(err)
	}
	if got := b2.Durations(); len(got) != 2 || got[0] != 0 || got[1] != 5 {
		t.Fatalf("round trip mismatch: %v", got)
	}
	if len(b2.durationsSomeInvalid) != 3 {
		t.Fatalf("round trip lost bucket capacity: %d", len(b2.durationsSomeInvalid))
	}
	b2.addDuration(-time.Second)
	if got := b2.Durations(); len(got) != 3 || got[2] != 0 {
		t.Fatalf("negative duration should clamp to 0, got %v", got)
	}
}

func TestRegression_AdvanceClearsBeforePublishing(t *testing.T) {
	// Deterministic single-goroutine check of the ordering contract: while clearBucket runs, LastAbsIndex must not
	// yet point at (or past) the bucket being cleared.
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	rb := RollingBuckets{NumBuckets: 4, BucketWidth: time.Second, StartTime: start}
	rb.Advance(start.Add(100*time.Second), func(idx int) {
		if rb.LastAbsIndex.Get() != 0 {
			t.Fatalf("LastAbsIndex published (%d) before bucket %d was cleared", rb.LastAbsIndex.Get(), idx)
		}
	})
	if rb.LastAbsIndex.Get() != 100 {
		t.Fatalf("expected LastAbsIndex=100, got %d", rb.LastAbsIndex.Get())
	}
}
