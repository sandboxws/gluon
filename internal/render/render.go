// Package render turns an accumulated session into a real Go program: one
// main.go holding the user's code, plus sibling files carrying the value
// printer and its fd gate. Everything that makes Go hostile to scratch work
// is fixed here.
package render

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/tools/imports"

	"github.com/sandboxws/gluon/internal/gluonrt"
	"github.com/sandboxws/gluon/internal/session"
)

// PrintFunc is the injected printer's name. It is __gluon-prefixed so a
// session that declares its own p or print does not collide.
const (
	PrintFunc  = "__gluonPrint"
	MuteFunc   = "__gluonMute"
	UnmuteFunc = "__gluonUnmute"
	// HdrFunc describes slice and string headers rather than contents. It
	// writes its own payload, so it is called as a statement, not printed.
	HdrFunc = "__gluonHdrs"
	// DrainFunc waits briefly for goroutines the session started, so their
	// output is not lost to main returning first.
	DrainFunc = "__gluonDrain"

	// ItName is the REPL's name for the most recently printed value — irb's
	// `_`, which Go cannot spell because a bare underscore is the blank
	// identifier. It is never emitted: rewriteIt resolves it to the ordinal
	// that was in scope at that entry, so it never reaches the compiler.
	ItName = "it"
)

// Sink decides how a printable expression is emitted. The /*line*/ directive
// is written between Prefix and the expression, never before Prefix, so a
// reported column maps to what the user typed rather than to the wrapper.
type Sink struct{ Prefix, Suffix string }

var (
	// PrintSink describes the value, which is what a REPL is for.
	PrintSink = Sink{Prefix: PrintFunc + "(", Suffix: ")"}
	// EscSink discards it instead. __gluonPrint cannot be used for escape
	// analysis: its parameter is ...any, tagged "leaking param content: vs",
	// so every argument to it is reported as escaping — gluon's doing, not the
	// user's. An assignment to blank gets a discard hole in the compiler's
	// escape pass, so the expression is still analysed and still reported
	// while flowing nowhere.
	EscSink = Sink{Prefix: "_ = "}
)

// OrdName is the binding for the nth printed value, 1-based. `_1` is an
// ordinary identifier — only a bare `_` is blank — so this needs no prefix.
func OrdName(n int) string { return "_" + strconv.Itoa(n) }

// Synthetic reports whether a name is one gluon binds on the user's behalf.
// goimports can never resolve these, and treating one as a package qualifier
// would cost a pointless resolution pass on every line that uses it.
func Synthetic(name string) bool {
	if name == ItName {
		return true
	}
	rest, ok := strings.CutPrefix(name, "_")
	if !ok || rest == "" || rest[0] == '0' {
		return false
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Every entry is preceded by a //line directive naming a synthetic file, so
// the compiler, go/types and runtime panics all report positions relative to
// what the user typed instead of to the assembled program. One directive per
// entry is enough: positions after it advance normally, so line 2 of a
// multi-line entry reports as line 2 without any arithmetic here.
//
// It must be the /*line*/ form, not //line. In go/scanner the guard is
// `lit[1] == '*' || offs == s.lineOffset`, so the // form is only honoured at
// column 1 — and gofmt indents comments inside a function body, which would
// silently stop it working.
const (
	lineFilePrefix = "gluon-in-"

	// ColumnShift compensates for gofmt inserting one space between the
	// directive and the token after it, which moves every reported column by
	// exactly that much. It applies to statements and expressions, where the
	// directive stays on the same line as the code.
	ColumnShift = 1

	// DeclLineShift compensates for the other half of the same behaviour.
	// gofmt treats a comment before a top-level declaration as a doc comment
	// and hoists it onto its own line, so the position the directive names is
	// the newline rather than the declaration — putting the code one line
	// later, with its columns untouched.
	//
	// Both constants are pinned by a test that renders, formats and builds for
	// real, so a change in gofmt's behaviour fails loudly instead of quietly
	// misreporting positions.
	DeclLineShift = 1
)

// LineFile is the synthetic file name standing in for entry i.
func LineFile(i int) string { return lineFilePrefix + strconv.Itoa(i) + ".go" }

func lineDirective(i int) string { return "/*line " + LineFile(i) + ":1:1*/" }

// EntryOf reports which entry a synthetic file name refers to.
func EntryOf(name string) (int, bool) {
	rest, ok := strings.CutPrefix(name, lineFilePrefix)
	if !ok {
		return 0, false
	}
	rest, ok = strings.CutSuffix(rest, ".go")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// directiveRe matches an emitted directive so it can be stripped for display.
var directiveRe = regexp.MustCompile(`/\*line ` + lineFilePrefix + `\d+\.go:\d+:\d+\*/ ?`)

// ImportSpec is one resolved import, carried between evaluations so goimports
// (~135ms) does not have to re-resolve an unchanged import set every line.
type ImportSpec struct {
	Name string // alias, usually empty
	Path string
}

// PackageName guesses the identifier an import path is used under.
//
// The last element is right almost always, and wrong in exactly two shapes the
// module ecosystem made common: a major-version suffix (github.com/foo/bar/v2
// is `bar`) and gopkg.in's dotted form (gopkg.in/yaml.v3 is `yaml`). Callers
// that care write the guess out as an explicit alias, so a wrong guess produces
// a program that still compiles under the name gluon predicted.
func PackageName(p string) string {
	p = strings.Trim(p, "/")
	if p == "" {
		return ""
	}
	elems := strings.Split(p, "/")
	last := elems[len(elems)-1]
	if isMajorVersion(last) && len(elems) > 1 {
		last = elems[len(elems)-2]
	}
	// gopkg.in/yaml.v3 — the version rides on the element itself.
	if i := strings.LastIndex(last, "."); i > 0 && isMajorVersion(last[i+1:]) {
		last = last[:i]
	}
	if !isIdent(last) {
		return ""
	}
	return last
}

// ImportedName is the identifier an import binds: its alias, else the guess
// PackageName makes. Empty for a blank or dot import, which bind no qualifier,
// and for a path whose name cannot be guessed (a directory like 2d-geom holding
// package geom) — callers then have only the path to go on.
func ImportedName(im ImportSpec) string {
	switch im.Name {
	case "_", ".":
		return ""
	case "":
		return PackageName(im.Path)
	}
	return im.Name
}

// DeclaredImports is what the session's own entries import: an `import` typed
// at the prompt, or the import block of a file pasted in with -e or :load.
// Those are the program as its author wrote it, which is why the block gluon
// generates defers to them — see withoutDeclared.
func DeclaredImports(s *session.Session) []ImportSpec {
	var out []ImportSpec
	for _, e := range s.Entries {
		// A pinned entry emits no code, so its imports are not in the program.
		if e.Kind != session.KindDecl || e.Pinned {
			continue
		}
		// ImportsOnly stops at the first other declaration, so a func costs
		// next to nothing here.
		f, err := parser.ParseFile(token.NewFileSet(), "", "package p\n"+e.Src, parser.ImportsOnly)
		if err != nil {
			continue
		}
		for _, im := range f.Imports {
			p, err := strconv.Unquote(im.Path.Value)
			if err != nil {
				continue
			}
			spec := ImportSpec{Path: p}
			if im.Name != nil {
				spec.Name = im.Name.Name
			}
			out = append(out, spec)
		}
	}
	return out
}

// withoutDeclared drops from a generated import set whatever a declaration
// already covers: the same path, or any path under a name a declaration
// already binds. Either would be the redeclaration the compiler rejects — and
// the second is the one a host produces, whose package called math or heap is
// not the standard library's the file imported by that name.
func withoutDeclared(imps, declared []ImportSpec) []ImportSpec {
	if len(declared) == 0 || len(imps) == 0 {
		return imps
	}
	paths := make(map[string]bool, len(declared))
	names := make(map[string]bool, len(declared))
	for _, d := range declared {
		paths[d.Path] = true
		if n := ImportedName(d); n != "" {
			names[n] = true
		}
	}
	out := make([]ImportSpec, 0, len(imps))
	for _, im := range imps {
		if paths[im.Path] {
			continue
		}
		if n := ImportedName(im); n != "" && names[n] {
			continue
		}
		out = append(out, im)
	}
	return out
}

func isMajorVersion(s string) bool {
	if len(s) < 2 || s[0] != 'v' {
		return false
	}
	for _, r := range s[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isIdent(s string) bool {
	for i, r := range s {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			continue
		}
		if i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return s != ""
}

// Main renders the session's main.go. When imps is nil the file is emitted
// with no import block, for FixImports to resolve from scratch; when it is
// supplied the cached block is written directly and plain formatting suffices.
func Main(s *session.Session, imps []ImportSpec) (string, error) {
	return MainAs(s, imps, PrintSink)
}

// MainAs renders the session with a chosen sink for printable expressions.
// Everything else about the program is identical.
func MainAs(s *session.Session, imps []ImportSpec, sink Sink) (string, error) {
	return MainFrom(s, imps, sink, len(s.Entries)-1)
}

// MainFrom renders the session unmuting from entry firstNew onward, so every
// entry a pasted batch just appended prints while the replayed prefix stays
// silent. The ordinary one-line rule is firstNew == len(Entries)-1 — MainAs —
// and for that case the program text is byte-identical to what it always was,
// which the result cache (keyed on program text) and the pinned Plain corpus
// both rest on.
func MainFrom(s *session.Session, imps []ImportSpec, sink Sink, firstNew int) (string, error) {
	srcs, need, err := lastResults(s)
	if err != nil {
		return "", err
	}
	ord := Ordinals(s)

	var decls []string
	// groups holds each entry's body lines separately so the replayed prefix
	// can be muted up to the newest entry.
	var groups [][]string
	known := map[string]bool{}

	for i, e := range s.Entries {
		var body []string
		// A pinned entry contributes no code at all: it has already run, and
		// PinBlockers guaranteed nothing later needs what it bound. The empty
		// slot still goes into groups so indices stay aligned with Entries,
		// which is what the unmute boundary below is counted in.
		if e.Pinned {
			groups = append(groups, nil)
			continue
		}
		// Positions inside this entry are reported against its own synthetic
		// file, so an error names the line the user typed.
		at := lineDirective(i)
		switch e.Kind {
		case session.KindDecl:
			decls = append(decls, at+srcs[i])

		case session.KindStmt:
			src, fresh, err := rewriteRedeclare(srcs[i], known)
			if err != nil {
				return "", err
			}
			body = append(body, at+src)
			for _, b := range fresh {
				known[b] = true
				// Go treats an unused local as a compile error. A REPL binds
				// names precisely so it can look at them later, so suppress it.
				body = append(body, "_ = "+b)
			}

		case session.KindExpr:
			switch {
			case e.NoValue:
				// A call with no results cannot be an argument; emit it bare.
				body = append(body, at+srcs[i])

			case need[ord[i]]:
				// A later entry addresses this value, so bind it on the way
				// past. No `_ = ` suppression is needed: the print call below
				// is itself a use.
				//
				// Emission is demand-driven, which is also what keeps the
				// newest entry in its original shape for free — nothing can
				// follow it, so its ordinal is never needed. That matters:
				// check.Result.Printed reads the type of whatever
				// __gluonPrint's argument is, and an identifier bound to a
				// constant expression is a variable, whose TV.Value is nil.
				// Binding the newest entry would quietly cost the constant
				// fast path.
				body = append(body, OrdName(ord[i])+" := "+at+srcs[i])
				body = append(body, sink.Prefix+OrdName(ord[i])+sink.Suffix)

			default:
				// The directive goes inside the call, not before it, so a
				// column maps to the expression the user typed rather than to
				// the wrapper gluon put around it.
				body = append(body, sink.Prefix+at+srcs[i]+sink.Suffix)
			}
		}
		groups = append(groups, body)
	}

	// Unmute just before the first new entry that contributes code, so the
	// replayed prefix stays silent and everything from there on prints. If no
	// new entry contributes code — declarations — nothing is unmuted at all:
	// walking backwards to an earlier entry would replay output the user has
	// already seen.
	last := -1
	for i := max(firstNew, 0); i < len(groups); i++ {
		if len(groups[i]) > 0 {
			last = i
			break
		}
	}

	// The session's own import declarations are emitted with the other
	// declarations below, so the generated block must not repeat them.
	imps = withoutDeclared(imps, DeclaredImports(s))

	var b strings.Builder
	b.WriteString("package main\n\n")
	if len(imps) > 0 {
		b.WriteString("import (\n")
		for _, im := range imps {
			b.WriteString("\t")
			if im.Name != "" {
				b.WriteString(im.Name)
				b.WriteString(" ")
			}
			b.WriteString(strconv.Quote(im.Path))
			b.WriteString("\n")
		}
		b.WriteString(")\n\n")
	}
	for _, d := range decls {
		b.WriteString(d)
		b.WriteString("\n\n")
	}
	b.WriteString("func main() {\n")
	b.WriteString("\t" + MuteFunc + "()\n")
	for i, g := range groups {
		if i == last {
			b.WriteString("\t" + UnmuteFunc + "()\n")
		}
		for _, line := range g {
			b.WriteString("\t")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if last < 0 {
		b.WriteString("\t" + UnmuteFunc + "()\n")
	}
	// Last thing before main returns: give any goroutine the session started a
	// bounded chance to run, and say so.
	b.WriteString("\t" + DrainFunc + "()\n")
	b.WriteString("}\n")
	return b.String(), nil
}

// RuntimeFiles returns the injected runtime's sources with their package
// clauses rewritten so they compile alongside main.go. Two files, because the
// fd gate is per-platform: gluonrt_fd.go is the variant for this machine's
// GOOS, and it keeps its build tag — the child compiles here, so the tag is
// satisfied by construction, and a copy that strays to another OS fails to
// build rather than muting wrongly.
func RuntimeFiles() map[string]string {
	return map[string]string{
		"gluonrt.go":    asMainPackage(gluonrt.Source),
		"gluonrt_fd.go": asMainPackage(gluonrt.FDSource(runtime.GOOS)),
	}
}

func asMainPackage(src string) string {
	return strings.Replace(src, "package gluonrt", "package main", 1)
}

// FixImports runs goimports, which both adds the imports the user did not type
// and drops the ones their last edit orphaned. filename must be the path
// inside the temp module so resolution happens in that module's context.
func FixImports(filename string, src []byte) ([]byte, error) {
	return imports.Process(filename, src, &imports.Options{
		Comments:  true,
		TabWidth:  8,
		Fragment:  false,
		AllErrors: false,
	})
}

// Display strips the fd-gating calls, which are replay machinery rather than
// anything the user wrote. Used by :src and :save so both show a program that
// reads like one a person would write.
func Display(src string) string {
	var keep []string
	for _, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		if t == MuteFunc+"()" || t == UnmuteFunc+"()" || t == DrainFunc+"()" {
			continue
		}
		keep = append(keep, line)
	}
	out := directiveRe.ReplaceAllString(strings.Join(keep, "\n"), "")
	if b, err := format.Source([]byte(out)); err == nil {
		return string(b)
	}
	return out
}

// Executes reports whether running the program can produce new output. A
// declaration cannot: it binds a name and runs nothing, so the build still has
// to happen to validate it, but the exec can be skipped.
func Executes(s *session.Session) bool {
	if len(s.Entries) == 0 {
		return false
	}
	return s.Entries[len(s.Entries)-1].Kind != session.KindDecl
}

// Qualifiers returns the identifiers used as a package qualifier in src — the
// X in every X.Y. Some will be local variables rather than packages; the
// caller subtracts those. Used to predict whether an entry needs goimports,
// so a doomed build can be skipped rather than discovered.
func Qualifiers(src string) []string {
	f, _, _, ok := parseEntry(src)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && !seen[id.Name] {
			seen[id.Name] = true
			out = append(out, id.Name)
		}
		return true
	})
	return out
}

// Callee returns the textual function of a call expression — "slices.Sort" for
// "slices.Sort(x)". Zero-result calls cannot be printed, and without a type
// checker that is only discoverable by failing a build; remembering the callee
// means the same mistake is made at most once per function.
func Callee(src string) (string, bool) {
	expr, err := parser.ParseExpr(src)
	if err != nil {
		return "", false
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, token.NewFileSet(), call.Fun); err != nil {
		return "", false
	}
	return buf.String(), true
}

// Format runs gofmt in-process. It is the fast path: no package resolution,
// no subprocess, and it surfaces syntax errors before a build is attempted.
func Format(src []byte) ([]byte, error) { return format.Source(src) }

// ExtractImports reads back what goimports decided, so the next evaluation can
// skip resolution entirely.
func ExtractImports(src []byte) ([]ImportSpec, error) {
	f, err := parser.ParseFile(token.NewFileSet(), "", src, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	var out []ImportSpec
	for _, im := range f.Imports {
		path, err := strconv.Unquote(im.Path.Value)
		if err != nil {
			continue
		}
		spec := ImportSpec{Path: path}
		if im.Name != nil {
			spec.Name = im.Name.Name
		}
		out = append(out, spec)
	}
	return out, nil
}

// rewriteRedeclare downgrades `:=` to `=` when every name on the left is
// already bound, so retyping `x := 1` behaves the way it does in irb instead
// of failing with "no new variables on left side of :=". It returns the
// statement source and the names this entry genuinely introduces.
func rewriteRedeclare(src string, known map[string]bool) (string, []string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", "package p\nfunc _() {\n"+src+"\n}", 0)
	if err != nil {
		return "", nil, err
	}
	fn, ok := f.Decls[0].(*ast.FuncDecl)
	if !ok || fn.Body == nil {
		return src, nil, nil
	}

	var fresh []string
	changed := false

	for _, st := range fn.Body.List {
		switch st := st.(type) {
		case *ast.AssignStmt:
			if st.Tok != token.DEFINE {
				continue
			}
			var names []string
			allKnown := true
			for _, lhs := range st.Lhs {
				id, ok := lhs.(*ast.Ident)
				if !ok || id.Name == "_" {
					continue
				}
				names = append(names, id.Name)
				if !known[id.Name] {
					allKnown = false
				}
			}
			if len(names) == 0 {
				continue
			}
			if allKnown {
				// Every name already exists in this scope; plain assignment.
				st.Tok = token.ASSIGN
				changed = true
				continue
			}
			for _, n := range names {
				if !known[n] {
					fresh = append(fresh, n)
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
					if name.Name != "_" && !known[name.Name] {
						fresh = append(fresh, name.Name)
					}
				}
			}
		}
	}

	if !changed {
		return src, fresh, nil
	}

	var buf bytes.Buffer
	for i, st := range fn.Body.List {
		if i > 0 {
			buf.WriteByte('\n')
		}
		if err := printer.Fprint(&buf, fset, st); err != nil {
			return "", nil, err
		}
	}
	return buf.String(), fresh, nil
}

// UnboundQualifiers is Qualifiers minus the names the entry itself binds.
//
// A method body is full of selector expressions whose base is a receiver or a
// parameter — p.X in `func (p Point) Dist() int` — and treating those as
// package names makes the import predictor give up and run goimports, at about
// 135ms, to learn nothing. Filtering them is worth roughly that much on every
// method declaration.
//
// It over-approximates what is bound, which is the safe direction: a name
// wrongly treated as local only means goimports is skipped, and the type
// checker still catches the missing import and resolves it. Qualifiers stays
// unfiltered for that recovery path, which must not filter anything out.
func UnboundQualifiers(src string) []string {
	quals := Qualifiers(src)
	if len(quals) == 0 {
		return nil
	}
	bound := boundNames(src)
	out := quals[:0:0]
	for _, q := range quals {
		if !bound[q] {
			out = append(out, q)
		}
	}
	return out
}

// boundNames collects every identifier the source puts in a binding position:
// receivers, parameters, results, type parameters, short variable
// declarations, var specs, and range clauses.
func boundNames(src string) map[string]bool {
	f, _, _, ok := parseEntry(src)
	if !ok {
		return nil
	}

	bound := map[string]bool{}
	add := func(e ast.Expr) {
		if id, ok := e.(*ast.Ident); ok && id.Name != "_" {
			bound[id.Name] = true
		}
	}
	addFields := func(fl *ast.FieldList) {
		if fl == nil {
			return
		}
		for _, fld := range fl.List {
			for _, n := range fld.Names {
				add(n)
			}
		}
	}

	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncDecl:
			addFields(n.Recv)
			addFields(n.Type.TypeParams)
		case *ast.FuncType:
			addFields(n.Params)
			addFields(n.Results)
		case *ast.AssignStmt:
			if n.Tok == token.DEFINE {
				for _, lhs := range n.Lhs {
					add(lhs)
				}
			}
		case *ast.ValueSpec:
			for _, name := range n.Names {
				add(name)
			}
		case *ast.RangeStmt:
			add(n.Key)
			add(n.Value)
		case *ast.TypeSwitchStmt:
			if a, ok := n.Assign.(*ast.AssignStmt); ok {
				for _, lhs := range a.Lhs {
					add(lhs)
				}
			}
		}
		return true
	})
	return bound
}
