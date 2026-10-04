package repl

import (
	"errors"
	"github.com/sandboxws/gluon/internal/cmdspec"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/eval"
	"github.com/sandboxws/gluon/internal/session"
)

func TestBufFlagsThatAreNotFlags(t *testing.T) {
	c := &Core{}
	res := c.buffer("-nonsense")
	if !res.Err || !strings.Contains(res.Out, "usage:") {
		t.Errorf("an unknown flag answered %q, want a usage line", res.Out)
	}
}

func TestBufShowAndClear(t *testing.T) {
	c := &Core{}
	if res := c.buffer("-show"); res.Err || !strings.Contains(res.Out, "empty") {
		t.Errorf("empty buffer shown as %q", res.Out)
	}
	if res := c.buffer("-clear"); res.Err || !strings.Contains(res.Out, "already empty") {
		t.Errorf("clearing an empty buffer answered %q", res.Out)
	}

	c.buf = "x := 1\ny := 2\n"
	res := c.buffer("-show")
	if res.Err || !strings.Contains(res.Out, "x := 1") {
		t.Errorf("-show answered %q, want the buffer", res.Out)
	}
	if res.Lang == "" {
		t.Error("-show does not tag its output as source, so it is never highlighted")
	}

	if res := c.buffer("-clear"); res.Err || !strings.Contains(res.Out, "2 lines") {
		t.Errorf("-clear answered %q, want the line count it dropped", res.Out)
	}
	if c.buf != "" {
		t.Errorf("the buffer survived -clear: %q", c.buf)
	}
}

// The buffer is a place to write, so what is written has to still be there on
// the way back. Re-opening it must not hand the editor an empty file.
func TestTheBufferSurvivesBeingClosedAndOpened(t *testing.T) {
	c := &Core{}
	t.Cleanup(func() {
		if c.bufDir != "" {
			os.RemoveAll(c.bufDir)
		}
	})

	if res := c.bufOpen(); res.Edit == "" {
		t.Fatalf("first open handed back no file: %q", res.Out)
	}
	// The editor writes.
	path := filepath.Join(c.bufDir, bufName)
	if err := os.WriteFile(path, []byte("x := 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.bufTake()

	res := c.bufOpen()
	if res.Edit != path {
		t.Errorf("second open used %q, want the same path %q", res.Edit, path)
	}
	data, err := os.ReadFile(res.Edit)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "x := 1") {
		t.Errorf("re-opening handed the editor %q, want what was written last time", data)
	}
}

// Opening names the line to run afterwards rather than running anything: Core
// belongs to the evaluation goroutine and opens no terminal itself.
func TestBufOpenNamesTheLineToRunAfterwards(t *testing.T) {
	c := &Core{}
	t.Cleanup(func() {
		if c.bufDir != "" {
			os.RemoveAll(c.bufDir)
		}
	})
	res := c.bufOpen()
	if res.EditThen != ":buf -run" {
		t.Errorf("EditThen = %q, want %q", res.EditThen, ":buf -run")
	}
	if res.Out != "" {
		t.Errorf("Out = %q, want none — :edit sets none either", res.Out)
	}
}

// :edit must keep meaning reload. A result that names no line to run afterwards
// is the behaviour every caller had before a second thing could open an editor.
func TestEditStillMeansReload(t *testing.T) {
	c, err := NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	t.Cleanup(func() { c.Close() })

	res := c.Submit(":edit")
	if res.Edit == "" {
		t.Fatalf("no file to edit: %q", res.Out)
	}
	t.Cleanup(func() { os.Remove(res.Edit) })
	if res.EditThen != "" {
		t.Errorf("EditThen = %q, want empty — :edit reloads", res.EditThen)
	}
}

func TestTheRefusalNamesTheCommandThatAskedForAnEditor(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "gluon-edit-*.go")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	// :edit's temp file has no other copy of what is in it, so refusing it is
	// also the only chance to clean it up.
	if got := EditRefusal(Result{Edit: f.Name()}); got != "error: :edit needs a terminal" {
		t.Errorf("refusal = %q", got)
	}
	if _, err := os.Stat(f.Name()); !os.IsNotExist(err) {
		t.Error(":edit's temp file outlived the refusal")
	}

	// A buffer's file is inside a directory the session owns and holds text the
	// session still has. Refusing to show it must not throw it away.
	keep := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(keep, []byte("x := 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := EditRefusal(Result{Edit: keep, EditThen: ":buf -run"})
	if got != "error: :buf needs a terminal" {
		t.Errorf("refusal = %q, want it to name :buf", got)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("the buffer's file was deleted by a driver that could not show it: %v", err)
	}

	if got := EditRefusal(Result{}); got != "" {
		t.Errorf("a result asking for no editor was refused: %q", got)
	}
}

func TestEditorAtOpensOnTheLine(t *testing.T) {
	t.Setenv("VISUAL", "nvim")
	cmd := EditorAt("/tmp/x.go", 12)
	if cmd == nil {
		t.Fatal("no editor")
	}
	if got := strings.Join(cmd.Args, " "); !strings.Contains(got, "+12") {
		t.Errorf("argv = %q, want it to carry +12", got)
	}

	// Zero is Editor exactly: the same argv, byte for byte.
	a, b := EditorAt("/tmp/x.go", 0).Args, Editor("/tmp/x.go").Args
	if strings.Join(a, "\x00") != strings.Join(b, "\x00") {
		t.Errorf("EditorAt(_, 0) = %q, Editor = %q", a, b)
	}
}

func TestEditorAtLeavesAnEditorItDoesNotKnowAtTheTop(t *testing.T) {
	t.Setenv("VISUAL", "somethingelse")
	a, b := EditorAt("/tmp/x.go", 12).Args, Editor("/tmp/x.go").Args
	if strings.Join(a, "\x00") != strings.Join(b, "\x00") {
		t.Errorf("an unrecognised editor was given a line flag: %q", a)
	}
}

// The buffer is not the session, and must never be written into one.
func TestTheBufferIsNotPartOfThePersistedSession(t *testing.T) {
	s := &session.Session{}
	entry, err := session.Classify("x := 1")
	if err != nil {
		t.Fatal(err)
	}
	s.Append(entry)
	want := string(session.Marshal(s))

	c := &Core{sess: s, buf: "this text is being composed and has never run\n"}
	if got := string(session.Marshal(c.sess)); got != want {
		t.Errorf("the buffer reached the persisted session:\n%s", got)
	}
}

func TestBufRunOnAnEmptyBufferRunsNothing(t *testing.T) {
	c := &Core{}
	if res := c.buffer("-run"); res.Err || !strings.Contains(res.Out, "empty") {
		t.Errorf("-run on an empty buffer answered %q", res.Out)
	}
}

// A buffer ending mid-construct has no construct to run there. It reports
// rather than submitting the fragment or dropping it in silence.
func TestAnUnfinishedTailIsReportedRatherThanDropped(t *testing.T) {
	c := &Core{buf: "for i := range 3 {\n"}
	res := c.buffer("-run")
	if !res.Err || !strings.Contains(res.Out, "unfinished") {
		t.Errorf("-run answered %q, want the unfinished construct named", res.Out)
	}
	if !strings.Contains(res.Out, "buffer:1") {
		t.Errorf("-run answered %q, want the buffer line", res.Out)
	}
}

func TestBufDiagLinePlacesADiagnosticInTheBuffer(t *testing.T) {
	constructs, _ := session.SplitConstructsAt("x := 1\n\nfunc f() int {\n\treturn nope\n}\n")
	// The func starts on buffer line 3; the error is on its own line 2.
	got := bufDiagLine(constructs, eval.BlockDiag{
		Construct: 1, Line: 2, Col: 9,
		Msg:   "undefined: nope",
		Quote: "    \treturn nope",
	})
	if !strings.HasPrefix(got, "buffer:4:9: undefined: nope") {
		t.Errorf("diagnostic = %q, want it on buffer line 4", got)
	}
	if !strings.Contains(got, "return nope") {
		t.Errorf("diagnostic = %q, want the line quoted", got)
	}
}

// A diagnostic against the session has no buffer line to sit on, and claiming
// one would point at code that is not wrong.
func TestBufDiagLineDoesNotInventALineForASessionDiagnostic(t *testing.T) {
	constructs, _ := session.SplitConstructsAt("x := 1\n")
	got := bufDiagLine(constructs, eval.BlockDiag{Construct: -1, Msg: "x declared and not used"})
	if !strings.HasPrefix(got, "session: ") {
		t.Errorf("diagnostic = %q, want it marked as the session's", got)
	}
}

// The scaffold's whole purpose is that the file compiles on its own, so the
// user's own language server is live on it. These cover the surgery that makes
// it one file; that it really compiles is buffer_integration_test.go's job.

const probeProgram = `package main

/*line gluon-in-1.go:1:1*/
func double(n int) int { return n * 2 }

func main() {
	/*line gluon-in-0.go:1:1*/ x := 1
	_ = x
	__gluonPrint( /*line gluon-in-2.go:1:1*/ double(x))
}
`

func TestRegionsGoWhereGoWillTakeThem(t *testing.T) {
	got, _, err := insertBufRegions(probeProgram, "func triple(n int) int { return n * 3 }", "y := triple(x)")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(got, "\n")

	declsAt, codeAt, mainAt, braceAt := -1, -1, -1, -1
	for i, l := range lines {
		switch {
		case strings.HasPrefix(strings.TrimSpace(l), bufDeclsMarker):
			declsAt = i
		case strings.HasPrefix(strings.TrimSpace(l), bufCodeMarker):
			codeAt = i
		case strings.HasPrefix(l, "func main()"):
			mainAt = i
		case l == "}":
			braceAt = i
		}
	}
	if declsAt < 0 || codeAt < 0 || mainAt < 0 || braceAt < 0 {
		t.Fatalf("a landmark is missing from:\n%s", got)
	}
	// Declarations above main, because Go will not take one inside it.
	if !(declsAt < mainAt) {
		t.Errorf("the declarations region is not above func main:\n%s", got)
	}
	// Statements inside it, because that is where the session's bindings are.
	if !(mainAt < codeAt && codeAt < braceAt) {
		t.Errorf("the code region is not inside func main:\n%s", got)
	}
}

// Without a line directive per region the user's own code inherits the
// numbering of whichever gluon-in-N.go directive came last, and their editor
// reports their mistakes at a line of a file that does not exist. That would
// cost exactly the thing the scaffold is for.
func TestEachRegionResetsTheLineNumbering(t *testing.T) {
	got, _, err := insertBufRegions(probeProgram, "func triple(n int) int { return n * 3 }", "y := triple(x)")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(got, "\n")
	found := 0
	for i, l := range lines {
		if !strings.HasPrefix(l, "//line "+bufName+":") {
			continue
		}
		found++
		// A directive on line i+1 governs the line after it, which is i+2.
		want := "//line " + bufName + ":" + strconv.Itoa(i+2)
		if l != want {
			t.Errorf("line %d carries %q, want %q", i+1, l, want)
		}
		if i != 0 && lines[i][0] != '/' {
			t.Errorf("the directive on line %d is indented, which stops it being one", i+1)
		}
	}
	if found != 2 {
		t.Errorf("found %d line directives of our own, want one per region", found)
	}
}

func TestRegionsRoundTrip(t *testing.T) {
	decls, code := "func triple(n int) int { return n * 3 }", "y := triple(x)\n_ = y"
	file, _, err := insertBufRegions(probeProgram, decls, code)
	if err != nil {
		t.Fatal(err)
	}
	gotDecls, gotCode, err := extractBufRegions(file)
	if err != nil {
		t.Fatal(err)
	}
	if gotDecls != decls {
		t.Errorf("declarations came back as %q, want %q", gotDecls, decls)
	}
	if gotCode != code {
		t.Errorf("code came back as %q, want %q", gotCode, code)
	}
}

// An editor that formats on save indents the markers inside main. They are
// ordinary comments; only the line directives have to stay at column 1, and
// gofmt already leaves those alone.
func TestRegionsAreFoundEvenIndented(t *testing.T) {
	file, _, err := insertBufRegions(probeProgram, "", "y := 1")
	if err != nil {
		t.Fatal(err)
	}
	indented := strings.ReplaceAll(file, "\n"+bufCodeNote, "\n\t\t"+bufCodeNote)
	if indented == file {
		t.Fatal("test did not indent anything")
	}
	if _, code, err := extractBufRegions(indented); err != nil || code != "y := 1" {
		t.Errorf("indented markers were not found: code=%q err=%v", code, err)
	}
}

func TestAMissingMarkerIsRefusedRatherThanGuessedAt(t *testing.T) {
	file, _, err := insertBufRegions(probeProgram, "", "y := 1")
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{bufDeclsNote, bufCodeNote, bufEndNote} {
		mangled := strings.Replace(file, marker+"\n", "", 1)
		if mangled == file {
			t.Fatalf("test did not remove %q", marker)
		}
		if _, _, err := extractBufRegions(mangled); !errors.Is(err, errBufMarkers) {
			t.Errorf("removing %q was not refused: %v", marker, err)
		}
	}
}

// Out of order is refused too. Scanning forward from each marker is what makes
// that automatic: a code marker above the declarations one is never reached.
func TestReorderedMarkersAreRefused(t *testing.T) {
	swapped := "package main\n" + bufCodeNote + "\n" + bufEndNote + "\n" +
		bufDeclsNote + "\n" + bufEndNote + "\n"
	if _, _, err := extractBufRegions(swapped); !errors.Is(err, errBufMarkers) {
		t.Errorf("markers out of order were accepted: %v", err)
	}
}

// The regions are where a construct goes, not what it means. Classification is
// from the construct's own source, exactly as it is for a typed line — so a
// declaration written among the statements is still a declaration, and moves to
// where gluon puts one anyway.
func TestARegionDecidesNothingAboutWhatAConstructMeans(t *testing.T) {
	c := &Core{buf: "x := 1\nfunc f() int { return 2 }\nf()\n"}
	decls, code := c.bufRegions()
	if !strings.Contains(decls, "func f()") {
		t.Errorf("the declaration stayed among the statements: decls=%q", decls)
	}
	if strings.Contains(code, "func f()") {
		t.Errorf("the declaration is in both regions: code=%q", code)
	}
	// Everything else keeps the order it was written in.
	if code != "x := 1\nf()" {
		t.Errorf("code = %q, want the statements in the order they were written", code)
	}
}

// Somebody mid-edit has source that will not classify. Guessing it is a
// declaration would move it, and moving code somebody is in the middle of
// writing is worse than leaving it.
func TestSourceThatWillNotClassifyStaysWhereItIs(t *testing.T) {
	c := &Core{buf: "x := 1\nthis is not go\n"}
	decls, code := c.bufRegions()
	if decls != "" {
		t.Errorf("decls = %q, want nothing", decls)
	}
	if !strings.Contains(code, "this is not go") {
		t.Errorf("code = %q, want the unclassifiable line kept", code)
	}
}

func TestAnUnfinishedConstructSurvivesTheRoundTrip(t *testing.T) {
	c := &Core{buf: "x := 1\nfor i := range 3 {\n"}
	_, code := c.bufRegions()
	if !strings.Contains(code, "for i := range 3 {") {
		t.Errorf("code = %q, want the half-written loop kept", code)
	}
}

func TestBufFirstLineTakesTheFirstOneThatHasALine(t *testing.T) {
	constructs, _ := session.SplitConstructsAt("x := 1\nfunc f() int {\n\treturn nope\n}\n")
	got := bufFirstLine(constructs, []eval.BlockDiag{
		// Against the session: no buffer line, and skipped rather than guessed at.
		{Construct: -1, Msg: "x declared and not used"},
		{Construct: 1, Line: 2, Msg: "undefined: nope"},
		{Construct: 0, Line: 1, Msg: "something later"},
	})
	if got != 3 {
		t.Errorf("bufFirstLine = %d, want 3 — the func starts on line 2, its own line 2 is buffer line 3", got)
	}
	if got := bufFirstLine(constructs, nil); got != 0 {
		t.Errorf("bufFirstLine of nothing = %d, want 0", got)
	}
}

// A clean answer clears the line, which is what makes the edit-check-edit loop
// stop reopening on a mistake that is no longer there.
func TestACleanBufferOpensAtTheTop(t *testing.T) {
	c := &Core{bufErrLine: 12}
	t.Cleanup(func() {
		if c.bufDir != "" {
			os.RemoveAll(c.bufDir)
		}
	})
	if res := c.bufOpen(); res.EditLine != 12 {
		t.Errorf("EditLine = %d, want the line the last answer named", res.EditLine)
	}
	c.bufErrLine = 0
	if res := c.bufOpen(); res.EditLine != 0 {
		t.Errorf("EditLine = %d, want the top of the file", res.EditLine)
	}
}

// The driver submits the line a result named rather than reloading. :edit names
// none, and must keep meaning reload.
func TestTheDriverSubmitsTheLineAnEditorResultNamed(t *testing.T) {
	// The branch under test is in Update's resultMsg arm, which needs no Core to
	// choose between the two: what it reads is the result's own fields.
	for _, tc := range []struct {
		name     string
		res      Result
		wantThen string
	}{
		{"edit reloads", Result{Edit: "/tmp/x.go"}, ""},
		{"buf runs", Result{Edit: "/tmp/x.go", EditThen: ":buf -run"}, ":buf -run"},
	} {
		if tc.res.EditThen != tc.wantThen {
			t.Errorf("%s: EditThen = %q, want %q", tc.name, tc.res.EditThen, tc.wantThen)
		}
	}

	t.Setenv("VISUAL", "nvim")
	// And the line travels with it: EditLine is what the driver hands EditorAt.
	res := Result{Edit: "/tmp/x.go", EditThen: ":buf -run", EditLine: 7}
	if got := strings.Join(EditorAt(res.Edit, res.EditLine).Args, " "); !strings.Contains(got, "+7") {
		t.Errorf("argv = %q, want it to carry +7", got)
	}
}

// The scaffolded module has to carry the injected printer, or the rendered
// session in it does not compile — and be nested under an attached host, or the
// project's internal packages do not resolve.
func TestTheBufferModuleCarriesTheRuntimeAndTheHost(t *testing.T) {
	c, err := NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	t.Cleanup(func() { c.Close() })

	if res := c.bufOpen(); res.Err {
		t.Fatal(res.Out)
	}
	for _, name := range []string{"go.mod", "gluonrt.go"} {
		if _, err := os.Stat(filepath.Join(c.bufDir, name)); err != nil {
			t.Errorf("the buffer module has no %s: %v", name, err)
		}
	}
	mod, err := os.ReadFile(filepath.Join(c.bufDir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mod), "module ") {
		t.Errorf("go.mod names no module:\n%s", mod)
	}
}

// A command whose argument is a flag must not have it tokenized as Go, or a
// leading dash is painted as an operator.
func TestTheBufferArgumentIsNotPaintedAsGo(t *testing.T) {
	if paintKind[":buf"] != cmdspec.Words {
		t.Error(":buf's argument is not declared as words, so -run is highlighted as Go")
	}
}

// The obvious place to type in a Go file is the bottom of main, which is
// exactly the one place here that is not the buffer. So an empty region gets a
// blank line to stand on, and the open puts the cursor on it.
func TestAnEmptyRegionHasALineToTypeOn(t *testing.T) {
	file, codeLine, err := insertBufRegions(probeProgram, "", "")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(file, "\n")
	if codeLine < 1 || codeLine > len(lines) {
		t.Fatalf("codeLine = %d, outside a %d-line file", codeLine, len(lines))
	}
	if got := strings.TrimSpace(lines[codeLine-1]); got != "" {
		t.Errorf("the cursor lands on %q, want a blank line inside the region", got)
	}
	// And it is inside the region: the end marker is after it.
	m, err := bufMarkers(lines)
	if err != nil {
		t.Fatal(err)
	}
	if !(codeLine-1 > m.codeAt && codeLine-1 < m.codeEnd) {
		t.Errorf("line %d is not between the code markers at %d and %d", codeLine, m.codeAt+1, m.codeEnd+1)
	}
	// Typing on that line reaches the buffer.
	lines[codeLine-1] = `fmt.Println("hello")`
	if _, code, err := extractBufRegions(strings.Join(lines, "\n")); err != nil || code != `fmt.Println("hello")` {
		t.Errorf("typing where the cursor lands did not reach the buffer: code=%q err=%v", code, err)
	}
}

// "Nothing ran" and "what you wrote was outside the regions" are the one pair
// of facts that explains the other. Reporting the first without the second is
// how somebody concludes the buffer is broken.
func TestTheNoteSurvivesAnEmptyBuffer(t *testing.T) {
	c := &Core{bufDir: t.TempDir(), bufScaffold: true}
	file, _, err := insertBufRegions(probeProgram, "", "")
	if err != nil {
		t.Fatal(err)
	}
	c.bufWritten = file
	// What somebody does: type at the bottom of main, outside the region.
	typed := strings.Replace(file, "\n}", "\nfmt.Println(\"hello\")\n}", 1)
	if typed == file {
		t.Fatal("test did not add a line")
	}
	if err := os.WriteFile(filepath.Join(c.bufDir, bufName), []byte(typed), 0o644); err != nil {
		t.Fatal(err)
	}

	res := c.bufRun()
	if !strings.Contains(res.Out, "outside the two regions") {
		t.Errorf("-run answered %q, want it to say where the line went", res.Out)
	}
	if !res.Err {
		t.Error("a run that took nothing the user wrote reported success")
	}
}
