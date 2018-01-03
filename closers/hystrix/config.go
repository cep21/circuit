package circuit

import "github.com/cep21/circuit"

// ConfigFactory aids making hystrix circuit logic
type ConfigFactory struct {
	ConfigureSleepyCloseCheck    ConfigureSleepyCloseCheck
	ConfigureOpenOnErrPercentage ConfigureOpenOnErrPercentage
}

// Configure creates a circuit configuration constructor that uses hystrix open/close logic
func (c *ConfigFactory) Configure(_ string) circuit.Config {
	return circuit.Config{
		General: circuit.GeneralConfig{
			OpenToClosedFactory: SleepyCloseCheckFactory(c.ConfigureSleepyCloseCheck),
			ClosedToOpenFactory: OpenOnErrPercentageFactory(c.ConfigureOpenOnErrPercentage),
		},
	}
}
