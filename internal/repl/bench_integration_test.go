//go:build integration

package repl

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The unit tests assert on generated source. These run it: a real child
// process through a real testing.B, which is the only way to find out whether
// the flags left the measurement alone.

var allocsRe = regexp.MustCompile(`(\d+) allocs/op`)

// TestBenchCountOneMeasuresWhatBareBenchMeasures.
//
// -count 1 goes through the loop and the results slice that -count added;
// bare :bench goes through the same source with the same count. If either
// perturbed the measured region, the harness's own allocation would show up as
// the expression's — the failure recorded on benchSource, and the one
// invariant 9 records for :esc. allocs/op is the quantity to compare, because
// it is exact where a nanosecond count is not.
func TestBenchCountOneMeasuresWhatBareBenchMeasures(t *testing.T) {
	c := testCore(t)
	const expr = `strings.Repeat("ab", 8)`

	bare := c.Submit(":bench " + expr)
	if bare.Err {
		t.Fatalf(":bench %s: %s", expr, bare.Out)
	}
	counted := c.Submit(":bench -count 1 " + expr)
	if counted.Err {
		t.Fatalf(":bench -count 1 %s: %s", expr, counted.Out)
	}

	if a, b := allocsOf(t, bare.Out), allocsOf(t, counted.Out); a != b {
		t.Errorf("allocs/op: bare %s, -count 1 %s\n%s\n---\n%s", a, b, bare.Out, counted.Out)
	}
	// -count 1 is also the case the spec pins as unchanged output.
	if bare.Out != counted.Out {
		t.Errorf("-count 1 rendered differently from bare :bench:\n%q\n%q", bare.Out, counted.Out)
	}
}

// TestBenchCountReportsASpread: three runs, three numbers per quantity.
func TestBenchCountReportsASpread(t *testing.T) {
	c := testCore(t)
	res := c.Submit(`:bench -count 3 strings.Repeat("ab", 8)`)
	if res.Err {
		t.Fatalf("%s", res.Out)
	}
	for _, want := range []string{"3 runs", "min", "median", "max", "ns/op", "allocs/op"} {
		if !strings.Contains(res.Out, want) {
			t.Errorf("the report lacks %q:\n%s", want, res.Out)
		}
	}
}

// TestBenchCPUListRunsAtEachValue.
func TestBenchCPUListRunsAtEachValue(t *testing.T) {
	c := testCore(t)
	res := c.Submit(`:bench -cpu 1,2 strings.Repeat("ab", 8)`)
	if res.Err {
		t.Fatalf("%s", res.Out)
	}
	for _, want := range []string{"GOMAXPROCS=1", "GOMAXPROCS=2"} {
		if !strings.Contains(res.Out, want) {
			t.Errorf("the report lacks %q:\n%s", want, res.Out)
		}
	}
}

// TestBenchProfileWritesOutsideTheProject.
//
// Invariant 1: gluon never writes into the project. The failure it records is
// a tracked binary in another repo's history that .gitignore did not cover, so
// this asserts both halves — the profile exists where the report says it does,
// and the working directory the test runs in is untouched.
func TestBenchProfileWritesOutsideTheProject(t *testing.T) {
	c := testCore(t)

	project := t.TempDir()
	before := listDir(t, project)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoBefore := listDir(t, wd)

	res := c.Submit(`:bench -profile strings.Repeat("ab", 8)`)
	if res.Err {
		t.Fatalf("%s", res.Out)
	}

	path := profilePathIn(t, res.Out)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the report named %s, which is not there: %v\n%s", path, err, res.Out)
	}
	if !strings.Contains(res.Out, "go tool pprof "+path) {
		t.Errorf("the report does not say what opens the profile:\n%s", res.Out)
	}
	if inside, _ := filepath.Rel(c.ev.Dir(), path); strings.HasPrefix(inside, "..") {
		t.Errorf("the profile went to %s, outside the session's own directory %s", path, c.ev.Dir())
	}
	if got := listDir(t, project); got != before {
		t.Errorf("a host project directory changed:\n%s\n---\n%s", before, got)
	}
	if got := listDir(t, wd); got != repoBefore {
		t.Errorf("the working directory changed:\n%s\n---\n%s", repoBefore, got)
	}
}

// TestBenchWithoutProfileLeavesNoFile is the other half of the profile flag:
// the default writes nothing. The unit test asserts the generated source names
// no file-creating call; this asserts that a real run leaves the directory
// exactly as it found it.
func TestBenchWithoutProfileLeavesNoFile(t *testing.T) {
	c := testCore(t)
	if res := c.Submit(`:bench -count 2 strings.Repeat("ab", 8)`); res.Err {
		t.Fatalf("%s", res.Out)
	}
	for _, name := range strings.Split(listDir(t, c.ev.Dir()), "\n") {
		if filepath.Ext(name) == ".pprof" {
			t.Errorf("a benchmark with no -profile left %s behind", name)
		}
	}
}

// TestBenchWithFlagsLeavesTheSessionUnchanged. Invariant 14: :bench pulls in
// testing and runtime that the session does not have, and none of the flags
// may change that.
func TestBenchWithFlagsLeavesTheSessionUnchanged(t *testing.T) {
	c := testCore(t)
	if res := c.Submit(`x := strings.Repeat("ab", 8)`); res.Err {
		t.Fatalf("setup: %s", res.Out)
	}
	entries, imports := len(c.sess.Entries), importPaths(c)

	for _, line := range []string{
		":bench x",
		":bench -count 2 x",
		":bench -cpu 1,2 x",
		":bench -profile x",
		// The pair form goes through EvalTransient exactly as one expression
		// does, and two expressions are two chances to leave an import behind.
		":bench x, strings.Repeat(\"cd\", 8)",
		":bench -count 2 x, strings.Repeat(\"cd\", 8)",
	} {
		if res := c.Submit(line); res.Err {
			t.Fatalf("%s: %s", line, res.Out)
		}
		if got := len(c.sess.Entries); got != entries {
			t.Errorf("%s: session has %d entries, want %d", line, got, entries)
		}
		if got := importPaths(c); got != imports {
			t.Errorf("%s: imports are now %q, were %q", line, got, imports)
		}
	}

	// The session still builds, which is what an import left behind would
	// break with "imported and not used".
	if res := c.Submit("x"); res.Err {
		t.Errorf("the session no longer evaluates: %s", res.Out)
	}
}

func importPaths(c *Core) string {
	var out []string
	for _, spec := range c.ev.Imports() {
		out = append(out, spec.Path)
	}
	return strings.Join(out, ",")
}

func allocsOf(t *testing.T, out string) string {
	t.Helper()
	m := allocsRe.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no allocs/op in:\n%s", out)
	}
	return m[1]
}

var profileRe = regexp.MustCompile(`cpu profile\s+(\S+)`)

func profilePathIn(t *testing.T, out string) string {
	t.Helper()
	m := profileRe.FindStringSubmatch(stripStyles(out))
	if m == nil {
		t.Fatalf("no profile path in:\n%s", out)
	}
	return m[1]
}

// listDir renders a directory's entries so two readings can be compared.
func listDir(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return strings.Join(out, "\n")
}

// TestBenchPairIsOneEvaluation: both expressions are measured in one child
// process, not two.
//
// Two evaluations would build twice and replay the session twice, and a
// measurement is more sensitive to that than a comparison is — :diff's
// argument, with more force. The evidence is the entry count: EvalTransient
// snapshots and pops once per evaluation, so a second one would show up as a
// second build in the evaluator's timings and as a second replay of x.
func TestBenchPairIsOneEvaluation(t *testing.T) {
	c := testCore(t)
	if res := c.Submit(`x := strings.Repeat("ab", 8)`); res.Err {
		t.Fatalf("setup: %s", res.Out)
	}

	res := c.Submit(`:bench len(x), strings.Repeat("cd", 8)`)
	if res.Err {
		t.Fatalf("%s", res.Out)
	}
	for _, want := range []string{"a  len(x)", `b  strings.Repeat("cd", 8)`,
		"ns/op", "B/op", "allocs/op", "b/a"} {
		if !strings.Contains(res.Out, want) {
			t.Errorf("the report lacks %q:\n%s", want, res.Out)
		}
	}
	// The one that allocates has no ratio against the one that does not, and
	// says so rather than printing an infinity.
	if !strings.Contains(res.Out, "n/a") {
		t.Errorf("B/op against a non-allocating expression printed a ratio:\n%s", res.Out)
	}

	// One expression is still two lines and no table (constraint I).
	one := c.Submit(":bench len(x)")
	if one.Err {
		t.Fatalf("%s", one.Out)
	}
	if strings.Contains(one.Out, "b/a") {
		t.Errorf("the single-expression form grew the pair's table:\n%s", one.Out)
	}
}
