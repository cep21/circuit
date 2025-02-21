package evar

import (
	"expvar"
	"fmt"
	"testing"
	"time"
)

// Mock implementation for testing
type mockExpvar struct {
	val interface{}
}

func (m *mockExpvar) String() string {
	return "mock"
}

func (m *mockExpvar) Value() interface{} {
	return m.val
}

// Mock implementation that has a Var() method
type hasVarType struct {
	v expvar.Var
}

func (h hasVarType) Var() expvar.Var {
	return h.v
}

func TestExpvarToVal(t *testing.T) {
	// Test with a valid expvar implementation
	mock := &mockExpvar{val: 42}
	result := ExpvarToVal(mock)
	if result != 42 {
		t.Errorf("Expected result to be 42, got %v", result)
	}

	// Test with a non-Value-implementing expvar - using unique name to avoid collision
	nonValueVar := expvar.NewString("test_" + t.Name() + "_" + fmt.Sprintf("%d", time.Now().UnixNano()))
	result = ExpvarToVal(nonValueVar)
	if result != nil {
		t.Errorf("Expected result to be nil, got %v", result)
	}
}

func TestForExpvar(t *testing.T) {
	// Test with an object that has Var()
	mock := &mockExpvar{val: "test-value"}
	hasVar := hasVarType{v: mock}
	result := ForExpvar(hasVar)
	if result != "test-value" {
		t.Errorf("Expected result to be 'test-value', got %v", result)
	}

	// Test with a regular value that doesn't implement hasVar
	directValue := "direct-value"
	result = ForExpvar(directValue)
	if result != "direct-value" {
		t.Errorf("Expected result to be 'direct-value', got %v", result)
	}
}
