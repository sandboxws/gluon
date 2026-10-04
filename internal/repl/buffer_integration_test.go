//go:build integration

package repl

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/eval"
)

// fakeEditor writes text into the buffer's two regions the way a person would,
// and is what the driver would hand the terminal to. A real editor cannot be
// driven from a test — ROADMAP's "Testing the UI" says why a pty harness
// cannot reach this UI at all — so the round trip is exercised through the
// same file the editor would have been given.
func editRegions(t *testing.T, path, decls, code string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	file := string(data)
	put := func(file, marker, body string) string {
		lines := strings.Split(file, "\n")
		var out []string
		for i := 0; i < len(lines); i++ {
			out = append(out, lines[i])
			if !strings.HasPrefix(strings.TrimSpace(lines[i]), marker) {
				continue
			}
			if body != "" {
				out = append(out, strings.Split(body, "\n")...)
			}
			// Skip whatever was between this marker and its end.
			for i++; i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), bufEndMarker); i++ {
			}
			if i < len(lines) {
				out = append(out, lines[i])
			}
		}
		return strings.Join(out, "\n")
	}
	file = put(file, bufDeclsMarker, decls)
	file = put(file, bufCodeMarker, code)
	if err := os.WriteFile(path, []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
}

func bufCore(t *testing.T, lines ...string) *Core {
	t.Helper()
	c, err := NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	t.Cleanup(func() { c.Close() })
	for _, l := range lines {
		if res := c.Submit(l); res.Err {
			t.Fatalf("%s: %s", l, res.Out)
		}
	}
	return c
}

func TestBufRoundTripThroughARealEditor(t *testing.T) {
	c := bufCore(t, "x := 21")

	res := c.Submit(":buf")
	if res.Edit == "" {
		t.Fatalf("no file to open: %q", res.Out)
	}
	if res.EditThen != ":buf -run" {
		t.Fatalf("EditThen = %q", res.EditThen)
	}

	// What the editor does.
	editRegions(t, res.Edit, "func double(n int) int { return n * 2 }", "double(x)")

	// What the driver does when the editor exits.
	got := c.Submit(res.EditThen)
	if got.Err {
		t.Fatalf(":buf -run: %s", got.Out)
	}
	if !strings.Contains(got.Out, "42") {
		t.Errorf("-run answered %q, want 42", got.Out)
	}

	// Both constructs are in the session now, and the buffer still holds them.
	if n := len(c.sess.Entries); n != 3 {
		t.Errorf("the session has %d entries, want 3", n)
	}
	if !strings.Contains(c.buf, "func double") || !strings.Contains(c.buf, "double(x)") {
		t.Errorf("the buffer lost what was written: %q", c.buf)
	}
}

// The buffer is a place to come back to, so reopening hands the editor what was
// left in it rather than an empty region.
func TestReopeningTheBufferHandsBackWhatWasWritten(t *testing.T) {
	c := bufCore(t, "x := 1")

	res := c.Submit(":buf")
	editRegions(t, res.Edit, "", "y := x + 1")
	if got := c.Submit(":buf -run"); got.Err {
		t.Fatalf("-run: %s", got.Out)
	}

	again := c.Submit(":buf")
	if again.Edit != res.Edit {
		t.Errorf("reopened %q, want the same path %q", again.Edit, res.Edit)
	}
	data, err := os.ReadFile(again.Edit)
	if err != nil {
		t.Fatal(err)
	}
	if _, code, err := extractBufRegions(string(data)); err != nil || !strings.Contains(code, "y := x + 1") {
		t.Errorf("reopening handed back code=%q err=%v", code, err)
	}
}

// The file the editor is given has to compile on its own, or the user's
// language server is useless on it — which is the whole argument for handing
// the terminal to an editor they already configured.
//
// Built with -o into a temp path, from a cwd that is not the repository:
// invariant 1, and the assertion below that the tree is unchanged is the other
// half of it.
func TestTheBufferModuleCompiles(t *testing.T) {
	c := bufCore(t, "x := 21", "func double(n int) int { return n * 2 }", "double(x)")

	res := c.Submit(":buf")
	editRegions(t, res.Edit, "func triple(n int) int { return n * 3 }", "y := triple(x)\n_ = y")

	out := filepath.Join(t.TempDir(), "prog")
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = c.bufDir
	if b, err := cmd.CombinedOutput(); err != nil {
		data, _ := os.ReadFile(res.Edit)
		t.Fatalf("the scaffolded module does not compile: %v\n%s\n--- file ---\n%s", err, b, data)
	}
}

// A buffer is one evaluation. Four constructs costing four builds would make it
// a slower way to type four lines, which is the opposite of the point.
func TestABufferIsOneBuildNotOnePerConstruct(t *testing.T) {
	c := bufCore(t, "x := 1")

	res := c.Submit(":buf")
	editRegions(t, res.Edit, "", "a := x + 1\nb := a + 1\nc := b + 1\nc")

	eval.Timing(true)
	t.Cleanup(func() { eval.Timing(false) })
	eval.TakePhases()

	if got := c.Submit(":buf -run"); got.Err {
		t.Fatalf("-run: %s", got.Out)
	}
	builds := 0
	for _, p := range eval.TakePhases() {
		if p.Name == "build" {
			builds++
		}
	}
	if builds != 1 {
		t.Errorf("a four-construct buffer cost %d builds, want 1", builds)
	}
}

// Invariant 14, and the reason Analyze snapshots the same two fields: a check
// must leave the evaluator exactly as it found it, or the next ordinary line
// fails to build with "imported and not used".
func TestCheckBlockLeavesTheSessionExactlyAsItFoundIt(t *testing.T) {
	c := bufCore(t, "x := 1")
	before := len(c.sess.Entries)

	c.buf = "strings.ToUpper(\"a\")\n"
	if got := c.Submit(":buf -check"); got.Err {
		t.Fatalf("-check: %s", got.Out)
	}
	if n := len(c.sess.Entries); n != before {
		t.Errorf("the session has %d entries, want the %d it had", n, before)
	}
	// The import the check pulled in must not be left behind for the next line.
	if got := c.Submit("x + 1"); got.Err {
		t.Errorf("the line after a check failed: %s", got.Out)
	}
}

func TestBufCheckPlacesAnErrorOnTheBufferLine(t *testing.T) {
	c := bufCore(t, "x := 1")

	res := c.Submit(":buf")
	editRegions(t, res.Edit, "", "a := x + 1\nb := nope\n_ = b")

	got := c.Submit(":buf -check")
	if !got.Err {
		t.Fatalf("-check accepted an undefined name: %q", got.Out)
	}
	if !strings.Contains(got.Out, "undefined: nope") {
		t.Errorf("-check answered %q", got.Out)
	}
	// The declarations region is empty, so the code starts the buffer and the
	// second line is where the mistake is.
	if !strings.Contains(got.Out, "buffer:2") {
		t.Errorf("-check answered %q, want it to name buffer line 2", got.Out)
	}
}

func TestBufCheckOnACleanBufferSaysSo(t *testing.T) {
	c := bufCore(t, "x := 1")
	res := c.Submit(":buf")
	editRegions(t, res.Edit, "", "a := x + 1\n_ = a")

	got := c.Submit(":buf -check")
	if got.Err {
		t.Fatalf("-check rejected a clean buffer: %s", got.Out)
	}
	if !strings.Contains(got.Out, "no errors") {
		t.Errorf("-check answered %q", got.Out)
	}
}

// Invariant 5. With the checker off there is no verdict to give, and reporting
// the buffer as either good or bad would be the checker claiming an authority
// it does not have.
func TestBufCheckNeverClaimsAVerdictItCannotHave(t *testing.T) {
	t.Setenv("GLUON_NO_TYPECHECK", "1")
	c := bufCore(t, "x := 1")

	c.buf = "y := nope\n"
	got := c.Submit(":buf -check")
	if !strings.Contains(got.Out, "could not answer") {
		t.Errorf("-check with no checker answered %q, want it to say so", got.Out)
	}
	if strings.Contains(got.Out, "undefined") {
		t.Errorf("-check with no checker gave a verdict anyway: %q", got.Out)
	}
}

// Running does not consult the checker at all: the compiler decides, exactly as
// it does for a line typed at the prompt.
func TestBufRunBuildsEvenWhenTheCheckerIsOff(t *testing.T) {
	t.Setenv("GLUON_NO_TYPECHECK", "1")
	c := bufCore(t, "x := 21")

	c.buf = "x * 2\n"
	got := c.Submit(":buf -run")
	if got.Err {
		t.Fatalf("-run with no checker: %s", got.Out)
	}
	if !strings.Contains(got.Out, "42") {
		t.Errorf("-run answered %q, want 42", got.Out)
	}
}

// A file whose markers have been mangled must report that rather than guess.
// Reading it anyway would take the rendered session — line directives, printer
// wrappers and all — as the thing somebody meant to write.
func TestAMangledFileIsRefusedAndNothingIsRun(t *testing.T) {
	c := bufCore(t, "x := 1")
	res := c.Submit(":buf")
	editRegions(t, res.Edit, "", "y := x + 1")

	data, err := os.ReadFile(res.Edit)
	if err != nil {
		t.Fatal(err)
	}
	mangled := strings.Replace(string(data), bufCodeNote+"\n", "", 1)
	if err := os.WriteFile(res.Edit, []byte(mangled), 0o644); err != nil {
		t.Fatal(err)
	}

	before := len(c.sess.Entries)
	got := c.Submit(":buf -run")
	if !got.Err || !strings.Contains(got.Out, "markers") {
		t.Errorf("-run on a mangled file answered %q", got.Out)
	}
	if n := len(c.sess.Entries); n != before {
		t.Errorf("the session grew to %d entries from a file that was refused", n)
	}
	if _, err := os.Stat(res.Edit); err != nil {
		t.Errorf("the refused file was not left alone: %v", err)
	}
}

// An edit to the context is dropped — but never in silence. A preamble edit
// that vanished without a word is the one thing this shape must not do.
func TestAnEditOutsideTheRegionsIsReportedRatherThanSwallowed(t *testing.T) {
	c := bufCore(t, "x := 1")
	res := c.Submit(":buf")
	editRegions(t, res.Edit, "", "y := x + 1")

	data, err := os.ReadFile(res.Edit)
	if err != nil {
		t.Fatal(err)
	}
	touched := strings.Replace(string(data), "func main() {", "func main() { // mine now", 1)
	if touched == string(data) {
		t.Fatal("test did not edit the context")
	}
	if err := os.WriteFile(res.Edit, []byte(touched), 0o644); err != nil {
		t.Fatal(err)
	}

	got := c.Submit(":buf -run")
	if !strings.Contains(got.Out, "outside the two regions") {
		t.Errorf("-run answered %q, want the dropped edit named", got.Out)
	}
	if !strings.Contains(c.buf, "y := x + 1") {
		t.Errorf("the buffer lost the region's own text: %q", c.buf)
	}
}

// Edit, check, edit: a buffer that will not compile reopens on the construct
// that would not, so the loop costs one keystroke rather than a hunt. A clean
// answer clears it, which is what makes the loop stop.
func TestARefusedRunReopensOnTheOffendingLine(t *testing.T) {
	c := bufCore(t, "x := 1")

	res := c.Submit(":buf")
	editRegions(t, res.Edit, "", "a := x + 1\nb := nope\n_ = b")

	if got := c.Submit(":buf -run"); !got.Err {
		t.Fatalf("-run accepted an undefined name: %q", got.Out)
	}
	again := c.Submit(":buf")
	if again.EditLine != 2 {
		t.Errorf("reopened on line %d, want 2 — the line that would not compile", again.EditLine)
	}

	// Fixed, and the loop stops: the next open is no longer pointed at a
	// mistake that is not there any more, so the cursor goes where typing works
	// instead — the first line of the statements region.
	editRegions(t, again.Edit, "", "a := x + 1\nb := a + 1\n_ = b")
	if got := c.Submit(":buf -run"); got.Err {
		t.Fatalf("-run after the fix: %s", got.Out)
	}
	third := c.Submit(":buf")
	if third.EditLine == 2 {
		t.Error("reopened on the line that used to be wrong, after a clean run")
	}
	data, err := os.ReadFile(third.Edit)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	m, merr := bufMarkers(lines)
	if merr != nil {
		t.Fatal(merr)
	}
	if !(third.EditLine-1 > m.codeAt && third.EditLine-1 <= m.codeEnd) {
		t.Errorf("reopened on line %d, want it inside the statements region (%d..%d)",
			third.EditLine, m.codeAt+2, m.codeEnd+1)
	}
}

// The same, from -check rather than -run: the fast answer places the line too.
func TestACheckAlsoPlacesTheNextOpen(t *testing.T) {
	c := bufCore(t, "x := 1")
	res := c.Submit(":buf")
	editRegions(t, res.Edit, "func f() int {\n\treturn nope\n}", "f()")

	if got := c.Submit(":buf -check"); !got.Err {
		t.Fatalf("-check accepted an undefined name: %q", got.Out)
	}
	// The declarations region comes first, so the func starts on buffer line 1
	// and its own second line is buffer line 2.
	if again := c.Submit(":buf"); again.EditLine != 2 {
		t.Errorf("reopened on line %d, want 2", again.EditLine)
	}
}
