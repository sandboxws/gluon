package eval

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/sandboxws/gluon/internal/render"
	"github.com/sandboxws/gluon/internal/session"
)

// Diag is one compiler diagnostic, already mapped back to the entry the user
// typed. Escape decisions, inlining decisions and vet findings all arrive in
// the same file:line:col shape, so they are all read into this.
type Diag struct {
	Entry int // which session entry it belongs to
	Line  int // line within that entry
	Col   int
	Text  string // the compiler's own words
	Quote string // the source line with a caret, from quoteEntry
}

// EscMsg is what :esc called a Diag before three more commands wanted the
// same shape. It is an alias rather than a second type so the two can never
// drift apart.
type EscMsg = Diag

// EscResult is what :esc found.
type EscResult struct {
	// Here is what the analysed expression itself produced; Elsewhere is what
	// the rest of the session produced, which matters when the allocation
	// happens inside a function the session declared.
	Here      []EscMsg
	Elsewhere []EscMsg
	Source    string
}

// escapeKeep is the complete level-1 vocabulary worth reporting. -m also emits
// inlining decisions, which are ~80% of its volume and a different question.
var escapeKeep = []string{
	"escapes to heap",
	"moved to heap:",
	"does not escape",
	"leaking param",
}

func isEscapeMsg(text string) bool {
	for _, k := range escapeKeep {
		if strings.Contains(text, k) {
			return true
		}
	}
	return false
}

// splitKeep separates the diagnostics whose text keep accepts from everything
// else.
//
// It has to happen before explain() sees the output: explain runs diagRe over
// the whole buffer, and with -m on, a build that fails also carries several
// hundred analysis lines that all match. Every one would be reported as an
// error.
func splitKeep(out string, keep func(string) bool) (kept []string, rest string) {
	var others []string
	for _, line := range strings.Split(out, "\n") {
		if m := diagRe.FindStringSubmatch(line); m != nil && keep(m[4]) {
			kept = append(kept, line)
			continue
		}
		others = append(others, line)
	}
	return kept, strings.Join(others, "\n")
}

// splitEscape is splitKeep over the level-1 escape vocabulary.
func splitEscape(out string) (esc []string, rest string) {
	return splitKeep(out, isEscapeMsg)
}

// buildOnly renders the session with the entry appended, builds it with a
// chosen gcflags value, and never runs it.
//
// It is the shape :esc established and :inline and :asm share: the snapshot of
// imports, resolved and healthy (invariant 14), the append and pop that keep
// the session unchanged, the EscSink rendering that keeps gluon's own printer
// out of the compiler's report (invariant 9), and the /dev/null target that
// leaves prog — and its Gatekeeper assessment — as the ordinary path left it.
// Written once because a caller that forgot any one of them would leak into
// the next line rather than fail here.
//
// report reads the build's output while the entry is still part of the
// session: every caller maps positions back onto it, and quoting the newest
// line needs that line to still be there.
func (e *Evaluator) buildOnly(s *session.Session, entry session.Entry, gcflags string, report func(out, src string, berr error) error) error {
	return e.withEntry(s, entry, render.EscSink, func(src string) error {
		// -o /dev/null rather than <dir>/prog: -gcflags changes the action ID,
		// so this is a separate cache entry, and writing it over prog would
		// cost the next ordinary line a fresh Gatekeeper validation on a new
		// binary.
		out, berr := e.buildWith(gcflags, os.DevNull)
		return report(out, src, berr)
	})
}

// withEntry renders the session with entry appended and hands the program text
// to use, restoring everything an evaluation would otherwise leak.
//
// The snapshot is invariant 14: imports and resolved because a transient entry
// that pulled in an import would leave it in the cached block for the next
// ordinary line to write verbatim, and healthy because whether the session
// exits cleanly is a property of the session, not of a question asked about
// it. use runs while the entry is still part of the session — every caller
// maps positions back onto it, and quoting the newest line needs that line to
// still be there.
//
// An entry with no source is not appended: :asm asks about a function the
// session already declares, so there is nothing to add to the program.
func (e *Evaluator) withEntry(s *session.Session, entry session.Entry, sink render.Sink, use func(src string) error) error {
	imports, resolved, healthy := e.imports, e.resolved, e.healthy
	defer func() { e.imports, e.resolved, e.healthy = imports, resolved, healthy }()

	if entry.Src != "" {
		s.Append(entry)
		defer s.Pop()
	}

	src, _, err := e.writeAs(s, sink, len(s.Entries)-1)
	if err != nil {
		return err
	}
	return use(src)
}

// EscapeAnalysis renders the session with the expression appended, builds it
// with -m, and never runs it.
//
// It is build-only by construction: escape analysis is a compile-time fact,
// and the exec is what costs ~105ms of macOS validating a fresh Mach-O.
//
// Every printable expression in the session is rendered through EscSink, not
// just the one being analysed. Otherwise each replayed entry reports an escape
// caused by gluon's own printer.
func (e *Evaluator) EscapeAnalysis(s *session.Session, entry session.Entry) (EscResult, error) {
	var res EscResult
	err := e.buildOnly(s, entry, "-e -m", func(out, src string, berr error) error {
		esc, rest := splitEscape(out)
		if berr != nil && strings.TrimSpace(rest) != "" {
			return &BuildError{Msg: e.explain(s, rest, src), Source: src}
		}
		here, elsewhere := mapMessages(s, esc)
		res = EscResult{Source: src, Here: here, Elsewhere: elsewhere}
		return nil
	})
	if err != nil {
		var be *BuildError
		if errors.As(err, &be) {
			return EscResult{Source: be.Source}, err
		}
		return EscResult{}, err
	}
	return res, nil
}

// mapMessages attributes compiler diagnostics to the entries they belong to,
// splitting them at the newest one — which is the line the user asked about.
//
// A position naming a file no entry rendered (gluonrt.go, the package header)
// is dropped: it is gluon's own code, and a decision about it is not one the
// user can act on.
func mapMessages(s *session.Session, lines []string) (here, elsewhere []Diag) {
	newest := len(s.Entries) - 1
	for _, line := range lines {
		m := diagRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		text := strings.TrimSpace(m[4])
		idx, ok := render.EntryOf(filepath.Base(m[1]))
		if !ok {
			// gluonrt.go and the package header report under their own names.
			continue
		}
		msg := Diag{Entry: idx, Text: text}
		msg.Line, msg.Col = atoi(m[2]), atoi(m[3])
		msg.Quote = quoteEntry(s, idx, msg.Line, msg.Col)
		if idx == newest {
			here = append(here, msg)
		} else {
			elsewhere = append(elsewhere, msg)
		}
	}
	return here, elsewhere
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}
