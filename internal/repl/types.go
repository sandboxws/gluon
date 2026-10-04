package repl

import (
	"errors"
	"go/types"
	"strings"

	"github.com/sandboxws/gluon/internal/check"
	"github.com/sandboxws/gluon/internal/inspect"
)

// The interface questions: does this implement that, is this assertion legal,
// what does this interface require and who satisfies it, what does embedding
// promote, what does this generic declare.
//
// All six answer from go/types in gluon's own process. Nothing here builds and
// nothing runs, which is what lets them be MCP tools with no --eval gate — and
// what obliges every one of them to say "unavailable" rather than guess when
// the checker cannot answer (invariant 5).

// probe type-checks the session with the command's arguments appended as a
// single expression, and hands back what each one resolved to.
//
// One check for all the arguments, not one per argument. A named type's
// identity in go/types belongs to the check that created it, so a type from
// one run and an interface from another are different types even when they are
// the same declaration — and the report would then name a method as
// mismatched while printing the same signature as both required and actual.
// inspect.ProbeExpr is the expression that carries them together.
func (c *Core) probe(arg string, want int, usage string) (*check.Result, []inspect.Operand, *Result) {
	var args []string
	if want == 1 {
		// A single argument is never split: `:embeds Map[string, int]` is one
		// name that happens to contain a comma.
		if s := strings.TrimSpace(arg); s != "" {
			args = []string{s}
		}
	} else {
		args = splitTop(arg)
	}
	if len(args) != want {
		return nil, nil, &Result{Out: usage, Err: true}
	}

	res, err := c.ev.Analyze(c.sess, inspect.ProbeExpr(args))
	if err != nil {
		return nil, nil, ptr(unavailable(err))
	}
	ops, err := inspect.Operands(res, args)
	if err != nil {
		return nil, nil, ptr(unavailable(err))
	}
	return res, ops, nil
}

func ptr(r Result) *Result { return &r }

// unavailable is what every one of these commands returns when the checker
// could not run.
//
// It is deliberately not phrased as a verdict. "T does not implement I" and
// "I could not tell" are different facts, and invariant 5 says the checker is
// never an authority — so its failure has to read as a failure. This is the
// one word that separates the two.
func unavailable(err error) Result {
	return Result{Out: "unavailable: the type checker could not answer — " + err.Error(), Err: true}
}

// unresolved is the answer for a name the session cannot see. Also not a
// verdict: a type that does not exist does not fail to implement anything.
func unresolved(op inspect.Operand) Result {
	return Result{Out: "error: " + op.Src + " does not name anything this session can see", Err: true}
}

// needsInstantiation is the refusal for a question asked of a generic type by
// its bare name. Also not a verdict, and for the same reason the other two are
// not: the answer belongs to List[int], and computing it from List would print
// a confident no about a type that does not exist (invariant 5).
func needsInstantiation(name, why, retry string) Result {
	return Result{Out: "error: " + name + " is generic — " + why + "; try " + retry, Err: true}
}

// whyInstantiation is the clause :impl and :sat share. Both ask one question,
// of a type and of a value, and neither can answer it from a declaration.
const whyInstantiation = "the answer belongs to an instantiation, not to the declaration"

// notInterface reports the second argument of :impl or :sat naming something
// that is not an interface, which is a different mistake from a name that does
// not resolve.
func notInterface(op inspect.Operand, err error) Result {
	if errors.Is(err, inspect.ErrUnresolved) {
		return unresolved(op)
	}
	return Result{Out: "error: " + op.Src + " is not an interface — it is " +
		inspect.Kind(op.Type), Err: true}
}

// splitTop cuts an argument list at the commas that separate its arguments,
// ignoring those nested inside brackets, parentheses or braces, and those
// inside a string or rune literal.
//
// :slice's splitExprs splits on every comma, which is right for the expressions
// it takes. A type argument is not so simple: `:impl Map[string, int], I` has
// three commas' worth of ambiguity and only the last one separates arguments.
func splitTop(arg string) []string {
	var (
		out   []string
		cur   strings.Builder
		depth int
		quote rune // the literal being scanned, 0 outside one
		esc   bool
	)
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	for _, r := range arg {
		switch {
		case esc:
			esc = false
		case quote != 0:
			switch r {
			case '\\':
				if quote != '`' {
					esc = true
				}
			case quote:
				quote = 0
			}
		case r == '"' || r == '\'' || r == '`':
			quote = r
		case r == '(' || r == '[' || r == '{':
			depth++
		case r == ')' || r == ']' || r == '}':
			depth--
		case r == ',' && depth <= 0:
			flush()
			continue
		}
		cur.WriteRune(r)
	}
	flush()
	return out
}

// implements is :impl — whether a type satisfies an interface the user names,
// and which method is in the way when it does not.
//
// :m already answers this against a fixed list. The question a Go developer
// actually has is about their own interface, and the useful half of a "no" is
// the missing method and whether a pointer receiver would have fixed it —
// which is the half the compiler's own error leaves out.
func (c *Core) implements(arg string) Result {
	const usage = "usage: :impl <type>, <interface>   e.g. :impl bytes.Buffer, io.Writer"
	res, ops, bad := c.probe(arg, 2, usage)
	if bad != nil {
		return *bad
	}
	t, i := ops[0], ops[1]
	if t.Type == nil {
		return unresolved(t)
	}
	if !t.IsType {
		return Result{Out: "error: " + t.Src + " is a value, not a type — :sat " + t.Src +
			", " + i.Src + " asks the same question of a value", Err: true}
	}
	if inspect.Uninstantiated(t.Type) {
		return needsInstantiation(t.Src, whyInstantiation,
			":impl "+inspect.InstantiationForm(res.Pkg, t.Type)+", "+i.Src)
	}
	iface, err := i.Iface()
	if err != nil {
		return notInterface(i, err)
	}
	// A generic interface is checked after Iface, so a generic struct still
	// gets the message about not being an interface — the nearer mistake.
	if inspect.Uninstantiated(i.Type) {
		return needsInstantiation(i.Src, whyInstantiation,
			":impl "+t.Src+", "+inspect.InstantiationForm(res.Pkg, i.Type))
	}
	return c.satisfaction(inspect.Satisfies(res.Pkg, t.Type, iface, i.Name()))
}

// satisfies is :sat — the same question asked of a value.
//
// The verdict is about the static type, and says so when that type is itself
// an interface. Reporting the dynamic type would need a run, which is the same
// limitation :t's hint documents.
func (c *Core) satisfies(arg string) Result {
	const usage = "usage: :sat <expression>, <interface>   e.g. :sat buf, io.Writer"
	res, ops, bad := c.probe(arg, 2, usage)
	if bad != nil {
		return *bad
	}
	e, i := ops[0], ops[1]
	if e.Type == nil {
		return unresolved(e)
	}
	if e.Values == 0 {
		return Result{Out: "error: " + e.Src + " returns nothing to test", Err: true}
	}
	if inspect.Uninstantiated(e.Type) {
		return needsInstantiation(e.Src, whyInstantiation,
			":sat "+inspect.InstantiationForm(res.Pkg, e.Type)+", "+i.Src)
	}
	iface, err := i.Iface()
	if err != nil {
		return notInterface(i, err)
	}
	if inspect.Uninstantiated(i.Type) {
		return needsInstantiation(i.Src, whyInstantiation,
			":sat "+e.Src+", "+inspect.InstantiationForm(res.Pkg, i.Type))
	}
	s := inspect.Satisfies(res.Pkg, e.Type, iface, i.Name())
	s.Expr = e.Src
	if _, isIface := e.Type.Underlying().(*types.Interface); isIface && !e.IsType {
		s.Note = "the static type only — the dynamic type is not known without running the expression"
	}
	return c.satisfaction(s)
}

func (c *Core) satisfaction(s inspect.Satisfaction) Result {
	if c.Rich {
		return Result{Out: inspect.RenderSatisfaction(s, c.Styles)}
	}
	return Result{Out: inspect.PlainSatisfaction(s)}
}

// cast is :cast — whether `e.(t)` is legal.
//
// Three outcomes, and the one worth the command is the first: an assertion the
// compiler rejects outright reads in Go as an error about a method the user
// never mentioned, and this names it.
func (c *Core) cast(arg string) Result {
	const usage = "usage: :cast <expression>, <type>   e.g. :cast r, *os.File"
	res, ops, bad := c.probe(arg, 2, usage)
	if bad != nil {
		return *bad
	}
	e, t := ops[0], ops[1]
	if e.Type == nil {
		return unresolved(e)
	}
	if t.Type == nil {
		return unresolved(t)
	}
	if !t.IsType {
		return Result{Out: "error: " + t.Src + " is a value — an assertion names a type", Err: true}
	}
	cst := inspect.Assertion(res.Pkg, e, t)
	if c.Rich {
		return Result{Out: inspect.RenderCast(cst, c.Styles)}
	}
	return Result{Out: inspect.PlainCast(cst)}
}

// iface is :iface — an interface's method set, what it embeds, and every type
// in scope that satisfies it.
func (c *Core) iface(arg string) Result {
	const usage = "usage: :iface <interface>   e.g. :iface io.Reader"
	res, ops, bad := c.probe(arg, 1, usage)
	if bad != nil {
		return *bad
	}
	op := ops[0]
	in, err := op.Iface()
	if err != nil {
		return notInterface(op, err)
	}
	f := inspect.DescribeInterface(res, op, in, c.loadedHostPkgs())
	if c.Rich {
		return Result{Out: inspect.RenderIface(f, c.Styles)}
	}
	return Result{Out: inspect.PlainIface(f)}
}

// loadedHostPkgs widens the implementor search over the attached module,
// without paying for it.
//
// The index built on attach names every package the host offers, but their
// types are not loaded until something imports them, and loading one runs
// `go list -deps -export`, which compiles. A static command must not turn into
// a build, so only the packages an earlier check already read are searched.
func (c *Core) loadedHostPkgs() []*types.Package {
	ix, err := c.ev.Index()
	if err != nil || ix == nil {
		return nil
	}
	var out []*types.Package
	for _, p := range ix.All() {
		if pkg := c.ev.Loaded(p.Path); pkg != nil {
			out = append(out, pkg)
		}
	}
	return out
}

// embeds is :embeds — the embedded fields and the methods they promote.
func (c *Core) embeds(arg string) Result {
	const usage = "usage: :embeds <type>   e.g. :embeds bufio.ReadWriter"
	res, ops, bad := c.probe(arg, 1, usage)
	if bad != nil {
		return *bad
	}
	op := ops[0]
	if op.Type == nil {
		return unresolved(op)
	}
	e := inspect.Embeds(res.Pkg, op.Type)
	if c.Rich {
		return Result{Out: inspect.RenderEmbeds(e, c.Styles)}
	}
	return Result{Out: inspect.PlainEmbeds(e)}
}

// generics is :gen — a generic type's parameters and their constraints.
func (c *Core) generics(arg string) Result {
	const usage = "usage: :gen <type>   e.g. :gen List"
	res, ops, bad := c.probe(arg, 1, usage)
	if bad != nil {
		return *bad
	}
	op := ops[0]
	if op.Type == nil {
		return unresolved(op)
	}
	g := inspect.TypeParams(res.Pkg, op.Type, op.Src)
	if c.Rich {
		return Result{Out: inspect.RenderGenerics(g, c.Styles)}
	}
	return Result{Out: inspect.PlainGenerics(g)}
}
