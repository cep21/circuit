// Package hystrix is a Go implementation of Netflix's Hystrix library
package hystrix

import (
	"errors"
	"expvar"
	"sync"
)

// Hystrix manages circuits with unique names
type Hystrix struct {
	DefaultCircuitProperties []func(circuitName string) CommandProperties
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
	for _, prop := range h.DefaultCircuitProperties {
		config.Merge(prop(name))
	}
	_, exists := h.circuitMap[name]
	if exists {
		return nil, errors.New("circuit with that name already exists")
	}
	h.circuitMap[name] = NewCircuitFromConfig(name, config)
	return h.circuitMap[name], nil
}
