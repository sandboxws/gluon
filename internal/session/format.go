package session

import (
	"fmt"
	"strconv"
	"strings"
)

// DirectivePrefix marks a line that carries what the source cannot say.
//
// An entry is its Src plus four decisions the session made about it, and only
// one of them — Kind — can be recovered by reading the source back. Pinning is
// a decision the user made, and NoValue and Values are things a build taught
// the session. A line beginning with this prefix, met where an entry would
// begin, is one of those decisions rather than something somebody typed.
const DirectivePrefix = "//gluon:"

// The three directives, spelled once. They are comments so that a file full of
// them still opens in an editor as Go, which is what :scratch -edit hands to
// $EDITOR.
const (
	pinDirective     = DirectivePrefix + "pin"
	noValueDirective = DirectivePrefix + "novalue"
	valuesDirective  = DirectivePrefix + "values"
)

// Marshal renders a session as the lines that were typed, each preceded by the
// directives needed to put the entry back as it is now.
//
// The durable form is the entry stream and not the rendered program. :edit's
// own comment is the argument: the rendered program wraps every expression in
// __gluonPrint and hoists declarations, and there is no way to write a bare
// `len(x)` back as valid Go — so a reader would have to re-derive entries from
// generated source, and pins appear nowhere in it at all.
//
// Kind and Binds are deliberately absent: Classify recomputes both from Src,
// and a field stored twice is a field that can disagree with itself. The
// round-trip test asserts they come back equal rather than trusting that.
func Marshal(s *Session) []byte {
	if s == nil {
		return nil
	}
	var b strings.Builder
	for _, e := range s.Entries {
		if e.Pinned {
			b.WriteString(pinDirective + "\n")
		}
		// NoValue is the one that is easy to think optional and is not. It is
		// learned from a failed build and re-learned by markNoValue, but
		// evalWith retries exactly once and `go build` stops at ten errors,
		// while predictNoValue only inspects the last entry — so a session
		// holding eleven value-less calls would fail to replay, and fail again
		// on the retry. Recording it is what makes this stream restore's equal
		// rather than :edit's.
		if e.NoValue {
			b.WriteString(noValueDirective + "\n")
		}
		if e.Values > 0 {
			b.WriteString(valuesDirective + " " + strconv.Itoa(e.Values) + "\n")
		}
		b.WriteString(e.Src)
		b.WriteString("\n")
	}
	return []byte(b.String())
}

// Unmarshal reads a session back.
//
// Where an entry ends is decided by IsIncomplete, exactly as the drivers decide
// it while somebody is typing — not by a delimiter written into the file. Using
// the same rule as the driver is what makes "type it, save it, reopen it"
// produce the same entries; a delimiter would be a second definition of where
// an entry ends, and the two would eventually disagree about a raw string with
// a brace in it.
//
// A //gluon: line met while the buffer is empty is a directive. One met while
// the buffer is non-empty is user text, because it is inside a construct they
// typed — that single rule is why a comment in a function body survives.
func Unmarshal(data []byte) (*Session, error) {
	s := &Session{}
	var pending Entry
	var buf []string
	start := 0

	for i, line := range strings.Split(string(data), "\n") {
		if len(buf) == 0 && strings.HasPrefix(line, DirectivePrefix) {
			if err := applyDirective(&pending, line); err != nil {
				return nil, fmt.Errorf("line %d: %w", i+1, err)
			}
			continue
		}
		if len(buf) == 0 {
			start = i + 1
		}
		buf = append(buf, line)
		joined := strings.Join(buf, "\n")
		if strings.TrimSpace(joined) == "" {
			buf = nil
			continue
		}
		if IsIncomplete(joined) {
			continue
		}
		buf = nil

		e, err := Classify(joined)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", start, err)
		}
		// The directives describe the entry they precede, so they are applied
		// after Classify rather than before: Classify has never seen a pin and
		// would not know what to do with one.
		e.Pinned, e.NoValue, e.Values = pending.Pinned, pending.NoValue, pending.Values
		pending = Entry{}
		s.Append(e)
	}

	if strings.TrimSpace(strings.Join(buf, "\n")) != "" {
		// An unclosed construct at the end is a truncated file, and saying
		// where it starts is the difference between a fixable file and one that
		// has to be read from the top.
		return nil, fmt.Errorf("line %d: the file ends inside an unclosed construct", start)
	}
	return s, nil
}

// applyDirective records one directive against the entry it precedes. An
// unknown one is refused rather than skipped: a directive gluon does not
// understand means the file says something about the entry that this reader is
// about to drop, and dropping it silently is how a pin goes missing.
func applyDirective(e *Entry, line string) error {
	switch {
	case line == pinDirective:
		e.Pinned = true
	case line == noValueDirective:
		e.NoValue = true
	case strings.HasPrefix(line, valuesDirective+" "):
		n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, valuesDirective+" ")))
		if err != nil || n < 0 {
			return fmt.Errorf("%s takes a count: %q", valuesDirective, line)
		}
		e.Values = n
	default:
		return fmt.Errorf("unknown directive %q", line)
	}
	return nil
}
