package eval

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sandboxws/gluon/internal/host"
)

// indexHost is a host module with one importable package in it.
func indexHost(t *testing.T) *host.Host {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/proj\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "counter")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "counter.go"),
		[]byte("package counter\n\nfunc New() int { return 0 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &host.Host{Dir: dir, Path: "example.com/proj", Go: "1.25"}
}

// The walk is ~0.3s on a large repository, and a `gluon -host` startup used to
// pay it before the banner. Counting is how that is asserted: timing would be
// brittle, and the claim is about how many walks there are, not how long one
// takes.
func TestAttachingDoesNotWalkAndTheFirstIndexDoes(t *testing.T) {
	h := indexHost(t)
	e := &Evaluator{dir: t.TempDir()}

	if err := e.UseHost(h); err != nil {
		t.Fatal(err)
	}
	if got := h.Walks(); got != 0 {
		t.Errorf("attaching walked %d times, want 0", got)
	}

	ix, err := e.Index()
	if err != nil {
		t.Fatal(err)
	}
	if got := h.Walks(); got != 1 {
		t.Errorf("the first Index() walked %d times, want 1", got)
	}
	if ix.Total() != 1 {
		t.Errorf("importable = %d, want 1 (%v)", ix.Total(), ix.All())
	}

	// Every later ask reuses it — including the ones on the keystroke path.
	for i := 0; i < 3; i++ {
		if again, _ := e.Index(); again != ix {
			t.Fatal("Index() rebuilt instead of reusing")
		}
	}
	if got := h.Walks(); got != 1 {
		t.Errorf("%d walks after four asks, want 1", got)
	}
}

// A session with no host has no index and nothing to walk, and must not be
// made to look like a failure.
func TestStandaloneHasNoIndex(t *testing.T) {
	e := &Evaluator{dir: t.TempDir()}
	ix, err := e.Index()
	if ix != nil || err != nil {
		t.Errorf("Index() on a standalone session = (%v, %v), want (nil, nil)", ix, err)
	}
}

// Attaching somewhere else must forget the previous host's packages: offering
// one is a completion that writes an import the new module cannot resolve.
func TestAttachingAgainRebuildsTheIndex(t *testing.T) {
	first, second := indexHost(t), indexHost(t)
	e := &Evaluator{dir: t.TempDir()}

	if err := e.UseHost(first); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Index(); err != nil {
		t.Fatal(err)
	}
	if err := e.UseHost(second); err != nil {
		t.Fatal(err)
	}
	if got := second.Walks(); got != 0 {
		t.Errorf("re-attaching walked the new host %d times, want 0", got)
	}
	if _, err := e.Index(); err != nil {
		t.Fatal(err)
	}
	if got := second.Walks(); got != 1 {
		t.Errorf("the new host was walked %d times, want 1", got)
	}
	if got := first.Walks(); got != 1 {
		t.Errorf("the old host was walked %d times after detaching, want 1", got)
	}
}

// Reload exists to re-read a host whose source changed, so it is the one
// caller that must throw the index away rather than reuse it.
func TestReloadWalksAgain(t *testing.T) {
	h := indexHost(t)
	e := &Evaluator{dir: t.TempDir()}
	if err := e.UseHost(h); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Index(); err != nil {
		t.Fatal(err)
	}

	// A package added since the attach is one the session could not otherwise
	// name.
	sub := filepath.Join(h.Dir, "added")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "added.go"), []byte("package added\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ix, err := e.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if got := h.Walks(); got != 2 {
		t.Errorf("%d walks after a reload, want 2", got)
	}
	if ix.Total() != 2 {
		t.Errorf("importable = %d after adding a package, want 2 (%v)", ix.Total(), ix.All())
	}
}

// An unreadable directory is not an attach failure — it never was, because the
// walk skips what it cannot read rather than giving up. What moved is when the
// answer arrives: at the first ask, once, and the same answer afterwards.
func TestAnUnreadableHostDirectoryIsAnsweredOnceAndTheSameWayTwice(t *testing.T) {
	h := indexHost(t)
	locked := filepath.Join(h.Dir, "locked")
	if err := os.MkdirAll(filepath.Join(locked, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "pkg", "p.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Skip("cannot make the directory unreadable:", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if _, err := os.ReadDir(locked); err == nil {
		t.Skip("the directory is still readable — running as root?")
	}

	e := &Evaluator{dir: t.TempDir()}
	if err := e.UseHost(h); err != nil {
		t.Fatal(err)
	}
	if got := h.Walks(); got != 0 {
		t.Errorf("attaching walked %d times, want 0", got)
	}

	ix, err := e.Index()
	if err != nil {
		t.Fatalf("an unreadable subtree failed the whole index: %v", err)
	}
	// The packages under it simply do not appear; the rest of the host does.
	if ix.Total() != 1 {
		t.Errorf("importable = %d, want 1 (%v)", ix.Total(), ix.All())
	}

	// The answer is settled once. A first use arriving later gets the same
	// one rather than walking the tree again to be told the same thing.
	ix2, err2 := e.Index()
	if ix2 != ix || err2 != err {
		t.Errorf("the second Index() answered differently: (%v, %v)", ix2, err2)
	}
	if got := h.Walks(); got != 1 {
		t.Errorf("%d walks for two asks, want 1", got)
	}
}
