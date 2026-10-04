package docgen

import (
	"html"
	"html/template"
	"regexp"
	"strings"

	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/syntax"
)

// roleClass is the site's span for each syntax role — the classes the
// hand-written transcripts already use, so a generated example and a typed one
// are painted alike.
var roleClass = map[syntax.Role]string{
	syntax.RoleKeyword: "kw",
	syntax.RoleString:  "s",
	syntax.RoleType:    "ty",
	syntax.RoleComment: "c",
	syntax.RoleNumber:  "nu",
	syntax.RoleBuiltin: "f",
	syntax.RolePunct:   "p",
}

// paintLine is an example line as the prompt paints it: the command a keyword,
// and its argument in the language the command reads — Go through gluon's own
// tokenizer, SQL through its SQL one, and words as they are.
func paintLine(line string, kind cmdspec.Kind) template.HTML {
	name, arg, _ := strings.Cut(line, " ")
	var b strings.Builder
	b.WriteString(`<span class="pr">gluon&gt;</span> <span class="kw">` + html.EscapeString(name) + `</span>`)
	if arg == "" {
		return template.HTML(b.String())
	}
	b.WriteString(" ")
	switch {
	case kind.IsGo():
		b.WriteString(paint(arg, syntax.Go))
	case kind == cmdspec.SQL:
		b.WriteString(paint(arg, syntax.SQL))
	default:
		b.WriteString(html.EscapeString(arg))
	}
	return template.HTML(b.String())
}

// paint is src with each token in its role's span.
func paint(src string, lang syntax.Lang) string {
	var b strings.Builder
	at := 0
	for _, t := range syntax.Tokens(lang, src) {
		if t.Start < at || t.End > len(src) {
			continue
		}
		b.WriteString(html.EscapeString(src[at:t.Start]))
		text := html.EscapeString(src[t.Start:t.End])
		if cls, ok := roleClass[t.Role]; ok {
			b.WriteString(`<span class="` + cls + `">` + text + `</span>`)
		} else {
			b.WriteString(text)
		}
		at = t.End
	}
	b.WriteString(html.EscapeString(src[at:]))
	return b.String()
}

// paintLines paints src as one text and returns it a line at a time, a token
// that crosses a line — a raw string, a block comment — closed at the end of
// each line and reopened on the next, so every line is whole markup.
func paintLines(src string, lang syntax.Lang) []string {
	var lines []string
	var b strings.Builder
	emit := func(text, cls string) {
		for i, part := range strings.Split(text, "\n") {
			if i > 0 {
				lines = append(lines, b.String())
				b.Reset()
			}
			if part == "" {
				continue
			}
			if cls == "" {
				b.WriteString(html.EscapeString(part))
			} else {
				b.WriteString(`<span class="` + cls + `">` + html.EscapeString(part) + `</span>`)
			}
		}
	}
	at := 0
	for _, t := range syntax.Tokens(lang, src) {
		if t.Start < at || t.End > len(src) {
			continue
		}
		emit(src[at:t.Start], "")
		emit(src[t.Start:t.End], roleClass[t.Role])
		at = t.End
	}
	emit(src[at:], "")
	return append(lines, b.String())
}

// Go renders a line of Go as the site paints it, for a guide's own snippets.
func Go(src string) template.HTML { return template.HTML(paint(src, syntax.Go)) }

var (
	backticks = regexp.MustCompile("`([^`]+)`")
	emphasis  = regexp.MustCompile(`\*([A-Za-z][^*]*[A-Za-z])\*`)
	command   = regexp.MustCompile(`(^|[\s(—])(:[a-z]+)\b`)
)

// prose is a Detail as HTML. A Detail is written for a terminal: paragraphs
// separated by a blank line, lines wrapped by hand, and a block indented two
// spaces or more when it is code or a table. So paragraphs are rejoined, an
// indented block is kept as it was, a backquoted span is code, *a word* is
// emphasis, and a command the registry knows links to its entry.
func prose(text string, known map[string]bool) template.HTML {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	var b strings.Builder
	for _, para := range strings.Split(text, "\n\n") {
		lines := strings.Split(para, "\n")
		if indented(lines) {
			b.WriteString("<pre>" + html.EscapeString(dedent(lines)) + "</pre>\n")
			continue
		}
		s := html.EscapeString(strings.Join(lines, " "))
		s = backticks.ReplaceAllString(s, "<code>$1</code>")
		s = emphasis.ReplaceAllString(s, "<em>$1</em>")
		s = command.ReplaceAllStringFunc(s, func(m string) string {
			sub := command.FindStringSubmatch(m)
			if !known[sub[2]] {
				return m
			}
			return sub[1] + `<a href="commands.html#` + slug(strings.TrimPrefix(sub[2], ":")) +
				`"><code>` + sub[2] + `</code></a>`
		})
		b.WriteString("<p>" + s + "</p>\n")
	}
	return template.HTML(b.String())
}

// indented reports whether every line of a paragraph is indented, which is how
// a Detail marks code or a table.
func indented(lines []string) bool {
	for _, l := range lines {
		if !strings.HasPrefix(l, "  ") {
			return false
		}
	}
	return true
}

// dedent takes the common indent off.
func dedent(lines []string) string {
	min := -1
	for _, l := range lines {
		n := len(l) - len(strings.TrimLeft(l, " "))
		if min < 0 || n < min {
			min = n
		}
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l[min:]
	}
	return strings.Join(out, "\n")
}
