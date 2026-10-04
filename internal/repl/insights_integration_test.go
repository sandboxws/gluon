//go:build integration

package repl

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// The unit tests assert on fixtures and on generated source. These run the
// real toolchain: the compiler's own inlining decisions, its own listing, the
// real `go vet`, and a real race detector watching a real child. That is the
// only way to find out whether the harness ended up in a report, which is the
// failure invariant 9 records.

// bigDecl is a function whose body deliberately exceeds the inlining budget.
// The compiler's cost model changes between releases, so this is written to be
// far past it rather than just over: the assertion is "cannot inline, and here
// is the cost against the budget", not any particular number.
const bigDecl = "func big(n int) int {\n" +
	"\tt := 0\n" +
	"\tfor i := 0; i < n; i++ {\n" +
	"\t\tt += i * 1 % 8\n" +
	"\t\tt += i * 2 % 9\n" +
	"\t\tt += i * 3 % 10\n" +
	"\t\tt += i * 4 % 11\n" +
	"\t\tt += i * 5 % 7\n" +
	"\t\tt += i * 6 % 8\n" +
	"\t\tt += i * 7 % 9\n" +
	"\t\tt += i * 8 % 10\n" +
	"\t\tt += i * 9 % 11\n" +
	"\t\tt += i * 10 % 7\n" +
	"\t\tt += i * 11 % 8\n" +
	"\t\tt += i * 12 % 9\n" +
	"\t\tt += i * 13 % 10\n" +
	"\t\tt += i * 14 % 11\n" +
	"\t\tt += i * 15 % 7\n" +
	"\t\tt += i * 16 % 8\n" +
	"\t\tt += i * 17 % 9\n" +
	"\t\tt += i * 18 % 10\n" +
	"\t\tt += i * 19 % 11\n" +
	"\t\tt += i * 20 % 7\n" +
	"\t\tt += i * 21 % 8\n" +
	"\t\tt += i * 22 % 9\n" +
	"\t\tt += i * 23 % 10\n" +
	"\t\tt += i * 24 % 11\n" +
	"\t\tt += i * 25 % 7\n" +
	"\t\tt += i * 26 % 8\n" +
	"\t\tt += i * 27 % 9\n" +
	"\t\tt += i * 28 % 10\n" +
	"\t\tt += i * 29 % 11\n" +
	"\t}\n" +
	"\treturn t\n" +
	"}"

// insightCore is a session with a small function, a big one, and a type with
// both receiver forms — everything the four commands are asked about.
func insightCore(t *testing.T) *Core {
	t.Helper()
	c := testCore(t)
	for _, decl := range []string{
		"func add(a, b int) int { return a + b }",
		bigDecl,
		"type P struct{ n int }",
		"func (p *P) Inc() { p.n++ }",
	} {
		if res := c.Submit(decl); res.Err {
			t.Fatalf("%s: %s", decl, res.Out)
		}
	}
	return c
}

func TestInlineReportsTheCompilersDecisions(t *testing.T) {
	c := insightCore(t)

	// A call to a small function: the compiler flattened it, and says so in
	// its own words.
	res := c.Submit(":inline add(1, 2)")
	if res.Err {
		t.Fatalf(":inline on a call: %s", res.Out)
	}
	if !strings.Contains(res.Out, "inlining call to add") {
		t.Errorf("the inlined call was not reported: %q", res.Out)
	}

	// A function past the budget: the reason is the useful half, and it is
	// what -m=2 exists for.
	res = c.Submit(":inline big")
	if res.Err {
		t.Fatalf(":inline on a declared function: %s", res.Out)
	}
	if !strings.Contains(res.Out, "cannot inline") || !strings.Contains(res.Out, "exceeds budget") {
		t.Errorf("the refusal or its cost is missing: %q", res.Out)
	}

	// A declared function that fits: the verdict, with its cost.
	res = c.Submit(":inline add")
	if res.Err || !strings.Contains(res.Out, "can inline add") {
		t.Errorf(":inline add: %q", res.Out)
	}

	// A line with no calls has no decisions, which is an answer rather than an
	// empty pane.
	res = c.Submit(":inline 1 + 2")
	if res.Err {
		t.Fatalf(":inline on a line with no calls: %s", res.Out)
	}
	if !strings.Contains(res.Out, "nothing on this line to inline") {
		t.Errorf("an empty report did not say so: %q", res.Out)
	}

	// gluon's own printer is never a decision the user asked about.
	for _, out := range []string{res.Out} {
		if strings.Contains(out, "__gluon") {
			t.Errorf("gluon's own harness reached the report: %q", out)
		}
	}
}

// asmPosRe matches the parenthesised positions the listing carries, which is
// where a temp path would appear if any survived.
var asmPosRe = regexp.MustCompile(`\(([^()]*)\)`)

func TestAsmListsOneFunctionWithMappedPositions(t *testing.T) {
	c := insightCore(t)

	for _, name := range []string{"add", "(*P).Inc", "P.Inc"} {
		res := c.Submit(":asm " + name)
		if res.Err {
			t.Fatalf(":asm %s: %s", name, res.Out)
		}
		if !strings.Contains(res.Out, "STEXT") {
			t.Errorf(":asm %s produced no listing: %q", name, res.Out)
		}
		// The listing is one function's, and nothing else's.
		if n := strings.Count(res.Out, "STEXT"); n != 1 {
			t.Errorf(":asm %s returned %d blocks, want 1:\n%s", name, n, res.Out)
		}
		// No temporary path, and no synthetic file name either: every position
		// the user sees names an entry.
		if strings.Contains(res.Out, "gluon-in-") {
			t.Errorf(":asm %s leaked a synthetic file name:\n%s", name, res.Out)
		}
		if strings.Contains(res.Out, c.ev.Dir()) {
			t.Errorf(":asm %s leaked the session directory:\n%s", name, res.Out)
		}
		if strings.Contains(res.Out, "__gluon") {
			t.Errorf(":asm %s named gluon's own harness:\n%s", name, res.Out)
		}

		// Every source position — a parenthesised group naming a line — says
		// which entry it belongs to and carries no path separator.
		for _, m := range asmPosRe.FindAllStringSubmatch(res.Out, -1) {
			inner := m[1]
			if !strings.Contains(inner, ".go:") && !strings.HasPrefix(inner, "entry ") {
				continue // an operand like (SB) or (R30), not a position
			}
			if !strings.HasPrefix(inner, "entry ") {
				t.Errorf(":asm %s: position %q does not name an entry", name, m[0])
			}
			if strings.Contains(inner, "/") {
				t.Errorf(":asm %s: position %q still carries a path", name, m[0])
			}
		}
	}

	// Both spellings of the same method answer identically: which one is right
	// is the receiver's business, not the user's.
	if a, b := c.Submit(":asm P.Inc"), c.Submit(":asm (*P).Inc"); a.Out != b.Out {
		t.Errorf("P.Inc and (*P).Inc gave different listings")
	}
}

func TestVetFindsWhatItShouldAndNothingElse(t *testing.T) {
	c := testCore(t)

	// A clean session: vet says it found nothing rather than printing nothing.
	if res := c.Submit(":vet"); res.Err || !strings.Contains(res.Out, "vet found nothing") {
		t.Errorf(":vet on a clean session: %q (err=%v)", res.Out, res.Err)
	}

	// A format verb that does not match its argument, on the appended line.
	res := c.Submit(`:vet fmt.Printf("%s", 42)`)
	if res.Err {
		t.Fatalf(":vet on a bad format: %s", res.Out)
	}
	if !strings.Contains(res.Out, "%s") || !strings.Contains(res.Out, "wrong type") {
		t.Errorf("the printf finding is missing: %q", res.Out)
	}
	// The finding quotes the line the user typed, not the program gluon built.
	if !strings.Contains(res.Out, `fmt.Printf("%s", 42)`) {
		t.Errorf("the finding does not quote the typed line: %q", res.Out)
	}
	if strings.Contains(res.Out, "__gluon") || strings.Contains(res.Out, "gluonrt.go") {
		t.Errorf("a finding in gluon's own code was shown: %q", res.Out)
	}

	// A finding on an earlier entry is reported against that entry, distinct
	// from anything on the newest line.
	if res := c.Submit(`fmt.Printf("%d", "no")`); res.Err {
		t.Fatalf("setting up an earlier finding: %s", res.Out)
	}
	res = c.Submit(":vet")
	if res.Err {
		t.Fatalf(":vet after an earlier finding: %s", res.Out)
	}
	if !strings.Contains(res.Out, `fmt.Printf("%d", "no")`) {
		t.Errorf("the earlier entry's finding is missing: %q", res.Out)
	}
}

// raceDecl spawns two goroutines that increment the same variable with no
// synchronisation, which is the smallest thing the detector reliably catches.
const raceDecl = "func spawn() int {\n" +
	"\tn := 0\n" +
	"\tvar wg sync.WaitGroup\n" +
	"\tfor range 2 {\n" +
	"\t\twg.Add(1)\n" +
	"\t\tgo func() { n++; wg.Done() }()\n" +
	"\t}\n" +
	"\twg.Wait()\n" +
	"\treturn n\n" +
	"}"

// raceSupported lists the platforms `go help build` names for the detector.
// The command is expected to refuse elsewhere with the toolchain's own words,
// and this test has nothing to assert there.
func raceSupported() bool {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64", "linux/ppc64le", "linux/arm64", "linux/s390x",
		"freebsd/amd64", "netbsd/amd64", "openbsd/amd64",
		"darwin/amd64", "darwin/arm64", "windows/amd64":
		return true
	}
	return false
}

func TestRaceReportsARaceAndNamesItsFrames(t *testing.T) {
	if !raceSupported() {
		// Named so the CI silent-skip guard can catch it: every platform CI
		// runs on supports the detector, so a skip here means the tier proved
		// nothing.
		t.Skip("race detector unavailable on " + runtime.GOOS + "/" + runtime.GOARCH)
	}
	c := testCore(t)
	if res := c.Submit(raceDecl); res.Err {
		t.Fatalf("declaring the racy function: %s", res.Out)
	}

	res := c.Submit(":race spawn()")
	if !strings.Contains(res.Out, "DATA RACE") {
		t.Fatalf(":race did not report the race:\n%s", res.Out)
	}
	// Every frame in the session's own code names the entry it came from, and
	// no path under the temp directory survives.
	if !strings.Contains(res.Out, "entry 0 line") {
		t.Errorf("no frame names the entry that declared the race:\n%s", res.Out)
	}
	if strings.Contains(res.Out, "gluon-in-") || strings.Contains(res.Out, c.ev.Dir()) {
		t.Errorf("a temporary path survived:\n%s", res.Out)
	}
	if strings.Contains(res.Out, "__gluon") {
		t.Errorf("gluon's own harness reached the report:\n%s", res.Out)
	}

	// A clean expression: the value, and the caveat that one run finding
	// nothing is not proof.
	clean := c.Submit(":race 1 + 1")
	if clean.Err {
		t.Fatalf(":race on a clean expression: %s", clean.Out)
	}
	if !strings.Contains(clean.Out, "2") {
		t.Errorf("the expression's value is missing: %q", clean.Out)
	}
	if !strings.Contains(clean.Out, "not proof") {
		t.Errorf("the absence caveat is missing: %q", clean.Out)
	}
}

// TestInsightsLeaveTheSessionAlone is invariant 14 for all four commands at
// once: a question asked about the session must not change it.
func TestInsightsLeaveTheSessionAlone(t *testing.T) {
	c := insightCore(t)
	if res := c.Submit("s := fmt.Sprint(1)"); res.Err {
		t.Fatalf("seeding an import: %s", res.Out)
	}

	entries := len(c.sess.Entries)
	imports := importPaths(c)
	cached := c.ev.CacheLen()

	cmds := []string{":inline add(1, 2)", ":asm add", ":vet", ":race 1 + 1"}
	if !raceSupported() {
		cmds = cmds[:3]
	}
	for _, cmd := range cmds {
		res := c.Submit(cmd)
		if res.Err && !strings.Contains(cmd, ":race") {
			t.Fatalf("%s: %s", cmd, res.Out)
		}
		if got := len(c.sess.Entries); got != entries {
			t.Errorf("%s changed the entry count: %d, want %d", cmd, got, entries)
		}
		if got := importPaths(c); got != imports {
			t.Errorf("%s changed the import set:\n got %s\nwant %s", cmd, got, imports)
		}
		if got := c.ev.CacheLen(); got != cached {
			t.Errorf("%s changed the result cache length: %d, want %d", cmd, got, cached)
		}
		if strings.Contains(res.Out, "__gluon") {
			t.Errorf("%s named gluon's own harness:\n%s", cmd, res.Out)
		}
	}
}

// TestInsightsDoNotRewriteProg: none of the four writes over the ordinary
// binary, so the next ordinary line pays no fresh Gatekeeper validation on a
// binary macOS has not seen before — which is the ~105ms the build-only path
// exists to avoid.
func TestInsightsDoNotRewriteProg(t *testing.T) {
	c := insightCore(t)
	// An ordinary line first, so prog exists to be compared against.
	if res := c.Submit("add(1, 2)"); res.Err {
		t.Fatalf("seeding prog: %s", res.Out)
	}

	prog := filepath.Join(c.ev.Dir(), "prog")
	before, err := os.Stat(prog)
	if err != nil {
		t.Fatalf("prog was not built: %v", err)
	}

	cmds := []string{":inline add(1, 2)", ":asm add", ":vet"}
	if raceSupported() {
		cmds = append(cmds, ":race 1 + 1")
	}
	for _, cmd := range cmds {
		if res := c.Submit(cmd); res.Err {
			t.Fatalf("%s: %s", cmd, res.Out)
		}
		after, err := os.Stat(prog)
		if err != nil {
			t.Fatalf("%s removed prog: %v", cmd, err)
		}
		if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
			t.Errorf("%s rewrote prog", cmd)
		}
	}

	// The instrumented binary is a second one beside it, not a replacement.
	if raceSupported() {
		if _, err := os.Stat(filepath.Join(c.ev.Dir(), "prog-race")); err != nil {
			t.Errorf(":race did not leave its own binary beside prog: %v", err)
		}
	}
}
