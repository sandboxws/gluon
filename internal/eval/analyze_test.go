package eval

import (
	"go/constant"
	"go/types"
	"testing"

	"github.com/sandboxws/gluon/internal/session"
)

func sessionOf(t *testing.T, srcs ...string) *session.Session {
	t.Helper()
	s := &session.Session{}
	for _, src := range srcs {
		e, err := session.Classify(src)
		if err != nil {
			t.Fatalf("classify %q: %v", src, err)
		}
		s.Append(e)
	}
	return s
}

func errsOf(msgs ...string) []types.Error {
	out := make([]types.Error, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, types.Error{Msg: m})
	}
	return out
}

// goimports resolves pkg.Sym and nothing else. Telling the two apart matters
// because the message is identical and guessing wrong costs ~135ms on the
// mistake a person makes most often while learning.
func TestImportFixable(t *testing.T) {
	tests := []struct {
		name string
		sess *session.Session
		errs []types.Error
		want bool
	}{
		{
			name: "missing import for a qualifier used in the session",
			sess: sessionOf(t, `strings.ToUpper("a")`),
			errs: errsOf("undefined: strings"),
			want: true,
		},
		{
			name: "bare misspelled identifier is never an import",
			sess: sessionOf(t, "undefinedThing"),
			errs: errsOf("undefined: undefinedThing"),
			want: false,
		},
		{
			name: "method on a local is not an import",
			sess: sessionOf(t, "x := 1", "x.Foo()"),
			errs: errsOf("x.Foo undefined (type int has no field or method Foo)"),
			want: false,
		},
		{
			name: "an orphaned import is worth a full resolve",
			sess: sessionOf(t, "1 + 1"),
			errs: errsOf(`"strings" imported and not used`),
			want: true,
		},
		{
			name: "a plain type error is not an import problem",
			sess: sessionOf(t, `"a" + 1`),
			errs: errsOf(`invalid operation: "a" + 1 (mismatched types untyped string and untyped int)`),
			want: false,
		},
		{
			name: "one fixable diagnostic among several is enough",
			sess: sessionOf(t, "x := 1", `strings.ToUpper("a")`),
			errs: errsOf("undefined: nope", "undefined: strings"),
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := importFixable(tc.sess, tc.errs); got != tc.want {
				t.Errorf("importFixable = %v, want %v", got, tc.want)
			}
		})
	}
}

// The constant fast path may only answer when it can promise the same bytes a
// real run would have produced.
func TestConstResultRefusesWhatItCannotPromise(t *testing.T) {
	// A named type carries its own String method; time.Duration(1) prints
	// "1ns", not "1".
	named := types.NewNamed(
		types.NewTypeName(0, types.NewPackage("time", "time"), "Duration", nil),
		types.Typ[types.Int64], nil,
	)
	if _, ok := constResult(named, constant.MakeInt64(1)); ok {
		t.Error("a named type was answered from its constant value")
	}

	// Complex constants are not worth a second formatting implementation.
	if _, ok := constResult(types.Typ[types.Complex128], constant.MakeInt64(1)); ok {
		t.Error("a complex constant was answered from its value")
	}

	// A predeclared basic type is fine.
	if _, ok := constResult(types.Typ[types.Int], constant.MakeInt64(42)); !ok {
		t.Error("a plain int constant was refused")
	}
}

// The concrete Go type decides how the encoder renders a value, so an untyped
// constant has to arrive as its default type: an untyped rune is int32, which
// prints with its glyph.
func TestGoValueUsesTheConcreteType(t *testing.T) {
	tests := []struct {
		kind types.BasicKind
		val  constant.Value
		want any
	}{
		{types.Int, constant.MakeInt64(7), int(7)},
		{types.Int32, constant.MakeInt64(97), int32(97)},
		{types.Uint8, constant.MakeInt64(200), uint8(200)},
		{types.Int8, constant.MakeInt64(-5), int8(-5)},
		{types.Uint64, constant.MakeUint64(1 << 40), uint64(1 << 40)},
		{types.String, constant.MakeString("hi"), "hi"},
		{types.Bool, constant.MakeBool(true), true},
		{types.Float64, constant.MakeFloat64(0.5), float64(0.5)},
		{types.Float32, constant.MakeFloat64(0.5), float32(0.5)},
	}
	for _, tc := range tests {
		got, ok := goValue(tc.kind, tc.val)
		if !ok {
			t.Errorf("goValue(%v) refused", tc.kind)
			continue
		}
		if got != tc.want {
			t.Errorf("goValue(%v) = %#v, want %#v", tc.kind, got, tc.want)
		}
	}

	// A value that does not fit its type must be refused rather than wrapped.
	if _, ok := goValue(types.Int64, constant.MakeFromLiteral("99999999999999999999", 5, 0)); ok {
		t.Error("an out-of-range constant was converted")
	}
}
