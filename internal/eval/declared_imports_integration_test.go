//go:build integration

package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/host"
	"github.com/sandboxws/gluon/internal/session"
)

// writeShadowingHost builds a module whose package names collide with the
// standard library's — `math`, `heap` — beside an ordinary one, `ds`. That is
// the shape of a module grouped by topic, an algorithms collection say, and
// exactly where the import gluon generates must defer to the imports the
// session wrote itself.
func writeShadowingHost(t *testing.T) *host.Host {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":          "module example.com/lab\n\ngo 1.24\n",
		"math/math.go":    "package math\n\nfunc Answer() int { return 42 }\n",
		"heap/heap.go":    "package heap\n\nfunc Answer() int { return 43 }\n",
		"ds/ds.go":        "package ds\n\nfunc Two() int { return 2 }\n",
		"2d-geom/geom.go": "package geom\n\nfunc Three() int { return 3 }\n",
		"cmd/x/main.go":   "package main\n\nfunc main() {}\n",
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

// evalBatch evaluates constructs the way -e and :load do: all appended, one
// evaluation.
func evalBatch(t *testing.T, e *Evaluator, srcs ...string) Result {
	t.Helper()
	s := &session.Session{}
	for _, src := range srcs {
		entry, err := session.Classify(src)
		if err != nil {
			t.Fatalf("classify %q: %v", src, err)
		}
		s.Append(entry)
	}
	res, err := e.Eval(s)
	if err != nil {
		rendered, _ := e.Render(s)
		t.Fatalf("eval: %v\n--- rendered ---\n%s", err, rendered)
	}
	return res
}

func attachedEvaluator(t *testing.T) *Evaluator {
	t.Helper()
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	if err := e.UseHost(writeShadowingHost(t)); err != nil {
		t.Fatal(err)
	}
	return e
}

// A file pasted in with its own `import "math"` means the standard library's,
// even when the attached module has a package named math. Seeding the host's
// under the same name redeclared it.
func TestADeclaredImportIsNotShadowedByAHostPackage(t *testing.T) {
	e := attachedEvaluator(t)
	res := evalBatch(t, e,
		"import (\n\t\"container/heap\"\n\t\"math\"\n\t\"sort\"\n)",
		"func root(n float64) int { return int(math.Sqrt(n)) }",
		"func smallest(a []int) int { h := sort.IntSlice(a); heap.Init(anyHeap{&h}); return h[0] }",
		"type anyHeap struct{ *sort.IntSlice }",
		"func (h anyHeap) Push(x any) { *h.IntSlice = append(*h.IntSlice, x.(int)) }",
		"func (h anyHeap) Pop() any { s := *h.IntSlice; x := s[len(s)-1]; *h.IntSlice = s[:len(s)-1]; return x }",
		"root(16) + smallest([]int{5, 2, 9})",
	)
	if !strings.Contains(res.Output, `"r":"6"`) {
		t.Errorf("output does not report 6: %q", res.Output)
	}
	for _, shadow := range []string{"example.com/lab/math", "example.com/lab/heap"} {
		if strings.Contains(res.Source, shadow) {
			t.Errorf("the host's package shadowed a declared stdlib import (%s):\n%s", shadow, res.Source)
		}
	}
}

// A declared import of a host package is already the import: gluon must not
// write it a second time ("ds redeclared in this block") — whatever name the
// package clause gives it.
func TestADeclaredHostImportIsNotImportedTwice(t *testing.T) {
	e := attachedEvaluator(t)
	res := evalBatch(t, e,
		"import (\n\t\"fmt\"\n\n\t\"example.com/lab/2d-geom\"\n\t\"example.com/lab/ds\"\n)",
		"func sum() int { fmt.Println(\"summing\"); return ds.Two() + geom.Three() }",
		"sum()",
	)
	if !strings.Contains(res.Output, `"r":"5"`) {
		t.Errorf("output does not report 5: %q", res.Output)
	}
	for _, p := range []string{`"example.com/lab/ds"`, `"example.com/lab/2d-geom"`} {
		if n := strings.Count(res.Source, p); n != 1 {
			t.Errorf("%s is imported %d times:\n%s", p, n, res.Source)
		}
	}
}

// An alias typed at the prompt used to hold only until something else needed
// resolving: goimports' answer was cached with the alias in it, and the next
// line wrote it into the generated block beside the declaration — the
// program imported it twice.
func TestADeclaredAliasSurvivesTheNextResolution(t *testing.T) {
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	res := evalLines(t, e,
		`import m "math"`,
		`int(m.Sqrt(16))`,
		`fmt.Sprint(int(m.Sqrt(25)))`,
		`strings.Repeat("x", int(m.Sqrt(9)))`,
	)
	if !strings.Contains(res.Output, `xxx`) {
		t.Errorf("output does not report xxx: %q", res.Output)
	}
}

// The same, attached, for an alias of one of the host's own packages — the
// shape a real host hit (`import ref ".../reference"`, then `fmt.Sprint(1)`:
// "ref redeclared in this block").
func TestADeclaredHostAliasSurvivesTheNextResolution(t *testing.T) {
	e := attachedEvaluator(t)
	res := evalLines(t, e,
		`import ref "example.com/lab/ds"`,
		`ref.Two()`,
		`fmt.Sprint(ref.Two())`,
		`strings.Repeat("y", ref.Two())`,
	)
	if !strings.Contains(res.Output, `yy`) {
		t.Errorf("output does not report yy: %q", res.Output)
	}
}
