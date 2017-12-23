package hystrix

import (
	"strings"
	"testing"
)

func TestHystrix_Empty(t *testing.T) {
	h := Hystrix{}
	if h.GetCircuit("does_not_exist") != nil {
		t.Error("found a circuit that does not exist")
	}
}

func TestHystrix_Var(t *testing.T) {
	h := Hystrix{}
	c := h.MustCreateCircuit("hello-world", CommandProperties{})
	if !strings.Contains(h.Var().String(), "hello-world") {
		t.Error("Var() does not seem to work for hystrix", h.Var())
	}
	if !strings.Contains(c.Var().String(), "hello-world") {
		t.Error("Var() does not seem to work for circuits")
	}
}

func TestSimpleCreate(t *testing.T) {
	h := Hystrix{}
	c := h.MustCreateCircuit("hello-world", CommandProperties{})
	if c.Name() != "hello-world" {
		t.Error("unexpeted name")
	}
	c = h.GetCircuit("hello-world")
	if c.Name() != "hello-world" {
		t.Error("unexpeted name")
	}
}

func TestDoubleCreate(t *testing.T) {
	h := Hystrix{}
	h.MustCreateCircuit("hello-world", CommandProperties{})
	var foundErr interface{}
	func() {
		defer func() {
			foundErr = recover()
		}()
		h.MustCreateCircuit("hello-world", CommandProperties{})
	}()
	if foundErr == nil {
		t.Error("Expect panic when must creating twice")
	}
}
