package hystrix

import (
	"errors"
	"expvar"
	"sync"
)

// CommandPropertiesConstructor is a generic function that can create command properties to configure a circuit by name
// It is safe to leave not configured properties their empty value.
type CommandPropertiesConstructor func(circuitName string) CircuitConfig

// Manager manages circuits with unique names
type Manager struct {
	// DefaultCircuitProperties is a list of CircuitConfig constructors called, in reverse order,
	// to append or modify configuration for your circuit.
	DefaultCircuitProperties []CommandPropertiesConstructor

	circuitMap map[string]*Circuit
	// mu locks circuitMap, not DefaultCircuitProperties
	mu sync.RWMutex
}

// AllCircuits returns every hystrix circuit tracked
func (h *Manager) AllCircuits() []*Circuit {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	ret := make([]*Circuit, 0, len(h.circuitMap))
	for _, c := range h.circuitMap {
		ret = append(ret, c)
	}
	return ret
}

// Var allows you to expose all your hystrix circuits on expvar
func (h *Manager) Var() expvar.Var {
	return expvar.Func(func() interface{} {
		h.mu.RLock()
		defer h.mu.RUnlock()
		ret := make(map[string]interface{})
		for k, v := range h.circuitMap {
			ev := expvarToVal(v.Var())
			if ev != nil {
				ret[k] = ev
			}
		}
		return ret
	})
}

// GetCircuit returns the circuit with a given name, or nil if the circuit does not exist.  You should not call this
// in live code.  Instead, store the circuit somewhere and use the circuit directly.
func (h *Manager) GetCircuit(name string) *Circuit {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.circuitMap[name]
}

// MustCreateCircuit calls CreateCircuit, but panics if the circuit name already exists
func (h *Manager) MustCreateCircuit(name string, config ...CircuitConfig) *Circuit {
	c, err := h.CreateCircuit(name, config...)
	if err != nil {
		panic(err)
	}
	return c
}

// CreateCircuit creates a new circuit, or returns error if a circuit with that name already exists
func (h *Manager) CreateCircuit(name string, configs ...CircuitConfig) (*Circuit, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.circuitMap == nil {
		h.circuitMap = make(map[string]*Circuit, 5)
	}
	finalConfig := CircuitConfig{}
	for _, c := range configs {
		finalConfig.Merge(c)
	}
	// Merge in reverse order so the most recently appending constructor is more important
	for i := len(h.DefaultCircuitProperties) - 1; i >= 0; i-- {
		finalConfig.Merge(h.DefaultCircuitProperties[i](name))
	}
	_, exists := h.circuitMap[name]
	if exists {
		return nil, errors.New("circuit with that name already exists")
	}
	h.circuitMap[name] = NewCircuitFromConfig(name, finalConfig)
	return h.circuitMap[name], nil
}
