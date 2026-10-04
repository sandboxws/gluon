package inspect

import (
	"go/token"
	"go/types"
	"sort"
)

// MemberNames lists what can follow a dot after this value: its fields and its
// methods, together, because that is how a selector reads and completion has no
// reason to separate them.
//
// Both method sets are included. The value/pointer receiver rule decides
// whether a *call* is legal on an unaddressable value, but a variable is
// addressable, and every value a session binds is a variable — so offering only
// T's set would hide most of what a name can actually do.
//
// Accessibility is enforced rather than assumed: an unexported member of
// another package's type cannot be written here, and offering it would produce
// a line that does not compile.
func MemberNames(t *Target) []string {
	if t == nil || t.Type == nil {
		return nil
	}
	base := t.Type
	if p, ok := base.Underlying().(*types.Pointer); ok {
		base = p.Elem()
	}

	seen := map[string]bool{}
	var out []string
	add := func(name string, pkg *types.Package) {
		if seen[name] || !accessible(name, pkg, t.Pkg) {
			return
		}
		seen[name] = true
		out = append(out, name)
	}

	if st, ok := base.Underlying().(*types.Struct); ok {
		for i := range st.NumFields() {
			f := st.Field(i)
			add(f.Name(), f.Pkg())
		}
	}
	if in, ok := base.Underlying().(*types.Interface); ok {
		for i := range in.NumMethods() {
			m := in.Method(i)
			add(m.Name(), m.Pkg())
		}
	}
	for _, set := range [][]*types.Selection{
		typeutilMethodSet(base),
		typeutilMethodSet(types.NewPointer(base)),
	} {
		for _, sel := range set {
			add(sel.Obj().Name(), sel.Obj().Pkg())
		}
	}

	sort.Strings(out)
	return out
}

// PackageMembers lists the names a package qualifier can be followed by.
// Only exported ones: the session is a different package, so the rest are not
// spellable from here.
func PackageMembers(p *types.Package) []string {
	if p == nil {
		return nil
	}
	sc := p.Scope()
	out := make([]string, 0, sc.Len())
	for _, name := range sc.Names() {
		if token.IsExported(name) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// accessible reports whether the session's package may name this member.
func accessible(name string, owner, sess *types.Package) bool {
	if token.IsExported(name) {
		return true
	}
	// A type the session declared itself: its unexported fields are in scope
	// here, and hiding them would make completion useless for exactly the
	// types the user just wrote.
	return owner != nil && sess != nil && owner == sess
}

// FieldNames lists only the struct fields of a type, in declaration order.
//
// It is separate from MemberNames because a composite literal takes fields and
// nothing else: `Point{Dist: 1}` does not compile, so offering a method there
// would be offering a mistake. Order is the struct's own rather than
// alphabetical, since that is the order the fields are usually written in and
// the order a positional literal would need.
//
// An embedded field is offered under its type name, which is how it is spelled
// in a literal.
func FieldNames(t *Target) []string {
	if t == nil || t.Type == nil {
		return nil
	}
	base := t.Type
	if p, ok := base.Underlying().(*types.Pointer); ok {
		base = p.Elem()
	}
	st, ok := base.Underlying().(*types.Struct)
	if !ok {
		return nil
	}

	var out []string
	for i := range st.NumFields() {
		f := st.Field(i)
		if !accessible(f.Name(), f.Pkg(), t.Pkg) {
			continue
		}
		out = append(out, f.Name())
	}
	return out
}
