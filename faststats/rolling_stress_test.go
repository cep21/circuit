package faststats

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestRollingCounterConcurrency tests that RollingCounter is thread-safe
func TestRollingCounterConcurrency(t *testing.T) {
	numBuckets := 10
	bucketWidth := time.Millisecond * 10

	counter := NewRollingCounter(bucketWidth, numBuckets, time.Now())

	// Set up concurrent increments
	goroutines := 100
	incrementsPerRoutine := 1000

	var wg sync.WaitGroup
	var totalIncrements int64

	// Start multiple goroutines that increment the counter
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for i := 0; i < incrementsPerRoutine; i++ {
				// Use different increment values
				incrementBy := int64(1) // RollingCounter only supports Inc(now), not with a value
				counter.Inc(time.Now())
				atomic.AddInt64(&totalIncrements, incrementBy)

				// Add a small sleep occasionally to allow buckets to roll
				if i%100 == 0 {
					time.Sleep(bucketWidth / 2)
				}
			}
		}()
	}

	wg.Wait()

	// Sleep slightly longer than the entire window to ensure all increments have rolled out
	time.Sleep(bucketWidth * time.Duration(numBuckets+1))

	// The TotalSum is never reset and should match our total increments
	require.Equal(t, totalIncrements, counter.TotalSum())

	t.Logf("Successfully processed %d concurrent increments", totalIncrements)
}

// TestRollingBucketConcurrency tests that RollingBuckets are thread-safe
func TestRollingBucketConcurrency(t *testing.T) {
	// Skip this test since we don't have direct access to bucket functionality
	t.Skip("RollingBucket implementation not directly accessible")
}

// TestRollingPercentileConcurrency tests that RollingPercentile is thread-safe
func TestRollingPercentileConcurrency(t *testing.T) {
	numBuckets := 10
	bucketWidth := time.Millisecond * 10
	bucketSize := 100

	// Default bucket size 100
	percentile := NewRollingPercentile(bucketWidth, numBuckets, bucketSize, time.Now())

	// Set up concurrent adds
	goroutines := 50
	addsPerRoutine := 500

	var wg sync.WaitGroup

	// Start multiple goroutines that add values
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for i := 0; i < addsPerRoutine; i++ {
				// Add a range of values
				value := int64((id*100 + i) % 1000)
				now := time.Now()
				percentile.AddDuration(time.Duration(value), now)

				// Concurrent reads while adding
				if i%10 == 0 {
					// Use Snapshot which returns SortedDurations with proper methods
					snap := percentile.SnapshotAt(now)
					_ = snap.Percentile(50)
					_ = snap.Mean()
					_ = snap.Min() // Max not directly accessible
				}

				// Occasionally sleep to allow buckets to roll
				if i%50 == 0 {
					time.Sleep(bucketWidth / 5)
				}
			}
		}(g)
	}

	// Start additional goroutines that just read percentiles
	readGoroutines := 10
	for g := 0; g < readGoroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Read different percentiles
			percentiles := []float64{50, 90, 95, 99}

			for i := 0; i < 1000; i++ {
				now := time.Now()
				snap := percentile.SnapshotAt(now)
				for _, p := range percentiles {
					_ = snap.Percentile(p)
				}
				_ = snap.Mean()
				_ = snap.Min() // No direct Max method

				time.Sleep(bucketWidth / 10)
			}
		}()
	}

	wg.Wait()

	// Sleep slightly longer than the entire window to ensure all values have rolled out
	time.Sleep(bucketWidth * time.Duration(numBuckets+1))

	// After all buckets have rolled, the percentile should be empty
	// There's no direct Max method, so we'll check if the snapshot is empty
	snap := percentile.Snapshot()
	if len(snap) > 0 {
		t.Errorf("Expected empty snapshot after rollout, got %v entries", len(snap))
	}
}

// TestTimedCheckConcurrency tests that TimedCheck is thread-safe
func TestTimedCheckConcurrency(t *testing.T) {
	// Skip since we don't have direct access to TimedCheck
	t.Skip("TimedCheck not directly accessible for testing")
}

// TestRollingCounterBucketRolloverRace tests for race conditions during bucket rollover
func TestRollingCounterBucketRolloverRace(t *testing.T) {
	// Create a counter with very small buckets for frequent rollovers
	numBuckets := 5
	bucketWidth := time.Millisecond * 5

	counter := NewRollingCounter(bucketWidth, numBuckets, time.Now())

	// Start threads that constantly increment
	goroutines := 30
	duration := time.Millisecond * 300 // Run for 300ms

	var wg sync.WaitGroup
	var running int32 = 1
	var totalAdded int64

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for atomic.LoadInt32(&running) == 1 {
				// Mix of operations
				counter.Inc(time.Now())
				atomic.AddInt64(&totalAdded, 1)

				if counter.TotalSum() < 0 {
					t.Errorf("Counter sum went negative: %d", counter.TotalSum())
				}
			}
		}()
	}

	// Start more threads that read percentiles during rollover
	readThreads := 10
	for g := 0; g < readThreads; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for atomic.LoadInt32(&running) == 1 {
				sum := counter.TotalSum()
				if sum < 0 {
					t.Errorf("Counter sum went negative: %d", sum)
				}

				// Also check rolling sum
				now := time.Now()
				rollingSum := counter.RollingSumAt(now)
				if rollingSum < 0 {
					t.Errorf("Rolling sum went negative: %d", rollingSum)
				}
			}
		}()
	}

	// Let it run for the duration
	time.Sleep(duration)
	atomic.StoreInt32(&running, 0)

	wg.Wait()

	t.Logf("Added %d items during high-frequency rollover test", totalAdded)
}
