package inspect

import (
	"go/types"
	"sort"
	"strings"

	"github.com/sandboxws/gluon/internal/check"
)

// Arguments answers what belongs at an argument position: the callee's
// signature as it should be shown, and the names in scope the parameter there
// will actually take.
//
// r is a session type-checked with the callee appended as its trailing
// expression — what Evaluator.Analyze produces — so the callee resolves through
// Resolve with no new checker plumbing. callee is passed as text as well
// because the one thing the resolved signature cannot say is whether the user
// wrote an explicit instantiation.
//
// Every failure is silence. A disabled checker, a callee that will not resolve,
// a type used as a conversion, a value that is not callable: all of them return
// "", nil, and the caller offers nothing rather than guessing. That is
// invariant 5 in its completion form — the checker is consulted, never obeyed.
func Arguments(r *check.Result, callee string, index int) (string, []string) {
	if r == nil {
		return "", nil
	}
	t, err := Resolve(r)
	if err != nil {
		return "", nil
	}
	// A type before the parenthesis is a conversion, and a conversion takes one
	// value of whatever converts — a question about assignability that has no
	// parameter to ask it of.
	if t.IsType {
		return "", nil
	}
	sig, ok := callSignature(t.Type)
	if !ok {
		return "", nil
	}

	qual := qualifier(r.Pkg)
	text := signatureText(callee, sig, index, qual)

	// A generic callee that was never instantiated has a type parameter where
	// its parameter type should be, and assignability against a type parameter
	// is inference rather than a rule. The signature still reads, which is the
	// half of the answer that does not require guessing.
	if sig.TypeParams().Len() > 0 && !strings.Contains(callee, "[") {
		return text, nil
	}
	param, ok := paramAt(sig, index)
	if !ok {
		return text, nil
	}
	return text, assignable(r, param)
}

// callSignature unwraps what was named into the signature it calls with. A
// named func type counts: `type Handler func(int)` is called exactly as its
// underlying signature is.
func callSignature(t types.Type) (*types.Signature, bool) {
	if t == nil {
		return nil, false
	}
	if sig, ok := t.(*types.Signature); ok {
		return sig, true
	}
	sig, ok := t.Underlying().(*types.Signature)
	return sig, ok
}

// paramAt is the type of the parameter at index, with the variadic rule: every
// position from the variadic parameter onward takes its element type, because
// that is what each of them is written as at the call.
//
// An index past the end of a non-variadic signature has no parameter at all —
// the call already has too many arguments, and nothing in scope would fix it.
func paramAt(sig *types.Signature, index int) (types.Type, bool) {
	params := sig.Params()
	if index < 0 || params.Len() == 0 {
		return nil, false
	}
	if last := params.Len() - 1; sig.Variadic() && index >= last {
		s, ok := params.At(last).Type().(*types.Slice)
		if !ok {
			return nil, false
		}
		return s.Elem(), true
	}
	if index >= params.Len() {
		return nil, false
	}
	return params.At(index).Type(), true
}

// assignable is every name the session has bound whose type the parameter will
// take, by types.AssignableTo — the rule the compiler applies, so a candidate
// accepted here is a line that compiles.
//
// The two scopes are the same split Bindings reads: variables live in main's
// body, and funcs, types and consts are hoisted above it. Only what the session
// itself declared is offered; the injected runtime is a sibling in the same
// package and its names are not the user's to call.
func assignable(r *check.Result, param types.Type) []string {
	var out []string
	keep := func(o types.Object) {
		name := o.Name()
		if name == "" || name == "_" || name == "main" || strings.HasPrefix(name, "__gluon") {
			return
		}
		if !r.DeclaredHere(o) || !types.AssignableTo(o.Type(), param) {
			return
		}
		out = append(out, name)
	}

	if sc := r.MainScope(); sc != nil {
		for _, name := range sc.Names() {
			if v, ok := sc.Lookup(name).(*types.Var); ok {
				keep(v)
			}
		}
	}
	if r.Pkg != nil {
		sc := r.Pkg.Scope()
		for _, name := range sc.Names() {
			switch o := sc.Lookup(name).(type) {
			case *types.Var, *types.Const, *types.Func:
				keep(o)
			}
		}
	}
	sort.Strings(out)
	return out
}

// signatureText is the hint: the callee written with the parameters it takes
// and the one being typed marked, qualified against the session's own package
// so it reads `strings.Replace(...)` rather than carrying import paths.
//
// The active parameter is bracketed rather than coloured. A hint is drawn in
// one dim role — colouring inside it would make part of it look like a
// different kind of thing — and brackets are what :help already uses to mark
// the argument a reader should be looking at.
func signatureText(callee string, sig *types.Signature, index int, qual types.Qualifier) string {
	var b strings.Builder
	b.WriteString(callee)

	if tp := sig.TypeParams(); tp.Len() > 0 {
		b.WriteString("[")
		for i := range tp.Len() {
			if i > 0 {
				b.WriteString(", ")
			}
			p := tp.At(i)
			b.WriteString(p.Obj().Name() + " " + types.TypeString(p.Constraint(), qual))
		}
		b.WriteString("]")
	}

	b.WriteString("(")
	params := sig.Params()
	active := activeParam(sig, index)
	for i := range params.Len() {
		if i > 0 {
			b.WriteString(", ")
		}
		p := params.At(i)
		one := types.TypeString(p.Type(), qual)
		if sig.Variadic() && i == params.Len()-1 {
			// The declared type is a slice; the call site writes an ellipsis.
			one = "..." + strings.TrimPrefix(one, "[]")
		}
		if n := p.Name(); n != "" {
			one = n + " " + one
		}
		if i == active {
			one = "[" + one + "]"
		}
		b.WriteString(one)
	}
	b.WriteString(")")

	switch res := sig.Results(); {
	case res.Len() == 0:
	case res.Len() == 1 && res.At(0).Name() == "":
		b.WriteString(" " + types.TypeString(res.At(0).Type(), qual))
	default:
		b.WriteString(" " + types.TypeString(res, qual))
	}
	return b.String()
}

// activeParam is which parameter the index is typing, or -1 when it is typing
// past the last one. The variadic parameter stays active however many
// arguments follow it, because each of them is still that parameter.
func activeParam(sig *types.Signature, index int) int {
	last := sig.Params().Len() - 1
	if last < 0 || index < 0 {
		return -1
	}
	if sig.Variadic() && index >= last {
		return last
	}
	if index > last {
		return -1
	}
	return index
}
