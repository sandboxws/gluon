package render

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"

	"github.com/sandboxws/gluon/internal/session"
)

var errNoPrevValue = errors.New("no value to refer to yet — " + ItName +
	" is the result of the previous line")

// The REPL's answer to irb's `_`: `it` is the value the previous line printed,
// and `_1`, `_2`, … address any earlier one by its position in the transcript.
//
// `_N` is a real variable, because it holds a value. `it` is not: it is
// rewritten to the ordinal that was in scope at that entry, and never reaches
// the compiler.
//
// That asymmetry is forced by replay. The whole session is re-rendered on every
// line, so an entry that says `it` is still in the program long after it stopped
// being the newest one. A single `it := _2` before the newest entry would leave
// every earlier reference undefined, and re-binding before each of them would
// declare `it` repeatedly at whatever type that line's predecessor had —
// rewriteRedeclare would turn the second `:=` into `=` and the compiler would
// reject the assignment. Resolving `it` per entry sidesteps all of it: entry 2
// renders `_1 * 10` and entry 3 renders `_2 + 1`, each with its own type.

// entryPrefix is what parseEntry prepends when it parses a statement or
// expression, so a position in the wrapped file maps back to one in src.
const (
	stmtPrefix = "package p\nfunc _() {\n"
	declPrefix = "package p\n"
)

// parseEntry parses one entry the way the rest of this package needs it: first
// as a function body, then as a top-level declaration. It reports the byte
// offset of src within the file it built, so callers can map an identifier's
// position back onto the user's own source.
func parseEntry(src string) (f *ast.File, fset *token.FileSet, off int, ok bool) {
	fset = token.NewFileSet()
	f, err := parser.ParseFile(fset, "", stmtPrefix+src+"\n}", 0)
	if err == nil {
		return f, fset, len(stmtPrefix), true
	}
	// Declarations do not fit inside a function body.
	fset = token.NewFileSet()
	f, err = parser.ParseFile(fset, "", declPrefix+src, 0)
	if err != nil {
		return nil, nil, 0, false
	}
	return f, fset, len(declPrefix), true
}

// Ordinals numbers the entries that actually print a value, 1-based, and
// returns 0 for every entry that does not. Declarations, statements and
// zero-result calls consume no ordinal, so the numbering matches the values the
// user watched go by rather than the lines they typed.
//
// It is a forward scan, and Session.Pop only ever truncates, so an ordinal
// never changes meaning under :undo.
func Ordinals(s *session.Session) []int {
	out := make([]int, len(s.Entries))
	n := 0
	for i, e := range s.Entries {
		if e.Kind == session.KindExpr && !e.NoValue {
			n++
			out[i] = n
		}
	}
	return out
}

// use is one reference to a synthesized name inside an entry.
type use struct {
	off  int // byte offset in the entry's own source
	name string
	ord  int // the ordinal named, or 0 for `it`
}

// uses finds every reference to `it` or `_N` in src, in source order.
//
// It has to be an AST walk: `it` appears in ordinary English, and a text scan
// would rewrite it inside "wait for it" or a trailing comment. ast.Inspect only
// ever yields identifiers, so literals and comments are invisible to it.
//
// Over-approximating is safe and under-approximating is not — an unnecessary
// binding is still consumed by its own print call, while a missed one is
// `undefined: _3` — so anything ambiguous counts as a reference.
func uses(src string) []use {
	f, fset, off, ok := parseEntry(src)
	if !ok {
		return nil
	}

	var out []use
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			// In x.it the `it` is a field name, not a reference.
			ast.Inspect(n.X, func(inner ast.Node) bool {
				if id, ok := inner.(*ast.Ident); ok {
					if u, ok := identUse(id, fset, off); ok {
						out = append(out, u)
					}
				}
				return true
			})
			return false
		case *ast.KeyValueExpr:
			// A struct-literal key is a field name; the value is not.
			ast.Inspect(n.Value, func(inner ast.Node) bool {
				if id, ok := inner.(*ast.Ident); ok {
					if u, ok := identUse(id, fset, off); ok {
						out = append(out, u)
					}
				}
				return true
			})
			return false
		case *ast.Ident:
			if u, ok := identUse(n, fset, off); ok {
				out = append(out, u)
			}
		}
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i].off < out[j].off })
	return out
}

func identUse(id *ast.Ident, fset *token.FileSet, off int) (use, bool) {
	if !Synthetic(id.Name) {
		return use{}, false
	}
	u := use{off: fset.Position(id.Pos()).Offset - off, name: id.Name}
	if id.Name != ItName {
		n, err := strconv.Atoi(id.Name[1:])
		if err != nil {
			return use{}, false
		}
		u.ord = n
	}
	return u, true
}

// resolve rewrites every `it` in src to the ordinal in scope at that point, and
// reports which ordinals the entry ends up referencing.
//
// Splices run right to left so an earlier offset stays valid after a later one
// has changed length.
func resolve(src string, us []use, prev int) (string, map[int]bool, error) {
	need := map[int]bool{}
	for _, u := range us {
		if u.name == ItName {
			continue
		}
		// References always resolve backwards, so anything past the newest
		// ordinal in scope names a value that does not exist. Catching it here
		// gives the user their own name back instead of `undefined: _9`.
		if u.ord > prev {
			return "", nil, fmt.Errorf("%s does not name a value yet — "+
				"the session has printed %d", u.name, prev)
		}
		need[u.ord] = true
	}
	out := []byte(src)
	for i := len(us) - 1; i >= 0; i-- {
		u := us[i]
		if u.name != ItName {
			continue
		}
		if prev == 0 {
			return "", nil, errNoPrevValue
		}
		need[prev] = true
		out = append(out[:u.off], append([]byte(OrdName(prev)), out[u.off+len(ItName):]...)...)
	}
	return string(out), need, nil
}

// lastResults resolves every `it` in the session to the ordinal in scope where
// it was typed, and reports which ordinals some entry addresses — the set that
// has to be bound on the way past.
func lastResults(s *session.Session) ([]string, map[int]bool, error) {
	ord := Ordinals(s)
	srcs := make([]string, len(s.Entries))
	need := map[int]bool{}
	// Names the user has bound are the user's from that entry onward. Their
	// own `it` shadows the REPL's, exactly as a local shadows anything else.
	userBound := map[string]bool{}
	prev := 0

	for i, e := range s.Entries {
		srcs[i] = e.Src

		bound := boundNames(e.Src)
		var us []use
		for _, u := range uses(e.Src) {
			if userBound[u.name] || bound[u.name] {
				continue
			}
			us = append(us, u)
		}

		if len(us) > 0 {
			if e.Kind == session.KindDecl {
				return nil, nil, fmt.Errorf("%s cannot be used in a declaration: "+
					"it is hoisted above main, where the session's values are not in scope",
					us[0].name)
			}
			src, n, err := resolve(e.Src, us, prev)
			if err != nil {
				return nil, nil, err
			}
			srcs[i] = src
			for k := range n {
				need[k] = true
			}
		}

		for _, b := range e.Binds {
			userBound[b] = true
		}
		for name := range bound {
			if Synthetic(name) {
				userBound[name] = true
			}
		}
		if ord[i] != 0 {
			prev = ord[i]
		}
	}

	// Only a single value can be bound to a name. Refusing here names the
	// ordinal the user actually typed; letting it through would surface as
	// "assignment mismatch" reported against the earlier line instead.
	for i, e := range s.Entries {
		if ord[i] == 0 || !need[ord[i]] || e.Values <= 1 {
			continue
		}
		return nil, nil, fmt.Errorf("%s is %d values, so it cannot be carried forward — "+
			"bind them yourself:  a, b := %s", OrdName(ord[i]), e.Values, e.Src)
	}
	return srcs, need, nil
}

// Uses reports which ordinals an entry addresses, and whether it says `it`.
// It exists so :drop can refuse to renumber a value something later names —
// `it` counts, because it resolves to whichever value came last.
func Uses(src string) (ords []int, usesIt bool) {
	for _, u := range uses(src) {
		if u.name == ItName {
			usesIt = true
			continue
		}
		ords = append(ords, u.ord)
	}
	return ords, usesIt
}
