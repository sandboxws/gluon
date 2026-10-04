package render

import (
	"fmt"
	"go/ast"
	"go/token"

	"github.com/sandboxws/gluon/internal/session"
)

// Pinning is the answer to the one property replay cannot lose on its own:
// every line re-renders the whole session, so an entry that fetched a URL or
// wrote a file does it again on every subsequent line. A pinned entry is
// simply not emitted — the user saying "this already happened".
//
// That is sound only when nothing later needs what the entry bound. gluon does
// not reconstruct the value: a scalar's encoded repr is a *display* string
// (a uint8 reads `195 (0xc3)`, a rune `99 'c'`), so rebuilding a literal from
// it would be parsing prose, and a value that came back through the printer
// has already lost the aliasing the printer exists to tell the truth about.
// So the rule is the honest one: pin what nothing depends on, and refuse the
// rest with the reason.

// PinBlockers reports why entry i cannot be pinned, as sentences fit to print.
// An empty result means pinning is safe. It is deliberately over-cautious:
// a reference from an already-pinned entry still counts, because refusing a
// pin costs a line of typing and allowing a wrong one costs `undefined: _3`.
func PinBlockers(s *session.Session, i int) []string {
	if i < 0 || i >= len(s.Entries) {
		return []string{fmt.Sprintf("there is no entry %d", i+1)}
	}
	e := s.Entries[i]

	if e.Kind == session.KindDecl {
		return []string{"it is a declaration — it binds a name and runs nothing, so there is nothing to pin"}
	}

	var out []string

	// An ordinal a later entry addresses has to be bound on the way past, and
	// a pinned entry binds nothing.
	if ord := Ordinals(s)[i]; ord != 0 {
		if _, need, err := lastResults(s); err == nil && need[ord] {
			out = append(out, fmt.Sprintf("%s is used later — :unpin it or :drop the line that uses it",
				OrdName(ord)))
		}
	}

	for _, name := range e.Binds {
		if j, ok := firstUseAfter(s, i, name); ok {
			out = append(out, fmt.Sprintf("%s is used by entry %d — pinning would leave it undefined",
				name, j+1))
		}
	}
	return out
}

// firstUseAfter reports the first entry after i that reads name, stopping at
// any entry that re-declares it — from there the name is that entry's, and
// entry i's binding is dead anyway.
//
// A read is checked before a re-declaration within the same entry, because
// `x := x + 1` does both and the read is the one that would break.
func firstUseAfter(s *session.Session, i int, name string) (int, bool) {
	for j := i + 1; j < len(s.Entries); j++ {
		e := s.Entries[j]
		if !e.Pinned && references(e.Src, name) {
			return j, true
		}
		if !e.Pinned && bindsName(e, name) {
			return 0, false
		}
	}
	return 0, false
}

func bindsName(e session.Entry, name string) bool {
	for _, b := range e.Binds {
		if b == name {
			return true
		}
	}
	return false
}

// references reports whether src *reads* name. Four positions spell a name
// without reading it, and each one would otherwise refuse a pin that is
// perfectly safe:
//
//	x.Sel          the Sel is a field, only x is read
//	T{Key: v}      the Key is a field
//	x := expr      the left of a := is being bound, not read
//	struct{ x int} a field's own name is a declaration
//
// Everything else counts. Over-approximating past this point is deliberate:
// it costs a refused pin, where under-approximating costs `undefined: x`.
func references(src, name string) bool {
	f, _, _, ok := parseEntry(src)
	if !ok {
		// An entry that will not parse cannot be reasoned about; assume it
		// reads the name rather than pin something it might need.
		return true
	}
	found := false
	var walk func(ast.Node) bool
	walk = func(n ast.Node) bool {
		if found {
			return false
		}
		switch n := n.(type) {
		case *ast.SelectorExpr:
			ast.Inspect(n.X, walk)
			return false
		case *ast.KeyValueExpr:
			ast.Inspect(n.Value, walk)
			return false
		case *ast.AssignStmt:
			// A plain `=` needs the name to already exist, so both sides read.
			// A `:=` binds its left, so only the right does.
			if n.Tok == token.DEFINE {
				for _, rhs := range n.Rhs {
					ast.Inspect(rhs, walk)
				}
				return false
			}
		case *ast.ValueSpec:
			// var x T = expr — Names are bound, Type and Values are read.
			if n.Type != nil {
				ast.Inspect(n.Type, walk)
			}
			for _, v := range n.Values {
				ast.Inspect(v, walk)
			}
			return false
		case *ast.Field:
			// A struct field, parameter or result: the name is the
			// declaration, the type is what it refers to.
			ast.Inspect(n.Type, walk)
			return false
		case *ast.Ident:
			if n.Name == name {
				found = true
			}
		}
		return true
	}
	ast.Inspect(f, walk)
	return found
}

// Reads is references, for a caller outside this package that has to know
// whether an entry depends on a name. :test asks it the same question pinning
// does — which earlier entries a line cannot be separated from — and gets the
// same over-cautious answer, for the same reason.
func Reads(src, name string) bool { return references(src, name) }
