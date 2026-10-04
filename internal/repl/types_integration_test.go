//go:build integration

package repl

import (
	"strings"
	"testing"
)

// TestImplAgreesWithTheCompiler is invariant 5 checked against the only
// authority there is.
//
// :impl answers from go/types in gluon's own process, which is the whole reason
// it is fast enough to be an always-on tool — and the whole reason a wrong "no"
// would be worse than no answer at all. So every verdict is put to the real
// toolchain as `var _ I = T{}`, the throwaway a Go developer writes to ask the
// same question, and the two have to agree.
//
// The oracle is a second session built with the checker off, so its answer
// comes from `go build` and not from the same go/types call :impl already made.
// Without that the test would only be asking gluon to agree with itself.
func TestImplAgreesWithTheCompiler(t *testing.T) {
	decls := []string{
		"type Shape interface{ Area() float64 }",
		"type Sq struct{ side float64 }",
		"func (s Sq) Area() float64 { return s.side * s.side }",
		"type Bad struct{}",
		"func (Bad) Area() int { return 0 }",
		"type Tri struct{ b, h float64 }",
		"func (t *Tri) Area() float64 { return t.b * t.h / 2 }",
		// A session-declared type inside the method signature. Resolved in two
		// separate checks these compare as different types and the verdict
		// flips, which is exactly what this test would catch.
		"type ID string",
		"type Store interface{ Get(ID) error }",
		"type S struct{}",
		"func (S) Get(ID) error { return nil }",
	}

	// The compiler's session first, while GLUON_NO_TYPECHECK is set: the
	// evaluator reads it once, when it is built.
	t.Setenv("GLUON_NO_TYPECHECK", "1")
	oracle := newSession(t, decls)
	defer oracle.Close()

	t.Setenv("GLUON_NO_TYPECHECK", "")
	c := newSession(t, decls)
	defer c.Close()

	tests := []struct {
		typ, iface string
		want       bool
	}{
		{typ: "Sq", iface: "Shape", want: true},
		{typ: "Bad", iface: "Shape", want: false},
		{typ: "Tri", iface: "Shape", want: false},
		{typ: "S", iface: "Store", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.typ+"/"+tc.iface, func(t *testing.T) {
			res := c.Submit(":impl " + tc.typ + ", " + tc.iface)
			if res.Err {
				t.Fatalf(":impl errored: %s", res.Out)
			}
			said := !strings.Contains(res.Out, "does not implement")
			if said != tc.want {
				t.Fatalf(":impl said %v, want %v:\n%s", said, tc.want, res.Out)
			}

			probe := oracle.Submit("var _ " + tc.iface + " = " + tc.typ + "{}")
			built := !probe.Err
			if built {
				oracle.Submit(":undo") // leave the session as the next case found it
			}
			if built != said {
				t.Errorf(":impl said %v; `var _ %s = %s{}` built %v:\n%s",
					said, tc.iface, tc.typ, built, probe.Out)
			}
		})
	}
}

// newSession is a Core with the given declarations already submitted.
func newSession(t *testing.T, lines []string) *Core {
	t.Helper()
	c, err := NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	for _, line := range lines {
		if res := c.Submit(line); res.Err {
			c.Close()
			t.Fatalf("setup %q: %s", line, res.Out)
		}
	}
	return c
}

// TestImplNamesWhatTheCompilerOnlyHints: the pointer-receiver case is the one
// :impl exists for. The compiler reports it as a method that "has pointer
// receiver" buried in a longer message; :impl has to say it in the open.
func TestImplNamesThePointerCase(t *testing.T) {
	c, err := NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	defer c.Close()

	for _, line := range []string{
		"type Shape interface{ Area() float64 }",
		"type Tri struct{ b, h float64 }",
		"func (t *Tri) Area() float64 { return t.b * t.h / 2 }",
	} {
		if res := c.Submit(line); res.Err {
			t.Fatalf("setup %q: %s", line, res.Out)
		}
	}

	res := c.Submit(":impl Tri, Shape")
	if res.Err {
		t.Fatalf(":impl errored: %s", res.Out)
	}
	for _, want := range []string{"*Tri implements Shape", "Area"} {
		if !strings.Contains(res.Out, want) {
			t.Errorf("output missing %q:\n%s", want, res.Out)
		}
	}

	// And the compiler agrees that the pointer is what fixes it.
	if probe := c.Submit("var _ Shape = &Tri{}"); probe.Err {
		t.Errorf("the compiler rejected `var _ Shape = &Tri{}`: %s", probe.Out)
	}
}
