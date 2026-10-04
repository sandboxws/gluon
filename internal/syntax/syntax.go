// Package syntax turns source text into coloured source text without ever
// changing a byte of it.
//
// That sentence is the whole design. Everything gluon prints through here —
// :src, :doc -src, a generated mock, the line you just typed — is source a
// person is going to read as source, and a highlighter that reformats while it
// colours is worse than no highlighter: gofmt indents with tabs, and a tab that
// became four spaces is a diff nobody asked for.
//
// So the tokenizers report byte offsets and nothing else, and Paint is the only
// thing that writes. It emits src[a:b] slices with a non-decreasing and
// gapless, interleaved with fixed escape strings. Nothing here formats, pads,
// wraps, trims or re-encodes. The consequence worth stating: a tokenizer that
// gets a token's class wrong produces a wrong colour, and a tokenizer that gets
// its extent wrong steals or cedes colour to its neighbour. Neither can produce
// a wrong byte. That is what lets the scanners for SQL, JSON and TOML be
// plausible rather than perfect.
//
// The same reasoning is why a Palette is escape strings rather than
// lipgloss.Style. lipgloss.Style.Render converts tabs to four spaces on every
// path including its zero-props early return, rewrites \r\n to \n, and pads
// every line of a multi-line string out to the widest. internal/ui derives the
// sequences once and hands them over; this package never links lipgloss, which
// is also what makes the round-trip test hermetic.
package syntax

import "strings"

// Lang is a language this package can tokenize. The zero value paints nothing,
// so a Result that never sets one behaves exactly as it did before.
type Lang string

const (
	None Lang = ""
	Go   Lang = "go"
	SQL  Lang = "sql"
	JSON Lang = "json"
	TOML Lang = "toml"
)

// Known resolves a name a plugin or a config file wrote.
func Known(name string) (Lang, bool) {
	switch Lang(name) {
	case Go:
		return Go, true
	case SQL:
		return SQL, true
	case JSON:
		return JSON, true
	case TOML:
		return TOML, true
	}
	return None, false
}

// A Role is a class of token: what a theme paints, and the only thing a
// tokenizer is allowed to say about a token.
type Role uint8

const (
	RoleNone Role = iota // not painted, and not an error either
	RoleKeyword
	RoleString
	RoleType
	RoleComment
	RoleNumber
	RoleBuiltin
	RolePunct
	RoleIdent
	NumRoles
)

// roleNames are the spellings a config file and a theme file use. They are the
// same words in both places on purpose: a role is one idea with one name.
var roleNames = [NumRoles]string{
	RoleNone:    "none",
	RoleKeyword: "keyword",
	RoleString:  "string",
	RoleType:    "type",
	RoleComment: "comment",
	RoleNumber:  "number",
	RoleBuiltin: "builtin",
	RolePunct:   "punctuation",
	RoleIdent:   "ident",
}

func (r Role) String() string {
	if int(r) >= len(roleNames) {
		return "none"
	}
	return roleNames[r]
}

// A Token is one lexical run, as byte offsets into the source it came from.
//
// Bytes no token covers — whitespace, and anything a scanner could not make
// sense of — are simply absent rather than given a role of their own. Paint
// copies them through untouched, which is both cheaper and more honest: an
// unpainted byte should carry no escape at all, not an escape that happens to
// select the default colour.
type Token struct {
	Start, End int
	Role       Role
}

// Tokens are the tokens of src, in order. An unknown language has none, which
// is how None ends up meaning "print it exactly as it came".
func Tokens(lang Lang, src string) []Token {
	switch lang {
	case Go:
		return goTokens(src)
	case SQL:
		return sqlTokens(src)
	case JSON:
		return jsonTokens(src)
	case TOML:
		return tomlTokens(src)
	}
	return nil
}

// A Palette is the escape sequence each role opens with, and the one that
// closes any of them. An empty Open means the role is not painted and its bytes
// are written through untouched.
//
// Strings rather than lipgloss.Style — see the package comment. The zero
// Palette paints nothing, which is what NO_COLOR, a pipe and every test get.
type Palette struct {
	Open  [NumRoles]string
	Close string
}

// Painted reports whether this palette would colour anything.
func (p Palette) Painted() bool {
	if p.Close == "" {
		return false
	}
	for _, o := range p.Open {
		if o != "" {
			return true
		}
	}
	return false
}

// Span paints one run in one role. It is what the REPL uses for a meta
// command's name, which is a keyword of gluon's own language rather than of Go.
func (p Palette) Span(r Role, text string) string {
	open := ""
	if int(r) < len(p.Open) {
		open = p.Open[r]
	}
	if open == "" || text == "" {
		return text
	}
	var b strings.Builder
	writeSpan(&b, text, open, p.Close)
	return b.String()
}

// Highlight returns src with escape sequences inserted around its tokens.
// Stripping those sequences yields src back, byte for byte.
func Highlight(lang Lang, src string, p Palette) string {
	if lang == None || src == "" || !p.Painted() {
		return src
	}
	return Paint(src, 0, Tokens(lang, src), p)
}

// Paint writes src from byte offset `from`, colouring the tokens that fall at
// or after it and copying everything else verbatim.
//
// `from` exists for the REPL echo. A continuation line is tokenized together
// with the lines above it — `world` closing a raw string opened on the previous
// line is string content, and a scanner starting at it would see an identifier
// — so the caller tokenizes the whole construct and paints only its tail.
func Paint(src string, from int, toks []Token, p Palette) string {
	if from < 0 {
		from = 0
	}
	if from > len(src) {
		return ""
	}
	var b strings.Builder
	b.Grow(len(src) - from + len(toks)*16)
	cursor := from
	for _, t := range toks {
		// Skipped rather than clamped. A token that starts behind the cursor
		// is the inserted semicolon go/scanner positions inside a block comment
		// it has already returned (see golang.go); a zero-width or out-of-range
		// one is a scanner saying it does not know. Painting none of them costs
		// a colour and can never cost a byte.
		if t.Start < cursor || t.End <= t.Start || t.End > len(src) {
			continue
		}
		b.WriteString(src[cursor:t.Start])
		if open := p.Open[t.Role]; open == "" {
			b.WriteString(src[t.Start:t.End])
		} else {
			writeSpan(&b, src[t.Start:t.End], open, p.Close)
		}
		cursor = t.End
	}
	b.WriteString(src[cursor:])
	return b.String()
}

// writeSpan opens and closes the run once per line rather than once per token.
//
// A raw string or a block comment can be a hundred lines long, and a style left
// open across a newline paints the left margin of every line the terminal
// reflows or the pager scrolls. Closing at each newline costs a few bytes and
// makes the output survive being cut into lines by anything downstream —
// which bubbles/viewport does, and which the input widget does too.
func writeSpan(b *strings.Builder, text, open, close string) {
	for {
		i := strings.IndexByte(text, '\n')
		if i < 0 {
			break
		}
		if i > 0 {
			b.WriteString(open)
			b.WriteString(text[:i])
			b.WriteString(close)
		}
		b.WriteByte('\n')
		text = text[i+1:]
	}
	if text == "" {
		return
	}
	b.WriteString(open)
	b.WriteString(text)
	b.WriteString(close)
}
