package repl

import (
	"github.com/sandboxws/gluon/internal/cmdspec"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/sandboxws/gluon/internal/syntax"
	"github.com/sandboxws/gluon/internal/ui"
)

// litPalette is literal escape strings. A palette derived from a ui.Theme
// renders nothing under `go test` — lipgloss resolves its profile from
// os.Stdout, which is never a terminal there — so a Theme-derived palette would
// make every assertion below pass whatever this package did.
func litPalette() syntax.Palette {
	var p syntax.Palette
	p.Close = "\x1b[0m"
	for r := syntax.RoleNone + 1; r < syntax.NumRoles; r++ {
		p.Open[r] = "\x1b[" + string(rune('0'+int(r))) + "1m"
	}
	return p
}

func stripEsc(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j < len(s) {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// withAttributes gives lipgloss a profile that emits escapes, for the tests
// about the cursor. The insert cursor is an underline, and an underline is an
// attribute: under the Ascii profile a test binary's stdout resolves to,
// lipgloss drops it along with every colour, so without this there is nothing
// on the line to assert about.
func withAttributes(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	lipgloss.SetColorProfile(termenv.TrueColor)
}

// underlined is the text drawn while SGR 4 is on: the cell the insert cursor
// marks, and nothing else.
//
// Reading the state rather than searching for a literal ESC[4m, because termenv
// joins a style into one sequence — the underline arrives as `4` among the
// colour's parameters when the character under the cursor is painted, and as
// `4` alone when it is not.
func underlined(s string) string {
	var b strings.Builder
	on := false
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j < len(s) && s[j] == 'm' {
				for _, param := range strings.Split(s[i+2:j], ";") {
					switch param {
					case "4":
						on = true
					case "24", "0", "":
						on = false
					}
				}
			}
			i = j + 1
			continue
		}
		if on {
			b.WriteByte(s[i])
		}
		i++
	}
	return b.String()
}

// withPalette installs a painting palette for the duration of a test. The
// package-level syntaxPal is what echoOf reads.
func withPalette(t *testing.T) syntax.Palette {
	t.Helper()
	prev := syntaxPal
	t.Cleanup(func() { syntaxPal = prev })
	syntaxPal = litPalette()
	return syntaxPal
}

func testInput(t *testing.T, value string, pos int) textinput.Model {
	t.Helper()
	in := textinput.New()
	in.CharLimit = 0
	in.ShowSuggestions = true
	in.Focus()
	in.SetValue(value)
	in.SetCursor(pos)
	return in
}

func colourOpts(pending []string) inputOpts {
	return inputOpts{Theme: ui.Theme{Colour: true}, Pal: litPalette(), Pending: pending}
}

// TestEchoStripsToTheTypedLine is the invariant: colour never changes what is
// on screen. It is the same property internal/syntax holds, asserted at the
// place a user would actually notice it breaking.
func TestEchoStripsToTheTypedLine(t *testing.T) {
	withPalette(t)
	for _, tc := range []struct {
		name    string
		pending []string
		line    string
	}{
		{"go", nil, `x := "hello"`},
		{"meta with a go argument", nil, ":t http.Handler"},
		{"meta with a sql argument", nil, ":query SELECT * FROM users"},
		{"meta with a path argument", nil, ":use ./some/dir"},
		{"unknown meta command", nil, ":foo bar"},
		{"bare colon", nil, ": x"},
		{"indented", nil, "    y()"},
		{"continuation of a raw string", []string{"s := `a"}, "b`"},
		{"continuation of a block", []string{"for i := range 3 {"}, "\tfmt.Println(i)"},
		{"tabs", nil, "\tif x {"},
		{"half-typed string", nil, `s := "abc`},
		{"unicode", nil, `π := "世界"`},
		{"empty", nil, ""},
	} {
		got := stripEsc(echoOf(tc.pending, tc.line))
		if got != tc.line {
			t.Errorf("%s: echo stripped to %q, want %q", tc.name, got, tc.line)
		}
	}
}

// TestEchoIsPaintedAtAll: without this, every strip-based assertion above
// passes trivially when highlighting has silently stopped working.
func TestEchoIsPaintedAtAll(t *testing.T) {
	withPalette(t)
	for _, line := range []string{`x := "s"`, ":query SELECT 1", "// a comment"} {
		if !strings.ContainsRune(echoOf(nil, line), 0x1b) {
			t.Errorf("%q was echoed with no colour at all", line)
		}
	}
}

// TestEchoIsPlainWithoutAPalette is the NO_COLOR and pipe contract.
func TestEchoIsPlainWithoutAPalette(t *testing.T) {
	prev := syntaxPal
	t.Cleanup(func() { syntaxPal = prev })
	syntaxPal = syntax.Palette{}
	if got := echoOf(nil, `x := "s"`); got != `x := "s"` {
		t.Errorf("an unpainted palette still changed the echo: %q", got)
	}
}

// TestMetaCommandIsNotGo pins the dispatch: the command word is gluon's own
// keyword, and its argument belongs to whatever the command takes.
func TestMetaCommandIsNotGo(t *testing.T) {
	p := litPalette()
	for _, tc := range []struct{ name, line, want string }{
		{"the command word is a keyword", ":t x",
			"keyword(:t) none( ) ident(x)"},
		{"a sql argument", ":query SELECT 1",
			"keyword(:query) none( ) keyword(SELECT) none( ) number(1)"},
		{"a path argument stays plain", ":use ./x",
			"keyword(:use) none( ./x)"},
		{"indentation is kept verbatim", "  :ls",
			"none(  ) keyword(:ls)"},
		{"an unknown command still reads as one", ":nope x",
			"keyword(:nope) none( ) ident(x)"},
	} {
		if got := runTrace(lineRuns(nil, tc.line)); got != tc.want {
			t.Errorf("%s\n  got  %s\n  want %s", tc.name, got, tc.want)
		}
	}
	_ = p
}

// TestColonInsideAConstructIsGo: submitLine joins pending before Core
// classifies, so `:x` as the second line of a block is not a meta command.
func TestColonInsideAConstructIsGo(t *testing.T) {
	got := runTrace(lineRuns([]string{"switch x {"}, "case 1:"))
	if strings.HasPrefix(got, "keyword(:") {
		t.Errorf("a continuation line was read as a meta command: %s", got)
	}
}

// TestContinuationIsTokenizedInContext: a line closing a raw string opened
// above is string content, and a scanner starting at the line alone would call
// it two identifiers.
func TestContinuationIsTokenizedInContext(t *testing.T) {
	got := runTrace(lineRuns([]string{"s := `hello"}, "world`"))
	want := "string(world`)"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func runTrace(runs []run) string {
	var parts []string
	for _, r := range runs {
		parts = append(parts, r.role.String()+"("+r.text+")")
	}
	return strings.Join(parts, " ")
}

// TestRunsTileTheLine is what makes the cursor placement total: every byte is
// covered exactly once, so the cursor is always inside exactly one run.
func TestRunsTileTheLine(t *testing.T) {
	for _, tc := range []struct {
		pending []string
		line    string
	}{
		{nil, `x := "hello"`}, {nil, ":query SELECT 1"}, {nil, "\t\tfoo()"},
		{[]string{"s := `a"}, "b`"}, {nil, "π := 1"}, {nil, ":use ./x"},
		{nil, `s := "abc`}, {nil, "/* open"},
	} {
		var b strings.Builder
		for _, r := range lineRuns(tc.pending, tc.line) {
			b.WriteString(r.text)
		}
		if b.String() != tc.line {
			t.Errorf("runs for %q reconstruct %q", tc.line, b.String())
		}
	}
}

// TestInputViewShowsTheLineExactly is the live-typing half of the same
// invariant, at every cursor position of every line.
func TestInputViewShowsTheLineExactly(t *testing.T) {
	for _, line := range []string{
		`x := "hello"`, ":query SELECT 1", "for i := range 3 {", `s := "abc`,
		"  if x {", "π := 1", "len(s)",
	} {
		for pos := 0; pos <= len([]rune(line)); pos++ {
			in := testInput(t, line, pos)
			got := stripEsc(inputView(in, colourOpts(nil)))
			want := in.Prompt + line
			if pos == len([]rune(line)) {
				want += " " // the trailing space textinput draws the cursor on
			}
			if got != want {
				t.Fatalf("%q at %d: view stripped to %q, want %q", line, pos, got, want)
			}
		}
	}
}

// TestInputViewIsPaintedAtAll, again so the strip assertions cannot pass
// vacuously.
func TestInputViewIsPaintedAtAll(t *testing.T) {
	in := testInput(t, `x := "hello"`, 4)
	if !strings.ContainsRune(inputView(in, colourOpts(nil)), 0x1b) {
		t.Error("the input line was rendered with no colour at all")
	}
}

// TestPlainThemeRendersExactlyLikeTextinput is the degradation guarantee, and
// it is total: not "renders the same", but literally upstream's own function.
func TestPlainThemeRendersExactlyLikeTextinput(t *testing.T) {
	for _, line := range []string{"", `x := 1`, ":query SELECT 1", "  foo()"} {
		for _, pos := range []int{0, 1, len([]rune(line))} {
			if pos > len([]rune(line)) {
				continue
			}
			in := testInput(t, line, pos)
			plain := inputOpts{Theme: ui.Theme{Colour: false}, Pal: litPalette()}
			if got, want := inputView(in, plain), in.View(); got != want {
				t.Errorf("%q at %d: got %q, want textinput's own %q", line, pos, got, want)
			}
		}
	}
}

// TestInputViewFallsBackWhenWidthIsSet: Rule F. gluon never sets Width, so
// there is no scroll window to reproduce — and if someone sets one later they
// get upstream's, uncoloured, rather than a widget that ignores it.
func TestInputViewFallsBackWhenWidthIsSet(t *testing.T) {
	in := testInput(t, "x := 1", 3)
	in.Width = 4
	if got, want := inputView(in, colourOpts(nil)), in.View(); got != want {
		t.Errorf("a widthed input was not handed back to textinput")
	}
}

// TestInputWidthIsUnset documents the coupling the test above relies on.
func TestInputWidthIsUnset(t *testing.T) {
	if w := newModel(nil, "test").in.Width; w != 0 {
		t.Errorf("the REPL input has Width %d; inputView assumes no scroll window", w)
	}
}

// TestGhostIsDimAndNotTokenized: a suggestion is not code the user wrote, and
// painting it as code would make it look typed.
func TestGhostIsDimAndNotTokenized(t *testing.T) {
	in := testInput(t, "strings.To", 10)
	in.SetSuggestions([]string{"strings.ToUpper"})
	view := inputView(in, colourOpts(nil))
	got := stripEsc(view)
	if !strings.HasSuffix(got, "Upper") {
		t.Fatalf("the ghost is missing: %q", got)
	}
	p := litPalette()
	if strings.Contains(view[strings.Index(view, "To")+2:], p.Open[syntax.RoleIdent]) {
		t.Error("the ghost was tokenized")
	}
}

// TestGhostRefusesACaseMismatch is invariant 16 asserted at the widget: a
// candidate that only matched case-insensitively would preview a line that
// accepting it does not produce.
func TestGhostRefusesACaseMismatch(t *testing.T) {
	in := testInput(t, "strings.tou", 11)
	in.SetSuggestions([]string{"strings.ToUpper"})
	if got := stripEsc(inputView(in, colourOpts(nil))); got != in.Prompt+"strings.tou " {
		t.Errorf("a case-mismatched suggestion was previewed: %q", got)
	}
}

// TestCursorKeepsItsTokenColour: the run is reopened after the cursor, so the
// rest of a string literal stays a string rather than falling back to default.
func TestCursorKeepsItsTokenColour(t *testing.T) {
	p := litPalette()
	in := testInput(t, `s := "hello"`, 7) // inside the literal
	view := inputView(in, colourOpts(nil))
	open := p.Open[syntax.RoleString]
	if strings.Count(view, open) < 2 {
		t.Errorf("the string run was not reopened after the cursor:\n%q", view)
	}
}

// TestInputValueNeverHoldsATab records why the input view does not have to deal
// with tabs, and why the echo path does. textinput.SetValue runs bubbles'
// runeutil sanitizer over what it is given, so a tab never reaches Value() —
// and Tab is the completion key anyway. A pasted line does not go through
// SetValue, which is why TestEchoStripsToTheTypedLine covers tabs and this
// does not.
func TestInputValueNeverHoldsATab(t *testing.T) {
	in := testInput(t, "\tif x {", 0)
	if strings.ContainsRune(in.Value(), '\t') {
		t.Skip("textinput no longer sanitizes tabs; the input view now has to preserve them")
	}
}

// TestPaintingFollowsTheRegistry: an argument is painted as what its command
// declares it takes, under every name the command answers to. The lists this
// replaced went stale silently when a command was renamed; a map built from the
// registry cannot, so what is left to pin is the reading of it.
func TestPaintingFollowsTheRegistry(t *testing.T) {
	for _, cmd := range builtins {
		for _, n := range append([]string{cmd.Name}, cmd.Aliases...) {
			if got := paintKind[n]; got != cmd.Usage.Kind {
				t.Errorf("%s paints its argument as %s, and %s declares %s",
					n, got, cmd.Name, cmd.Usage.Kind)
			}
		}
	}
	for _, n := range []string{":query", ":qq"} {
		if paintKind[n] != cmdspec.SQL {
			t.Errorf("%s reaches :query but its argument is not painted as SQL", n)
		}
	}
	// A path's slashes are not division, and a URL's colon is not a label.
	runs, _ := metaSplit(nil, ":use ./internal/api")
	if len(runs) != 2 || runs[1].role != syntax.RoleNone {
		t.Errorf(":use's directory was tokenized: %+v", runs)
	}
	// A name gluon has never heard of is painted as Go, the zero Kind: a
	// plugin command's argument usually is Go, and c.extra is not safe here.
	runs, _ = metaSplit(nil, ":nosuch x+1")
	if len(runs) < 3 {
		t.Errorf("an unknown command's argument was not tokenized as Go: %+v", runs)
	}
}

// hintOpts is a coloured line with a signature beside it, at a given terminal
// width.
func hintOpts(hint string, width int) inputOpts {
	o := colourOpts(nil)
	o.Hint, o.Width = hint, width
	return o
}

// TestHintIsDrawnBesideTheLine: the signature appears after the cursor, and
// after the line the user typed rather than inside it.
func TestHintIsDrawnBesideTheLine(t *testing.T) {
	withPalette(t)
	const sig = "strings.Repeat([s string], count int) string"
	line := "strings.Repeat("
	in := testInput(t, line, len([]rune(line)))

	// The space between them is the one the cursor is drawn on, which
	// textinput puts at the end of every line.
	got := stripEsc(inputView(in, hintOpts(sig, 100)))
	if want := in.Prompt + line + " " + "  " + sig; got != want {
		t.Errorf("view stripped to %q, want %q", got, want)
	}
}

// TestHintYieldsToTheLine: the line is what the terminal's width belongs to. A
// hint takes what is left, and nothing when what is left is not worth reading.
func TestHintYieldsToTheLine(t *testing.T) {
	withPalette(t)
	const sig = "strings.Repeat([s string], count int) string"
	line := "strings.Repeat("
	in := testInput(t, line, len([]rune(line)))
	prompt := lipgloss.Width(in.Prompt)

	for _, tc := range []struct {
		name  string
		width int
		want  string
	}{
		{name: "no room at all", width: prompt + len(line) + 4, want: ""},
		{name: "no width yet", width: 0, want: ""},
		{
			name:  "room for some of it",
			width: prompt + len(line) + 1 + 2 + 20,
			want:  "  strings.Repeat([s s…",
		},
		{
			name:  "room for all of it",
			width: prompt + len(line) + 1 + 2 + len(sig),
			want:  "  " + sig,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := stripEsc(inputView(in, hintOpts(sig, tc.width)))
			rest := strings.TrimPrefix(got, in.Prompt+line+" ")
			if rest != tc.want {
				t.Errorf("after the line: %q, want %q", rest, tc.want)
			}
			if w := lipgloss.Width(got); tc.width > 0 && w > tc.width {
				t.Errorf("the line took %d columns of %d", w, tc.width)
			}
		})
	}
}

// TestHintDoesNotDisturbTheLine is the guarantee that makes a hint safe: it is
// not a suggestion and it is not code. The line's own bytes, and the ghost the
// suggestion draws, are what they would be with no hint at all.
func TestHintDoesNotDisturbTheLine(t *testing.T) {
	withPalette(t)
	line := "fmt.Fprintln(w"
	in := testInput(t, line, len([]rune(line)))
	in.SetSuggestions([]string{"fmt.Fprintln(writer"})

	const sig = "fmt.Fprintln([w io.Writer], a ...any) (n int, err error)"
	withHint := stripEsc(inputView(in, hintOpts(sig, 120)))
	without := stripEsc(inputView(in, colourOpts(nil)))
	if !strings.HasPrefix(withHint, without) {
		t.Fatalf("the hint changed the line:\n with %q\n without %q", withHint, without)
	}
	if !strings.Contains(without, "riter") {
		t.Fatal("the ghost is missing, so this asserts nothing")
	}
	if got := strings.TrimPrefix(withHint, without); got != "  "+sig {
		t.Errorf("after the ghost: %q, want the hint", got)
	}
}

// TestHintIsDimAndNotTokenized: a signature is not code the user wrote.
func TestHintIsDimAndNotTokenized(t *testing.T) {
	withPalette(t)
	line := "strings.Repeat("
	in := testInput(t, line, len([]rune(line)))
	view := inputView(in, hintOpts("strings.Repeat([s string], count int) string", 100))

	p := litPalette()
	tail := view[strings.LastIndex(view, "  strings"):]
	if strings.Contains(tail, p.Open[syntax.RoleIdent]) || strings.Contains(tail, p.Open[syntax.RoleType]) {
		t.Error("the hint was tokenized")
	}
}

// TestHintNeedsTheCursorAtTheEnd: the hint rides where the ghost is drawn, and
// the ghost is only drawn at the end of the line. A cursor in the middle of the
// line is not in an argument position the TUI would ask about.
func TestHintNeedsTheCursorAtTheEnd(t *testing.T) {
	withPalette(t)
	line := "strings.Repeat("
	in := testInput(t, line, 3)
	if got := stripEsc(inputView(in, hintOpts("strings.Repeat(...)", 100))); strings.Contains(got, "...") {
		t.Errorf("a hint was drawn with the cursor mid-line: %q", got)
	}
}

// insertOpts is a painting inputView with insert mode's cursor asked for.
func insertOpts(pending []string) inputOpts {
	o := colourOpts(pending)
	o.Insert = true
	return o
}

// The insert cursor is an underline under the cell it is in, at every position
// of every line. Two things at once, and the second is the bug the first
// replaced: the character it marks is still drawn, and the line is the same
// line it is in normal mode — same characters, same columns, so switching modes
// moves the cursor and never the text.
func TestTheInsertCursorUnderlinesTheCell(t *testing.T) {
	withAttributes(t)
	for _, line := range []string{
		`x := "hello"`, "len(s)", "π := 1", ":query SELECT 1", `s := "日本"`,
	} {
		rs := []rune(line)
		for pos := 0; pos <= len(rs); pos++ {
			in := testInput(t, line, pos)
			view := inputView(in, insertOpts(nil))
			// The line, and the trailing cell textinput draws at the end of
			// one: nothing the cursor added, and nothing it took away.
			want := in.Prompt + line
			if pos >= len(rs) {
				want += " "
			}
			if got := stripEsc(view); got != want {
				t.Fatalf("%q at %d: view stripped to %q, want %q", line, pos, got, want)
			}
			if got, want := underlined(view), cellAt(rs, pos); got != want {
				t.Fatalf("%q at %d: underlined %q, want %q", line, pos, got, want)
			}
		}
	}
}

// cellAt is the character the cursor stands on, which is a space at the end of
// the line — textinput's own trailing cell, and the one the block stands on
// there too.
func cellAt(rs []rune, pos int) string {
	if pos >= len(rs) {
		return " "
	}
	return string(rs[pos])
}

// The shape is not the colour. A theme with the colour turned off still has two
// modes to tell apart, so it is the one that has to keep the underline — where
// every other reason to fall through to textinput still falls through.
func TestTheInsertCursorSurvivesAThemeWithoutColour(t *testing.T) {
	withAttributes(t)
	plain := inputOpts{Theme: ui.Theme{Colour: false}, Pal: syntax.Palette{}, Insert: true}
	for _, tc := range []struct{ line, cell string }{{"", " "}, {"x := 1", " "}} {
		in := testInput(t, tc.line, len([]rune(tc.line)))
		got := inputView(in, plain)
		if u := underlined(got); u != tc.cell {
			t.Errorf("%q: the plain view underlines %q, want %q", tc.line, u, tc.cell)
		}
		if want := in.Prompt + tc.line + " "; stripEsc(got) != want {
			t.Errorf("%q: plain view is %q, want %q", tc.line, stripEsc(got), want)
		}
	}
}

// Normal mode's cursor is upstream's block, and vim off is upstream's prompt
// byte for byte — the underline is the one thing that changes, and only in
// insert.
func TestWithoutInsertNothingChanges(t *testing.T) {
	withAttributes(t)
	for _, pos := range []int{0, 3, 6} {
		in := testInput(t, "x := 1", pos)
		if got := underlined(inputView(in, colourOpts(nil))); got != "" {
			t.Errorf("at %d: a block-cursor prompt underlined %q", pos, got)
		}
	}
}

// The underline is under the ghost's first character rather than over it: a
// suggestion whose first character is hidden is a suggestion misread.
func TestTheInsertCursorLeavesTheSuggestionWhole(t *testing.T) {
	withAttributes(t)
	in := testInput(t, "strings.To", 10)
	in.SetSuggestions([]string{"strings.ToUpper"})

	got := inputView(in, insertOpts(nil))
	if want := in.Prompt + "strings.ToUpper"; stripEsc(got) != want {
		t.Errorf("view stripped to %q, want %q", stripEsc(got), want)
	}
	if u := underlined(got); u != "U" {
		t.Errorf("underlined %q, want the ghost's first character", u)
	}
}

// The hint is sized around what is on the line, and neither cursor adds a cell
// to it. Getting that wrong wraps the line being typed onto a second row, which
// is the one thing hintAfter exists to not do.
func TestTheHintIsTheSameWidthInBothModes(t *testing.T) {
	withAttributes(t)
	in := testInput(t, "strings.To", 10)
	in.SetSuggestions([]string{"strings.ToUpper"})

	o := insertOpts(nil)
	o.Hint = "func(s string) string"
	o.Ghost = lipgloss.NewStyle()
	block := o
	block.Insert = false

	for w := 20; w < 80; w++ {
		o.Width, block.Width = w, w
		insert := lipgloss.Width(stripEsc(inputView(in, o)))
		if insert > w {
			t.Fatalf("at width %d the insert line is %d columns wide and wraps", w, insert)
		}
		if got := lipgloss.Width(stripEsc(inputView(in, block))); got != insert {
			t.Fatalf("at width %d the insert line is %d columns and the normal one %d",
				w, insert, got)
		}
	}
}
