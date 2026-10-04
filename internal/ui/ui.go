// Package ui is gluon's palette. It exists because the styles were in two
// places that could drift: internal/pretty held the eight roles the value
// renderer and the inspectors share, and internal/repl held five more for the
// prompt, and nothing connected them or let a user change either.
//
// Colour is decided once, here, and expressed by handing back styles that
// simply do not colour when colour is off. There is no global profile switch:
// a global would be invisible to a test and would reach into the inspectors'
// existing rich/plain split from the side, and that split is already the
// honest way gluon says "this is going to a pipe".
//
// # On the default having changed
//
// The default palette used to be ANSI 0-15, on the argument that those sixteen
// slots are the user's own terminal theme and a truecolor default would
// override a palette someone chose. That argument was right about the cost and
// wrong about the benefit, and the wrong half turned out to matter more.
//
// It was wrong because sixteen slots cannot carry the distinctions gluon now
// draws. A keyword, a builtin call and a type are three different things; in
// ANSI they were magenta, blue and cyan whose actual appearance gluon could not
// read, and on a screen of source they blur. What gluon looked like was not
// "the terminal's" so much as unspecified.
//
// It was right that a chosen palette should not be overridden without a way
// back. So the way back is one line — `[theme] name = "terminal"` restores the
// ANSI mapping exactly, role for role, and a test pins it — and gluon never
// picks that for you based on what it thinks your terminal is. Every palette is
// a file you can read and copy, and the shipped default is one of those files
// rather than literals in here.
//
// Degradation is termenv's, not gluon's: on a 256-colour terminal a hex value
// becomes the nearest cube entry, on a 16-colour one the nearest of sixteen,
// and on a pipe it is dropped entirely.
package ui

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"

	"github.com/sandboxws/gluon/internal/config"
	"github.com/sandboxws/gluon/internal/pretty"
	"github.com/sandboxws/gluon/internal/syntax"
	"github.com/sandboxws/gluon/internal/theme"
)

// Theme is every styled role in gluon, in one place.
type Theme struct {
	// Colour reports whether this theme paints anything. It is false under
	// NO_COLOR and when the destination is not a terminal.
	Colour bool

	// Name is the palette this was resolved from, for `gluon doctor`.
	Name string

	// Value rendering, shared with the inspectors.
	Type   lipgloss.Style
	Annot  lipgloss.Style
	Note   lipgloss.Style
	Str    lipgloss.Style
	Num    lipgloss.Style
	Header lipgloss.Style
	Index  lipgloss.Style
	Border lipgloss.Style

	// The REPL. Mode and ModeCont are the two prompts again, in the colour
	// that says normal mode has the keyboard: the mode is drawn as the prompt
	// rather than beside it, so the prompt is what has to change.
	Prompt   lipgloss.Style
	Cont     lipgloss.Style
	Mode     lipgloss.Style
	ModeCont lipgloss.Style
	Err      lipgloss.Style
	Dim      lipgloss.Style
	Search   lipgloss.Style

	// The CLI: gluon doctor, and the subcommand reports.
	Heading lipgloss.Style
	OK      lipgloss.Style
	Fail    lipgloss.Style
	Path    lipgloss.Style

	// Source. These share their colours with the roles above — a string is one
	// colour whether it is a value or a literal — and differ only in weight:
	// SrcType drops the bold that Type carries, because a table has one type
	// per row and a screen of source has dozens.
	Keyword lipgloss.Style
	Comment lipgloss.Style
	Builtin lipgloss.Style
	Punct   lipgloss.Style
	Ident   lipgloss.Style
	SrcType lipgloss.Style
}

// Styles is the subset internal/pretty and internal/inspect already take, so
// their signatures do not change.
func (t Theme) Styles() pretty.Styles {
	return pretty.Styles{
		Type:   t.Type,
		Annot:  t.Annot,
		Note:   t.Note,
		Str:    t.Str,
		Num:    t.Num,
		Header: t.Header,
		Index:  t.Index,
		Border: t.Border,
	}
}

// Syntax is the palette the source highlighter paints with.
//
// It is escape strings rather than styles because internal/syntax must not link
// lipgloss: lipgloss.Style.Render converts every tab to four spaces on every
// path it has, rewrites \r\n to \n, and pads each line of a multi-line string
// out to the widest. gofmt indents with tabs, so a highlighter that called
// Render would silently reformat everything :src prints. Deriving the sequences
// once, here, is what keeps that impossible.
func (t Theme) Syntax() syntax.Palette {
	var p syntax.Palette
	if !t.Colour {
		return p
	}
	for role, style := range t.SyntaxStyles() {
		open, closing := seqs(style)
		if open == "" {
			continue
		}
		p.Open[role], p.Close = open, closing
	}
	return p
}

// SyntaxStyles is the same mapping as lipgloss styles, for the one caller that
// needs a style rather than a sequence: the REPL's cursor, which has to take
// the colour of the token it is standing in and can only get that by handing a
// style to bubbles/cursor.
func (t Theme) SyntaxStyles() [syntax.NumRoles]lipgloss.Style {
	var out [syntax.NumRoles]lipgloss.Style
	out[syntax.RoleKeyword] = t.Keyword
	out[syntax.RoleString] = t.Str
	out[syntax.RoleType] = t.SrcType
	out[syntax.RoleComment] = t.Comment
	out[syntax.RoleNumber] = t.Num
	out[syntax.RoleBuiltin] = t.Builtin
	out[syntax.RolePunct] = t.Punct
	out[syntax.RoleIdent] = t.Ident
	return out
}

// seqs splits a style into the sequence it opens with and the one it closes
// with, by rendering a byte that cannot occur in source and cutting there.
//
// lipgloss exposes no accessor for this, and rendering the source itself is not
// an option — see Syntax. Everything that could make Render add or move a byte
// is unset first, so a style that somehow carried a layout property degrades to
// a plain colour rather than to a corrupt line.
func seqs(s lipgloss.Style) (open, closing string) {
	s = s.UnsetPadding().UnsetMargins().UnsetWidth().UnsetHeight().
		UnsetAlign().UnsetMaxWidth().UnsetMaxHeight().UnsetInline().
		TabWidth(lipgloss.NoTabConversion)
	const sentinel = "\x00"
	out := s.Render(sentinel)
	i := strings.Index(out, sentinel)
	if i <= 0 {
		// Either Render painted nothing — the Ascii profile, which is every
		// pipe and every test — or it did something unexpected. Both mean
		// "do not paint".
		return "", ""
	}
	return out[:i], out[i+len(sentinel):]
}

// New builds the theme for a destination. Pass the file output will go to —
// os.Stdout for the CLI, os.Stdin for the REPL, which is what decides raw mode
// there — or nil to force colour off.
//
// A theme gluon cannot find is not an error here: see NewWithError.
func New(cfg *config.Config, out *os.File) Theme {
	t, _ := NewWithError(cfg, out)
	return t
}

// NewWithError is New, and also whatever went wrong choosing the palette.
//
// A named theme that does not exist is reported rather than fatal, unlike a
// misspelled role. A role is a typo in a key, decidable from the config file
// alone, and it stays wrong forever; a missing theme file is a fact about a
// directory that changes independently of the config — one deleted, or a config
// synced from another machine. Refusing to start a REPL over a colour would be
// worse than the problem. `gluon doctor` names it instead.
func NewWithError(cfg *config.Config, out *os.File) (Theme, error) {
	name, overrides := "", map[string]string(nil)
	if cfg != nil {
		name, overrides = cfg.ThemeName, cfg.Theme
	}
	p, err := theme.Resolve(name, overrides, theme.Dir())
	if name == "" {
		name = theme.Default
	}
	t := build(p, colourFor(out))
	t.Name = name
	return t, err
}

// FromPalette builds a theme from a palette directly, for `gluon theme show`:
// it renders a palette the config has not selected, which is the whole point of
// being able to look at one before switching to it.
func FromPalette(p theme.Palette, colour bool) Theme { return build(p, colour) }

// RoleStyles is every configurable role as the style it resolves to, keyed by
// the name a config file writes. `gluon theme show` draws its swatches with
// these, so what it prints is what the terminal will actually do with the
// value rather than gluon's opinion of it.
func (t Theme) RoleStyles() map[string]lipgloss.Style {
	return map[string]lipgloss.Style{
		"type": t.SrcType, "annotation": t.Annot, "note": t.Note,
		"string": t.Str, "number": t.Num, "border": t.Border,
		"prompt": t.Prompt, "mode": t.Mode, "error": t.Err,
		"dim": t.Dim, "search": t.Search,
		"keyword": t.Keyword, "comment": t.Comment, "builtin": t.Builtin,
		"punctuation": t.Punct, "ident": t.Ident,
	}
}

// Plain is the uncoloured theme. Pipes, NO_COLOR and tests get this.
func Plain() Theme {
	p, _ := theme.Resolve("", nil, "")
	return build(p, false)
}

// colourFor applies the three signals in the order everyone else does:
// NO_COLOR wins over everything, then CLICOLOR_FORCE, then whether the
// destination is actually a terminal.
//
// NO_COLOR is honoured for being *set*, empty or not — that is what the
// convention says, and treating NO_COLOR= as "colour on" would be a surprise.
// termenv disagrees about the empty case, which is exactly why build applies
// bold inside the colour gate rather than outside it: the terminal's profile
// must never be the only thing keeping escapes out of a pipe.
func colourFor(out *os.File) bool {
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return false
	}
	if v := os.Getenv("CLICOLOR_FORCE"); v != "" && v != "0" {
		return true
	}
	if out == nil {
		return false
	}
	return term.IsTerminal(int(out.Fd()))
}

func build(p theme.Palette, colour bool) Theme {
	// c is the colour for a role, or a bare style. An empty value and the
	// explicit "none" both mean "leave the terminal's own foreground alone" —
	// calling Foreground(Color("")) instead would set the property to a colour
	// termenv resolves to nil, which lipgloss's `fg != noColor` guard does not
	// catch.
	c := func(role string) lipgloss.Style {
		s := lipgloss.NewStyle()
		v := p[role]
		if !colour || v == "" || v == theme.None {
			return s
		}
		return s.Foreground(lipgloss.Color(v))
	}
	// bold is inside the colour gate. Outside it, `NO_COLOR= gluon doctor` on a
	// terminal emits ESC[1m: gluon honours a set-but-empty NO_COLOR and termenv
	// does not, so the profile is not enough on its own.
	bold := func(s lipgloss.Style) lipgloss.Style {
		if !colour {
			return s
		}
		return s.Bold(true)
	}

	return Theme{
		Colour: colour,
		Type:   bold(c("type")),
		Annot:  c("annotation"),
		Note:   c("note"),
		Str:    c("string"),
		Num:    c("number"),
		Header: bold(c("annotation")).Padding(0, 1),
		Index:  c("annotation").Padding(0, 1),
		Border: c("border"),

		Prompt: bold(c("prompt")),
		Cont:   c("dim"),
		// Mode carries Prompt's bold and ModeCont carries Cont's plainness, so
		// switching mode changes the colour and never the weight: the main
		// prompt stays louder than a continuation in both of them.
		Mode:     bold(c("mode")),
		ModeCont: c("mode"),
		Err:      c("error"),
		Dim:      c("dim"),
		Search:   c("search"),

		Heading: bold(c("type")),
		OK:      c("string"),
		Fail:    c("error"),
		Path:    c("annotation"),

		Keyword: c("keyword"),
		Comment: c("comment"),
		Builtin: c("builtin"),
		Punct:   c("punctuation"),
		Ident:   c("ident"),
		SrcType: c("type"),
	}
}

// Roles is every role name a config file may set. config.validate uses it so a
// misspelled role is an error rather than a setting that silently does nothing.
func Roles() []string { return theme.Roles() }

// Named builds one palette by name, with the config's own per-role overrides
// still applied on top of it.
//
// It is what a driver that switches palettes while it runs calls: the REPL's
// `:theme` previews a theme the config has not selected, and then keeps it.
// Overrides are passed in rather than read from a *config.Config because the
// caller doing the previewing — the UI goroutine — must not be reading a Core
// the evaluation goroutine owns.
//
// A theme that will not load is reported the way NewWithError reports one: the
// palette handed back is complete regardless, so a caller may paint with it and
// say what went wrong.
func Named(name string, overrides map[string]string, colour bool) (Theme, error) {
	p, err := theme.Resolve(name, overrides, theme.Dir())
	if name == "" {
		name = theme.Default
	}
	t := build(p, colour)
	t.Name = name
	return t, err
}
