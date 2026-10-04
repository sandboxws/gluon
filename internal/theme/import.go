package theme

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"sort"
	"strings"
)

// Import converts an editor theme into a gluon one.
//
// Two formats, one model. A TextMate .tmTheme is a plist and a VS Code theme is
// JSON, but both describe the same thing — a list of rules, each naming some
// TextMate scopes and a colour — so both are read into tmTheme below and mapped
// once.
//
// The mapping is lossy in one direction and cannot be otherwise: roughly half
// of gluon's palette (border, search, mode, prompt, dim, note) describes a terminal
// program rather than a syntax highlighter, and no TextMate scope means "the
// reverse-i-search prompt". Those keep gluon's own values, and the report says
// which — a converter that silently invented them would be guessing in a place
// the user cannot see.
func Import(name string, data []byte) (File, Report, error) {
	var tm tmTheme
	var err error
	switch detect(data) {
	case formatPlist:
		tm, err = readPlist(data)
	case formatJSON:
		tm, err = readVSCode(data)
	case formatIntelliJ:
		// The one format that does not go through tmTheme. It has no scopes to
		// arbitrate — see intellij.go — so putting it through a scope matcher
		// would mean inventing scopes in order to look them up again.
		return readIntelliJ(data, name)
	default:
		return File{}, Report{}, fmt.Errorf(
			"not a theme gluon can read: expected a TextMate .tmTheme (XML plist), " +
				"a VS Code theme (JSON), or an IntelliJ colour scheme (XML)")
	}
	if err != nil {
		return File{}, Report{}, err
	}
	return tm.toFile(name)
}

// A Report says where each role's colour came from, so `gluon theme import` can
// print it and the user can see every guess.
type Report struct {
	Rows []ReportRow
	// Mapped is how many roles the source theme actually decided.
	Mapped int
	// Source names the theme as it named itself, when it did.
	Source string
	Format string
	// Appearance is the ground the conversion decided the theme was drawn for,
	// which is what chose the values it kept.
	Appearance string
}

// A ReportRow is one role and its provenance.
type ReportRow struct {
	Role  string
	Value string
	// From is the scope that decided it, or a short phrase when nothing did.
	From string
	// Kept is true when gluon's own value survived.
	Kept bool
}

type format int

const (
	formatUnknown format = iota
	formatPlist
	formatJSON
	formatIntelliJ
)

// detect reads the format from the bytes rather than the extension, because a
// theme downloaded from anywhere is as likely to be named .json as .tmTheme and
// the contents are unambiguous.
func detect(data []byte) format {
	b := bytes.TrimLeft(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), " \t\r\n")
	// Comments are skipped before the first byte decides anything, because a VS
	// Code theme is JSONC and a published one often opens with a comment rather
	// than a brace — the stock Monokai names the six greys it was built from
	// before it says a word of JSON. stripJSONC below already knew this; detect
	// ran first and refused the file as "not a theme gluon can read".
	b = skipComments(b)
	switch {
	case len(b) == 0:
		return formatUnknown
	case b[0] == '<':
		return xmlFormat(b)
	case b[0] == '{':
		return formatJSON
	}
	return formatUnknown
}

// skipComments drops leading whitespace and // and /* */ comments, and stops at
// the first byte that is neither.
func skipComments(b []byte) []byte {
	for {
		b = bytes.TrimLeft(b, " \t\r\n")
		switch {
		case len(b) < 2 || b[0] != '/':
			return b
		case b[1] == '/':
			if i := bytes.IndexByte(b, '\n'); i >= 0 {
				b = b[i+1:]
			} else {
				return nil
			}
		case b[1] == '*':
			if i := bytes.Index(b[2:], []byte("*/")); i >= 0 {
				b = b[2+i+2:]
			} else {
				return nil
			}
		default:
			return b
		}
	}
}

// xmlFormat tells the two XML dialects apart by their root element, which is
// the only thing about them that cannot be a coincidence: a .tmTheme opens
// <plist> under a DOCTYPE, an IntelliJ scheme opens <scheme>. Reading tokens
// rather than searching the bytes is what keeps the word "scheme" inside a
// comment or a colour name from deciding anything.
func xmlFormat(b []byte) format {
	dec := xml.NewDecoder(bytes.NewReader(b))
	dec.Strict = false
	for {
		tok, err := dec.Token()
		if err != nil {
			return formatUnknown
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue // the declaration, the DOCTYPE directive, a comment
		}
		switch start.Name.Local {
		case "plist":
			return formatPlist
		case "scheme":
			return formatIntelliJ
		}
		return formatUnknown
	}
}

// tmTheme is the shape both formats share.
type tmTheme struct {
	Name   string
	Format string
	// Appearance is the source theme's own word for the ground it was drawn
	// for, exactly as written — "dark", "Darcula". It is the fallback for when
	// there is no background to read, and no more than that.
	Appearance string
	// Global is the settings of the rule with no scope: the editor's own
	// foreground and background.
	Global map[string]string
	Rules  []tmRule
	// Extra carries a VS Code theme's `colors` object, which has UI colours a
	// .tmTheme has no way to express.
	Extra map[string]string
}

type tmRule struct {
	Scopes   []string
	Settings map[string]string
}

// scopes maps a gluon role to the TextMate scopes that might decide it, best
// first. The first candidate that resolves wins.
//
// Order matters more than completeness: `keyword.control` before `keyword`
// because a theme that distinguishes them means the distinction, and gluon has
// one keyword colour to spend.
var scopes = []struct {
	role       string
	candidates []string
}{
	{"keyword", []string{"keyword.control", "keyword.other", "keyword", "storage.modifier", "storage"}},
	{"string", []string{"string.quoted.double", "string.quoted", "string"}},
	{"type", []string{"entity.name.type", "support.type", "storage.type", "entity.name.class", "support.class"}},
	{"comment", []string{"comment.line", "comment.block", "comment"}},
	{"number", []string{"constant.numeric", "constant.language", "constant"}},
	{"builtin", []string{"support.function.builtin", "support.function", "entity.name.function", "meta.function-call"}},
	// keyword.operator is deliberately not a candidate. TextMate inheritance
	// would let a theme's generic `keyword` rule claim it — faithful to the
	// editor, and wrong here: punctuation is on every line of a REPL, gluon's
	// own palette makes it deliberately quiet, and a screen of Go painted in
	// keyword colour is loud in a way the theme's author never chose. An
	// explicit punctuation scope is honoured; otherwise this falls through to
	// the theme's own foreground below, which is what unstyled text is.
	{"punctuation", []string{"punctuation.separator", "punctuation", "meta.brace"}},
	// Only reached when the theme states no foreground of its own. gluon's
	// `ident` is every identifier a line of Go has — a package name, a field, a
	// function being declared — and not just the locals a `variable` rule means.
	// Themes that accent variables are common (Dracula's pink, One's red,
	// Catppuccin's maroon) and taking that here paints a whole screen in it,
	// which is the same mistake the punctuation note below is about. toFile
	// puts the editor's own foreground in first, and this is what answers when
	// there is none.
	{"ident", []string{"variable.other", "variable", "source"}},
	{"error", []string{"invalid.illegal", "invalid", "message.error"}},
	// note is deliberately not mapped. The tempting candidate is
	// invalid.deprecated, but TextMate inheritance means a theme with only an
	// `invalid` rule hands note the *error* colour — amber becomes red, and
	// gluon uses note for the quiet annotations beside a value ("3 items"),
	// where red says something is wrong when nothing is. Same reasoning as
	// search: no scope in any editor theme means what these mean, and a
	// converter that guessed would be guessing where the user cannot see.
}

// derived roles take whatever another role resolved to. They are not guesses so
// much as gluon's own relationships: an annotation has always been the colour a
// comment is, and the prompt has always been the colour a type is.
var derived = []struct{ role, from string }{
	{"annotation", "comment"},
	{"prompt", "type"},
}

// uiColors are the VS Code `colors` keys worth taking. A .tmTheme has no
// equivalent, so these simply do not fire for one.
var uiColors = map[string][]string{
	"dim":    {"editorLineNumber.foreground", "editorCodeLens.foreground"},
	"border": {"panel.border", "editorGroup.border", "editorWidget.border"},
}

func (tm tmTheme) toFile(name string) (File, Report, error) {
	pal := make(Palette, len(roles))
	from := map[string]string{}
	bg := normaliseHex(tm.Global["background"])
	fg := normaliseHex(tm.Global["foreground"])

	// The editor's own foreground is what unscoped text is, which is what an
	// identifier is in every language gluon paints — so it goes on before the
	// scope table rather than after it. That ordering is the whole of the fix:
	// the rule was always written down here, and the lookup ran first and won.
	if fg != "" {
		pal["ident"], from["ident"] = fg, "the theme's own foreground"
	}
	for _, s := range scopes {
		if pal[s.role] != "" {
			continue
		}
		if v, scope := tm.look(s.candidates, s.role != "punctuation"); v != "" {
			pal[s.role], from[s.role] = v, scope
		}
	}
	if fg != "" && pal["punctuation"] == "" {
		pal["punctuation"], from["punctuation"] = fg, "the theme's own foreground"
	}
	// `error` is the one role whose whole job is to not look like ordinary
	// output, so it is the one role that can resolve to something and still have
	// resolved to nothing. An `invalid` rule usually means "red behind, page
	// colour in front", and gluon takes only the front: Dracula hands over
	// #F8F8F0, Nord #D8DEE9, gruvbox its #fbf1c7 — in each case the colour the
	// rest of the line is already painted in.
	if v, ok := pal["error"]; ok && (sameColour(v, fg) || sameColour(v, bg)) {
		delete(pal, "error")
		from["error"] = ""
	}
	for role, keys := range uiColors {
		for _, k := range keys {
			if v := normaliseHex(tm.Extra[k]); v != "" && pal[role] == "" {
				pal[role], from[role] = v, k
				break
			}
		}
	}
	// A role that resolved to the theme's own background is dropped, because it
	// resolved to nothing: gluon paints no background, so a colour equal to one
	// is a colour on top of itself. It happens for real and in two ways — an
	// `invalid` rule that says "red behind, page colour in front" hands `error`
	// the page colour (Dracula's #F8F8F0, gruvbox's #fbf1c7), and a
	// `panel.border` drawn on a dark editor chrome hands `border` something
	// near-black (Tokyo Night's #101014). Both are honest readings of the
	// source and both are invisible here, so gluon's own value is kept instead
	// and the report says which.
	if bg != "" {
		for role, v := range pal {
			if sameColour(v, bg) {
				delete(pal, role)
				from[role] = ""
			}
		}
	}

	// The background decides, and the theme's own word for itself is only the
	// fallback. That is the opposite of the obvious order and Tokyo Night Light
	// is why: it ships `"type": "dark"` over an #e6e7ed background. A label can
	// be wrong because nothing reads it; the background is the ground the
	// colours were actually chosen against, which is what `appearance` means.
	appearance := groundOf(bg)
	if appearance == "" {
		appearance = appearanceOf(tm.Appearance)
	}
	return fill(name, tm.Name, tm.Format, appearance, pal, from)
}

// fill completes a half-mapped palette and says where every role came from.
//
// It is the part that is the same whichever format was read: gluon's own
// relationships between roles, then its own values for whatever the source
// theme had no answer for, then the report. A format reader's whole job is to
// decide as many roles as it honestly can and hand the rest to this.
func fill(name, source, format, appearance string, pal Palette, from map[string]string) (File, Report, error) {
	// Which of gluon's own themes the leftovers come from is decided by the
	// ground, and it has to be: `search` and `note` are amber because amber
	// reads on black, and the same two values on a white terminal are a pale
	// smear. A light conversion that inherited them would be a light theme with
	// two roles nobody can see.
	baseName := Default
	if appearance == Light {
		baseName = DefaultLight
	}
	base, ok := Builtin(baseName)
	if !ok {
		return File{}, Report{}, fmt.Errorf("the built-in %q theme is missing", baseName)
	}
	for _, d := range derived {
		if pal[d.role] == "" && pal[d.from] != "" {
			pal[d.role], from[d.role] = pal[d.from], "derived from "+d.from
		}
	}

	// A colour that cannot be seen on the ground this theme was drawn for did
	// not survive the conversion, whatever the source meant by it. Tokyo Night's
	// `panel.border` is #101014 against a #1a1b26 editor — a real border in a
	// window that has its own chrome, and a rule drawn in the dark on a terminal
	// that has none.
	for role, v := range pal {
		if !Readable(role, appearance, v) {
			delete(pal, role)
			from[role] = ""
		}
	}

	rep := Report{Source: source, Format: format, Appearance: appearance}
	for _, role := range Roles() {
		if pal[role] == "" {
			// Filled from gluon's own theme rather than left out, so the file
			// written is always complete and every guess is on screen where it
			// can be edited.
			pal[role] = base.Palette[role]
			rep.Rows = append(rep.Rows, ReportRow{Role: role, Value: pal[role],
				From: "gluon's " + baseName, Kept: true})
			continue
		}
		rep.Mapped++
		rep.Rows = append(rep.Rows, ReportRow{Role: role, Value: pal[role], From: from[role]})
	}
	sort.Slice(rep.Rows, func(i, j int) bool { return rep.Rows[i].Role < rep.Rows[j].Role })

	about := source
	if about == "" {
		about = name
	}
	return File{
		Name:       name,
		About:      "imported from " + about,
		Appearance: appearance,
		Source:     source,
		Palette:    pal,
	}, rep, nil
}

// lookup finds the foreground for the first candidate scope any rule matches.
//
// A rule's scope matches a candidate when it is that scope or an ancestor of it
// — `keyword` decides `keyword.control` — and among the rules that match, the
// most specific one wins, which is TextMate's own rule and the only one that
// gives a theme what it asked for.
//
// Three passes, in decreasing order of how much the theme actually said:
//
//  1. Rules with no descendant context. `punctuation` means punctuation.
//  2. Rules with one. `source.elixir readwrite.module punctuation` is keyed on
//     its last element, because that is the scope being styled — but it is what
//     punctuation looks like *in an Elixir module*, and letting it answer the
//     general question is how One Light hands gluon a red comma for every
//     language. It answers only when nothing context-free did.
//  3. Anything beneath the most general candidate. A theme that styles
//     `string.quoted.single` and never `string` has said what a string looks
//     like; the first two passes only ever walk up, so without this gruvbox's
//     strings come out the colour of ordinary text.
func (tm tmTheme) lookup(candidates []string) (colour, scope string) {
	return tm.look(candidates, true)
}

// look is lookup with pass 2 optional. It is skipped for `ident` and
// `punctuation`, the two roles that are on every line of every language: a rule
// written for one language is a poor answer to a question asked about all of
// them, and the theme's own foreground — which toFile offers next — is a much
// better one. One Light's `source.elixir readwrite.module punctuation` is the
// case in point, and it is why every comma in a Go session came out red.
func (tm tmTheme) look(candidates []string, contextual bool) (colour, scope string) {
	passes := []bool{false}
	if contextual {
		passes = append(passes, true)
	}
	for _, ctx := range passes {
		for _, want := range candidates {
			if v, sc := tm.match(want, ctx); v != "" {
				return v, sc
			}
		}
	}
	if n := len(candidates); n > 0 {
		return tm.beneath(candidates[n-1])
	}
	return "", ""
}

// foreground is what a rule contributes, which is nothing when the rule's
// meaning is in its background.
//
// `invalid` is written the same way in theme after theme — white ink on a red
// field — and gluon takes only the ink, because a terminal owns backgrounds.
// So the red that made it an error is exactly the half gluon drops, and the
// half it keeps is the colour of ordinary text: Dracula's #F8F8F0 on #ff79c6,
// gruvbox's #282828 on #9d0006. A rule that highlights has not said what
// colour the thing is, so it is not asked.
func (tm tmTheme) foreground(r tmRule) string {
	fg := normaliseHex(r.Settings["foreground"])
	if fg == "" {
		return ""
	}
	// Only a background that differs from the editor's own is a highlight; one
	// that restates it is a rule saying "ordinary ground", which decides nothing.
	if rb := normaliseHex(r.Settings["background"]); rb != "" {
		if tb := normaliseHex(tm.Global["background"]); tb == "" || !sameColour(rb, tb) {
			return ""
		}
	}
	return fg
}

// match is one pass: the most specific rule whose scope is want or an ancestor
// of it, among rules that either do or do not carry descendant context.
func (tm tmTheme) match(want string, contextual bool) (colour, scope string) {
	best, bestLen := "", -1
	for _, r := range tm.Rules {
		fg := tm.foreground(r)
		if fg == "" {
			continue
		}
		for _, raw := range r.Scopes {
			s, ctx := lastScope(raw)
			if s == "" || ctx != contextual {
				continue
			}
			if s != want && !strings.HasPrefix(want, s+".") {
				continue
			}
			if len(s) > bestLen {
				best, bestLen = fg, len(s)
				scope = s
			}
		}
	}
	return best, scope
}

// beneath answers a general question from a specific rule: the shortest scope
// under want, context-free first.
//
// `source` is excluded, and it is the reason this takes the candidate rather
// than looping over all of them. Every rule in a theme is beneath `source` —
// it names the document, not a kind of token — so a pass over it would hand
// `ident` whatever rule happened to sort first.
func (tm tmTheme) beneath(want string) (colour, scope string) {
	if want == "source" {
		return "", ""
	}
	for _, contextual := range []bool{false, true} {
		best, bestLen := "", -1
		for _, r := range tm.Rules {
			fg := tm.foreground(r)
			if fg == "" {
				continue
			}
			for _, raw := range r.Scopes {
				s, ctx := lastScope(raw)
				if s == "" || ctx != contextual || !strings.HasPrefix(s, want+".") {
					continue
				}
				if bestLen < 0 || len(s) < bestLen {
					best, bestLen = fg, len(s)
					scope = s
				}
			}
		}
		if best != "" {
			return best, scope
		}
	}
	return "", ""
}

// lastScope reduces a selector to the scope it styles, and says whether
// anything was in front of it. Both separators occur in real themes, sometimes
// in the same selector: "meta.key-pair > punctuation", "markup.heading
// punctuation.definition.heading".
func lastScope(s string) (scope string, contextual bool) {
	if i := strings.LastIndexAny(s, " \t>"); i >= 0 {
		s, contextual = s[i+1:], true
	}
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "-") {
		return "", contextual // a negation, which gluon cannot honour
	}
	return s, contextual
}

// normaliseHex keeps #rrggbb and drops everything else.
//
// An editor theme may write #rgb, or #rrggbbaa with an alpha channel, and a
// terminal has no alpha: the eight-digit form is truncated to its opaque part
// rather than refused, because dropping the role entirely over a transparency
// nobody asked about would be worse.
func normaliseHex(v string) string {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "#") {
		return ""
	}
	d := v[1:]
	for _, c := range d {
		if !isHexRune(c) {
			return ""
		}
	}
	switch len(d) {
	case 3:
		return "#" + string([]byte{d[0], d[0], d[1], d[1], d[2], d[2]})
	case 6, 8:
		return "#" + d[:6]
	}
	return ""
}

func isHexRune(c rune) bool {
	return (c >= '0' && c <= '9') || (c|0x20 >= 'a' && c|0x20 <= 'f')
}

// Encode writes a theme back out as the TOML a user can read and edit.
//
// Hand-written rather than through BurntSushi's encoder, for the reason the
// ROADMAP already records about config files: the encoder emits a whole
// document with comments dropped and keys reordered, and the point of a theme
// file is that a person opens it.
func (f File) Encode() string {
	var b strings.Builder
	// Widest of the three, so the `=` line up the way they do inside [theme].
	head := func(key, val string) {
		if val != "" {
			b.WriteString(key + strings.Repeat(" ", 10-len(key)) + " = " + quoteTOML(val) + "\n")
		}
	}
	head("about", f.About)
	head("appearance", f.Appearance)
	head("source", f.Source)
	b.WriteString("\n[theme]\n")
	width := 0
	for _, role := range Roles() {
		if len(role) > width {
			width = len(role)
		}
	}
	for _, role := range Roles() {
		v, ok := f.Palette[role]
		if !ok {
			continue
		}
		b.WriteString(role + strings.Repeat(" ", width-len(role)) + " = " + quoteTOML(v) + "\n")
	}
	return b.String()
}

func quoteTOML(s string) string {
	out, err := json.Marshal(s) // TOML basic strings are JSON strings for this subset
	if err != nil {
		return `""`
	}
	return string(out)
}
