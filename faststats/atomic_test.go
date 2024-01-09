package faststats

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAtomicInt64(t *testing.T) {
	var x AtomicInt64
	x.Add(1)
	if x.Get() != 1 {
		t.Error("Expect 1 after an add")
	}
	if x.Swap(100) != 1 {
		t.Error("expect 1 back after a swap")
	}
	x.Set(time.Second.Nanoseconds())
	if x.Duration() != time.Second {
		t.Error("expected to get second after a set")
	}
	asBytes, err := json.Marshal(&x)
	if err != nil {
		t.Error("unknown error marshalling", err)
	}
	require.Equal(t, []byte("1000000000"), asBytes)
	var y AtomicInt64
	if err := json.Unmarshal(asBytes, &y); err != nil {
		t.Error("unknown error unmarshalling", err)
	}
	if y.Get() != x.Get() {
		t.Error("Did not JSON encode correctly")
	}
	y.Set(1)
	if y.String() != "1" {
		t.Error("String value inconsistent")
	}
}

func TestAtomicBoolean(t *testing.T) {
	var b AtomicBoolean
	b.Set(true)
	if !b.Get() {
		t.Error("Could not set")
	}
	if b.String() != "true" {
		t.Error("Could not convert to string")
	}
	asBytes, err := json.Marshal(&b)
	if err != nil {
		t.Error("Could not json marshal")
	}
	require.Equal(t, []byte("true"), asBytes)
	var c AtomicBoolean
	if err := json.Unmarshal(asBytes, &c); err != nil {
		t.Error("Could not unmarshal")
	}
	if !c.Get() {
		t.Error("Value not stored in correctly")
	}
}
