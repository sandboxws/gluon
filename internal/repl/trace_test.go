package repl

import (
	"go/ast"
	"go/parser"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/pretty"
	"github.com/sandboxws/gluon/internal/render"
	"github.com/sandboxws/gluon/internal/session"
)

// TestTraceSourceIsStandardLibraryGo: the rewrite has to be something a line
// you could have typed would do (constraint C), and the generated child links
// nothing but the standard library (constraint B). Both halves are checked
// here — it parses as one expression, and the only packages it reaches for are
// runtime and fmt.
func TestTraceSourceIsStandardLibraryGo(t *testing.T) {
	for _, void := range []bool{false, true} {
		src := traceSource(`double(21)`, void)
		expr, err := parser.ParseExpr(src)
		if err != nil {
			t.Fatalf("void=%v: the rewrite does not parse: %v\n%s", void, err, src)
		}
		pkgs := map[string]bool{}
		ast.Inspect(expr, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok {
				pkgs[id.Name] = true
			}
			return true
		})
		// Both are standard library, and neither needs a go.mod entry. A third
		// name here would mean the child had grown a dependency.
		for name := range pkgs {
			switch name {
			case "runtime", "fmt", "__gluonF", "__gluonIt":
			default:
				t.Errorf("void=%v: the rewrite reaches %s, which is not runtime or fmt", void, name)
			}
		}
	}
}

// TestTraceFramesNameSessionEntries is the promise that a captured position is
// reported as the user's own line rather than the temp file the program was
// generated into. The frames are synthesized here because the mapping is what
// is under test, not the capture.
func TestTraceFramesNameSessionEntries(t *testing.T) {
	c := &Core{sess: &session.Session{}}
	c.sess.Append(session.Entry{Kind: session.KindDecl, Src: "func double(n int) int {\n\treturn n * 2\n}"})
	c.sess.Append(session.Entry{Kind: session.KindExpr, Src: "double(21)"})

	tmp := "/private/var/folders/xy/T/gluon-1234/" + render.LineFile(0)
	items := []pretty.Value{
		// gofmt hoists a doc comment off a declaration's own line, so the
		// runtime names one line later than the source does — DeclLineShift.
		{Repr: "main.double\x1f" + tmp + "\x1f3"},
		{Repr: "main.main\x1f/private/var/folders/xy/T/gluon-1234/" + render.LineFile(1) + "\x1f1"},
		{Repr: "runtime.main\x1f/usr/local/go/src/runtime/proc.go\x1f283"},
	}

	frames := c.traceFrames(items)
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want only the session's own: %+v", len(frames), frames)
	}
	if frames[0].Fn != "main.double" {
		t.Errorf("frame = %q, want main.double", frames[0].Fn)
	}
	if frames[0].Where != "entry 1" {
		t.Errorf("where = %q, want entry 1", frames[0].Where)
	}
	if frames[0].Src != "return n * 2" {
		t.Errorf("src = %q, want the session's own line", frames[0].Src)
	}
	// The whole point: nothing a user could not open.
	for _, f := range frames {
		if strings.Contains(f.Where, "/private/var") || strings.Contains(f.Where, "gluon-in-") {
			t.Errorf("a temp path reached the output: %q", f.Where)
		}
	}
}

// TestHarnessFramesAreDropped: gluon's own wrapper, the main render writes
// around every session, and the injected printer are all gluon rather than the
// user, and none of them belongs in a call chain the user reads.
func TestHarnessFramesAreDropped(t *testing.T) {
	drop := []string{
		"main.main", "main.main.func1", "main.main.func2.1",
		"main.__gluonPrint", "main.__gluonPayload",
		"runtime.main", "runtime.goexit",
	}
	for _, fn := range drop {
		if !harnessFrame(fn) {
			t.Errorf("harnessFrame(%q) = false, so gluon's own frame would be shown", fn)
		}
	}
	keep := []string{"main.double", "main.Point.String", "example.com/x/pkg.F"}
	for _, fn := range keep {
		if harnessFrame(fn) {
			t.Errorf("harnessFrame(%q) = true, so the user's own frame would be dropped", fn)
		}
	}
}

// TestTraceNeedsAnExpression: :trace's Arg names a required operand, so a bare
// invocation says so rather than failing deeper down.
func TestTraceNeedsAnExpression(t *testing.T) {
	c := &Core{}
	res := c.trace("  ")
	if !res.Err || !strings.HasPrefix(res.Out, "usage:") {
		t.Errorf("bare :trace = %q (err=%v), want a usage line", res.Out, res.Err)
	}
}
