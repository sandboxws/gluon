package state

import (
	"os"
	"path/filepath"
	"testing"
)

// XDG_STATE_HOME winning is what makes every caller testable: a test points the
// whole tree at a temp directory and nothing touches the real ~/.local/state.
func TestDirHonoursXDGStateHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)

	dir := Dir()
	if want := filepath.Join(tmp, "gluon"); dir != want {
		t.Fatalf("Dir() = %q, want %q", dir, want)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Dir() did not create it: %v", err)
	}
	if !fi.IsDir() {
		t.Fatalf("%s is not a directory", dir)
	}
}

func TestFileIsUnderDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)

	if got, want := File("history"), filepath.Join(tmp, "gluon", "history"); got != want {
		t.Errorf("File(history) = %q, want %q", got, want)
	}
}

// An unset XDG_STATE_HOME falls back to ~/.local/state rather than to the
// working directory, which is the whole reason this is not just filepath.Join.
func TestDirFallsBackToLocalState(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got, want := Dir(), filepath.Join(home, ".local", "state", "gluon"); got != want {
		t.Errorf("Dir() = %q, want %q", got, want)
	}
}

// "" rather than an error, because every caller's fallback is to do without
// the file: a state directory that cannot exist must not stop a REPL starting.
func TestFileIsEmptyWhenThereIsNoStateDir(t *testing.T) {
	tmp := t.TempDir()
	blocked := filepath.Join(tmp, "not-a-dir")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", blocked)

	if got := File("history"); got != "" {
		t.Errorf("File(history) = %q, want \"\"", got)
	}
}
