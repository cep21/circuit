package evar

import (
	"expvar"
)

// ExpvarToVal is a helper to extract the root value() from an expvar
func ExpvarToVal(in expvar.Var) interface{} {
	type iv interface {
		Value() interface{}
	}
	if rawVal, ok := in.(iv); ok {
		return rawVal.Value()
	}
	return nil
}

// ForExpvar is a helper to extract the root value() from any interface
func ForExpvar(in interface{}) interface{} {
	type hasVar interface {
		Var() expvar.Var
	}
	if withVar, ok := in.(hasVar); ok {
		return ExpvarToVal(withVar.Var())
	}
	return in
}
