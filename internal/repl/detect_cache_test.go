package repl

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sandboxws/gluon/internal/db"
)

// countDetect replaces the one call that reads the project's files, so a test
// can say how many times it happened rather than how long it took.
//
// The stamp it returns is a real one, computed over real files, because the
// cache check recomputes it: a stub answering with a made-up stamp would miss
// every time and prove nothing.
func countDetect(t *testing.T, searched, roots []string) *int {
	t.Helper()
	calls := 0
	prev := detectFn
	detectFn = func(string, []string) db.Detection {
		calls++
		d := db.Detection{Searched: searched, Roots: roots}
		d.Stamp = db.Stamp(d.Searched, d.Roots)
		return d
	}
	t.Cleanup(func() { detectFn = prev })
	return &calls
}

// fixtureProject is a directory with one file a detection would have read.
func fixtureProject(t *testing.T) (dir, file string) {
	t.Helper()
	dir = t.TempDir()
	file = filepath.Join(dir, ".env")
	if err := os.WriteFile(file, []byte("DATABASE_URL=postgres://localhost/a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, file
}

// TestABuildListChangeDoesNotRescanTheProject.
//
// refreshPlugins runs at every :get and :use, and used to drop the detection
// with everything else derived from the build list. But adding an unrelated
// module leaves every file the detection read exactly as it was, and paying for
// a fresh walk of the ancestors and every .env under the project to discover
// that was the cost this removes.
func TestABuildListChangeDoesNotRescanTheProject(t *testing.T) {
	dir, file := fixtureProject(t)
	calls := countDetect(t, []string{file}, []string{dir})

	c := &Core{}
	c.detect()
	if *calls != 1 {
		t.Fatalf("the first :db read the project %d times, want 1", *calls)
	}

	// What :get does, and what :use does.
	c.refreshPlugins()
	c.detect()
	if *calls != 1 {
		t.Errorf("a build-list change re-read an unchanged project (%d calls)", *calls)
	}

	// The re-attach case: :use -off then :use . runs it twice more, and the
	// stamp rather than the drop is what decides.
	c.refreshPlugins()
	c.refreshPlugins()
	c.detect()
	if *calls != 1 {
		t.Errorf("re-attaching the same project re-read it (%d calls)", *calls)
	}
}

// TestAChangedSourceFileInvalidatesTheDetection — the other direction, and the
// one that matters more: a cache that cannot go stale is the point of keying it
// on content rather than on the moment.
func TestAChangedSourceFileInvalidatesTheDetection(t *testing.T) {
	dir, file := fixtureProject(t)
	calls := countDetect(t, []string{file}, []string{dir})

	c := &Core{}
	c.detect()
	if *calls != 1 {
		t.Fatalf("calls = %d, want 1", *calls)
	}

	if err := os.WriteFile(file, []byte("DATABASE_URL=postgres://localhost/b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pinned rather than trusted to the clock: two writes inside one
	// filesystem tick share a timestamp.
	when := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(file, when, when); err != nil {
		t.Fatal(err)
	}

	c.detect()
	if *calls != 2 {
		t.Errorf("an edited .env was answered from cache (%d calls)", *calls)
	}
}

// TestADifferentRootIsADifferentProject. The stamp is checked alongside the
// root, never instead of it: two projects can be byte-identical.
func TestADifferentRootIsADifferentProject(t *testing.T) {
	dir, file := fixtureProject(t)
	calls := countDetect(t, []string{file}, []string{dir})

	c := &Core{}
	c.detect()
	c.detectedFrom = "/somewhere/else"
	c.detect()
	if *calls != 2 {
		t.Errorf("a different root was answered from cache (%d calls)", *calls)
	}
}

// TestReloadDropsTheDetection.
//
// A reload is the session saying the host moved underneath it, which is the
// one thing the stamp cannot notice: a different checkout at the same path
// reads as the same project, file for file.
func TestReloadDropsTheDetection(t *testing.T) {
	dir, file := fixtureProject(t)
	calls := countDetect(t, []string{file}, []string{dir})

	c, _ := attached(t)
	c.detect()
	if *calls != 1 {
		t.Fatalf("calls = %d, want 1", *calls)
	}

	if res := c.Submit(":reload"); res.Err {
		t.Fatalf(":reload: %s", res.Out)
	}
	c.detect()
	if *calls != 2 {
		t.Errorf(":reload did not drop the detection (%d calls)", *calls)
	}

	// And the reload the watcher asks for, which goes through the same place
	// for the same reason.
	if res := c.ReloadOnChange("greet.go changed"); res.Err {
		t.Fatalf("ReloadOnChange: %s", res.Out)
	}
	c.detect()
	if *calls != 3 {
		t.Errorf("a watched change did not drop the detection (%d calls)", *calls)
	}
}

// TestEditReloadDropsTheDetection — :edit hands a file back through Reload,
// which replaces the session from source edited outside gluon.
func TestEditReloadDropsTheDetection(t *testing.T) {
	dir, file := fixtureProject(t)
	calls := countDetect(t, []string{file}, []string{dir})

	c := testCore(t)
	c.detect()
	if *calls != 1 {
		t.Fatalf("calls = %d, want 1", *calls)
	}

	edited := filepath.Join(t.TempDir(), "session.go")
	if err := os.WriteFile(edited, []byte("x := 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := c.Reload(edited); res.Err {
		t.Fatalf("Reload: %s", res.Out)
	}
	c.detect()
	if *calls != 2 {
		t.Errorf(":edit's reload did not drop the detection (%d calls)", *calls)
	}
}

// TestEvaluationNeverDetects is invariant 22.
//
// Reading the project's files is a cost that belongs at a transition, and the
// rule is not "rarely" — it is never. An expression, a statement and a batch
// each go the whole way through Submit here, and none of them may touch it.
func TestEvaluationNeverDetects(t *testing.T) {
	dir, file := fixtureProject(t)
	calls := countDetect(t, []string{file}, []string{dir})

	c, _ := attached(t)
	if *calls != 0 {
		t.Fatalf("attaching a host detected %d times, want 0", *calls)
	}

	c.Submit("x := 1")
	c.Submit("x + 1")
	c.SubmitBatch([]string{"y := 2", "y * 3"})
	if *calls != 0 {
		t.Errorf("evaluation read the project %d times — invariant 22", *calls)
	}

	// And asking is still what makes it happen.
	c.detect()
	if *calls != 1 {
		t.Errorf(":db did not detect (%d calls)", *calls)
	}
}
