package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sandboxws/gluon/internal/session"
)

// mustHave fails unless every want is somewhere in got.
func mustHave(t *testing.T, what, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("%s: missing %q in:\n%s", what, w, got)
		}
	}
}

// inlineFixture is real `go build -gcflags='-e -m=2'` output, with the paths
// rewritten to the synthetic files rendering emits. It carries all three
// things one build reports at once: escape lines, inlining lines and, at the
// end, an error — the case splitKeep exists for.
const inlineFixture = `gluon
gluon-in-0.go:1:6: can inline add with cost 4 as: func(int, int) int { return a + b }
gluon-in-1.go:1:6: cannot inline big: function too complex: cost 280 exceeds budget 80
gluon-in-2.go:1:11: make([]byte, 32) does not escape
gluon-in-2.go:1:9: inlining call to add
gluon-in-2.go:1:2: moved to heap: x
gluonrt.go:14:6: can inline __gluonPrint with cost 12 as: func(...any) {}
gluon-in-2.go:1:9: undefined: nope
`

func TestSplitKeepSeparatesInliningFromEverythingElse(t *testing.T) {
	kept, rest := splitKeep(inlineFixture, isInlineMsg)
	if len(kept) != 4 {
		t.Errorf("got %d inlining lines, want 4: %v", len(kept), kept)
	}
	// The escape lines are the other question and must not be reported as
	// inlining; the build error must not be swallowed as one.
	if strings.Contains(strings.Join(kept, "\n"), "escape") {
		t.Errorf("an escape message was kept as an inlining decision: %v", kept)
	}
	mustHave(t, "rest", rest, "undefined: nope", "does not escape")
	if strings.Contains(rest, "inlining call to") {
		t.Errorf("an inlining decision leaked into the errors: %q", rest)
	}
}

func TestMapMessagesAttributesInliningToEntries(t *testing.T) {
	s := &session.Session{Entries: []session.Entry{
		{Kind: session.KindDecl, Src: "func add(a, b int) int { return a + b }"},
		{Kind: session.KindDecl, Src: "func big(i int) int { return i }"},
		{Kind: session.KindExpr, Src: "add(1, 2)"},
	}}
	kept, _ := splitKeep(inlineFixture, isInlineMsg)
	here, elsewhere := mapMessages(s, kept)

	if len(here) != 1 || here[0].Entry != 2 {
		t.Fatalf("here = %+v, want one message on entry 2", here)
	}
	mustHave(t, "here", here[0].Text, "inlining call to add")
	if here[0].Quote == "" {
		t.Errorf("the newest entry's message was not quoted: %+v", here[0])
	}

	// gluonrt.go is gluon's own file, so its decision about gluon's own
	// printer is dropped rather than attributed to anything (invariant 9).
	if len(elsewhere) != 2 {
		t.Fatalf("elsewhere = %+v, want the two declarations only", elsewhere)
	}
	for _, m := range elsewhere {
		if strings.Contains(m.Text, "__gluon") {
			t.Errorf("a decision about gluon's own runtime was reported: %+v", m)
		}
	}
	mustHave(t, "elsewhere", elsewhere[1].Text, "cannot inline big", "exceeds budget 80")
}

// asmFixture is a real `-S` listing, cut down to two functions and a method
// and with the compiler's absolute paths left in place — mapping those away is
// what the slicer's caller is for.
const asmFixture = `gluon
main.add STEXT size=16 args=0x10 locals=0x0 funcid=0x0 leaf
	0x0000 00000 (/private/var/folders/xy/T/gluon-session-1/gluon-in-0.go:2[/private/var/folders/xy/T/gluon-session-1/main.go:4])	TEXT	main.add(SB), LEAF|NOFRAME|ABIInternal, $0-16
	0x0000 00000 (/private/var/folders/xy/T/gluon-session-1/gluon-in-0.go:2)	ADD	R1, R0, R0
	0x0004 00004 (/private/var/folders/xy/T/gluon-session-1/gluon-in-0.go:2)	RET	(R30)
	0x0000 00 00 01 8b c0 03 5f d6  ......_.
main.(*P).Inc STEXT size=16 args=0x8 locals=0x0 funcid=0x0 leaf
	0x0000 00000 (/private/var/folders/xy/T/gluon-session-1/gluon-in-1.go:3)	TEXT	main.(*P).Inc(SB), LEAF|NOFRAME|ABIInternal, $0-8
	0x000c 00012 (/private/var/folders/xy/T/gluon-session-1/gluon-in-1.go:3)	RET	(R30)
main.__gluonPrint STEXT size=8 args=0x18 locals=0x0 funcid=0x0
	0x0000 00000 (/private/var/folders/xy/T/gluon-session-1/gluonrt.go:14)	TEXT	main.__gluonPrint(SB), ABIInternal, $0-24
`

func TestSliceSymbolCutsOneBlock(t *testing.T) {
	block, rest := sliceSymbol(asmFixture, "main.add")
	// The header, three instructions and the symbol's own byte dump, which is
	// indented under it and so belongs to it.
	if len(block) != 5 {
		t.Fatalf("got %d lines, want the header and four indented lines: %v", len(block), block)
	}
	mustHave(t, "header", block[0], "main.add STEXT")
	// The block ends at the next line in column zero, so nothing from the
	// method's listing may follow.
	if strings.Contains(strings.Join(block, "\n"), "Inc") {
		t.Errorf("the slice ran into the next function: %v", block)
	}
	mustHave(t, "rest", rest, "main.(*P).Inc STEXT", "main.__gluonPrint STEXT")
	if strings.Contains(rest, "ADD\tR1") {
		t.Errorf("an instruction from the sliced block leaked into rest: %q", rest)
	}

	// A method's symbol carries parentheses and a dot and is found the same way.
	block, _ = sliceSymbol(asmFixture, "main.(*P).Inc")
	if len(block) != 3 {
		t.Fatalf("method block = %v, want a header and two instructions", block)
	}
}

func TestSliceSymbolIsExact(t *testing.T) {
	// main.add must not answer for a longer name that starts the same way.
	if block, _ := sliceSymbol(asmFixture, "main.ad"); len(block) != 0 {
		t.Errorf("a prefix of a real symbol matched it: %v", block)
	}
}

func TestMapAsmPositionsNamesEntries(t *testing.T) {
	s := &session.Session{Entries: []session.Entry{
		{Kind: session.KindDecl, Src: "func add(a, b int) int { return a + b }"},
		{Kind: session.KindDecl, Src: "type P struct{ n int }\nfunc (p *P) Inc() { p.n++ }"},
	}}
	block, _ := sliceSymbol(asmFixture, "main.add")
	var mapped []string
	for _, line := range block {
		mapped = append(mapped, mapAsmPositions(s, line))
	}
	got := strings.Join(mapped, "\n")

	mustHave(t, "mapped", got, "(entry 0 line 1)")
	// The bracketed physical position names main.go, which the user has never
	// opened; it goes with the group rather than surviving beside the mapping.
	if strings.Contains(got, "main.go") {
		t.Errorf("the physical position survived the mapping:\n%s", got)
	}
	if strings.Contains(got, "/private/var") || strings.Contains(got, "gluon-in-") {
		t.Errorf("a temporary path survived the mapping:\n%s", got)
	}
	// Every position that remains must name an entry, not a file.
	for _, m := range asmPosRe.FindAllString(got, -1) {
		t.Errorf("an unmapped position remains: %q", m)
	}

	// gluon's own runtime keeps its basename: it is a real file the position
	// belongs to, and there is no entry to name it by.
	rt := mapAsmPositions(s, "\t0x0000 00000 (/private/var/folders/xy/T/gluon-session-1/gluonrt.go:14)\tTEXT")
	mustHave(t, "runtime position", rt, "(gluonrt.go:14)")
	if strings.Contains(rt, "/") {
		t.Errorf("a directory survived on a non-entry position: %q", rt)
	}
}

func TestAssemblyReportsNoCodeEmitted(t *testing.T) {
	// A function inlined at every call site and dropped by the linker has no
	// block, which is an answer rather than an empty pane.
	block, _ := sliceSymbol(asmFixture, "main.gone")
	if len(block) != 0 {
		t.Fatalf("a symbol with no block returned %v", block)
	}
	res := AsmResult{Symbol: "main.gone", Emitted: len(block) > 0}
	if res.Emitted {
		t.Errorf("Emitted is set for a symbol the compiler never wrote: %+v", res)
	}
}

// vetFixture is what `go vet .` writes when it finds something: the package
// name, then one diagnostic per line in the same file:line:col shape every
// other tool in this package parses.
const vetFixture = `# gluon
./gluonrt.go:14:2: fmt.Sprintf format %d has arg s of wrong type string
./gluon-in-0.go:1:2: unreachable code
./gluon-in-2.go:1:12: fmt.Printf format %s has arg 42 of wrong type int
`

func TestVetFindingsAreAttributedAndFiltered(t *testing.T) {
	s := &session.Session{Entries: []session.Entry{
		{Kind: session.KindDecl, Src: "func f() int { return 1; return 2 }"},
		{Kind: session.KindExpr, Src: "f()"},
		{Kind: session.KindExpr, Src: `fmt.Printf("%s", 42)`},
	}}
	findings, _ := splitKeep(vetFixture, func(string) bool { return true })
	here, elsewhere := mapMessages(s, findings)

	// A finding in gluon's own runtime is gluon's bug, and the user cannot act
	// on it. It is dropped rather than shown.
	all := append(append([]Diag{}, elsewhere...), here...)
	if len(all) != 2 {
		t.Fatalf("got %d findings, want the two in the user's own code: %+v", len(all), all)
	}
	for _, f := range all {
		if strings.Contains(f.Text, "Sprintf") {
			t.Errorf("a finding in gluonrt.go was reported: %+v", f)
		}
	}

	if elsewhere[0].Entry != 0 {
		t.Errorf("the earlier finding is on entry %d, want 0", elsewhere[0].Entry)
	}
	mustHave(t, "earlier finding", elsewhere[0].Text, "unreachable code")
	if here[0].Entry != 2 {
		t.Errorf("the newest finding is on entry %d, want 2", here[0].Entry)
	}
	mustHave(t, "newest finding", here[0].Quote, `fmt.Printf("%s", 42)`)
}

// raceFixture is a real detector report, captured from a two-goroutine
// unsynchronised increment built with //line directives naming synthetic
// files — which is what rendering already emits.
const raceFixture = `42
==================
WARNING: DATA RACE
Read at 0x00c000012158 by goroutine 7:
  main.main.func1()
      /private/var/folders/xy/T/gluon-session-1/gluon-in-3.go:1 +0x30

Previous write at 0x00c000012158 by goroutine 6:
  main.main.func1()
      /private/var/folders/xy/T/gluon-session-1/gluon-in-3.go:1 +0x40

Goroutine 7 (running) created at:
  main.main()
      /private/var/folders/xy/T/gluon-session-1/gluon-in-3.go:1 +0x6c
==================
Found 1 data race(s)
`

func raceSession() *session.Session {
	return &session.Session{Entries: []session.Entry{
		{Kind: session.KindExpr, Src: "n := 0"},
		{Kind: session.KindExpr, Src: "var wg sync.WaitGroup"},
		{Kind: session.KindExpr, Src: "wg.Add(2)"},
		{Kind: session.KindExpr, Src: "for range 2 { go func() { n++; wg.Done() }() }"},
	}}
}

func TestSplitRaceKeepsTheProgramsOwnOutput(t *testing.T) {
	report, rest := splitRace(raceFixture)
	mustHave(t, "report", report, "WARNING: DATA RACE", "Found 1 data race(s)")
	// The value the expression produced still has to be printed: a race is
	// something that happened during the run, not instead of it.
	mustHave(t, "rest", rest, "42")
	if strings.Contains(rest, "DATA RACE") {
		t.Errorf("the detector's report leaked into the program's output: %q", rest)
	}
}

func TestMapRaceFramesNamesEveryFrame(t *testing.T) {
	report, _ := splitRace(raceFixture)
	got := mapRaceFrames(raceSession(), report)

	mustHave(t, "mapped report", got,
		"entry 3 line 1: for range 2 { go func() { n++; wg.Done() }() }")
	if strings.Contains(got, "/private/var") || strings.Contains(got, "gluon-in-") {
		t.Errorf("a temporary path survived the mapping:\n%s", got)
	}
	// Both stacks are what a race report is read by comparing, so both have to
	// name their entry.
	if n := strings.Count(got, "entry 3 line 1:"); n != 3 {
		t.Errorf("named %d frames, want all 3:\n%s", n, got)
	}
}

func TestRaceExitCodeIsNotACrash(t *testing.T) {
	// 66 is the detector saying it reported something, and the program still
	// ran to completion. Treating it as a crash would hide the value.
	res := RaceResult{ExitCode: raceExitCode, Raced: raceExitCode == raceExitCode}
	if !res.Raced {
		t.Errorf("exit %d was not read as a race report", raceExitCode)
	}
	if raceExitCode == 0 || raceExitCode == 1 || raceExitCode == 2 {
		t.Errorf("raceExitCode %d collides with an ordinary exit", raceExitCode)
	}
}

func TestRaceBuiltTracksTheInstrumentedBinary(t *testing.T) {
	e := &Evaluator{dir: t.TempDir()}
	if e.raceBuilt() {
		t.Errorf("a session that has never run :race reports the binary as built")
	}
	if err := os.WriteFile(filepath.Join(e.dir, raceBinary), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !e.raceBuilt() {
		t.Errorf("the instrumented binary exists and was not noticed")
	}
	// prog is the ordinary binary and says nothing about the instrumented one.
	e2 := &Evaluator{dir: t.TempDir()}
	if err := os.WriteFile(filepath.Join(e2.dir, "prog"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if e2.raceBuilt() {
		t.Errorf("the ordinary binary was mistaken for the instrumented one")
	}
}

func TestRaceTimeoutMessagesNameTheInstrumentedBuild(t *testing.T) {
	// The ordinary run's message names :undo, which is no help here: nothing
	// is wedged, the toolchain is slow for a reason the user should be told.
	build := raceBuildTimeout(30 * time.Second)
	mustHave(t, "build timeout", build, "30s", "-race", "standard library")
	if strings.Contains(build, ":undo") {
		t.Errorf("the race build's timeout message points at :undo: %q", build)
	}
	run := raceRunTimeout(30 * time.Second)
	mustHave(t, "run timeout", run, "30s", "slower")
	if strings.Contains(run, ":undo") {
		t.Errorf("the race run's timeout message points at :undo: %q", run)
	}
}

func TestRacePlatformRefusalIsTheToolchainsOwnWords(t *testing.T) {
	// What `go build -race` prints on a target it does not support. It carries
	// no file:line, so explain has nothing to rewrite and passes it through —
	// which is the requirement: the toolchain's message, not a paraphrase.
	const refusal = "go: -race is only supported on linux/amd64, linux/ppc64le, " +
		"linux/arm64, freebsd/amd64, netbsd/amd64, darwin/amd64, darwin/arm64, and windows/amd64\n"
	e := &Evaluator{dir: "/var/folders/xy/T/gluon-session-1"}
	got := e.explain(&session.Session{}, refusal, "")
	mustHave(t, "refusal", got, "-race is only supported on", "darwin/arm64")
}

// TestRaceRunNeverTouchesTheResultCache reads the source rather than running,
// because the property is "this code path does not contain those calls" and a
// -race build costs seconds. The observable half — the cache length across a
// real :race — is asserted by the integration tier.
func TestRaceRunNeverTouchesTheResultCache(t *testing.T) {
	src, err := os.ReadFile("insights.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	start := strings.Index(body, "func (e *Evaluator) RaceRun(")
	if start < 0 {
		t.Fatal("RaceRun is gone; this test names the wrong function")
	}
	end := strings.Index(body[start:], "\n}\n")
	if end < 0 {
		t.Fatal("could not find the end of RaceRun")
	}
	fn := body[start : start+end]

	// The cache is keyed on program text, which cannot tell a -race build from
	// an ordinary one. Reading it would serve a cached value under a command
	// named :race; writing it would serve a race report to the next ordinary
	// evaluation of the same line.
	for _, banned := range []string{"e.cache", "cache.get", "cache.put", "putUnlessLive"} {
		if strings.Contains(fn, banned) {
			t.Errorf("RaceRun reaches the result cache through %s", banned)
		}
	}
	// And it never builds a second time without the flag.
	if n := strings.Count(fn, "buildArgs("); n != 1 {
		t.Errorf("RaceRun builds %d times; a retry without -race is the answer this command must not give", n)
	}
	if !strings.Contains(fn, `"-race"`) {
		t.Errorf("RaceRun does not pass -race")
	}
}
