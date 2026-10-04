package complete

import "strings"

// Composite literal completion: inside `Point{X: 1, |}`, offer the fields that
// are not set yet.
//
// This is a scan of the typed line rather than a parse, because the line is by
// definition incomplete — an unclosed brace is the whole signal — and go/parser
// has nothing useful to say about it. What the scan has to get right is which
// braces are real: one inside a string or a rune literal is not a brace.

// literal describes the composite literal the cursor is sitting in.
type literal struct {
	// Type is the text before the opening brace: "Point", "pkg.Config".
	Type string
	// Set are the field names already given, so they are not offered twice.
	Set []string
	// Prefix is the partly typed field name at the cursor.
	Prefix string
}

// literalAt reports the composite literal the end of line falls inside, if any.
func literalAt(line string) (literal, bool) {
	open, ok := innermostOpenBrace(line)
	if !ok {
		return literal{}, false
	}

	typ := typeBefore(line[:open])
	if typ == "" {
		// A block, a func literal, or a map/slice literal whose type ends in a
		// bracket. Nothing to offer either way.
		return literal{}, false
	}

	body := line[open+1:]
	return literal{
		Type:   typ,
		Set:    keysIn(body),
		Prefix: fieldPrefix(body),
	}, true
}

// innermostOpenBrace is the index of the `{` that is still open at the end of
// line, ignoring anything inside a string, rune or comment.
func innermostOpenBrace(line string) (int, bool) {
	var stack []int
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"', '`', '\'':
			i = skipQuoted(line, i)
		case '/':
			// A line comment runs to the end, and there is no end here.
			if i+1 < len(line) && line[i+1] == '/' {
				return last(stack)
			}
		case '{':
			stack = append(stack, i)
		case '}':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	return last(stack)
}

func last(stack []int) (int, bool) {
	if len(stack) == 0 {
		return 0, false
	}
	return stack[len(stack)-1], true
}

// skipQuoted returns the index of the closing quote for the literal starting at
// i, or the end of the line if it is unterminated — which it often is, since
// this runs on a line being typed.
func skipQuoted(s string, i int) int {
	q := s[i]
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			if q != '`' {
				j++ // an escape, so the next byte is not the terminator
			}
		case q:
			return j
		}
	}
	return len(s)
}

// typeBefore reads the type expression immediately preceding a brace, or
// returns "" when the brace does not open a struct literal.
//
// Three things have to be excluded, and none of them is the identifier itself:
//
//   - `if x {`, `for x {`, `switch x {` — the word before the identifier is a
//     block keyword, so the brace opens a block. Go requires parentheses to
//     write a composite literal in those positions anyway.
//   - `[]Point{`, `map[string]T{`, `[4]byte{` — the character before the
//     identifier is a bracket. Those elements are not named fields.
//   - `struct {`, `func() {`, `) {` — nothing that looks like a type name.
func typeBefore(head string) string {
	head = strings.TrimRight(head, " \t")
	i := len(head)
	for i > 0 {
		c := head[i-1]
		if c == '.' || c == '_' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			i--
			continue
		}
		break
	}
	name := head[i:]
	if name == "" || isKeyword(name) {
		return ""
	}
	// A digit cannot start a type.
	if c := name[0]; c >= '0' && c <= '9' {
		return ""
	}

	prev := strings.TrimRight(head[:i], " \t")
	if prev == "" {
		return name
	}
	// `[]Point{` and `map[string]T{`: the element type of a composite whose
	// elements are positions, not names.
	if prev[len(prev)-1] == ']' {
		return ""
	}
	if isKeyword(lastWord(prev)) {
		return ""
	}
	return name
}

// lastWord is the identifier ending prev, for deciding whether a brace follows
// `if x` rather than `p := Point`.
func lastWord(prev string) string {
	i := len(prev)
	for i > 0 {
		c := prev[i-1]
		if c == '_' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			i--
			continue
		}
		break
	}
	return prev[i:]
}

// keysIn is the field names already assigned in a literal body, so they are not
// offered again. Only the literal's own keys count — a nested literal's are its
// own business, so anything inside a deeper brace is skipped.
func keysIn(body string) []string {
	var (
		out   []string
		depth int
		word  strings.Builder
	)
	flush := func(isKey bool) {
		if isKey && depth == 0 && word.Len() > 0 {
			out = append(out, word.String())
		}
		word.Reset()
	}
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case c == '"' || c == '`' || c == '\'':
			i = skipQuoted(body, i)
			word.Reset()
		case c == '{' || c == '[' || c == '(':
			depth++
			word.Reset()
		case c == '}' || c == ']' || c == ')':
			depth--
			word.Reset()
		case c == ':':
			// A key, unless this is `:=` — which cannot appear in a literal,
			// but the scan should not invent one either.
			if i+1 < len(body) && body[i+1] == '=' {
				word.Reset()
				i++
				continue
			}
			flush(true)
		case c == ',':
			flush(false)
		case c == '_' || c == '.' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9'):
			word.WriteByte(c)
		default:
			word.Reset()
		}
	}
	return out
}

// fieldPrefix is the partly typed field name at the end of the body.
func fieldPrefix(body string) string {
	i := len(body)
	for i > 0 {
		c := body[i-1]
		if c == '_' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			i--
			continue
		}
		break
	}
	// Anything but whitespace, a comma or the opening brace before the word
	// means this is a value being typed, not a key: `Point{X: fo`.
	head := strings.TrimRight(body[:i], " \t")
	if head != "" {
		if c := head[len(head)-1]; c != ',' && c != '{' {
			return "\x00" // a sentinel no field name can match
		}
	}
	return body[i:]
}

func isKeyword(s string) bool {
	for _, k := range keywords {
		if k == s {
			return true
		}
	}
	return false
}
