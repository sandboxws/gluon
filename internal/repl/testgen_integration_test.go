//go:build integration

package repl

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The failure that would make :test worse than useless is generated source
// that does not compile: a refusal costs a line of typing, and a broken test
// file costs the trust in every other one. So the guarantee is checked the
// only way it can be — by writing the tests into a real module and running the
// real toolchain over them.
//
// `go build` alone would not see them: it does not compile _test.go files at
// all. `go test -run` with a pattern nothing matches builds the test binary and
// runs no test, which is the compile check with none of the observed values
// re-asserted — the corpus below is about whether the source is Go, not about
// whether the expressions still return what they returned.

// buildScratch runs the toolchain over a saved module: the program with
// `go build`, and the test file with a `go test` that matches nothing.
func buildScratch(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"build", "./..."},
		{"test", "-run", "^gluonMatchesNothing$", "./..."},
	} {
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %s in the saved module: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}

// TestGeneratedTestsCompile walks a corpus of value shapes — every kind the
// encoder produces that has a literal form — generates a test for each, writes
// them into a real scratch module and compiles the result.
func TestGeneratedTestsCompile(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	c := testCore(t)

	for _, line := range []string{
		"type Point struct{ X, Y int }",
		"type Celsius float64",
		"func double(n int) int { return n * 2 }",
		"n := 21",
	} {
		if res := c.Submit(line); res.Err {
			t.Fatalf("setup %q: %s", line, res.Out)
		}
	}

	corpus := []string{
		// Scalars, including the two the encoder glosses for display and the
		// named type an untyped constant would not become on its own.
		"1 + 1",
		"3.5",
		"true",
		`"hello"`,
		"int64(42)",
		"Celsius(36.6)",
		`"abc"[0]`,
		"'x'",
		// Composites.
		"[]int{3, 1, 2}",
		"[2]string{\"a\", \"b\"}",
		`map[string]int{"b": 2, "a": 1}`,
		"Point{X: 1, Y: 2}",
		"[]Point{{1, 2}, {3, 4}}",
		`map[string][]int{"a": {1, 2}}`,
		"struct{ N int }{7}",
		// Nil, which has a literal form even though it has no contents.
		"[]int(nil)",
		// An expression over a session binding, whose setup the test carries.
		"double(n)",
		// A package the session imports but the test file does not yet.
		`strings.ToUpper("a")`,
		// The table form is source too.
		"-table double(n)",
	}
	for _, exp := range corpus {
		if res := c.metaTest(exp); res.Err {
			t.Fatalf(":test %s: %s", exp, res.Out)
		}
	}
	if len(c.tests) != len(corpus) {
		t.Fatalf("%d tests held, want %d", len(c.tests), len(corpus))
	}

	res := c.save("-test corpus")
	if res.Err {
		t.Fatalf(":save -test: %s", res.Out)
	}
	buildScratch(t, savedDir(t, res))
}

// TestGeneratedTestsPass is the other half of the corpus claim: the source
// compiles, and what it asserts is what was observed. A generated test that
// compiled and failed would be worse than one that did not compile.
func TestGeneratedTestsPass(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	c := testCore(t)

	for _, line := range []string{"func double(n int) int { return n * 2 }", "n := 21"} {
		if res := c.Submit(line); res.Err {
			t.Fatalf("setup %q: %s", line, res.Out)
		}
	}
	for _, exp := range []string{"double(n)", "[]int{1, 2, 3}", `map[string]int{"a": 1}`} {
		if res := c.metaTest(exp); res.Err {
			t.Fatalf(":test %s: %s", exp, res.Out)
		}
	}

	res := c.save("-test passing")
	if res.Err {
		t.Fatalf(":save -test: %s", res.Out)
	}
	dir := savedDir(t, res)
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the generated tests do not pass: %v\n%s", err, out)
	}
}

// TestTestLeavesTheSessionAlone is invariant 14 on this path. :test has to run
// the expression to see its value, and it does so through EvalTransient
// precisely so the session cannot tell: a session that grew an entry — or an
// import — because you asked what a line returned would be a session you could
// no longer reproduce from its own transcript.
func TestTestLeavesTheSessionAlone(t *testing.T) {
	c := testCore(t)
	for _, line := range []string{"func double(n int) int { return n * 2 }", "x := 21"} {
		if res := c.Submit(line); res.Err {
			t.Fatalf("%s: %s", line, res.Out)
		}
	}
	entries := len(c.sess.Entries)
	imports := len(c.ev.Imports())

	res := c.Submit(":test double(x)")
	if res.Err {
		t.Fatalf(":test failed: %s", res.Out)
	}
	if !strings.Contains(res.Out, "want := 42") {
		t.Errorf(":test did not record the observed value:\n%s", res.Out)
	}

	if got := len(c.sess.Entries); got != entries {
		t.Errorf("entries = %d, want %d — :test added one", got, entries)
	}
	if got := len(c.ev.Imports()); got != imports {
		t.Errorf("imports = %d, want %d — :test moved the session's import set", got, imports)
	}
	for _, im := range c.ev.Imports() {
		if im.Path == "testing" {
			t.Error("testing reached the session's imports; the next ordinary line would not build")
		}
	}
	if src, err := c.source(); err != nil {
		t.Fatal(err)
	} else if strings.Contains(src, "testing") {
		t.Errorf("testing reached the program the session became:\n%s", src)
	}
}

// TestGeneratedMockSatisfiesItsInterface. `var _ I = (*IMock)(nil)` is the
// declaration that makes the claim structural: if a method is missing or a
// signature is wrong, the module does not compile.
func TestGeneratedMockSatisfiesItsInterface(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	c := testCore(t)

	for _, line := range []string{
		"type Reader interface{ Read(p []byte) (int, error) }",
		"type Store interface { Reader; Get(k string) (string, error); Put(k, v string) error; Logf(f string, a ...any) }",
	} {
		if res := c.Submit(line); res.Err {
			t.Fatalf("setup %q: %s", line, res.Out)
		}
	}

	for _, tc := range []struct{ cmd, typ string }{
		{":mock Store", "StoreMock"},
		{":spy Store", "StoreSpy"},
		{":mock io.ReadWriter", "ReadWriterMock"},
		{":spy io.ReadWriter", "ReadWriterSpy"},
	} {
		t.Run(tc.cmd, func(t *testing.T) {
			res := c.Submit(tc.cmd)
			if res.Err {
				t.Fatalf("%s: %s", tc.cmd, res.Out)
			}
			// Pasting it is what the command is for, so paste it: the mock
			// becomes a declaration in the session, and the satisfaction
			// claim becomes one the compiler has to agree with.
			paste := testCore(t)
			for _, line := range []string{
				"type Reader interface{ Read(p []byte) (int, error) }",
				"type Store interface { Reader; Get(k string) (string, error); Put(k, v string) error; Logf(f string, a ...any) }",
			} {
				if r := paste.Submit(line); r.Err {
					t.Fatalf("setup: %s", r.Out)
				}
			}
			for _, decl := range splitDecls(res.Out) {
				if r := paste.Submit(decl); r.Err {
					t.Fatalf("pasting the generated source failed: %s\n%s", r.Out, decl)
				}
			}
			iface := strings.TrimPrefix(tc.cmd, ":mock ")
			iface = strings.TrimPrefix(iface, ":spy ")
			if r := paste.Submit("var _ " + iface + " = (*" + tc.typ + ")(nil)"); r.Err {
				t.Fatalf("the generated %s does not satisfy %s: %s", tc.typ, iface, r.Out)
			}
		})
	}
}

// TestGeneratedMockPanicsNamingTheUnsetMethod. The panic is the decision the
// whole shape rests on, and a decision that only holds in the printed source
// is not one — so it is checked by calling the method for real.
func TestGeneratedMockPanicsNamingTheUnsetMethod(t *testing.T) {
	c := testCore(t)
	if res := c.Submit("type Store interface{ Get(k string) (string, error) }"); res.Err {
		t.Fatalf("%s", res.Out)
	}
	res := c.Submit(":mock Store")
	if res.Err {
		t.Fatalf(":mock: %s", res.Out)
	}
	for _, decl := range splitDecls(res.Out) {
		if r := c.Submit(decl); r.Err {
			t.Fatalf("pasting the generated source failed: %s\n%s", r.Out, decl)
		}
	}

	got := c.Submit(`func() (s string) { defer func() { s = recover().(string) }(); m := &StoreMock{}; m.Get("k"); return }()`)
	if got.Err {
		t.Fatalf("calling the unset method: %s", got.Out)
	}
	if !strings.Contains(got.Out, "StoreMock.Get was called with no GetFunc set") {
		t.Errorf("the unset method did not panic naming itself:\n%s", got.Out)
	}
}

// splitDecls cuts generated source into the declarations a REPL takes one at a
// time. Every generated declaration starts in column one and nothing else
// does, which is what makes this enough here.
func splitDecls(src string) []string {
	var out []string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.Join(cur, "\n"))
			cur = nil
		}
	}
	for _, line := range strings.Split(src, "\n") {
		switch {
		case strings.HasPrefix(line, "//"):
			continue
		case line == "":
			continue
		case strings.HasPrefix(line, "type "), strings.HasPrefix(line, "func "):
			flush()
		}
		cur = append(cur, line)
	}
	flush()
	return out
}

// TestSavedTestFileIsBesideTheProgram keeps the destination honest: the tests
// go into the module :save already writes, not into a directory of their own.
func TestSavedTestFileIsBesideTheProgram(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	c := testCore(t)
	if res := c.metaTest("1 + 1"); res.Err {
		t.Fatalf(":test: %s", res.Out)
	}
	res := c.save("-test beside")
	if res.Err {
		t.Fatalf(":save -test: %s", res.Out)
	}
	dir := savedDir(t, res)
	if !strings.Contains(res.Out, filepath.Join(dir, "main_test.go")) {
		t.Errorf("the output does not name the test file:\n%s", res.Out)
	}
	buildScratch(t, dir)
}
