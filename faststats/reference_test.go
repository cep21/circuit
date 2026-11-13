package faststats

import (
	"math"
	"math/rand"
	"slices"
	"testing"
	"time"
)

// referenceAdvance is the straightforward, division based, single threaded definition of RollingBuckets.Advance.
// The optimized implementation must be indistinguishable from it for *any* value of the exported fields.
func referenceAdvance(r *RollingBuckets, now time.Time, clearBucket func(int)) int {
	if r.NumBuckets <= 0 || r.BucketWidth <= 0 {
		return -1
	}
	diff := now.Sub(r.StartTime)
	if diff < 0 {
		return -1
	}
	n := int64(r.NumBuckets)
	absIndex := diff.Nanoseconds() / r.BucketWidth.Nanoseconds()
	bucket := int(absIndex % n)
	last := r.LastAbsIndex.Get()
	if behind := last - absIndex; behind >= 0 {
		if behind >= n {
			return -1
		}
		return bucket
	}
	if absIndex <= last {
		if last-absIndex >= n {
			return -1
		}
		return bucket
	}
	for i := last + 1; i <= absIndex && i <= last+n; i++ {
		clearBucket(int(i % n))
	}
	r.LastAbsIndex.Set(absIndex)
	return bucket
}

// checkAdvanceMatchesReference runs one Advance on both implementations from identical state
func checkAdvanceMatchesReference(t *testing.T, start time.Time, numBuckets int, width time.Duration, last int64, offset time.Duration) {
	t.Helper()
	var got, want RollingBuckets
	for _, rb := range []*RollingBuckets{&got, &want} {
		rb.NumBuckets, rb.StartTime, rb.BucketWidth = numBuckets, start, width
		rb.LastAbsIndex.Set(last)
	}
	// Absurd NumBuckets are interesting for the arithmetic, but bail out rather than clear billions of buckets
	const maxClears = 64
	type bailed struct{}
	run := func(advance func(func(int)) int) (idx int, cleared []int) {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(bailed); !ok {
					panic(r)
				}
				idx = -2
			}
		}()
		idx = advance(func(i int) {
			if cleared = append(cleared, i); len(cleared) >= maxClears {
				panic(bailed{})
			}
		})
		return idx, cleared
	}
	now := start.Add(offset)
	gotIdx, gotCleared := run(func(f func(int)) int { return got.Advance(now, f) })
	wantIdx, wantCleared := run(func(f func(int)) int { return referenceAdvance(&want, now, f) })
	if gotIdx != wantIdx || !slices.Equal(gotCleared, wantCleared) || got.LastAbsIndex.Get() != want.LastAbsIndex.Get() {
		t.Fatalf("n=%d width=%d last=%d offset=%d: got idx=%d cleared=%v last=%d, want idx=%d cleared=%v last=%d",
			numBuckets, width, last, offset,
			gotIdx, gotCleared, got.LastAbsIndex.Get(), wantIdx, wantCleared, want.LastAbsIndex.Get())
	}
}

var edgeInt64s = []int64{
	math.MinInt64, math.MinInt64 + 1, -1 << 32, -1<<32 + 1, -1 << 31, -11, -10, -2, -1, 0, 1, 2, 3, 9, 10, 11, 999999999,
	1000000000, 1000000001, 1<<31 - 1, 1 << 31, 1<<31 + 1, 1<<32 - 1, 1 << 32, 1<<32 + 1, 1 << 33, 3037000499,
	3037000500, 1<<62 - 1, 1 << 62, 1<<62 + 1, math.MaxInt64 / 10, math.MaxInt64/10 + 1, math.MaxInt64 - 1, math.MaxInt64,
}

func TestAdvanceMatchesReference_Edges(t *testing.T) {
	for _, start := range []time.Time{time.Now(), time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)} {
		for _, n := range []int{-1, 0, 1, 2, 3, 10, 1 << 16, math.MaxInt32, math.MaxInt} {
			for _, width := range edgeInt64s {
				for _, last := range edgeInt64s {
					for _, offset := range edgeInt64s {
						checkAdvanceMatchesReference(t, start, n, time.Duration(width), last, time.Duration(offset))
					}
					// Both sides of both edges of bucket `last` (including where last*width overflows and wraps)
					for _, d := range []int64{-1, 0, 1, width - 1, width, width + 1} {
						checkAdvanceMatchesReference(t, start, n, time.Duration(width), last, time.Duration(last*width+d))
					}
				}
			}
		}
	}
}

func TestAdvanceMatchesReference_Random(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	start := time.Now()
	magnitude := func() int64 { return r.Int63() >> uint(r.Intn(63)) }
	for i := 0; i < 200000; i++ {
		n, width := r.Intn(20)+1, magnitude()+1
		offset := magnitude()
		// Keep last close to the true index, where all of the interesting branches are
		last := offset/width + int64(r.Intn(2*n+5)-n-2)
		checkAdvanceMatchesReference(t, start, n, time.Duration(width), last, time.Duration(offset))
	}
}

func FuzzAdvanceMatchesReference(f *testing.F) {
	f.Add(int32(10), int64(time.Second), int64(259200), int64(259200*time.Second))
	f.Add(int32(6), int64(1), int64(1<<32), int64(1<<32))
	f.Add(int32(4), int64(1<<32), int64(1<<32), int64(5))
	f.Add(int32(4), int64(1), int64(-1), int64(0))
	start := time.Now()
	f.Fuzz(func(t *testing.T, n int32, width int64, last int64, offset int64) {
		checkAdvanceMatchesReference(t, start, int(n), time.Duration(width), last, time.Duration(offset))
		if width > 0 {
			checkAdvanceMatchesReference(t, start, int(n), time.Duration(width), offset/width+last%4, time.Duration(offset))
		}
	})
}

func TestMod(t *testing.T) {
	for _, a := range edgeInt64s {
		for _, n := range edgeInt64s {
			if n > 0 && mod(a, n) != a%n {
				t.Errorf("mod(%d, %d) = %d, want %d", a, n, mod(a, n), a%n)
			}
		}
	}
}

// A bucket keeps exactly the most recent len(bucket) samples, whatever the magnitude of its running index
func TestDurationsBucketWrapsAtAnyIndex(t *testing.T) {
	for _, size := range []int{1, 3, 100} {
		for _, first := range []int64{0, 1, 99, 1<<31 - 150, 1<<32 - 150, 1 << 40, math.MaxInt64 - 400} {
			b := newDurationsBucket(size)
			b.currentIndex.Set(first)
			want := make([]int64, size)
			for i := int64(0); i < 300; i++ {
				b.addDuration(time.Duration(i))
				want[(first+i)%int64(size)] = encodeDuration(time.Duration(i))
			}
			for i := range want {
				if got := b.durationsSomeInvalid[i].Get(); got != want[i] {
					t.Fatalf("size=%d first=%d: slot %d = %d, want %d", size, first, i, got, want[i])
				}
			}
		}
	}
}
