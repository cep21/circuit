package circuit

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestManagerCircuitCreationStress tests high-concurrency circuit creation
func TestManagerCircuitCreationStress(t *testing.T) {
	mgr := Manager{}

	// Create many circuits concurrently to test race conditions in creation
	goroutines := 100
	circuitsPerRoutine := 50

	var wg sync.WaitGroup
	uniqueCircuits := make(map[string]struct{})
	var mu sync.Mutex

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(routineNum int) {
			defer wg.Done()

			for i := 0; i < circuitsPerRoutine; i++ {
				// Some goroutines will try to create the same circuit names
				circuitNum := i % (circuitsPerRoutine / 2)
				circuitName := fmt.Sprintf("stress-circuit-%d", routineNum*1000+circuitNum)

				// Try both creation methods
				if i%2 == 0 {
					c, err := mgr.CreateCircuit(circuitName)
					if err == nil && c != nil {
						mu.Lock()
						uniqueCircuits[circuitName] = struct{}{}
						mu.Unlock()
					}
				} else if mgr.GetCircuit(circuitName) == nil {
					// We need to check if the circuit exists first
					// Doesn't exist yet, so create it
					func() {
						defer func() {
							// Recover from any panics
							_ = recover()
						}()
						c := mgr.MustCreateCircuit(circuitName)
						if c != nil {
							mu.Lock()
							uniqueCircuits[circuitName] = struct{}{}
							mu.Unlock()
						}
					}()
				}

				// Verify we can get the circuit
				c := mgr.GetCircuit(circuitName)
				require.NotNil(t, c)
			}
		}(g)
	}

	wg.Wait()

	t.Logf("Created %d unique circuits", len(uniqueCircuits))

	// Verify all circuits can be used
	for name := range uniqueCircuits {
		c := mgr.GetCircuit(name)
		require.NotNil(t, c)

		err := c.Execute(context.Background(), func(ctx context.Context) error {
			return nil
		}, nil)
		require.NoError(t, err)
	}
}

// TestManagerConcurrentCircuitAccess tests concurrent access to the same circuits
func TestManagerConcurrentCircuitAccess(t *testing.T) {
	mgr := Manager{}

	// Create a fixed number of circuits
	circuitCount := 20
	for i := 0; i < circuitCount; i++ {
		mgr.MustCreateCircuit(fmt.Sprintf("shared-circuit-%d", i))
	}

	// Access them concurrently
	goroutines := 50
	operationsPerRoutine := 1000

	var wg sync.WaitGroup
	var operationCounter int64

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for i := 0; i < operationsPerRoutine; i++ {
				// Randomly access one of the circuits
				circuitNum := i % circuitCount
				circuitName := fmt.Sprintf("shared-circuit-%d", circuitNum)

				c := mgr.GetCircuit(circuitName)
				require.NotNil(t, c)

				// Perform an operation
				err := c.Execute(context.Background(), func(ctx context.Context) error {
					// Mix success and failure
					if i%7 == 0 {
						return errors.New("random failure")
					}
					return nil
				}, nil)

				// Don't care about the error, just that we performed the operation
				atomic.AddInt64(&operationCounter, 1)
				_ = err

				if i%11 == 0 {
					// Occasionally check if exists
					exists := mgr.GetCircuit(circuitName) != nil
					require.True(t, exists)

					// Also try a non-existent circuit
					exists = mgr.GetCircuit("non-existent-circuit") != nil
					require.False(t, exists)
				}
			}
		}()
	}

	wg.Wait()

	t.Logf("Performed %d operations on %d shared circuits", operationCounter, circuitCount)
	require.Equal(t, int64(goroutines*operationsPerRoutine), operationCounter)
}

// TestManagerCircuitRunningMetrics tests that the manager correctly tracks running metrics
func TestManagerCircuitRunningMetrics(t *testing.T) {
	mgr := Manager{}

	// Create some circuits
	circuitCount := 5
	for i := 0; i < circuitCount; i++ {
		mgr.MustCreateCircuit(fmt.Sprintf("metric-circuit-%d", i))
	}

	// Update metrics from multiple goroutines
	goroutines := 20
	updatesPerRoutine := 100

	var wg sync.WaitGroup

	// Run many goroutines that will put load on the circuits
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for i := 0; i < updatesPerRoutine; i++ {
				// Cycle through circuits
				circuitNum := i % circuitCount
				circuitName := fmt.Sprintf("metric-circuit-%d", circuitNum)

				c := mgr.GetCircuit(circuitName)

				// Different goroutines will hit circuits with different patterns
				var err error
				switch id % 4 {
				case 0:
					// Always successful
					err = c.Execute(context.Background(), func(ctx context.Context) error {
						return nil
					}, nil)
				case 1:
					// Always error
					err = c.Execute(context.Background(), func(ctx context.Context) error {
						return errors.New("failure")
					}, nil)
				case 2:
					// Slow execution (possible timeout)
					ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*5)
					err = c.Execute(ctx, func(ctx context.Context) error {
						select {
						case <-time.After(time.Millisecond * 10):
							return nil
						case <-ctx.Done():
							return ctx.Err()
						}
					}, nil)
					cancel()
				case 3:
					// Mix of success/failure
					shouldFail := i%2 == 0
					err = c.Execute(context.Background(), func(ctx context.Context) error {
						if shouldFail {
							return errors.New("conditional failure")
						}
						return nil
					}, nil)
				}

				// Don't assert on err, we're just generating metrics
				_ = err
			}
		}(g)
	}

	wg.Wait()

	// Get the manager's metrics - just validate it works
	metrics := mgr.Var()
	require.NotNil(t, metrics)
}

// TestManagerConcurrentFactoryConfiguration tests the manager's handling of
// circuit creation with concurrent configuration factories
func TestManagerConcurrentFactoryConfiguration(t *testing.T) {
	// Create factories that vary the configuration
	factoryCount := 5
	factories := make([]CommandPropertiesConstructor, factoryCount)

	for i := 0; i < factoryCount; i++ {
		timeoutValue := time.Millisecond * time.Duration(20*(i+1))
		factories[i] = func(circuitName string) Config {
			return Config{
				Execution: ExecutionConfig{
					Timeout: timeoutValue,
				},
			}
		}
	}

	mgr := Manager{
		DefaultCircuitProperties: factories,
	}

	// Create circuits concurrently
	goroutines := 30
	circuitsPerRoutine := 10

	var wg sync.WaitGroup
	var mu sync.Mutex
	circuitTimeouts := make(map[string]time.Duration)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for i := 0; i < circuitsPerRoutine; i++ {
				circuitName := fmt.Sprintf("factory-circuit-%d-%d", id, i)

				// Create the circuit - this should apply all factories
				c := mgr.MustCreateCircuit(circuitName)

				// Check its timeout
				timeout := c.Config().Execution.Timeout
				mu.Lock()
				circuitTimeouts[circuitName] = timeout
				mu.Unlock()

				// Execute it
				ctx, cancel := context.WithTimeout(context.Background(), timeout*2)
				err := c.Execute(ctx, func(ctx context.Context) error {
					sleepTime := timeout / 2 // Should finish in time
					time.Sleep(sleepTime)
					return nil
				}, nil)
				cancel()

				require.NoError(t, err)
			}
		}(g)
	}

	wg.Wait()

	t.Logf("Created %d circuits with factories", len(circuitTimeouts))
	require.Equal(t, goroutines*circuitsPerRoutine, len(circuitTimeouts))
}

// TestRaceOnParallelCircuitControlPlane tests for race conditions
// from goroutines performing control plane operations like circuit creation and deletion
// while other goroutines perform data plane operations
func TestRaceOnParallelCircuitControlPlane(t *testing.T) {
	mgr := Manager{}

	// Use atomic operations to coordinate goroutines
	var controlPlaneOps, dataPlaneOps int64
	var running int32 = 1

	// Control plane goroutine that creates circuits
	go func() {
		for atomic.LoadInt32(&running) == 1 {
			// Create circuit
			circuitName := fmt.Sprintf("test-circuit-%d", atomic.LoadInt64(&controlPlaneOps))
			_, _ = mgr.CreateCircuit(circuitName)

			// Let it be used for a bit
			time.Sleep(time.Microsecond)

			// We don't have a delete API, but we can stop using this circuit
			// and create new ones to stress the manager

			atomic.AddInt64(&controlPlaneOps, 1)
		}
	}()

	// Data plane goroutines
	dataPlaneCount := 5
	var wg sync.WaitGroup

	for i := 0; i < dataPlaneCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for atomic.LoadInt32(&running) == 1 {
				// Get all circuits
				allCircuits := mgr.AllCircuits()

				// Try to use each one
				for _, c := range allCircuits {
					// Just try to use it, don't care about result
					_ = c.Execute(context.Background(), func(ctx context.Context) error {
						return nil
					}, nil)
				}

				// Check if a specific circuit exists
				circuitNum := atomic.LoadInt64(&dataPlaneOps) % 100
				circuitName := fmt.Sprintf("test-circuit-%d", circuitNum)
				c := mgr.GetCircuit(circuitName)
				if c != nil {
					// Try to use it
					_ = c.Execute(context.Background(), func(ctx context.Context) error {
						return nil
					}, nil)
				}

				atomic.AddInt64(&dataPlaneOps, 1)
			}
		}()
	}

	// Let it run for a short time
	time.Sleep(time.Millisecond * 500)
	atomic.StoreInt32(&running, 0)

	wg.Wait()

	t.Logf("Performed %d control plane operations and %d data plane operations",
		atomic.LoadInt64(&controlPlaneOps), atomic.LoadInt64(&dataPlaneOps))
}
