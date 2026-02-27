package faststats

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"sort"
	"testing"
	"time"
)

// ============================================================================
// Fuzz targets for faststats. These encode structural invariants as executable
// specifications — the kind of edge-case bugs (zero-values, empty JSON,
// nil-derefs, div-by-zero) that unit tests miss and fuzzing finds fast.
//
// Run with: go test -fuzz=FuzzRollingCounterOps -fuzztime=30s ./faststats
// ============================================================================

// naiveRollingCounter is a trivially-correct, lock-free-unfriendly reference
// implementation used as a differential oracle. It records every Inc time and
// answers queries by linear scan — obviously correct, obviously slow.
type naiveRollingCounter struct {
	bucketWidth time.Duration
	numBuckets  int
	startTime   time.Time
	incs        []time.Time
}

func newNaive(bucketWidth time.Duration, numBuckets int, startTime time.Time) *naiveRollingCounter {
	return &naiveRollingCounter{
		bucketWidth: bucketWidth,
		numBuckets:  numBuckets,
		startTime:   startTime,
	}
}

func (n *naiveRollingCounter) Inc(now time.Time) {
	// Mirror real RollingCounter semantics: ignore times before startTime.
	if now.Before(n.startTime) {
		return
	}
	n.incs = append(n.incs, now)
}

// RollingSumAt returns events whose bucket is still within the window at 'now'.
// A past event at time t is in-window iff the current bucket index minus t's
// bucket index < numBuckets. This matches RollingBuckets.Advance semantics.
func (n *naiveRollingCounter) RollingSumAt(now time.Time) int64 {
	if n.numBuckets == 0 || n.bucketWidth == 0 {
		return 0
	}
	if now.Before(n.startTime) {
		return 0
	}
	nowIdx := now.Sub(n.startTime).Nanoseconds() / n.bucketWidth.Nanoseconds()
	var sum int64
	for _, t := range n.incs {
		if t.After(now) {
			// The real RollingCounter can retain future Incs if the window
			// later advances past them. But for a monotone time sequence
			// (which we use in the fuzz test) this never happens.
			continue
		}
		tIdx := t.Sub(n.startTime).Nanoseconds() / n.bucketWidth.Nanoseconds()
		if nowIdx-tIdx < int64(n.numBuckets) {
			sum++
		}
	}
	return sum
}

func (n *naiveRollingCounter) TotalSum() int64 {
	return int64(len(n.incs))
}

// FuzzRollingCounterOps drives RollingCounter with a fuzzed sequence of
// monotone-time Inc/RollingSumAt/GetBuckets ops and checks invariants:
//   - TotalSum == number of Inc calls (the obvious conservation law)
//   - RollingSumAt ≥ 0 always
//   - RollingSumAt ≤ TotalSum always
//   - sum(GetBuckets) == RollingSumAt (buckets and rolling sum agree)
//   - RollingSumAt matches the naive oracle
//
// Time is monotone here because RollingCounter's behaviour on backward time is
// intentionally lossy (see rolling_bucket.go: events landing in buckets that
// have since been cleared are dropped). Fuzzing backward time would produce
// spurious oracle mismatches.
func FuzzRollingCounterOps(f *testing.F) {
	f.Add([]byte{3, 1, 0, 1, 0, 2, 0, 1, 2})
	f.Add([]byte{10, 5, 0, 0, 0, 0, 2})
	f.Add([]byte{1, 100})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 2 {
			return
		}
		// Derive config from first two bytes. Keep ranges small so the fuzzer
		// explores op sequences rather than giant bucket arrays.
		numBuckets := int(data[0])%16 + 1                                   // 1..16
		bucketWidth := time.Duration(int(data[1])%100+1) * time.Millisecond // 1..100ms
		ops := data[2:]

		start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		rc := NewRollingCounter(bucketWidth, numBuckets, start)
		oracle := newNaive(bucketWidth, numBuckets, start)

		now := start
		var incCount int64

		for _, b := range ops {
			op := b % 3
			// Always advance time by a small fuzz-derived amount.
			// Monotone non-decreasing; may advance by 0.
			step := time.Duration(b>>2) * bucketWidth / 8
			now = now.Add(step)

			switch op {
			case 0: // Inc
				rc.Inc(now)
				oracle.Inc(now)
				incCount++
			case 1: // RollingSumAt + invariants
				got := rc.RollingSumAt(now)
				if got < 0 {
					t.Fatalf("RollingSumAt < 0: %d (now=%s)", got, now.Sub(start))
				}
				if got > rc.TotalSum() {
					t.Fatalf("RollingSumAt=%d > TotalSum=%d", got, rc.TotalSum())
				}
				want := oracle.RollingSumAt(now)
				if got != want {
					t.Fatalf("RollingSumAt mismatch: real=%d oracle=%d "+
						"(numBuckets=%d bucketWidth=%s now=+%s incs=%d)",
						got, want, numBuckets, bucketWidth, now.Sub(start), incCount)
				}
			case 2: // GetBuckets + sum-consistency
				buckets := rc.GetBuckets(now)
				if len(buckets) != numBuckets {
					t.Fatalf("GetBuckets len=%d, want %d", len(buckets), numBuckets)
				}
				var bsum int64
				for _, v := range buckets {
					if v < 0 {
						t.Fatalf("negative bucket value: %d", v)
					}
					bsum += v
				}
				rsum := rc.RollingSumAt(now)
				if bsum != rsum {
					t.Fatalf("sum(GetBuckets)=%d ≠ RollingSumAt=%d (buckets=%v)", bsum, rsum, buckets)
				}
			}
		}

		// Final conservation check.
		if rc.TotalSum() != incCount {
			t.Fatalf("TotalSum=%d ≠ Inc count=%d", rc.TotalSum(), incCount)
		}
		if oracle.TotalSum() != incCount {
			t.Fatalf("oracle TotalSum=%d ≠ Inc count=%d", oracle.TotalSum(), incCount)
		}
	})
}

// FuzzRollingCounterJSON verifies Marshal→Unmarshal round-trips and that
// arbitrary bytes fed to Unmarshal never panic (bug #8 in past fixes was a
// nil-deref on `{}`). The guard at rolling_counter.go:65 should make this safe.
func FuzzRollingCounterJSON(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"TotalSum":5}`))
	f.Add([]byte(`{"Buckets":[1,2],"RollingSum":3,"TotalSum":3,"RollingBucket":{"NumBuckets":2,"StartTime":"2020-01-01T00:00:00Z","BucketWidth":1000000,"LastAbsIndex":0}}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`[]`))
	f.Add([]byte(`"`))

	f.Fuzz(func(t *testing.T, data []byte) {
		// Part 1: arbitrary bytes must not panic UnmarshalJSON. On error the
		// receiver is unmodified; on success the state is validated-consistent.
		// Either way, subsequent method calls must be safe.
		var sink RollingCounter
		_ = sink.UnmarshalJSON(data) // error is fine; panic is not
		now := time.Now()
		_ = sink.GetBuckets(now)
		_ = sink.RollingSumAt(now)
		_ = sink.TotalSum()
		_ = sink.String()

		// Part 2: seed a counter from the same data bytes, then verify
		// Marshal→Unmarshal is a clean round trip.
		if len(data) < 4 {
			return
		}
		numBuckets := int(data[0])%8 + 1
		bucketWidth := time.Duration(int(data[1])%50+1) * time.Millisecond
		start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		orig := NewRollingCounter(bucketWidth, numBuckets, start)
		for _, b := range data[2:] {
			orig.Inc(start.Add(time.Duration(b) * time.Millisecond))
		}

		marshaled, err := json.Marshal(&orig)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var restored RollingCounter
		if err := json.Unmarshal(marshaled, &restored); err != nil {
			t.Fatalf("Unmarshal of valid Marshal output: %v (json=%s)", err, marshaled)
		}
		if restored.TotalSum() != orig.TotalSum() {
			t.Fatalf("round-trip TotalSum: got=%d want=%d", restored.TotalSum(), orig.TotalSum())
		}
		// GetBuckets must not panic on restored value.
		_ = restored.GetBuckets(start)
	})
}

// FuzzSortedDurationsPercentile checks the Percentile function:
//   - Percentile(p) ∈ [Min, Max] for any p (including NaN, ±Inf, out-of-range)
//   - Monotone non-decreasing in p
//   - Never panics on any input
func FuzzSortedDurationsPercentile(f *testing.F) {
	// Seeds: edge cases from past bug hunting.
	seed := func(durs []uint64, p float64) []byte {
		buf := make([]byte, 8+8*len(durs))
		binary.LittleEndian.PutUint64(buf, math.Float64bits(p))
		for i, d := range durs {
			binary.LittleEndian.PutUint64(buf[8+8*i:], d)
		}
		return buf
	}
	f.Add(seed([]uint64{1, 2, 3}, 50.0))
	f.Add(seed([]uint64{100}, 0.0))
	f.Add(seed([]uint64{}, 99.9))
	f.Add(seed([]uint64{1, 2}, math.Inf(1)))
	f.Add(seed([]uint64{1, 2}, math.Inf(-1)))
	f.Add(seed([]uint64{5, 5, 5}, -1.0))

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 8 {
			return
		}
		p := math.Float64frombits(binary.LittleEndian.Uint64(data))

		// Build a sorted duration list from remaining bytes. Keep values
		// non-negative and bounded to avoid int64 overflow in Mean()'s sum.
		raw := data[8:]
		n := len(raw) / 8
		if n > 1000 {
			n = 1000
		}
		durs := make([]time.Duration, n)
		const maxDur = uint64(time.Hour)
		for i := 0; i < n; i++ {
			v := binary.LittleEndian.Uint64(raw[8*i:]) % maxDur
			// Bound to [0, 1h] so sum of 1000 durations stays well under int64 max.
			durs[i] = time.Duration(int64(v)) //nolint:gosec // v < 2^62, fits int64
		}
		sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
		sd := SortedDurations(durs)

		// Any input, including NaN/Inf p, must not panic.
		got := sd.Percentile(p)
		mn := sd.Min()
		mx := sd.Max()
		_ = sd.Mean()
		_ = sd.String()

		if len(sd) == 0 {
			if got != -1 || mn != -1 || mx != -1 {
				t.Fatalf("empty list should return -1, got pct=%d min=%d max=%d", got, mn, mx)
			}
			return
		}

		// Bounds: Percentile(p) ∈ [Min, Max] for any non-NaN p; NaN returns -1.
		if math.IsNaN(p) {
			if got != -1 {
				t.Fatalf("Percentile(NaN)=%v, want -1", got)
			}
			return
		}
		if got < mn || got > mx {
			t.Fatalf("Percentile(%g)=%v out of [Min=%v, Max=%v] (n=%d)", p, got, mn, mx, len(sd))
		}

		// Monotonicity: for any p1 ≤ p2, Percentile(p1) ≤ Percentile(p2).
		// Sample a second percentile from the same fuzz input.
		if len(data) >= 16 {
			p2 := math.Float64frombits(binary.LittleEndian.Uint64(data[len(data)-8:]))
			if !math.IsNaN(p2) && !math.IsInf(p2, 0) && !math.IsInf(p, 0) {
				lo, hi := p, p2
				if lo > hi {
					lo, hi = hi, lo
				}
				if sd.Percentile(lo) > sd.Percentile(hi) {
					t.Fatalf("Percentile not monotone: P(%g)=%v > P(%g)=%v",
						lo, sd.Percentile(lo), hi, sd.Percentile(hi))
				}
			}
		}
	})
}

// TestRollingCounter_UnmarshalJSON_InconsistentState is a regression test for
// a panic found by FuzzRollingCounterJSON: hostile JSON with NumBuckets > 0
// but Buckets == nil previously passed validation and caused GetBuckets to
// index out of range. Now rejected at unmarshal time.
func TestRollingCounter_UnmarshalJSON_InconsistentState(t *testing.T) {
	var x RollingCounter
	// Buckets omitted; NumBuckets=1 via RollingBucket.
	hostile := []byte(`{"RollingSum":0,"TotalSum":0,"RollingBucket":{"NumBuckets":1,"StartTime":"2020-01-01T00:00:00Z","BucketWidth":1000000,"LastAbsIndex":0}}`)
	if err := x.UnmarshalJSON(hostile); err == nil {
		t.Fatal("expected error for inconsistent JSON (NumBuckets=1, Buckets=nil)")
	}
	// Receiver must be unmodified (zero-value) on error; methods must be safe.
	if b := x.GetBuckets(time.Now()); b != nil {
		t.Errorf("GetBuckets after rejected unmarshal = %v, want nil", b)
	}
}

// TestSortedDurations_Percentile_NaN is a regression test for a panic found
// by FuzzSortedDurationsPercentile: Percentile(NaN) falls through both the
// p <= 0 and p >= 100 guards (NaN comparisons are always false), then did
// int(math.Floor(NaN)) which is platform-undefined — INT64_MIN on amd64,
// causing index-out-of-range. Now returns -1.
func TestSortedDurations_Percentile_NaN(t *testing.T) {
	sd := SortedDurations{time.Millisecond, time.Millisecond * 2}
	if got := sd.Percentile(math.NaN()); got != -1 {
		t.Errorf("Percentile(NaN) = %v, want -1", got)
	}
}

// FuzzRollingBucketAdvance checks the lock-free Advance loop at
// rolling_bucket.go:28-74 — the hairiest code in the package. Invariants:
//   - Returned index is either -1 or in [0, NumBuckets)
//   - clearBucket is never called with an out-of-range index
//   - LastAbsIndex is monotone non-decreasing across calls
func FuzzRollingBucketAdvance(f *testing.F) {
	f.Add([]byte{5, 10, 0, 1, 2, 3, 4, 5, 100, 200})
	f.Add([]byte{1, 1, 0, 0, 0})
	f.Add([]byte{3, 50, 255, 0, 255})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 2 {
			return
		}
		numBuckets := int(data[0])%16 + 1
		bucketWidth := time.Duration(int(data[1])%100+1) * time.Millisecond
		timeBytes := data[2:]

		start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		rb := RollingBuckets{
			NumBuckets:  numBuckets,
			StartTime:   start,
			BucketWidth: bucketWidth,
		}

		clearFn := func(idx int) {
			if idx < 0 || idx >= numBuckets {
				t.Fatalf("clearBucket called with out-of-range idx=%d (numBuckets=%d)", idx, numBuckets)
			}
		}

		prevLastAbs := rb.LastAbsIndex.Get()
		for _, b := range timeBytes {
			// Time can jump forward by up to ~63 bucket widths, or backward
			// by up to ~2 bucket widths. Both are realistic workloads.
			delta := time.Duration(int(b)-4) * bucketWidth / 4
			now := start.Add(delta)
			// Allow start to creep forward too so we explore large absolute
			// indices over many ops.
			if delta > 0 {
				start = start.Add(delta)
			}

			idx := rb.Advance(now, clearFn)

			if idx != -1 && (idx < 0 || idx >= numBuckets) {
				t.Fatalf("Advance returned out-of-range index: %d (numBuckets=%d)", idx, numBuckets)
			}

			lastAbs := rb.LastAbsIndex.Get()
			if lastAbs < prevLastAbs {
				t.Fatalf("LastAbsIndex went backward: %d -> %d", prevLastAbs, lastAbs)
			}
			prevLastAbs = lastAbs
		}
	})
}

// FuzzTimedCheckJSON verifies Marshal→Unmarshal round-trip and that arbitrary
// bytes never panic Unmarshal (or subsequent method calls).
func FuzzTimedCheckJSON(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"SleepDuration":1000000000,"EventCountToAllow":5}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`{"NextOpenTime":"invalid"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		// Part 1: arbitrary bytes never panic.
		var tc TimedCheck
		_ = tc.UnmarshalJSON(data)
		now := time.Now()
		_ = tc.Check(now)
		_ = tc.String()

		// Part 2: round-trip a configured TimedCheck.
		if len(data) < 2 {
			return
		}
		var orig TimedCheck
		orig.SetSleepDuration(time.Duration(data[0]) * time.Millisecond)
		orig.SetEventCountToAllow(int64(data[1]))

		marshaled, err := json.Marshal(&orig)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var restored TimedCheck
		if err := json.Unmarshal(marshaled, &restored); err != nil {
			t.Fatalf("Unmarshal valid Marshal output: %v (json=%s)", err, marshaled)
		}
		// Can't directly compare because fields are unexported, but Check must
		// not panic.
		_ = restored.Check(now)
	})
}
