//go:build integration

package repl

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// builtinEditorConfig points config at a directory that asks for gluon's own
// editor, which is the only way the view is ever reached.
func builtinEditorConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "gluon"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gluon", "config.toml"),
		[]byte("editor = \"builtin\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// typeInto writes keystrokes with a gap, because a lone escape followed by
// another byte in the same read arrives as Alt on that key — the thing vimKey's
// comment is about, and an io.Pipe is one of the things that does it.
func typeInto(t *testing.T, pw io.Writer, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if _, err := pw.Write([]byte(k)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(60 * time.Millisecond)
	}
}

// The whole point of the change: write a line, run it, and read the answer
// without the editor closing.
func TestTheBuiltinEditorRunsWithoutClosing(t *testing.T) {
	builtinEditorConfig(t)

	core, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	if res := core.Submit("x := 21"); res.Err {
		t.Fatalf("seeding the session: %s", res.Out)
	}

	pr, pw := io.Pipe()
	out := &safeBuf{}
	p := tea.NewProgram(newModel(core, "test"),
		tea.WithInput(pr), tea.WithOutput(out), tea.WithoutSignalHandler())
	done := make(chan error, 1)
	go func() { _, err := p.Run(); done <- err }()
	defer func() { p.Kill(); pw.Close() }()

	typeInto(t, pw, ":edit\r")
	// The view is open when its own title is on screen. No editor process was
	// started: there is none to start.
	waitFor(t, out, "the session", 20*time.Second)

	mark := len(out.String())
	typeInto(t, pw, "G", "o", "fmt.Println(x * 2)", "\x1b")
	// Ctrl-E runs it. The answer lands in the pane, and the view is still up.
	typeInto(t, pw, "\x05")
	waitForAfter(t, out, mark, "42", 60*time.Second)

	after := out.String()
	if !strings.Contains(after[mark:], "reloaded (2 entries)") {
		t.Errorf("the run did not report the session it left:\n%s", after[mark:])
	}
	// Still open: the footer is what only an open view draws.
	if !strings.Contains(after[mark:], "check") {
		t.Errorf("the view closed when it ran; its footer is gone:\n%s", after[mark:])
	}

	// Running the same document again must leave the same session, not twice
	// as much of it. This is the idempotence :edit gets for free by replacing
	// the session rather than appending to it, and the reason running in place
	// is offered here and not for :buf.
	//
	// Asserted on the session after the program has stopped rather than on the
	// screen: the pane still holds the first run's answer and repaints it every
	// frame, so waiting for that text again would match the frame before the
	// second run had even started.
	typeInto(t, pw, "\x05")
	typeInto(t, pw, "ZQ")
	typeInto(t, pw, ":q\r")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program returned %v", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("the session did not quit")
	}
	if n := len(core.sess.Entries); n != 2 {
		t.Errorf("two runs of the same document left %d entries, want 2", n)
	}
}

// The check answers into the status line. tea.Println is a no-op under the alt
// screen, so a check that printed would be a check that said nothing at all.
func TestTheBuiltinEditorChecksWithoutRunning(t *testing.T) {
	builtinEditorConfig(t)

	core, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	if res := core.Submit("x := 21"); res.Err {
		t.Fatalf("seeding the session: %s", res.Out)
	}

	pr, pw := io.Pipe()
	out := &safeBuf{}
	p := tea.NewProgram(newModel(core, "test"),
		tea.WithInput(pr), tea.WithOutput(out), tea.WithoutSignalHandler())
	go func() { _, _ = p.Run() }()
	defer func() { p.Kill(); pw.Close() }()

	typeInto(t, pw, ":edit\r")
	waitFor(t, out, "the session", 20*time.Second)

	mark := len(out.String())
	typeInto(t, pw, "G", "o", "nosuchname(1)", "\x1b")
	typeInto(t, pw, "\x0b") // Ctrl-K
	waitForAfter(t, out, mark, "nosuchname", 60*time.Second)

	// The session is untouched: a check runs nothing and appends nothing.
	if n := len(core.sess.Entries); n != 1 {
		t.Errorf("the session has %d entries after a check, want the 1 it started with", n)
	}
}

// Closing without running leaves the session alone and says so in one line.
func TestClosingTheBuiltinEditorWithoutRunningKeepsTheSession(t *testing.T) {
	builtinEditorConfig(t)

	core, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	if res := core.Submit("x := 21"); res.Err {
		t.Fatalf("seeding the session: %s", res.Out)
	}

	pr, pw := io.Pipe()
	out := &safeBuf{}
	p := tea.NewProgram(newModel(core, "test"),
		tea.WithInput(pr), tea.WithOutput(out), tea.WithoutSignalHandler())
	go func() { _, _ = p.Run() }()
	defer func() { p.Kill(); pw.Close() }()

	typeInto(t, pw, ":edit\r")
	waitFor(t, out, "the session", 20*time.Second)

	mark := len(out.String())
	typeInto(t, pw, "G", "o", "fmt.Println(999)", "\x1b")
	typeInto(t, pw, "ZQ")
	waitForAfter(t, out, mark, "closed, nothing run", 20*time.Second)

	if n := len(core.sess.Entries); n != 1 {
		t.Errorf("the session has %d entries after ZQ, want the 1 it started with", n)
	}
	// Not asserted on the screen: the abandoned line is drawn in the editor
	// while it is open, so its text is in the output either way. The session is
	// the thing ZQ promises not to touch.
	for _, e := range core.sess.Entries {
		if strings.Contains(e.Src, "999") {
			t.Error("ZQ ran the document it was supposed to abandon")
		}
	}
}

// A scratchpad is the other surface running in place is offered on, and it is
// the one with something to lose: the pad file carries directives, and a pin is
// a decision the session holds that Classify has never seen. Reload routes a
// pad through the pad reader for exactly that reason, and the builtin editor
// has to reach the same path rather than a second one beside it.
func TestEditingAPadInTheBuiltinEditorKeepsItsPins(t *testing.T) {
	builtinEditorConfig(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	core, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	if res, _ := core.OpenPad("viewed"); res.Err {
		t.Fatal(res.Out)
	}
	if res := core.Submit("func double(n int) int { return n * 2 }"); res.Err {
		t.Fatalf("the declaration: %s", res.Out)
	}
	if res := core.Submit("x := double(21)"); res.Err {
		t.Fatalf("the statement: %s", res.Out)
	}
	// Entry 2, the statement: a declaration binds a name and runs nothing, so
	// there is nothing about it to pin.
	if res := core.Submit(":pin 2"); res.Err {
		t.Fatalf("pinning: %s", res.Out)
	}

	pr, pw := io.Pipe()
	out := &safeBuf{}
	p := tea.NewProgram(newModel(core, "test"),
		tea.WithInput(pr), tea.WithOutput(out), tea.WithoutSignalHandler())
	done := make(chan error, 1)
	go func() { _, err := p.Run(); done <- err }()
	defer func() { p.Kill(); pw.Close() }()

	typeInto(t, pw, ":scratch -edit\r")
	waitFor(t, out, "scratchpad viewed", 20*time.Second)

	mark := len(out.String())
	typeInto(t, pw, "G", "o", "fmt.Println(double(3))", "\x1b")
	typeInto(t, pw, "\x05")
	waitForAfter(t, out, mark, "6", 60*time.Second)

	typeInto(t, pw, "ZQ")
	typeInto(t, pw, ":q\r")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program returned %v", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("the session did not quit")
	}

	var pinned int
	for _, e := range core.sess.Entries {
		if e.Pinned {
			pinned++
		}
	}
	if pinned != 1 {
		t.Errorf("the pad has %d pinned entries after being run from the editor, want 1", pinned)
	}
	if n := len(core.sess.Entries); n != 3 {
		t.Errorf("the pad has %d entries, want the 2 it had plus the one written in", n)
	}
}

// The replay's own output used to be computed and then dropped on the floor:
// submitAll collected it, swapSession returned Result{} on success, and Reload
// answered with a receipt and nothing else. An editor that runs the session
// without closing itself has nothing else to show, so this is the assertion
// that keeps it.
func TestReloadReportsWhatTheSessionPrinted(t *testing.T) {
	core, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()

	path := filepath.Join(t.TempDir(), "gluon-edit.go")
	if err := os.WriteFile(path,
		[]byte("x := 21\nfmt.Println(x * 2)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := core.Reload(path)
	if res.Err {
		t.Fatalf("reload failed: %s", res.Out)
	}
	if !strings.Contains(res.Out, "42") {
		t.Errorf("the reload did not report what the session printed:\n%s", res.Out)
	}
	if !strings.Contains(res.Out, "reloaded (2 entries)") {
		t.Errorf("the reload lost its receipt:\n%s", res.Out)
	}
}

// :buf is the third surface, and the one the run key is deliberately not
// offered on: running it appends, so a second press would leave two copies of
// everything. ZZ still runs it, once.
func TestTheBuiltinEditorRunsTheBufferOnlyOnTheWayOut(t *testing.T) {
	builtinEditorConfig(t)

	core, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()

	pr, pw := io.Pipe()
	out := &safeBuf{}
	p := tea.NewProgram(newModel(core, "test"),
		tea.WithInput(pr), tea.WithOutput(out), tea.WithoutSignalHandler())
	done := make(chan error, 1)
	go func() { _, err := p.Run(); done <- err }()
	defer func() { p.Kill(); pw.Close() }()

	typeInto(t, pw, ":buf\r")
	waitFor(t, out, "the buffer", 20*time.Second)

	mark := len(out.String())
	typeInto(t, pw, "ofmt.Println(6 * 7)", "\x1b")
	// The run key is not offered here, and says so rather than doing nothing.
	typeInto(t, pw, "\x05")
	waitForAfter(t, out, mark, "ZZ runs it once", 20*time.Second)
	if n := len(core.sess.Entries); n != 0 {
		t.Fatalf("the run key evaluated something: %d entries", n)
	}

	// ZZ does run it, and the answer reaches scrollback the way an evaluation
	// does rather than a pane that is being torn down.
	typeInto(t, pw, "ZZ")
	waitForAfter(t, out, mark, "42", 60*time.Second)

	typeInto(t, pw, ":q\r")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program returned %v", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("the session did not quit")
	}
	if n := len(core.sess.Entries); n != 1 {
		t.Errorf("the buffer left %d entries, want 1", n)
	}
}
