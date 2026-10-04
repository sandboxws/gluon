package syntax

import "strings"

// tomlTokens scans TOML for display.
//
// TOML is the one of these three whose lexical surface is genuinely closed:
// comments, four string forms, numbers, datetimes, bare keys and table
// headers, and seven punctuation bytes. No anchors, no merge keys, no
// references — which is to say none of the three features that made the YAML
// scanner in the ROADMAP's rejected list unscannable.
//
// The only state is whether we are left of the first '=' on this line, because
// that is what separates a key from a value.
func tomlTokens(src string) []Token {
	var toks []Token
	add := func(a, b int, r Role) {
		if b > a {
			toks = append(toks, Token{Start: a, End: b, Role: r})
		}
	}
	i := 0
	inKey := true // a fresh line starts left of its '='
	for i < len(src) {
		c := src[i]
		switch {
		case c == '\n':
			inKey = true
			i++

		case c == '#':
			j := strings.IndexByte(src[i:], '\n')
			if j < 0 {
				add(i, len(src), RoleComment)
				return toks
			}
			add(i, i+j, RoleComment)
			i += j

		case strings.HasPrefix(src[i:], `"""`), strings.HasPrefix(src[i:], "'''"):
			// Checked before the single forms, or """ scans as an empty string
			// followed by a quote.
			q := src[i : i+3]
			j := strings.Index(src[i+3:], q)
			if j < 0 {
				add(i, len(src), RoleString)
				return toks
			}
			add(i, i+3+j+3, RoleString)
			i += 3 + j + 3

		case c == '"' || c == '\'':
			j := scanQuoted(src, i, c, c == '"')
			role := RoleString
			if inKey {
				role = RoleType // a quoted key is still a key
			}
			add(i, j, role)
			i = j

		case c == '[' || c == ']':
			add(i, i+1, RolePunct)
			i++

		case c == '=':
			add(i, i+1, RolePunct)
			inKey = false
			i++

		case strings.IndexByte(",.{}", c) >= 0:
			add(i, i+1, RolePunct)
			i++

		case isDigit(c) || ((c == '+' || c == '-') && i+1 < len(src) && isDigit(src[i+1])):
			j := scanTOMLNumber(src, i)
			add(i, j, RoleNumber)
			i = j

		case isWordStart(c):
			j := i + 1
			for j < len(src) && (isWordByte(src[j]) || src[j] == '-') {
				j++
			}
			switch {
			case inKey:
				// A bare key, or the dotted name inside a [table] header —
				// both are the thing being named, so both read as a type.
				add(i, j, RoleType)
			case src[i:j] == "true" || src[i:j] == "false":
				add(i, j, RoleKeyword)
			case src[i:j] == "inf" || src[i:j] == "nan":
				add(i, j, RoleNumber)
			default:
				add(i, j, RoleIdent)
			}
			i = j

		default:
			i++
		}
	}
	return toks
}

// scanTOMLNumber consumes a number or a datetime as one run.
//
// One token rather than four, because 1979-05-27T07:32:00Z is a value and not
// three subtractions. The '-' and ':' continuations are allowed only once the
// run already holds a digit, so a bare '-' still scans as punctuation. This is
// the one approximation in the file: a malformed datetime is painted as if it
// were well formed, which is a colour and not a claim.
func scanTOMLNumber(src string, i int) int {
	j := i
	if src[j] == '+' || src[j] == '-' {
		j++
	}
	if j+1 < len(src) && src[j] == '0' && (src[j+1]|0x20 == 'x' || src[j+1]|0x20 == 'o' || src[j+1]|0x20 == 'b') {
		j += 2
		for j < len(src) && (isHex(src[j]) || src[j] == '_') {
			j++
		}
		return j
	}
	for j < len(src) {
		c := src[j]
		switch {
		case isDigit(c) || c == '_' || c == '.':
			j++
		case c == '-' || c == ':' || c == 'T' || c == 'Z' || c == '+':
			// Only inside a run that has already started with digits, and only
			// when something follows — otherwise "1 -" would swallow the dash.
			if j+1 < len(src) && (isDigit(src[j+1]) || src[j+1] == ':') {
				j++
				continue
			}
			if c == 'Z' {
				return j + 1
			}
			return j
		case (c|0x20) == 'e' && j+1 < len(src) && (isDigit(src[j+1]) || ((src[j+1] == '+' || src[j+1] == '-') && j+2 < len(src) && isDigit(src[j+2]))):
			j += 2
		default:
			return j
		}
	}
	return j
}
