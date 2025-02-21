package circuit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestConcurrentExecutions tests the circuit under high concurrency
func TestConcurrentExecutions(t *testing.T) {
	concurrency := 100
	iterations := 1000

	c := NewCircuitFromConfig("concurrent-test", Config{})

	var wg sync.WaitGroup
	var successCount int64
	var failureCount int64

	// Start multiple goroutines to hammer the circuit
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for j := 0; j < iterations; j++ {
				success := j%3 != 0 // Introduce some failures
				err := c.Execute(context.Background(), func(ctx context.Context) error {
					if !success {
						return errors.New("intentional failure")
					}
					return nil
				}, nil)

				if err == nil {
					atomic.AddInt64(&successCount, 1)
				} else {
					atomic.AddInt64(&failureCount, 1)
				}
			}
		}()
	}

	wg.Wait()

	t.Logf("Total executions: %d, Successes: %d, Failures: %d",
		concurrency*iterations, successCount, failureCount)

	// Ensure we get the expected counts
	require.Equal(t, int64(concurrency*iterations), successCount+failureCount,
		"Total executions should match successes + failures")
}

// TestRaceOnConfigChange tests for race conditions when configuration changes during operation
func TestRaceOnConfigChange(t *testing.T) {
	c := NewCircuitFromConfig("config-race-test", Config{})

	var wg sync.WaitGroup
	configChanges := 100
	executions := 1000

	// Goroutine that constantly updates configuration
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < configChanges; i++ {
			// Update various configuration values
			timeout := time.Millisecond * time.Duration(50+i%100)
			c.SetConfigThreadSafe(Config{
				Execution: ExecutionConfig{
					Timeout: timeout,
				},
				Fallback: FallbackConfig{
					MaxConcurrentRequests: int64(10 + i%20),
				},
				Metrics: MetricsCollectors{},
			})
			time.Sleep(time.Millisecond)
		}
	}()

	// Multiple goroutines executing the circuit
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for j := 0; j < executions; j++ {
				_ = c.Execute(context.Background(), func(ctx context.Context) error {
					time.Sleep(time.Millisecond)
					return nil
				}, nil)
			}
		}()
	}

	wg.Wait()
}

// TestCircuitStateTransitionRace tests for race conditions during circuit state transitions
func TestCircuitStateTransitionRace(t *testing.T) {
	// Create a circuit that will open after 20 consecutive failures
	c := NewCircuitFromConfig("state-transition-race", Config{})

	var wg sync.WaitGroup
	goroutines := 100

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start with consistent failures to trigger circuit opening
	for i := 0; i < 50; i++ {
		_ = c.Execute(ctx, func(ctx context.Context) error {
			return errors.New("intentional failure")
		}, nil)
	}

	// Now create multiple goroutines that will hit the circuit as it's changing state
	circuitOpenObserved := int64(0)
	circuitClosedObserved := int64(0)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			// Half will try to trigger success (to close circuit)
			// Half will continue to fail
			shouldFail := id%2 == 0

			for j := 0; j < 100; j++ {
				err := c.Execute(ctx, func(ctx context.Context) error {
					if shouldFail {
						return errors.New("intentional failure")
					}
					time.Sleep(time.Millisecond)
					return nil
				}, nil)

				// Check if circuit is open by using the CircuitOpen method on the err
				if err != nil {
					if cerr, ok := err.(Error); ok && cerr.CircuitOpen() {
						atomic.AddInt64(&circuitOpenObserved, 1)
					}
				} else if err == nil {
					atomic.AddInt64(&circuitClosedObserved, 1)
				}
			}
		}(i)
	}

	wg.Wait()

	t.Logf("Circuit open observed: %d, Circuit closed observed: %d",
		circuitOpenObserved, circuitClosedObserved)
}

// TestContextCancellationStress tests how the circuit handles many context cancellations
func TestContextCancellationStress(t *testing.T) {
	c := NewCircuitFromConfig("context-cancel-test", Config{})

	var wg sync.WaitGroup
	goroutines := 100
	iterations := 100

	timeoutCount := int64(0)
	successCount := int64(0)
	failureCount := int64(0)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for j := 0; j < iterations; j++ {
				// Random timeout between 1-10ms
				timeout := time.Duration(1+j%10) * time.Millisecond
				ctx, cancel := context.WithTimeout(context.Background(), timeout)

				// Function that takes 0-20ms to complete
				err := c.Execute(ctx, func(ctx context.Context) error {
					sleepTime := time.Duration(j%20) * time.Millisecond
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(sleepTime):
						return nil
					}
				}, nil)

				switch {
				case errors.Is(err, context.DeadlineExceeded):
					atomic.AddInt64(&timeoutCount, 1)
				case err != nil:
					atomic.AddInt64(&failureCount, 1)
				default:
					atomic.AddInt64(&successCount, 1)
				}

				cancel() // Always cancel to clean up resources
			}
		}()
	}

	wg.Wait()

	t.Logf("Timeouts: %d, Successes: %d, Failures: %d",
		timeoutCount, successCount, failureCount)
}

// TestFallbackUnderStress tests the fallback mechanism under high concurrency
func TestFallbackUnderStress(t *testing.T) {
	c := NewCircuitFromConfig("fallback-stress-test", Config{
		Fallback: FallbackConfig{
			MaxConcurrentRequests: 10, // Limit fallback concurrency
		},
	})

	var wg sync.WaitGroup
	goroutines := 50
	iterations := 100

	fallbackCount := int64(0)
	fallbackRejectionCount := int64(0)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for j := 0; j < iterations; j++ {
				err := c.Execute(context.Background(),
					// Main function always fails
					func(ctx context.Context) error {
						return errors.New("intentional failure")
					},
					// Fallback function does some work and succeeds
					func(ctx context.Context, err error) error {
						// Small delay to increase contention
						time.Sleep(time.Millisecond * 5)
						return nil
					})

				if err == nil {
					atomic.AddInt64(&fallbackCount, 1)
				} else if strings.Contains(err.Error(), "fallback") {
					// Check for fallback rejection using the error string
					atomic.AddInt64(&fallbackRejectionCount, 1)
				}
			}
		}()
	}

	wg.Wait()

	t.Logf("Fallback successes: %d, Fallback rejections: %d",
		fallbackCount, fallbackRejectionCount)

	// We should have some successful fallbacks
	require.Greater(t, fallbackCount, int64(0))

	// With the concurrency limit, we should also see some rejections
	require.Greater(t, fallbackRejectionCount, int64(0))
}

// TestManyCircuitsStress tests creating and using many circuits simultaneously
func TestManyCircuitsStress(t *testing.T) {
	manager := Manager{}

	circuitCount := 50
	goroutinesPerCircuit := 20
	iterations := 100

	var wg sync.WaitGroup

	// Metrics to track
	var totalExecutions int64
	var circuitOpens int64

	// Create and use many circuits simultaneously
	for c := 0; c < circuitCount; c++ {
		circuitName := fmt.Sprintf("stress-circuit-%d", c)
		_ = manager.MustCreateCircuit(circuitName)

		// Each circuit gets multiple goroutines hitting it
		for g := 0; g < goroutinesPerCircuit; g++ {
			wg.Add(1)
			go func(circuitID, goroutineID int) {
				defer wg.Done()

				localCircuit := manager.GetCircuit(fmt.Sprintf("stress-circuit-%d", circuitID))
				if localCircuit == nil {
					t.Errorf("Failed to get circuit %d", circuitID)
					return
				}

				// Determine if this goroutine causes failures
				causeFailures := goroutineID%4 == 0

				for i := 0; i < iterations; i++ {
					err := localCircuit.Execute(context.Background(), func(ctx context.Context) error {
						if causeFailures {
							return errors.New("intentional failure")
						}
						return nil
					}, nil)

					atomic.AddInt64(&totalExecutions, 1)

					// Check if circuit is open
					if err != nil {
						if cerr, ok := err.(Error); ok && cerr.CircuitOpen() {
							atomic.AddInt64(&circuitOpens, 1)
						}
					}
				}
			}(c, g)
		}
	}

	wg.Wait()

	t.Logf("Total executions across all circuits: %d", totalExecutions)
	t.Logf("Circuit open rejections: %d", circuitOpens)

	require.Equal(t, int64(circuitCount*goroutinesPerCircuit*iterations), totalExecutions)
}

// TestNestedCircuitStress tests nested circuit patterns under concurrency
func TestNestedCircuitStress(t *testing.T) {
	manager := Manager{}

	outerCircuit := manager.MustCreateCircuit("outer")
	innerCircuit := manager.MustCreateCircuit("inner")

	var wg sync.WaitGroup
	goroutines := 50
	iterations := 100

	var successCount int64
	var failureCount int64

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for i := 0; i < iterations; i++ {
				// Determine success/failure pattern
				innerShouldFail := i%5 == 0
				outerShouldFail := i%7 == 0

				err := outerCircuit.Execute(context.Background(), func(outerCtx context.Context) error {
					if outerShouldFail {
						return errors.New("outer circuit failure")
					}

					// Call the inner circuit from within the outer circuit
					return innerCircuit.Execute(outerCtx, func(innerCtx context.Context) error {
						if innerShouldFail {
							return errors.New("inner circuit failure")
						}
						return nil
					}, nil)
				}, nil)

				if err == nil {
					atomic.AddInt64(&successCount, 1)
				} else {
					atomic.AddInt64(&failureCount, 1)
				}
			}
		}()
	}

	wg.Wait()

	t.Logf("Nested circuit executions - Success: %d, Failure: %d",
		successCount, failureCount)

	require.Equal(t, int64(goroutines*iterations), successCount+failureCount)
}
