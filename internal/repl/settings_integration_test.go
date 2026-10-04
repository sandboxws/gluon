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

// TestProgramSettingsModalAndForm drives a real Bubble Tea program, because the
// two claims this feature makes about the terminal cannot be made anywhere
// else: that `:settings` opens the modal gluon already has rather than a widget
// written for it, and that changing a form repaints the running session.
//
// It also holds invariant 19 at the point it actually applies — nothing printed
// while the modal is up, exactly one line left behind when it closes.
func TestProgramSettingsModalAndForm(t *testing.T) {
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

	pr, pw := io.Pipe()
	out := &safeBuf{}
	p := tea.NewProgram(newModel(core, "test"),
		tea.WithInput(pr), tea.WithOutput(out), tea.WithoutSignalHandler())

	done := make(chan error, 1)
	go func() { _, err := p.Run(); done <- err }()

	// A value first, drawn in the form gluon has always drawn.
	pw.Write([]byte("[]int{1, 2, 3}\r"))
	waitFor(t, out, "value", 60*time.Second)
	if !strings.Contains(out.String(), "╭") {
		t.Errorf("the default form did not draw a table:\n%s", out.String())
	}

	// The listing opens the modal rather than printing the wide table.
	mark := len(out.String())
	pw.Write([]byte(":settings\r"))
	waitForAfter(t, out, mark, "value.form", 20*time.Second)
	for _, want := range []string{"setting", "allowed", "timeout"} {
		if !strings.Contains(out.String()[mark:], want) {
			t.Errorf("the settings modal did not draw %q:\n%s", want, out.String()[mark:])
		}
	}

	// Closing puts the prompt back. That the summary line survives into
	// scrollback is invariant 19's other half, and it is asserted at the model
	// level by TestTheSettingsModalClosesWithOneSummaryLine — tea.Println is
	// batched with tea.ExitAltScreen, so which of the two reaches a captured
	// byte stream first is not something this harness can pin.
	mark = len(out.String())
	pw.Write([]byte("q"))
	waitForAfter(t, out, mark, "gluon>", 20*time.Second)

	// Change the form, and the next value comes back in it.
	mark = len(out.String())
	pw.Write([]byte(":settings value.form tree\r"))
	waitForAfter(t, out, mark, "value.form is tree", 20*time.Second)

	mark = len(out.String())
	pw.Write([]byte("[]int{1, 2, 3}\r"))
	waitForAfter(t, out, mark, "└", 60*time.Second)
	if got := out.String()[mark:]; strings.Contains(got, "╭") {
		t.Errorf("the session still drew a table after the form changed:\n%s", got)
	}

	body, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)
	if !strings.Contains(src, `default = "tree"`) {
		t.Errorf("the form did not reach the config:\n%s", src)
	}
	for _, keep := range []string{"# keep me", `imports = ["fmt"]`} {
		if !strings.Contains(src, keep) {
			t.Errorf("the write lost %q:\n%s", keep, src)
		}
	}

	pw.Write([]byte(":q\r"))
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		p.Kill()
		t.Fatal("the program did not quit")
	}
}
