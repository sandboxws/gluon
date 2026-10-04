//go:build integration

package repl

import (
	"regexp"
	"strings"
	"testing"
)

// The unit tests assert on generated source and on fixtures. These run it: a
// real child under a real profiler, and a real `go tool pprof` reading what it
// wrote. That is the only way to find out whether the harness ended up in the
// report, which is the failure invariant 9 records for :esc.

// hotDecl is an expression slow enough to be sampled. Go samples at 100 Hz and
// macOS delivers fewer SIGPROFs than that, so this has to be worth hundreds of
// milliseconds for the report to have anything in it at all — which is itself
// the reason the sample count is printed on every report.
const hotDecl = "func hot(n int) int {\n" +
	"\ttotal := 0\n" +
	"\tfor i := 0; i < n; i++ {\n" +
	"\t\tfor j := 0; j < n; j++ {\n" +
	"\t\t\ttotal += i ^ j\n" +
	"\t\t}\n" +
	"\t}\n" +
	"\treturn total\n" +
	"}"

var pprofCmdRe = regexp.MustCompile(`go tool pprof (\S+)`)

func profileFileIn(t *testing.T, out string) string {
	t.Helper()
	m := pprofCmdRe.FindStringSubmatch(stripStyles(out))
	if m == nil {
		t.Fatalf("the report does not say what opens the profile:\n%s", out)
	}
	return m[1]
}

// TestProfileNamesTheUsersOwnFunction, and nothing of gluon's.
//
// Two assertions, and the second is the one with history behind it. :esc once
// reported gluon's own wrapper as the user's allocation because the wrapper
// looked like user code from the outside, and it took a measurement to notice.
// A profile is the same trap: the harness runs inside the region it measures.
func TestProfileNamesTheUsersOwnFunction(t *testing.T) {
	c := testCore(t)
	if res := c.Submit(hotDecl); res.Err {
		t.Fatalf("setup: %s", res.Out)
	}

	res := c.Submit(":profile hot(9000)")
	if res.Err {
		t.Fatalf(":profile: %s", res.Out)
	}
	out := stripStyles(res.Out)

	if !strings.Contains(out, "main.hot") {
		t.Fatalf("the report does not name the function that did the work:\n%s", out)
	}
	// The heaviest row is the first one under the head.
	if top := firstRankingLine(t, out); !strings.Contains(top, "main.hot") {
		t.Errorf("the top contributor is %q, want main.hot:\n%s", top, out)
	}
	if !strings.Contains(out, "entry 1") {
		t.Errorf("the report does not name the session entry:\n%s", out)
	}
	for _, forbidden := range []string{"__gluon", "main.main", "runtime/pprof."} {
		if strings.Contains(out, forbidden) {
			t.Errorf("gluon's own harness is in the report as %q:\n%s", forbidden, out)
		}
	}
	// Invariant 1's other half, and the reason mapTraceback exists: a position
	// under /var/folders means nothing to a reader.
	if strings.Contains(out, "gluon-in-") {
		t.Errorf("the report names a generated file:\n%s", out)
	}
	if !strings.Contains(out, "samples") {
		t.Errorf("the report does not state the sample count:\n%s", out)
	}
}

// TestMemprofNamesTheAllocationSite, labels its measure, and says so plainly
// when there is nothing to report.
func TestMemprofNamesTheAllocationSite(t *testing.T) {
	c := testCore(t)
	if res := c.Submit("func grow(n int) []int {\n\tout := []int{}\n\tfor i := 0; i < n; i++ {\n\t\tout = append(out, i)\n\t}\n\treturn out\n}"); res.Err {
		t.Fatalf("setup: %s", res.Out)
	}

	res := c.Submit(":memprof grow(200000)")
	if res.Err {
		t.Fatalf(":memprof: %s", res.Out)
	}
	out := stripStyles(res.Out)
	if !strings.Contains(out, "allocated bytes") {
		t.Errorf("the report does not name its measure:\n%s", out)
	}
	if !strings.Contains(out, "main.grow") || !strings.Contains(out, "entry 1") {
		t.Errorf("the report does not name the allocation site:\n%s", out)
	}
	for _, forbidden := range []string{"__gluon", "main.main", "gluon-in-"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("gluon's own harness is in the report as %q:\n%s", forbidden, out)
		}
	}

	// An expression that allocates nothing gets a sentence, not a blank table.
	quiet := c.Submit(":memprof 1 + 1")
	if quiet.Err {
		t.Fatalf(":memprof 1 + 1: %s", quiet.Out)
	}
	if !strings.Contains(stripStyles(quiet.Out), "no allocation recorded") {
		t.Errorf("an expression that allocates nothing did not say so:\n%s", quiet.Out)
	}
}

// TestProfileWritesOutsideTheHostProject is invariant 1's standing assertion on
// this path. The failure behind it is a tracked 3.1 MB binary in another repo's
// history that .gitignore did not cover, and a profile is exactly that kind of
// artefact.
func TestProfileWritesOutsideTheHostProject(t *testing.T) {
	root := hostFixture(t)
	c := attachedCore(t, root)

	before := treeSnapshot(t, root)
	if _, ok := before["go.mod"]; !ok {
		// Two empty snapshots would compare equal and prove nothing.
		t.Fatalf("the snapshot did not see the fixture's own files: %v", before)
	}

	for _, line := range []string{":profile 1 + 1", ":memprof 1 + 1"} {
		res := c.Submit(line)
		if res.Err {
			t.Fatalf("%s: %s", line, res.Out)
		}
		path := profileFileIn(t, res.Out)
		if strings.HasPrefix(path, root) {
			t.Errorf("%s wrote %s, inside the host project %s", line, path, root)
		}
		if !strings.HasPrefix(path, c.ev.Dir()) {
			t.Errorf("%s wrote %s, outside the session's own directory %s", line, path, c.ev.Dir())
		}
	}

	after := treeSnapshot(t, root)
	for name, size := range after {
		if before[name] != size {
			t.Errorf("%s changed in the host tree", name)
		}
	}
	for name := range before {
		if _, ok := after[name]; !ok {
			t.Errorf("%s disappeared from the host tree", name)
		}
	}
}

// TestProfileLeavesTheSessionUnchanged. Invariant 14: the rewrite pulls in os,
// runtime and runtime/pprof, which the session does not have, and none of them
// may still be there afterwards. An import left behind is not a cosmetic
// problem — the next ordinary line writes an import it does not use and fails
// to build with "imported and not used".
func TestProfileLeavesTheSessionUnchanged(t *testing.T) {
	c := testCore(t)
	if res := c.Submit(`x := strings.Repeat("ab", 8)`); res.Err {
		t.Fatalf("setup: %s", res.Out)
	}
	entries, imports := len(c.sess.Entries), importPaths(c)

	for _, line := range []string{":profile x", ":memprof x", ":profile len(x)"} {
		if res := c.Submit(line); res.Err {
			t.Fatalf("%s: %s", line, res.Out)
		}
		if got := len(c.sess.Entries); got != entries {
			t.Errorf("%s: session has %d entries, want %d", line, got, entries)
		}
		if got := importPaths(c); got != imports {
			t.Errorf("%s: imports are now %q, were %q", line, got, imports)
		}
		for _, gone := range []string{"runtime/pprof", "os"} {
			if strings.Contains(importPaths(c), gone) {
				t.Errorf("%s left %s in the session's imports: %s", line, gone, importPaths(c))
			}
		}
	}

	// The session still builds, which is what an orphaned import would break.
	if res := c.Submit("x"); res.Err {
		t.Errorf("the session no longer evaluates: %s", res.Out)
	}
}

// TestProfileRunsAVoidExpression. Unlike :bench, a call made for its effects is
// a normal thing to profile — it is often the only thing worth profiling.
func TestProfileRunsAVoidExpression(t *testing.T) {
	c := testCore(t)
	if res := c.Submit("func work() { for i := 0; i < 1000; i++ { _ = i } }"); res.Err {
		t.Fatalf("setup: %s", res.Out)
	}
	for _, line := range []string{":profile work()", ":memprof work()"} {
		res := c.Submit(line)
		if res.Err {
			t.Fatalf("%s: %s", line, res.Out)
		}
		if _, err := c.pprofTop(cpuProfile, profileFileIn(t, res.Out)); err != nil {
			t.Errorf("%s wrote no readable profile: %v", line, err)
		}
	}
}

// firstRankingLine returns the first row under the report's head — the
// heaviest contributor, since pprof sorts by flat value.
func firstRankingLine(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "%") && strings.HasPrefix(line, "  ") {
			return line
		}
	}
	t.Fatalf("no ranking in:\n%s", out)
	return ""
}
