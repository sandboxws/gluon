//go:build integration

package repl

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// safeBuf lets the test read the renderer's output while the program writes it.
type safeBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// waitForAfter polls until want appears in output written after mark.
//
// Scrollback makes plain waitFor useless for anything the session has already
// echoed: waiting for "appleCount" matches the line that bound it and returns
// immediately, letting the next keystrokes race the ones being tested.
func waitForAfter(t *testing.T, buf *safeBuf, mark int, want string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if s := buf.String(); len(s) > mark && strings.Contains(s[mark:], want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q after offset %d:\n%s", want, mark, buf.String())
}

// waitFor polls until the rendered output contains want.
func waitFor(t *testing.T, buf *safeBuf, want string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q in output:\n%s", want, buf.String())
}

// TestProgramEndToEnd drives a real tea.Program: keystrokes in, evaluation on a
// background command, results printed above the prompt. A pty harness cannot
// test this (bubbletea's cursor-position query goes unanswered and swallows the
// input), so the program is driven through a pipe instead.
func TestProgramEndToEnd(t *testing.T) {
	// NewCore reads the real user config, and this test asserts on the shape a
	// value is drawn in — so a developer whose own config sets [value.form]
	// fails it while CI, which has no config file, passes. An empty config
	// directory is what makes the assertions about the defaults true.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	core, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()

	pr, pw := io.Pipe()
	out := &safeBuf{}
	p := tea.NewProgram(
		newModel(core, "test"),
		tea.WithInput(pr),
		tea.WithOutput(out),
		tea.WithoutSignalHandler(),
	)

	done := make(chan error, 1)
	go func() { _, err := p.Run(); done <- err }()

	// \r is what Enter delivers.
	if _, err := pw.Write([]byte("1+1\r")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, out, "(int) 2", 20*time.Second)

	// A second line proves the session survives and keeps evaluating.
	if _, err := pw.Write([]byte("x := []int{3,1,2}\r")); err != nil {
		t.Fatal(err)
	}
	if _, err := pw.Write([]byte("slices.Sort(x)\r")); err != nil {
		t.Fatal(err)
	}
	if _, err := pw.Write([]byte("x\r")); err != nil {
		t.Fatal(err)
	}
	// A slice renders as a table in the terminal, so assert on the table's
	// content rather than the plain one-line form pipes get.
	waitFor(t, out, "value", 20*time.Second)
	for _, want := range []string{"[]int", "len=3 cap=3", "│ 0 │ 1", "│ 2 │ 3"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in rendered table:\n%s", want, out.String())
		}
	}

	if _, err := pw.Write([]byte(":q\r")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program returned %v", err)
		}
	case <-time.After(10 * time.Second):
		p.Kill()
		t.Fatal(":q did not quit the program")
	}
	pw.Close()
}

// A construct spanning several lines must not evaluate until it closes.
func TestProgramMultiLine(t *testing.T) {
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

	pw.Write([]byte("func double(n int) int {\r"))
	pw.Write([]byte("return n * 2\r"))
	pw.Write([]byte("}\r"))
	pw.Write([]byte("double(21)\r"))
	waitFor(t, out, "(int) 42", 20*time.Second)

	pw.Write([]byte(":q\r"))
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		p.Kill()
		t.Fatal(":q did not quit")
	}
	pw.Close()
}

// TestProgramBracketedPaste sends a real bracketed-paste sequence — the wire
// format a terminal emits — and checks the block is split into lines instead
// of collapsing onto one.
func TestProgramBracketedPaste(t *testing.T) {
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

	const (
		bpStart = "\x1b[200~"
		bpEnd   = "\x1b[201~"
	)
	// A multi-line construct plus a following expression, exactly as it would
	// arrive from a terminal paste.
	paste := "func double(n int) int {\n\treturn n * 2\n}\ndouble(21)\n"
	if _, err := pw.Write([]byte(bpStart + paste + bpEnd)); err != nil {
		t.Fatal(err)
	}

	waitFor(t, out, "(int) 42", 30*time.Second)

	// The construct must have been echoed across several lines, not flattened.
	if got := out.String(); !strings.Contains(got, "...>") {
		t.Errorf("expected a continuation prompt from the pasted block:\n%s", got)
	}

	pw.Write([]byte(":q\r"))
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		p.Kill()
		t.Fatal(":q did not quit")
	}
	pw.Close()
}

// Ghost text and tab-accept, driven through a real tea.Program. The engine is
// unit-tested elsewhere; what this covers is the wiring — that suggestions
// reach the input, render as completion text, and that tab commits them.
func TestProgramCompletion(t *testing.T) {
	defaultConfig(t)

	core, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()

	pr, pw := io.Pipe()
	out := &safeBuf{}
	p := tea.NewProgram(
		newModel(core, "test"),
		tea.WithInput(pr),
		tea.WithOutput(out),
		tea.WithoutSignalHandler(),
	)
	done := make(chan error, 1)
	go func() { _, err := p.Run(); done <- err }()

	// Bind something with a distinctive name, so what comes back can only be
	// a completion of it.
	if _, err := pw.Write([]byte("appleCount := 7\r")); err != nil {
		t.Fatal(err)
	}
	// A binding prints nothing, and the prompt is rendered from the start, so
	// neither is a sync point. Evaluate something that does print, and wait for
	// that — otherwise the keystrokes below land while Core is busy, which is
	// exactly when it declines to complete.
	if _, err := pw.Write([]byte("\"ready\"\r")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, out, `ready`, 20*time.Second)

	// Typing a prefix should render the rest as ghost text without accepting
	// it. Only output written from here on counts.
	mark := len(out.String())
	if _, err := pw.Write([]byte("appleCo")); err != nil {
		t.Fatal(err)
	}
	waitForAfter(t, out, mark, "appleCount", 20*time.Second)

	// Tab accepts it, and Enter then evaluates the completed line.
	if _, err := pw.Write([]byte("\t\r")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, out, "(int) 7", 20*time.Second)

	if _, err := pw.Write([]byte(":q\r")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program returned %v", err)
		}
	case <-time.After(10 * time.Second):
		p.Kill()
		t.Fatal(":q did not quit the program")
	}
	pw.Close()
}

// TestProgramThemePicker drives the whole path a person takes: `:theme` opens
// the chooser, a keystroke previews another palette, enter keeps it, and the
// choice is on disk when the session ends.
//
// It runs against a real tea.Program because the interesting parts are the
// seams — the alt screen, the keyboard changing hands, and a meta command
// started by the view rather than by a typed line.
func TestProgramThemePicker(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfgPath := filepath.Join(dir, "gluon", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("# keep me\nimports = [\"fmt\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	core, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()

	// What one press of j lands on, asked before the program starts rather than
	// written down here. The list is every installed theme, so a test that
	// named one would be a test of which themes gluon ships.
	spec := core.themeSpec("")
	if len(spec.Choices) < 2 {
		t.Skip("only one theme installed, so there is nothing to move to")
	}
	at := 0
	for i, ch := range spec.Choices {
		if ch.Name == spec.Active {
			at = i
		}
	}
	next := spec.Choices[(at+1)%len(spec.Choices)]

	pr, pw := io.Pipe()
	out := &safeBuf{}
	p := tea.NewProgram(newModel(core, "test"),
		tea.WithInput(pr), tea.WithOutput(out), tea.WithoutSignalHandler())

	done := make(chan error, 1)
	go func() { _, err := p.Run(); done <- err }()

	pw.Write([]byte(":theme\r"))
	// The picker is up when its footer is: it is the one line no other view
	// prints.
	waitFor(t, out, "esc", 20*time.Second)
	for _, want := range []string{"themes", spec.Active, "gluon>"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the picker did not draw %q:\n%s", want, out.String())
		}
	}

	// Down one, which repaints the session and retitles the view, then keep it.
	// The title is the sync point, and it is waited on by its first few
	// characters: `cut` trims it to the window, so a long `about` line is not
	// there to wait for.
	mark := len(out.String())
	pw.Write([]byte("j"))
	waitForAfter(t, out, mark, next.Name+" · ", 20*time.Second)
	pw.Write([]byte("\r"))
	waitFor(t, out, "theme is "+next.Name, 20*time.Second)

	body, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `name = "`+next.Name+`"`) {
		t.Errorf("the choice was not written down:\n%s", body)
	}
	if !strings.Contains(string(body), "# keep me") {
		t.Errorf("the write lost what was already in the config:\n%s", body)
	}

	pw.Write([]byte(":q\r"))
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program returned %v", err)
		}
	case <-time.After(10 * time.Second):
		p.Kill()
		t.Fatal(":q did not quit the program")
	}
	pw.Close()
}

// defaultConfig points the config at an empty directory, so that a test about
// something else is not also a test of whatever the person running it keeps in
// their own config.toml. A developer with `mode = "vim"` set reads back a
// prompt that says `gluon[i]>`, where CI, with no config at all, reads
// `gluon>`. Neither cursor adds a character to a scraped screen.
func defaultConfig(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

// vimConfig writes a config that turns modal editing on, into a config
// directory this test owns.
func vimConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "gluon", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[input]\nmode = \"vim\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestVimModeEditsTheLineBeforeItRuns drives a real tea.Program with modal
// editing on: type a line, leave insert, fix it in normal mode, and submit.
//
// The escape goes in its own write with a pause after it, deliberately. A lone
// escape followed by another byte in the same read is decoded as Alt on that
// key rather than as KeyEsc — which the dispatcher handles, but which would
// make this test assert the batched path while claiming to assert the ordinary
// one.
func TestVimModeEditsTheLineBeforeItRuns(t *testing.T) {
	vimConfig(t)

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

	// A line with one word too many at the end of it.
	pw.Write([]byte("2 + 2 + 99"))
	time.Sleep(200 * time.Millisecond)
	pw.Write([]byte{0x1b}) // escape, alone
	time.Sleep(200 * time.Millisecond)
	// $ to the last character, b to the start of 99, two h back onto the +,
	// then D takes the rest of the line — leaving "2 + 2 ".
	pw.Write([]byte("$bhhD"))
	time.Sleep(200 * time.Millisecond)
	pw.Write([]byte("\r"))

	waitFor(t, out, "(int) 4", 20*time.Second)

	pw.Write([]byte(":q\r"))
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		p.Kill()
		t.Fatal(":q did not quit the program")
	}
	pw.Close()
}

// TestTheModeReachesTheTerminal: "which mode am I in" has to be answerable
// from the screen, because a swallowed key with no answer is a wedged prompt.
func TestTheModeReachesTheTerminal(t *testing.T) {
	vimConfig(t)

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

	p.Send(tea.WindowSizeMsg{Width: 100, Height: 24})
	pw.Write([]byte("x := 1"))
	time.Sleep(200 * time.Millisecond)
	mark := len(out.String())
	pw.Write([]byte{0x1b})

	waitForAfter(t, out, mark, "gluon[n]>", 10*time.Second)

	// dd empties the line, which is exactly when somebody needs to know which
	// mode they are in — and the mode is in the prompt, which is drawn whether
	// there is a line or not. The output here is not a terminal, so this is
	// also the uncoloured path: the marker is present without colour carrying
	// it.
	mark = len(out.String())
	pw.Write([]byte("dd"))
	waitForAfter(t, out, mark, "gluon[n]>", 10*time.Second)

	// And back: i returns to insert, and the prompt says so.
	mark = len(out.String())
	pw.Write([]byte("i"))
	waitForAfter(t, out, mark, "gluon[i]>", 10*time.Second)

	pw.Write([]byte("\x03")) // ctrl-c abandons, and comes back in insert
	time.Sleep(200 * time.Millisecond)
	pw.Write([]byte(":q\r"))
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		p.Kill()
		t.Fatal(":q did not quit the program")
	}
	pw.Close()
}
