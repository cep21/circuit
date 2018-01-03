package circuit

import "github.com/cep21/circuit"

var _ circuit.CommandPropertiesConstructor = Config(ConfigureSleepyCloseCheck{}, ConfigureOpenOnErrPercentage{})

// Config creates a circuit configuration constructor that uses hystrix open/close logic
func Config(closeConfig ConfigureSleepyCloseCheck, openConfig ConfigureOpenOnErrPercentage) func(circuitName string) circuit.Config {
	return func(_ string) circuit.Config {
		return circuit.Config{
			General: circuit.GeneralConfig{
				OpenToClosedFactory: SleepyCloseCheckFactory(closeConfig),
				ClosedToOpenFactory: OpenOnErrPercentageFactory(openConfig),
			},
		}
	}
}
