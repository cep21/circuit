package faststats

import (
	"strings"
	"testing"
	"time"
)

func TestRollingBuckets_String(t *testing.T) {
	x := RollingBuckets{
		NumBuckets: 101,
	}
	if !strings.Contains(x.String(), "101") {
		t.Fatal("expected string 101 in output")
	}
}

func TestRollingBuckets_Advance(t *testing.T) {
	now := time.Now()
	x := RollingBuckets{
		NumBuckets:  5,
		StartTime:   now,
		BucketWidth: time.Second,
	}
	clearBucketCount := 0
	clearIgnore := func(_ int) {
		clearBucketCount++
	}
	if nextBucket := x.Advance(now.Add(-time.Second), nil); nextBucket != -1 {
		t.Fatal("expected negative bucket in the past")
	}

	// advance 1 forward
	if nextBucket := x.Advance(now.Add(time.Second*1), clearIgnore); nextBucket != 1 {
		t.Fatalf("Should get to bucket 1, not %d", nextBucket)
	}

	// advance 10 forward, then backwards should be negative again
	if nextBucket := x.Advance(now.Add(time.Second*10), clearIgnore); nextBucket != 0 {
		t.Fatal("bucket zero at index 10")
	}

	if clearBucketCount != 6 {
		t.Fatalf("Expect 6 clears, not %d", clearBucketCount)
	}
}
