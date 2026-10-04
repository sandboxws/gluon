//go:build integration

package repl

import (
	"strings"
	"testing"
)

// :t -d is the one thing under :t that builds and runs, so it is the one thing
// under :t with an integration tier. What is asserted here is what only a real
// child can answer: that the type reported is the one actually inside the
// interface, and that asking left nothing behind.

// dynCore is a session with an interface holding something concrete, an
// interface holding nothing, and a value whose static type is already the
// answer — the three shapes the output has.
func dynCore(t *testing.T) *Core {
	t.Helper()
	c := testCore(t)
	for _, line := range []string{
		"var buf bytes.Buffer",
		"var r io.Reader = &buf",
		"var e error",
		"n := 42",
		"bs := []byte(\"hi\")",
	} {
		if res := c.Submit(line); res.Err {
			t.Fatalf("setup %q: %s", line, res.Out)
		}
	}
	return c
}

// TestDynamicTypeOfAnInterface is the question the command exists for: the
// static type says io.Reader and says nothing about what is in it, which is
// the gap the hint under a bare :t has been pointing at since v2.
func TestDynamicTypeOfAnInterface(t *testing.T) {
	c := dynCore(t)
	res := c.Submit(":t -d r")
	if res.Err {
		t.Fatalf(":t -d r failed: %s", res.Out)
	}
	for _, want := range []string{"static io.Reader", "dynamic *bytes.Buffer", "pointer"} {
		if !strings.Contains(res.Out, want) {
			t.Errorf("output missing %q:\n%s", want, res.Out)
		}
	}
	// The generated program is package main; a temp path or the child's own
	// qualifier reaching the reader would name something they cannot type.
	for _, leak := range []string{"main.", "__gluon", "/private/var", "gluon-in-"} {
		if strings.Contains(res.Out, leak) {
			t.Errorf("%q reached the output:\n%s", leak, res.Out)
		}
	}
}

// TestDynamicTypeOfANilInterface: nil is not a type. Reporting <nil> where a
// type name goes would read as a type called nil, which is the confusion the
// command is meant to end rather than add to.
func TestDynamicTypeOfANilInterface(t *testing.T) {
	c := dynCore(t)
	res := c.Submit(":t -d e")
	if res.Err {
		t.Fatalf(":t -d e failed: %s", res.Out)
	}
	for _, want := range []string{"static error", "nil"} {
		if !strings.Contains(res.Out, want) {
			t.Errorf("output missing %q:\n%s", want, res.Out)
		}
	}
	for _, leak := range []string{"<nil>", ", dynamic "} {
		if strings.Contains(res.Out, leak) {
			t.Errorf("a nil interface reported a dynamic type (%q):\n%s", leak, res.Out)
		}
	}
}

// TestDynamicTypeOfAConcreteValue: when the static type is not an interface
// the two halves cannot differ, so the type is named once. []byte is the case
// worth having — reflect spells it []uint8, and printing both spellings under
// two labels would invent a distinction the language does not have.
func TestDynamicTypeOfAConcreteValue(t *testing.T) {
	c := dynCore(t)
	for _, tc := range []struct {
		cmd     string
		want    string
		notWant string
	}{
		{cmd: ":t -d n", want: "int", notWant: ", dynamic "},
		{cmd: ":t -d bs", want: "[]byte", notWant: "[]uint8"},
	} {
		t.Run(tc.cmd, func(t *testing.T) {
			res := c.Submit(tc.cmd)
			if res.Err {
				t.Fatalf("%s failed: %s", tc.cmd, res.Out)
			}
			if !strings.Contains(res.Out, tc.want) || !strings.Contains(res.Out, "concrete") {
				t.Errorf("%s: want %q and concrete:\n%s", tc.cmd, tc.want, res.Out)
			}
			if strings.Contains(res.Out, tc.notWant) {
				t.Errorf("%s: output contains %q:\n%s", tc.cmd, tc.notWant, res.Out)
			}
		})
	}
}

// TestDynamicTypeWithoutTheChecker is invariant 5 on this path: the checker is
// the one component that could reject a program the compiler accepts, so its
// absence may not decide whether the run happens. The measured half is still
// reported, and the half that was not measured says so rather than guessing.
func TestDynamicTypeWithoutTheChecker(t *testing.T) {
	t.Setenv("GLUON_NO_TYPECHECK", "1")
	c := testCore(t)
	for _, line := range []string{"var buf bytes.Buffer", "var r io.Reader = &buf"} {
		if res := c.Submit(line); res.Err {
			t.Fatalf("setup %q: %s", line, res.Out)
		}
	}

	res := c.Submit(":t -d r")
	if res.Err {
		t.Fatalf(":t -d with no checker failed: %s", res.Out)
	}
	for _, want := range []string{"static unavailable", "dynamic *bytes.Buffer"} {
		if !strings.Contains(res.Out, want) {
			t.Errorf("output missing %q:\n%s", want, res.Out)
		}
	}
	// "unavailable" is the whole point: it must not read as a static type
	// gluon claims to know, and it must not read as a verdict either.
	if strings.Contains(res.Out, "concrete") {
		t.Errorf("a checker that could not answer must not report concrete:\n%s", res.Out)
	}
}

// TestDynamicTypeLeavesTheSessionAlone is invariant 14 on this path, and the
// requirement's own sentence: no entry is added, no import is recorded, and no
// cached result is created.
//
// The rewrite pulls in fmt and reflect that the session does not have, and it
// runs the user's own expression — so a result left in the cache would be
// served as current the next time the same program text came round, which for
// a command whose answer is a measurement is the wrong answer, not a stale one.
func TestDynamicTypeLeavesTheSessionAlone(t *testing.T) {
	c := dynCore(t)
	entries := len(c.sess.Entries)
	imports := len(c.ev.Imports())
	cached := c.ev.CacheLen()

	res := c.Submit(":t -d r")
	if res.Err {
		t.Fatalf(":t -d r failed: %s", res.Out)
	}

	if got := len(c.sess.Entries); got != entries {
		t.Errorf("entries = %d, want %d — :t -d added one", got, entries)
	}
	if got := len(c.ev.Imports()); got != imports {
		t.Errorf("imports = %d, want %d — the rewrite leaked fmt or reflect", got, imports)
	}
	if got := c.ev.CacheLen(); got != cached {
		t.Errorf("cached results = %d, want %d — :t -d left an answer to be served later", got, cached)
	}

	// The session still builds and still says what it said, which is the
	// thing all three counts are protecting.
	if out := c.Submit(":t r"); out.Err || !strings.Contains(out.Out, "io.Reader") {
		t.Errorf("the session no longer answers as it did: %s", out.Out)
	}
}

// TestDynamicTypeRunsTheExpressionEveryTime is the other half of the cache
// promise. Nothing is asserted about speed — only that the second ask is a
// real one, which is what the count above pins from the outside.
func TestDynamicTypeRunsTheExpressionEveryTime(t *testing.T) {
	c := dynCore(t)
	before := c.ev.CacheLen()
	for range 2 {
		if res := c.Submit(":t -d r"); res.Err {
			t.Fatalf(":t -d r failed: %s", res.Out)
		}
	}
	if got := c.ev.CacheLen(); got != before {
		t.Errorf("cached results = %d, want %d — the second ask could be answered from the first",
			got, before)
	}
}

// TestDynamicTypeOfAnExpressionThatCannotRun: the static half was free and is
// still half the answer, so it is reported before the failure rather than
// swallowed with it.
func TestDynamicTypeOfAnExpressionThatCannotRun(t *testing.T) {
	c := dynCore(t)
	if res := c.Submit("func boom() io.Reader { panic(\"no\") }"); res.Err {
		t.Fatalf("setup: %s", res.Out)
	}
	res := c.Submit(":t -d boom()")
	if !res.Err {
		t.Fatalf("a panicking expression should not report a dynamic type: %s", res.Out)
	}
	if !strings.Contains(res.Out, "io.Reader") {
		t.Errorf("the static half was known and is missing:\n%s", res.Out)
	}
}
