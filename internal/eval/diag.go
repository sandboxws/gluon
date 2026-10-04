package eval

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/sandboxws/gluon/internal/render"
	"github.com/sandboxws/gluon/internal/session"
)

// diagRe matches one compiler or go/types diagnostic. The file is captured
// because rendering names each entry's own synthetic file, so the position
// already refers to what the user typed.
var diagRe = regexp.MustCompile(`(?m)^(?:\./)?([\w./-]+\.go):(\d+)(?::(\d+))?:\s*(.*)$`)

// explain rewrites diagnostics to point at the line the user actually typed.
//
// Rendering emits a //line directive per entry, so the compiler reports
// gluon-in-7.go:2:11 rather than a line in the assembled program. That is
// resolved here into the entry's own source with a caret under the column,
// which is the difference between a position in a file the user has never
// seen and one in the line they just wrote.
//
// Anything that does not parse as a diagnostic is passed through untouched. A
// missing func main fails at link time with no file:line at all, so a rewriter
// that assumed the shape would drop the only useful message.
func (e *Evaluator) explain(s *session.Session, out, src string) string {
	lines := strings.Split(src, "\n")
	var msgs []string
	seen := map[string]bool{}

	for _, m := range diagRe.FindAllStringSubmatch(out, -1) {
		file, msg := m[1], strings.TrimSpace(m[4])
		line, _ := strconv.Atoi(m[2])
		col, _ := strconv.Atoi(m[3])

		var body string
		if i, ok := render.EntryOf(file); ok {
			body = quoteEntry(s, i, line, col)
		} else if line >= 1 && line <= len(lines) {
			// A position in the assembled program: no directive covered it,
			// so the best available context is the generated line itself.
			body = "    in: " + unwrapPrint(strings.TrimSpace(lines[line-1]))
		}

		text := msg
		if body != "" {
			text += "\n" + body
		}
		// The same mistake replayed across entries reports once per entry.
		if !seen[text] {
			seen[text] = true
			msgs = append(msgs, text)
		}
	}

	if len(msgs) == 0 {
		return trimPath(strings.TrimSpace(out), e.dir)
	}
	return strings.Join(msgs, "\n")
}

// entryPos maps a position in the rendered program onto the entry's own source.
//
// gofmt places the //line directive differently for a declaration than for a
// statement, so the two shift along different axes — see render.DeclLineShift
// and render.ColumnShift, which are gofmt's behaviour rather than arbitrary
// constants (invariant 7). One definition because two callers need the same
// answer: the text quoted under a diagnostic, and the position reported beside
// it.
func entryPos(kind session.Kind, line, col int) (int, int) {
	if kind == session.KindDecl {
		line -= render.DeclLineShift
	} else {
		col -= render.ColumnShift
	}
	return line, col
}

// quoteEntry renders the offending line of an entry with a caret beneath the
// reported column.
func quoteEntry(s *session.Session, entry, line, col int) string {
	if s == nil || entry < 0 || entry >= len(s.Entries) {
		return ""
	}
	en := s.Entries[entry]
	line, col = entryPos(en.Kind, line, col)

	src := strings.Split(en.Src, "\n")
	if line < 1 || line > len(src) {
		return ""
	}
	text := src[line-1]
	if col < 1 || col > len(text)+1 {
		return "    " + strings.TrimRight(text, " \t")
	}

	// The caret has to be placed in the printed line, where a tab occupies one
	// column but is rendered wider, so indentation is copied rather than
	// counted.
	pad := make([]rune, 0, col)
	for _, r := range text[:col-1] {
		if r == '\t' {
			pad = append(pad, '\t')
		} else {
			pad = append(pad, ' ')
		}
	}
	return "    " + strings.TrimRight(text, " \t") + "\n    " + string(pad) + "^"
}

// unwrapPrint hides the injected printer call so errors quote what the user
// typed rather than the wrapper gluon put around it.
func unwrapPrint(line string) string {
	pre := render.PrintFunc + "("
	if strings.HasPrefix(line, pre) && strings.HasSuffix(line, ")") {
		return line[len(pre) : len(line)-1]
	}
	return line
}

// mapTraceback replaces the file positions in a panic traceback with the
// source they stand for. The runtime resolves the same //line directives the
// compiler does, so every frame in the session's own code already names a
// synthetic file; without this the user sees a path under /var/folders they
// have never opened.
func mapTraceback(s *session.Session, out, dir string) string {
	if !strings.Contains(out, lineFileHint) {
		return trimPath(out, dir)
	}
	return traceRe.ReplaceAllStringFunc(out, func(match string) string {
		m := traceRe.FindStringSubmatch(match)
		i, ok := render.EntryOf(pathBase(match))
		if !ok || i >= len(s.Entries) {
			return match
		}
		line, _ := strconv.Atoi(m[1])
		en := s.Entries[i]
		if en.Kind == session.KindDecl {
			line -= render.DeclLineShift
		}
		src := strings.Split(en.Src, "\n")
		if line < 1 || line > len(src) {
			return match
		}
		return strings.TrimSpace(src[line-1])
	})
}

const lineFileHint = "gluon-in-"

// traceRe matches a whole path ending in a synthetic file, so the temp
// directory in front of it goes too. The path arrives in its resolved form
// (/private/var/...) rather than the one gluon created, which is why the
// prefix is matched here rather than trimmed by string replacement.
var traceRe = regexp.MustCompile(`\S*` + lineFileHint + `\d+\.go:(\d+)`)

// pathBase returns the synthetic file name out of a matched frame path.
func pathBase(match string) string {
	if i := strings.LastIndexByte(match, ':'); i >= 0 {
		match = match[:i]
	}
	if i := strings.LastIndexByte(match, '/'); i >= 0 {
		match = match[i+1:]
	}
	return match
}
