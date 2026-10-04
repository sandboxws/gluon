// Package session holds what the user has typed so far and decides, for each
// new line, whether it is a top-level declaration, a statement, or a bare
// expression to print.
package session

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"strings"
)

type Kind int

const (
	// KindDecl is hoisted above main: func (incl. methods), type, const.
	// Types must be package-level or methods could not be declared on them.
	KindDecl Kind = iota
	// KindStmt goes inside main. var lives here, not at package level, so it
	// can reference earlier locals.
	KindStmt
	// KindExpr is a bare expression; the trailing one gets auto-printed.
	KindExpr
)

func (k Kind) String() string {
	switch k {
	case KindDecl:
		return "decl"
	case KindStmt:
		return "stmt"
	default:
		return "expr"
	}
}

type Entry struct {
	Kind Kind
	Src  string
	// Binds are value identifiers this entry introduces. Rendering emits
	// `_ = name` for each so Go's "declared and not used" never fires.
	Binds []string
	// Values is how many results a printed expression yields, learned from a
	// clean type check the way NoValue is learned from a failed one. It stays
	// 0 when nothing has told us, in which case the compiler is left to report
	// a mismatch itself.
	Values int
	// NoValue is set once a build proves this expression is a call with no
	// results, so it must be emitted bare rather than wrapped in a print.
	NoValue bool
	// Pinned suppresses this entry on replay. Every line re-renders the whole
	// session, so an entry that talked to the network or wrote a file does it
	// again on every subsequent line; pinning is the user saying "this one has
	// already happened". A pinned entry contributes no code at all, which is
	// why it is only allowed when nothing later depends on what it bound —
	// see render.PinBlockers, which is what refuses the rest.
	Pinned bool
}

type Session struct {
	Entries []Entry
}

func (s *Session) Append(e Entry) { s.Entries = append(s.Entries, e) }

func (s *Session) Pop() {
	if n := len(s.Entries); n > 0 {
		s.Entries = s.Entries[:n-1]
	}
}

func (s *Session) Reset() { s.Entries = nil }

// Clone is a deep copy of the session, for a caller that wants to hold what it
// looks like now and put it back later.
//
// Deep because Entry carries a []string: a copied entry slice would share every
// Binds with the live session, so a later Classify writing into one would reach
// into the copy that was taken to be safe from exactly that. The helper lives
// here, beside the struct, so a field added to Entry is a field this function
// is looked at for.
func (s *Session) Clone() *Session {
	if s == nil {
		return nil
	}
	out := &Session{}
	if s.Entries == nil {
		return out
	}
	out.Entries = make([]Entry, len(s.Entries))
	for i, e := range s.Entries {
		if e.Binds != nil {
			e.Binds = append([]string(nil), e.Binds...)
		}
		out.Entries[i] = e
	}
	return out
}

// Remove drops the entry at i, 0-based. Unlike Pop this can reach into the
// middle, so the caller has to re-evaluate.
func (s *Session) Remove(i int) bool {
	if i < 0 || i >= len(s.Entries) {
		return false
	}
	s.Entries = append(s.Entries[:i], s.Entries[i+1:]...)
	return true
}

// Classify decides how a line of input should be folded into the session.
// Order matters: declaration, then expression, then statement.
func Classify(src string) (Entry, error) {
	trimmed := strings.TrimSpace(src)
	if trimmed == "" {
		return Entry{}, errors.New("empty input")
	}

	if isDecl(trimmed) {
		return Entry{Kind: KindDecl, Src: trimmed}, nil
	}

	// A bare expression parses on its own. Note ParseExpr rejects `x := 1`
	// and any statement form, which is exactly the discrimination we want.
	if _, err := parser.ParseExpr(trimmed); err == nil {
		return Entry{Kind: KindExpr, Src: trimmed}, nil
	}

	binds, err := stmtBinds(trimmed)
	if err != nil {
		return Entry{}, err
	}
	return Entry{Kind: KindStmt, Src: trimmed, Binds: binds}, nil
}

// isDecl reports whether src is a top-level declaration worth hoisting.
// var is deliberately excluded: at package level it could not reference
// locals bound earlier in the session.
func isDecl(src string) bool {
	f, err := parser.ParseFile(token.NewFileSet(), "", "package p\n"+src, 0)
	if err != nil || len(f.Decls) == 0 {
		return false
	}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			continue
		case *ast.GenDecl:
			if d.Tok == token.TYPE || d.Tok == token.CONST || d.Tok == token.IMPORT {
				continue
			}
			return false
		default:
			return false
		}
	}
	return true
}

// stmtBinds parses src as statements and returns the value identifiers it
// introduces at the top level of the entry. Nested declarations are skipped:
// they fall out of scope at the end of their own block, so Go never reports
// them as unused across entries.
func stmtBinds(src string) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), "", "package p\nfunc _() {\n"+src+"\n}", 0)
	if err != nil {
		return nil, cleanErr(err)
	}
	if len(f.Decls) == 0 {
		return nil, errors.New("could not parse input")
	}
	fn, ok := f.Decls[0].(*ast.FuncDecl)
	if !ok || fn.Body == nil {
		return nil, errors.New("could not parse input")
	}

	var binds []string
	add := func(e ast.Expr) {
		if id, ok := e.(*ast.Ident); ok && id.Name != "_" {
			binds = append(binds, id.Name)
		}
	}
	for _, st := range fn.Body.List {
		switch st := st.(type) {
		case *ast.AssignStmt:
			if st.Tok == token.DEFINE {
				for _, lhs := range st.Lhs {
					add(lhs)
				}
			}
		case *ast.DeclStmt:
			gd, ok := st.Decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range vs.Names {
					add(name)
				}
			}
		}
	}
	return binds, nil
}

// IsIncomplete reports whether src has an unclosed brace, bracket, or paren,
// meaning the REPL should keep reading. Scanning rather than brace-counting
// keeps delimiters inside string literals and comments from being counted.
func IsIncomplete(src string) bool {
	var s scanner.Scanner
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))

	// A literal left open spans the newline, so it means "keep reading" just
	// as much as an unclosed brace does. The scanner is what makes both checks
	// safe: delimiters inside strings and comments are never counted.
	unterminated := false
	s.Init(file, []byte(src), func(_ token.Position, msg string) {
		if strings.Contains(msg, "not terminated") {
			unterminated = true
		}
	}, scanner.ScanComments)

	depth := 0
	for {
		_, tok, _ := s.Scan()
		switch tok {
		case token.EOF:
			return depth > 0 || unterminated
		case token.LBRACE, token.LBRACK, token.LPAREN:
			depth++
		case token.RBRACE, token.RBRACK, token.RPAREN:
			depth--
		}
	}
}

// SplitConstructs cuts text into the complete constructs it holds, in order,
// and hands back whatever was left over unfinished.
//
// It is the accumulate-until-complete loop the piped driver, a pasted block and
// a reloaded file each used to spell for themselves: append a line, ask
// IsIncomplete about everything accumulated so far, and emit when it says no.
// One spelling rather than three because they have to agree — a file that
// reloads into different entries than the same text pasted would be two ideas
// of what a construct is.
//
// A run of blank lines is dropped rather than emitted: whitespace is not a
// construct, and the callers all treated it that way already.
//
// rest is the trailing text that never completed — an unclosed brace at the end
// of the file. It is returned rather than emitted or discarded, because the two
// callers want different things from it: a driver still reading keeps it as the
// line being typed, and one that has run out of input has to say the construct
// was never finished.
func SplitConstructs(text string) (complete []string, rest string) {
	cs, rest := SplitConstructsAt(text)
	complete = make([]string, 0, len(cs))
	for _, c := range cs {
		complete = append(complete, c.Src)
	}
	return complete, rest
}

// A Construct is one complete construct and the line of the text it started on.
type Construct struct {
	Src string
	// Line is 1-based in the text it was cut from. It exists for callers with a
	// coordinate system of their own to report against — a diagnostic naming
	// line 12 of a buffer has to come from somewhere, and recovering it by
	// searching the text for the construct again would be a second, worse
	// answer to a question this loop already knows.
	Line int
}

// SplitConstructsAt is SplitConstructs with the line each construct began on.
func SplitConstructsAt(text string) (complete []Construct, rest string) {
	var buf []string
	start := 0
	for i, line := range strings.Split(text, "\n") {
		if len(buf) == 0 {
			start = i
		}
		buf = append(buf, line)
		joined := strings.Join(buf, "\n")
		if strings.TrimSpace(joined) == "" {
			buf = nil
			continue
		}
		if IsIncomplete(joined) {
			continue
		}
		buf = nil
		complete = append(complete, Construct{Src: joined, Line: start + 1})
	}
	return complete, strings.Join(buf, "\n")
}

// cleanErr strips the synthetic wrapper's position prefixes so errors read
// against what the user actually typed.
func cleanErr(err error) error {
	var list scanner.ErrorList
	if errors.As(err, &list) && len(list) > 0 {
		return fmt.Errorf("%s", list[0].Msg)
	}
	return err
}
