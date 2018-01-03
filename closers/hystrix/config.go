package circuit

import "github.com/cep21/circuit"

type ConfigFactory struct {
	ConfigureSleepyCloseCheck    ConfigureSleepyCloseCheck
	ConfigureOpenOnErrPercentage ConfigureOpenOnErrPercentage
}

// Configure creates a circuit configuration constructor that uses hystrix open/close logic
func (c *ConfigFactory) Configure(circuitName string) circuit.Config {
	return circuit.Config{
		General: circuit.GeneralConfig{
			OpenToClosedFactory: SleepyCloseCheckFactory(c.ConfigureSleepyCloseCheck),
			ClosedToOpenFactory: OpenOnErrPercentageFactory(c.ConfigureOpenOnErrPercentage),
		},
	}
}
