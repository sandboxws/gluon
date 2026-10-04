package repl

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/inspect"
	"github.com/sandboxws/gluon/internal/pretty"
	"github.com/sandboxws/gluon/internal/scratch"
	"github.com/sandboxws/gluon/internal/session"
)

// These are the fast tier: they assert on generated source without invoking a
// toolchain. What the source does when it is compiled is testgen_integration_test.go.

// generate drives the source builders directly, the way metaTest does once it
// has a value, so the shape of the output can be checked without a build.
func generate(t *testing.T, exp, want string, comparable bool) string {
	t.Helper()
	src := plainTest("TestIt", exp, want, nil, comparable)
	parses(t, src)
	return src
}

func parses(t *testing.T, src string) {
	t.Helper()
	if _, err := parser.ParseFile(token.NewFileSet(), "", "package main\n\n"+src, 0); err != nil {
		t.Fatalf("generated source does not parse: %v\n%s", err, src)
	}
}

// TestGeneratedSourceNamesTheExpression. The whole value of generating the
// test is that it says what you typed: a rewritten or temporary form would
// leave the reader translating back.
func TestGeneratedSourceNamesTheExpression(t *testing.T) {
	const exp = `strings.ToUpper("a")`
	src := generate(t, exp, `"A"`, true)
	if !strings.Contains(src, "got := "+exp) {
		t.Errorf("the expression is not in the generated source as written:\n%s", src)
	}
	if !strings.Contains(src, `want := "A"`) {
		t.Errorf("the observed value is not the expectation:\n%s", src)
	}
	// And the doc comment has to say the expectation was observed, not decided.
	if !strings.Contains(src, "not what it should produce") {
		t.Errorf("the generated test does not say what it is not asserting:\n%s", src)
	}
}

// TestTheAssertionFollowsComparability covers both branches of the one choice
// the type checker makes here.
func TestTheAssertionFollowsComparability(t *testing.T) {
	direct := generate(t, "double(21)", "42", true)
	if !strings.Contains(direct, "if got != want {") {
		t.Errorf("a comparable value did not get a direct comparison:\n%s", direct)
	}
	if strings.Contains(direct, "reflect.DeepEqual") {
		t.Errorf("a comparable value was compared with reflect:\n%s", direct)
	}

	deep := generate(t, "[]int{1, 2}", "[]int{1, 2}", false)
	if !strings.Contains(deep, "if !reflect.DeepEqual(got, want) {") {
		t.Errorf("a non-comparable value did not get deep equality:\n%s", deep)
	}
	// The comment is the point: a reader who cannot see why would otherwise
	// have to guess whether the choice was arbitrary.
	if !strings.Contains(deep, "not comparable with ==") {
		t.Errorf("deep equality was used without saying why:\n%s", deep)
	}
}

// TestPercentInTheExpressionIsNotAVerb. fmt.Sprintf("%d", n) is an entirely
// ordinary thing to :test, and its % lands inside the Errorf format string.
func TestPercentInTheExpressionIsNotAVerb(t *testing.T) {
	src := generate(t, `fmt.Sprintf("%d", 7)`, `"7"`, true)
	if !strings.Contains(src, `t.Errorf("fmt.Sprintf(\"%%d\", 7) = %v, want %v", got, want)`) {
		t.Errorf("the expression's %% was not escaped for the format string:\n%s", src)
	}
}

// TestTableFormIteratesWithSubtests. The first case is the observed one; the
// shape is what a second case gets added to.
func TestTableFormIteratesWithSubtests(t *testing.T) {
	src := tableTest("TestIt", "double(21)", "42", nil, "int", true)
	parses(t, src)
	for _, want := range []string{
		"tests := []struct {",
		"name string",
		"{\"double(21)\", double(21), 42},",
		"for _, tc := range tests {",
		"t.Run(tc.name, func(t *testing.T) {",
		"if tc.got != tc.want {",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("the table form is missing %q:\n%s", want, src)
		}
	}
}

// TestGeneratedSetupCarriesTheBindingsTheExpressionNeeds. A binding made at
// the prompt is a local inside main(), invisible from a test file beside that
// program — so without this the source reads perfectly and does not compile.
func TestGeneratedSetupCarriesTheBindingsTheExpressionNeeds(t *testing.T) {
	c := &Core{sess: &session.Session{}}
	for _, e := range []session.Entry{
		{Kind: session.KindStmt, Src: "x := 21", Binds: []string{"x"}},
		{Kind: session.KindStmt, Src: "y := x + 1", Binds: []string{"y"}},
		{Kind: session.KindStmt, Src: "unrelated := 9", Binds: []string{"unrelated"}},
		{Kind: session.KindDecl, Src: "func double(n int) int { return n * 2 }"},
	} {
		c.sess.Append(e)
	}

	setup, err := c.setupFor("double(y)")
	if err != nil {
		t.Fatalf("setupFor: %v", err)
	}
	// Transitively: y needs x. In session order, and without the entry
	// nothing depends on.
	want := []string{"x := 21", "y := x + 1"}
	if strings.Join(setup, "\n") != strings.Join(want, "\n") {
		t.Errorf("setup = %q, want %q", setup, want)
	}

	// A declaration is already at package scope, so an expression that needs
	// only that carries nothing.
	setup, err = c.setupFor("double(2)")
	if err != nil {
		t.Fatalf("setupFor: %v", err)
	}
	if len(setup) != 0 {
		t.Errorf("setup = %q, want nothing", setup)
	}
}

// TestSetupRefusesAPreviousResult. `_2` is bound on the way past inside main()
// and is not a name a test can build for itself; saying so beats `undefined: _2`
// from the compiler.
func TestSetupRefusesAPreviousResult(t *testing.T) {
	c := &Core{sess: &session.Session{}}
	c.sess.Append(session.Entry{Kind: session.KindStmt, Src: "x := it + 1", Binds: []string{"x"}})
	if _, err := c.setupFor("x * 2"); err == nil {
		t.Error("setupFor carried an entry that names a printed value")
	} else if !strings.Contains(err.Error(), "printed") {
		t.Errorf("error = %q, want it to say the value was one the session printed", err)
	}
}

// TestTestRefusesAPreviousResult is the same rule on the expression itself.
func TestTestRefusesAPreviousResult(t *testing.T) {
	c := testCore(t)
	for _, arg := range []string{"it", "it + 1", "_1"} {
		res := c.metaTest(arg)
		if !res.Err {
			t.Errorf(":test %s was accepted; a test cannot see a value the session printed", arg)
		}
	}
}

// TestTestNamesAreUnique: two :test calls on the same expression would
// otherwise declare the same function twice in one file.
func TestTestNamesAreUnique(t *testing.T) {
	c := &Core{}
	first := c.testName("double(21)")
	c.tests = append(c.tests, generated{Name: first})
	second := c.testName("double(21)")
	if first != "TestDouble" {
		t.Errorf("first name = %q, want TestDouble", first)
	}
	if second == first {
		t.Errorf("both tests are named %q", first)
	}
}

// TestTestNameFallsBackWhenThereIsNothingToNameItAfter. An expression of pure
// operators and numbers has no identifier to read a name out of.
func TestTestNameFallsBackWhenThereIsNothingToNameItAfter(t *testing.T) {
	c := &Core{}
	if got := c.testName("1 + 2*3"); got != "TestExpr" {
		t.Errorf("testName = %q, want TestExpr", got)
	}
}

// TestComparabilityFallsBackWithoutTheChecker is invariant 5 on this path: the
// checker is never an authority, so when it cannot answer the assertion falls
// to deep equality — correct for a comparable value too, and wrong for
// neither.
func TestComparabilityFallsBackWithoutTheChecker(t *testing.T) {
	t.Setenv("GLUON_NO_TYPECHECK", "1")
	c := testCore(t)

	target, err := c.resolve("1 + 1")
	if err == nil {
		t.Fatal("resolve answered with the checker disabled")
	}
	if inspect.Comparable(target) {
		t.Error("an unanswerable type was treated as comparable")
	}
	// And the fallback still produces a test, rather than refusing because the
	// checker was silent.
	src := plainTest("TestIt", "1 + 1", "2", nil, inspect.Comparable(target))
	parses(t, src)
	if !strings.Contains(src, "reflect.DeepEqual") {
		t.Errorf("the fallback did not use deep equality:\n%s", src)
	}
}

// TestRefusalNamesTheOffendingPartAndEmitsNothing. The refusal is only
// actionable if it says which part of the value caused it, and a refusal that
// still printed source would be the placeholder this design rejected.
func TestRefusalNamesTheOffendingPartAndEmitsNothing(t *testing.T) {
	c := testCore(t)
	tests := []struct {
		name string
		exp  string
		want string
	}{
		{"top level", "make(chan int)", "channel"},
		{"nested", "struct{ N int; C chan int }{1, make(chan int)}", ".C"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			held := len(c.tests)
			res := c.metaTest(tc.exp)
			if !res.Err {
				t.Fatalf(":test %s was accepted:\n%s", tc.exp, res.Out)
			}
			if !strings.Contains(res.Out, tc.want) {
				t.Errorf("refusal = %q, want it to name %q", res.Out, tc.want)
			}
			if strings.Contains(res.Out, "func Test") {
				t.Errorf("source was emitted alongside a refusal:\n%s", res.Out)
			}
			if len(c.tests) != held {
				t.Error("a refused test was held anyway")
			}
		})
	}
}

// TestTestsAccumulate: :save -test writes what the session generated, so what
// it generated has to still be there.
func TestTestsAccumulate(t *testing.T) {
	c := testCore(t)
	for _, exp := range []string{"1 + 1", `"a" + "b"`} {
		if res := c.metaTest(exp); res.Err {
			t.Fatalf(":test %s: %s", exp, res.Out)
		}
	}
	if len(c.tests) != 2 {
		t.Fatalf("%d tests held, want 2", len(c.tests))
	}
	file := c.TestFile()
	parses(t, strings.TrimPrefix(file, "package main\n\n"))
	if n := strings.Count(file, "func Test"); n != 2 {
		t.Errorf("%d functions in the test file, want 2:\n%s", n, file)
	}
}

// TestSaveWithTestsWritesThemBesideTheProgram, and — the half that matters —
// leaves the program itself byte-for-byte what a plain :save writes. A flag
// that changed the program would make `:save -test` a different save.
func TestSaveWithTestsWritesThemBesideTheProgram(t *testing.T) {
	c := testCore(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if res := c.Submit("x := 1"); res.Err {
		t.Fatalf("%s", res.Out)
	}
	if res := c.metaTest("x + 1"); res.Err {
		t.Fatalf(":test: %s", res.Out)
	}

	plainDir := saveInto(t, c, "plain")
	testDir := saveInto(t, c, "-test withtests")

	plain, err := os.ReadFile(filepath.Join(plainDir, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	withTests, err := os.ReadFile(filepath.Join(testDir, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != string(withTests) {
		t.Error("-test changed the program it saved")
	}
	if _, err := os.Stat(scratch.TestPath(plainDir)); !os.IsNotExist(err) {
		t.Errorf("a plain :save wrote a test file: %v", err)
	}
	src, err := os.ReadFile(scratch.TestPath(testDir))
	if err != nil {
		t.Fatalf("no test file beside the program: %v", err)
	}
	if !strings.Contains(string(src), "got := x + 1") {
		t.Errorf("the test file does not hold the generated test:\n%s", src)
	}
}

// TestSaveWithNothingGeneratedWritesNoFile. An empty test file would look like
// a suite that ran and found nothing to say.
func TestSaveWithNothingGeneratedWritesNoFile(t *testing.T) {
	c := testCore(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	res := c.save("-test empty")
	if res.Err {
		t.Fatalf(":save -test failed: %s", res.Out)
	}
	if !strings.Contains(res.Out, "nothing to write") {
		t.Errorf("output does not say there was nothing to write:\n%s", res.Out)
	}
	dir := savedDir(t, res)
	if _, err := os.Stat(scratch.TestPath(dir)); !os.IsNotExist(err) {
		t.Errorf(":save -test wrote a test file with no tests in it: %v", err)
	}
}

func saveInto(t *testing.T, c *Core, arg string) string {
	t.Helper()
	res := c.save(arg)
	if res.Err {
		t.Fatalf(":save %s: %s", arg, res.Out)
	}
	return savedDir(t, res)
}

func savedDir(t *testing.T, res Result) string {
	t.Helper()
	for _, line := range strings.Split(res.Out, "\n") {
		if rest, ok := strings.CutPrefix(line, "saved → "); ok {
			return filepath.Dir(rest)
		}
	}
	t.Fatalf(":save did not report a path:\n%s", res.Out)
	return ""
}

// TestMockReportsTheActualKind. A name that resolves to something that is not
// an interface is a different mistake from one that resolves to nothing, and
// the message has to separate them.
func TestMockReportsTheActualKind(t *testing.T) {
	c := testCore(t)
	if res := c.Submit("type Point struct{ X, Y int }"); res.Err {
		t.Fatalf("%s", res.Out)
	}
	for _, cmd := range []string{":mock Point", ":spy Point"} {
		res := c.Submit(cmd)
		if !res.Err {
			t.Fatalf("%s was accepted:\n%s", cmd, res.Out)
		}
		if !strings.Contains(res.Out, "not an interface") || !strings.Contains(res.Out, "struct") {
			t.Errorf("%s: %q, want it to name the actual kind", cmd, res.Out)
		}
	}
}

// TestMockAndSpyAreStaticAndTestIsNot. :mock and :spy emit source rather than
// answer a question about a type, which is why they were once kept off the
// tool surface entirely — but the tier is decided by what the handler does,
// and metaMock calls probe and inspect.Mock and nothing else. :test evaluates
// to observe the value it asserts, so it is the one of the three that runs.
//
// TestMCPTierIsIntentional pins the names; this holds the reading underneath
// them, next to the code it is a reading of.
func TestMockAndSpyAreStaticAndTestIsNot(t *testing.T) {
	c := &Core{}
	for name, wantStatic := range map[string]bool{":mock": true, ":spy": true, ":test": false} {
		cmd, ok := c.lookup(name)
		if !ok {
			t.Fatalf("%s is not registered", name)
		}
		if cmd.MCP == "" {
			t.Errorf("%s is not exposed as a tool", name)
		}
		if cmd.Static != wantStatic {
			t.Errorf("%s: Static = %v, want %v", name, cmd.Static, wantStatic)
		}
	}
}

// TestLiteralOfAnObservedValue is the seam between the two halves: what the
// child described becomes the expectation, or the refusal names why it cannot.
func TestLiteralOfAnObservedValue(t *testing.T) {
	got, err := pretty.Literal(pretty.Value{Type: "[]string", Kind: "list", Items: []pretty.Value{
		{Type: "string", Kind: "string", Repr: "a"},
	}})
	if err != nil {
		t.Fatalf("Literal: %v", err)
	}
	if got != `[]string{"a"}` {
		t.Errorf("Literal = %q", got)
	}
}
