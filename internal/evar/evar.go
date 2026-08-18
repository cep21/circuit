package evar

import (
	"encoding/json"
	"expvar"
)

// ExpvarToVal is a helper to extract the root value() from an expvar.  Vars from the standard library (and
// expvar.Func) expose a Value() method that we prefer.  For any other Var we fall back to its String(), which the
// expvar.Var contract requires to be valid JSON, rather than silently dropping it.
func ExpvarToVal(in expvar.Var) interface{} {
	if in == nil {
		return nil
	}
	type iv interface {
		Value() interface{}
	}
	if rawVal, ok := in.(iv); ok {
		return rawVal.Value()
	}
	asStr := in.String()
	if !json.Valid([]byte(asStr)) {
		return asStr
	}
	return json.RawMessage(asStr)
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
