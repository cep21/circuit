package circuit

import (
	"expvar"
	"runtime"
	"testing"
	"time"
)

// collectorThatReadsManager is a collector made by a DefaultCircuitProperties constructor: it only knows the circuit's
// name, so it looks the circuit up when asked for its expvar value.
type collectorThatReadsManager struct {
	neverOpens
	value func() interface{}
}

func (c *collectorThatReadsManager) Var() expvar.Var { return expvar.Func(c.value) }

// Manager.Var must not hold the manager's lock while it runs user code.  A collector that reads from the manager
// would take the read lock recursively, which deadlocks for good if a CreateCircuit arrives in between.
func TestManagerVarDoesNotHoldLockOverCollectors(t *testing.T) {
	m := &Manager{}
	created := make(chan struct{})
	collector := &collectorThatReadsManager{}
	collector.value = func() interface{} {
		go func() {
			m.MustCreateCircuit("second")
			close(created)
		}()
		// Wait until the create either finished or is queued on the write lock (TryRLock fails once a writer waits)
		for waiting := true; waiting; {
			select {
			case <-created:
				waiting = false
			default:
				if waiting = m.mu.TryRLock(); waiting {
					m.mu.RUnlock()
					runtime.Gosched()
				}
			}
		}
		return m.GetCircuit("first").IsOpen()
	}
	m.MustCreateCircuit("first", Config{Metrics: MetricsCollectors{Run: []RunMetrics{collector}}})

	done := make(chan string, 1)
	go func() { done <- m.Var().String() }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Manager.Var deadlocked against a concurrent CreateCircuit")
	}
	<-created
}
