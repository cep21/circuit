package faststats

import (
	"encoding/json"
	"math"
	"runtime"
	"strings"
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
			runtime.Gosched()
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

func TestRegression_ZeroAndNegativeDurationsStored(t *testing.T) {
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
	b.addDuration(-time.Second)
	if got := b.appendDurations(nil); len(got) != 3 || got[0] != 0 || got[1] != 5 || got[2] != 0 {
		t.Fatalf("expected [0 5 0] (negative duration clamps to 0), got %v", got)
	}
}

func TestEncodeDecodeDuration(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want time.Duration
	}{
		{0, 0},
		{time.Nanosecond, time.Nanosecond},
		{-5 * time.Nanosecond, 0},
		{time.Hour, time.Hour},
		{math.MaxInt64, math.MaxInt64},
		{math.MinInt64, 0},
	} {
		enc := encodeDuration(tc.in)
		if enc == unsetDuration {
			t.Errorf("encodeDuration(%d) collided with the unset sentinel", int64(tc.in))
		}
		if got := decodeDuration(enc); got != tc.want {
			t.Errorf("decodeDuration(encodeDuration(%d)) = %d, want %d", int64(tc.in), int64(got), int64(tc.want))
		}
	}
}

// Advance's slow path must report -1 when, while it waited for advanceMu, another goroutine rolled the window a full
// window (or more) past it.
func TestRegression_AdvanceBehindAfterLockWait(t *testing.T) {
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	rb := RollingBuckets{NumBuckets: 4, BucketWidth: time.Second, StartTime: start}
	window := time.Duration(rb.NumBuckets) * rb.BucketWidth

	entered := make(chan struct{})
	release := make(chan struct{})
	g1Result := make(chan int, 1)
	g2Result := make(chan int, 1)

	// G1 rolls 100 windows forward and parks inside the first clearBucket callback, holding advanceMu with
	// LastAbsIndex still unpublished (0).
	go func() {
		var once sync.Once
		g1Result <- rb.Advance(start.Add(100*window), func(int) {
			once.Do(func() {
				close(entered)
				<-release
			})
		})
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("G1 never reached clearBucket")
	}
	if got := rb.LastAbsIndex.Get(); got != 0 {
		t.Fatalf("LastAbsIndex published (%d) while G1 is still clearing", got)
	}

	// G2 is only two buckets in: it sees LastAbsIndex=0 < 2, takes the slow path and must queue behind G1.
	var g2Cleared atomic.Int64
	go func() {
		g2Result <- rb.Advance(start.Add(2*rb.BucketWidth), func(int) { g2Cleared.Add(1) })
	}()
	// Give G2 every chance to reach advanceMu before G1 is released.  The outcome is -1 either way (if G1 finishes
	// first G2 takes the fast-path -1), so this only steers which path is exercised; it cannot make the test flaky.
	waitForBlockedOn(t, "rollForward", time.Second)
	select {
	case got := <-g2Result:
		t.Fatalf("G2 returned %d while G1 still held advanceMu", got)
	default:
	}

	close(release)
	select {
	case got := <-g1Result:
		if got != 0 {
			t.Fatalf("G1: got bucket %d, want 0", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("G1 never returned")
	}
	select {
	case got := <-g2Result:
		if got != -1 {
			t.Fatalf("G2: got bucket %d, want -1 (it is 100 windows behind)", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("G2 never returned (advanceMu leaked?)")
	}
	if n := g2Cleared.Load(); n != 0 {
		t.Fatalf("G2 cleared %d buckets; a caller behind the window must clear nothing", n)
	}
	if got := rb.LastAbsIndex.Get(); got != 400 {
		t.Fatalf("LastAbsIndex = %d, want 400", got)
	}
}

// waitForBlockedOn polls goroutine stacks until one is parked in sync.Mutex.Lock underneath a frame containing fn, or
// the timeout passes.  It is best effort (used to steer an interleaving, never to decide pass/fail).
func waitForBlockedOn(t *testing.T, fn string, timeout time.Duration) {
	t.Helper()
	buf := make([]byte, 1<<16)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		n := runtime.Stack(buf, true)
		for _, g := range strings.Split(string(buf[:n]), "\n\n") {
			if strings.Contains(g, "sync.(*Mutex).") && strings.Contains(g, fn) {
				return
			}
		}
		runtime.Gosched()
	}
	t.Logf("did not observe a goroutine blocked in %s within %s; continuing", fn, timeout)
}

// A panicking clearBucket callback must not leak advanceMu.
func TestRegression_AdvanceUnlocksOnPanic(t *testing.T) {
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	rb := RollingBuckets{NumBuckets: 4, BucketWidth: time.Second, StartTime: start}
	func() {
		defer func() { _ = recover() }()
		rb.Advance(start.Add(time.Second), func(int) { panic("boom") })
	}()
	done := make(chan int, 1)
	go func() { done <- rb.Advance(start.Add(2*time.Second), func(int) {}) }()
	select {
	case got := <-done:
		if got != 2 {
			t.Fatalf("got bucket %d, want 2", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Advance deadlocked: advanceMu leaked by a panicking clearBucket")
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
