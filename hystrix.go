package hystrix

import (
	"errors"
	"expvar"
	"sync"
)

type CommandPropertiesConstructor func(circuitName string) CommandProperties

// Hystrix manages circuits with unique names
type Hystrix struct {
	// DefaultCircuitProperties is a list of CommandProperties constructors called, in reverse order,
	// to append or modify configuration for your circuit.
	DefaultCircuitProperties []CommandPropertiesConstructor
	circuitMap               map[string]*Circuit
	mu                       sync.RWMutex
}

// AllCircuits returns every hystrix circuit tracked
func (h *Hystrix) AllCircuits() []*Circuit {
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
func (h *Hystrix) Var() expvar.Var {
	return expvar.Func(func() interface{} {
		h.mu.RLock()
		defer h.mu.RUnlock()
		ret := make(map[string]interface{})
		for k, v := range h.circuitMap {
			ret[k] = v.DebugValues()
		}
		return ret
	})
}

// GetCircuit returns the circuit with a given name, or nil if the circuit does not exist.  You should not call this
// in live code.  Instead, store the circuit somewhere and use the circuit directly.
func (h *Hystrix) GetCircuit(name string) *Circuit {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.circuitMap[name]
}

// MustCreateCircuit calls CreateCircuit, but panics if the circuit name already exists
func (h *Hystrix) MustCreateCircuit(name string, config CommandProperties) *Circuit {
	c, err := h.CreateCircuit(name, config)
	if err != nil {
		panic(err)
	}
	return c
}

// CreateCircuit creates a new circuit, or returns error if a circuit with that name already exists
func (h *Hystrix) CreateCircuit(name string, config CommandProperties) (*Circuit, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.circuitMap == nil {
		h.circuitMap = make(map[string]*Circuit, 5)
	}
	// Merge in reverse order so the most recently appending constructor is more important
	for i := len(h.DefaultCircuitProperties) - 1; i >= 0; i-- {
		config.Merge(h.DefaultCircuitProperties[i](name))
	}
	_, exists := h.circuitMap[name]
	if exists {
		return nil, errors.New("circuit with that name already exists")
	}
	h.circuitMap[name] = NewCircuitFromConfig(name, config)
	return h.circuitMap[name], nil
}
