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

func TestRollingBuckets_ZeroBucketWidth(t *testing.T) {
	now := time.Now()
	x := RollingBuckets{
		NumBuckets:  5,
		StartTime:   now,
		BucketWidth: 0,
	}
	// Should not panic with division by zero
	if nextBucket := x.Advance(now, nil); nextBucket != -1 {
		t.Fatalf("Expected -1 for zero BucketWidth, got %d", nextBucket)
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

	// Bug 3: backward time beyond window should return -1
	if nextBucket := x.Advance(now.Add(time.Second*1), clearIgnore); nextBucket != -1 {
		t.Fatalf("Expected -1 for backward time beyond window, got %d", nextBucket)
	}
}
