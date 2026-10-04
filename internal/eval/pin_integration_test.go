//go:build integration

package eval

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/sandboxws/gluon/internal/session"
)

// The point of :pin is that replay stops re-running an entry. Nothing about
// the rendered program text proves that — only running it does. So this test
// counts a real side effect: one byte appended to a file every time the entry
// executes.
func TestPinnedEntryStopsRepeatingItsSideEffect(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}

	// Two runs of the same session, differing only in whether entry 2 is
	// pinned after it has run once. The control is what makes the number
	// meaningful: without it, "1 byte" could just mean the append never worked.
	control := sideEffectCount(t, false)
	pinned := sideEffectCount(t, true)

	if control < 2 {
		t.Fatalf("control did not replay the side effect (%d appends) — "+
			"the test can no longer tell pinning from doing nothing", control)
	}
	if pinned != 1 {
		t.Errorf("pinned entry ran %d times, want exactly 1 (the run before it was pinned)", pinned)
	}
}

// sideEffectCount runs a four-entry session and returns how many times the
// side-effecting entry actually executed. When pin is set, entry 2 is pinned
// as soon as it has run once.
func sideEffectCount(t *testing.T, pin bool) int {
	t.Helper()
	path := filepath.Join(t.TempDir(), "appends")

	ev, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ev.Close() })

	// os.Getpid() is deliberately not a constant: a constant-folding entry is
	// answered by the type checker without building or running, so it would
	// not replay the prefix at all and the test would pass for the wrong
	// reason.
	srcs := []string{
		fmt.Sprintf(`func bump() { f, _ := os.OpenFile(%q, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); f.WriteString("x"); f.Close() }`, path),
		`bump()`,
		`os.Getpid()`,
		`os.Getpid()`,
	}

	s := &session.Session{}
	for i, src := range srcs {
		e, err := session.Classify(src)
		if err != nil {
			t.Fatalf("Classify(%q): %v", src, err)
		}
		s.Append(e)
		if _, err := ev.Eval(s); err != nil {
			t.Fatalf("Eval(%q): %v", src, err)
		}
		if pin && i == 1 {
			s.Entries[i].Pinned = true
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("side effect never happened at all: %v", err)
	}
	return len(data)
}
