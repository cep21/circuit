package hystrix_test

import (
	"fmt"
	"github.com/cep21/circuit/closers/hystrix"
	"github.com/cep21/circuit"
	"time"
)

// This example configures the circuit to use Hystrix open/close logic with the default Hystrix parameters
func ExampleConfigFactory_Configure() {
	configuration := hystrix.ConfigFactory{
		// Hystrix open logic is to open the circuit after an % of errors
		ConfigureOpenOnErrPercentage: hystrix.ConfigureOpenOnErrPercentage{
			// We change the default to wait for 10 requests, not 20, before checking to close
			RequestVolumeThreshold: 10,
			// The default values match what hystrix does by default
		},
		// Hystrix close logic is to sleep then check
		ConfigureSleepyCloseCheck: hystrix.ConfigureSleepyCloseCheck{
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

// This example shows how most circuits can update their configuration at runtime.
func ExampleSleepyCloseCheck_SetConfigThreadSafe() {
	// Start off using the defaults
	configuration := hystrix.ConfigFactory{}
	h := circuit.Manager{
		// Tell the manager to use this configuration factory whenever it makes a new circuit
		DefaultCircuitProperties: []circuit.CommandPropertiesConstructor{configuration.Configure},
	}
	c := h.MustCreateCircuit("hystrix-circuit")
	fmt.Println("The default sleep window", c.OpenToClose.(*hystrix.SleepyCloseCheck).Config().SleepWindow)
	// This configuration update function is thread safe.  We can modify this at runtime while the circuit is active
	c.OpenToClose.(*hystrix.SleepyCloseCheck).SetConfigThreadSafe(hystrix.ConfigureSleepyCloseCheck{
		SleepWindow: time.Second * 3,
	})
	fmt.Println("The new sleep window", c.OpenToClose.(*hystrix.SleepyCloseCheck).Config().SleepWindow)
	// Output:
	// The default sleep window 5s
	// The new sleep window 3s
}