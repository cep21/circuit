package faststats

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// referenceCounter is a single threaded RollingCounter that keeps an explicit total
type referenceCounter struct {
	buckets []AtomicInt64
	total   AtomicInt64
	rb      RollingBuckets
}

func (r *referenceCounter) advance(now time.Time) int {
	return referenceAdvance(&r.rb, now, func(i int) { r.buckets[i].Set(0) })
}

func (r *referenceCounter) inc(now time.Time) {
	r.total.Add(1)
	if idx := r.advance(now); idx >= 0 {
		r.buckets[idx].Add(1)
	}
}

func (r *referenceCounter) rollingSum() int64 {
	var ret int64
	for i := range r.buckets {
		ret += r.buckets[i].Get()
	}
	return ret
}

func (r *referenceCounter) reset(now time.Time) {
	r.advance(now)
	for i := range r.buckets {
		r.buckets[i].Set(0)
	}
}

func (r *referenceCounter) json(t *testing.T) string {
	var rolling AtomicInt64
	rolling.Set(r.rollingSum())
	b, err := json.Marshal(jsonCounter{Buckets: r.buckets, RollingSum: &rolling, TotalSum: &r.total, RollingBucket: &r.rb})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// checkCounterMatchesReference interprets ops as Inc/RollingSumAt/Reset/JSON round trip calls at times that wander
// forwards, backwards, out of the window and before the start, comparing all observable state after every step.
func checkCounterMatchesReference(t *testing.T, numBuckets int, width time.Duration, ops []byte) {
	t.Helper()
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	got := NewRollingCounter(width, numBuckets, start)
	want := &referenceCounter{buckets: make([]AtomicInt64, numBuckets)}
	want.rb.NumBuckets, want.rb.BucketWidth, want.rb.StartTime = numBuckets, width, start
	clock := start
	for i := 0; i+1 < len(ops); i += 2 {
		step := time.Duration(int(ops[i+1])-128) * time.Millisecond
		now := clock.Add(step)
		if step > 0 {
			clock = now
		}
		switch op := ops[i] % 16; {
		case op < 11:
			got.Inc(now)
			want.inc(now)
		case op < 13:
			want.advance(now)
			if g, w := got.RollingSumAt(now), want.rollingSum(); g != w {
				t.Fatalf("op %d: RollingSumAt=%d want %d", i, g, w)
			}
		case op == 13:
			got.Reset(now)
			want.reset(now)
		case op == 14:
			b, err := json.Marshal(&got)
			if err != nil {
				t.Fatal(err)
			}
			// Restore over a dirty counter
			got.Inc(now)
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatal(err)
			}
		default:
			// Foreign JSON whose TotalSum has no relation to its buckets
			want.total.Set((int64(ops[i+1]) - 128) << (ops[i] >> 2))
			if numBuckets > 0 {
				want.buckets[0].Set(-int64(ops[i]))
			}
			if err := json.Unmarshal([]byte(want.json(t)), &got); err != nil {
				t.Fatal(err)
			}
		}
		if g, w := got.TotalSum(), want.total.Get(); g != w {
			t.Fatalf("op %d: TotalSum=%d want %d", i, g, w)
		}
		b, err := json.Marshal(&got)
		if err != nil {
			t.Fatal(err)
		}
		if w := want.json(t); string(b) != w {
			t.Fatalf("op %d: JSON\n got %s\nwant %s", i, b, w)
		}
	}
}

func TestCounterMatchesReference(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for i := 0; i < 100; i++ {
		ops := make([]byte, 200)
		r.Read(ops)
		checkCounterMatchesReference(t, r.Intn(6), time.Duration(r.Intn(40))*time.Millisecond, ops)
	}
}

func FuzzCounterMatchesReference(f *testing.F) {
	f.Add(uint8(3), uint8(10), []byte{0, 1, 0, 200, 13, 5, 14, 9, 15, 100, 0, 127, 11, 0})
	f.Add(uint8(0), uint8(0), []byte{0, 1, 14, 0, 15, 3})
	f.Fuzz(func(t *testing.T, numBuckets uint8, width uint8, ops []byte) {
		checkCounterMatchesReference(t, int(numBuckets%12), time.Duration(width%64)*time.Millisecond, ops)
	})
}

func TestZeroCounterJSON(t *testing.T) {
	var zero RollingCounter
	zero.Inc(time.Now())
	b, err := json.Marshal(&zero)
	if err != nil {
		t.Fatal(err)
	}
	want := &referenceCounter{}
	want.total.Set(1)
	if string(b) != want.json(t) {
		t.Fatalf("got %s want %s", b, want.json(t))
	}
}

// TotalSum must be linearizable while buckets roll, events fall outside of the window and Reset runs: never below
// the number of Inc calls that had returned, never above the number that had started, and never moving backwards.
func TestTotalSumExactUnderRollover(t *testing.T) {
	for _, numBuckets := range []int{1, 2, 10} {
		t.Run(fmt.Sprint(numBuckets), func(t *testing.T) {
			const writers, readers, perWriter = 4, 3, 20000
			start := time.Now()
			x := NewRollingCounter(time.Microsecond, numBuckets, start)
			var clock, started, finished atomic.Int64
			var stop atomic.Bool
			var writersDone, others sync.WaitGroup
			for w := 0; w < writers; w++ {
				writersDone.Add(1)
				go func() {
					defer writersDone.Done()
					for i := 0; i < perWriter; i++ {
						// Roughly three events per bucket; every 16th is too old to be bucketed
						now := start.Add(time.Duration(clock.Add(300)))
						if i%16 == 0 {
							now = start.Add(-time.Second)
						}
						started.Add(1)
						x.Inc(now)
						finished.Add(1)
					}
				}()
			}
			for r := 0; r < readers; r++ {
				others.Add(1)
				go func() {
					defer others.Done()
					var prev int64
					for !stop.Load() {
						lo := finished.Load()
						rolling := x.RollingSumAt(start.Add(time.Duration(clock.Load())))
						got := x.TotalSum()
						hi := started.Load()
						if got < lo || got > hi || got < prev || got < rolling {
							t.Errorf("TotalSum=%d: want within [%d, %d], >= previous %d and >= rolling sum %d", got, lo, hi, prev, rolling)
							return
						}
						prev = got
					}
				}()
			}
			others.Add(1)
			go func() {
				defer others.Done()
				for !stop.Load() {
					x.Reset(start.Add(time.Duration(clock.Load())))
					var into struct {
						Buckets              []int64
						RollingSum, TotalSum int64
					}
					b, err := json.Marshal(&x)
					if err == nil {
						err = json.Unmarshal(b, &into)
					}
					if err != nil || into.RollingSum > into.TotalSum || slices.Min(into.Buckets) < 0 {
						t.Errorf("bad concurrent JSON %s (%v)", b, err)
						return
					}
					runtime.Gosched()
				}
			}()
			writersDone.Wait()
			stop.Store(true)
			others.Wait()
			if got := x.TotalSum(); got != writers*perWriter {
				t.Fatalf("TotalSum=%d, want %d", got, writers*perWriter)
			}
		})
	}
}
