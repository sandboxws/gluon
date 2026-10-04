package cmdspec

import "strings"

// Rest is the usage hint: what is still to come on the line, beginning with
// the element under the cursor — `:http GET ` reads `<url> [-H <header>]...`.
//
// The element being typed comes first rather than being marked, because
// brackets already mean "optional" in a synopsis and a second meaning for them
// would be read wrong. Inside a flag's value the hint is that value and the
// flag's own help, which is the one thing worth knowing at that moment. It is
// empty where there is nothing to say: past the grammar, inside a quote, and
// inside a Go operand that has begun — where a call's signature, or nothing,
// is the better hint.
func (s Spec) Rest(arg string, at At) string {
	switch at.Where {
	case InQuote:
		return ""
	case Nowhere:
		// Past the operands, a command that reads flags after them still
		// reads the ones not given.
		if s.FlagsAt == Lead {
			return ""
		}
		return flagSynopsis(s.left(at))
	case InFlagValue:
		h := "<" + at.Flag.Value + ">"
		if at.Flag.Help != "" {
			h += "  " + at.Flag.Help
		}
		return h
	}
	params := s.Operands(arg)
	from := 0
	switch {
	case at.Where == OnFlag:
		from = len(at.Operands)
	case at.Where == InOperand && (s.Kind.IsGo() || s.Kind == SQL):
		if at.Begun || at.Word != "" {
			// A Go operand under way: the only thing left to say is the next
			// comma-joined operand, once its comma has been typed.
			n, open := commaIndex(at.Text)
			if !open || n == 0 || n >= len(params) {
				return ""
			}
			return withHelp(params[n:], nil)
		}
	case at.Where == InOperand:
		from = at.Index
	}
	if from > len(params) {
		from = len(params)
	}
	// Flags that come after the operands are still to come, so the hint shows
	// them from the start. Flags that must lead are gone once the operand has.
	var flags []Flag
	if s.FlagsAt != Lead || (len(at.Operands) == 0 && !(at.Where == InOperand && at.Word != "")) {
		flags = s.left(at)
	}
	if s.FlagsAt == After {
		return join(withHelp(params[from:], flags), flagSynopsis(flags))
	}
	return join(flagSynopsis(flags), withHelp(params[from:], flags))
}

// left is the visible flags still to be typed, by the rules FlagsFor applies.
func (s Spec) left(at At) []Flag {
	names := map[string]bool{}
	for _, n := range s.FlagsFor(at) {
		names[strings.TrimSpace(n)] = true
	}
	var out []Flag
	for _, f := range s.Visible() {
		if names[f.Name] {
			out = append(out, f)
		}
	}
	return out
}

// withHelp writes operands as a synopsis whose first element starts the line,
// and adds the operand's help when it is the only thing left on the line —
// with flags still to come, the help would read as theirs.
func withHelp(ps []Param, flags []Flag) string {
	if len(ps) == 0 {
		return ""
	}
	first := ps[0]
	first.Sep = ""
	out := operandSynopsis(append([]Param{first}, ps[1:]...))
	if len(ps) == 1 && len(flags) == 0 && ps[0].Help != "" {
		out += "  " + ps[0].Help
	}
	return out
}

func join(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + " " + b
}

// commaIndex counts the top-level commas in a Go operand, and says whether the
// text after the last one is still empty — the moment the next operand has
// been reached and nothing of it typed. Commas inside brackets, braces,
// parentheses and literals are not separators, which is the rule :bench a, b
// already splits by.
func commaIndex(text string) (n int, open bool) {
	depth := 0
	var quote byte
	last := -1
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case quote != 0:
			if c == '\\' && quote != '`' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'' || c == '`':
			quote = c
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		case c == ',' && depth == 0:
			n, last = n+1, i
		}
	}
	return n, n > 0 && strings.TrimSpace(text[last+1:]) == ""
}
