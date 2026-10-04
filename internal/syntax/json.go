package syntax

import "strings"

// jsonTokens scans JSON for display.
//
// A key and a value are both quoted runs, and telling them apart is the whole
// reason this is worth having: a key is what you scan a document for. The rule
// is one byte of lookahead — a string whose next non-space byte is ':' is a
// key — which is exactly right for any JSON that parses and harmlessly wrong
// for JSON that does not.
//
// No comment handling. encoding/json never emits comments, and gluon's JSON
// comes from :json, which marshals.
func jsonTokens(src string) []Token {
	var toks []Token
	add := func(a, b int, r Role) {
		if b > a {
			toks = append(toks, Token{Start: a, End: b, Role: r})
		}
	}
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == '"':
			j := scanQuoted(src, i, '"', true)
			role := RoleString
			if nextNonSpace(src, j) == ':' {
				role = RoleType
			}
			add(i, j, role)
			i = j

		case isDigit(c) || (c == '-' && i+1 < len(src) && isDigit(src[i+1])):
			j := scanNumberRun(src, i)
			add(i, j, RoleNumber)
			i = j

		case c == 't' || c == 'f' || c == 'n':
			j := i
			for j < len(src) && src[j] >= 'a' && src[j] <= 'z' {
				j++
			}
			switch src[i:j] {
			case "true", "false", "null":
				add(i, j, RoleKeyword)
			default:
				add(i, j, RoleIdent)
			}
			if j == i {
				j++
			}
			i = j

		case strings.IndexByte("{}[]:,", c) >= 0:
			add(i, i+1, RolePunct)
			i++

		default:
			i++
		}
	}
	return toks
}

// scanQuoted consumes a quoted run and returns the offset just past it. An
// unterminated literal runs to the end of the input, which is what a half-typed
// line is. When escapes is false a backslash is an ordinary byte, which is what
// TOML's single-quoted form means by "literal".
func scanQuoted(src string, i int, q byte, escapes bool) int {
	j := i + 1
	for j < len(src) {
		switch src[j] {
		case '\\':
			if escapes {
				j += 2
				continue
			}
			j++
		case q:
			return j + 1
		default:
			j++
		}
	}
	return len(src)
}
