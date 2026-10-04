package render

import (
	"regexp"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/session"
)

func sessionOf(t *testing.T, srcs ...string) *session.Session {
	t.Helper()
	s := &session.Session{}
	for _, src := range srcs {
		e, err := session.Classify(src)
		if err != nil {
			t.Fatalf("Classify(%q): %v", src, err)
		}
		s.Append(e)
	}
	return s
}

func build(t *testing.T, srcs ...string) string {
	t.Helper()
	s := &session.Session{}
	for _, src := range srcs {
		e, err := session.Classify(src)
		if err != nil {
			t.Fatalf("Classify(%q): %v", src, err)
		}
		s.Append(e)
	}
	out, err := Main(s, nil)
	if err != nil {
		t.Fatalf("Main: %v", err)
	}
	return out
}

// The whole point of the tool: neither of Go's two scratch-hostile rules
// should ever reach the user.
func TestSuppressesUnusedVariable(t *testing.T) {
	got := build(t, "x := 1")
	if !strings.Contains(got, "_ = x") {
		t.Errorf("no unused-variable suppression emitted:\n%s", got)
	}
}

func TestTrailingExpressionIsPrinted(t *testing.T) {
	got := build(t, "x := 1", "x + 1")
	if !strings.Contains(stripDirectives(got), PrintFunc+"(x + 1)") {
		t.Errorf("trailing expression not wrapped for printing:\n%s", got)
	}
}

func TestNoValueExpressionEmittedBare(t *testing.T) {
	s := &session.Session{}
	e, _ := session.Classify("close(ch)")
	e.NoValue = true // what the build failure teaches us
	s.Append(e)
	got, err := Main(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, PrintFunc+"(close(ch))") {
		t.Errorf("no-value call must not be wrapped in a print:\n%s", got)
	}
	if !strings.Contains(got, "close(ch)") {
		t.Errorf("no-value call was dropped entirely:\n%s", got)
	}
}

// Retyping `x := 1` is natural in a REPL but is "no new variables on left
// side of :=" in real Go. :src shows the rewrite, so nothing is hidden.
func TestRedeclarationBecomesAssignment(t *testing.T) {
	got := build(t, "x := 1", "x := 2")
	if strings.Count(got, ":=") != 1 {
		t.Errorf("second := should have become =:\n%s", got)
	}
	if !strings.Contains(got, "x = 2") {
		t.Errorf("expected reassignment:\n%s", got)
	}
}

func TestPartialRedeclarationKeepsDefine(t *testing.T) {
	// `x` exists but `err` does not, which is legal Go; leave it alone.
	got := build(t, "x := 1", "x, err := 2, error(nil)")
	if !strings.Contains(got, "x, err := ") {
		t.Errorf("partial redeclaration must keep :=\n%s", got)
	}
}

// Everything before the newest entry is a replay; its output must be muted at
// the fd level rather than diffed away, or nondeterministic programs (map
// range order, time, rand) produce garbage.
func TestMuteWrapsReplayedPrefix(t *testing.T) {
	got := build(t, `fmt.Println("a")`, "1 + 1")
	stripped := stripDirectives(got)
	mute := strings.Index(stripped, MuteFunc+"()")
	unmute := strings.Index(stripped, UnmuteFunc+"()")
	first := strings.Index(stripped, `fmt.Println("a")`)
	last := strings.Index(stripDirectives(got), PrintFunc+"(1 + 1)")

	if mute < 0 || unmute < 0 {
		t.Fatalf("missing fd gate:\n%s", got)
	}
	if !(mute < first && first < unmute && unmute < last) {
		t.Errorf("gate misplaced: mute=%d first=%d unmute=%d last=%d\n%s",
			mute, first, unmute, last, got)
	}
}

func TestDeclarationsHoistAboveMain(t *testing.T) {
	got := build(t, "type T struct{ X int }", "func (t T) Sum() int { return t.X }", "T{1}.Sum()")
	mainAt := strings.Index(got, "func main()")
	if i := strings.Index(got, "type T struct"); i < 0 || i > mainAt {
		t.Errorf("type must hoist above main (methods require it):\n%s", got)
	}
	if i := strings.Index(got, "func (t T) Sum()"); i < 0 || i > mainAt {
		t.Errorf("method must hoist above main:\n%s", got)
	}
}

// A session of nothing but declarations still needs func main, because a main
// package without it fails at LINK, not compile.
func TestAlwaysEmitsMain(t *testing.T) {
	got := build(t, "type T int")
	if !strings.Contains(got, "func main()") {
		t.Errorf("func main must always be emitted:\n%s", got)
	}
}

func TestImportBlockRenderedFromCache(t *testing.T) {
	s := &session.Session{}
	e, _ := session.Classify("1 + 1")
	s.Append(e)
	got, err := Main(s, []ImportSpec{{Path: "slices"}, {Name: "f", Path: "fmt"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"slices"`) || !strings.Contains(got, `f "fmt"`) {
		t.Errorf("cached imports not rendered:\n%s", got)
	}
}

// A declaration contributes no code to main, so nothing may be unmuted — the
// old behaviour walked back to an earlier entry and replayed its output.
func TestTrailingDeclarationUnmutesNothing(t *testing.T) {
	got := build(t, "1 + 1", "type T int")
	if strings.Contains(got, UnmuteFunc+"()\n\t"+PrintFunc) {
		t.Errorf("declaration must not unmute an earlier entry:\n%s", got)
	}
	if Executes(sessionOf(t, "1 + 1", "type T int")) {
		t.Error("Executes should be false for a trailing declaration")
	}
	if !Executes(sessionOf(t, "type T int", "1 + 1")) {
		t.Error("Executes should be true for a trailing expression")
	}
}

func TestQualifiers(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"slices.Sort(x)", []string{"slices"}},
		{"strings.ToUpper(s)", []string{"strings"}},
		{"x + 1", nil},
		{"t.Mark(a)", []string{"t"}}, // a local; the caller subtracts those
		{"func f() { fmt.Println(1) }", []string{"fmt"}},
	}
	for _, tc := range tests {
		got := Qualifiers(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("Qualifiers(%q) = %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Errorf("Qualifiers(%q) = %v, want %v", tc.in, got, tc.want)
			}
		}
	}
}

func TestCallee(t *testing.T) {
	if got, ok := Callee("slices.Sort(x)"); !ok || got != "slices.Sort" {
		t.Errorf("Callee = %q,%v; want slices.Sort,true", got, ok)
	}
	if got, ok := Callee("delete(m, k)"); !ok || got != "delete" {
		t.Errorf("Callee = %q,%v; want delete,true", got, ok)
	}
	if _, ok := Callee("x + 1"); ok {
		t.Error("Callee should reject a non-call expression")
	}
}

// stripDirectives removes the //line comments so an assertion can be about the
// generated code rather than about position bookkeeping.
func stripDirectives(s string) string {
	return regexp.MustCompile(`/\*line [^*]*\*/ ?`).ReplaceAllString(s, "")
}

// Every entry is preceded by a directive naming its own synthetic file, which
// is what makes the compiler, go/types and panic tracebacks all report
// positions against what the user typed.
func TestLineDirectivesNameTheEntry(t *testing.T) {
	s := &session.Session{}
	s.Append(session.Entry{Kind: session.KindDecl, Src: "type T int"})
	s.Append(session.Entry{Kind: session.KindStmt, Src: "x := 1", Binds: []string{"x"}})
	s.Append(session.Entry{Kind: session.KindExpr, Src: "x + 1"})
	s.Append(session.Entry{Kind: session.KindExpr, Src: "close(ch)", NoValue: true})

	got, err := Main(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"/*line gluon-in-0.go:1:1*/type T int",
		"/*line gluon-in-1.go:1:1*/x := 1",
		// Inside the call, not before it, so a column maps to the user's
		// expression rather than to the wrapper.
		PrintFunc + "(/*line gluon-in-2.go:1:1*/x + 1)",
		"/*line gluon-in-3.go:1:1*/close(ch)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestEntryOf(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
		ok   bool
	}{
		{"gluon-in-0.go", 0, true},
		{"gluon-in-12.go", 12, true},
		{"main.go", 0, false},
		{"gluon-in-.go", 0, false},
		{"gluon-in-x.go", 0, false},
		{"gluon-in-3.txt", 0, false},
	} {
		got, ok := EntryOf(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("EntryOf(%q) = %d,%v; want %d,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// :src and :save show a program a person would write, so the position
// bookkeeping must not appear in it.
func TestDisplayStripsDirectives(t *testing.T) {
	s := &session.Session{}
	s.Append(session.Entry{Kind: session.KindStmt, Src: "x := 1", Binds: []string{"x"}})
	s.Append(session.Entry{Kind: session.KindExpr, Src: "x + 1"})

	raw, err := Main(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := Display(raw)
	if strings.Contains(got, "/*line") {
		t.Errorf("directives survived into the display copy:\n%s", got)
	}
	if !strings.Contains(got, PrintFunc+"(x + 1)") {
		t.Errorf("stripping mangled the code:\n%s", got)
	}
}

// A method body's selector bases are receivers and parameters, not packages.
// Treating them as packages makes the import predictor give up and pay for
// goimports to learn nothing.
func TestUnboundQualifiers(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "a receiver is not a package",
			src:  "func (p Point) Dist() int { return p.X*p.X + p.Y*p.Y }",
			want: nil,
		},
		{
			name: "a parameter is not a package",
			src:  "func area(r Rect) int { return r.W * r.H }",
			want: nil,
		},
		{
			name: "a real package survives",
			src:  `func shout(s string) string { return strings.ToUpper(s) }`,
			want: []string{"strings"},
		},
		{
			name: "a local from := is not a package",
			src:  "b := bytes.Buffer{}; b.WriteString(\"x\")",
			want: []string{"bytes"},
		},
		{
			name: "a range variable is not a package",
			src:  "for _, u := range users { fmt.Println(u.Name) }",
			want: []string{"fmt"},
		},
		{
			name: "a var spec is not a package",
			src:  "var w sync.WaitGroup; w.Add(1)",
			want: []string{"sync"},
		},
		{
			name: "a type switch binding is not a package",
			src:  "switch v := any(1).(type) { case error: _ = v.Error() }",
			want: nil,
		},
		{
			name: "a named result is not a package",
			src:  "func f() (buf bytes.Buffer) { buf.WriteString(\"x\"); return }",
			want: []string{"bytes"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := UnboundQualifiers(tc.src)
			if len(got) != len(tc.want) {
				t.Fatalf("UnboundQualifiers(%q) = %v, want %v", tc.src, got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("UnboundQualifiers(%q) = %v, want %v", tc.src, got, tc.want)
				}
			}
		})
	}
}

// Qualifiers itself must stay unfiltered: it backs the recovery path that
// decides whether a missing name could be fixed by an import, and filtering
// there would turn a resolvable import into a reported error.
func TestQualifiersStaysUnfiltered(t *testing.T) {
	src := "func (p Point) Show() { fmt.Println(p.X) }"
	got := Qualifiers(src)
	var hasP, hasFmt bool
	for _, q := range got {
		hasP = hasP || q == "p"
		hasFmt = hasFmt || q == "fmt"
	}
	if !hasP || !hasFmt {
		t.Errorf("Qualifiers(%q) = %v, want both p and fmt", src, got)
	}
}

func TestPackageName(t *testing.T) {
	cases := map[string]string{
		"strings":            "strings",
		"os/exec":            "exec",
		"github.com/foo/bar": "bar",
		// A major-version suffix is not the package name.
		"github.com/foo/bar/v2":  "bar",
		"github.com/foo/bar/v12": "bar",
		// gopkg.in carries the version on the element itself.
		"gopkg.in/yaml.v3":  "yaml",
		"gopkg.in/check.v1": "check",
		// A dotted element that is not a version cannot be an identifier, so
		// there is no name to guess and goimports keeps the job.
		"example.com/foo.bar": "",
		"":                    "",
		"/":                   "",
		// A single element is a legal import path (the stdlib is full of them),
		// so there is nothing to strip and v2 is the name.
		"v2": "v2",
	}
	for in, want := range cases {
		if got := PackageName(in); got != want {
			t.Errorf("PackageName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestBlankImportSurvivesGoimports pins the assumption :query is built on.
//
// The driver a query needs is named by nobody: sql.Open("postgres", …) takes a
// string, not the package, so the import exists only to run the driver's init.
// If goimports treated that as unused and dropped it, the program would still
// compile and then fail at run time with `sql: unknown driver "postgres"` — an
// error that blames the driver rather than the import that went missing, on a
// line the user did not write.
//
// x/tools/imports does exempt `_` and `.` from unused-removal. This test is
// what says so out loud, and what would catch a toolchain upgrade that changed
// it.
func TestBlankImportSurvivesGoimports(t *testing.T) {
	for _, path := range []string{
		"modernc.org/sqlite",
		// Not in the build list at all, so goimports cannot verify it exists.
		// The unresolvable case is the one that would plausibly be dropped.
		"github.com/lib/pq",
	} {
		src := "package main\n\nimport (\n\t\"database/sql\"\n\t_ \"" + path + "\"\n)\n\n" +
			"func main() {\n\t_, _ = sql.Open(\"x\", \"y\")\n}\n"
		out, err := FixImports("/tmp/gluonspike/main.go", []byte(src))
		if err != nil {
			t.Fatalf("%s: FixImports: %v", path, err)
		}
		if want := `_ "` + path + `"`; !strings.Contains(string(out), want) {
			t.Errorf("goimports dropped the blank import %s:\n%s", path, out)
		}
	}
}
