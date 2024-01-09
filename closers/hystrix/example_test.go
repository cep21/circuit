package hystrix_test

import (
	"fmt"
	"time"

	"github.com/cep21/circuit/v4"
	"github.com/cep21/circuit/v4/closers/hystrix"
)

// This example configures the circuit to use Hystrix open/close logic with the default Hystrix parameters
func ExampleFactory() {
	configuration := hystrix.Factory{
		// Hystrix open logic is to open the circuit after an % of errors
		ConfigureOpener: hystrix.ConfigureOpener{
			// We change the default to wait for 10 requests, not 20, before checking to close
			RequestVolumeThreshold: 10,
			// The default values match what hystrix does by default
		},
		// Hystrix close logic is to sleep then check
		ConfigureCloser: hystrix.ConfigureCloser{
			// The default values match what hystrix does by default
		},
	}
	h := circuit.Manager{
		// Tell the manager to use this configuration factory whenever it makes a new circuit
		DefaultCircuitProperties: []circuit.CommandPropertiesConstructor{configuration.Configure},
	}
	// This circuit will inherit the configuration from the example
	c := h.MustCreateCircuit("hystrix-circuit")
	fmt.Println("This is a hystrix configured circuit", c.Name())
	// Output: This is a hystrix configured circuit hystrix-circuit
}

// Most configuration properties on [the Hystrix Configuration page](https://github.com/Netflix/Hystrix/wiki/Configuration) that say
// they are modifyable at runtime can be changed on the Circuit in a thread safe way.  Most of the ones that cannot are
// related to stat collection.
//
// This example shows how to update hystrix configuration at runtime.
func ExampleCloser_SetConfigThreadSafe() {
	// Start off using the defaults
	configuration := hystrix.Factory{}
	h := circuit.Manager{
		// Tell the manager to use this configuration factory whenever it makes a new circuit
		DefaultCircuitProperties: []circuit.CommandPropertiesConstructor{configuration.Configure},
	}
	c := h.MustCreateCircuit("hystrix-circuit")
	fmt.Println("The default sleep window", c.OpenToClose.(*hystrix.Closer).Config().SleepWindow)
	// This configuration update function is thread safe.  We can modify this at runtime while the circuit is active
	c.OpenToClose.(*hystrix.Closer).SetConfigThreadSafe(hystrix.ConfigureCloser{
		SleepWindow: time.Second * 3,
	})
	fmt.Println("The new sleep window", c.OpenToClose.(*hystrix.Closer).Config().SleepWindow)
	// Output:
	// The default sleep window 5s
	// The new sleep window 3s
}
