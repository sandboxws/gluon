package complete

import "strings"

// Argument completion: inside `fmt.Fprintln(|`, offer the names in scope whose
// type the parameter will take.
//
// Like the composite-literal scanner beside it, this is a scan of the typed
// line rather than a parse: an unclosed parenthesis is the whole signal, and
// go/parser has nothing to say about a line that is incomplete by definition.
// What the scan has to get right is which parentheses are real — one inside a
// string or a comment is not — and which of the commas after it belong to this
// call rather than to a nested one.

// call describes the open call the cursor is sitting in.
type call struct {
	// Callee is the text before the opening parenthesis: "f", "pkg.F",
	// "x.Method". It is text, not a resolved object: resolving it costs a type
	// check, and that happens once per callee rather than once per keystroke.
	Callee string
	// Index is the argument position at the cursor, counting the commas that
	// belong to this call.
	Index int
	// Prefix is the partly typed argument. Dots are part of it, so a caller
	// can tell `os.Std` from `st` and leave the selector to the path that
	// already handles selectors.
	Prefix string
}

// callAt reports the innermost open call the end of line falls inside, if any.
func callAt(line string) (call, bool) {
	open, ok := innermostOpenParen(line)
	if !ok {
		return call{}, false
	}
	callee, ok := calleeBefore(line[:open])
	if !ok {
		return call{}, false
	}
	body := line[open+1:]
	return call{
		Callee: callee,
		Index:  argIndex(body),
		Prefix: body[tokenStart(body):],
	}, true
}

// innermostOpenParen is the index of the `(` that is still open at the end of
// line, ignoring anything inside a string, rune or comment.
//
// Unlike innermostOpenBrace it refuses outright when the line ends inside a
// literal or a comment. A brace still open there is still a literal being
// typed; a parenthesis is not an argument position, and the words after it are
// prose or string content rather than a name being completed.
func innermostOpenParen(line string) (int, bool) {
	// Backed by a fixed array so the common line allocates nothing on the
	// keystroke path. It is a starting capacity and not a limit: a line that
	// nests deeper than this appends past it and costs one allocation.
	var buf [8]int
	stack := buf[:0]
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"', '`', '\'':
			j := skipQuoted(line, i)
			if j >= len(line) {
				return 0, false // unterminated: the cursor is inside it
			}
			i = j
		case '/':
			// A line comment runs to the end, and there is no end here.
			if i+1 < len(line) && line[i+1] == '/' {
				return 0, false
			}
		case '(':
			stack = append(stack, i)
		case ')':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	return last(stack)
}

// calleeBefore reads the callee immediately preceding an open parenthesis, or
// refuses when what precedes it is not a name gluon can look up.
//
// Three refusals, and none of them is the identifier itself:
//
//   - `if (`, `for (`, `switch (`, `func (` — the word before the parenthesis
//     is a keyword, so the parenthesis groups an expression or opens a
//     parameter list rather than a call. A keyword before the *callee* is
//     fine: `if f(` and `go f(` are ordinary calls.
//   - `get()(`, `fs[i](` — the callee is a call result or an index. Naming its
//     type means evaluating it, which completion does not do (invariant 5's
//     completion form: the checker is consulted, never run).
//   - `) (`, `+ (` — nothing that looks like a name at all.
func calleeBefore(head string) (string, bool) {
	head = strings.TrimRight(head, " \t")
	if head == "" {
		return "", false
	}
	if c := head[len(head)-1]; c == ')' || c == ']' {
		return "", false
	}
	name := head[tokenStart(head):]
	if name == "" || isKeyword(name) {
		return "", false
	}
	// A digit cannot start a name, and a leading dot means the scan ran off
	// the front of something that is not a selector chain.
	if c := name[0]; (c >= '0' && c <= '9') || c == '.' {
		return "", false
	}
	return name, true
}

// argIndex is the argument position at the end of body: the number of commas
// that belong to this call. A comma inside a nested call, index, literal or
// string belongs to that one instead.
func argIndex(body string) int {
	var n, depth int
	for i := 0; i < len(body); i++ {
		switch c := body[i]; {
		case c == '"' || c == '`' || c == '\'':
			i = skipQuoted(body, i)
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		case c == ',' && depth <= 0:
			n++
		}
	}
	return n
}
