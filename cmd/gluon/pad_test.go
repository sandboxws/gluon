package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/scratch"
	"github.com/sandboxws/gluon/internal/session"
)

// writePad puts a scratchpad on disk the way a REPL session would have left
// one, without needing a REPL to do it.
func writeTestPad(t *testing.T, name string, srcs ...string) *scratch.Pad {
	t.Helper()
	p := &scratch.Pad{Name: name, Dir: scratch.PadDir(name), Sess: &session.Session{}}
	for _, src := range srcs {
		e, err := session.Classify(src)
		if err != nil {
			t.Fatalf("Classify(%q): %v", src, err)
		}
		p.Sess.Append(e)
	}
	if err := scratch.WritePad(p); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestScratchFlagTakesBothSpellings: -scratch is a long flag, so longFlagNames
// collects it and normalizeArgs rewrites it with no entry of its own —
// invariant 20, which is what keeps every single-dash invocation in the docs
// true without a list to maintain.
func TestScratchFlagTakesBothSpellings(t *testing.T) {
	root := newRootCmd()
	cases := []struct {
		in   []string
		want []string
	}{
		{[]string{"-scratch", "parser-bug"}, []string{"--scratch", "parser-bug"}},
		{[]string{"--scratch", "parser-bug"}, []string{"--scratch", "parser-bug"}},
		{[]string{"-no-scratch"}, []string{"--no-scratch"}},
		{[]string{"-scratch=notes"}, []string{"--scratch=notes"}},
	}
	for _, tc := range cases {
		got := normalizeArgs(root, tc.in)
		if strings.Join(got, " ") != strings.Join(tc.want, " ") {
			t.Errorf("normalizeArgs(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestShowEvaluatesNothing: reopening a scratchpad replays it, so there has to
// be a way to read one that does not. There is no evaluator in this process at
// all, which is what makes that true rather than intended.
func TestShowEvaluatesNothing(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	// An entry that would be obvious if it ran.
	marker := filepath.Join(data, "it-ran")
	writeTestPad(t, "effects", "os.WriteFile(`"+marker+"`, nil, 0o644)")

	out := captureStdout(t, func() {
		if code := showPad("effects"); code != 0 {
			t.Fatalf("gluon scratch show exited %d", code)
		}
	})
	if !strings.Contains(out, marker) {
		t.Errorf("the entry was not printed:\n%s", out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("gluon scratch show evaluated the scratchpad: %v", err)
	}
}

func TestScratchListsWhatIsThere(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	writeTestPad(t, "one", "x := 1")
	writeTestPad(t, "two", "y := 2", "y + 1")

	out := captureStdout(t, func() {
		if code := listPads(); code != 0 {
			t.Fatalf("gluon scratch exited %d", code)
		}
	})
	for _, want := range []string{"one", "1 entry", "two", "2 entries"} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing is missing %q:\n%s", want, out)
		}
	}
}

// TestRemovingWithoutATerminalRefuses: a removal that happened because nobody
// could be asked would be worse than one that did not happen.
func TestRemovingWithoutATerminalRefuses(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	writeTestPad(t, "keepme", "x := 1")

	if code := removePad("keepme", false); code == 0 {
		t.Error("a scratchpad was removed with nothing to confirm on")
	}
	if !scratch.IsPad(scratch.PadDir("keepme")) {
		t.Fatal("the scratchpad was removed")
	}
	if code := removePad("keepme", true); code != 0 {
		t.Fatalf("-force exited %d", code)
	}
	if scratch.IsPad(scratch.PadDir("keepme")) {
		t.Error("-force did not remove it")
	}
}

func TestDoctorReportsThePadRoot(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	writeTestPad(t, "notes", "x := 1")

	rep := collectDoctor(t.TempDir())
	if rep.Scratch == nil {
		t.Fatal("doctor reports nothing about the scratchpads")
	}
	if rep.Scratch.Root != scratch.Root() {
		t.Errorf("root = %q, want %q", rep.Scratch.Root, scratch.Root())
	}
	if rep.Scratch.Pads != 1 {
		t.Errorf("pads = %d, want 1", rep.Scratch.Pads)
	}
}
