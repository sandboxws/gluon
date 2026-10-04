package repl

import (
	"errors"
	"fmt"
	"go/types"
	"strings"

	"github.com/sandboxws/gluon/internal/eval"
	"github.com/sandboxws/gluon/internal/inspect"
	"github.com/sandboxws/gluon/internal/session"
	"github.com/sandboxws/gluon/internal/syntax"
)

// inline is :inline — what the compiler decided about inlining.
//
// It builds and never runs, exactly as :esc does, and for the same reason: an
// inlining decision is a compile-time fact and the exec buys nothing. The two
// are the same build with a different -gcflags value and a different half of
// its output kept.
func (c *Core) inline(arg string) Result {
	if arg == "" {
		return Result{Out: "usage: :inline <expression or function>   e.g. :inline add(1, 2)", Err: true}
	}

	// A name the session declared is answered from its own decision line,
	// which the compiler reports against the entry that declared it rather
	// than against the line asking about it. Anything else — an expression, a
	// name from another package, a name that does not resolve — takes the
	// expression path, where the compiler's own error is the answer. The
	// checker is never an authority here (invariant 5).
	if fn, symbol, ok := c.declaredFunc(arg); ok {
		return c.inlineFunc(fn, symbol)
	}

	target, err := c.resolve(arg)
	if err != nil && !checkerUnavailable(err) {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	entry := session.Entry{Kind: session.KindExpr, Src: arg}
	if target != nil {
		entry.NoValue = target.Void
	}

	res, err := c.ev.InlineAnalysis(c.sess, entry)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	// Only decisions on this line are "here". A decision about a function
	// declared three entries ago is answered by naming that function, not by
	// asking about an unrelated line, so the rest are dropped rather than
	// reported as elsewhere.
	const nothing = "nothing on this line to inline — no calls the compiler could have flattened"
	if c.Rich {
		return Result{Out: inspect.RenderInline(res.Here, nothing, c.Styles, true)}
	}
	return Result{Out: inspect.PlainInline(res.Here, nothing)}
}

// inlineFunc answers for one declared function, from the decision the compiler
// records against its declaration.
func (c *Core) inlineFunc(fn *types.Func, symbol string) Result {
	// No entry is appended: the function is already one of the session's
	// declarations, so there is nothing to add to the program.
	res, err := c.ev.InlineAnalysis(c.sess, session.Entry{})
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}

	// The compiler names a function the way it spells the symbol, without the
	// package: "can inline add with cost 4", "cannot inline (*P).Inc: ...".
	name := strings.TrimPrefix(symbol, "main.")
	var mine []eval.Diag
	for _, m := range append(append([]eval.Diag{}, res.Elsewhere...), res.Here...) {
		if isDecisionAbout(m.Text, name) {
			mine = append(mine, m)
		}
	}
	nothing := "the compiler reported no inlining decision for " + name +
		" — it is neither a candidate nor a call site on any line in this session"
	if c.Rich {
		return Result{Out: inspect.RenderInline(mine, nothing, c.Styles, true)}
	}
	return Result{Out: inspect.PlainInline(mine, nothing)}
}

// isDecisionAbout reports whether a decision line is the verdict on this
// function rather than on one whose name merely starts the same way.
//
// The compiler writes "can inline add with cost 4 as: ..." and "cannot inline
// heavy: function too complex", so the name is followed by a space in one form
// and a colon in the other.
func isDecisionAbout(text, name string) bool {
	for _, verb := range []string{"can inline ", "cannot inline "} {
		rest, ok := strings.CutPrefix(text, verb)
		if !ok {
			continue
		}
		rest, ok = strings.CutPrefix(rest, name)
		if ok && (rest == "" || rest[0] == ' ' || rest[0] == ':') {
			return true
		}
	}
	return false
}

// asm is :asm — the assembly the compiler emitted for one declared function.
//
// It is the terminal-sized half of the question GOSSAFUNC answers with several
// megabytes of HTML. The listing is sliced to one function because the whole
// program's is thousands of lines of gluon's harness and the standard library
// the session happens to have pulled in.
func (c *Core) asm(arg string) Result {
	if arg == "" {
		return Result{Out: "usage: :asm <function or method>   e.g. :asm add, :asm (*P).Inc", Err: true}
	}
	_, symbol, ok := c.declaredFunc(arg)
	if !ok {
		return Result{Out: "error: " + c.whyNotAsm(arg), Err: true}
	}

	res, err := c.ev.Assembly(c.sess, session.Entry{}, symbol)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}

	text := inspect.PlainAsm(res)
	if !res.Emitted {
		return Result{Out: text}
	}
	// Out carries the linear listing whatever the driver can do, and the modal
	// is advisory (invariant 19). Both are the same bytes; only the pane knows
	// it is looking at assembly.
	return Result{
		Out:   text,
		Lang:  syntax.None,
		Modal: pageableIn("asm "+res.Symbol, text, syntax.None),
	}
}

// whyNotAsm says why a name has no listing, which is a different answer for
// each way it can fail.
func (c *Core) whyNotAsm(arg string) string {
	res, err := c.ev.Analyze(c.sess, "")
	if err != nil {
		return err.Error()
	}
	name := arg
	if i := strings.LastIndexByte(arg, '.'); i >= 0 {
		name = arg[:i]
	}
	name = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(name, "("), "*"), ")")

	if obj := res.Pkg.Scope().Lookup(name); obj != nil && res.DeclaredHere(obj) {
		switch o := obj.(type) {
		case *types.TypeName:
			if arg == name {
				return arg + " is a type, not a function — :m " + arg + " lists its methods"
			}
			return arg + " is not a method of " + name + " — :m " + name + " lists the ones it has"
		case *types.Const:
			return arg + " is a constant, not a function"
		case *types.Var:
			return arg + " is a variable, not a function"
		default:
			_ = o
		}
	}
	if sc := res.MainScope(); sc != nil {
		if _, obj := sc.LookupParent(name, 0); obj != nil {
			return arg + " is a variable, not a function"
		}
	}
	if strings.Contains(arg, ".") {
		// The refusal and the reason are the same fact: -gcflags carries no
		// package pattern, deliberately, so the listing flag reaches only the
		// package on the command line. An `all=` pattern would answer this and
		// recompile the standard library under different flags, destroying the
		// warm cache every line in the REPL depends on.
		return arg + " is not a function this session declared — the listing flag applies " +
			"only to the session's own package, because widening it would recompile the " +
			"standard library under different flags and throw away the warm build cache"
	}
	return "unknown: " + arg
}

// vet is :vet — the toolchain's own analysers over the session.
//
// It builds nothing and runs nothing: vet type-checks the package itself,
// which is why it costs about half a second warm rather than a build.
func (c *Core) vet(arg string) Result {
	var entry session.Entry
	if arg != "" {
		// The checker runs first so an expression that does not type-check
		// reports the compile error the way an ordinary line would, rather
		// than as a vet failure — vet prints type errors in the same shape and
		// they would read as findings.
		target, err := c.resolve(arg)
		if err != nil && !checkerUnavailable(err) {
			return Result{Out: "error: " + err.Error(), Err: true}
		}
		entry = session.Entry{Kind: session.KindExpr, Src: arg}
		if target != nil {
			entry.NoValue = target.Void
		}
	}

	res, err := c.ev.Vet(c.sess, entry)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	if c.Rich {
		return Result{Out: inspect.RenderVet(res, c.Styles, true)}
	}
	return Result{Out: inspect.PlainVet(res)}
}

// race is :race — one evaluation under the race detector.
//
// It is the only command here that runs, because a data race is a runtime
// fact: no build can be asked about it. The binary it runs is a second one
// beside prog, and nothing on the path reads or writes the result cache — the
// cache is keyed on program text, which cannot tell an instrumented build from
// an ordinary one.
func (c *Core) race(arg string) Result {
	if arg == "" {
		return Result{Out: "usage: :race <expression>   e.g. :race spawn(2)", Err: true}
	}
	target, err := c.resolve(arg)
	if err != nil && !checkerUnavailable(err) {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	entry := session.Entry{Kind: session.KindExpr, Src: arg}
	if target != nil {
		entry.NoValue = target.Void
	}

	res, err := c.ev.RaceRun(c.sess, entry)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}

	// The expression's own result is shown the way an ordinary evaluation
	// shows it: the run happened, and its value is still the answer.
	value := c.format(res.Output)
	if c.Rich {
		return Result{Out: inspect.RenderRace(res, value, c.Styles, true), Err: res.Raced}
	}
	return Result{Out: inspect.PlainRace(res, value), Err: res.Raced}
}

// declaredFunc resolves a name the user typed into the function it names and
// the linker symbol the compiler emits for it, for the session's own
// declarations only.
//
// The symbol is derived from the resolved *types.Func rather than from the
// typed text, so :asm P.Inc and :asm (*P).Inc both find the same block — the
// receiver's pointer-ness is the compiler's fact, not the user's spelling.
func (c *Core) declaredFunc(arg string) (*types.Func, string, bool) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return nil, "", false
	}
	res, err := c.ev.Analyze(c.sess, "")
	if err != nil || res.Pkg == nil {
		// Without the checker there is no symbol to derive, and the caller
		// falls back to the path that asks the compiler instead (invariant 5).
		return nil, "", false
	}

	recv, name := splitMethod(arg)
	if recv == "" {
		obj := res.Pkg.Scope().Lookup(name)
		fn, ok := obj.(*types.Func)
		if !ok || !res.DeclaredHere(obj) {
			return nil, "", false
		}
		return fn, "main." + name, true
	}

	obj := res.Pkg.Scope().Lookup(recv)
	tn, ok := obj.(*types.TypeName)
	if !ok || !res.DeclaredHere(obj) {
		return nil, "", false
	}
	// Addressable, so a pointer-receiver method is found on the value type
	// too: the spelling the user chose must not decide whether the method
	// exists, only the method set does.
	m, _, _ := types.LookupFieldOrMethod(tn.Type(), true, res.Pkg, name)
	fn, ok := m.(*types.Func)
	if !ok {
		return nil, "", false
	}
	return fn, "main." + methodSymbol(recv, fn), true
}

// splitMethod reads T.M and (*T).M into their receiver and method halves, and
// reports an empty receiver for a plain name.
func splitMethod(arg string) (recv, name string) {
	i := strings.LastIndexByte(arg, '.')
	if i < 0 {
		return "", arg
	}
	recv, name = strings.TrimSpace(arg[:i]), strings.TrimSpace(arg[i+1:])
	if inner, ok := strings.CutPrefix(recv, "("); ok {
		recv = strings.TrimSuffix(inner, ")")
	}
	recv = strings.TrimPrefix(recv, "*")
	return recv, name
}

// methodSymbol spells a method the way the linker does: main.T.M for a value
// receiver and main.(*T).M for a pointer one.
func methodSymbol(recv string, fn *types.Func) string {
	sig, ok := fn.Type().(*types.Signature)
	if ok && sig.Recv() != nil {
		if _, ptr := sig.Recv().Type().(*types.Pointer); ptr {
			return fmt.Sprintf("(*%s).%s", recv, fn.Name())
		}
	}
	return recv + "." + fn.Name()
}

// checkerUnavailable reports that the type checker could not run at all, as
// opposed to running and finding a problem.
//
// Invariant 5: the checker is never an authority. A command that refused to
// build because the checker was switched off would make it one, so its absence
// falls through to the compiler, which answers the same question slower and
// for certain.
func checkerUnavailable(err error) bool { return errors.Is(err, eval.ErrNoChecker) }
