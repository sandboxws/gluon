//go:build integration

// These tests build a real host module and evaluate against it. Run with:
//
//	go test -tags=integration ./internal/eval/
package eval

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/host"
	"github.com/sandboxws/gluon/internal/session"
)

// writeTestHost builds a module whose only interesting package sits under
// internal/, which is the case -host exists for. A second package named the
// same thing, one level down, is there to prove the internal rule disambiguates
// rather than making the name unusable.
func writeTestHost(t *testing.T) *host.Host {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/proj\n\ngo 1.24\n",
		"internal/counter/counter.go": `package counter

type Counter struct{ n int }

func New() *Counter      { return &Counter{} }
func (c *Counter) Add()  { c.n++ }
func (c *Counter) N() int { return c.n }
`,
		// Unreachable from the session module: its parent is .../sub.
		"sub/internal/counter/counter.go": "package counter\n\nfunc New() int { return -1 }\n",
		"cmd/tool/main.go":                "package main\n\nfunc main() {}\n",
	}
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h, err := host.FromGoMod(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func evalLines(t *testing.T, e *Evaluator, lines ...string) Result {
	t.Helper()
	s := &session.Session{}
	var res Result
	for _, line := range lines {
		entry, err := session.Classify(line)
		if err != nil {
			t.Fatalf("classify %q: %v", line, err)
		}
		s.Append(entry)
		res, err = e.Eval(s)
		if err != nil {
			src, _ := e.Render(s)
			t.Fatalf("eval %q: %v\n--- rendered ---\n%s", line, err, src)
		}
	}
	return res
}

// The point of the whole feature: a module named gluon.local/session can never
// import example.com/proj/internal/counter, but one named
// example.com/proj/gluonsession can, because disallowInternal tests the
// importer's *import path* and nothing else.
func TestSessionImportsHostInternalPackage(t *testing.T) {
	h := writeTestHost(t)
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	// Before attaching, the package is not reachable at all.
	s := &session.Session{}
	entry, err := session.Classify(`counter.New()`)
	if err != nil {
		t.Fatal(err)
	}
	s.Append(entry)
	if _, err := e.Eval(s); err == nil {
		t.Fatal("a standalone session resolved a host-only package")
	}

	if err := e.UseHost(h); err != nil {
		t.Fatal(err)
	}
	ix, err := e.Index()
	if err != nil {
		t.Fatal(err)
	}
	if ix.Total() != 1 {
		t.Errorf("importable = %d, want 1 (%v)", ix.Total(), ix.All())
	}

	res := evalLines(t, e,
		`c := counter.New()`,
		`c.Add()`,
		`c.Add()`,
		`c.N()`,
	)
	if !strings.Contains(res.Output, `"r":"2"`) {
		t.Errorf("output does not report 2: %q", res.Output)
	}
}

// The type checker resolves host packages too, so :t and friends work against
// them without a build.
func TestCheckerSeesHostPackage(t *testing.T) {
	h := writeTestHost(t)
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.UseHost(h); err != nil {
		t.Fatal(err)
	}

	s := &session.Session{}
	res, err := e.Analyze(s, `counter.New()`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errs) != 0 {
		t.Fatalf("type errors against the host: %v", res.Errs)
	}
}

// Detaching must leave nothing of the old module behind — not the go.mod, not
// the checker's export data, and not the import cache.
func TestDetachRestoresAStandaloneSession(t *testing.T) {
	h := writeTestHost(t)
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.UseHost(h); err != nil {
		t.Fatal(err)
	}
	evalLines(t, e, `counter.New()`)

	if err := e.UseHost(nil); err != nil {
		t.Fatal(err)
	}
	mod, err := os.ReadFile(filepath.Join(e.Dir(), "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mod), "module gluon.local/session") {
		t.Errorf("go.mod not restored:\n%s", mod)
	}
	if e.Host() != nil {
		t.Error("Host() still reports a host after detaching")
	}

	s := &session.Session{}
	entry, _ := session.Classify(`counter.New()`)
	s.Append(entry)
	if _, err := e.Eval(s); err == nil {
		t.Fatal("host package still resolved after detaching")
	}
}

// Attaching must not disturb the stdlib path: goimports still has to answer
// for names the host does not offer.
func TestStdlibStillResolvesWhenAttached(t *testing.T) {
	h := writeTestHost(t)
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.UseHost(h); err != nil {
		t.Fatal(err)
	}
	res := evalLines(t, e, `strings.ToUpper("ok")`)
	if !strings.Contains(res.Output, "OK") {
		t.Errorf("stdlib import broke under -host: %q", res.Output)
	}
}

// Invariant 1: a session must never write into the host tree.
func TestHostTreeIsUntouched(t *testing.T) {
	h := writeTestHost(t)
	before := treeSnapshot(t, h.Dir)

	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.UseHost(h); err != nil {
		t.Fatal(err)
	}
	evalLines(t, e, `c := counter.New()`, `c.Add()`, `c.N()`)

	if after := treeSnapshot(t, h.Dir); after != before {
		t.Errorf("the host tree changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func treeSnapshot(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		b.WriteString(rel)
		if !fi.IsDir() {
			b.WriteString(" " + strconv.FormatInt(fi.Size(), 10))
		}
		b.WriteString("\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// A preloaded import must be written only when the session names it, and must
// let the session skip goimports entirely when it does. That second half is the
// whole point: the pass costs ~135ms, and preloading is worthless if it only
// saves goimports the work of searching.
func TestPreloadedImportsSkipGoimports(t *testing.T) {
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	e.SetPreload([]string{"strings", "slices"})

	Timing(true)
	defer Timing(false)
	res := evalLines(t, e, `strings.ToUpper("hi")`)
	if !strings.Contains(res.Output, "HI") {
		t.Fatalf("output = %q", res.Output)
	}
	for _, p := range TakePhases() {
		if p.Name == "goimports" {
			t.Errorf("goimports ran despite strings being preloaded (%v)", p.D)
		}
	}
	if !strings.Contains(res.Source, `"strings"`) {
		t.Errorf("the preloaded import was not written:\n%s", res.Source)
	}
	// An entry nothing names is not an unused import — it is not an import.
	if strings.Contains(res.Source, `"slices"`) {
		t.Errorf("an unnamed preload was written:\n%s", res.Source)
	}
}

// A name the preload set does not cover still has to reach goimports, or the
// optimisation would turn every unknown qualifier into a build failure.
func TestUnknownQualifierStillResolves(t *testing.T) {
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	e.SetPreload([]string{"strings"})

	res := evalLines(t, e, `sort.SearchInts([]int{1, 2, 3}, 2)`)
	if !strings.Contains(res.Output, `"r":"1"`) {
		t.Errorf("output = %q", res.Output)
	}
	if !strings.Contains(res.Source, `"sort"`) {
		t.Errorf("goimports did not resolve sort:\n%s", res.Source)
	}
}

// The host wins over config on a name they both offer: attaching is an explicit
// statement about this project, and config is global.
func TestHostBeatsPreloadOnACollision(t *testing.T) {
	h := writeTestHost(t)
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.UseHost(h); err != nil {
		t.Fatal(err)
	}
	// A stdlib path whose name collides with the host's counter package.
	e.SetPreload([]string{"example.com/nowhere/counter"})

	res := evalLines(t, e, `counter.New()`)
	if !strings.Contains(res.Source, "example.com/proj/internal/counter") {
		t.Errorf("the host package did not win:\n%s", res.Source)
	}
}
