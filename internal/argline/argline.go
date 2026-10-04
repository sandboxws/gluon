// Package argline splits a meta command's argument the way a shell would, and
// separates the parts of a value that name an environment variable from the
// parts that carry one.
//
// It exists because two commands need exactly the same answer and must not
// arrive at it twice. The REPL hands a command its argument as one string with
// no shell anywhere near it, so quoting is the only way to say that
// `Authorization: Bearer $T` is one argument written as four words — and the
// shape-based credential test (invariant 23) has to run on the value with its
// references still spelled $NAME, which is what makes a referenced token pass
// and a typed one fail without the test knowing what a reference is. Two copies
// of that reconstruction would eventually disagree, and the copy that drifted
// would be the one that let a secret through.
package argline

import (
	"fmt"
	"strings"
)

// Fields splits a command line into words, honouring quotes.
//
// A backslash is not an escape here. It passes through, so that SplitRefs can
// still see the \$ that means a literal dollar — a tokenizer that consumed it
// would have to guess which of the two layers the escape belonged to, and would
// guess wrong for whichever one the user meant.
func Fields(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	var quote byte
	inWord := false
	flush := func() {
		if inWord {
			out = append(out, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case quote != 0:
			if ch == quote {
				quote = 0
			} else {
				cur.WriteByte(ch)
			}
		case ch == '\'' || ch == '"':
			quote, inWord = ch, true
		case ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r':
			flush()
		default:
			cur.WriteByte(ch)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("the %c quote is never closed", quote)
	}
	flush()
	return out, nil
}

// A Part is one piece of a value: literal text, or the name of an environment
// variable standing in for it.
type Part struct {
	Text string
	Ref  string // a variable named by $NAME, or "" when Text is literal
}

// SplitRefs breaks a value into literal text and the variables it names.
//
// $NAME is a reference and \$ is a literal dollar, which is the way out for a
// value that really contains one. A $ followed by anything that is not an
// identifier is literal too: a lone $ is a character people write, and refusing
// it would be a rule with no purpose.
func SplitRefs(v string) []Part {
	var parts []Part
	var lit strings.Builder
	emit := func() {
		if lit.Len() > 0 {
			parts = append(parts, Part{Text: lit.String()})
			lit.Reset()
		}
	}
	for i := 0; i < len(v); i++ {
		switch {
		case v[i] == '\\' && i+1 < len(v) && v[i+1] == '$':
			lit.WriteByte('$')
			i++
		case v[i] == '$':
			name := refName(v[i+1:])
			if name == "" {
				lit.WriteByte('$')
				continue
			}
			emit()
			parts = append(parts, Part{Ref: name})
			i += len(name)
		default:
			lit.WriteByte(v[i])
		}
	}
	emit()
	return parts
}

// refName reads the identifier after a $, or "" when there is not one.
func refName(s string) string {
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == '_', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case i > 0 && c >= '0' && c <= '9':
		default:
			return s[:i]
		}
		i++
	}
	return s
}

// Probe rebuilds the value the shape test judges: escapes resolved, and every
// reference still spelled $NAME.
func Probe(parts []Part) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Ref != "" {
			b.WriteString("$" + p.Ref)
			continue
		}
		b.WriteString(p.Text)
	}
	return b.String()
}

// Refs is every variable the parts name, in first-seen order.
func Refs(parts []Part) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range parts {
		if p.Ref == "" || seen[p.Ref] {
			continue
		}
		seen[p.Ref] = true
		out = append(out, p.Ref)
	}
	return out
}
