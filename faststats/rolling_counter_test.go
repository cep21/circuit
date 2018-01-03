package faststats

import (
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRollingCounter_Empty(t *testing.T) {
	x := RollingCounter{}
	now := time.Now()
	s := x.RollingSumAt(now)
	if s != 0 {
		t.Errorf("expect to start with empty sum %d", s)
	}
	x.Inc(time.Now())
	if x.TotalSum() != 1 {
		t.Error("Total sum should work even on empty structure")
	}
}

func TestRollingCounter_MovingBackwards(t *testing.T) {
	now := time.Now()
	x := NewRollingCounter(time.Millisecond, 10, now)
	x.Inc(now)
	x.Inc(now.Add(time.Millisecond * 2))
	x.Inc(now)
	endTime := now.Add(time.Millisecond * 2)
	b := x.GetBuckets(endTime)
	if b[0] != 1 {
		t.Error("Expect one value at current bucket")
	}
	if b[2] != 2 {
		t.Error("expect 2 values at 2 back buckets")
	}
}

func TestRollingCounter_NormalConsistency(t *testing.T) {
	now := time.Now()
	bucketSize := 100
	numBuckets := 20
	x := NewRollingCounter(time.Millisecond*time.Duration(bucketSize), numBuckets, now)
	concurrent := 20
	end := 10000
	wg := sync.WaitGroup{}
	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < end; j++ {
				newNow := now.Add(time.Duration(time.Millisecond.Nanoseconds() * int64(j)))
				x.Inc(newNow)
				if j%bucketSize == 0 {
					time.Sleep(time.Millisecond * 10)
				}
			}
		}()
	}
	wg.Wait()
	newNow := now.Add(time.Duration(time.Millisecond.Nanoseconds() * int64(end)))
	if x.RollingSumAt(newNow) != int64(concurrent*bucketSize*(numBuckets-1)) {
		t.Error("small rolling sum", x.RollingSumAt(newNow), "when we want", concurrent*bucketSize*(numBuckets-1))
	}
	b := x.GetBuckets(newNow)
	if b[1] != int64(concurrent*bucketSize) {
		t.Error("incorrect size at b[1]", b[1], "wanting", concurrent*bucketSize)
	}
}

func BenchmarkRollingCounter(b *testing.B) {
	type rollingCounterTestCase struct {
		name       string
		bucketSize time.Duration
		numBuckets int
	}
	concurrents := []int{1, 50}
	runs := []rollingCounterTestCase{
		{
			name:       "super-small-buckets",
			bucketSize: time.Nanosecond,
			numBuckets: 20,
		},
		{
			name:       "normal-rate",
			bucketSize: time.Nanosecond * 100,
			numBuckets: 10,
		},
		{
			name:       "default",
			bucketSize: time.Millisecond * 100,
			numBuckets: 10,
		},
	}
	for _, run := range runs {
		b.Run(run.name, func(b *testing.B) {
			for _, concurrent := range concurrents {
				b.Run(strconv.Itoa(concurrent), func(b *testing.B) {
					now := time.Now()
					x := NewRollingCounter(run.bucketSize, run.numBuckets, now)
					wg := sync.WaitGroup{}
					addAmount := AtomicInt64{}
					for i := 0; i < concurrent; i++ {
						wg.Add(1)
						go func() {
							defer wg.Done()
							for i := 0; i < b.N/concurrent; i++ {
								x.Inc(now.Add(time.Duration(addAmount.Add(1))))
							}
						}()
					}
					wg.Wait()
				})
			}
		})
	}
}

func doTillTime(endTime time.Time, wg *sync.WaitGroup, f func()) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		for time.Now().Before(endTime) {
			f()
			// Don't need to sleep.  Just busy loop.  But let another thread take over if it wants (to get some concurrency)
			runtime.Gosched()
		}
	}()
}

func TestRollingCounter_Race(t *testing.T) {
	startTime := time.Now()
	x := NewRollingCounter(time.Millisecond, 10, startTime)
	wg := sync.WaitGroup{}
	concurrent := 50
	doNotPassTime := startTime.Add(time.Millisecond * 50)
	for i := 0; i < concurrent; i++ {
		doTillTime(doNotPassTime, &wg, func() {
			x.Inc(time.Now())
		})
		doTillTime(doNotPassTime, &wg, func() {
			x.TotalSum()
		})
		doTillTime(doNotPassTime, &wg, func() {
			x.RollingSumAt(time.Now())
		})
		doTillTime(doNotPassTime, &wg, func() {
			x.GetBuckets(time.Now())
		})
	}
	wg.Wait()
}

func TestRollingCounter_IncPast(t *testing.T) {
	now := time.Now()
	x := NewRollingCounter(time.Millisecond, 4, now)
	x.Inc(now)
	if x.RollingSumAt(now) != 1 {
		t.Errorf("Should see a single item after adding by 1")
	}
	x.Inc(now.Add(time.Millisecond * 100))
	if x.RollingSumAt(now) != 1 {
		t.Errorf("Should see one item, saw %d", x.RollingSumAt(now))
	}
}

func TestRollingCounter_Inc(t *testing.T) {
	now := time.Now()
	x := NewRollingCounter(time.Millisecond, 10, now)
	if x.String() != "rolling_sum=0 total_sum=0 parts=(0,0,0,0,0,0,0,0,0,0)" {
		t.Errorf("String() function does not work: %s", x.String())
	}
	x.Inc(now)
	if x.RollingSumAt(now) != 1 {
		t.Errorf("Should see a single item after adding by 1")
	}
	x.Inc(now)
	if x.RollingSumAt(now) != 2 {
		t.Errorf("Should see two items now")
	}
}

func expectBuckets(t *testing.T, now time.Time, in *RollingCounter, b []int64) {
	a := in.GetBuckets(now)
	if len(a) != len(b) {
		t.Fatalf("Len not right: %d vs %d", len(a), len(b))
	}
	var p1 []string
	var p2 []string
	for i := range b {
		p1 = append(p1, strconv.FormatInt(a[i], 10))
		p2 = append(p2, strconv.FormatInt(b[i], 10))
	}
	c1 := strings.Join(p1, ",")
	c2 := strings.Join(p2, ",")
	if c1 != c2 {
		t.Fatalf("buckets not as expected: seen=(%s) vs expected=(%s)", c1, c2)
	}
}

func TestRollingCounter_MoveForward(t *testing.T) {
	startTime := time.Now()
	x := NewRollingCounter(time.Millisecond, 4, startTime)

	expectBuckets(t, startTime, &x, []int64{0, 0, 0, 0})
	x.Inc(startTime)
	x.Inc(startTime)
	if x.RollingSumAt(startTime) != 2 {
		t.Errorf("Should see two items after adding by 1 twice")
	}
	expectBuckets(t, startTime, &x, []int64{2, 0, 0, 0})

	nextTime := startTime.Add(time.Millisecond)
	x.Inc(nextTime)
	if x.RollingSumAt(nextTime) != 3 {
		t.Errorf("Should see a sum of 3 after advancing")
	}
	if x.TotalSum() != 3 {
		t.Errorf("Should see a sum of 3 after advancing")
	}
	expectBuckets(t, nextTime, &x, []int64{1, 2, 0, 0})

	moveCloseToEnd := startTime.Add(time.Millisecond * 3)

	x.Inc(moveCloseToEnd)
	expectBuckets(t, moveCloseToEnd, &x, []int64{1, 0, 1, 2})
	if x.RollingSumAt(moveCloseToEnd) != 4 {
		t.Errorf("Should see a sum of 3 after advancing close to the end")
	}

	movePastOneBucket := startTime.Add(time.Millisecond * 4)
	x.Inc(movePastOneBucket)
	expectBuckets(t, movePastOneBucket, &x, []int64{1, 1, 0, 1})
	if x.RollingSumAt(movePastOneBucket) != 3 {
		t.Errorf("Should see a sum of 3 after advancing close to the end again")
	}

	movePastAllButOneBucket := movePastOneBucket.Add(time.Millisecond * 3)
	x.Inc(movePastAllButOneBucket)
	expectBuckets(t, movePastAllButOneBucket, &x, []int64{1, 0, 0, 1})
	if x.RollingSumAt(movePastAllButOneBucket) != 2 {
		t.Errorf("Should see a sum of 2 after advancing close to the end")
	}

	movePastAllBuckets := movePastAllButOneBucket.Add(time.Millisecond * 4)
	x.Inc(movePastAllBuckets)
	expectBuckets(t, movePastAllBuckets, &x, []int64{1, 0, 0, 0})
	if s := x.RollingSumAt(movePastAllBuckets); s != 1 {
		t.Errorf("Should see a sum of 1 after advancing past all the buckets, saw %d", s)
	}
}
