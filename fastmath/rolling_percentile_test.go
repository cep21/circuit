package fastmath

import (
	"sync"
	"testing"
	"time"
)

func TestRollingPercentile_Fresh(t *testing.T) {
	now := time.Now()
	x := NewRollingPercentile(time.Second, 10, 100, now)
	snap := x.Snapshot(now)
	expectSnap(t, "at empty", snap, 0, -1, map[float64]time.Duration{
		50: -1,
	})
}

func TestRollingPercentile_Empty(t *testing.T) {
	x := RollingPercentile{}
	x.AddDuration(time.Millisecond, time.Now())
	snap := x.Snapshot(time.Now())
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
			x.Snapshot(time.Now())
		})
	}
	wg.Wait()
}

func TestRollingPercentile_AddDuration(t *testing.T) {
	now := time.Now()
	x := NewRollingPercentile(time.Second, 10, 100, now)
	x.AddDuration(time.Second*2, now)
	snap := x.Snapshot(now)
	expectSnap(t, "at one item", snap, 1, time.Second*2, map[float64]time.Duration{
		0:   time.Second * 2,
		99:  time.Second * 2,
		100: time.Second * 2,
	})

	x.AddDuration(time.Second, now)
	snap = x.Snapshot(now)
	expectSnap(t, "at second item", snap, 2, time.Second*3/2, map[float64]time.Duration{
		0:   time.Second,
		50:  time.Second + time.Second/2,
		100: time.Second * 2,
	})

	x.AddDuration(time.Second*3, now)
	snap = x.Snapshot(now)
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

	snap := x.Snapshot(now.Add(time.Millisecond * 900))
	expectSnap(t, "at start", snap, 4, time.Millisecond*10/4, map[float64]time.Duration{
		0:               time.Millisecond,
		1.0 / 3.0 * 100: time.Millisecond * 2,
		50:              time.Millisecond*2 + time.Millisecond/2,
		2.0 / 3.0 * 100: time.Millisecond * 3,
		100:             time.Millisecond * 4,
	})

	x.AddDuration(time.Millisecond*5, now.Add(time.Millisecond*1001))
	snap = x.Snapshot(now.Add(time.Millisecond * 1001))
	// The first two values should fall off, and we should add one new one
	// expect [2, 4, 5]
	expectSnap(t, "after falling off", snap, 3, time.Millisecond*11/3, map[float64]time.Duration{
		0:   time.Millisecond * 2,
		50:  time.Millisecond * 4,
		100: time.Millisecond * 5,
	})

	snap = x.Snapshot(now.Add(time.Hour))
	// All values should fall off
	expectSnap(t, "after all falling off", snap, 0, -1, map[float64]time.Duration{
		0:   -1,
		50:  -1,
		100: -1,
	})
}
