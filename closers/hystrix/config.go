package hystrix

import "github.com/cep21/circuit/v4"

// Factory aids making hystrix circuit logic
type Factory struct {
	ConfigureCloser       ConfigureCloser
	ConfigureOpener       ConfigureOpener
	CreateConfigureCloser []func(circuitName string) ConfigureCloser
	CreateConfigureOpener []func(circuitName string) ConfigureOpener
}

// Configure creates a circuit configuration constructor that uses hystrix open/close logic
func (c *Factory) Configure(circuitName string) circuit.Config {
	return circuit.Config{
		General: circuit.GeneralConfig{
			OpenToClosedFactory: c.createCloser(circuitName),
			ClosedToOpenFactory: c.createOpener(circuitName),
		},
	}
}

func (c *Factory) createCloser(circuitName string) func() circuit.OpenToClosed {
	finalConfig := ConfigureCloser{}
	// Merge in reverse order so the most recently appending constructor is more important
	for i := len(c.CreateConfigureCloser) - 1; i >= 0; i-- {
		finalConfig.Merge(c.CreateConfigureCloser[i](circuitName))
	}
	finalConfig.Merge(c.ConfigureCloser)
	return CloserFactory(finalConfig)
}

func (c *Factory) createOpener(circuitName string) func() circuit.ClosedToOpen {
	finalConfig := ConfigureOpener{}
	// Merge in reverse order so the most recently appending constructor is more important
	for i := len(c.CreateConfigureOpener) - 1; i >= 0; i-- {
		finalConfig.Merge(c.CreateConfigureOpener[i](circuitName))
	}
	finalConfig.Merge(c.ConfigureOpener)
	return OpenerFactory(finalConfig)
}
