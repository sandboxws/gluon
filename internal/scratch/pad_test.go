package scratch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sandboxws/gluon/internal/session"
)

func padWith(t *testing.T, name string, srcs ...string) *Pad {
	t.Helper()
	p := &Pad{Name: name, Dir: PadDir(name), Sess: &session.Session{}}
	for _, src := range srcs {
		e, err := session.Classify(src)
		if err != nil {
			t.Fatalf("Classify(%q): %v", src, err)
		}
		p.Sess.Append(e)
	}
	return p
}

func TestAPadIsADirectoryWithASessionFile(t *testing.T) {
	root := isolate(t)
	p := padWith(t, "parser-bug", "x := 1", "x + 1")
	p.Host = "/tmp/proj"
	p.Requires = []string{"example.com/m v1.2.3"}
	if err := WritePad(p); err != nil {
		t.Fatal(err)
	}
	if !IsPad(filepath.Join(root, "parser-bug")) {
		t.Fatalf("the directory is not recognised as a pad: %s", filepath.Join(root, "parser-bug"))
	}

	back, err := ReadPad("parser-bug")
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Sess.Entries) != 2 {
		t.Errorf("got %d entries, want 2", len(back.Sess.Entries))
	}
	if back.Host != "/tmp/proj" {
		t.Errorf("host = %q, want /tmp/proj", back.Host)
	}
	if len(back.Requires) != 1 || back.Requires[0] != "example.com/m v1.2.3" {
		t.Errorf("requires = %v", back.Requires)
	}
}

// TestAPadThatDoesNotExistIsNotAnError: a pad exists once somebody names it,
// and the empty one is what the first `gluon` lands on.
func TestAPadThatDoesNotExistIsNotAnError(t *testing.T) {
	isolate(t)
	p, err := ReadPad("default")
	if err != nil {
		t.Fatalf("reading a pad that has never been written: %v", err)
	}
	if len(p.Sess.Entries) != 0 {
		t.Errorf("got %d entries, want an empty session", len(p.Sess.Entries))
	}
}

// TestAPadFileIsWrittenThroughARename: an interrupted write must not turn a
// week of session into a truncated one, so nothing is ever truncated in place.
func TestAPadFileIsWrittenThroughARename(t *testing.T) {
	root := isolate(t)
	p := padWith(t, "atomic", "x := 1")
	if err := WritePad(p); err != nil {
		t.Fatal(err)
	}
	// Nothing of the temp form is left behind, which is the observable half of
	// "through a rename" — the other half is that the destination is never
	// opened for truncation.
	ents, err := os.ReadDir(filepath.Join(root, "atomic"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".gluon-") {
			t.Errorf("a temp file survived the write: %s", e.Name())
		}
	}
}

func TestTheSidecarIsOwnerReadableOnly(t *testing.T) {
	root := isolate(t)
	p := padWith(t, "secretish", `os.Getenv("PGPASSWORD")`)
	p.Sum = []byte("example.com/m v1.2.3 h1:abc=\n")
	if err := WritePad(p); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{PadName, PadSumName} {
		fi, err := os.Stat(filepath.Join(root, "secretish", name))
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != padMode {
			t.Errorf("%s is %o, want %o — a transcript can hold what somebody typed", name, perm, padMode)
		}
	}
}

func TestAPadNameThatLooksLikeADateIsRefused(t *testing.T) {
	isolate(t)
	if _, err := ValidPadName("2026-09-05-heap-sort"); err == nil {
		t.Error("a dated name was accepted — a date in that tree means a throwaway")
	}
	if _, err := ValidPadName("heap-sort"); err != nil {
		t.Errorf("an ordinary name was refused: %v", err)
	}
	// The slug is what is opened, so the two spellings are one scratchpad.
	slug, err := ValidPadName("Parser Bug")
	if err != nil {
		t.Fatal(err)
	}
	if slug != "parser-bug" {
		t.Errorf("slug = %q, want parser-bug", slug)
	}
}

func TestANewerFormatVersionIsNeitherOpenedNorOverwritten(t *testing.T) {
	root := isolate(t)
	dir := filepath.Join(root, "future")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	before := []byte("//gluon:pad 99\nx := 1\n")
	if err := os.WriteFile(filepath.Join(dir, PadName), before, padMode); err != nil {
		t.Fatal(err)
	}

	_, err := ReadPad("future")
	if err == nil {
		t.Fatal("a newer format was read as though it were understood")
	}
	if !strings.Contains(err.Error(), "99") {
		t.Errorf("the error does not name the version: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(dir, PadName))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Error("reading a newer pad changed it")
	}
}

func TestOpeningANameThatIsAlreadyASavedScratchIsRefused(t *testing.T) {
	root := isolate(t)
	// The shape `gluon new` leaves: a module with a program and no session.
	dir := filepath.Join(root, "heap-sort")
	if _, err := Write(dir, Options{Topic: "heap-sort"}); err != nil {
		t.Fatal(err)
	}

	if _, err := ReadPad("heap-sort"); err != ErrNotAPad {
		t.Errorf("ReadPad on a saved scratch = %v, want ErrNotAPad", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "main.go")); err != nil {
		t.Errorf("the saved scratch was touched: %v", err)
	}
	if err := RemovePad("heap-sort"); err != ErrNotAPad {
		t.Errorf("RemovePad on a saved scratch = %v, want ErrNotAPad", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "main.go")); err != nil {
		t.Errorf("the saved scratch was removed: %v", err)
	}
}

func TestRenamingRefusesATakenName(t *testing.T) {
	isolate(t)
	if err := WritePad(padWith(t, "one", "x := 1")); err != nil {
		t.Fatal(err)
	}
	if err := WritePad(padWith(t, "two", "y := 2")); err != nil {
		t.Fatal(err)
	}
	if _, err := RenamePad("one", "two"); err == nil {
		t.Fatal("renaming over an existing pad was allowed")
	}
	if _, err := ReadPad("one"); err != nil {
		t.Errorf("the source pad did not survive the refusal: %v", err)
	}

	if _, err := RenamePad("one", "Three Ways"); err != nil {
		t.Fatal(err)
	}
	p, err := ReadPad("three-ways")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Sess.Entries) != 1 {
		t.Errorf("the renamed pad holds %d entries, want 1", len(p.Sess.Entries))
	}
}

func TestPadsListsWhatEachHolds(t *testing.T) {
	isolate(t)
	p := padWith(t, "listed", "x := 1", "x + 1")
	p.Sess.Entries[0].Pinned = true
	p.Host = "/tmp/proj"
	if err := WritePad(p); err != nil {
		t.Fatal(err)
	}
	all := Pads()
	if len(all) != 1 {
		t.Fatalf("got %d pads, want 1", len(all))
	}
	got := all[0]
	if got.Name != "listed" || got.Entries != 2 || got.Pinned != 1 || got.Host != "/tmp/proj" {
		t.Errorf("row = %+v", got)
	}
	if got.Program {
		t.Error("a pad with no main.go was reported as having a program")
	}
}

func TestAPadWithNoProgramIsNotInTheRunnableList(t *testing.T) {
	isolate(t)
	if err := WritePad(padWith(t, "typed-in", "x := 1")); err != nil {
		t.Fatal(err)
	}
	for _, p := range List() {
		if filepath.Base(p) == "typed-in" {
			t.Fatal("a pad with no program is offered as something to run")
		}
	}
	// :save gives it one, and then it is a scratch like any other.
	if _, err := Write(PadDir("typed-in"), Options{Topic: "typed-in"}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range List() {
		if filepath.Base(p) == "typed-in" {
			found = true
		}
	}
	if !found {
		t.Error("a saved pad is not in the runnable list")
	}
}

// TestTypingALineDoesNotMakeAPadTheLatestScratch is the property scratch.go
// argues for at length: `gluon run` with no argument means the scratch you were
// last editing, and a pad written on every line would take that over for good.
func TestTypingALineDoesNotMakeAPadTheLatestScratch(t *testing.T) {
	isolate(t)
	real, err := New(Options{Topic: "heap-sort"})
	if err != nil {
		t.Fatal(err)
	}
	// The pad is saved, so it is in the list at all — the harder case.
	if _, err := Write(PadDir("notes"), Options{Topic: "notes"}); err != nil {
		t.Fatal(err)
	}
	older := time.Now().Add(-time.Hour)
	ents, err := os.ReadDir(PadDir("notes"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if err := os.Chtimes(filepath.Join(PadDir("notes"), e.Name()), older, older); err != nil {
			t.Fatal(err)
		}
	}

	// Now type into the pad, repeatedly, the way a session does.
	for i := range 3 {
		p := padWith(t, "notes", "x := 1")
		p.Sess.Entries[0].Values = i
		if err := WritePad(p); err != nil {
			t.Fatal(err)
		}
	}

	latest, err := Latest()
	if err != nil {
		t.Fatal(err)
	}
	if latest != filepath.Dir(real) {
		t.Errorf("Latest() = %q, want the scratch that was last edited (%q)", latest, filepath.Dir(real))
	}
}
