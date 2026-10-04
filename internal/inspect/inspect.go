// Package inspect answers the questions a REPL exists for: what is this, and
// what can it do. It is Ruby's .class and .methods, in a language where both
// answers are static and so can be given without running anything.
//
// Everything here reads a type-checked session. Nothing here builds or runs a
// program, which is why :t and :m answer in about a millisecond.
package inspect

import (
	"fmt"
	"go/types"
	"sort"
	"strings"

	"github.com/sandboxws/gluon/internal/check"
	"github.com/sandboxws/gluon/internal/pretty"
	"github.com/sandboxws/gluon/internal/render"
)

// Target is the thing the user asked about, resolved from the trailing
// expression of an analyzed session.
type Target struct {
	// Type is the expression's type, or the type itself when the user named a
	// type rather than a value.
	Type types.Type
	// IsType records that the user named a type (`:m Point`) rather than a
	// value (`:m p`). The distinction changes nothing about the method set but
	// everything about how the answer should read.
	IsType bool
	// Void is a call with no results — there is nothing to describe.
	Void bool
	// Tuple holds the components of a multi-value call.
	Tuple []types.Type
	// Const is the compile-time value, when there is one.
	Const string
	// Pkg is the session's own package, for shortening qualified names.
	Pkg *types.Package
}

// Resolve reads the analyzed session's trailing expression.
func Resolve(r *check.Result) (*Target, error) {
	tail, ok := r.Printed(render.PrintFunc)
	if !ok {
		return nil, fmt.Errorf("could not find the expression to inspect")
	}
	t := &Target{Pkg: r.Pkg, Void: tail.Void, IsType: tail.TV.IsType()}

	if tail.TV.Type == nil {
		// The checker could not type it at all; its diagnostics say why.
		if len(r.Errs) > 0 {
			return nil, fmt.Errorf("%s", r.Errs[0].Msg)
		}
		return nil, fmt.Errorf("could not determine a type")
	}
	if tail.TV.Value != nil {
		t.Const = tail.TV.Value.String()
	}

	if tup, ok := tail.TV.Type.(*types.Tuple); ok {
		for i := range tup.Len() {
			t.Tuple = append(t.Tuple, tup.At(i).Type())
		}
		if tup.Len() > 0 {
			t.Type = tup.At(0).Type()
		}
		return t, nil
	}
	t.Type = tail.TV.Type
	return t, nil
}

// qualifier shortens type names for display: bytes.Buffer rather than the full
// import path, and a bare Point for a type the session itself declared.
func qualifier(sess *types.Package) types.Qualifier {
	return func(p *types.Package) string {
		if p == nil || p == sess {
			return ""
		}
		return p.Name()
	}
}

func (t *Target) name(ty types.Type) string {
	return types.TypeString(ty, qualifier(t.Pkg))
}

// Name is the target's own type as it displays — the static half of :t -d,
// which the command has to hold on to across a run.
func (t *Target) Name() string { return t.name(t.Type) }

// IsInterface reports that the static type's underlying type is an interface,
// which is the one thing that decides whether a dynamic type can differ from
// the static one.
func (t *Target) IsInterface() bool {
	if t.Type == nil {
		return false
	}
	_, ok := t.Type.Underlying().(*types.Interface)
	return ok
}

// Describe is :t — Ruby's .class. Verbose adds the underlying type, which is
// the thing worth seeing for a named type like `type Celsius float64`.
func (t *Target) Describe(verbose bool, st pretty.Styles) string {
	if t.Void {
		return st.Annot.Render("no value — this call returns nothing")
	}

	var b strings.Builder
	switch {
	case len(t.Tuple) > 1:
		parts := make([]string, 0, len(t.Tuple))
		for _, ty := range t.Tuple {
			parts = append(parts, st.Type.Render(t.name(ty)))
		}
		b.WriteString("(" + strings.Join(parts, ", ") + ")")
		b.WriteString(st.Annot.Render(fmt.Sprintf("  %d values", len(t.Tuple))))
	case t.IsType:
		b.WriteString(st.Type.Render(t.name(t.Type)))
		b.WriteString(st.Annot.Render("  a type, not a value"))
	default:
		b.WriteString(st.Type.Render(t.name(t.Type)))
		if t.Const != "" {
			b.WriteString(st.Annot.Render("  constant " + t.Const))
		}
	}

	if !verbose {
		if hint := t.hint(st); hint != "" {
			b.WriteString("\n" + hint)
		}
		return b.String()
	}

	for _, row := range t.detail() {
		b.WriteString("\n  " + st.Annot.Render(row[0]) + "  " + row[1])
	}
	return b.String()
}

// hint is the one line worth volunteering without -v. An interface's static
// type says nothing about what is actually in it, and that gap is the single
// most common surprise in Go.
func (t *Target) hint(st pretty.Styles) string {
	if t.Type == nil || len(t.Tuple) > 1 {
		return ""
	}
	if _, ok := t.Type.Underlying().(*types.Interface); ok && !t.IsType {
		return st.Annot.Render("  an interface — print the value to see its dynamic type")
	}
	return ""
}

// Dynamic is what a run reported about a value: the type actually inside it.
// It is the answer hint has been pointing at since v2 — the one thing about a
// value the type checker cannot be asked.
type Dynamic struct {
	// Type is the value's %T, already stripped of the child's own package
	// qualifier. Empty when the interface held nothing.
	Type string
	// Kind is reflect's kind for it, empty for a nil interface.
	Kind string
	// Nil records an interface holding nothing. It is not a type and must not
	// be reported as one: `<nil>` printed where a type name goes reads as a
	// type called nil.
	Nil bool
}

// DescribeDynamic is :t -d — the static type beside what was actually in it.
//
// static is empty when the checker could not answer, which is not a reason to
// withhold the half that was measured (invariant 5). concrete says the static
// type's underlying type is not an interface: the one case where the two
// halves cannot differ, so the type is named once rather than twice under two
// labels that mean the same thing.
//
// The two names are never compared. types.TypeString and %T do not spell every
// type the same way — []byte against []uint8, a qualifier against none — and
// deciding that two spellings name one type would be the checker acting as an
// authority on a question a run had already answered.
func DescribeDynamic(static string, concrete bool, d Dynamic, st pretty.Styles) string {
	var b strings.Builder
	if concrete && !d.Nil {
		b.WriteString(st.Type.Render(static))
		b.WriteString(st.Annot.Render("  concrete — the static type is the dynamic type"))
		return b.String()
	}
	b.WriteString(st.Annot.Render("static "))
	if static == "" {
		b.WriteString(st.Annot.Render("unavailable"))
	} else {
		b.WriteString(st.Type.Render(static))
	}
	if d.Nil {
		b.WriteString(st.Annot.Render("  nil — an interface holding nothing has no dynamic type"))
		return b.String()
	}
	b.WriteString(st.Annot.Render(", dynamic ") + st.Type.Render(d.Type))
	if d.Kind != "" {
		b.WriteString(st.Annot.Render("  " + d.Kind))
	}
	return b.String()
}

// detail is the -v body.
func (t *Target) detail() [][2]string {
	if t.Type == nil {
		return nil
	}
	var rows [][2]string
	if u := t.Type.Underlying(); u != t.Type {
		rows = append(rows, [2]string{"underlying", t.name(u)})
	}
	if b, ok := t.Type.Underlying().(*types.Basic); ok {
		rows = append(rows, [2]string{"kind", basicKind(b)})
	}
	switch u := t.Type.Underlying().(type) {
	case *types.Slice:
		rows = append(rows, [2]string{"element", t.name(u.Elem())})
	case *types.Map:
		rows = append(rows, [2]string{"key", t.name(u.Key())})
		rows = append(rows, [2]string{"element", t.name(u.Elem())})
	case *types.Chan:
		rows = append(rows, [2]string{"direction", chanDir(u.Dir())})
		rows = append(rows, [2]string{"element", t.name(u.Elem())})
	case *types.Pointer:
		rows = append(rows, [2]string{"points to", t.name(u.Elem())})
	case *types.Interface:
		rows = append(rows, [2]string{"methods", fmt.Sprintf("%d", u.NumMethods())})
	case *types.Struct:
		rows = append(rows, [2]string{"fields", fmt.Sprintf("%d", u.NumFields())})
	}
	return rows
}

func basicKind(b *types.Basic) string {
	switch {
	case b.Info()&types.IsUntyped != 0:
		return "untyped constant"
	case b.Info()&types.IsInteger != 0:
		return "integer"
	case b.Info()&types.IsFloat != 0:
		return "floating point"
	case b.Info()&types.IsString != 0:
		return "string"
	case b.Info()&types.IsBoolean != 0:
		return "boolean"
	case b.Info()&types.IsComplex != 0:
		return "complex"
	}
	return b.String()
}

func chanDir(d types.ChanDir) string {
	switch d {
	case types.SendOnly:
		return "send only (chan<-)"
	case types.RecvOnly:
		return "receive only (<-chan)"
	}
	return "bidirectional"
}

// Method is one entry in a method set.
type Method struct {
	Name string
	Sig  string
	// PointerOnly marks a method declared on *T rather than T. It is in *T's
	// method set and not in T's, which is the value/pointer receiver rule and
	// a genuine Go stumbling block — so it is called out rather than merged.
	PointerOnly bool
}

// Methods is :m — Ruby's .methods, plus the thing Ruby has no need for: which
// interfaces the type satisfies.
func (t *Target) Methods(ifaces []Named) (methods []Method, satisfied []string, ptrSatisfied []string) {
	if t.Type == nil {
		return nil, nil, nil
	}
	base := t.Type
	ptr := types.Type(types.NewPointer(base))
	if p, ok := base.Underlying().(*types.Pointer); ok {
		// *T already; asking about **T would find nothing.
		ptr, base = base, p.Elem()
	}
	if _, ok := base.Underlying().(*types.Interface); ok {
		// An interface has no pointer method set worth showing.
		ptr = base
	}

	value := map[string]bool{}
	for _, sel := range typeutilMethodSet(base) {
		value[sel.Obj().Name()] = true
		methods = append(methods, Method{
			Name: sel.Obj().Name(),
			Sig:  t.sigOf(sel),
		})
	}
	for _, sel := range typeutilMethodSet(ptr) {
		if value[sel.Obj().Name()] {
			continue
		}
		methods = append(methods, Method{
			Name:        sel.Obj().Name(),
			Sig:         t.sigOf(sel),
			PointerOnly: true,
		})
	}
	sort.Slice(methods, func(i, j int) bool { return methods[i].Name < methods[j].Name })

	self := t.name(t.Type)
	for _, in := range ifaces {
		// An interface trivially implements itself, and saying so is noise.
		if in.Name == self {
			continue
		}
		switch {
		case types.Implements(base, in.Iface):
			satisfied = append(satisfied, in.Name)
		case types.Implements(ptr, in.Iface):
			ptrSatisfied = append(ptrSatisfied, in.Name)
		}
	}
	return methods, satisfied, ptrSatisfied
}

func (t *Target) sigOf(sel *types.Selection) string {
	sig, ok := sel.Obj().Type().(*types.Signature)
	if !ok {
		return ""
	}
	// TypeString on a signature leads with "func"; the name reads better.
	return strings.TrimPrefix(types.TypeString(sig, qualifier(t.Pkg)), "func")
}

// typeutilMethodSet returns the selections in a type's method set. Promoted
// methods from embedded types come along for free.
func typeutilMethodSet(t types.Type) []*types.Selection {
	ms := types.NewMethodSet(t)
	out := make([]*types.Selection, 0, ms.Len())
	for i := range ms.Len() {
		out = append(out, ms.At(i))
	}
	return out
}

// Named is an interface to test satisfaction against.
type Named struct {
	Name  string
	Iface *types.Interface
}
