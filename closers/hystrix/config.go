package hystrix

import "github.com/cep21/hystrix"

var _ hystrix.CommandPropertiesConstructor = Config(ConfigureSleepyCloseCheck{}, ConfigureOpenOnErrPercentage{})

// Config creates a circuit configuration constructor that uses hystrix open/close logic
func Config(closeConfig ConfigureSleepyCloseCheck, openConfig ConfigureOpenOnErrPercentage) func(circuitName string) hystrix.CircuitConfig {
	return func(_ string) hystrix.CircuitConfig {
		return hystrix.CircuitConfig{
			General: hystrix.GeneralConfig{
				OpenToClosedFactory: SleepyCloseCheckFactory(closeConfig),
				ClosedToOpenFactory: OpenOnErrPercentageFactory(openConfig),
			},
		}
	}
}
