package repl

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"

	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/syntax"
	"github.com/sandboxws/gluon/internal/ui"
)

// This file paints a typed line, twice over: once while it is being typed, and
// once when it is echoed into scrollback. Both go through runs, below, because
// the two have to agree — a line that changed colour on Enter would read as
// gluon disagreeing with itself about what was typed.

// A run is one stretch of a line with one role. Runs tile the line: they are in
// order and cover every byte, including whitespace.
//
// Tiling is what makes the cursor easy. Placing it means splitting exactly one
// run at a byte offset, so nothing ever has to cut a string that already
// contains escape sequences — which is the bug class this design is built to
// not have.
type run struct {
	text string
	role syntax.Role
}

// paintKind is each builtin's argument kind, under its name and every alias,
// for painting the argument as what it is. It replaced two hand-kept lists —
// the SQL commands and the ones taking paths, names and switches — which a
// renamed command left stale without anything noticing.
//
// It is built from the registry once, at init, and it holds builtins only.
// Painting runs on the UI goroutine while an evaluation may be in flight, and
// the plugin commands live in c.extra, which refreshPlugins writes from the
// evaluation goroutine — invariant 15's concern. So a plugin command's argument
// is painted as Go, the zero Kind: a colour that may be wrong rather than a
// race.
var paintKind map[string]cmdspec.Kind

// argKinds is paintKind for a list of commands.
func argKinds(cmds []Command) map[string]cmdspec.Kind {
	out := make(map[string]cmdspec.Kind, 2*len(cmds))
	for _, c := range cmds {
		out[c.Name] = c.Usage.Kind
		for _, a := range c.Aliases {
			out[a] = c.Usage.Kind
		}
	}
	return out
}

// lineRuns tiles one typed line.
//
// pending is the unfinished construct this line continues, and it is not
// decoration: a line closing a raw string opened above is string content, and a
// scanner starting at the line alone would call it two identifiers. So the
// whole construct is tokenized and only the last line's runs are kept.
func lineRuns(pending []string, line string) []run {
	if cmd, ok := metaSplit(pending, line); ok {
		return cmd
	}
	src, from := line, 0
	if len(pending) > 0 {
		// Capped, because a paste can leave an arbitrarily large unfinished
		// construct in pending and this runs on every keystroke. Past the cap
		// the line is tokenized alone, which is wrong only inside a very long
		// open literal.
		const enough = 4 << 10
		joined := strings.Join(append(append([]string{}, pending...), line), "\n")
		if len(joined) <= enough {
			src, from = joined, len(joined)-len(line)
		}
	}
	return tile(src, from, syntax.Tokens(syntax.Go, src))
}

// metaSplit paints a meta command: its name is a keyword of gluon's own
// language, and its argument belongs to whatever the command takes.
//
// It deliberately does not ask Core whether the name exists. Core.lookup reads
// c.extra, which refreshPlugins writes from the evaluation goroutine, and this
// runs on the UI one while an evaluation may be in flight — invariant 15's
// concern, in a place where the answer is only a colour. paintKind is built
// from the builtins at init and never written again, so it is safe here; `:foo`
// is painted like a command and then told it is not one, which is a cosmetic
// inconsistency rather than a race.
func metaSplit(pending []string, line string) ([]run, bool) {
	// A line inside an unfinished construct is not a meta command, because
	// submitLine joins pending before Core classifies it. Typing `:x` as the
	// second line of a block is Go.
	if len(pending) > 0 {
		return nil, false
	}
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i >= len(line) || line[i] != ':' {
		return nil, false
	}
	j := i + 1
	for j < len(line) && line[j] != ' ' && line[j] != '\t' {
		j++
	}
	if j == i+1 {
		return nil, false // a bare colon is not a command name
	}
	name := line[i:j]
	var out []run
	if i > 0 {
		out = append(out, run{text: line[:i]})
	}
	out = append(out, run{text: name, role: syntax.RoleKeyword})
	rest := line[j:]
	if rest == "" {
		return out, true
	}
	switch paintKind[name] {
	case cmdspec.SQL:
		out = append(out, tile(rest, 0, syntax.Tokens(syntax.SQL, rest))...)
	case cmdspec.Words, cmdspec.NoArg:
		// A path, a URL or a name tokenized as Go paints a directory's
		// slashes as division and a URL's colon as a label.
		out = append(out, run{text: rest})
	default:
		out = append(out, tile(rest, 0, syntax.Tokens(syntax.Go, rest))...)
	}
	return out, true
}

// tile turns tokens into runs covering src[from:] with no gaps.
//
// A token straddling `from` is clipped rather than dropped, and that is the
// whole point of taking `from` at all: the raw string a continuation line
// closes was opened on a line above, so its token starts before the text being
// painted. Clipping is what carries its colour onto this line.
//
// Clipping is safe because goTokens emits tokens whose starts never go
// backwards — it tracks the previous end for exactly that — so the only token
// that can begin before the cursor is the first one.
func tile(src string, from int, toks []syntax.Token) []run {
	var out []run
	cursor := from
	for _, t := range toks {
		start, end := max(t.Start, cursor), t.End
		if end <= start || end > len(src) {
			continue
		}
		if start > cursor {
			out = append(out, run{text: src[cursor:start]})
		}
		out = append(out, run{text: src[start:end], role: t.Role})
		cursor = end
	}
	if cursor < len(src) {
		out = append(out, run{text: src[cursor:]})
	}
	return out
}

// paint renders runs with a palette. It is the echo path.
func paint(runs []run, p syntax.Palette) string {
	var b strings.Builder
	for _, r := range runs {
		b.WriteString(p.Span(r.role, r.text))
	}
	return b.String()
}

// echoOf paints one submitted line for scrollback.
func echoOf(pending []string, line string) string {
	if !syntaxPal.Painted() || line == "" {
		return line
	}
	return paint(lineRuns(pending, line), syntaxPal)
}

// inputOpts is what inputView needs beyond the textinput itself.
type inputOpts struct {
	Theme   ui.Theme
	Pal     syntax.Palette
	Ghost   lipgloss.Style
	Pending []string
	// Insert asks for the insert-mode cursor: an underline under the character
	// the next keystroke pushes right, rather than a block standing on it.
	// False for everyone who has not turned modal editing on, and for normal
	// mode, which is where a block is what vim itself draws.
	Insert bool
	// Hint is the callee's signature while the cursor is inside a call. It is
	// drawn after the line and is not part of it: it never reaches the
	// suggestion list, so tab cannot accept it, and it never reaches the
	// tokenizer, so the line's own bytes are what they were.
	Hint string
	// Width is the terminal's, for deciding how much of the hint there is room
	// for. Zero means no size has arrived yet, and the hint waits: a hint that
	// overflows would wrap the line being typed, which is worse than no hint.
	Width int
}

// inputView renders the prompt, the line and the cursor with per-token colour.
//
// It replaces textinput.Model.View rather than wrapping it, because that View
// paints the whole value with a single TextStyle and offers no seam — TextStyle
// is a struct, not an interface, and SetValue is out of the question because
// Value() is what gets submitted. What makes replacing it cheap is that gluon
// never sets Width: handleOverflow then always yields offset 0 and offsetRight
// len(value), so there is no scroll window to reproduce, and the twenty lines
// of View that gluon actually reaches are the twenty lines below.
//
// Anything this was not written for falls through to upstream. That bounds the
// entire "our imitation drifted from theirs" risk to "colour disappears",
// which is a bad afternoon rather than a corrupted line. The insert cursor is
// the one thing that does not fall through, and it pays for that: with it asked
// for, this function renders the plain line too, which upstream used to render.
//
// The mode's letter is drawn nowhere in here, and that is what makes it survive
// every bail-out below: it is part of in.Prompt, which upstream's own View
// renders for the plain and NO_COLOR case, and which is drawn whether there is
// a line or not for the empty one dd leaves behind. "Which mode am I in" must
// be answerable from the screen always, or a swallowed key is a wedged prompt.
//
// The mode's *cursor* is drawn here, which is why o.Insert survives the three
// bail-outs about painting: colour off, a palette that paints nothing, and a
// line with nothing in it to paint. A shape is not a colour — a theme with the
// colour turned off still has two modes to tell apart — and the empty line is
// exactly where dd leaves the cursor. The other three bail-outs are
// unconditional: a cursor is not worth reproducing a scroll window for, and
// upstream draws a perfectly good block.
func inputView(in textinput.Model, o inputOpts) string {
	value := in.Value()
	if in.Width > 0 || in.EchoMode != textinput.EchoNormal || !in.Focused() {
		return in.View()
	}
	if !o.Insert && (!o.Theme.Colour || !o.Pal.Painted() || value == "") {
		return in.View()
	}

	runs := lineRuns(o.Pending, value)
	// The line is not optional; the colour is. If the runs do not reconstruct
	// what was typed, something upstream is wrong and the honest response is to
	// show the line plainly rather than to show something else confidently.
	var sum strings.Builder
	for _, r := range runs {
		sum.WriteString(r.text)
	}
	if sum.String() != value {
		return in.View()
	}

	styles := o.Theme.SyntaxStyles()
	rs := []rune(value)
	pos := min(max(in.Position(), 0), len(rs))
	curByte := len(string(rs[:pos]))

	var b strings.Builder
	b.WriteString(in.Prompt)
	at := 0
	placed := false
	for _, r := range runs {
		end := at + len(r.text)
		if placed || curByte < at || curByte >= end {
			b.WriteString(o.Pal.Span(r.role, r.text))
			at = end
			continue
		}
		// The cursor is inside this run. Split it, and let lipgloss write every
		// escape either half of the split needs: nothing here writes one by
		// hand, which is what keeps a session with stdout redirected from
		// getting bytes no other part of gluon would have produced.
		off := curByte - at
		_, size := utf8.DecodeRuneInString(r.text[off:])
		b.WriteString(o.Pal.Span(r.role, r.text[:off]))
		// Both cursors are one cell wide and stand on the same cell, so both
		// carry the colour of the token they are in: the block by reversing it,
		// the underline by keeping it and ruling a line under it.
		if o.Insert {
			b.WriteString(cursorUnder(styles[r.role], r.text[off:off+size]))
		} else {
			// bubbles/cursor draws the character between the halves.
			b.WriteString(cursorOver(in, styles[r.role], r.text[off:off+size]))
		}
		b.WriteString(o.Pal.Span(r.role, r.text[off+size:]))
		at, placed = end, true
	}
	if !placed {
		b.WriteString(ghostOrSpace(in, o, rs))
		b.WriteString(hintAfter(in, o, value, rs))
	}
	return b.String()
}

// hintAfter is the signature drawn to the right of the line.
//
// It only ever appears where the cursor is at the end of the line, because that
// is the only place ghostOrSpace runs and the only place the TUI asks for a
// hint at all. What it must not do is push the line onto a second row, so it
// takes what is left of the terminal and no more.
func hintAfter(in textinput.Model, o inputOpts, value string, rs []rune) string {
	// Two spaces of separation, so the hint reads as being beside the line
	// rather than typed at the end of it.
	const sep = "  "
	// Fewer than this many columns of a signature is noise rather than a hint,
	// and the line is what the space belongs to.
	const least = 8

	if o.Hint == "" || o.Width <= 0 {
		return ""
	}
	// What the line already occupies: the prompt, the value, and whatever
	// ghostOrSpace just drew after it — the ghost's overhang, or the one cell
	// the cursor sits on. Neither cursor adds a cell of its own, so this is the
	// same count in both modes.
	after := 1
	if sugg := []rune(in.CurrentSuggestion()); len(sugg) > len(rs) &&
		strings.HasPrefix(string(sugg), string(rs)) {
		after = lipgloss.Width(string(sugg[len(rs):]))
	}
	room := o.Width - lipgloss.Width(in.Prompt) - lipgloss.Width(value) - after - len(sep)
	if room < least {
		return ""
	}

	hint := o.Hint
	if lipgloss.Width(hint) > room {
		hint = truncate(hint, room-1) + "…"
	}
	// The ghost's role, because the hint is the same kind of thing: text gluon
	// put on the line rather than text the user typed.
	return o.Ghost.Inline(true).Render(sep + hint)
}

// truncate cuts a string to n columns. It counts columns rather than bytes
// because a signature can carry a name in any script the terminal draws wide.
func truncate(s string, n int) string {
	var w int
	for i, r := range s {
		rw := lipgloss.Width(string(r))
		if w+rw > n {
			return s[:i]
		}
		w += rw
	}
	return s
}

// cursorOver draws the cursor carrying the colour of the token it stands in.
//
// Both of bubbles/cursor's phases are covered: Style is reversed to make the
// block, so the token's colour becomes the block; TextStyle is what shows
// between blinks, so the character keeps its colour there too.
func cursorOver(in textinput.Model, style lipgloss.Style, char string) string {
	// NoTabConversion, because lipgloss.Render turns a tab into spaces and the
	// cursor character is the one byte here that goes through Render at all.
	//
	// It should never fire: textinput.SetValue runs bubbles' runeutil sanitizer
	// (TestInputValueNeverHoldsATab), so a typed line cannot contain a tab in
	// the first place — and Tab is the completion key besides. This is belt to
	// that braces, because the sanitizer is textinput's to change and the
	// failure would be silent reformatting of the line being typed.
	style = style.TabWidth(lipgloss.NoTabConversion)
	c := in.Cursor
	c.Style, c.TextStyle = style, style
	c.SetChar(char)
	return c.View()
}

// cursorUnder draws the insert cursor: the character with a line ruled under
// the cell it is in.
//
// A terminal draws its own insert cursor as a beam in the gap between two
// cells. A frame gluon renders itself has no gaps — every cursor it draws is a
// whole cell — so the two shapes are the two things a cell can be: reversed, or
// underlined. An underline is the shape vim's `guicursor` calls `hor20`, and it
// is the one that costs the line nothing: the character keeps its colour, keeps
// its cell, and every column to the right of it stays where it was. A cursor
// that moved the line sideways on esc would be the mode marker doing more
// damage than the mode.
//
// The cost is that an underline is an attribute, and termenv's Ascii profile
// drops every attribute — so under NO_COLOR neither cursor is drawn, the block
// included, which is the terminal upstream's own block was already invisible
// on. The mode is still said there, because `[i]` and `[n]` are text.
func cursorUnder(style lipgloss.Style, char string) string {
	// NoTabConversion, for the same reason cursorOver has it: this is the one
	// cell of the line that goes through lipgloss.Render, and Render turns a
	// tab into spaces.
	return style.Underline(true).Inline(true).
		TabWidth(lipgloss.NoTabConversion).Render(char)
}

// ghostOrSpace is the cursor at the end of the line: over the first character
// of the completion suggestion when there is one, and over a space otherwise.
// The trailing space is textinput's, and it is what keeps the line the same
// width between blinks.
//
// Both cursors take the cell they are in, here as everywhere else, so a ghost
// is the same width in both modes and the line under it never moves. The
// suggestion stays readable either way: the block shows the character it covers
// and the underline never covered one.
func ghostOrSpace(in textinput.Model, o inputOpts, rs []rune) string {
	sugg := []rune(in.CurrentSuggestion())
	// Case-sensitive, which textinput's own matching is not. Invariant 16: a
	// candidate that only matched case-insensitively would preview a line that
	// accepting it does not produce, so it gets no ghost rather than a lie.
	if len(sugg) > len(rs) && strings.HasPrefix(string(sugg), string(rs)) {
		// The ghost is never tokenized. It is a suggestion, not code the user
		// wrote, and painting it as code would make it look typed.
		head, tail := string(sugg[len(rs)]), string(sugg[len(rs)+1:])
		if o.Insert {
			return cursorUnder(o.Ghost, head) + o.Ghost.Inline(true).Render(tail)
		}
		c := in.Cursor
		c.Style, c.TextStyle = o.Ghost, o.Ghost
		c.SetChar(head)
		return c.View() + o.Ghost.Inline(true).Render(tail)
	}
	if o.Insert {
		// The cell textinput would have drawn a block on: the end of the line
		// is where insert mode nearly always is, and the line under it is a
		// space, which is what an underline there looks like anyway.
		return cursorUnder(lipgloss.NewStyle(), " ")
	}
	c := in.Cursor
	c.SetChar(" ")
	return c.View()
}
