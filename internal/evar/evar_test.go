package evar

import (
	"encoding/json"
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

	// A Var without a `Value() interface{}` method is preserved as its (JSON) String() rather than dropped
	nonValueVar := expvar.NewString("test_" + t.Name() + "_" + fmt.Sprintf("%d", time.Now().UnixNano()))
	nonValueVar.Set("hello")
	result = ExpvarToVal(nonValueVar)
	raw, ok := result.(json.RawMessage)
	if !ok || string(raw) != `"hello"` {
		t.Errorf("Expected raw JSON \"hello\", got %T %v", result, result)
	}
	asMap := expvar.NewMap("map_" + t.Name() + "_" + fmt.Sprintf("%d", time.Now().UnixNano()))
	asMap.Add("k", 3)
	if raw, ok := ExpvarToVal(asMap).(json.RawMessage); !ok || string(raw) != `{"k": 3}` {
		t.Errorf("Expected map JSON to be preserved, got %s", raw)
	}

	if ExpvarToVal(nil) != nil {
		t.Error("Expected nil for nil Var")
	}

	// A misbehaving Var whose String() is not JSON is passed through as a plain string
	if s, ok := ExpvarToVal(notJSONVar{}).(string); !ok || s != "not json" {
		t.Errorf("Expected plain string fallback, got %T %v", ExpvarToVal(notJSONVar{}), ExpvarToVal(notJSONVar{}))
	}
}

type notJSONVar struct{}

func (notJSONVar) String() string { return "not json" }

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
