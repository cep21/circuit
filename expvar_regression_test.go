package circuit_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/cep21/circuit/v4"
	"github.com/cep21/circuit/v4/closers/hystrix"
	"github.com/stretchr/testify/require"
)

// Perfectly valid open/close logic that encoding/json cannot represent (func fields)
type openerWithClock struct {
	circuit.ClosedToOpen
	Now func() time.Time
}

type closerWithHook struct {
	circuit.OpenToClosed
	OnProbe func()
}

// Has its own encoding, which fails
type openerWithBadMarshal struct {
	circuit.ClosedToOpen
}

func (openerWithBadMarshal) MarshalJSON() ([]byte, error) { return nil, errors.New("cannot encode") }

// A failing MarshalJSON must not take the other circuits' output down with it.
func TestVarWithCustomLogicWhoseMarshalJSONFails(t *testing.T) {
	var m circuit.Manager
	m.MustCreateCircuit("healthy")
	m.MustCreateCircuit("custom", circuit.Config{
		General: circuit.GeneralConfig{
			ClosedToOpenFactory: func() circuit.ClosedToOpen {
				return openerWithBadMarshal{ClosedToOpen: hystrix.OpenerFactory(hystrix.ConfigureOpener{})()}
			},
		},
	})
	var all map[string]struct {
		Opener json.RawMessage `json:"opener"`
	}
	out := m.Var().String()
	require.NoError(t, json.Unmarshal([]byte(out), &all), "Manager.Var is not JSON: %q", out)
	require.Contains(t, all, "healthy")
	require.JSONEq(t, `"circuit_test.openerWithBadMarshal"`, string(all["custom"].Opener))
}

// Nothing requires ClosedToOpen/OpenToClosed implementations to be JSON encodable.  expvar.Func swallows the encoding
// error and renders "", which makes the entire /debug/vars document (not just this circuit) invalid JSON.
func TestVarWithCustomLogicThatIsNotJSON(t *testing.T) {
	var m circuit.Manager
	m.MustCreateCircuit("hystrix", (&hystrix.Factory{}).Configure("hystrix"))
	c := m.MustCreateCircuit("custom", circuit.Config{
		General: circuit.GeneralConfig{
			ClosedToOpenFactory: func() circuit.ClosedToOpen {
				return &openerWithClock{ClosedToOpen: hystrix.OpenerFactory(hystrix.ConfigureOpener{})(), Now: time.Now}
			},
			OpenToClosedFactory: func() circuit.OpenToClosed {
				return &closerWithHook{OpenToClosed: hystrix.CloserFactory(hystrix.ConfigureCloser{})(), OnProbe: func() {}}
			},
		},
	})
	require.True(t, json.Valid([]byte(c.Var().String())), "Circuit.Var is not JSON: %q", c.Var().String())

	var all map[string]struct {
		Name   string          `json:"name"`
		Opener json.RawMessage `json:"opener"`
		Closer json.RawMessage `json:"closer"`
	}
	out := m.Var().String()
	require.NoError(t, json.Unmarshal([]byte(out), &all), "Manager.Var is not JSON: %q", out)
	require.Equal(t, "custom", all["custom"].Name)
	require.JSONEq(t, `"*circuit_test.openerWithClock"`, string(all["custom"].Opener))
	require.JSONEq(t, `"*circuit_test.closerWithHook"`, string(all["custom"].Closer))
	// Logic that can be encoded is rendered as before
	require.Contains(t, string(all["hystrix"].Opener), `"err_%"`)
	require.Contains(t, string(all["hystrix"].Closer), `"concurrentSuccessfulAttempts"`)
}
