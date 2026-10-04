package scratch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sandboxws/gluon/internal/host"
)

// isolate points Root at a temp directory for the duration of one test.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	return filepath.Join(dir, "gluon", "scratch")
}

func TestNewScaffoldsAModuleDirectory(t *testing.T) {
	root := isolate(t)
	p, err := New(Options{Topic: "Heap Sort!"})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) != "main.go" {
		t.Errorf("New returned %q, want a main.go", p)
	}
	dir := filepath.Dir(p)
	if filepath.Dir(dir) != root {
		t.Errorf("scaffolded outside the scratch root: %q", dir)
	}
	// The topic is slugged into the directory name so the tree says what each
	// scratch is without opening it.
	if !strings.HasSuffix(dir, "-heap-sort") {
		t.Errorf("directory = %q, want it to end in -heap-sort", dir)
	}
	mod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mod), "module gluon.local/scratch/heap-sort") {
		t.Errorf("go.mod = %q", mod)
	}
	// A module directory is the default precisely so gopls works, which it
	// cannot do for a //go:build ignore file.
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "go:build ignore") {
		t.Error("the default scaffold used the ignore form")
	}
}

func TestNewFlatWritesTheConstraintFirst(t *testing.T) {
	isolate(t)
	p, err := New(Options{Topic: "quick", Flat: true})
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	// The constraint must precede the package clause with a blank line
	// between, or it is an ordinary comment and the file joins the package.
	if !strings.HasPrefix(string(body), "//go:build ignore\n\npackage main") {
		t.Errorf("flat scaffold = %q", body)
	}
	if !strings.HasSuffix(p, ".go") {
		t.Errorf("flat scratch is not a .go file: %q", p)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(p), "go.mod")); err == nil {
		t.Error("the flat form wrote a go.mod")
	}
}

func TestNewNeverOverwrites(t *testing.T) {
	isolate(t)
	first, err := New(Options{Topic: "same"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(Options{Topic: "same"})
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("two scratches named the same collided at %q", first)
	}
	if _, err := os.Stat(first); err != nil {
		t.Error("the first scratch was replaced")
	}
}

func TestNewUnderAHost(t *testing.T) {
	isolate(t)
	hostDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(hostDir, "go.mod"),
		[]byte("module example.com/proj\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &host.Host{Dir: hostDir, Path: "example.com/proj", Go: "1.24"}
	p, err := New(Options{Topic: "probe", Host: h})
	if err != nil {
		t.Fatal(err)
	}
	mod, err := os.ReadFile(filepath.Join(filepath.Dir(p), "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	// Nested under the host, which is what lets the scratch import its
	// internal/ packages — the same arrangement a session gets.
	for _, want := range []string{
		"module example.com/proj/scratch/probe",
		"require example.com/proj v0.0.0",
		"go 1.24",
	} {
		if !strings.Contains(string(mod), want) {
			t.Errorf("go.mod missing %q:\n%s", want, mod)
		}
	}
}

func TestListIsNewestFirstByMtime(t *testing.T) {
	isolate(t)
	// Names carry a date but no clock, so alphabetical order says nothing
	// about which was touched last. "aaa" sorts before "zzz" either way, which
	// is what makes this test meaningful.
	older, err := New(Options{Topic: "zzz"})
	if err != nil {
		t.Fatal(err)
	}
	newer, err := New(Options{Topic: "aaa"})
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	for _, p := range []string{older, filepath.Dir(older), filepath.Join(filepath.Dir(older), "go.mod")} {
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
	}

	got := List()
	if len(got) != 2 {
		t.Fatalf("List = %v, want 2 entries", got)
	}
	if got[0] != filepath.Dir(newer) {
		t.Errorf("List[0] = %q, want the recently touched %q", got[0], filepath.Dir(newer))
	}

	latest, err := Latest()
	if err != nil {
		t.Fatal(err)
	}
	if latest != filepath.Dir(newer) {
		t.Errorf("Latest = %q, want %q", latest, filepath.Dir(newer))
	}
}

func TestListSeesAnEditedFileInsideADirectory(t *testing.T) {
	isolate(t)
	first, err := New(Options{Topic: "aaa"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(Options{Topic: "zzz"})
	if err != nil {
		t.Fatal(err)
	}
	// Editing main.go in place does not change the directory's own mtime, so
	// looking only at the directory would leave this scratch looking untouched.
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(first, future, future); err != nil {
		t.Fatal(err)
	}

	got := List()
	if len(got) == 0 || got[0] != filepath.Dir(first) {
		t.Errorf("List = %v, want the edited %q first", got, filepath.Dir(first))
	}
	_ = second
}

func TestLatestOnAnEmptyTree(t *testing.T) {
	isolate(t)
	if _, err := Latest(); err == nil {
		t.Fatal("Latest succeeded with no scratches")
	}
}

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Heap Sort":     "heap-sort",
		"  spaced  ":    "spaced",
		"a/b:c":         "a-b-c",
		"":              "session",
		"!!!":           "session",
		"Already-Slug1": "already-slug1",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}
