package repl

// :test, :mock and :spy — writing down what the session already established.
//
// A REPL session is a test that has not been written down. You try an
// expression, you look at the answer, and you decide the answer is right: that
// is got, want, and a judgement, which is the whole content of a Go test. What
// is missing is only the typing, and gluon already holds every part — the
// expression as the user wrote it, its type from the in-process checker, and
// its value from having run it.
//
// The judgement stays the user's. :test writes down what was observed and says
// so in the generated doc comment; it does not decide the answer was right,
// which is why the default is to print rather than to write. :save -test is
// what keeps one.
//
// :mock and :spy sit on the other side of the same line: they run nothing at
// all. An interface's method set is a go/types computation — the one :m
// already performs — and a mock is that method set with func fields in place
// of bodies.

import (
	"fmt"
	"go/format"
	"strconv"
	"strings"
	"unicode"

	"github.com/sandboxws/gluon/internal/inspect"
	"github.com/sandboxws/gluon/internal/pretty"
	"github.com/sandboxws/gluon/internal/render"
	"github.com/sandboxws/gluon/internal/session"
	"github.com/sandboxws/gluon/internal/syntax"
)

// generated is one test :test produced, held so :save -test can write it.
type generated struct {
	Name string
	Src  string
}

// metaTest is :test — the session's own judgement, as Go source.
func (c *Core) metaTest(arg string) Result {
	table, exp := parseTestArgs(arg)
	if exp == "" {
		return Result{Out: "usage: :test [-table] <expression>   e.g. :test double(21)", Err: true}
	}

	// An entry addressing a previous result is bound inside main(), and a test
	// file beside that program cannot see it. Said here rather than left to
	// `undefined: _2` from the compiler.
	if ords, usesIt := render.Uses(exp); len(ords) > 0 || usesIt {
		return Result{Out: "error: " + exp + " names a value this session printed — " +
			"a test has to build its own, so write the expression out", Err: true}
	}

	// The checker is asked but never required. Invariant 5: a failure here is
	// not a verdict, so it costs the direct comparison and nothing else — the
	// deep-equality form is correct for a comparable value too.
	target, resolveErr := c.resolve(exp)
	if resolveErr == nil {
		switch {
		case target.IsType:
			return Result{Out: "error: that is a type, not a value — :mock generates one for an interface", Err: true}
		case target.Void:
			return Result{Out: "error: " + exp + " has no value to assert on", Err: true}
		case len(target.Tuple) > 1:
			return Result{Out: "error: :test takes one value; " + exp + " returns " +
				strconv.Itoa(len(target.Tuple)), Err: true}
		}
	}

	setup, err := c.setupFor(exp)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}

	// Transient, like :bench and :err: asking what an expression evaluates to
	// must not add an entry or move the session's imports (invariant 14).
	res, err := c.ev.EvalTransient(c.sess, session.Entry{Kind: session.KindExpr, Src: exp})
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	userOut, vals := pretty.Parse(res.Output)
	if len(vals) != 1 {
		out := strings.TrimRight(userOut, "\n")
		if out == "" {
			out = "error: " + exp + " produced no single value to assert on"
		}
		return Result{Out: out, Err: true}
	}

	want, err := pretty.Literal(vals[0])
	if err != nil {
		// The refusal names the component, so `:test` a narrower expression is
		// something the user can actually act on.
		return Result{Out: "error: no test — the value " + err.Error(), Err: true}
	}

	g := generated{Name: c.testName(exp)}
	if table {
		g.Src = tableTest(g.Name, exp, want, setup, inspect.TypeName(target, vals[0]), inspect.Comparable(target))
	} else {
		g.Src = plainTest(g.Name, exp, want, setup, inspect.Comparable(target))
	}
	src, ferr := format.Source([]byte(g.Src))
	if ferr != nil {
		return Result{Out: "error: the generated test did not format — " + ferr.Error(), Err: true}
	}
	g.Src = strings.TrimRight(string(src), "\n")

	c.tests = append(c.tests, g)
	// Tagged Go although the two-line footer below is prose. One language per
	// Out is the whole simplification the tag buys; the footer scans as an
	// unpainted em-dash, a number and some identifiers, which reads as nothing
	// in particular rather than as damage.
	return Result{Out: g.Src + "\n\n" + testFooter(len(c.tests)), Lang: syntax.Go}
}

func testFooter(n int) string {
	if n == 1 {
		return "— 1 test held; :save -test writes it beside the program"
	}
	return fmt.Sprintf("— %d tests held; :save -test writes them beside the program", n)
}

// parseTestArgs splits :test's leading flag from its expression, following
// parseSaveArgs and parseQueryArgs: a flag is read only while it leads, and
// everything from the first non-flag word on is the expression verbatim.
func parseTestArgs(arg string) (table bool, exp string) {
	rest := strings.TrimSpace(arg)
	for {
		switch {
		case strings.HasPrefix(rest, "-table "), rest == "-table":
			table = true
			rest = strings.TrimSpace(strings.TrimPrefix(rest, "-table"))
		default:
			return table, rest
		}
	}
}

// plainTest is the one-assertion form.
func plainTest(name, exp, want string, setup []string, comparable bool) string {
	var b strings.Builder
	b.WriteString(docComment(name, exp))
	fmt.Fprintf(&b, "func %s(t *testing.T) {\n", name)
	writeSetup(&b, setup)
	fmt.Fprintf(&b, "\twant := %s\n", want)
	fmt.Fprintf(&b, "\tgot := %s\n", exp)
	if comparable {
		b.WriteString("\tif got != want {\n")
	} else {
		b.WriteString("\t// The value's type is not comparable with ==, so this asserts deep equality.\n")
		b.WriteString("\tif !reflect.DeepEqual(got, want) {\n")
	}
	fmt.Fprintf(&b, "\t\tt.Errorf(%s, got, want)\n", strconv.Quote(escapeVerbs(exp)+" = %v, want %v"))
	b.WriteString("\t}\n}\n")
	return b.String()
}

// tableTest is the same assertion in the shape a second case can be added to.
// The observed expression and value are the first row; the rest of the table is
// for the cases the user has in mind and has not typed yet.
func tableTest(name, exp, want string, setup []string, typ string, comparable bool) string {
	var b strings.Builder
	b.WriteString(docComment(name, exp))
	fmt.Fprintf(&b, "func %s(t *testing.T) {\n", name)
	writeSetup(&b, setup)
	b.WriteString("\ttests := []struct {\n\t\tname string\n")
	fmt.Fprintf(&b, "\t\tgot  %s\n\t\twant %s\n\t}{\n", typ, typ)
	fmt.Fprintf(&b, "\t\t{%s, %s, %s},\n\t}\n", strconv.Quote(exp), exp, want)
	b.WriteString("\tfor _, tc := range tests {\n\t\tt.Run(tc.name, func(t *testing.T) {\n")
	if comparable {
		b.WriteString("\t\t\tif tc.got != tc.want {\n")
	} else {
		b.WriteString("\t\t\t// The value's type is not comparable with ==, so this asserts deep equality.\n")
		b.WriteString("\t\t\tif !reflect.DeepEqual(tc.got, tc.want) {\n")
	}
	b.WriteString("\t\t\t\tt.Errorf(\"%s = %v, want %v\", tc.name, tc.got, tc.want)\n")
	b.WriteString("\t\t\t}\n\t\t})\n\t}\n}\n")
	return b.String()
}

// docComment says what the test is and, more importantly, what it is not.
func docComment(name, exp string) string {
	return "// " + name + " was generated by gluon from a value observed at the prompt.\n" +
		"// It records what " + exp + " produced, not what it should produce: the\n" +
		"// judgement that the answer was right was made at the prompt, and this only\n" +
		"// writes it down.\n"
}

func writeSetup(b *strings.Builder, setup []string) {
	if len(setup) == 0 {
		return
	}
	b.WriteString("\t// The session bound these at the prompt, where they are locals inside\n" +
		"\t// main(). The test carries them so the expression can be evaluated again.\n")
	for _, s := range setup {
		for _, line := range strings.Split(s, "\n") {
			b.WriteString("\t" + line + "\n")
		}
	}
	b.WriteString("\n")
}

// escapeVerbs makes an expression safe to embed in a format string. A % in the
// user's own source — fmt.Sprintf("%d", n) is a thoroughly ordinary thing to
// :test — would otherwise be read as a verb by the Errorf it lands in.
func escapeVerbs(s string) string { return strings.ReplaceAll(s, "%", "%%") }

// setupFor returns the session statements the generated test has to carry.
//
// A binding made at the prompt is a local inside main(), so a test file beside
// that program cannot see it: without this, `x := 21` followed by
// `:test double(x)` generates source that reads perfectly and does not
// compile. The entries that bound what the expression reads come along, in
// order, which is also what makes "evaluate it again" mean anything.
//
// The dependency question is the one pinning already asks, so it is asked with
// the same over-cautious answer — see render.Reads. Declarations are skipped:
// they are already at package scope, where the test can see them.
func (c *Core) setupFor(exp string) ([]string, error) {
	wanted := []string{exp}
	var idx []int
	for i := len(c.sess.Entries) - 1; i >= 0; i-- {
		e := c.sess.Entries[i]
		if e.Pinned || e.Kind != session.KindStmt || len(e.Binds) == 0 {
			continue
		}
		if !bindsSomethingIn(e.Binds, wanted) {
			continue
		}
		if ords, usesIt := render.Uses(e.Src); len(ords) > 0 || usesIt {
			return nil, fmt.Errorf("entry %d — %s — names a value this session printed, "+
				"which a test cannot see; :test an expression that does not need it", i+1, e.Src)
		}
		wanted = append(wanted, e.Src)
		idx = append(idx, i)
	}
	out := make([]string, 0, len(idx))
	for i := len(idx) - 1; i >= 0; i-- {
		out = append(out, c.sess.Entries[idx[i]].Src)
	}
	return out, nil
}

func bindsSomethingIn(binds, srcs []string) bool {
	for _, b := range binds {
		for _, src := range srcs {
			if render.Reads(src, b) {
				return true
			}
		}
	}
	return false
}

// testName turns an expression into a test function name. The identifiers in
// it are what a reader recognises the test by — TestStringsToUpper for
// strings.ToUpper("a") — and an expression with none falls back to a name that
// at least does not collide.
func (c *Core) testName(exp string) string {
	var parts []string
	var cur strings.Builder
	for _, r := range exp {
		if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(r)
			continue
		}
		if cur.Len() > 0 {
			parts = append(parts, cur.String())
			cur.Reset()
		}
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}

	var b strings.Builder
	b.WriteString("Test")
	for _, p := range parts {
		if len(b.String()) > 40 {
			break
		}
		r := []rune(p)
		if unicode.IsDigit(r[0]) {
			// A leading digit cannot start an identifier segment, and a number
			// in an expression is rarely what names the test anyway.
			continue
		}
		r[0] = unicode.ToUpper(r[0])
		b.WriteString(string(r))
	}
	name := b.String()
	if name == "Test" {
		name = "TestExpr"
	}
	return c.uniqueTestName(name)
}

// uniqueTestName keeps two generated tests from declaring the same function,
// which is what `:test x` twice would otherwise do.
func (c *Core) uniqueTestName(name string) string {
	taken := map[string]bool{}
	for _, g := range c.tests {
		taken[g.Name] = true
	}
	if !taken[name] {
		return name
	}
	for n := 2; ; n++ {
		try := name + strconv.Itoa(n)
		if !taken[try] {
			return try
		}
	}
}

// TestFile assembles the held tests into one file for the scratch module.
// Empty when nothing has been generated, which is what :save -test reports
// rather than writing a test file with no tests in it.
func (c *Core) TestFile() string {
	if len(c.tests) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("package main\n\n")
	// goimports resolves the rest when the file is written into the module —
	// the expressions carry whatever packages the session's own line did.
	b.WriteString("import \"testing\"\n")
	for _, g := range c.tests {
		b.WriteString("\n" + g.Src + "\n")
	}
	return b.String()
}

// metaMock is :mock, and with spy set, :spy.
//
// It runs nothing. The method set comes from the checker the same way :iface's
// does, which is why neither is a build and why an interface that embeds
// another needs no extra walk.
func (c *Core) metaMock(arg string, spy bool) Result {
	usage := "usage: :mock <interface>   e.g. :mock io.Reader"
	if spy {
		usage = "usage: :spy <interface>   e.g. :spy io.Reader"
	}
	res, ops, bad := c.probe(arg, 1, usage)
	if bad != nil {
		return *bad
	}
	op := ops[0]
	in, err := op.Iface()
	if err != nil {
		return notInterface(op, err)
	}
	src, err := inspect.Mock(res.Pkg, op.Name(), in, spy)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	text := strings.TrimRight(src, "\n")
	return sourceResult(op.Name(), text, syntax.Go)
}
