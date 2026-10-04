package repl

import (
	"slices"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/complete"
)

// suggestMeta is completion as the prompt asks for it, with the Go paths given
// names so a line that falls through to them can be told apart.
func suggestMeta(c *Core, line string) []string {
	return complete.Suggest(line, complete.Context{
		Metas:    c.MetaNames(),
		Names:    []string{"strs", "status"},
		MetaArgs: c.completionMetaArgs,
	})
}

// TestFlagsCompleteAtAFlagPosition: :http reads its flags after the method and
// the URL, so that is where they are offered — with the space a value needs.
func TestFlagsCompleteAtAFlagPosition(t *testing.T) {
	c := &Core{}
	got := suggestMeta(c, ":http GET https://example.com -")
	want := []string{":http GET https://example.com -H ", ":http GET https://example.com -d ",
		":http GET https://example.com -t "}
	if !slices.Equal(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if got := suggestMeta(c, ":http -"); len(got) != 0 {
		t.Errorf("flags were offered where :http reads a method: %q", got)
	}
	if got := suggestMeta(c, ":t -"); !slices.Equal(got, []string{":t -d", ":t -v"}) {
		t.Errorf(":t's flags: %q", got)
	}
}

// TestAFlagIsNotOfferedTwice, except the one that repeats.
func TestAFlagIsNotOfferedTwice(t *testing.T) {
	got := suggestMeta(&Core{}, ":http GET u -d x -H 'a: b' -")
	want := []string{":http GET u -d x -H 'a: b' -H ", ":http GET u -d x -H 'a: b' -t "}
	if !slices.Equal(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if got := suggestMeta(&Core{}, ":doc -src -"); len(got) != 0 {
		t.Errorf("a second :doc mode was offered: %q", got)
	}
}

// TestClosedValuesComplete: a set the command declares, or one the session
// knows, completes as itself.
func TestClosedValuesComplete(t *testing.T) {
	c := withPlugins()
	cases := map[string][]string{
		":watch o":    {":watch off", ":watch on"},
		":guide e":    {":guide ent"},
		":help :pl":   {":help :plugins"},
		":help pl":    {":help plugins"},
		":settings v": {":settings value.depth", ":settings value.form", ":settings value.items"},
	}
	for line, want := range cases {
		got := suggestMeta(c, line)
		for _, w := range want {
			if !slices.Contains(got, w) {
				t.Errorf("%q: %q is not offered in %q", line, w, got)
			}
		}
	}
	if got := suggestMeta(c, ":settings value.form=t"); !slices.Contains(got, ":settings value.form=tree") {
		t.Errorf("key=value did not complete the value: %q", got)
	}
}

// TestAnHTTPMethodCompletesInTheCaseTyped: the parser upper-cases the method,
// so either case is right, and the one offered is the one being typed —
// anything else would not extend the line (invariant 16).
func TestAnHTTPMethodCompletesInTheCaseTyped(t *testing.T) {
	c := &Core{}
	if got := suggestMeta(c, ":http g"); !slices.Equal(got, []string{":http get"}) {
		t.Errorf(":http g → %q", got)
	}
	if got := suggestMeta(c, ":http G"); !slices.Equal(got, []string{":http GET"}) {
		t.Errorf(":http G → %q", got)
	}
}

// TestNothingInsideAQuote: a quoted value is typed, not chosen.
func TestNothingInsideAQuote(t *testing.T) {
	if got := suggestMeta(&Core{}, ":http GET u -H 'Acc"); len(got) != 0 {
		t.Errorf("a quote was completed: %q", got)
	}
}

// TestAGoOperandFallsBackToGo: the meta grammar does not own a Go operand, so
// the Go paths complete it exactly as before.
func TestAGoOperandFallsBackToGo(t *testing.T) {
	c := &Core{}
	if got := suggestMeta(c, ":t str"); !slices.Contains(got, ":t strs") || !slices.Contains(got, ":t struct") {
		t.Errorf(":t str → %q, want the Go completion: names and keywords", got)
	}
	// -st is not a flag :t reads, so it is Go: the negation of st….
	if got := suggestMeta(c, ":t -st"); !slices.Contains(got, ":t -status") {
		t.Errorf(":t -st → %q, want Go's completion of st", got)
	}
}

// TestEveryMetaCandidateExtendsTheLine is invariant 16 over a corpus: textinput
// keeps what was typed and appends the rest, so a candidate that does not
// extend the line corrupts it.
func TestEveryMetaCandidateExtendsTheLine(t *testing.T) {
	c := withPlugins()
	lines := []string{
		":http g", ":http GET u -", ":http GET u -H 'x: y' -", ":bench -", ":bench -count 3 -c",
		":doc -", ":since -", ":since 1.2", ":scratch -", ":use -", ":get -", ":buf -",
		":theme g", ":settings theme.", ":settings value.form t", ":guide ", ":help :",
		":query -", ":replay -", ":reset -", ":save -", ":test -", ":inspect -", ":time o",
	}
	for _, line := range lines {
		for _, cand := range suggestMeta(c, line) {
			if !strings.HasPrefix(cand, line) || cand == line {
				t.Errorf("%q offered %q, which does not extend it", line, cand)
			}
		}
	}
}

// TestValueCachesDropWhereTheyChange: a value set is kept until what it lists
// can have changed — :get and :use for everything, :scratch for the pads.
func TestValueCachesDropWhereTheyChange(t *testing.T) {
	c := withPlugins()
	calls := 0
	fill := func() []string { calls++; return []string{"x"} }
	c.cached(cmdspec.Plugins, fill)
	c.cached(cmdspec.Plugins, fill)
	if calls != 1 {
		t.Errorf("filled %d times for two keystrokes, want once", calls)
	}
	c.invalidateCommands()
	c.cached(cmdspec.Plugins, fill)
	if calls != 2 {
		t.Error("the cache survived :get/:use")
	}
}

// BenchmarkCompleteMetaFlag is the keystroke on a flag position. The whole
// budget for a keystroke is 12-22µs; this has to be a small part of it.
func BenchmarkCompleteMetaFlag(b *testing.B) {
	c := &Core{}
	for i := 0; i < b.N; i++ {
		c.completionMetaArgs(":http GET https://example.com -H 'Accept: x' -")
	}
}

// TestUsageHintShowsWhatIsLeft: typing a command, the rest of its usage sits
// beside the line, beginning with what goes where the cursor is.
func TestUsageHintShowsWhatIsLeft(t *testing.T) {
	c := &Core{}
	cases := map[string]string{
		":http ":               "<method> <url> [-H <header>]... [-d <body>] [-t <duration>]",
		":http GET ":           "<url> [-H <header>]... [-d <body>] [-t <duration>]",
		":http GET u -t ":      "<duration>  how long to wait: 5s, 500ms, 1m (30s by default)",
		":bench -count 3 ":     "[-cpu <list>] [-profile] <exp>[, <other>]",
		":impl bytes.Buffer, ": "<i>  the interface, from anywhere the session can see",
		":q ":                  "",
		":http":                "",
	}
	for line, want := range cases {
		if got := c.Hint(line); got != want {
			t.Errorf("Hint(%q)\n got %q\nwant %q", line, got, want)
		}
	}
}

// TestTheHintIsNeverACandidate: the hint is drawn beside the line and cannot
// be accepted, so it must never reach the suggestion list (invariant 16 by
// construction).
func TestTheHintIsNeverACandidate(t *testing.T) {
	c := &Core{}
	for _, line := range []string{":http ", ":http GET ", ":bench -count 3 "} {
		hint := c.Hint(line)
		for _, cand := range suggestMeta(c, line) {
			if strings.Contains(cand, hint) {
				t.Errorf("%q: the hint %q reached the candidates as %q", line, hint, cand)
			}
		}
	}
}

// BenchmarkMetaHint is the hint's share of a keystroke on a meta command.
func BenchmarkMetaHint(b *testing.B) {
	c := &Core{}
	for i := 0; i < b.N; i++ {
		c.Hint(":http GET https://example.com -H 'Accept: x' ")
	}
}
