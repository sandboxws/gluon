package eval

import (
	"go/constant"
	"go/types"
	"math"

	"github.com/sandboxws/gluon/internal/gluonrt"
)

// constResult renders a compile-time constant without building or running
// anything, which takes an expression like 1+2 or len("héllo") from ~270ms to
// roughly nothing.
//
// The value is converted to a real Go value of the constant's default type and
// handed to the same encoder the child process uses, so the answer is
// byte-identical to the one a real run would have produced. That matters more
// than it sounds: 'a' is an untyped rune whose default type is int32, and the
// encoder prints int32 as "97 'a'" rather than "97".
//
// It reports false whenever it cannot promise that identity, and the caller
// then builds as usual.
func constResult(t types.Type, v constant.Value) (string, bool) {
	// An untyped constant takes its default type when it crosses into a
	// parameter of type any, exactly as it would at run time.
	b, ok := types.Default(t).(*types.Basic)
	if !ok {
		// A named type such as time.Duration has its own String method, so the
		// encoder would print "1ns" where the constant says 1. Only
		// predeclared basic types are safe to answer from here.
		return "", false
	}
	g, ok := goValue(b.Kind(), v)
	if !ok {
		return "", false
	}
	return gluonrt.Payload(g), true
}

// goValue converts a constant to the Go value of its kind. The concrete type
// matters: the encoder switches on it, treating uint8 and int32 differently
// from every other integer.
func goValue(k types.BasicKind, v constant.Value) (any, bool) {
	switch k {
	case types.Bool:
		if v.Kind() != constant.Bool {
			return nil, false
		}
		return constant.BoolVal(v), true

	case types.String:
		if v.Kind() != constant.String {
			return nil, false
		}
		return constant.StringVal(v), true

	case types.Int, types.Int8, types.Int16, types.Int32, types.Int64:
		n, exact := constant.Int64Val(constant.ToInt(v))
		if !exact {
			return nil, false
		}
		switch k {
		case types.Int:
			return int(n), true
		case types.Int8:
			return int8(n), true
		case types.Int16:
			return int16(n), true
		case types.Int32:
			return int32(n), true
		default:
			return n, true
		}

	case types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64, types.Uintptr:
		n, exact := constant.Uint64Val(constant.ToInt(v))
		if !exact {
			return nil, false
		}
		switch k {
		case types.Uint:
			return uint(n), true
		case types.Uint8:
			return uint8(n), true
		case types.Uint16:
			return uint16(n), true
		case types.Uint32:
			return uint32(n), true
		case types.Uintptr:
			return uintptr(n), true
		default:
			return n, true
		}

	case types.Float32, types.Float64:
		f, _ := constant.Float64Val(constant.ToFloat(v))
		// Float64Val is inexact for most constants, which is fine — it rounds
		// the same way the compiler would. An overflow is not: it would print
		// +Inf where the real program would not have compiled.
		if math.IsInf(f, 0) {
			return nil, false
		}
		if k == types.Float32 {
			return float32(f), true
		}
		return f, true
	}

	// Complex and unsafe.Pointer constants are rare enough that building is
	// the honest answer.
	return nil, false
}
