package faststats

import (
	"encoding/json"
	"expvar"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRollingPercentile_Fresh(t *testing.T) {
	now := time.Now()
	x := NewRollingPercentile(time.Second, 10, 100, now)
	snap := x.SnapshotAt(now)
	expectSnap(t, "at empty", snap, 0, -1, map[float64]time.Duration{
		50: -1,
	})
}

func TestRollingPercentile_Reset(t *testing.T) {
	now := time.Now()
	x := NewRollingPercentile(time.Second, 10, 100, now)
	x.AddDuration(time.Second, now)
	expectSnap(t, "at first", x.SnapshotAt(now), 1, time.Second, map[float64]time.Duration{
		0.0: time.Second,
		1.0: time.Second,
	})
	x.Reset(now)
	expectSnap(t, "at first", x.SnapshotAt(now), 0, -1, map[float64]time.Duration{
		0.0: -1,
	})
}

func TestDurationsBucket_String(t *testing.T) {
	x := newDurationsBucket(10)
	x.addDuration(time.Second)
	dur := x.Durations()
	if !reflect.DeepEqual(dur, []time.Duration{time.Second}) {
		t.Fatalf("unexpected durations")
	}
	if x.String() != "durationsBucket(idx=1)" {
		t.Fatalf("unexpected string value: %s", x.String())
	}

	b, err := json.Marshal(&x)
	if err != nil {
		t.Fatalf("Expect no error: %s", err)
	}
	var y durationsBucket
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatal("unexpected error marshalling", err)
	}
	if !reflect.DeepEqual(y.Durations(), x.Durations()) {
		t.Fatal("expected same durations")
	}
}

func TestDurationsBucket_IterateDurations(t *testing.T) {
	x := newDurationsBucket(10)
	x.IterateDurations(0, func(_ time.Duration) {
		t.Fatal("nothing in there")
	})
	c := 0
	x.addDuration(time.Second)
	x.IterateDurations(0, func(d time.Duration) {
		c++
		if d != time.Second {
			t.Fatal("Expected a second")
		}
	})
	if c != 1 {
		t.Fatal("Expected 1 counter")
	}
}

func TestSortedDurations_asJSON(t *testing.T) {
	x := SortedDurations{
		time.Second, time.Millisecond,
	}
	t.Log(x.String())
	b, err := json.Marshal(x)
	if err != nil {
		t.Error("Could not marshal durations", err)
	}
	t.Log(string(b))
}

func TestRollingPercentile_Empty(t *testing.T) {
	x := RollingPercentile{}
	x.AddDuration(time.Millisecond, time.Now())
	snap := x.SnapshotAt(time.Now())
	expectSnap(t, "at empty", snap, 0, -1, map[float64]time.Duration{
		50: -1,
	})
}

func TestRollingPercentile_Race(t *testing.T) {
	now := time.Now()
	x := NewRollingPercentile(time.Millisecond, 10, 100, now)
	wg := sync.WaitGroup{}
	concurrent := 50
	doNotPassTime := time.Now().Add(time.Millisecond * 50)
	for i := 0; i < concurrent; i++ {
		doTillTime(doNotPassTime, &wg, func() {
			x.AddDuration(time.Second, time.Now())
		})
		doTillTime(doNotPassTime, &wg, func() {
			x.SnapshotAt(time.Now())
		})
		doTillTime(doNotPassTime, &wg, func() {
			//nolint:staticcheck
			_, err := json.Marshal(&x)
			if err != nil {
				t.Error("unable to marshal", err)
			}
		})
		doTillTime(doNotPassTime, &wg, func() {
			s := x.Var().String()
			if !strings.Contains(s, "snap") {
				t.Error("expected to contain snap")
			}
		})
	}
	wg.Wait()
}

func TestRollingPercentile_AddDuration(t *testing.T) {
	now := time.Now()
	x := NewRollingPercentile(time.Second, 10, 100, now)
	x.AddDuration(time.Second*2, now)
	snap := x.SnapshotAt(now)
	expectSnap(t, "at one item", snap, 1, time.Second*2, map[float64]time.Duration{
		0:   time.Second * 2,
		99:  time.Second * 2,
		100: time.Second * 2,
	})

	x.AddDuration(time.Second, now)
	snap = x.SnapshotAt(now)
	expectSnap(t, "at second item", snap, 2, time.Second*3/2, map[float64]time.Duration{
		0:   time.Second,
		50:  time.Second + time.Second/2,
		100: time.Second * 2,
	})

	x.AddDuration(time.Second*3, now)
	snap = x.SnapshotAt(now)
	expectSnap(t, "at third item", snap, 3, time.Second*2, map[float64]time.Duration{
		0:   time.Second,
		25:  time.Second + time.Second/2,
		50:  time.Second * 2,
		75:  time.Second*2 + time.Second/2,
		100: time.Second * 3,
	})
}

func expectSnap(t *testing.T, name string, snap SortedDurations, size int, mean time.Duration, percentiles map[float64]time.Duration) {
	if len(snap) != size {
		t.Errorf("Unexpected size: %d vs %d for %s", len(snap), size, name)
	}
	if mean != snap.Mean() {
		t.Fatalf("Unexpected mean: saw=%d vs expected=%d for %s at %s", snap.Mean(), mean, name, snap)
	}
	for p, expected := range percentiles {
		per := snap.Percentile(p)
		if per != expected {
			t.Errorf("Unexpected percentile %f: %d vs %d for %s", p, per, expected, name)
		}
	}
}

func TestRollingPercentile_Movement(t *testing.T) {
	// 100 ms per bucket
	now := time.Now()
	x := NewRollingPercentile(time.Millisecond*100, 10, 100, now)
	x.AddDuration(time.Millisecond, now)
	x.AddDuration(time.Millisecond*3, now)
	x.AddDuration(time.Millisecond*2, now.Add(time.Millisecond*500))
	x.AddDuration(time.Millisecond*4, now.Add(time.Millisecond*900))
	// should have vlaues 1, 2, 3, 4

	snap := x.SnapshotAt(now.Add(time.Millisecond * 900))
	expectSnap(t, "at start", snap, 4, time.Millisecond*10/4, map[float64]time.Duration{
		0:               time.Millisecond,
		1.0 / 3.0 * 100: time.Millisecond * 2,
		50:              time.Millisecond*2 + time.Millisecond/2,
		2.0 / 3.0 * 100: time.Millisecond * 3,
		100:             time.Millisecond * 4,
	})

	x.AddDuration(time.Millisecond*5, now.Add(time.Millisecond*1001))
	snap = x.SnapshotAt(now.Add(time.Millisecond * 1001))
	// The first two values should fall off, and we should add one new one
	// expect [2, 4, 5]
	expectSnap(t, "after falling off", snap, 3, time.Millisecond*11/3, map[float64]time.Duration{
		0:   time.Millisecond * 2,
		50:  time.Millisecond * 4,
		100: time.Millisecond * 5,
	})

	snap = x.SnapshotAt(now.Add(time.Hour))
	// All values should fall off
	expectSnap(t, "after all falling off", snap, 0, -1, map[float64]time.Duration{
		0:   -1,
		50:  -1,
		100: -1,
	})
}


func TestRollingPercentile_AddDurationBeforeStartTime(t *testing.T) {
	now := time.Now()
	x := NewRollingPercentile(time.Second, 10, 100, now)
	// Should not panic when adding a duration before StartTime
	x.AddDuration(time.Millisecond, now.Add(-time.Hour))
	// The duration should be silently dropped
	snap := x.SnapshotAt(now)
	expectSnap(t, "before start time", snap, 0, -1, map[float64]time.Duration{
		50: -1,
	})
}

func TestSortedDurations_Var(t *testing.T) {
	durations := make(SortedDurations, 100)
	for i := 0; i < 100; i++ {
		durations[i] = time.Duration(i+1) * time.Millisecond
	}
	v := durations.Var()
	m := v.(expvar.Func).Value().(map[string]string)
	// p50 should be near 50ms, not near 0.5ms
	if m["p50"] != durations.Percentile(50).String() {
		t.Errorf("p50 mismatch: got %s, want %s", m["p50"], durations.Percentile(50).String())
	}
	if m["p90"] != durations.Percentile(90).String() {
		t.Errorf("p90 mismatch: got %s, want %s", m["p90"], durations.Percentile(90).String())
	}
}
