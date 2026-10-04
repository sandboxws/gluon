package repl

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/mod/module"
	"golang.org/x/term"

	"github.com/sandboxws/gluon/internal/plugin"
	palettes "github.com/sandboxws/gluon/internal/theme"
	"github.com/sandboxws/gluon/internal/ui"
)

// The screen an interactive session opens with.
//
// Everything that decides bytes lives in this file as a function that takes
// facts and returns a string. That is not tidiness: the screen is printed
// before tea.NewProgram, so neither tier described in ROADMAP's "Testing the
// UI" can reach it — a model test drives Update, and a program test needs a
// program. A builder that returns a string is what makes it testable at all.
//
// What the screen answers is "what does gluon know about where I am". The
// banner it replaces answered a different and much smaller question, and the
// hints line is the part of it that survives: it names the things discoverable
// only by being told, because `?` cannot be a keybinding in a REPL — it is a
// character people type — so the discoverable keys went here instead. The fact
// block is not a replacement for that line and does not shorten it.

// The banner modes, as the `banner` setting spells them.
const (
	bannerFull    = "full"
	bannerCompact = "compact"
	bannerOff     = "off"
)

// tagline is the README's first sentence and the site's <h1> subtitle, in the
// one place a reader of the terminal would ever see it.
const tagline = "a REPL and scratch runner for Go"

// The wordmark, in Block Elements rather than in the box-drawing set the tables
// use. Two reasons: a table's rules are furniture and a wordmark is not, so
// they should not share a character family; and N has a diagonal, which the
// light box-drawing set can only imply with corners. Block Elements are close
// to universal in monospace faces — the one thing to check on a real terminal
// is the font's line height, which gluon cannot assert and a gap between the
// rows would reveal.
var wordmarkRows = [2]string{
	"█▀▀ █   █ █ █▀█ █▄ █",
	"█▄█ █▄▄ █▄█ █▄█ █ ▀█",
}

const (
	// bannerIndent is the left margin every row shares, wordmark included.
	bannerIndent = "  "
	// bannerLabel is the fact column. "scratchpad" is the longest label and
	// wants two spaces after it.
	bannerLabel = 10
	// bannerGap separates the wordmark from the text beside it.
	bannerGap = "   "
	// bannerMinWidth is the narrowest terminal that still carries the wordmark
	// with the tagline beside it. Below it the mark is dropped and the facts
	// are kept, because the facts are the part that is about this session.
	bannerMinWidth = 60
)

// bootRow is one labelled fact.
type bootRow struct{ label, value string }

// bootFacts is what gluon knows about where it is, before the first prompt.
//
// Rows are ordered rather than named because the order is the order they were
// found in, which is also the order they caused each other: -host attaches
// before a pad opens, and a pad that records a host is what attaches one when
// -host was not given. A screen that filled in top to bottom regardless would
// have to wait for the slowest fact before drawing the first.
type bootFacts struct {
	Version string // already through shortVersion
	Go      string
	Rows    []bootRow
}

// add appends a row, and drops one with nothing to say.
//
// This is the suppression rule in the one place it can be tested: a screen that
// reported "database  none" every morning would be furniture rather than news,
// and the difference between the two is whether the row is absent or is present
// saying nothing.
func (f *bootFacts) add(label, value string) {
	if value == "" {
		return
	}
	f.Rows = append(f.Rows, bootRow{label, value})
}

// shortVersion is the version as the banner says it, which is not always the
// version as everything else says it.
//
// resolveVersion falls back to debug.ReadBuildInfo, so every `go install` and
// every clone reports a Go pseudo-version: thirty-eight characters of which the
// revision is the only part anybody reads. The timestamp is already in the
// revision, and the 0.0.0 base is an artefact of there being no tag.
//
// Only the banner does this. `gluon --version`, fang's own version flag and the
// version template print the string untouched, because that is the one a bug
// report quotes.
func shortVersion(v string) string {
	if v == "" {
		return ""
	}
	// +dirty is build metadata. It is not part of a version x/mod will parse,
	// so it comes off before the question is asked and goes back on after.
	base, meta, hasMeta := strings.Cut(v, "+")
	// resolveVersion strips the leading v; module.IsPseudoVersion wants it.
	if !module.IsPseudoVersion("v" + base) {
		return v
	}
	rev, err := module.PseudoVersionRev("v" + base)
	if err != nil || rev == "" {
		return v
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	out := "dev · " + rev
	if hasMeta && meta != "" {
		out += " · " + meta
	}
	return out
}

// pluginsFact names the plugins worth reporting, which is not the plugins that
// are active.
//
// http, json and time are standard library and are always active; they are true
// of every session and so distinguish none. What is worth a row is a plugin
// that activated off this project's own build list, because that is a fact
// about where you are rather than about gluon.
func pluginsFact(set *plugin.Set) string {
	if set == nil {
		return ""
	}
	var names []string
	for _, p := range set.Active() {
		if m := p.Meta(); m.Module != "" {
			names = append(names, m.Name)
		}
	}
	return strings.Join(names, ", ")
}

// themeFact names the palette, and only where naming it says something.
//
// The default repeated on every launch is furniture. A palette that would not
// load is news, and is the one case gluon doctor already reports and a session
// otherwise starts silently wrong about.
func themeFact(name string, err error) string {
	if err != nil {
		if name == "" {
			name = palettes.Default
		}
		return name + " — would not load, using the default"
	}
	if name == "" || name == palettes.Default {
		return ""
	}
	return name
}

// shortPath is a provenance as a reader recognises it. The interesting half of
// "/Users/x/work/acme/.env" is ".env"; the directory is where they already are.
func shortPath(p string) string {
	if wd, err := os.Getwd(); err == nil {
		if rel := strings.TrimPrefix(p, wd+string(os.PathSeparator)); rel != p {
			return rel
		}
	}
	if i := strings.LastIndexByte(p, os.PathSeparator); i >= 0 {
		return p[i+1:]
	}
	return p
}

// bannerRow draws one labelled row. value arrives already styled, if it is
// going to be styled at all.
//
// pad is picker.go's, and it is called on the raw label before any style
// touches it, for the reason written there: a lipgloss style makes len() lie
// and the column would walk.
//
// A value wider than the room beside its label is wrapped, and carries on
// under itself rather than under the label. A terminal left to wrap it would
// carry on at column 0, through the label column — which is the one thing
// that makes the block readable — and a project that activates a plugin for
// each of the things gluon describes has a plugins row longer than eighty
// columns. width is the terminal's, and 0 means unknown: nothing wraps.
func bannerRow(t ui.Theme, label, value string, width int) string {
	col := len(bannerIndent) + bannerLabel + 2
	lines := []string{value}
	if width > col {
		lines = wrapShown(value, width-col)
	}
	return bannerIndent + t.Annot.Render(pad(label, bannerLabel)) + "  " +
		strings.Join(lines, "\n"+strings.Repeat(" ", col))
}

// wrapShown breaks text at spaces so no line shows wider than width. It
// measures what a terminal draws, so a style's escapes take no room, and it
// breaks only at a space: a word wider than width is left whole, because half
// a module path is not something anybody can type.
func wrapShown(text string, width int) []string {
	var lines []string
	line, shown := "", 0
	for i, w := range strings.Split(text, " ") {
		n := lipgloss.Width(w)
		if i > 0 && shown > 0 && shown+1+n > width {
			lines = append(lines, line)
			line, shown = w, n
			continue
		}
		if i > 0 {
			line += " "
			shown++
		}
		line += w
		shown += n
	}
	return append(lines, line)
}

// bannerHead is the wordmark, the versions and the tagline — everything that is
// known before any work is done, which is why it is a function of its own. It
// is printed first and unconditionally, so that a slow start is a screen
// filling in rather than a blank terminal.
func bannerHead(t ui.Theme, f bootFacts, width int) string {
	vers := f.Version
	if f.Go != "" {
		if vers != "" {
			vers += " · "
		}
		vers += f.Go
	}

	if width > 0 && width < bannerMinWidth {
		// No room for the mark beside the text. The facts are the half that is
		// about this session, so they are the half that is kept.
		return bannerIndent + t.Prompt.Render("gluon") + " " + t.Dim.Render(vers) + "\n"
	}

	col := len(bannerIndent) + lipgloss.Width(wordmarkRows[0]) + len(bannerGap)
	var b strings.Builder
	b.WriteString(bannerIndent + t.Prompt.Render(wordmarkRows[0]) + "\n")
	b.WriteString(bannerIndent + t.Prompt.Render(wordmarkRows[1]) + bannerGap + t.Dim.Render(vers) + "\n")
	b.WriteString(strings.Repeat(" ", col) + t.Dim.Render(tagline) + "\n")
	return b.String()
}

// bannerHints is the line that survives from the banner this replaces, word for
// word. Its charter is unchanged: it names tab completion and ctrl-r because
// those are discoverable only by being told, and everything else is in :help.
// The keys are lifted out of the prose so the line can be read at a glance
// without being shortened.
func bannerHints(t ui.Theme) string {
	key, txt := t.Note.Render, t.Dim.Render
	return bannerIndent +
		key(":help") + txt(" for commands") + txt(" · ") +
		key("tab") + txt(" completes") + txt(" · ") +
		key("ctrl-r") + txt(" searches history") + txt(" · ") +
		key(":q") + txt(" to quit") + "\n"
}

// renderBanner is the whole screen, for a caller that already has every fact —
// the golden tests, and any driver that is not filling rows in as it goes.
//
// runTUI does not call it, because it prints the head before it has the facts
// and each row as it arrives. Both go through bannerHead, bannerRow and
// bannerHints, so there is one implementation of each piece and this is the
// assembly rather than a second copy of it.
func renderBanner(t ui.Theme, f bootFacts, width int, mode string) string {
	switch mode {
	case bannerOff:
		return ""
	case bannerCompact:
		vers := strings.TrimSpace(f.Version + " · " + f.Go)
		return bannerIndent + t.Prompt.Render("gluon") + " " + t.Dim.Render(vers) + "\n" +
			bannerHints(t) + "\n"
	}

	var b strings.Builder
	b.WriteString(bannerHead(t, f, width))
	if len(f.Rows) > 0 {
		b.WriteString("\n")
		for _, r := range f.Rows {
			b.WriteString(bannerRow(t, r.label, r.value, width) + "\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(bannerHints(t))
	b.WriteString("\n")
	return b.String()
}

// stdoutIsTerminal is the question a row rewrite actually has to ask, and it is
// not the question the theme answers. ui.New is handed os.Stdin for the REPL,
// because that is what decides raw mode there; a carriage return goes to
// stdout. The two differ exactly when output is redirected and input is not,
// which is the case this guard exists for.
var stdoutIsTerminal = func() bool { return term.IsTerminal(int(os.Stdout.Fd())) }

// termWidth is the width to lay the screen out in, or 0 for "unknown", which
// bannerHead reads as "assume there is room".
func termWidth() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return 0
	}
	return w
}

// pending draws a row that does not know its value yet and hands back the
// function that replaces it.
//
// Attaching a host walks its package index and opening a scratchpad replays it,
// and either can take long enough that a screen which waited for them would be
// blank while it did. So the row is drawn first, saying what is happening, and
// is overwritten in place when the answer lands.
//
// A carriage return and a space pad is the whole trick. gluon owns no escape
// sequences — there is not one raw escape literal in non-test Go code, and
// keeping it that way is worth more than the one call to EraseEntireLine this
// would otherwise want — and a row this short cannot have wrapped on a terminal
// wide enough to be drawing a wordmark on. Off a terminal nothing is rewritten,
// so a redirected session gets the answer alone rather than the question.
func pending(w io.Writer, t ui.Theme, label, note string, width int) func(value string) {
	if !stdoutIsTerminal() {
		return func(value string) {
			if value != "" {
				fmt.Fprint(w, bannerRow(t, label, value, width)+"\n")
			}
		}
	}
	line := bannerRow(t, label, t.Dim.Render(note), 0)
	fmt.Fprint(w, line)
	n := lipgloss.Width(line)
	return func(value string) {
		if value == "" {
			// Nothing to say after all. Take the row back rather than leave a
			// question standing where an answer never came.
			fmt.Fprint(w, "\r"+strings.Repeat(" ", n)+"\r")
			return
		}
		// Blank the row, return, then write the answer — rather than padding
		// the answer out to cover what was there. Padding would leave trailing
		// spaces in scrollback, which is the thing that shows up the moment
		// somebody selects a line to copy it.
		fmt.Fprint(w, "\r"+strings.Repeat(" ", n)+"\r"+bannerRow(t, label, value, width)+"\n")
	}
}
