package circuit

import (
	"strings"
	"testing"
)

func TestManager_Empty(t *testing.T) {
	h := Manager{}
	if h.GetCircuit("does_not_exist") != nil {
		t.Error("found a circuit that does not exist")
	}
}

func TestManager_Var(t *testing.T) {
	h := Manager{}
	c := h.MustCreateCircuit("hello-world", Config{})
	if !strings.Contains(h.Var().String(), "hello-world") {
		t.Error("Var() does not seem to work for hystrix", h.Var())
	}
	if !strings.Contains(c.Var().String(), "hello-world") {
		t.Error("Var() does not seem to work for circuits")
	}
}

func TestSimpleCreate(t *testing.T) {
	h := Manager{}
	c := h.MustCreateCircuit("hello-world", Config{})
	if c.Name() != "hello-world" {
		t.Error("unexpected name")
	}
	c = h.GetCircuit("hello-world")
	if c.Name() != "hello-world" {
		t.Error("unexpected name")
	}
}

func TestDoubleCreate(t *testing.T) {
	h := Manager{}
	h.MustCreateCircuit("hello-world", Config{})
	var foundErr interface{}
	func() {
		defer func() {
			foundErr = recover()
		}()
		h.MustCreateCircuit("hello-world", Config{})
	}()
	if foundErr == nil {
		t.Error("Expect panic when must creating twice")
	}
}
