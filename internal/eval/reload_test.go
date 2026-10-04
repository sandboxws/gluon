package eval

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/sandboxws/gluon/internal/render"
)

// derived populates every piece of state invalidate is meant to drop, so a
// test can tell "already empty" from "emptied".
func derived(t *testing.T) *Evaluator {
	t.Helper()
	// Deterministic regardless of the developer's environment: the checker is
	// one of the three things being dropped, and it is nil when this is set.
	t.Setenv("GLUON_NO_TYPECHECK", "")

	e := &Evaluator{
		dir:      t.TempDir(),
		cache:    newResultCache(8),
		imports:  []render.ImportSpec{{Path: "example.com/proj/internal/counter"}},
		resolved: false,
	}
	e.newChecker()
	e.cache.put("package main // one", Result{Output: "1"}, nil)
	return e
}

// TestInvalidateEmptiesEverythingDerived is invariant 18 as an assertion. The
// result cache is the one that matters: a hit skips execution entirely, so a
// result computed against the previous source is served as though it were
// current.
func TestInvalidateEmptiesEverythingDerived(t *testing.T) {
	e := derived(t)
	checker := e.checker

	e.invalidate()

	if e.imports != nil {
		t.Errorf("imports = %v, want the import cache dropped", e.imports)
	}
	if !e.resolved {
		t.Error("resolved = false, want the import set treated as settled again")
	}
	if _, _, ok := e.cache.get("package main // one"); ok {
		t.Error("a result computed against the old source survived the invalidation")
	}
	if e.checker == checker {
		t.Error("the checker was reused, so its export data survived")
	}
}

// TestUseHostGetAndReloadShareOneInvalidation is the drift guard. Three copies
// of "drop the checker, the imports and the results" is exactly what invariant
// 18 exists to prevent, and the copy that forgets the result cache is the one
// that appears to work.
func TestUseHostGetAndReloadShareOneInvalidation(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "eval.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string]bool{}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if ok && sel.Sel.Name == "invalidate" {
				calls[fn.Name.Name] = true
			}
			return true
		})
	}
	for _, name := range []string{"UseHost", "Get", "Reload"} {
		if !calls[name] {
			t.Errorf("%s does not call invalidate — it has its own copy, and the copies drift", name)
		}
	}
}

// TestReloadWithoutAHostSaysSo. Nothing is derived from a host that is not
// there, and succeeding at nothing would leave the user believing a stale
// answer had been dropped.
func TestReloadWithoutAHostSaysSo(t *testing.T) {
	e := &Evaluator{dir: t.TempDir(), cache: newResultCache(8)}
	if _, err := e.Reload(); !errors.Is(err, ErrNoHost) {
		t.Errorf("Reload with no host: err = %v, want ErrNoHost", err)
	}
}

// TestReloadDropsDerivedStateForTheAttachedHost is the attached case: same
// module, same go.mod, and everything computed from its source gone.
func TestReloadDropsDerivedStateForTheAttachedHost(t *testing.T) {
	t.Setenv("GLUON_NO_TYPECHECK", "")
	e := &Evaluator{dir: t.TempDir(), cache: newResultCache(8), attached: writeSumHost(t, "")}
	e.newChecker()
	checker := e.checker
	e.cache.put("package main // one", Result{Output: "1"}, nil)

	ix, err := e.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if ix == nil {
		t.Error("Reload returned no index, so :reload cannot report what the host offers")
	}
	if e.attached == nil {
		t.Error("Reload detached the host")
	}
	if _, _, ok := e.cache.get("package main // one"); ok {
		t.Error("the result cache survived a reload")
	}
	if e.checker == checker {
		t.Error("the checker survived a reload")
	}
}
