package repl

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// :doc has five forms and one rule that covers all of them: it answers about
// code without running any. These are the tests for the forms; the ones that
// hold the rule are at the bottom.

// TestDocPagesWhenItOutgrowsAWindow. Documentation is the output most likely
// to be longer than the terminal — `go doc net/http` is 206 lines — and
// scrollback swallows the beginning of it. :src and :guide have paged through
// the same modal for two releases; this is :doc joining them rather than being
// the exception ROADMAP carried as an open item since v2.
func TestDocPagesWhenItOutgrowsAWindow(t *testing.T) {
	c := testCore(t)
	long := c.doc("net/http")
	if long.Err {
		t.Skip("go doc net/http:", long.Out)
	}
	if long.Modal == nil {
		t.Fatalf(":doc net/http did not page %d lines", strings.Count(long.Out, "\n")+1)
	}
	short := c.doc("strings.Count")
	if short.Err {
		t.Skip("go doc strings.Count:", short.Out)
	}
	if short.Modal != nil {
		t.Errorf(":doc strings.Count opened a view over %d lines",
			strings.Count(short.Out, "\n")+1)
	}
}

// TestDocModalAndOutSayTheSame is invariant 19's half that a driver cannot
// see: whatever the view shows, Out carries in linear form, so a pipe loses
// nothing by ignoring the modal.
func TestDocModalAndOutSayTheSame(t *testing.T) {
	c := testCore(t)
	res := c.doc("net/http")
	if res.Err {
		t.Skip("go doc net/http:", res.Out)
	}
	if res.Modal == nil {
		t.Fatal(":doc net/http did not page")
	}
	if res.Modal.Text != res.Out {
		t.Errorf("the view and Out differ: %d bytes paged, %d bytes linear",
			len(res.Modal.Text), len(res.Out))
	}
	if !strings.Contains(res.Modal.Summary, "lines") {
		t.Errorf("the closing line does not say how much there was: %q", res.Modal.Summary)
	}
}

// TestDocThroughAPipeIsWhole. The non-interactive driver has no full screen to
// open, so it prints Out — and must not be refused the way :edit and :watch
// are, which are the results that genuinely need a terminal.
func TestDocThroughAPipeIsWhole(t *testing.T) {
	c := testCore(t)
	res := c.Submit(":doc net/http")
	if res.Err {
		t.Skip("go doc net/http:", res.Out)
	}
	if msg := noTerminal(c, res); msg != "" {
		t.Fatalf("the pipe driver refused :doc: %s", msg)
	}
	if n := strings.Count(res.Out, "\n") + 1; n < 100 {
		t.Errorf("the pipe received %d lines of net/http's documentation", n)
	}
	if !strings.HasPrefix(res.Out, "package http") {
		t.Errorf("the pipe received something other than the documentation:\n%.80s", res.Out)
	}
}

// TestDocFlagsParse covers the cut, including that the argument after it is
// passed on verbatim — `go doc` resolves names, and gluon must not start
// reinterpreting them.
func TestDocFlagsParse(t *testing.T) {
	for _, tc := range []struct {
		in   string
		mode docMode
		arg  string
	}{
		{"strings.Builder", docDefault, "strings.Builder"},
		{"  strings.Builder  ", docDefault, "strings.Builder"},
		{"-src strings.Count", docSrc, "strings.Count"},
		{"-examples strings", docExamples, "strings"},
		{"-url strings.Builder", docURL, "strings.Builder"},
		{"-pkg json", docPkg, "json"},
		{"-src", docSrc, ""},
		// Not flags: a name that merely starts like one.
		{"-srcfoo", docDefault, "-srcfoo"},
		{"-urlish.Thing", docDefault, "-urlish.Thing"},
	} {
		mode, arg, err := parseDocArgs(tc.in)
		if err != nil {
			t.Errorf("parseDocArgs(%q): %v", tc.in, err)
			continue
		}
		if mode != tc.mode || arg != tc.arg {
			t.Errorf("parseDocArgs(%q) = (%v, %q), want (%v, %q)", tc.in, mode, arg, tc.mode, tc.arg)
		}
	}

	// Two modes is a question with no answer, and picking one silently would
	// be answering something the user did not ask.
	if _, _, err := parseDocArgs("-src -url strings.Builder"); err == nil {
		t.Error("-src -url was accepted")
	}
}

// TestDocDocumentsItsFlags. The declaration is `:help :doc`, the go_doc
// tool's description and the docs page, so a flag missing from it is a flag an
// agent and a user both have to find by accident.
func TestDocDocumentsItsFlags(t *testing.T) {
	c := &Core{}
	cmd, ok := c.lookup(":doc")
	if !ok {
		t.Fatal(":doc is not registered")
	}
	if cmd.Arg != "[flags] <s>" {
		t.Errorf(":doc's Arg is %q", cmd.Arg)
	}
	for _, f := range docFlags {
		fl, ok := cmd.Usage.Flag(f.name)
		if !ok || fl.Help == "" || !fl.Mode {
			t.Errorf("%s is not declared on :doc as a mode with help: %+v", f.name, fl)
		}
	}
}

// TestExampleIdent holds the mapping from an example's name to what it is an
// example of. The convention is the testing package's, so the mapping is
// mechanical rather than a guess — and the last two rows are where it stops
// guessing: a name outside the convention is package-level, never attached to
// a symbol that may not exist.
func TestExampleIdent(t *testing.T) {
	for _, tc := range []struct{ name, ident, suffix string }{
		{"Example", "", ""},
		{"ExampleNew", "New", ""},
		{"ExampleBuilder_Grow", "Builder.Grow", ""},
		{"ExampleNew_reuse", "New", "reuse"},
		{"ExampleBuilder_Grow_reuse", "Builder.Grow", "reuse"},
		{"Example_suffix", "", "suffix"},
		{"Example_Unconventional", "", ""},
		{"ExampleA_B_C", "", ""},
	} {
		ident, suffix := exampleIdent(tc.name)
		if ident != tc.ident || suffix != tc.suffix {
			t.Errorf("exampleIdent(%q) = (%q, %q), want (%q, %q)",
				tc.name, ident, suffix, tc.ident, tc.suffix)
		}
	}
}

const widgetExamples = `package widget_test

import (
	"fmt"
	"testing"
)

// Example shows the package.
func Example() {
	fmt.Println("package")
	// Output: package
}

func ExampleNew() {
	fmt.Println("func")
}

func ExampleWidget_Grow() {}

func ExampleWidget_Grow_reuse() {}

func Example_Unconventional() {}

func ExampleNotAnExample(t *testing.T) {}
`

// TestPackageExamplesAreLabelled reads examples the way :doc -examples does,
// off files on disk, and checks each one carries the identifier it belongs to.
func TestPackageExamplesAreLabelled(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "example_test.go"), []byte(widgetExamples), 0o644); err != nil {
		t.Fatal(err)
	}
	// Not a test file, so nothing in it is an example however it is named.
	if err := os.WriteFile(filepath.Join(dir, "widget.go"),
		[]byte("package widget\n\nfunc ExampleDecoy() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pkg, exs, err := packageExamples(dir)
	if err != nil {
		t.Fatal(err)
	}
	if pkg != "widget" {
		t.Errorf("package name is %q, want widget", pkg)
	}
	want := []docExample{
		{Ident: "", Suffix: ""},
		{Ident: "New"},
		{Ident: "Widget.Grow"},
		{Ident: "Widget.Grow", Suffix: "reuse"},
		{Ident: ""},
	}
	if len(exs) != len(want) {
		var got []string
		for _, ex := range exs {
			got = append(got, ex.Ident+"/"+ex.Suffix)
		}
		t.Fatalf("found %d examples (%v), want %d", len(exs), got, len(want))
	}
	for i, w := range want {
		if exs[i].Ident != w.Ident || exs[i].Suffix != w.Suffix {
			t.Errorf("example %d is (%q, %q), want (%q, %q)",
				i, exs[i].Ident, exs[i].Suffix, w.Ident, w.Suffix)
		}
	}
	// The body is sliced out of the source, so the comments that say what an
	// example demonstrates and what it prints come with it.
	if !strings.Contains(exs[0].Src, "// Output: package") ||
		!strings.Contains(exs[0].Src, "// Example shows the package.") {
		t.Errorf("the first example lost its comments:\n%s", exs[0].Src)
	}

	out := renderExamples(pkg, exs)
	for _, want := range []string{
		"// widget — 5 examples",
		"// package widget",
		"// widget.New",
		"// widget.Widget.Grow",
		"// widget.Widget.Grow (reuse)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the rendered examples have no %q line:\n%s", want, out)
		}
	}
}

// TestNoExamplesIsNotUnavailableSources. "Found none" and "did not look" are
// different answers, and collapsing them would have :doc claim a package has
// no examples when what happened is that its sources were never downloaded.
func TestNoExamplesIsNotUnavailableSources(t *testing.T) {
	// A package whose sources are here and hold no examples.
	pkg, exs, err := packageExamples(t.TempDir())
	if err != nil {
		t.Fatalf("an empty directory is not an error: %v", err)
	}
	if len(exs) != 0 || pkg != "" {
		t.Errorf("found %d examples in an empty directory", len(exs))
	}
	// A directory that is not there at all.
	if _, _, err := packageExamples(filepath.Join(t.TempDir(), "gone")); err == nil {
		t.Error("a missing source directory reported as no examples")
	}

	c := testCore(t)
	none := c.Submit(":doc -examples unicode/utf16")
	if none.Err || !strings.Contains(none.Out, "no examples") {
		t.Errorf(":doc -examples unicode/utf16: %q (err=%v), want it to say there are none",
			none.Out, none.Err)
	}
	absent := c.Submit(":doc -examples github.com/gluon/not-a-module")
	if !absent.Err || !strings.Contains(absent.Out, "not available locally") {
		t.Errorf(":doc -examples of an absent module: %q (err=%v), want it to say the sources "+
			"are unavailable", absent.Out, absent.Err)
	}
	if strings.Contains(absent.Out, "no examples") {
		t.Errorf("an absent module was reported as having no examples: %q", absent.Out)
	}
}

// TestDocExamplesReadsRealSources covers the whole path once against the
// standard library, which is the case the flag exists for: strings' examples
// are the documentation, and `go doc` does not show one of them.
func TestDocExamplesReadsRealSources(t *testing.T) {
	c := testCore(t)
	res := c.Submit(":doc -examples strings")
	if res.Err {
		t.Skip("strings' sources are not readable here:", res.Out)
	}
	for _, want := range []string{"// strings — ", "// strings.Builder", "func ExampleBuilder()"} {
		if !strings.Contains(res.Out, want) {
			t.Errorf("strings' examples have no %q:\n%.400s", want, res.Out)
		}
	}
}

// TestDocAddressNamesTheVersionInTheBuildList. An address without a version
// resolves to the latest published, which may not be the code the session is
// running — the same quiet wrongness that made :sql print its arguments rather
// than interpolate them.
func TestDocAddressNamesTheVersionInTheBuildList(t *testing.T) {
	reqs := []string{
		"github.com/google/uuid v1.6.0",
		"github.com/jackc/pgx/v5 v5.5.1",
		"gopkg.in/yaml.v3 v3.0.1",
		"example.com/local v0.0.0-00010101000000-000000000000",
	}
	for _, tc := range []struct {
		arg, url string
		known    bool
	}{
		{"github.com/google/uuid", "https://pkg.go.dev/github.com/google/uuid@v1.6.0", true},
		{"github.com/google/uuid.New", "https://pkg.go.dev/github.com/google/uuid@v1.6.0#New", true},
		{"github.com/google/uuid.UUID.String",
			"https://pkg.go.dev/github.com/google/uuid@v1.6.0#UUID.String", true},
		// A package under the module: pkg.go.dev versions the module, and the
		// package path follows the @version.
		{"github.com/jackc/pgx/v5/pgxpool.New",
			"https://pkg.go.dev/github.com/jackc/pgx/v5@v5.5.1/pgxpool#New", true},
		// A dot in the module's own last element is part of the path, not the
		// start of a symbol — which is why the build list is consulted first.
		{"gopkg.in/yaml.v3.Marshal", "https://pkg.go.dev/gopkg.in/yaml.v3@v3.0.1#Marshal", true},
		// The standard library is in no build list: its version is the
		// toolchain's, which go.mod does not carry.
		{"strings.Builder", "https://pkg.go.dev/strings#Builder", false},
		{"net/http.Client.Do", "https://pkg.go.dev/net/http#Client.Do", false},
		{"net/http", "https://pkg.go.dev/net/http", false},
		// Required only so a replace can point elsewhere: a version in form,
		// and not in fact.
		{"example.com/local.Thing", "https://pkg.go.dev/example.com/local#Thing", false},
		// A module the session does not have.
		{"github.com/nobody/nothing.X", "https://pkg.go.dev/github.com/nobody/nothing#X", false},
	} {
		url, known := docAddress(reqs, tc.arg)
		if url != tc.url || known != tc.known {
			t.Errorf("docAddress(%q) = (%q, %v), want (%q, %v)",
				tc.arg, url, known, tc.url, tc.known)
		}
	}

	// An unattached session has no requirements at all, and still gets an
	// address — with the version reported as unknown.
	if url, known := docAddress(nil, "github.com/google/uuid.New"); known ||
		url != "https://pkg.go.dev/github.com/google/uuid#New" {
		t.Errorf("with no build list: (%q, %v)", url, known)
	}
}

// TestDocURLSaysWhenTheVersionIsUnknown. The address is still produced — a
// version-independent page is the right answer for the standard library — but
// it must not be passed off as pinned to what the session has.
func TestDocURLSaysWhenTheVersionIsUnknown(t *testing.T) {
	c := testCore(t)
	res := c.Submit(":doc -url strings.Builder")
	if res.Err {
		t.Fatalf(":doc -url errored: %s", res.Out)
	}
	if !strings.HasPrefix(res.Out, "https://pkg.go.dev/strings#Builder") {
		t.Errorf(":doc -url strings.Builder = %q", res.Out)
	}
	if !strings.Contains(res.Out, "version is unknown") {
		t.Errorf("the unknown version is not stated: %q", res.Out)
	}
	if res.Modal != nil {
		t.Error("a one-line address opened a full-screen view")
	}
}

// TestDocURLFetchesNothing. -url prints; it does not open a browser, which
// would be a side effect on the user's desktop from a line that is also in the
// history file, and it does not go online, which is :get's alone (constraint
// G). Both are properties of what the handler is allowed to call, so they are
// checked where they are decided.
func TestDocURLFetchesNothing(t *testing.T) {
	for _, fn := range []struct{ file, name string }{
		{"core.go", "func (c *Core) docURL("},
		{"doc.go", "func docAddress("},
		{"doc.go", "func moduleFor("},
		{"doc.go", "func splitDocPath("},
	} {
		src, err := os.ReadFile(fn.file)
		if err != nil {
			t.Fatal(err)
		}
		body := functionBody(t, string(src), fn.name)
		for _, banned := range []string{"exec.", "http.", "browser", "open.Run", "c.ev.Doc", "PackageDir"} {
			if strings.Contains(body, banned) {
				t.Errorf("%s reaches %s; -url builds a string and prints it", fn.name, banned)
			}
		}
	}
}

// TestDocPackageFormResolvesToThePackage. A bare name can be both — `strings`
// is a package and `strings.Builder` a symbol inside it — and go doc's
// resolution order is not obvious from the REPL. -pkg states the intent, and
// answers "unresolved" rather than quietly documenting the other thing.
func TestDocPackageFormResolvesToThePackage(t *testing.T) {
	c := testCore(t)
	// A short name go doc resolves to a package: encoding/json, which
	// `go list json` cannot resolve, so the short form has to survive.
	pkg := c.Submit(":doc -pkg json")
	if pkg.Err {
		t.Skip("go doc json:", pkg.Out)
	}
	if !strings.HasPrefix(pkg.Out, `package json // import "encoding/json"`) {
		t.Errorf(":doc -pkg json did not document the package:\n%.120s", pkg.Out)
	}
	// The same name without the flag is unchanged.
	if bare := c.Submit(":doc json"); bare.Out != pkg.Out {
		t.Errorf(":doc json and :doc -pkg json differ")
	}
	// A symbol is not a package, even though go doc answers about it and even
	// though its answer opens with the same `package strings` header.
	sym := c.Submit(":doc -pkg strings.Builder")
	if !sym.Err || !strings.Contains(sym.Out, "unresolved") {
		t.Errorf(":doc -pkg strings.Builder = %q (err=%v), want it reported unresolved",
			firstLine(sym.Out), sym.Err)
	}
	// And a name that is nothing at all.
	if unknown := c.Submit(":doc -pkg no/such/package"); !unknown.Err ||
		!strings.Contains(unknown.Out, "unresolved") {
		t.Errorf(":doc -pkg no/such/package = %q (err=%v), want it reported unresolved",
			firstLine(unknown.Out), unknown.Err)
	}
}

// TestEveryDocFormBuildsNothing is the tier, checked rather than asserted.
//
// :doc is Static, which is what makes go_doc an MCP tool exposed without
// --eval (invariant 28). Every form has to stay a file read, a `go doc`, or a
// string build — so nothing may render a program, which is the step before a
// build and the one that leaves main.go behind in the session's module.
func TestEveryDocFormBuildsNothing(t *testing.T) {
	c := testCore(t)
	main := filepath.Join(c.ev.Dir(), "main.go")
	if _, err := os.Stat(main); err == nil {
		t.Fatal("the session had already rendered a program before any :doc ran")
	}
	imports := fmt.Sprint(c.ev.Imports())
	for _, line := range []string{
		":doc strings.Count",
		":doc -src strings.Count",
		":doc -examples strings",
		":doc -url strings.Builder",
		":doc -pkg json",
	} {
		c.Submit(line)
		if _, err := os.Stat(main); err == nil {
			t.Errorf("%s rendered a program", line)
			os.Remove(main)
		}
		if len(c.sess.Entries) != 0 {
			t.Errorf("%s added %d entries to the session", line, len(c.sess.Entries))
			c.sess.Reset()
		}
		if got := fmt.Sprint(c.ev.Imports()); got != imports {
			t.Errorf("%s changed the session's imports to %q", line, got)
		}
	}
}

// functionBody is the text of a function declaration in src, for the tests
// that hold what a handler is allowed to reach. It is the shape
// internal/eval's RaceRun test uses: the claim is about the code, so the code
// is what it reads.
func functionBody(t *testing.T, src, decl string) string {
	t.Helper()
	start := strings.Index(src, decl)
	if start < 0 {
		t.Fatalf("%s is gone; this test names the wrong function", decl)
	}
	end := strings.Index(src[start:], "\n}\n")
	if end < 0 {
		t.Fatalf("could not find the end of %s", decl)
	}
	return src[start : start+end]
}
