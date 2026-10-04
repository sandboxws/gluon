package theme

import (
	"bytes"
	"encoding/xml"
	"fmt"
)

// An IntelliJ editor colour scheme is the third format worth reading, and it is
// the one that answers questions TextMate cannot.
//
// A .tmTheme and a VS Code theme describe syntax and almost nothing else, which
// is why import.go's mapping is lossy in one direction: roughly a third of
// gluon's palette describes a terminal program, and no TextMate scope means
// "the prompt". An IntelliJ scheme is a whole editor's colours — it has a caret,
// line numbers, a console with its own error and warning foregrounds — so three
// of the roles that were guesses become facts here, and the report says so.
//
// The format is flat where TextMate is hierarchical: a scheme names attributes
// (DEFAULT_KEYWORD, DEFAULT_STRING) rather than dotted scopes, so there is no
// inheritance to resolve and no specificity to arbitrate. `baseAttributes` is
// the one exception — an alias saying "this is coloured like that" — and it is
// followed with a bound, because a scheme is a file gluon did not write and a
// cycle in it must not become a loop in here.
type ijScheme struct {
	XMLName xml.Name `xml:"scheme"`
	Name    string   `xml:"name,attr"`
	// Parent is Darcula for a dark scheme and Default for a light one. It used
	// to be recorded in the report and decide nothing, on the reasoning that a
	// terminal has its own background — which is still true of the background
	// and was never true of the palette: `appearance` is what a theme drawn for
	// white has to say so that the listing can, and this is a scheme saying it.
	// The scheme's own BACKGROUND is the check and the fallback.
	Parent string `xml:"parent_scheme,attr"`
	Colors struct {
		Options []ijOption `xml:"option"`
	} `xml:"colors"`
	Attributes struct {
		Options []ijAttribute `xml:"option"`
	} `xml:"attributes"`
}

type ijOption struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type ijAttribute struct {
	Name  string `xml:"name,attr"`
	Base  string `xml:"baseAttributes,attr"`
	Value struct {
		Options []ijOption `xml:"option"`
	} `xml:"value"`
}

// ijAttrs maps a gluon role to the attributes that might decide it, best first.
//
// The order carries the same judgement import.go's scope table does. Two
// choices are worth the sentence they cost:
//
// DEFAULT_OPERATION_SIGN is deliberately not a candidate for punctuation, for
// exactly the reason keyword.operator is not one on the TextMate side. In every
// scheme read while writing this it is a saturated accent — the colour an
// operator gets to stand out — and punctuation is on every line of a REPL,
// where gluon's own palette makes it deliberately quiet. DEFAULT_PARENTHS and
// its neighbours are the grey these schemes actually use for the marks between
// things, so those are what gluon takes.
//
// DEFAULT_BRACES is last for the same reason and a smaller one: several schemes
// paint braces as an accent while commas and dots stay grey. A screen of Go is
// mostly the grey ones.
var ijAttrs = []struct {
	role string
	keys []string
}{
	{"keyword", []string{"DEFAULT_KEYWORD"}},
	{"string", []string{"DEFAULT_STRING"}},
	{"type", []string{"DEFAULT_CLASS_NAME", "DEFAULT_CLASS_REFERENCE", "DEFAULT_INTERFACE_NAME"}},
	{"comment", []string{"DEFAULT_LINE_COMMENT", "DEFAULT_BLOCK_COMMENT", "DEFAULT_DOC_COMMENT"}},
	{"number", []string{"DEFAULT_NUMBER", "DEFAULT_CONSTANT"}},
	{"builtin", []string{"DEFAULT_FUNCTION_CALL", "DEFAULT_FUNCTION_DECLARATION", "DEFAULT_STATIC_METHOD"}},
	{"punctuation", []string{"DEFAULT_PARENTHS", "DEFAULT_COMMA", "DEFAULT_SEMICOLON", "DEFAULT_DOT", "DEFAULT_BRACES"}},
	{"ident", []string{"DEFAULT_IDENTIFIER", "TEXT"}},
	// A console's error and warning foregrounds are what an editor paints
	// *prose* in, which is what gluon's error and note are. This is the part a
	// TextMate theme cannot answer: `invalid.illegal` is a squiggle under code,
	// and import.go refuses to map note at all rather than hand it red.
	{"error", []string{"CONSOLE_ERROR_OUTPUT", "CONSOLE_RED_OUTPUT", "LOG_ERROR_OUTPUT"}},
	{"note", []string{"CONSOLE_YELLOW_OUTPUT", "LOG_WARNING_OUTPUT"}},
}

// ijColors maps the roles that describe the editor rather than the language.
//
// prompt takes the caret, which is the theme's own answer to "where you are
// typing" — the closest thing any editor format has to gluon's prompt, and the
// reason this importer guesses less than the other one.
//
// dim and border split the two line-number colours, and the split is the point.
// dim is furniture you read — the continuation prompt, ghost text, a modal
// footer — so it takes the *bright* line number, the one on the caret's row.
// border is a rule you look past, so it takes the ordinary one. Taking
// TEARLINE_COLOR for the border instead would be faithful to the editor and
// wrong here: it is a hairline drawn on a lit panel, and gluon draws box
// characters against a terminal background that is often black.
//
// search and mode are still not mapped. A search *result* in an editor is a
// background highlight, and gluon's search role paints the reverse-i-search
// prompt itself; mode paints that same prompt while vim's normal mode has it.
// Nothing here means either, so gluon's own values are kept and the report says
// so.
var ijColors = []struct {
	role string
	keys []string
}{
	{"prompt", []string{"CARET_COLOR"}},
	{"dim", []string{"LINE_NUMBER_ON_CARET_ROW_COLOR", "LINE_NUMBERS_COLOR"}},
	{"border", []string{"LINE_NUMBERS_COLOR", "ANNOTATIONS_COLOR", "TEARLINE_COLOR"}},
}

func readIntelliJ(data []byte, name string) (File, Report, error) {
	var s ijScheme
	dec := xml.NewDecoder(bytes.NewReader(data))
	// Not strict, for the reason a converter is never strict about a file it
	// did not write: real schemes carry stray characters between elements —
	// one of the themes this was built against has a lone quote after a
	// closing tag — and refusing a whole palette over a typo in a comment
	// would be refusing to do the job.
	dec.Strict = false
	if err := dec.Decode(&s); err != nil {
		return File{}, Report{}, fmt.Errorf("not an IntelliJ colour scheme: %w", err)
	}
	if len(s.Attributes.Options) == 0 && len(s.Colors.Options) == 0 {
		return File{}, Report{}, fmt.Errorf("no <attributes> and no <colors>: nothing to convert")
	}

	fg := s.foregrounds()
	colours := s.colours()
	pal := make(Palette, len(roles))
	from := map[string]string{}

	for _, a := range ijAttrs {
		for _, k := range a.keys {
			if v := ijHex(fg[k]); v != "" {
				pal[a.role], from[a.role] = v, k
				break
			}
		}
	}
	for _, c := range ijColors {
		for _, k := range c.keys {
			if v := ijHex(colours[k]); v != "" {
				pal[c.role], from[c.role] = v, k
				break
			}
		}
	}

	format := "IntelliJ"
	if s.Parent != "" {
		format = "IntelliJ, " + s.Parent
	}
	// BACKGROUND first, parent_scheme second — see toFile, and Tokyo Night
	// Light, for why the scheme's own claim is the weaker of the two.
	appearance := groundOf(ijHex(colours["BACKGROUND"]))
	if appearance == "" {
		appearance = appearanceOf(s.Parent)
	}
	return fill(name, s.Name, format, appearance, pal, from)
}

// foregrounds is every attribute's foreground, with baseAttributes followed.
//
// An alias may point at another alias, so this resolves iteratively and stops:
// the bound is the number of attributes, which is more hops than any honest
// chain needs and fewer than a cycle would take.
func (s ijScheme) foregrounds() map[string]string {
	direct := map[string]string{}
	alias := map[string]string{}
	for _, a := range s.Attributes.Options {
		if a.Name == "" {
			continue
		}
		for _, o := range a.Value.Options {
			if o.Name == "FOREGROUND" && o.Value != "" {
				direct[a.Name] = o.Value
			}
		}
		if _, ok := direct[a.Name]; !ok && a.Base != "" {
			alias[a.Name] = a.Base
		}
	}
	for range s.Attributes.Options {
		progress := false
		for name, base := range alias {
			if v, ok := direct[base]; ok {
				direct[name] = v
				delete(alias, name)
				progress = true
			}
		}
		if !progress {
			break
		}
	}
	return direct
}

// colours is the <colors> block, first value wins. A scheme sometimes sets the
// same key twice with the second commented out in the middle of an edit, and
// the live one is the first.
func (s ijScheme) colours() map[string]string {
	out := make(map[string]string, len(s.Colors.Options))
	for _, o := range s.Colors.Options {
		if o.Name == "" || o.Value == "" {
			continue
		}
		if _, seen := out[o.Name]; !seen {
			out[o.Name] = o.Value
		}
	}
	return out
}

// ijHex is normaliseHex for a format that writes its colours without the hash.
// The eight-digit form carries an alpha channel a terminal has no notion of,
// and normaliseHex already drops it.
func ijHex(v string) string {
	if v == "" {
		return ""
	}
	return normaliseHex("#" + v)
}
