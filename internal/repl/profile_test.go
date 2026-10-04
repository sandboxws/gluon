package repl

import (
	"go/ast"
	"go/parser"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/inspect"
	"github.com/sandboxws/gluon/internal/pretty"
	"github.com/sandboxws/gluon/internal/render"
	"github.com/sandboxws/gluon/internal/session"
)

// These assert on generated source and on parsed pprof output. Whether a real
// run produces a profile worth reading is the integration tier's question,
// since it takes a build; this is the half that can be answered without one.

// TestProfileSourceIsParseableStandardLibrary. Constraint B: the generated
// child program is strictly standard library, and the two variants reach for
// runtime only where they use it — an import the source does not name would
// not compile.
func TestProfileSourceIsParseableStandardLibrary(t *testing.T) {
	for _, tc := range []struct {
		label string
		kind  profileKind
		void  bool
		want  []string
	}{
		{"cpu", cpuProfile, false, []string{"os", "pprof", "runtime"}},
		{"cpu void", cpuProfile, true, []string{"os", "pprof"}},
		{"mem", memProfile, false, []string{"os", "pprof", "runtime"}},
		{"mem void", memProfile, true, []string{"os", "pprof", "runtime"}},
	} {
		src := profileSource("fib(30)", tc.kind, tc.void)
		got := freeQualifiers(t, src)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: packages %v, want %v\n%s", tc.label, got, tc.want, src)
		}
		// The import list follows the source. Anything it names and the list
		// omits is an unresolved qualifier; anything the list names and the
		// source does not is an unused import. Both fail to build.
		named := make([]string, 0, 3)
		for _, im := range profileImports(tc.kind, tc.void) {
			named = append(named, render.PackageName(im.Path))
		}
		sort.Strings(named)
		if !reflect.DeepEqual(named, tc.want) {
			t.Errorf("%s: profileImports names %v, source uses %v", tc.label, named, tc.want)
		}
	}
}

// TestProfileSourceCarriesNoPath is task 1.1's other half and the reason the
// path travels through the environment: a filesystem path in the generated
// source would show up in :src, in :save, and in every build error.
func TestProfileSourceCarriesNoPath(t *testing.T) {
	for _, kind := range []profileKind{cpuProfile, memProfile} {
		for _, void := range []bool{false, true} {
			src := profileSource("fib(30)", kind, void)
			if !strings.Contains(src, `os.Getenv("GLUON_PROFILE")`) {
				t.Errorf("%v void=%v: the path is not read from the environment:\n%s", kind, void, src)
			}
			for _, forbidden := range []string{"/", ".pprof"} {
				if strings.Contains(src, forbidden) {
					t.Errorf("%v void=%v: generated source names %q:\n%s", kind, void, forbidden, src)
				}
			}
		}
	}
}

// TestMemprofGodebugKeepsTheUsersSettings: the child starts with the profiler
// off, and a GODEBUG the user runs with is extended rather than replaced. Ours
// goes last, because the runtime keeps the last value it reads for a key.
func TestMemprofGodebugKeepsTheUsersSettings(t *testing.T) {
	for _, tc := range []struct{ user, want string }{
		{"", "GODEBUG=memprofilerate=0"},
		{"http2client=0", "GODEBUG=http2client=0,memprofilerate=0"},
		{"memprofilerate=1", "GODEBUG=memprofilerate=1,memprofilerate=0"},
	} {
		if got := memprofGodebug(tc.user); got != tc.want {
			t.Errorf("memprofGodebug(%q) = %q, want %q", tc.user, got, tc.want)
		}
	}
}

// TestMemprofOpensItsFileBeforeMeasuring: the open is gluon's, so it happens
// while the child's rate is still 0. Opened after the collection, it reached
// the report whenever nothing else had been recorded.
func TestMemprofOpensItsFileBeforeMeasuring(t *testing.T) {
	for _, void := range []bool{false, true} {
		src := profileSource("parse(doc)", memProfile, void)
		open := strings.Index(src, "os.Create(")
		rate := strings.Index(src, "runtime.MemProfileRate = 1")
		if open < 0 || rate < 0 || open > rate {
			t.Errorf("void=%v: the profile file is not opened before the rate is raised:\n%s", void, src)
		}
	}
}

// TestProfileSourceUsesTheReservedPrefix. Every identifier the rewrite
// introduces is __gluon-prefixed, the convention __gluonPrint and __gluonMute
// already follow. These names reach the profile, and invariant 9 records what
// happens when gluon's own wrapper is reported as the user's code.
func TestProfileSourceUsesTheReservedPrefix(t *testing.T) {
	for _, kind := range []profileKind{cpuProfile, memProfile} {
		src := profileSource("fib(30)", kind, false)
		for _, decl := range declaredNames(t, src) {
			if !strings.HasPrefix(decl, "__gluon") {
				t.Errorf("%v: the rewrite declares %q, which is not reserved-prefixed:\n%s",
					kind, decl, src)
			}
		}
	}
}

// TestProfilePathIsUniquePerInvocation: a second :profile must not overwrite a
// file the user may still have open in `go tool pprof`.
func TestProfilePathIsUniquePerInvocation(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 8; i++ {
		p := profilePath("/tmp/session", cpuProfile)
		if seen[p] {
			t.Fatalf("two invocations named the same file: %s", p)
		}
		seen[p] = true
	}
	if p := profilePath("/tmp/session", memProfile); strings.Contains(p, "cpu-") {
		t.Errorf("a memory profile went to %s", p)
	}
}

// TestProfileWithNoExpressionReportsUsage. Arg contains "<", so a bare
// invocation must return Err with "usage:" in Out — the rule
// TestCommandsAreWellFormed enforces across the registry. The Core here has no
// evaluator, so a command that reached one would panic rather than pass.
func TestProfileWithNoExpressionReportsUsage(t *testing.T) {
	for _, res := range []Result{(&Core{}).profile("  "), (&Core{}).memprof("")} {
		if !res.Err || !strings.HasPrefix(res.Out, "usage:") {
			t.Errorf("bare invocation: %+v", res)
		}
	}
}

// topFixture is a report in the shape `go tool pprof -top -lines` prints,
// holding one row of every kind the filter has to decide about.
const topFixture = `File: prog
Type: cpu
Time: 2026-08-30 03:09:01 EDT
Duration: 202.26ms, Total samples = 1.20s (14.82%)
Showing nodes accounting for 1.20s, 100% of 1.20s total
      flat  flat%   sum%        cum   cum%
     900ms 75.00% 75.00%      900ms 75.00%  main.fib /tmp/gluon-session-1/gluon-in-0.go:3
     200ms 16.67% 91.67%      200ms 16.67%  strings.Repeat /usr/local/go/src/strings/strings.go:576
     100ms  8.33%   100%      100ms  8.33%  main.__gluonPayload /tmp/gluon-session-1/gluonrt.go:120
         0     0%   100%      1.20s   100%  main.main /tmp/gluon-session-1/gluon-in-1.go:27
         0     0%   100%      1.20s   100%  main.main.func1 /tmp/gluon-session-1/gluon-in-1.go:22 (inline)
`

// coreWithEntries is a Core with a session and no evaluator, which is all the
// reporting half needs.
func coreWithEntries(entries ...session.Entry) *Core {
	s := &session.Session{}
	for _, e := range entries {
		s.Append(e)
	}
	return &Core{sess: s}
}

// TestParsePprofTopReadsTheRows: the loose parse picks up the five numeric
// columns, the function and the position, and skips the preamble.
func TestParsePprofTopReadsTheRows(t *testing.T) {
	head, rows, ok := parsePprofTop(topFixture)
	if !ok {
		t.Fatal("the column header was not found")
	}
	if head.samples != "1.20s" {
		t.Errorf("samples = %q, want 1.20s", head.samples)
	}
	if head.total != "1.20s" {
		t.Errorf("total = %q, want 1.20s", head.total)
	}
	if len(rows) != 5 {
		t.Fatalf("%d rows, want 5: %+v", len(rows), rows)
	}
	if rows[0].flat != "900ms" || rows[0].pct != "75.00%" || rows[0].fn != "main.fib" {
		t.Errorf("first row = %+v", rows[0])
	}
	if rows[0].loc != "/tmp/gluon-session-1/gluon-in-0.go:3" {
		t.Errorf("first row position = %q", rows[0].loc)
	}
	// "(inline)" trails the position and is not part of it.
	if rows[4].loc != "/tmp/gluon-session-1/gluon-in-1.go:22" {
		t.Errorf("inlined row position = %q", rows[4].loc)
	}
}

// TestParsePprofTopRefusesWhatItDoesNotUnderstand. The format is not a
// documented interface, so the caller must be able to tell "no summary" from
// "here is a summary" — invariant 5's posture, in a different report.
func TestParsePprofTopRefusesWhatItDoesNotUnderstand(t *testing.T) {
	for _, out := range []string{
		"",
		"some future pprof said something else entirely\n",
		"File: prog\nType: cpu\nprofile is empty\n",
	} {
		if _, _, ok := parsePprofTop(out); ok {
			t.Errorf("parsed %q as a report", out)
		}
	}
}

// TestProfileExcludesTheHarness is the requirement invariant 9 is behind: no
// function belonging to gluon's harness appears in the reported contributors.
func TestProfileExcludesTheHarness(t *testing.T) {
	c := coreWithEntries(
		session.Entry{Kind: session.KindDecl, Src: "func fib(n int) int {\n\treturn n\n}"},
	)
	p, ok := c.profileFrom(cpuProfile, topFixture)
	if !ok {
		t.Fatal("the fixture did not parse")
	}
	for _, r := range p.Rows {
		if strings.Contains(r.Fn, "__gluon") {
			t.Errorf("the harness function %s is in the report", r.Fn)
		}
		if r.Fn == "main.main" || strings.HasPrefix(r.Fn, "main.main.func") {
			t.Errorf("gluon's wrapper %s is in the report", r.Fn)
		}
	}
	if len(p.Rows) != 2 {
		t.Fatalf("%d rows survived, want 2 (the user's function and the stdlib): %+v", len(p.Rows), p.Rows)
	}
	if p.Rows[0].Fn != "main.fib" {
		t.Errorf("top contributor is %s, want main.fib", p.Rows[0].Fn)
	}
}

// TestProfileMapsPositionsOntoTheSession. Requirement: a position inside the
// session's own code is reported as the entry, never as the temporary file the
// program was generated into.
func TestProfileMapsPositionsOntoTheSession(t *testing.T) {
	c := coreWithEntries(
		session.Entry{Kind: session.KindDecl, Src: "func fib(n int) int {\n\treturn n\n}"},
	)
	p, ok := c.profileFrom(cpuProfile, topFixture)
	if !ok {
		t.Fatal("the fixture did not parse")
	}
	if p.Rows[0].Where != "entry 1" {
		t.Errorf("main.fib is at %q, want entry 1", p.Rows[0].Where)
	}
	// gluon-in-0.go:3 with DeclLineShift applied is the entry's second line.
	if p.Rows[0].Src != "return n" {
		t.Errorf("main.fib names %q, want the session line \"return n\"", p.Rows[0].Src)
	}
	// A position outside the session keeps a name a reader can use, and no
	// absolute path.
	if p.Rows[1].Where != "strings/strings.go:576" {
		t.Errorf("strings.Repeat is at %q", p.Rows[1].Where)
	}
	out := inspect.PlainProfile(p)
	for _, leaked := range []string{"/tmp/gluon-session-1", "gluon-in-"} {
		if strings.Contains(out, leaked) {
			t.Errorf("the report leaks %q:\n%s", leaked, out)
		}
	}
}

// TestProfileReportsTheSampleCount, and says so when there are too few of them
// to conclude anything. Both are stated: the count always, the caveat as a
// second signal on top of it.
func TestProfileReportsTheSampleCount(t *testing.T) {
	c := coreWithEntries()

	// 1.20s of samples at 100 Hz is 120 of them, over the threshold.
	rich, ok := c.profileFrom(cpuProfile, topFixture)
	if !ok {
		t.Fatal("the fixture did not parse")
	}
	if rich.Head != "120 samples" {
		t.Errorf("head = %q, want \"120 samples\"", rich.Head)
	}
	if rich.Caveat != "" {
		t.Errorf("a hundred and twenty samples drew a caveat: %q", rich.Caveat)
	}

	few, ok := c.profileFrom(cpuProfile, strings.Replace(topFixture, "= 1.20s", "= 30ms", 1))
	if !ok {
		t.Fatal("the thin fixture did not parse")
	}
	if few.Head != "3 samples" {
		t.Errorf("head = %q, want \"3 samples\"", few.Head)
	}
	if !strings.Contains(few.Caveat, "too few samples") {
		t.Errorf("three samples drew no caveat: %q", few.Caveat)
	}
}

// TestProfileWithNoSamplesSaysSo rather than printing an empty ranking.
func TestProfileWithNoSamplesSaysSo(t *testing.T) {
	empty := `File: prog
Type: cpu
Duration: 203.45ms, Total samples = 0
Showing nodes accounting for 0, 0% of 0 total
      flat  flat%   sum%        cum   cum%
`
	p, ok := coreWithEntries().profileFrom(cpuProfile, empty)
	if !ok {
		t.Fatal("the empty fixture did not parse")
	}
	if p.Head != "0 samples" {
		t.Errorf("head = %q, want \"0 samples\"", p.Head)
	}
	if len(p.Rows) != 0 {
		t.Errorf("%d rows from an empty profile", len(p.Rows))
	}
	out := inspect.PlainProfile(p)
	if !strings.Contains(out, "no samples were collected") {
		t.Errorf("an empty profile did not say so:\n%s", out)
	}
}

// memFixture is a heap profile's report, in alloc_space.
const memFixture = `File: prog
Type: alloc_space
Time: 2026-08-30 03:09:01 EDT
Showing nodes accounting for 21932.23kB, 99.77% of 21982.21kB total
Dropped 64 nodes (cum <= 109.91kB)
      flat  flat%   sum%        cum   cum%
19182.05kB 87.26% 87.26% 20651.51kB 93.95%  main.grow /tmp/gluon-session-1/gluon-in-0.go:3
 1462.19kB  6.65% 93.91%  1462.19kB  6.65%  fmt.Sprint /usr/local/go/src/fmt/print.go:272
    7.25kB  0.03% 93.94%     7.25kB  0.03%  runtime.mallocgc /usr/local/go/src/runtime/malloc.go:1125
`

// TestMemprofLabelsItsMeasure. A heap profile has four measures that answer
// different questions; an unlabelled number is how a reader concludes the
// wrong thing.
func TestMemprofLabelsItsMeasure(t *testing.T) {
	c := coreWithEntries(
		session.Entry{Kind: session.KindDecl, Src: "func grow(n int) []string {\n\treturn nil\n}"},
	)
	p, ok := c.profileFrom(memProfile, memFixture)
	if !ok {
		t.Fatal("the fixture did not parse")
	}
	out := inspect.PlainProfile(p)
	if !strings.Contains(out, "allocated bytes") {
		t.Errorf("the report does not name its measure:\n%s", out)
	}
	if !strings.Contains(out, "21982.21kB allocated") {
		t.Errorf("the report does not state the total:\n%s", out)
	}
	if p.Rows[0].Fn != "main.grow" || p.Rows[0].Where != "entry 1" {
		t.Errorf("top allocation site = %+v", p.Rows[0])
	}
	// The heap profiler records the stack above its own allocator, so a flat
	// runtime row is the runtime allocating for itself.
	for _, r := range p.Rows {
		if strings.HasPrefix(r.Fn, "runtime.") {
			t.Errorf("the runtime's own bookkeeping is in the report: %s", r.Fn)
		}
	}
}

// TestMemprofWithNoAllocationSaysSo rather than showing an empty table.
func TestMemprofWithNoAllocationSaysSo(t *testing.T) {
	empty := `File: prog
Type: alloc_space
Showing nodes accounting for 12.53kB, 99.59% of 12.80kB total
      flat  flat%   sum%        cum   cum%
    7.25kB 56.64% 56.64%     7.25kB 56.64%  runtime.mallocgc /usr/local/go/src/runtime/malloc.go:1125
    5.28kB 41.25% 97.89%     5.28kB 41.25%  runtime.newm /usr/local/go/src/runtime/proc.go:2888
`
	p, ok := coreWithEntries().profileFrom(memProfile, empty)
	if !ok {
		t.Fatal("the fixture did not parse")
	}
	out := inspect.PlainProfile(p)
	if !strings.Contains(out, "no allocation recorded") {
		t.Errorf("a profile with nothing of the user's in it did not say so:\n%s", out)
	}
	// The process-wide total is not printed beside it: two answers to one
	// question is worse than one.
	if strings.Contains(out, "12.80kB") {
		t.Errorf("the report claims a total it has just declined to attribute:\n%s", out)
	}
}

// TestProfileReportFallsBackToTheFile. A parse failure costs the summary and
// nothing else — the file and the command that opens it are the part that
// matters, and they are printed by the caller either way.
func TestProfileReportFallsBackToTheFile(t *testing.T) {
	out := (&Core{}).profileUnavailable(cpuProfile, "go tool pprof printed nothing this understands")
	if !strings.Contains(out, "cpu profile") || !strings.Contains(out, "no summary") {
		t.Errorf("the fallback does not say what happened:\n%s", out)
	}
}

// TestProfileTwinsAgree: the two renderings carry the same information, and
// only one of them has escape codes in it. Result.Out always gets the plain
// form when a modal opens, so a pipe loses nothing — invariant 19.
func TestProfileTwinsAgree(t *testing.T) {
	c := coreWithEntries(
		session.Entry{Kind: session.KindDecl, Src: "func fib(n int) int {\n\treturn n\n}"},
	)
	p, ok := c.profileFrom(cpuProfile, topFixture)
	if !ok {
		t.Fatal("the fixture did not parse")
	}
	plain := inspect.PlainProfile(p)
	if strings.ContainsRune(plain, 0x1b) {
		t.Errorf("the plain form carries escape codes:\n%q", plain)
	}
	for _, want := range []string{"cpu profile", "120 samples", "main.fib", "entry 1", "return n"} {
		if !strings.Contains(plain, want) {
			t.Errorf("the plain form lacks %q:\n%s", want, plain)
		}
	}
	// Whether the rich form actually emits escape codes depends on the colour
	// profile lipgloss detects, which under `go test` is none. What must hold
	// either way is that the two forms say the same thing.
	rich := inspect.RenderProfile(p, pretty.PlainStyles())
	if stripStyles(rich) != plain {
		t.Errorf("the two forms differ once styling is stripped:\n%s\n---\n%s", stripStyles(rich), plain)
	}
}

// declaredNames returns every identifier the generated expression binds.
func declaredNames(t *testing.T, src string) []string {
	t.Helper()
	expr, err := parser.ParseExpr(src)
	if err != nil {
		t.Fatalf("generated source does not parse: %v\n%s", err, src)
	}
	var out []string
	ast.Inspect(expr, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, lhs := range as.Lhs {
			if id, ok := lhs.(*ast.Ident); ok && id.Name != "_" {
				out = append(out, id.Name)
			}
		}
		return true
	})
	sort.Strings(out)
	return out
}
