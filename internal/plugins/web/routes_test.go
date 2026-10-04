package web

import (
	"go/ast"
	"go/parser"
	"strconv"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/plugin"
)

// frameworks is every plugin in this package that contributes :routes, with the
// argument name its usage line suggests. Adding one here is what puts it under
// every test below.
var frameworks = []struct {
	name string
	p    plugin.Plugin
	arg  string
}{
	{"chi", Chi{}, "r"},
	{"gin", Gin{}, "r"},
	{"echo", Echo{}, "e"},
	{"fiber", Fiber{}, "app"},
	{"hertz", Hertz{}, "h"},
	{"iris", Iris{}, "app"},
	{"mux", Mux{}, "r"},
}

func routesRewrite(t *testing.T, p plugin.Plugin, arg string) string {
	t.Helper()
	for _, c := range p.(plugin.Commander).Commands() {
		if c.Name == ":routes" {
			src, err := c.Rewrite(arg)
			if err != nil {
				t.Fatalf("rewrite rejected %q: %v", arg, err)
			}
			return src
		}
	}
	t.Fatalf("%T contributes no :routes", p)
	return ""
}

// TestRouteRowFixtures pins the three cases a cell can be in. The middle one is
// the whole reason routeUnknown exists: a framework that cannot name a handler
// must not produce a cell that reads as "this route has no handler".
func TestRouteRowFixtures(t *testing.T) {
	for _, tc := range []struct {
		what                  string
		method, path, handler string
		want                  string
	}{
		{
			"every column supplied",
			"GET", "/users/:id", "main.getUser",
			"GET     /users/:id                     main.getUser",
		},
		{
			"a framework that does not name handlers",
			"GET", "/users/:id", "",
			"GET     /users/:id                     " + routeUnknown,
		},
		{
			"a route matched by something other than a path",
			"POST", "", "main.submit",
			"POST    " + routeUnknown + strings.Repeat(" ", 30-len([]rune(routeUnknown))) + " main.submit",
		},
	} {
		if got := routeRow(tc.method, tc.path, tc.handler); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.what, got, tc.want)
		}
	}

	// An unavailable cell and an empty one must not be the same bytes, or the
	// distinction the column set is built on is not observable.
	if routeCol("") == "" {
		t.Error("an unsupplied column came back blank")
	}
}

// TestEveryRouteTableUsesTheSharedShape is how "the same columns everywhere" is
// enforced rather than reviewed. Each framework contributes only the statements
// that collect rows; the header, the sort, the empty-table sentence and the row
// formatter all come from routesExpr, so a plugin cannot quietly grow a fourth
// column or drop the no-routes branch.
func TestEveryRouteTableUsesTheSharedShape(t *testing.T) {
	// routesExpr around a marker no body could contain splits the wrapper into
	// the part before the body and the part after it.
	const marker = "\x00BODY\x00"
	pre, post, ok := strings.Cut(routesExpr(marker), marker)
	if !ok {
		t.Fatal("routesExpr no longer wraps its body")
	}
	if !strings.Contains(post, `"no routes registered"`) {
		t.Fatal("the shared ending lost its empty-table branch")
	}
	if !strings.Contains(post, strconv.Quote(routeHeader)) {
		t.Fatal("the shared ending lost the column header")
	}

	for _, f := range frameworks {
		src := routesRewrite(t, f.p, f.arg)
		if !strings.HasPrefix(src, pre) {
			t.Errorf("%s builds its own table preamble:\n%s", f.name, src)
		}
		if !strings.HasSuffix(src, post) {
			t.Errorf("%s builds its own table ending:\n%s", f.name, src)
		}
	}
}

// TestEveryRouteTableIsFormattedOnce walks the generated source for the calls
// that could produce a row. A framework formatting its own is exactly the
// failure the fixed column set exists to prevent: seven tables that look alike
// and do not line up.
func TestEveryRouteTableIsFormattedOnce(t *testing.T) {
	for _, f := range frameworks {
		src := routesRewrite(t, f.p, f.arg)
		expr, err := parser.ParseExpr(src)
		if err != nil {
			t.Errorf("%s produced source that does not parse: %v", f.name, err)
			continue
		}

		var formats []string
		ast.Inspect(expr, func(n ast.Node) bool {
			call, isCall := n.(*ast.CallExpr)
			if !isCall || len(call.Args) == 0 {
				return true
			}
			sel, isSel := call.Fun.(*ast.SelectorExpr)
			if !isSel || sel.Sel.Name != "Sprintf" {
				return true
			}
			if pkg, isIdent := sel.X.(*ast.Ident); !isIdent || pkg.Name != "fmt" {
				return true
			}
			lit, isLit := call.Args[0].(*ast.BasicLit)
			if !isLit {
				formats = append(formats, "<not a literal>")
				return true
			}
			v, err := strconv.Unquote(lit.Value)
			if err != nil {
				formats = append(formats, lit.Value)
				return true
			}
			formats = append(formats, v)
			return true
		})

		if len(formats) != 1 || formats[0] != routeFormat {
			t.Errorf("%s formats rows with %q, want exactly one %q",
				f.name, formats, routeFormat)
		}
	}
}

// TestEveryFrameworkNamesItsArgumentInTheUsageLine. The generic registry test
// checks that a bare :routes errors; this checks the error is usable, because
// "usage: :routes <router>" with no example is the same shrug for all seven.
func TestEveryFrameworkNamesItsArgumentInTheUsageLine(t *testing.T) {
	for _, f := range frameworks {
		for _, c := range f.p.(plugin.Commander).Commands() {
			_, err := c.Rewrite("   ")
			if err == nil {
				t.Errorf("%s accepted an empty argument", f.name)
				continue
			}
			if !strings.HasPrefix(err.Error(), "usage:") {
				t.Errorf("%s: %q does not begin with usage:", f.name, err)
			}
			if !strings.Contains(err.Error(), "e.g. :routes "+f.arg) {
				t.Errorf("%s: %q does not show a worked example", f.name, err)
			}
		}
	}
}
