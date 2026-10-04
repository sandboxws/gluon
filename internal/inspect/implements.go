package inspect

import (
	"errors"
	"fmt"
	"go/ast"
	"go/types"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"

	"github.com/sandboxws/gluon/internal/check"
	"github.com/sandboxws/gluon/internal/pretty"
	"github.com/sandboxws/gluon/internal/render"
)

// This file answers the interface questions go/types can settle without
// building anything: whether a type satisfies an interface, and — the half
// that makes a "no" useful — which method is in the way.
//
// Everything here reads one *check.Result, and for the two-argument commands
// that is a correctness rule rather than a convenience. A named type's
// identity in go/types is the object the check created, and every check
// creates new ones, so a type resolved in one run and an interface resolved in
// another compare as different even when they are the same declaration:
//
//	type ID string
//	type Store interface{ Get(ID) error }
//	type S struct{}
//	func (S) Get(ID) error { return nil }
//
// Resolved in two runs, S "does not implement" Store and the report names Get
// as having the wrong signature — printing the same signature twice as the
// required and the actual one. That is invariant 5's failure exactly: a
// confident verdict the compiler would not give. So both operands come out of
// a single check.

// Two failures worth telling apart, because the commands say different things
// about them: one is "I have never heard of that name", the other is "I know
// it, and it is not an interface".
var (
	ErrUnresolved   = errors.New("unresolved")
	ErrNotInterface = errors.New("not an interface")
)

// ProbeExpr is the expression a command hands to Analyze when it needs several
// names resolved together.
//
// `[]any{a, b}` is the smallest form that carries both: it parses as an
// expression, so session.Classify accepts it; goimports sees the qualifier on
// each side, so `:impl bytes.Buffer, io.Writer` pulls in both packages even
// though the session imported neither; and go/types records a type named in
// value position in Info.Types before it complains about it, which is the same
// behaviour `:t io.Writer` has always relied on. The diagnostics the probe
// provokes are ignored — Operands reads types, not verdicts.
func ProbeExpr(args []string) string {
	return "[]any{" + strings.Join(args, ", ") + "}"
}

// An Operand is one argument of a multi-argument command, as the checker saw
// it.
type Operand struct {
	// Src is what the user wrote, for the error messages that have to name it.
	Src string
	// Type is nil when the name resolved to nothing.
	Type types.Type
	// IsType records that the argument named a type rather than a value. The
	// distinction is what separates :impl's question from :sat's.
	IsType bool
	// Values is how many results a call yields; 1 for an ordinary value.
	Values int
	// Pkg is the session's package, for shortening displayed names.
	Pkg *types.Package
}

// Operands reads back what ProbeExpr's arguments resolved to.
func Operands(r *check.Result, args []string) ([]Operand, error) {
	tail, ok := r.Printed(render.PrintFunc)
	if !ok {
		return nil, errors.New("could not find the expression to inspect")
	}
	lit, ok := tail.Expr.(*ast.CompositeLit)
	if !ok || len(lit.Elts) != len(args) {
		// The probe is built two lines from here; if it does not come back in
		// the shape it went out, something rewrote it and no answer given from
		// the pieces would be trustworthy.
		return nil, errors.New("could not read the arguments back from the type checker")
	}
	out := make([]Operand, len(args))
	for i, e := range lit.Elts {
		op := Operand{Src: args[i], Pkg: r.Pkg, Values: 1}
		tv, seen := r.Info.Types[e]
		switch {
		case !seen || tv.Type == nil || !isValid(tv.Type):
			// Left with a nil Type: the name resolved to nothing the session
			// can see, which every caller reports rather than working around.
		case tv.IsVoid():
			op.Values = 0
		default:
			op.Type, op.IsType = tv.Type, tv.IsType()
			if tup, ok := tv.Type.(*types.Tuple); ok {
				op.Values = tup.Len()
				if tup.Len() > 0 {
					op.Type = tup.At(0).Type()
				}
			}
		}
		out[i] = op
	}
	return out, nil
}

// isValid rejects the invalid type go/types records for a name it could not
// resolve. Without this an undefined name would read as a resolved one whose
// method set is empty, and "implements nothing" is a verdict, not a failure.
func isValid(t types.Type) bool {
	b, ok := t.(*types.Basic)
	return !ok || b.Kind() != types.Invalid
}

// Iface resolves an operand to the interface it names.
func (op Operand) Iface() (*types.Interface, error) {
	if op.Type == nil {
		return nil, ErrUnresolved
	}
	in, ok := op.Type.Underlying().(*types.Interface)
	if !ok {
		return nil, ErrNotInterface
	}
	return in, nil
}

// Name is the operand's type as it should display.
func (op Operand) Name() string {
	if op.Type == nil {
		return op.Src
	}
	return types.TypeString(op.Type, qualifier(op.Pkg))
}

// Kind names what a type is, for the message that has to say why it is not an
// interface.
func Kind(t types.Type) string {
	switch u := t.Underlying().(type) {
	case *types.Struct:
		return "a struct"
	case *types.Basic:
		return "a " + basicKind(u)
	case *types.Slice:
		return "a slice"
	case *types.Array:
		return "an array"
	case *types.Map:
		return "a map"
	case *types.Chan:
		return "a channel"
	case *types.Pointer:
		return "a pointer"
	case *types.Signature:
		return "a function"
	case *types.Interface:
		return "an interface"
	}
	return "not an interface"
}

// Satisfaction is the answer to "does this implement that", with the part that
// makes a "no" worth having.
type Satisfaction struct {
	// Expr is the expression asked about, set only by :sat. :impl names a
	// type, and Type already is it.
	Expr  string
	Type  string
	Iface string
	OK    bool
	// PtrOK is the case that earns its own line: the value type does not
	// implement the interface but the pointer type does. It is the most common
	// cause, and the compiler's error for it names neither the method nor the
	// receiver rule.
	PtrOK bool
	// Method is the first method in the way, empty when OK.
	Method string
	Want   string // the signature the interface requires
	Have   string // the signature the type actually has, when Wrong
	// Wrong separates "the method is absent" from "the method is there with a
	// different signature". They read the same in the compiler's error and
	// call for entirely different fixes.
	Wrong bool
	// Note is a caveat printed under the verdict — :sat's static-type warning.
	Note string
}

// Satisfies reports whether v implements iface, and what is in the way when it
// does not.
//
// types.MissingMethod does the work rather than a walk of the method set: it is
// the computation the compiler itself performs, so the answer cannot disagree
// with the build, and it returns the offending *types.Func together with the
// flag separating an absent method from a mismatched one. It is asked twice,
// once for V and once for *V, because that second answer is what turns the
// flattest error in Go into an actionable one.
func Satisfies(pkg *types.Package, v types.Type, iface *types.Interface, ifaceName string) Satisfaction {
	q := qualifier(pkg)
	s := Satisfaction{Type: types.TypeString(v, q), Iface: ifaceName}
	if types.Implements(v, iface) {
		s.OK = true
		return s
	}

	// Only a non-pointer, non-interface V has a pointer question worth asking:
	// **T declares nothing of its own, and an interface has no pointer method
	// set to speak of.
	switch v.Underlying().(type) {
	case *types.Pointer, *types.Interface:
	default:
		s.PtrOK = types.Implements(types.NewPointer(v), iface)
	}

	m, wrong := types.MissingMethod(v, iface, true)
	if m == nil {
		// Implements said no and MissingMethod named nothing. That is the
		// checker contradicting itself; report the verdict without a cause
		// rather than inventing one.
		return s
	}
	s.Method, s.Want = m.Name(), signatureOf(m, q)
	// A pointer-only method is missing from V and present on *V. Calling that
	// a signature mismatch would send the reader looking at the wrong thing,
	// so PtrOK owns the explanation.
	s.Wrong = wrong && !s.PtrOK
	if s.Wrong {
		if obj, _, _ := types.LookupFieldOrMethod(v, true, pkg, m.Name()); obj != nil {
			if f, ok := obj.(*types.Func); ok {
				s.Have = signatureOf(f, q)
			}
		}
	}
	return s
}

// signatureOf renders a method's signature without the leading "func", the way
// sigOf does for a method set row: the name reads better in front of it.
func signatureOf(f *types.Func, q types.Qualifier) string {
	return strings.TrimPrefix(types.TypeString(f.Type(), q), "func")
}

// CastVerdict is what :cast concluded about `e.(t)`.
type CastVerdict int

const (
	// CastNotInterface: e is not an interface value, so there is nothing to
	// assert about.
	CastNotInterface CastVerdict = iota
	// CastImpossible: no value of e's type can ever be a t, so the compiler
	// rejects the assertion outright.
	CastImpossible
	// CastRuntime: the assertion compiles, and the dynamic type decides.
	CastRuntime
)

// Cast is the preview of a type assertion.
type Cast struct {
	Expr    string
	Static  string // e's static type
	Target  string // the type being asserted to
	Verdict CastVerdict
	Method  string
	Want    string
	Have    string
	Wrong   bool
}

// Assertion answers whether `e.(t)` is legal.
//
// The rule is the compiler's, and so is the call: for a concrete target every
// method of the interface must be present, while for an interface target only
// the methods both declare have to agree — which is exactly what
// types.MissingMethod's static=false mode means. Passing the flag through
// rather than branching on it keeps this from drifting away from the spec.
func Assertion(pkg *types.Package, e Operand, target Operand) Cast {
	q := qualifier(pkg)
	c := Cast{Expr: e.Src, Static: e.Name(), Target: target.Name()}
	iface, ok := e.Type.Underlying().(*types.Interface)
	if !ok {
		c.Verdict = CastNotInterface
		return c
	}
	m, wrong := types.MissingMethod(target.Type, iface, false)
	if m == nil {
		c.Verdict = CastRuntime
		return c
	}
	c.Verdict = CastImpossible
	c.Method, c.Wrong, c.Want = m.Name(), wrong, signatureOf(m, q)
	if wrong {
		if obj, _, _ := types.LookupFieldOrMethod(target.Type, true, pkg, m.Name()); obj != nil {
			if f, ok := obj.(*types.Func); ok {
				c.Have = signatureOf(f, q)
			}
		}
	}
	return c
}

// Iface is an interface definition and who satisfies it.
type Iface struct {
	Name    string
	Methods []Method
	Embeds  []string
	// Value and Pointer are the implementors, kept apart because the
	// value/pointer receiver rule is the thing the list is most often consulted
	// about.
	Value   []string
	Pointer []string
	// Scanned is how many named types the search considered. It is printed
	// when nothing implements the interface, so "nothing does" and "the search
	// did not run" cannot read the same.
	Scanned int
	// Extra is how many implementors were found beyond those listed.
	Extra int
	// SkippedGeneric names the generic types the search passed over. They are
	// not implementors and must not be listed as any, but omitting them
	// silently makes "it does not implement this" and "it was never asked"
	// look identical — the same distinction Scanned exists for.
	SkippedGeneric []string
}

// maxImplementors caps the printed list. A session that imports fmt and os and
// asks about error would otherwise get a screen of stdlib types before the one
// it declared itself.
const maxImplementors = 20

// DescribeInterface reads an interface's own definition and searches for
// implementors.
//
// The search covers what the session can actually name: its own package scope,
// the packages its program imports, and any host package already loaded. It
// does not force the host's package graph to load, because `go list -export`
// compiles what it lists — a static command must not turn into a build — and
// it does not reach into the module cache, which would surface types the
// session has no way to refer to.
func DescribeInterface(r *check.Result, op Operand, in *types.Interface, extra []*types.Package) *Iface {
	q := qualifier(r.Pkg)
	out := &Iface{Name: op.Name()}

	for i := range in.NumMethods() {
		m := in.Method(i)
		out.Methods = append(out.Methods, Method{Name: m.Name(), Sig: signatureOf(m, q)})
	}
	sort.Slice(out.Methods, func(i, j int) bool { return out.Methods[i].Name < out.Methods[j].Name })
	for i := range in.NumEmbeddeds() {
		out.Embeds = append(out.Embeds, types.TypeString(in.EmbeddedType(i), q))
	}

	self := op.Name()
	var value, pointer, skipped []string
	for _, pkg := range searchPackages(r, extra) {
		for _, name := range pkg.Scope().Names() {
			if strings.HasPrefix(name, "__gluon") {
				continue // gluon's own injected machinery
			}
			obj, ok := pkg.Scope().Lookup(name).(*types.TypeName)
			if !ok || obj.IsAlias() {
				continue
			}
			if pkg != r.Pkg && !obj.Exported() {
				// io.nopCloser really does implement io.Reader, and naming it
				// helps nobody: the session cannot write it down. Its own
				// package is the exception — everything the user declared is
				// unexported and all of it is nameable.
				continue
			}
			named, ok := obj.Type().(*types.Named)
			if !ok {
				continue
			}
			if named.TypeParams().Len() > 0 {
				// A generic type implements nothing until it is instantiated,
				// and this search has no instantiation to offer. Trying one
				// was rejected: there is no principled argument to choose, and
				// a lucky match would teach a false implementation. So it is
				// named as skipped rather than dropped.
				skipped = append(skipped, namedName(q, named))
				continue
			}
			shown := types.TypeString(named, q)
			if shown == self {
				continue // an interface trivially implements itself
			}
			out.Scanned++
			switch {
			case types.Implements(named, in):
				value = append(value, shown)
			case types.Implements(types.NewPointer(named), in):
				pointer = append(pointer, shown)
			}
		}
	}
	sort.Strings(value)
	sort.Strings(pointer)
	sort.Strings(skipped)
	out.Value, out.Pointer, out.Extra = capList(value, pointer)
	out.SkippedGeneric = skipped
	return out
}

// capList trims the two implementor lists to maxImplementors between them and
// reports how many were left out. Value implementors are kept first: they are
// the ones the reader can use without taking an address.
func capList(value, pointer []string) (v, p []string, extra int) {
	total := len(value) + len(pointer)
	if total <= maxImplementors {
		return value, pointer, 0
	}
	if len(value) >= maxImplementors {
		return value[:maxImplementors], nil, total - maxImplementors
	}
	room := maxImplementors - len(value)
	return value, pointer[:room], total - maxImplementors
}

// searchPackages is the scope the implementor search runs over: the session's
// own package, everything its program imports, and the already-loaded host
// packages the caller passed in. Deduplicated, because a host package is
// usually both.
func searchPackages(r *check.Result, extra []*types.Package) []*types.Package {
	seen := map[*types.Package]bool{}
	var out []*types.Package
	add := func(p *types.Package) {
		if p == nil || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	add(r.Pkg)
	for _, p := range r.Pkg.Imports() {
		add(p)
	}
	for _, p := range extra {
		add(p)
	}
	return out
}

// Embedded is one embedded field.
type Embedded struct {
	Name    string
	Type    string
	Pointer bool
}

// Promoted is a method embedding lifted into the outer type's method set.
type Promoted struct {
	Name string
	Sig  string
	// From is the embedded field it arrives through, Depth how many levels of
	// embedding it climbed. Depth 1 is a direct embed.
	From  string
	Depth int
	// PointerOnly marks a promotion that lands in *T's method set and not T's.
	PointerOnly bool
}

// Ambiguity is a method name two embedded types provide at the same depth.
type Ambiguity struct {
	Name string
	From []string
}

// Embedding is what :embeds reports.
type Embedding struct {
	Type      string
	Fields    []Embedded
	Promoted  []Promoted
	Ambiguous []Ambiguity
}

// Embeds reads a type's embedded fields and the methods they promote.
//
// Ambiguity is found rather than assumed: types.NewMethodSet leaves an
// ambiguous selector out of the set entirely, so a name that two embedded
// types both offer at the same depth and that is absent from the method set is
// exactly the case the compiler rejects. Reporting a winner there would teach
// something false about a selector that does not compile.
func Embeds(pkg *types.Package, t types.Type) *Embedding {
	q := qualifier(pkg)
	out := &Embedding{Type: types.TypeString(t, q)}

	base := t
	if p, ok := t.Underlying().(*types.Pointer); ok {
		base = p.Elem()
	}

	switch u := base.Underlying().(type) {
	case *types.Struct:
		for i := range u.NumFields() {
			f := u.Field(i)
			if !f.Embedded() {
				continue
			}
			_, isPtr := f.Type().(*types.Pointer)
			out.Fields = append(out.Fields, Embedded{
				Name:    f.Name(),
				Type:    types.TypeString(f.Type(), q),
				Pointer: isPtr,
			})
		}
	case *types.Interface:
		for i := range u.NumEmbeddeds() {
			et := u.EmbeddedType(i)
			out.Fields = append(out.Fields, Embedded{
				Name: embeddedName(et),
				Type: types.TypeString(et, q),
			})
		}
		// An embedded interface's methods land in the outer interface's set
		// outright — there is no receiver rule here, so these rows carry no
		// depth and no pointer marking, only where each method came from.
		explicit := map[string]bool{}
		for i := range u.NumExplicitMethods() {
			explicit[u.ExplicitMethod(i).Name()] = true
		}
		for i := range u.NumMethods() {
			m := u.Method(i)
			if explicit[m.Name()] {
				continue
			}
			out.Promoted = append(out.Promoted, Promoted{
				Name: m.Name(),
				Sig:  signatureOf(m, q),
				From: providerOf(u, m.Name(), q),
			})
		}
		sort.Slice(out.Promoted, func(i, j int) bool { return out.Promoted[i].Name < out.Promoted[j].Name })
		return out
	}
	if len(out.Fields) == 0 {
		return out
	}

	out.Promoted = promotedMethods(pkg, base, q)
	out.Ambiguous = ambiguous(pkg, base)
	return out
}

// promotedMethods reads the outer type's method set and keeps the entries that
// arrived through an embedded field. Selection.Index is the field path, so its
// length is the depth and its first element names the field it came through.
func promotedMethods(pkg *types.Package, base types.Type, q types.Qualifier) []Promoted {
	st, _ := base.Underlying().(*types.Struct)
	if st == nil {
		return nil
	}
	fieldName := func(i int) string {
		if i < 0 || i >= st.NumFields() {
			return ""
		}
		return st.Field(i).Name()
	}

	var out []Promoted
	seen := map[string]bool{}
	collect := func(t types.Type, ptrOnly bool) {
		ms := types.NewMethodSet(t)
		for i := range ms.Len() {
			sel := ms.At(i)
			idx := sel.Index()
			if len(idx) < 2 {
				continue // declared on the type itself, not promoted
			}
			name := sel.Obj().Name()
			if seen[name] {
				continue
			}
			seen[name] = true
			f, _ := sel.Obj().(*types.Func)
			sig := ""
			if f != nil {
				sig = signatureOf(f, q)
			}
			out = append(out, Promoted{
				Name:        name,
				Sig:         sig,
				From:        fieldName(idx[0]),
				Depth:       len(idx) - 1,
				PointerOnly: ptrOnly,
			})
		}
	}
	collect(base, false)
	collect(types.NewPointer(base), true)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ambiguous finds the method names embedding offers more than once at the same
// depth. The shallowest depth that offers a name is the only one that counts —
// Go's selector rule is depth-first-shallowest-wins — so a name resolved at
// depth 1 is not ambiguous however many times it recurs deeper.
func ambiguous(pkg *types.Package, base types.Type) []Ambiguity {
	have := map[string]bool{}
	for _, t := range []types.Type{base, types.NewPointer(base)} {
		ms := types.NewMethodSet(t)
		for i := range ms.Len() {
			have[ms.At(i).Obj().Name()] = true
		}
	}

	type node struct {
		typ  types.Type
		root string // the depth-1 field this branch came through
	}
	settled := map[string]bool{}
	var out []Ambiguity

	level := []node{{typ: base}}
	for depth := 0; depth < maxEmbedDepth && len(level) > 0; depth++ {
		var next []node
		offers := map[string]map[string]bool{}
		for _, n := range level {
			for _, name := range declaredMethods(n.typ) {
				if depth == 0 {
					// Declared on the type itself: it shadows everything
					// embedding could promote under that name.
					settled[name] = true
					continue
				}
				if offers[name] == nil {
					offers[name] = map[string]bool{}
				}
				offers[name][n.root] = true
			}
			for _, e := range embeddedFields(n.typ) {
				root := e.name
				if depth > 0 {
					root = n.root
				}
				next = append(next, node{typ: e.typ, root: root})
			}
		}
		for name, roots := range offers {
			if settled[name] {
				continue
			}
			settled[name] = true
			if len(roots) < 2 || have[name] {
				continue
			}
			names := make([]string, 0, len(roots))
			for r := range roots {
				names = append(names, r)
			}
			sort.Strings(names)
			out = append(out, Ambiguity{Name: name, From: names})
		}
		level = next
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// maxEmbedDepth bounds the walk. Go itself has no depth limit, but a type that
// embeds itself through a pointer is legal and would otherwise loop forever,
// and no real struct promotes a method from ten levels down.
const maxEmbedDepth = 10

type embField struct {
	name string
	typ  types.Type
}

// embeddedFields lists what a type embeds, following one level of pointer
// because *T embedded promotes exactly as T does.
func embeddedFields(t types.Type) []embField {
	if p, ok := t.Underlying().(*types.Pointer); ok {
		t = p.Elem()
	}
	var out []embField
	switch u := t.Underlying().(type) {
	case *types.Struct:
		for i := range u.NumFields() {
			if f := u.Field(i); f.Embedded() {
				out = append(out, embField{name: f.Name(), typ: f.Type()})
			}
		}
	case *types.Interface:
		for i := range u.NumEmbeddeds() {
			et := u.EmbeddedType(i)
			out = append(out, embField{name: embeddedName(et), typ: et})
		}
	}
	return out
}

// declaredMethods names the methods a type declares itself — not the ones it
// promotes. That is the distinction the ambiguity walk turns on: a promoted
// name belongs to the depth it was declared at, not the depth it surfaced.
func declaredMethods(t types.Type) []string {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	var out []string
	if named, ok := t.(*types.Named); ok {
		for i := range named.NumMethods() {
			out = append(out, named.Method(i).Name())
		}
	}
	if in, ok := t.Underlying().(*types.Interface); ok {
		for i := range in.NumExplicitMethods() {
			out = append(out, in.ExplicitMethod(i).Name())
		}
	}
	return out
}

// providerOf names the embedded interface a promoted method arrives through.
func providerOf(in *types.Interface, name string, q types.Qualifier) string {
	for i := range in.NumEmbeddeds() {
		et := in.EmbeddedType(i)
		if obj, _, _ := types.LookupFieldOrMethod(et, false, nil, name); obj != nil {
			return embeddedName(et)
		}
	}
	return types.TypeString(in, q)
}

// embeddedName is the field name an embedded type takes: the type's own name,
// with any pointer and qualification stripped.
func embeddedName(t types.Type) string {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	switch n := t.(type) {
	case *types.Named:
		return n.Obj().Name()
	case *types.Alias:
		return n.Obj().Name()
	}
	return types.TypeString(t, nil)
}

// Uninstantiated reports a generic named type used by its bare name — List
// rather than List[int].
//
// It is the distinction every question that turns on a method set has to make.
// An origin's methods still carry T in their signatures, so an answer computed
// from them is about a type that does not exist: asked whether Holder
// implements Container, go/types reports "required First() T, has First() int"
// — a confident no to a question the compiler was never asked, which is
// invariant 5's failure. types.Implements is explicitly unspecified on an
// uninstantiated generic for the same reason.
func Uninstantiated(t types.Type) bool {
	n, ok := t.(*types.Named)
	if !ok {
		return false
	}
	// An instance keeps its origin's parameters, so the arguments are what
	// separate List[int] from List.
	return n.TypeParams().Len() > 0 && n.TypeArgs().Len() == 0
}

// InstantiationForm is what to type instead of a bare generic name.
//
// An unconstrained parameter becomes int — any concrete type would do, and one
// the reader can substitute beats a placeholder. A constrained one keeps its
// own name: there is no principled argument to choose for a constraint, and a
// guess that happened to satisfy it would teach something false — the same
// reason the implementor search does not try instantiations.
func InstantiationForm(pkg *types.Package, t types.Type) string {
	n, ok := t.(*types.Named)
	if !ok {
		return types.TypeString(t, qualifier(pkg))
	}
	q := qualifier(pkg)
	tp := n.TypeParams()
	args := make([]string, 0, tp.Len())
	for i := range tp.Len() {
		p := tp.At(i)
		if constraintString(p.Constraint(), q) == "any" {
			args = append(args, "int")
			continue
		}
		args = append(args, p.Obj().Name())
	}
	return namedName(q, n) + "[" + strings.Join(args, ", ") + "]"
}

// namedName is a named type's own name as the session would write it, without
// the type parameter list types.TypeString puts on an origin: List, not
// List[T any]. It is the name the reader types, which is what both a
// suggestion and a skipped-type list want.
func namedName(q types.Qualifier, n *types.Named) string {
	name := n.Obj().Name()
	if p := q(n.Obj().Pkg()); p != "" {
		return p + "." + name
	}
	return name
}

// TypeParam is one type parameter and what constrains it.
type TypeParam struct {
	Name       string
	Constraint string
	// Arg is what an instantiation bound the parameter to, empty otherwise.
	Arg string
}

// Generics is what :gen reports.
type Generics struct {
	Type   string
	What   string // "type" or "function", for the sentence that names it
	Params []TypeParam
	// Args is set when the user named an instantiation rather than the generic
	// itself — List[int] rather than List. Calling that "not generic" would be
	// false, so it gets its own answer.
	Args []string
	// Of is the generic List[int] came from.
	Of string
}

// TypeParams reads a type's or function's type parameters and their
// constraints. src is what the user wrote, used as the heading for a function,
// whose type has no name of its own to print.
func TypeParams(pkg *types.Package, t types.Type, src string) *Generics {
	q := qualifier(pkg)
	g := &Generics{Type: types.TypeString(t, q), What: "type"}

	var tp *types.TypeParamList
	switch n := t.(type) {
	case *types.Named:
		tp = n.TypeParams()
		if args := n.TypeArgs(); args != nil && args.Len() > 0 {
			for i := range args.Len() {
				g.Args = append(g.Args, types.TypeString(args.At(i), q))
			}
			g.Of = n.Obj().Name()
			tp = n.Origin().TypeParams()
		}
	case *types.Signature:
		g.What, g.Type = "function", src
		tp = n.TypeParams()
	}
	if tp == nil {
		return g
	}
	for i := range tp.Len() {
		p := tp.At(i)
		param := TypeParam{
			Name:       p.Obj().Name(),
			Constraint: constraintString(p.Constraint(), q),
		}
		if i < len(g.Args) {
			// An instantiation binds each parameter to an argument; pairing
			// them is the whole answer for List[int].
			param.Arg = g.Args[i]
		}
		g.Params = append(g.Params, param)
	}
	return g
}

// constraintString renders a constraint the way it was written where it can:
// `any` rather than `interface{}`, and the interface's own name rather than
// its expansion.
func constraintString(c types.Type, q types.Qualifier) string {
	if c == nil {
		return "any"
	}
	if named, ok := c.(*types.Named); ok {
		return types.TypeString(named, q)
	}
	if in, ok := c.Underlying().(*types.Interface); ok && in.Empty() {
		return "any"
	}
	return types.TypeString(c, q)
}

// The renderers below come in the Render/Plain pairs RenderMethods and
// PlainMethods established: the rich form draws tables and colour for a
// terminal, the plain one is what a pipe reads. Neither goes near pretty.Plain
// or a -json envelope — those are frozen compatibility surfaces (invariant 21),
// and an inspector's output is not one of them.

// RenderSatisfaction draws :impl's and :sat's verdict.
func RenderSatisfaction(s Satisfaction, st pretty.Styles) string {
	var b strings.Builder
	b.WriteString(st.Type.Render(s.subject()))
	if s.OK {
		b.WriteString(st.Annot.Render("  implements  ") + st.Str.Render(s.Iface))
	} else {
		b.WriteString(st.Annot.Render("  does not implement  ") + st.Str.Render(s.Iface))
	}

	if rows := s.rows(); len(rows) > 0 {
		b.WriteString("\n" + methodTable(rows, st))
	}
	if s.PtrOK {
		b.WriteString("\n" + st.Annot.Render("  *"+s.Type+" implements "+s.Iface+
			" — the method is declared on the pointer receiver"))
	}
	if s.Note != "" {
		b.WriteString("\n" + st.Annot.Render("  "+s.Note))
	}
	return b.String()
}

// PlainSatisfaction is the pipe-friendly form.
func PlainSatisfaction(s Satisfaction) string {
	var b strings.Builder
	if s.OK {
		b.WriteString(s.subject() + " implements " + s.Iface)
	} else {
		b.WriteString(s.subject() + " does not implement " + s.Iface)
	}
	for _, r := range s.rows() {
		b.WriteString("\n  " + strings.TrimSpace(strings.Join(r, " ")))
	}
	if s.PtrOK {
		b.WriteString("\n  *" + s.Type + " implements " + s.Iface + " — pointer receiver")
	}
	if s.Note != "" {
		b.WriteString("\n  " + s.Note)
	}
	return b.String()
}

// subject is what the verdict is about: the type, or the expression with its
// type when :sat asked about a value.
func (s Satisfaction) subject() string {
	if s.Expr != "" && s.Expr != s.Type {
		return s.Expr + " (" + s.Type + ")"
	}
	return s.Type
}

func (s Satisfaction) rows() [][]string {
	switch {
	case s.OK || s.Method == "":
		return nil
	case s.Wrong:
		return [][]string{
			{"required", s.Method + s.Want},
			{"has", s.Method + s.Have},
		}
	case s.PtrOK:
		return [][]string{{"pointer only", s.Method + s.Want}}
	}
	return [][]string{{"missing", s.Method + s.Want}}
}

// RenderCast draws :cast's three outcomes.
func RenderCast(c Cast, st pretty.Styles) string {
	var b strings.Builder
	b.WriteString(st.Type.Render(c.Expr+".("+c.Target+")") + "  ")
	switch c.Verdict {
	case CastNotInterface:
		b.WriteString(st.Annot.Render("a type assertion applies only to an interface value; ") +
			st.Str.Render(c.Expr) + st.Annot.Render(" is "+c.Static))
		return b.String()
	case CastRuntime:
		b.WriteString(st.Annot.Render("compiles"))
		b.WriteString("\n" + st.Annot.Render("  whether it succeeds depends on the dynamic type at run time"))
		return b.String()
	}
	b.WriteString(st.Annot.Render("cannot compile"))
	b.WriteString("\n" + st.Annot.Render("  no "+c.Static+" can hold a value of type "+c.Target))
	b.WriteString("\n" + methodTable(c.rows(), st))
	return b.String()
}

// PlainCast is the pipe-friendly form.
func PlainCast(c Cast) string {
	head := c.Expr + ".(" + c.Target + ")"
	switch c.Verdict {
	case CastNotInterface:
		return head + " does not apply — a type assertion applies only to an interface value; " +
			c.Expr + " is " + c.Static
	case CastRuntime:
		return head + " compiles\n  whether it succeeds depends on the dynamic type at run time"
	}
	var b strings.Builder
	b.WriteString(head + " cannot compile\n  no " + c.Static + " can hold a value of type " + c.Target)
	for _, r := range c.rows() {
		b.WriteString("\n  " + strings.TrimSpace(strings.Join(r, " ")))
	}
	return b.String()
}

func (c Cast) rows() [][]string {
	if c.Method == "" {
		return nil
	}
	if c.Wrong {
		return [][]string{
			{c.Static + " requires", c.Method + c.Want},
			{c.Target + " has", c.Method + c.Have},
		}
	}
	return [][]string{{"missing", c.Method + c.Want}}
}

// RenderIface draws an interface's definition and its implementors.
func RenderIface(f *Iface, st pretty.Styles) string {
	var b strings.Builder
	b.WriteString(st.Type.Render(f.Name))
	b.WriteString(st.Annot.Render("  " + countOf(len(f.Methods), "method")))

	if len(f.Methods) > 0 {
		rows := make([][]string, 0, len(f.Methods))
		for _, m := range f.Methods {
			rows = append(rows, []string{m.Name, m.Sig})
		}
		b.WriteString("\n" + methodTable(rows, st))
	}
	if len(f.Embeds) > 0 {
		b.WriteString("\n" + st.Annot.Render("  embeds  ") + st.Str.Render(strings.Join(f.Embeds, ", ")))
	}
	if len(f.Value) > 0 {
		b.WriteString("\n" + st.Annot.Render("  implemented by  ") + st.Str.Render(strings.Join(f.Value, ", ")))
	}
	if len(f.Pointer) > 0 {
		b.WriteString("\n" + st.Annot.Render("  by pointer  ") +
			st.Str.Render("*"+strings.Join(f.Pointer, ", *")))
	}
	if len(f.Value) == 0 && len(f.Pointer) == 0 {
		b.WriteString("\n" + st.Annot.Render(fmt.Sprintf(
			"  no type in scope implements it — %d named types searched", f.Scanned)))
	}
	if f.Extra > 0 {
		b.WriteString("\n" + st.Annot.Render(fmt.Sprintf("  and %d more", f.Extra)))
	}
	if len(f.SkippedGeneric) > 0 {
		b.WriteString("\n" + st.Annot.Render("  "+countOf(len(f.SkippedGeneric), "generic type")+
			" not considered  ") + st.Str.Render(strings.Join(f.SkippedGeneric, ", ")) +
			st.Annot.Render("  "+skippedGenericWhy))
	}
	return b.String()
}

// skippedGenericWhy is the half of the skipped line that matters: without it
// the names read as an omission rather than as a question that was not asked.
const skippedGenericWhy = "— a generic type implements nothing until it is instantiated"

// PlainIface is the pipe-friendly form.
func PlainIface(f *Iface) string {
	var b strings.Builder
	b.WriteString(f.Name + " " + countOf(len(f.Methods), "method"))
	for _, m := range f.Methods {
		b.WriteString("\n  " + m.Name + m.Sig)
	}
	if len(f.Embeds) > 0 {
		b.WriteString("\n  embeds " + strings.Join(f.Embeds, ", "))
	}
	if len(f.Value) > 0 {
		b.WriteString("\n  implemented by " + strings.Join(f.Value, ", "))
	}
	if len(f.Pointer) > 0 {
		b.WriteString("\n  by pointer *" + strings.Join(f.Pointer, ", *"))
	}
	if len(f.Value) == 0 && len(f.Pointer) == 0 {
		b.WriteString(fmt.Sprintf("\n  no type in scope implements it — %d named types searched", f.Scanned))
	}
	if f.Extra > 0 {
		b.WriteString(fmt.Sprintf("\n  and %d more", f.Extra))
	}
	if len(f.SkippedGeneric) > 0 {
		b.WriteString("\n  " + countOf(len(f.SkippedGeneric), "generic type") + " not considered: " +
			strings.Join(f.SkippedGeneric, ", ") + " " + skippedGenericWhy)
	}
	return b.String()
}

// RenderEmbeds draws embedded fields and what they promote.
func RenderEmbeds(e *Embedding, st pretty.Styles) string {
	var b strings.Builder
	b.WriteString(st.Type.Render(e.Type))
	if len(e.Fields) == 0 {
		b.WriteString(st.Annot.Render("  embeds nothing"))
		return b.String()
	}
	names := make([]string, 0, len(e.Fields))
	for _, f := range e.Fields {
		n := f.Name
		if f.Pointer {
			n = "*" + n
		}
		names = append(names, n)
	}
	b.WriteString(st.Annot.Render("  embeds  ") + st.Str.Render(strings.Join(names, ", ")))

	if len(e.Promoted) > 0 {
		rows := make([][]string, 0, len(e.Promoted))
		for _, p := range e.Promoted {
			from := p.From
			if p.PointerOnly {
				from = "*" + from
			}
			rows = append(rows, []string{p.Name, p.Sig, "from " + from + depthNote(p.Depth)})
		}
		b.WriteString("\n" + methodTable(rows, st))
	} else {
		b.WriteString("\n" + st.Annot.Render("  nothing is promoted"))
	}
	for _, a := range e.Ambiguous {
		b.WriteString("\n" + st.Annot.Render("  ambiguous  ") + st.Str.Render(a.Name) +
			st.Annot.Render(" is promoted from both "+strings.Join(a.From, " and ")+
				" — the selector does not compile"))
	}
	return b.String()
}

// PlainEmbeds is the pipe-friendly form.
func PlainEmbeds(e *Embedding) string {
	var b strings.Builder
	b.WriteString(e.Type)
	if len(e.Fields) == 0 {
		b.WriteString(" embeds nothing")
		return b.String()
	}
	names := make([]string, 0, len(e.Fields))
	for _, f := range e.Fields {
		n := f.Name
		if f.Pointer {
			n = "*" + n
		}
		names = append(names, n)
	}
	b.WriteString(" embeds " + strings.Join(names, ", "))
	for _, p := range e.Promoted {
		from := p.From
		if p.PointerOnly {
			from = "*" + from
		}
		b.WriteString("\n  " + p.Name + p.Sig + " from " + from + depthNote(p.Depth))
	}
	if len(e.Promoted) == 0 {
		b.WriteString("\n  nothing is promoted")
	}
	for _, a := range e.Ambiguous {
		b.WriteString("\n  ambiguous " + a.Name + " is promoted from both " +
			strings.Join(a.From, " and ") + " — the selector does not compile")
	}
	return b.String()
}

func depthNote(d int) string {
	if d <= 1 {
		return ""
	}
	return fmt.Sprintf(" (depth %d)", d)
}

// RenderGenerics draws a type's parameters and their constraints.
func RenderGenerics(g *Generics, st pretty.Styles) string {
	var b strings.Builder
	b.WriteString(st.Type.Render(g.Type))
	if len(g.Args) > 0 {
		b.WriteString(st.Annot.Render("  "+g.Of+" instantiated with  ") +
			st.Str.Render(strings.Join(g.Args, ", ")))
	}
	if len(g.Params) == 0 {
		if len(g.Args) == 0 {
			b.WriteString(st.Annot.Render("  is not generic"))
		}
		return b.String()
	}
	if len(g.Args) == 0 {
		b.WriteString(st.Annot.Render("  " + countOf(len(g.Params), "type parameter")))
	}
	rows := make([][]string, 0, len(g.Params))
	for _, p := range g.Params {
		row := []string{p.Name, p.Constraint}
		if p.Arg != "" {
			row = append(row, "= "+p.Arg)
		}
		rows = append(rows, row)
	}
	b.WriteString("\n" + methodTable(rows, st))
	return b.String()
}

// PlainGenerics is the pipe-friendly form.
func PlainGenerics(g *Generics) string {
	var b strings.Builder
	b.WriteString(g.Type)
	if len(g.Args) > 0 {
		b.WriteString(" is " + g.Of + " instantiated with " + strings.Join(g.Args, ", "))
	}
	if len(g.Params) == 0 {
		if len(g.Args) == 0 {
			b.WriteString(" is not generic")
		}
		return b.String()
	}
	if len(g.Args) == 0 {
		b.WriteString(" " + countOf(len(g.Params), "type parameter"))
	}
	for _, p := range g.Params {
		b.WriteString("\n  " + p.Name + " " + p.Constraint)
		if p.Arg != "" {
			b.WriteString(" = " + p.Arg)
		}
	}
	return b.String()
}

func countOf(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

// methodTable is the bordered two-or-three column table these commands share,
// drawn the way RenderMethods draws a method set so the inspecting commands
// keep one look.
func methodTable(rows [][]string, st pretty.Styles) string {
	if len(rows) == 0 {
		return ""
	}
	return table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(st.Border).
		StyleFunc(func(_, col int) lipgloss.Style {
			if col == 0 {
				return st.Type.Padding(0, 1)
			}
			return st.Annot.Padding(0, 1)
		}).
		Rows(rows...).
		String()
}
