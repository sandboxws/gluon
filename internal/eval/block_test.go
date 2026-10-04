package eval

import (
	"errors"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/check"
	"github.com/sandboxws/gluon/internal/render"
	"github.com/sandboxws/gluon/internal/session"
)

// posIn builds a position inside the synthetic file rendering gives entry i, so
// these tests exercise the same EntryOf round trip a real check goes through
// without needing a toolchain to produce one.
func posIn(t *testing.T, fset *token.FileSet, entry, line, col int) token.Pos {
	t.Helper()
	f := fset.AddFile(render.LineFile(entry), fset.Base(), 4096)
	starts := make([]int, 0, line)
	for i := range line {
		starts = append(starts, i*64)
	}
	f.SetLines(starts)
	return f.LineStart(line) + token.Pos(col-1)
}

func TestBlockDiagsNameTheConstructAndItsLine(t *testing.T) {
	s := sessionOf(t, "x := 1")
	first := len(s.Entries)
	s.Append(session.Entry{Kind: session.KindStmt, Src: "y := undefinedThing"})

	fset := token.NewFileSet()
	res := &check.Result{Fset: fset, Errs: []types.Error{
		{Pos: posIn(t, fset, first, 1, 6), Msg: "undefined: undefinedThing"},
	}}

	got := blockDiags(s, res, first)
	if len(got) != 1 {
		t.Fatalf("got %d diagnostics, want 1", len(got))
	}
	d := got[0]
	if d.Construct != 0 {
		t.Errorf("Construct = %d, want 0 — the block's first construct", d.Construct)
	}
	if d.Line != 1 {
		t.Errorf("Line = %d, want 1", d.Line)
	}
	// A statement's directive shifts along the column axis, never the line.
	if d.Col != 6-render.ColumnShift {
		t.Errorf("Col = %d, want %d", d.Col, 6-render.ColumnShift)
	}
	if !strings.Contains(d.Quote, "y := undefinedThing") || !strings.Contains(d.Quote, "^") {
		t.Errorf("Quote does not quote the line with a caret:\n%s", d.Quote)
	}
}

// A declaration's directive shifts along the other axis. The two constants are
// gofmt's behaviour rather than arbitrary (invariant 7), and entryPos is the one
// place that applies them.
func TestBlockDiagsShiftADeclarationAlongTheLineAxis(t *testing.T) {
	s := &session.Session{}
	first := 0
	s.Append(session.Entry{Kind: session.KindDecl, Src: "func f() int {\n\treturn nope\n}"})

	fset := token.NewFileSet()
	res := &check.Result{Fset: fset, Errs: []types.Error{
		{Pos: posIn(t, fset, first, 3, 9), Msg: "undefined: nope"},
	}}

	d := blockDiags(s, res, first)[0]
	if d.Line != 3-render.DeclLineShift {
		t.Errorf("Line = %d, want %d", d.Line, 3-render.DeclLineShift)
	}
	if d.Col != 9 {
		t.Errorf("Col = %d, want 9 — a declaration does not shift the column", d.Col)
	}
}

// A diagnostic against the session the block was checked against is not a
// diagnostic against the block, and the caller has to be able to tell: it has
// no buffer line to put it on.
func TestBlockDiagsMarkASessionDiagnosticAsNotTheBlock(t *testing.T) {
	s := sessionOf(t, "x := 1")
	first := len(s.Entries)
	s.Append(session.Entry{Kind: session.KindStmt, Src: "y := 2"})

	fset := token.NewFileSet()
	res := &check.Result{Fset: fset, Errs: []types.Error{
		{Pos: posIn(t, fset, 0, 1, 2), Msg: "x declared and not used"},
	}}

	d := blockDiags(s, res, first)[0]
	if d.Construct != -1 {
		t.Errorf("Construct = %d, want -1 — this one belongs to the session", d.Construct)
	}
}

// A position no //line directive covers has no entry, so there is nothing to
// quote and nothing to number. It must still carry its message: a missing
// func main fails at link time with no file:line at all (invariant 2), and a
// mapper that dropped what it could not place would drop the only useful one.
func TestBlockDiagsKeepAMessageItCannotPlace(t *testing.T) {
	s := sessionOf(t, "x := 1")
	fset := token.NewFileSet()
	f := fset.AddFile("main.go", fset.Base(), 256)
	f.SetLines([]int{0, 20})
	res := &check.Result{Fset: fset, Errs: []types.Error{
		{Pos: f.LineStart(2), Msg: "something the renderer owns"},
	}}

	got := blockDiags(s, res, len(s.Entries))
	if len(got) != 1 {
		t.Fatalf("got %d diagnostics, want the unplaceable one kept", len(got))
	}
	if got[0].Msg != "something the renderer owns" {
		t.Errorf("Msg = %q", got[0].Msg)
	}
	if got[0].Line != 0 || got[0].Col != 0 || got[0].Quote != "" {
		t.Errorf("an unplaceable diagnostic claims a position: %+v", got[0])
	}
}

func TestBlockDiagsDedupeTheSameMistake(t *testing.T) {
	s := &session.Session{}
	s.Append(session.Entry{Kind: session.KindStmt, Src: "y := nope"})

	fset := token.NewFileSet()
	p := posIn(t, fset, 0, 1, 6)
	res := &check.Result{Fset: fset, Errs: []types.Error{
		{Pos: p, Msg: "undefined: nope"},
		{Pos: p, Msg: "undefined: nope"},
	}}

	if got := blockDiags(s, res, 0); len(got) != 1 {
		t.Errorf("got %d diagnostics, want the repeat collapsed", len(got))
	}
}

// Invariant 5: with no checker there is no verdict to give, and "the block is
// fine" is as wrong an answer as "the block is broken".
func TestCheckBlockWithoutACheckerSaysSoRatherThanAnswering(t *testing.T) {
	e := &Evaluator{}
	s := sessionOf(t, "x := 1")
	before := len(s.Entries)

	got, err := e.CheckBlock(s, []string{"y := 2"})
	if !errors.Is(err, ErrNoChecker) {
		t.Errorf("err = %v, want ErrNoChecker", err)
	}
	if got != nil {
		t.Errorf("diagnostics = %+v, want none — there was no answer to give", got)
	}
	if len(s.Entries) != before {
		t.Errorf("the session grew to %d entries", len(s.Entries))
	}
}

func TestCheckBlockOfNothingIsClean(t *testing.T) {
	e := &Evaluator{}
	if got, err := e.CheckBlock(sessionOf(t, "x := 1"), nil); got != nil || err != nil {
		t.Errorf("CheckBlock(nil) = %+v, %v; want no diagnostics and no error", got, err)
	}
}
