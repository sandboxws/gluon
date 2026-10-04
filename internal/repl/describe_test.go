package repl

import (
	"go/ast"
	"go/parser"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/inspect"
	"github.com/sandboxws/gluon/internal/pretty"
)

// :t -d is the one thing under :t that costs a run, so these are the tests
// that say what runs and what does not. The run itself is the integration
// tier's; here the rewrite is read as source and the rendering is exercised
// without a child.

// TestDescribeFlagsAreWords: a Go expression routinely begins with a unary
// minus, so a flag is only a flag when it stands alone. `:t -dx` is a negation
// of dx, and reading it as -d applied to x would answer about the wrong thing.
func TestDescribeFlagsAreWords(t *testing.T) {
	for _, tc := range []struct {
		arg   string
		flag  string
		rest  string
		found bool
	}{
		{arg: "-d x", flag: "-d", rest: "x", found: true},
		{arg: "-v x", flag: "-v", rest: "x", found: true},
		{arg: "-d", flag: "-d", rest: "", found: true},
		{arg: "-dx", rest: "-dx"},
		{arg: "-vx", rest: "-vx"},
		{arg: "-d.Field", rest: "-d.Field"},
		{arg: "x", rest: "x"},
	} {
		t.Run(tc.arg, func(t *testing.T) {
			flag, rest, ok := leadingFlag(tc.arg, "-v", "-d")
			if ok != tc.found || flag != tc.flag || rest != tc.rest {
				t.Errorf("leadingFlag(%q) = %q, %q, %v; want %q, %q, %v",
					tc.arg, flag, rest, ok, tc.flag, tc.rest, tc.found)
			}
		})
	}
}

// TestDynamicSourceIsStandardLibraryGo: the rewrite has to be something a line
// you could have typed would do (constraint C), and the generated child links
// nothing but the standard library (constraint B). Both halves are checked
// here — it parses as one expression, it calls Sprintf exactly once so an
// expression with a side effect runs once, and the only packages it reaches
// for are fmt and reflect.
func TestDynamicSourceIsStandardLibraryGo(t *testing.T) {
	src := dynamicSource(`readerOf("x")`)
	expr, err := parser.ParseExpr(src)
	if err != nil {
		t.Fatalf("the rewrite does not parse: %v\n%s", err, src)
	}

	pkgs := map[string]bool{}
	sprintfs := 0
	ast.Inspect(expr, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		pkgs[id.Name] = true
		if id.Name == "fmt" && sel.Sel.Name == "Sprintf" {
			sprintfs++
		}
		return true
	})
	for name := range pkgs {
		switch name {
		case "fmt", "reflect":
		default:
			t.Errorf("the rewrite reaches %s, which is not fmt or reflect", name)
		}
	}
	if sprintfs != 1 {
		t.Errorf("%d fmt.Sprintf calls; the rewrite is one line and the expression runs once", sprintfs)
	}
	if n := strings.Count(src, `readerOf("x")`); n != 1 {
		t.Errorf("the expression appears %d times; it must be evaluated exactly once", n)
	}
}

// TestParseDynamicReadsTheChildsPair covers the three answers the child can
// send: a concrete type, a nil interface — which reflect reports as the
// Invalid kind and fmt as <nil>, and which is not a type — and a session type,
// whose main. qualifier belongs to the generated program rather than to
// anything the user can write.
func TestParseDynamicReadsTheChildsPair(t *testing.T) {
	for _, tc := range []struct {
		name string
		repr string
		want inspect.Dynamic
	}{
		{
			name: "a pointer to a stdlib type",
			repr: "*bytes.Buffer\x1fptr",
			want: inspect.Dynamic{Type: "*bytes.Buffer", Kind: "pointer"},
		},
		{
			name: "a scalar",
			repr: "int\x1fint",
			want: inspect.Dynamic{Type: "int", Kind: "int"},
		},
		{
			name: "a nil interface",
			repr: "<nil>\x1finvalid",
			want: inspect.Dynamic{Nil: true},
		},
		{
			name: "a session type loses the generated package",
			repr: "[]main.Point\x1fslice",
			want: inspect.Dynamic{Type: "[]Point", Kind: "slice"},
		},
		{
			name: "a package whose name ends in main keeps it",
			repr: "domain.User\x1fstruct",
			want: inspect.Dynamic{Type: "domain.User", Kind: "struct"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseDynamic(tc.repr); got != tc.want {
				t.Errorf("parseDynamic(%q) = %+v, want %+v", tc.repr, got, tc.want)
			}
		})
	}
}

// TestDescribeDynamicRenders covers the four shapes the answer takes. The
// static and dynamic names are never compared: reporting "concrete" is decided
// by the static type's underlying type, because []byte and []uint8 name one
// type and deciding that from two strings would be a guess.
func TestDescribeDynamicRenders(t *testing.T) {
	plain := pretty.Styles{}
	for _, tc := range []struct {
		name     string
		static   string
		concrete bool
		d        inspect.Dynamic
		want     []string
		notWant  []string
	}{
		{
			name:   "an interface holding a concrete value",
			static: "io.Reader",
			d:      inspect.Dynamic{Type: "*bytes.Buffer", Kind: "pointer"},
			want:   []string{"static io.Reader", "dynamic *bytes.Buffer", "pointer"},
		},
		{
			name:    "a nil interface",
			static:  "error",
			d:       inspect.Dynamic{Nil: true},
			want:    []string{"static error", "nil"},
			notWant: []string{", dynamic ", "<nil>"},
		},
		{
			name:     "static and dynamic agree",
			static:   "[]byte",
			concrete: true,
			d:        inspect.Dynamic{Type: "[]uint8", Kind: "slice"},
			want:     []string{"[]byte", "concrete"},
			notWant:  []string{"[]uint8", ", dynamic ", "static []byte", "static unavailable"},
		},
		{
			name:    "the checker could not answer",
			static:  "",
			d:       inspect.Dynamic{Type: "*bytes.Buffer", Kind: "pointer"},
			want:    []string{"static unavailable", "dynamic *bytes.Buffer"},
			notWant: []string{"concrete"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := inspect.DescribeDynamic(tc.static, tc.concrete, tc.d, plain)
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("output missing %q:\n%s", w, got)
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(got, w) {
					t.Errorf("output should not contain %q:\n%s", w, got)
				}
			}
		})
	}
}

// TestDescribeRefusesBeforeRunning is the promise the refusals exist for: a
// type name and a multi-value call are known statically, so neither reaches a
// child. The session is the witness — a run that happened would have left the
// evaluator with the rewrite's imports.
func TestDescribeRefusesBeforeRunning(t *testing.T) {
	c := typeCore(t)
	before := len(c.sess.Entries)
	for _, tc := range []struct {
		cmd  string
		want []string
	}{
		{cmd: ":t -d Sq", want: []string{"is a type", "no dynamic type"}},
		{cmd: ":t -d io.Reader", want: []string{"is a type", "no dynamic type"}},
		{cmd: ":t -d strconv.Atoi(\"12\")", want: []string{"takes one value", "returns 2"}},
		{cmd: ":t -d", want: []string{"usage:"}},
		{cmd: ":t -v -d x", want: []string{"give one"}},
	} {
		t.Run(tc.cmd, func(t *testing.T) {
			res := c.Submit(tc.cmd)
			if !res.Err {
				t.Errorf("%s: expected a refusal, got %q", tc.cmd, res.Out)
			}
			for _, w := range tc.want {
				if !strings.Contains(res.Out, w) {
					t.Errorf("%s: output missing %q:\n%s", tc.cmd, w, res.Out)
				}
			}
			if strings.Contains(res.Out, "__gluonD") {
				t.Errorf("%s: the rewrite reached the output, so something ran:\n%s", tc.cmd, res.Out)
			}
		})
	}
	if got := len(c.sess.Entries); got != before {
		t.Errorf("session grew from %d entries to %d; a refusal must not touch it", before, got)
	}
}

// TestBareDescribeIsUnchanged: -d is opt-in, so :t and :t -v answer exactly as
// they did — from go/types, with the hint that -d now finishes.
func TestBareDescribeIsUnchanged(t *testing.T) {
	c := typeCore(t)
	for _, tc := range []struct {
		cmd  string
		want []string
	}{
		{cmd: ":t r", want: []string{"io.Reader", "an interface — print the value to see its dynamic type"}},
		{cmd: ":t x", want: []string{"int"}},
		{cmd: ":t -v x", want: []string{"int", "kind", "integer"}},
		{cmd: ":t Sq", want: []string{"Sq", "a type, not a value"}},
	} {
		t.Run(tc.cmd, func(t *testing.T) {
			res := c.Submit(tc.cmd)
			if res.Err {
				t.Fatalf("%s: %s", tc.cmd, res.Out)
			}
			for _, w := range tc.want {
				if !strings.Contains(res.Out, w) {
					t.Errorf("%s: output missing %q:\n%s", tc.cmd, w, res.Out)
				}
			}
			for _, w := range []string{"static ", ", dynamic ", "concrete"} {
				if strings.Contains(res.Out, w) {
					t.Errorf("%s: output contains %q, which only -d prints:\n%s", tc.cmd, w, res.Out)
				}
			}
		})
	}
}
