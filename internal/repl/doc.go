package repl

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

// docMode is which of :doc's questions was asked. The flags are modes rather
// than options because they are four different answers about one name, not
// four adjustments to one answer — asking for two of them at once is a
// question with no answer, which parseDocArgs refuses rather than picks from.
type docMode int

const (
	docDefault docMode = iota
	docSrc
	docExamples
	docURL
	docPkg
)

// docFlags are the mode flags, in the order :help documents them.
var docFlags = []struct {
	name string
	mode docMode
}{
	{"-src", docSrc},
	{"-examples", docExamples},
	{"-url", docURL},
	{"-pkg", docPkg},
}

// parseDocArgs cuts the mode flag off :doc's argument line and hands back what
// is left verbatim — a symbol, a method, or a package path, all of which `go
// doc` already knows how to read and gluon must not reinterpret.
//
// cutFlag rather than a prefix match, for the reason :get -rm has it: `-srcX`
// is a name, not a flag.
func parseDocArgs(arg string) (docMode, string, error) {
	arg = strings.TrimSpace(arg)
	mode, chosen := docDefault, ""
	for {
		cut := false
		for _, f := range docFlags {
			rest, ok := cutFlag(arg, f.name)
			if !ok {
				continue
			}
			if mode != docDefault {
				return docDefault, "", fmt.Errorf(
					"one flag at a time: %s and %s", chosen, f.name)
			}
			mode, chosen, arg, cut = f.mode, f.name, rest, true
			break
		}
		if !cut {
			return mode, arg, nil
		}
	}
}

// docExample is one runnable example, and the identifier it demonstrates.
type docExample struct {
	// Ident is what the example is of — "T.M" for a method — and is empty for
	// a package-level example.
	Ident string
	// Suffix is the lower-case variant suffix the convention allows, so a
	// second example of the same identifier is distinguishable from the first.
	Suffix string
	// Src is the declaration verbatim, sliced out of the file rather than
	// printed from the AST: the comments inside a body are the half of an
	// example that says what it demonstrates, and `// Output:` is the half
	// that says what it prints.
	Src string
}

// packageExamples reads a package's runnable examples off disk, and the
// package's own name.
//
// `go doc` does not report examples, so the sources are the only place they
// are. Every _test.go file is parsed, not just example_test.go: the convention
// names the *functions*, and a package with more than a handful splits them
// across files (sync's are in example_test.go and example_pool_test.go).
// Nothing is guessed from the file name.
func packageExamples(dir string) (string, []docExample, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), "_test.go") {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)

	fset := token.NewFileSet()
	pkg := ""
	var out []docExample
	for _, name := range names {
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return "", nil, err
		}
		file, err := parser.ParseFile(fset, name, src, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			// One unparseable file is not a package with no examples. Skip it
			// and keep the ones that do parse, for the same reason detection
			// separates "found nothing" from "did not look".
			continue
		}
		if pkg == "" {
			// An external test package is named pkg_test; the package it is
			// the examples of is the other half of that name.
			pkg = strings.TrimSuffix(file.Name.Name, "_test")
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Example") {
				continue
			}
			// An example takes nothing and returns nothing. A helper named
			// ExampleFoo that does not is not one, and `go test` would not run
			// it either.
			if fn.Type.Params.NumFields() != 0 || fn.Type.Results.NumFields() != 0 {
				continue
			}
			ident, suffix := exampleIdent(fn.Name.Name)
			start := fn.Pos()
			if fn.Doc != nil {
				start = fn.Doc.Pos()
			}
			out = append(out, docExample{
				Ident:  ident,
				Suffix: suffix,
				Src:    string(src[fset.Position(start).Offset:fset.Position(fn.End()).Offset]),
			})
		}
	}
	return pkg, out, nil
}

// exampleIdent maps an example's function name to the identifier it is an
// example of, using the convention the testing package fixes:
//
//	Example           the package
//	ExampleF          the function or type F
//	ExampleT_M        the method M of T
//	ExampleF_suffix   a second example of F, named by a lower-case suffix
//
// A name that does not fit — a leading underscore before an upper-case word, a
// third element, a lower-case head — is reported as package-level rather than
// attached to a guessed symbol. An example labelled with the wrong identifier
// is worse than one labelled with none: the label is the whole reason to show
// examples separately, so it has to be one the convention actually produced.
func exampleIdent(name string) (ident, suffix string) {
	rest := strings.TrimPrefix(name, "Example")
	if rest == "" {
		return "", ""
	}
	parts := strings.Split(rest, "_")
	if last := parts[len(parts)-1]; startsLower(last) {
		suffix, parts = last, parts[:len(parts)-1]
	}
	if len(parts) == 0 || len(parts) > 2 {
		return "", suffix
	}
	for _, p := range parts {
		if p == "" || !startsUpper(p) {
			return "", suffix
		}
	}
	return strings.Join(parts, "."), suffix
}

func startsLower(s string) bool {
	return s != "" && unicode.IsLower([]rune(s)[0])
}

func startsUpper(s string) bool {
	return s != "" && unicode.IsUpper([]rune(s)[0])
}

// renderExamples is the text :doc -examples answers with: every example
// verbatim, each labelled with what it is an example of.
//
// The labels are Go comments, so the whole answer is a Go file and can be
// highlighted as one without a header line pretending to be code.
func renderExamples(pkg string, exs []docExample) string {
	var b strings.Builder
	fmt.Fprintf(&b, "// %s — %s", pkg, plural(len(exs), "example"))
	for _, ex := range exs {
		label := "package " + pkg
		if ex.Ident != "" {
			label = pkg + "." + ex.Ident
		}
		if ex.Suffix != "" {
			label += " (" + ex.Suffix + ")"
		}
		b.WriteString("\n\n// " + label + "\n")
		b.WriteString(strings.TrimRight(ex.Src, "\n"))
	}
	return b.String()
}

// docSite is where rendered Go documentation lives.
const docSite = "https://pkg.go.dev/"

// zeroPseudo is the version go.mod carries for a module that is required only
// so a replace can point somewhere else. It is a version in form and not in
// fact, so it is treated as no version at all.
const zeroPseudo = "v0.0.0-00010101000000-"

// docAddress builds the pkg.go.dev address for a package or a symbol, and
// reports whether it names the version the session actually has.
//
// It is a pure function of the build list and the argument: no process runs,
// nothing is fetched, and no browser is opened. `reqs` is Requires()' "path
// version" form.
//
// The version matters because an address without one resolves to the latest
// published, which may not be the code the session is running — linking a
// reader at documentation for a different version is the quiet kind of wrong.
// When it cannot be determined the address is still built, and the caller says
// the version is unknown.
func docAddress(reqs []string, arg string) (string, bool) {
	arg = strings.TrimSpace(arg)
	mod, version := moduleFor(reqs, arg)
	pkg, sym := splitDocPath(arg, mod)

	url := docSite + pkg
	known := version != "" && !strings.HasPrefix(version, zeroPseudo)
	if known {
		// pkg.go.dev versions the module, not the package: the @version goes
		// after the module path, and the package under it follows.
		url = docSite + mod + "@" + version + strings.TrimPrefix(pkg, mod)
	}
	if sym != "" {
		url += "#" + sym
	}
	return url, known
}

// moduleFor is the module in the build list that arg names something in, with
// the version the list has for it.
//
// The longest match wins, so a module and its /v2 do not race. The standard
// library is in no build list, so it matches nothing and takes the
// unknown-version path — its version is the toolchain's, which go.mod does not
// carry.
func moduleFor(reqs []string, arg string) (mod, version string) {
	for _, r := range reqs {
		p, v, _ := strings.Cut(r, " ")
		if p == "" || len(p) <= len(mod) {
			continue
		}
		if arg == p || strings.HasPrefix(arg, p+"/") || strings.HasPrefix(arg, p+".") {
			mod, version = p, v
		}
	}
	return mod, version
}

// splitDocPath cuts arg into the package path and the symbol inside it.
//
// The cut is the first dot after the last slash. A package path's last element
// can contain a dot — gopkg.in/yaml.v3 — but a package *name* cannot, so from
// the first dot of the last element on is the symbol, which may itself be
// Type.Method. A module path known from the build list is left whole, which is
// what keeps yaml.v3 from being read as symbol v3 of a package yaml.
func splitDocPath(arg, mod string) (pkg, sym string) {
	rest := arg
	if mod != "" {
		rest = strings.TrimPrefix(arg, mod)
	}
	slash := strings.LastIndex(rest, "/")
	dot := strings.Index(rest[slash+1:], ".")
	if dot < 0 {
		return arg, ""
	}
	cut := slash + 1 + dot
	return arg[:len(arg)-len(rest)+cut], rest[cut+1:]
}
