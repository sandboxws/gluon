package docgen

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/syntax"
)

func TestScriptReadsEveryKindOfStep(t *testing.T) {
	s, err := ParseScript(strings.Join([]string{
		"# a comment",
		"%host shop",
		"@ x := 1",
		"x + 1",
		"%clip 3",
		":doc -examples strings",
		"<<<",
		"func f() int {",
		"\treturn 1",
		"}",
		">>>",
		"<<< paste",
		"a := 1",
		"a + 1",
		">>>",
		"%sh touch x.go",
		"$ gluon scratch",
		":buf",
		"%editor",
		"<<<",
		"greet()",
		">>>",
		"",
	}, "\n"), false)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"host shop"}; !reflect.DeepEqual(s.Directives, want) {
		t.Errorf("directives = %q, want %q", s.Directives, want)
	}
	want := []Step{
		{Src: "x := 1", Hidden: true},
		{Src: "x + 1"},
		{Src: ":doc -examples strings", Clip: 3},
		{Src: "func f() int {\n\treturn 1\n}"},
		{Src: "a := 1\na + 1", Paste: true},
		{Src: "touch x.go", Hidden: true, Shell: true},
		{Src: "gluon scratch", Shell: true},
		{Src: ":buf", Editor: "greet()", HasEditor: true},
	}
	if !reflect.DeepEqual(s.Steps, want) {
		t.Errorf("steps =\n%#v\nwant\n%#v", s.Steps, want)
	}
}

func TestAnUnclosedBlockIsAnError(t *testing.T) {
	for _, bad := range []string{
		"<<<\nfunc f() {\n",
		"%clip many\nx\n",
		"%editor\n<<<\nx\n>>>\n",
		":buf\n%editor\n:hist\n",
		":buf\n%editor\n",
	} {
		if _, err := ParseScript(bad, false); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestATranscriptReadsBackAsItWasWritten(t *testing.T) {
	entries := []Entry{
		{In: "x := []int{3, 1, 2}"},
		{In: "x", Out: "([]int) [3 1 2]  len=3 cap=3"},
		{In: "func f(a int) int {\n\treturn a + bogus\n}", Out: "error: undefined: bogus\n\treturn a + bogus\n\t           ^", Err: true},
		{In: ":src", Out: "package main\n\nfunc main() {\n}", Lang: syntax.Go, View: true, Clip: 2},
		{In: ":time on", Out: "timing on"},
		{In: ":buf", Editor: "func greet() string {\n\n\treturn \"hi\"\n}\ngreet()", Out: "(string) \"hi\"  len=2"},
		{In: "gluon -e ':hist'", Shell: true, Out: "gluon> is not a prompt here\n$ nor this\n!view nor this"},
		{In: "cat <<EOF\nx\nEOF", Shell: true, Out: "\nthe first line was empty"},
	}
	got := ParseTranscript(FormatTranscript(entries))
	if !reflect.DeepEqual(got, entries) {
		t.Errorf("read back\n%#v\nwant\n%#v", got, entries)
	}
}

func TestAShellScriptIsCommands(t *testing.T) {
	s, err := ParseScript("# setup\n@ mkdir -p x\ngluon -e '1 + 1'\n", true)
	if err != nil {
		t.Fatal(err)
	}
	want := []Step{{Src: "mkdir -p x", Hidden: true, Shell: true}, {Src: "gluon -e '1 + 1'", Shell: true}}
	if !reflect.DeepEqual(s.Steps, want) {
		t.Errorf("steps = %#v", s.Steps)
	}
}

func TestASessionShowsWhatWasClipped(t *testing.T) {
	root := t.TempDir()
	write(t, root, "site/sessions/long.out", FormatTranscript([]Entry{
		{In: ":since", Out: "one\ntwo\nthree\nfour\nfive", Clip: 2},
	}))
	h, err := NewTracker().session(root, "long", Page{Path: "guide/x.html"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{">one<", ">two<", "… 3 more lines"} {
		if !strings.Contains(string(h), want) {
			t.Errorf("the page has no %q:\n%s", want, h)
		}
	}
	if strings.Contains(string(h), ">three<") {
		t.Errorf("a clipped line was shown:\n%s", h)
	}
}

func TestOnlyAGuideCountsAsWorkingACommand(t *testing.T) {
	root := t.TempDir()
	write(t, root, "site/sessions/s.out", FormatTranscript([]Entry{{In: ":t x", Out: "int"}, {In: ":type x", Out: "int"}}))
	tr := NewTracker()
	if _, err := tr.session(root, "s", Page{Path: "reference/commands.html", Section: "Reference"}); err != nil {
		t.Fatal(err)
	}
	if len(tr.Worked[":t"]) != 0 {
		t.Errorf("a reference page counted: %v", tr.Worked)
	}
	if _, err := tr.session(root, "s", Page{Path: "guide/inspecting.html", Section: "Guides"}); err != nil {
		t.Fatal(err)
	}
	// :type is :t's alias, so both lines count once, for :t.
	if got := tr.Worked[":t"]; !reflect.DeepEqual(got, []string{"guide/inspecting.html"}) {
		t.Errorf("Worked[:t] = %v", got)
	}
}

// write puts a file under root, making its directory.
func write(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestARewrittenTableStaysAligned(t *testing.T) {
	long := "/var/folders/9b/T/TestRecord123/001/shop"
	in := "attached at " + long + "\n" +
		table([][2]string{{"key", "value"}, {"path", long + "/data/shop.db"}, {"addr", "127.0.0.1:8765"}}) + "\n" +
		table([][2]string{{"a", ""}})
	want := "attached at ~/src/shop\n" +
		table([][2]string{{"key", "value"}, {"path", "~/src/shop/data/shop.db"}, {"addr", "127.0.0.1:8765"}}) + "\n" +
		table([][2]string{{"a", ""}})
	if got := Rewrite(in, [][2]string{{long, "~/src/shop"}}); got != want {
		t.Errorf("rewritten:\n%s\nwant:\n%s", got, want)
	}
}

// table draws rows the way gluon's tables are drawn: rounded, a rule under
// the first row, one space of padding each side of a cell.
func table(rows [][2]string) string {
	w := [2]int{}
	for _, r := range rows {
		for i, c := range r {
			if n := len([]rune(c)); n > w[i] {
				w[i] = n
			}
		}
	}
	rule := func(l, m, r string) string {
		return l + strings.Repeat("─", w[0]+2) + m + strings.Repeat("─", w[1]+2) + r
	}
	cell := func(c string, i int) string { return " " + c + strings.Repeat(" ", w[i]-len([]rune(c))) + " " }
	lines := []string{rule("╭", "┬", "╮")}
	for i, r := range rows {
		lines = append(lines, "│"+cell(r[0], 0)+"│"+cell(r[1], 1)+"│")
		if i == 0 && len(rows) > 1 {
			lines = append(lines, rule("├", "┼", "┤"))
		}
	}
	return strings.Join(append(lines, rule("╰", "┴", "╯")), "\n")
}
