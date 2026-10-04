package repl

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"

	"github.com/sandboxws/gluon/internal/db"
	"github.com/sandboxws/gluon/internal/dsn"
	"github.com/sandboxws/gluon/internal/pretty"
)

// :env and :conf answer the two questions a Rails console answers with
// Rails.env and Rails.configuration, and a Go developer otherwise answers by
// typing os.Environ() and reading an unsorted, unredacted dump.
//
// Neither builds or runs anything: both read gluon's own process and files on
// disk, so they cost nothing per line and work in a session with no module
// attached. Neither writes anything, either — setting a variable would change
// what every later child sees while leaving no trace in :src or :save, which is
// a session that cannot be reproduced from its own transcript.
//
// Every value on the way to the screen goes through dsn.RedactValue, which
// judges by the shape of the value and is never given the name of the key
// holding it (invariant 23). The name is always shown in full: it is the useful
// half, and hiding it would leave a reader unable to tell which variable the
// mask belongs to.

// metaEnv is `:env [pattern]`.
func (c *Core) metaEnv(arg string) Result {
	pattern := strings.TrimSpace(arg)
	rows, redacted := envRows(os.Environ(), pattern)

	if len(rows) == 0 {
		// Not an empty table. A table with no rows and a table nobody filtered
		// look the same, and the difference is whether to try another pattern.
		if pattern != "" {
			return Result{Out: "no environment variable's name contains " +
				fmt.Sprintf("%q", pattern)}
		}
		return Result{Out: "the environment is empty"}
	}

	st := c.styles()
	var b strings.Builder
	head := "environment"
	count := fmt.Sprintf("  %d variables", len(rows))
	if pattern != "" {
		count = fmt.Sprintf("  %d matching %q", len(rows), pattern)
	}
	b.WriteString(st.Type.Render(head) + st.Annot.Render(count) + "\n")
	b.WriteString(kvTable(rows, st, "variable").String())
	if redacted > 0 {
		b.WriteString("\n" + st.Annot.Render(redactionNote(redacted)))
	}
	out := b.String()
	return Result{Out: out, Modal: pageable(strings.TrimSpace(head+" "+pattern), out)}
}

// envRows sorts, filters and redacts, and reports how many values it hid.
//
// It takes the environment as a slice rather than reading it, so the tests can
// state one and the "the key name does not decide" case is checkable without
// touching the process.
func envRows(environ []string, pattern string) (rows [][]string, redacted int) {
	pattern = strings.ToLower(pattern)
	names := make([]string, 0, len(environ))
	values := make(map[string]string, len(environ))
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			continue
		}
		if pattern != "" && !strings.Contains(strings.ToLower(name), pattern) {
			continue
		}
		names = append(names, name)
		values[name] = value
	}
	sort.Strings(names)

	for _, name := range names {
		// The name is deliberately not passed. RedactValue has no parameter to
		// take it — see internal/dsn/redact.go.
		shown, hid := dsn.RedactValue(values[name], "")
		mark := ""
		if hid {
			mark, redacted = "redacted", redacted+1
		}
		rows = append(rows, []string{printable(name), printable(shown), mark})
	}
	return rows, redacted
}

// printable spells out the control characters in something read from outside —
// an environment value, a config value — as the escape a Go literal would use.
//
// A terminal obeys a control character rather than showing it. A shell's prompt
// variable holding ESC[37m would repaint the table it sits in, a newline would
// break its row, and either reaches Result.Out, which is plain bytes by
// invariant 30. `\x1b[37m` is also the more useful answer: it is what the
// variable holds, where a painted cell only shows what it does.
func printable(s string) string {
	if strings.IndexFunc(s, unicode.IsControl) < 0 {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if !unicode.IsControl(r) {
			b.WriteRune(r)
			continue
		}
		q := strconv.QuoteRune(r) // '\x1b', '\n', '\u0085'
		b.WriteString(q[1 : len(q)-1])
	}
	return b.String()
}

// metaConf is `:conf [file]`.
func (c *Core) metaConf(arg string) Result {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return Result{Out: c.confSearchReport()}
	}

	entries, err := db.ReadConf(arg)
	if err != nil {
		// No keys alongside the error. See db.ReadConf: a half-read
		// configuration hides exactly the part that is missing.
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	if len(entries) == 0 {
		return Result{Out: shortenPath(arg, c) + " parses, and defines nothing"}
	}

	st := c.styles()
	dir := filepath.Dir(arg)
	rows := make([][]string, 0, len(entries))
	redacted := 0
	for _, e := range entries {
		// dir, so a relative sqlite path in the file resolves against the file
		// rather than against wherever gluon was launched.
		shown, hid := dsn.RedactValue(e.Value, dir)
		mark := ""
		if hid {
			mark, redacted = "redacted", redacted+1
		}
		rows = append(rows, []string{printable(e.Key), printable(shown), mark})
	}

	var b strings.Builder
	b.WriteString(st.Type.Render(shortenPath(arg, c)) +
		st.Annot.Render(fmt.Sprintf("  %d keys", len(rows))) + "\n")
	b.WriteString(kvTable(rows, st, "key").String())
	if redacted > 0 {
		b.WriteString("\n" + st.Annot.Render(redactionNote(redacted)))
	}
	out := b.String()
	return Result{Out: out, Modal: pageable(shortenPath(arg, c), out)}
}

// confSearchReport is `:conf` with no argument: what the project search found,
// and where it looked.
//
// It renders the detection gluon already ran for :db rather than walking the
// tree again — one bounded search, ceilinged at the repository root, paid for
// once per :use. Roots and Ceiling are printed even when files were found,
// because the reader has to know what "not listed" means.
func (c *Core) confSearchReport() string { return c.renderConfSearch(c.detect()) }

// renderConfSearch is the rendering alone, so a test can state a detection over
// a fixture tree instead of the machine gluon happens to be running on.
func (c *Core) renderConfSearch(d *db.Detection) string {
	st := c.styles()
	var b strings.Builder

	where := "this session"
	if h := c.host(); h != nil {
		where = h.Path
	}

	files := d.Searched
	if len(files) == 0 {
		b.WriteString("no configuration file found for " + st.Type.Render(where) + "\n")
	} else {
		b.WriteString(st.Type.Render("configuration") +
			st.Annot.Render(fmt.Sprintf("  %d files near %s", len(files), where)) + "\n")
		for _, p := range files {
			b.WriteString("  " + st.Str.Render(shorten(p, c, d.Ceiling)) + "\n")
		}
	}

	if len(d.Roots) > 0 {
		b.WriteString(st.Annot.Render("  looked in") + "\n")
		for _, dir := range d.Roots {
			b.WriteString(st.Annot.Render("    "+shorten(dir, c, d.Ceiling)) + "\n")
		}
	}
	if d.Ceiling != "" {
		b.WriteString(st.Annot.Render("  stopped at the repository root") + "\n")
	}
	b.WriteString(st.Annot.Render("  :conf <file> reads one. :settings changes gluon's own."))
	return b.String()
}

// redactionNote says what the mask means, and what it does not promise.
//
// Both halves are load-bearing. Redaction is by shape, so a secret in a format
// gluon does not recognise is shown — claiming otherwise would be the more
// dangerous error. And a value hidden that was not a secret is recoverable
// precisely because the name beside it is never hidden.
func redactionNote(n int) string {
	return "  " + plural(n, "value") +
		" redacted by shape — not a guarantee, and never by the key's name"
}

// kvTable is the two-column table both commands print, plus the column that
// marks a redacted value.
//
// The mark is a column rather than a decoration on the value, so that a value
// gluon hid and a value that happens to contain *** are not the same output.
func kvTable(rows [][]string, st pretty.Styles, nameHeader string) *table.Table {
	return table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(st.Border).
		Headers(nameHeader, "value", "").
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return st.Annot.Padding(0, 1)
			}
			switch col {
			case 0:
				return st.Type.Padding(0, 1)
			case 1:
				return st.Str.Padding(0, 1)
			default:
				return st.Annot.Padding(0, 1)
			}
		}).
		Rows(rows...)
}

// shortenPath is shorten for a path the user typed, which has no detection to
// borrow a ceiling from.
//
// When what they typed is already the shorter form, it wins: `:conf
// config/app.toml` should say config/app.toml back, not the absolute path they
// deliberately did not type.
func shortenPath(p string, c *Core) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	short := shorten(abs, c, "")
	if !filepath.IsAbs(p) && len(p) <= len(short) {
		return p
	}
	return short
}

// redactLines applies the shared secret test to a `key = value` listing, and
// reports how many values it hid.
//
// It is what plugin.Command.Redact asks for. Splitting on the first " = " is
// enough because the plugins that set the flag produce exactly that form, and a
// line that is not in it is passed through rather than guessed at — a redactor
// that reformats output it did not understand is one that loses answers.
func redactLines(text string) (string, int) {
	lines := strings.Split(text, "\n")
	n := 0
	for i, line := range lines {
		key, value, ok := strings.Cut(line, " = ")
		if !ok || value == "" {
			continue
		}
		// The key is never consulted, only skipped past — invariant 23.
		shown, hid := dsn.RedactValue(value, "")
		if !hid {
			continue
		}
		lines[i] = key + " = " + shown
		n++
	}
	if n == 0 {
		return text, 0
	}
	return strings.Join(lines, "\n"), n
}
