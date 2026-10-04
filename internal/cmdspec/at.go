package cmdspec

import "strings"

// Where says what the word at the end of an argument is.
type Where uint8

const (
	// Nowhere is past everything the grammar reads.
	Nowhere Where = iota
	// InQuote is inside a quoted value that has not been closed.
	InQuote
	// OnFlag is a word beginning with a dash, where the command reads flags.
	OnFlag
	// InFlagValue is the value of the flag before it.
	InFlagValue
	// InOperand is an operand.
	InOperand
)

// At is the word at the end of what has been typed after a command, and what
// the grammar says it is. Completion and the usage hint both start here, so
// the two cannot disagree about where the cursor is.
type At struct {
	Where Where
	// Word is the partial word under the cursor, and Start is where it begins
	// in the typed text. Word is empty right after a space.
	Word  string
	Start int
	// Param is the operand the word is, for InOperand, or nil past the ones
	// the grammar declares.
	Param *Param
	// Index is the operand's position.
	Index int
	// Flag is the flag whose value the word is, for InFlagValue.
	Flag Flag
	// Given is every flag already on the line.
	Given map[string]bool
	// Operands are the complete operand words before this one.
	Operands []string
	// Text is a Go or SQL operand as typed so far, from its first word to the
	// end of the line, and Begun says it started before the word under the
	// cursor. Both are empty for a command whose operands are words.
	Text  string
	Begun bool
}

// At reads typed — everything after the command's name — and says what its
// last word is. arg is the command's short Arg, for a Spec that declares no
// operands of its own.
//
// It reads the way the commands do: a flag with a value consumes the next word,
// flags are read only where FlagsAt says, and a Go or SQL argument is one
// operand, verbatim, from its first word on. A quote that has not closed is
// its own answer, because nothing inside one is a flag or a name.
func (s Spec) At(arg, typed string) At {
	words, starts, open := splitWords(typed)
	at := At{Start: len(typed), Given: map[string]bool{}}
	if n := len(words); n > 0 && (open || !endsInSpace(typed)) {
		at.Word, at.Start = words[n-1], starts[n-1]
		words = words[:n-1]
	}
	if open {
		at.Where = InQuote
		return at
	}
	if s.Kind == NoArg && len(s.Flags) == 0 {
		return at
	}
	params := s.Operands(arg)
	var pending *Flag
	for i, w := range words {
		if pending != nil {
			pending = nil
			continue
		}
		if f, ok := s.Flag(w); ok && s.readsFlags(len(at.Operands), params) {
			at.Given[w] = true
			if f.Value != "" {
				f := f
				pending = &f
			}
			continue
		}
		if s.Kind.IsGo() || s.Kind == SQL {
			// The rest of the line is one operand, verbatim: `:bench -nums`
			// benchmarks the negation of nums.
			at.Begun, at.Text = true, typed[starts[i]:]
			return s.operand(at, params, 0)
		}
		at.Operands = append(at.Operands, w)
	}
	if pending != nil {
		at.Where, at.Flag = InFlagValue, *pending
		return at
	}
	if strings.HasPrefix(at.Word, "-") && s.readsFlags(len(at.Operands), params) &&
		(!s.Kind.IsGo() || s.flagStartingWith(at.Word)) {
		// A Go argument's -dx is the negation of dx unless a flag begins so.
		at.Where = OnFlag
		return at
	}
	if s.Kind == NoArg {
		return at
	}
	if s.Kind.IsGo() || s.Kind == SQL {
		at.Text = at.Word
		return s.operand(at, params, 0)
	}
	return s.operand(at, params, len(at.Operands))
}

// operand places the word at operand i, or past the grammar when there is no
// operand i and the last one does not repeat.
func (s Spec) operand(at At, params []Param, i int) At {
	at.Where, at.Index = InOperand, i
	switch {
	case i < len(params):
		at.Param = &params[i]
	case len(params) > 0 && params[len(params)-1].Repeat:
		at.Param = &params[len(params)-1]
	case !s.Kind.IsGo() && s.Kind != SQL:
		at.Where = Nowhere
	}
	return at
}

// readsFlags says whether a flag is read after this many operands.
func (s Spec) readsFlags(operands int, params []Param) bool {
	switch s.FlagsAt {
	case After:
		required := 0
		for _, p := range params {
			if !p.Optional {
				required++
			}
		}
		return operands >= required
	case Anywhere:
		return true
	default:
		return operands == 0
	}
}

func (s Spec) flagStartingWith(prefix string) bool {
	for _, f := range s.Visible() {
		if strings.HasPrefix(f.Name, prefix) {
			return true
		}
	}
	return false
}

// FlagsFor is the flags that may be typed where at is: visible, not already
// given unless they repeat, no second mode once one is chosen, and a flag that
// is the whole argument only when nothing else has been typed. A flag taking a
// value is offered with the space after it, so accepting it moves on to the
// value.
func (s Spec) FlagsFor(at At) []string {
	moded := false
	for name := range at.Given {
		if f, ok := s.Flag(name); ok && (f.Mode || f.Alone) {
			moded = true
		}
	}
	var out []string
	for _, f := range s.Visible() {
		switch {
		case at.Given[f.Name] && !f.Repeat:
			continue
		case f.Mode && moded:
			continue
		case f.Alone && (len(at.Given) > 0 || len(at.Operands) > 0):
			continue
		}
		if f.Value != "" {
			out = append(out, f.Name+" ")
		} else {
			out = append(out, f.Name)
		}
	}
	return out
}

// splitWords splits typed text at whitespace outside quotes, and reports where
// each word starts and whether the last one is inside a quote still open.
func splitWords(s string) (words []string, starts []int, open bool) {
	var quote byte
	start := -1
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == ' ' || c == '\t':
			if start >= 0 {
				words, starts = append(words, s[start:i]), append(starts, start)
				start = -1
			}
		default:
			if start < 0 {
				start = i
			}
			if c == '\'' || c == '"' {
				quote = c
			}
		}
	}
	if start >= 0 {
		words, starts = append(words, s[start:]), append(starts, start)
	}
	return words, starts, quote != 0
}

func endsInSpace(s string) bool {
	return s != "" && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t')
}
