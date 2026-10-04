package repl

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sandboxws/gluon/internal/release"
)

func TestSinceReadsFlagsInEitherOrder(t *testing.T) {
	cases := map[string]sinceArgs{
		"":                            {},
		"1.24":                        {version: "1.24"},
		"go1.24":                      {version: "1.24"},
		"1.24 -lang":                  {version: "1.24", mode: sinceLang},
		"-lang 1.24":                  {version: "1.24", mode: sinceLang},
		"-pkg maps":                   {mode: sincePkg, pkg: "maps"},
		"1.21 -pkg maps":              {version: "1.21", mode: sincePkg, pkg: "maps"},
		"1.23 -run range-over-func":   {version: "1.23", mode: sinceRun, run: "range-over-func"},
		"-run range-over-func go1.23": {version: "1.23", mode: sinceRun, run: "range-over-func"},
	}
	for arg, want := range cases {
		got, err := parseSinceArgs(arg)
		if err != nil {
			t.Errorf("%q: %v", arg, err)
			continue
		}
		if got != want {
			t.Errorf("parseSinceArgs(%q) = %+v, want %+v", arg, got, want)
		}
	}
}

// Two modes named is a line whose author expected something, and picking one
// silently answers a question nobody asked. parseDocArgs' rule.
func TestSinceRefusesAnAmbiguousLine(t *testing.T) {
	for _, arg := range []string{
		"-lang -pkg maps",
		"-pkg maps -run x",
		"1.23 1.24",
		"-nope",
		"-pkg",
		"-run",
		"-run range-over-func",
	} {
		if _, err := parseSinceArgs(arg); err == nil {
			t.Errorf("parseSinceArgs(%q) was accepted", arg)
		}
	}
}

func TestSinceReportsUsageOnABadLine(t *testing.T) {
	c := testCore(t)
	res := c.since("-nope")
	if !res.Err || !strings.Contains(res.Out, "usage:") {
		t.Errorf("Err = %v, Out = %q, want an error naming usage:", res.Err, res.Out)
	}
}

// :since takes no required argument, so a bare invocation is a listing rather
// than a usage error — the opposite of what Arg containing "<" would mean.
func TestABareSinceLists(t *testing.T) {
	if _, err := releases(); err != nil {
		// A toolchain fetched by GOTOOLCHAIN ships without $GOROOT/api.
		t.Skip("no toolchain release data:", err)
	}
	c := testCore(t)
	res := c.since("")
	if res.Err {
		t.Fatalf("bare :since errored: %s", res.Out)
	}
	if res.Modal == nil || len(res.Modal.Rows) == 0 {
		t.Fatal("bare :since built no table")
	}
	if !strings.Contains(res.Out, "newest first") {
		t.Errorf("Out does not say the order:\n%s", res.Out)
	}
}

// Invariant 19: Out carries in linear form everything the view shows, so a
// pipe, gluon -e and the MCP tool lose nothing by not being able to open a row.
func TestOutCarriesWhatTheViewWouldShow(t *testing.T) {
	c := testCore(t)
	rels, err := releases()
	if err != nil {
		t.Skip("no toolchain release data:", err)
	}
	r, ok := release.Find(rels, "1.23")
	if !ok {
		t.Skip("this toolchain does not describe 1.23")
	}
	res := c.since("1.23")
	if res.Modal == nil {
		t.Fatal("no modal")
	}
	for _, e := range res.Modal.Entries {
		if e.Title == "" {
			t.Error("an entry has no title")
		}
	}
	if len(res.Modal.Entries) != len(res.Modal.Rows) {
		t.Fatalf("%d entries for %d rows — the driver pairs them by index",
			len(res.Modal.Entries), len(res.Modal.Rows))
	}
	// Every package the table names, and every note, must be in Out too.
	for _, pkg := range r.Packages() {
		if !strings.Contains(res.Out, pkg) {
			t.Errorf("Out never mentions %s, which the table has a row for", pkg)
		}
	}
	for _, n := range r.Lang {
		if !strings.Contains(res.Out, n.Title) || !strings.Contains(res.Out, n.Snippet) {
			t.Errorf("Out is missing %s or its snippet", n.Title)
		}
	}
}

// Invariant 19's other half: a choice hands the model a line to submit, never a
// callback. The line has to be one :since itself would accept.
func TestARunChoiceHandsBackATypeableCommand(t *testing.T) {
	c := testCore(t)
	res := c.since("1.23")
	if res.Modal == nil {
		t.Skip("no release data")
	}
	var found int
	for _, e := range res.Modal.Entries {
		for _, ch := range e.Choices {
			found++
			if !strings.HasPrefix(ch.Run, ":since ") {
				t.Errorf("choice runs %q, which is not a :since line", ch.Run)
				continue
			}
			if _, err := parseSinceArgs(strings.TrimPrefix(ch.Run, ":since ")); err != nil {
				t.Errorf("choice runs %q, which :since itself refuses: %v", ch.Run, err)
			}
		}
	}
	if found == 0 {
		t.Skip("no runnable note on this toolchain")
	}
}

// The two ways to be short of a directive have different fixes, so the refusal
// names which number is short rather than leaving the compiler to report it
// against a temp path the reader never wrote.
func TestANoteAboveTheSessionsDirectiveIsRefused(t *testing.T) {
	c := testCore(t)
	n := release.Note{Title: "a feature from the future", Needs: "9.99", Below: "error"}
	why := c.cannotRun(n)
	if why == "" {
		t.Fatal("a note needing go 9.99 was not refused")
	}
	for _, want := range []string{"9.99", "a feature from the future"} {
		if !strings.Contains(why, want) {
			t.Errorf("refusal does not name %q: %s", want, why)
		}
	}
	if lang := c.ev.Lang(); lang != "" && !strings.Contains(why, lang) {
		t.Errorf("refusal does not name the directive in force (%s): %s", lang, why)
	}
}

// A change no directive gates is a question about the toolchain. Refusing it on
// the strength of the session's go line would name the wrong thing to fix — and
// under an older host it must not be refused at all, because it really does run.
func TestAToolchainOnlyNoteIsJudgedAgainstTheToolchain(t *testing.T) {
	c := testCore(t)
	future := release.Note{Title: "later inference", Needs: "9.99", Below: "toolchain only"}
	why := c.cannotRun(future)
	if why == "" {
		t.Fatal("a note needing the go 9.99 toolchain was not refused")
	}
	if !strings.Contains(why, "no go directive gates it") {
		t.Errorf("the refusal blames a directive that is not the problem: %s", why)
	}
	if tc := c.ev.Toolchain(); tc != "" && !strings.Contains(why, tc) {
		t.Errorf("the refusal does not name the toolchain (%s): %s", tc, why)
	}
	past := release.Note{Title: "old inference", Needs: "1.0", Below: "toolchain only"}
	if why := c.cannotRun(past); why != "" {
		t.Errorf("a note needing the go 1.0 toolchain was refused: %s", why)
	}
}

func TestANoteAtOrBelowTheDirectiveIsNotRefused(t *testing.T) {
	c := testCore(t)
	lang := c.ev.Lang()
	if lang == "" {
		t.Skip("no toolchain")
	}
	if why := c.cannotRun(release.Note{Title: "x", Needs: "1.0"}); why != "" {
		t.Errorf("a note needing go 1.0 was refused: %s", why)
	}
}

// A dash means nobody wrote notes; "none" would mean someone looked and found
// nothing. Reporting the second on the strength of the first is the one thing
// this column must not do.
func TestTheLanguageCellSeparatesUnwrittenFromEmpty(t *testing.T) {
	twoGates := []release.Gate{{Version: "1.30", Feature: "a"}, {Version: "1.30"}}
	cases := []struct {
		name string
		r    release.Release
		want string
	}{
		{"nobody looked, nothing gated", release.Release{}, "—"},
		{"nobody looked, checker gates two", release.Release{Gates: twoGates}, "2 gated"},
		{"looked, found none", release.Release{Curated: true}, "none"},
		{"one note", release.Release{Curated: true, Lang: []release.Note{{}}}, "1 change"},
		{"two", release.Release{Curated: true, Lang: []release.Note{{}, {}}}, "2 changes"},
		// A curated release says what it was written up as, not what the
		// checker gates: the notes are the better account of the same changes.
		{"curated wins over gates", release.Release{
			Curated: true, Lang: []release.Note{{}}, Gates: twoGates}, "1 change"},
	}
	for _, c := range cases {
		if got := langCell(c.r); got != c.want {
			t.Errorf("%s: langCell = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestTheListMarksTheSessionsDirective(t *testing.T) {
	if got := markCurrent("1.24", "1.24"); got != "1.24 ←" {
		t.Errorf("markCurrent = %q, want the marker", got)
	}
	if got := markCurrent("1.23", "1.24"); got != "1.23" {
		t.Errorf("markCurrent = %q, want no marker", got)
	}
	if got := markCurrent("1.23", ""); got != "1.23" {
		t.Errorf("with no directive known, markCurrent = %q, want no marker", got)
	}
}

// godebug.md's links are written for go.dev, so printed as they stand they name
// a path on no machine. Only the target moves; every other byte is the file's.
func TestGodebugLinksAreMadeAbsoluteAndNothingElseMoves(t *testing.T) {
	in := "Go 1.23 changed [`os.Lstat`](/pkg/os#Lstat) and [x](https://example.com/y)."
	want := "Go 1.23 changed [`os.Lstat`](https://go.dev/pkg/os#Lstat) and [x](https://example.com/y)."
	if got := goDevLinks(in); got != want {
		t.Errorf("goDevLinks:\n got %q\nwant %q", got, want)
	}
}

// A table cell is a summary; the row still opens onto the paragraph as written.
func TestAGodebugSummaryIsOneReadableLine(t *testing.T) {
	in := "Go 1.23 changed the behavior of\n[`tls.X509KeyPair`](/pkg/crypto/tls#X509KeyPair) and more words here to push it well past the column."
	got := godebugSummary(in)
	if strings.ContainsAny(got, "\n`[]") {
		t.Errorf("summary keeps markdown or newlines: %q", got)
	}
	if !strings.HasPrefix(got, "Go 1.23 changed the behavior of tls.X509KeyPair") {
		t.Errorf("summary lost the joined wrapping: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("a truncated summary must say so: %q", got)
	}
}

func TestKindSummaryIsOrderedTheSameWayEveryTime(t *testing.T) {
	syms := []release.Symbol{
		{Decl: "const A = 1"}, {Decl: "type T struct"}, {Decl: "func F()"},
		{Decl: "method (*T) M()"}, {Decl: "func G()"},
	}
	if got, want := kindSummary(syms), "2 func, 1 method, 1 type, 1 const"; got != want {
		t.Errorf("kindSummary = %q, want %q", got, want)
	}
}

// The version in a note means a different thing for each kind, and the column
// has to say which — "needs go 1.27" on a change no directive gates would send
// a reader to edit a go.mod line that was never in the way.
func TestNeedsCellSaysWhatTheVersionMeans(t *testing.T) {
	cases := []struct {
		n    release.Note
		want string
	}{
		{release.Note{Needs: "1.27", Below: "error"}, "needs go 1.27"},
		{release.Note{Needs: "1.22", Below: "behaves differently"}, "go 1.22, or it means something else"},
		{release.Note{Needs: "1.27", Below: "toolchain only"}, "go 1.27 toolchain; no directive"},
	}
	for _, c := range cases {
		if got := needsCell(c.n); got != c.want {
			t.Errorf("needsCell(%q) = %q, want %q", c.n.Below, got, c.want)
		}
	}
}

func TestPluralsHandlesAWordThatIsNotJustAnS(t *testing.T) {
	if got := plurals(1, "entry", "entries"); got != "entry" {
		t.Errorf("got %q", got)
	}
	if got := plurals(9, "entry", "entries"); got != "entries" {
		t.Errorf("got %q", got)
	}
}

// The manual half of invariant 19, driven rather than described: a :since view
// enters the alt screen, says nothing while it is up, and leaves exactly one
// line behind. The generic modal tests hold the rule; this holds that :since's
// own spec satisfies it, including the run choice being reachable.
func TestTheSinceViewObeysTheModalRule(t *testing.T) {
	c := testCore(t)
	res := c.since("1.23")
	if res.Modal == nil {
		t.Skip("no release data")
	}
	m := newTestModel(t)
	m.winW, m.winH = 100, 30
	next, cmd := m.Update(resultMsg(res))
	m = next.(model)
	if m.modal == nil {
		t.Fatal(":since did not open a modal")
	}
	if !hasType(flatten(cmd), "altscreen") {
		t.Error("opening did not enter the alt screen")
	}
	if lines := printedLines(flatten(cmd)); len(lines) != 0 {
		t.Errorf("printed into the alt screen, where tea.Println is a no-op: %q", lines)
	}
	for _, k := range []string{"j", "j", "k"} {
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
		m = next.(model)
		if lines := printedLines(flatten(cmd)); len(lines) != 0 {
			t.Errorf("printed while the view was open: %q", lines)
		}
	}
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = next.(model)
	if m.modal != nil {
		t.Fatal("q did not close the view")
	}
	lines := printedLines(flatten(cmd))
	if len(lines) != 1 {
		t.Fatalf("want one summary line in scrollback, got %d: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "go 1.23") || !strings.Contains(lines[0], "browsed") {
		t.Errorf("the line left behind does not name what was browsed: %q", lines[0])
	}
}
