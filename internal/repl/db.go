package repl

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"

	"github.com/sandboxws/gluon/internal/config"
	"github.com/sandboxws/gluon/internal/db"
	"github.com/sandboxws/gluon/internal/dsn"
	"github.com/sandboxws/gluon/internal/plugin"
	"github.com/sandboxws/gluon/internal/pretty"
	"github.com/sandboxws/gluon/internal/session"
)

// databases is every configured entry that applies to the attached module,
// nearest first.
//
// The order is the one gluon states everywhere else — a project file is nearer
// than a global one — and the two are never merged: a half-merged database
// configuration is untraceable, and the symptom is a session connected to
// something nobody configured.
func (c *Core) databases() []config.Database {
	var module, dir string
	if h := c.ev.Host(); h != nil {
		module, dir = h.Path, h.Dir
	}
	if dir != "" {
		if p, ok := config.FindProject(dir); ok {
			if proj, err := config.LoadProject(p); err == nil && proj != nil && len(proj.Databases) > 0 {
				return proj.Databases
			}
		}
	}
	if c.cfg == nil {
		return nil
	}
	return c.cfg.DatabasesFor(module)
}

// pick chooses the entry a statement runs against.
//
// A bare name is matched exactly. With no name, one entry is the answer and
// several is a refusal — invariant 13: gluon does not guess between two
// databases, because the wrong guess is a query against production.
func pick(dbs []config.Database, name string) (config.Database, error) {
	switch {
	case len(dbs) == 0:
		return config.Database{}, fmt.Errorf("no database is configured")
	case name != "":
		for _, d := range dbs {
			if d.Name == name {
				return d, nil
			}
		}
		var names []string
		for _, d := range dbs {
			names = append(names, d.Label())
		}
		return config.Database{}, fmt.Errorf("no database named %q — configured: %s",
			name, strings.Join(names, ", "))
	case len(dbs) == 1:
		return dbs[0], nil
	default:
		var names []string
		for _, d := range dbs {
			names = append(names, d.Label())
		}
		sort.Strings(names)
		return config.Database{}, fmt.Errorf(
			"%d databases are configured and none is named — :query -d <name> <sql>\n      %s",
			len(dbs), strings.Join(names, "  "))
	}
}

// detectFn is db.Detect, indirected so a test can count how many times the
// project's files are actually read. Nothing replaces it in a running gluon.
var detectFn = db.Detect

// detect runs detection once per unchanged project.
//
// Invariant 22's rule: reading the project's files is a cost that belongs at a
// transition, not on every line. It is still computed on demand — only :db,
// :query and :conf ask — and it is now kept across the transitions that do not
// change what it read.
//
// What the cache is checked against is the project rather than the moment. A
// :get of an unrelated module leaves every file detection opened exactly as it
// was, and re-running the walk to discover that is the cost being removed;
// re-attaching the same directory with :use is the same case. So the check is
// the root plus a stamp of what was read, and the answer is dropped outright
// only where the host is reported to have moved — :reload, :edit's reload, and
// the watcher — which is the one signal the stamp cannot infer.
func (c *Core) detect() *db.Detection {
	root := "."
	if h := c.host(); h != nil {
		root = h.Dir
	}
	if c.detected != nil && c.detectedFrom == root &&
		db.Stamp(c.detected.Searched, c.detected.Roots) == c.detected.Stamp {
		return c.detected
	}
	var reqs []string
	if c.ev != nil {
		reqs, _ = c.ev.Requires()
	}
	got := detectFn(root, reqs)
	c.detected, c.detectedFrom = &got, root
	return c.detected
}

// dropDetection forgets where the database was.
//
// The stamp cannot see a host that moved underneath the session — a different
// checkout at the same path, a reload after :edit — so the commands that
// already say so drop it by hand.
func (c *Core) dropDetection() {
	c.detected, c.detectedFrom = nil, ""
}

// metaDB is `:db`.
func (c *Core) metaDB(arg string) Result {
	dbs := c.databases()
	if len(dbs) == 0 {
		return Result{Out: c.noDatabaseReport()}
	}
	if arg != "" {
		d, err := pick(dbs, arg)
		if err != nil {
			return Result{Out: "error: " + err.Error(), Err: true}
		}
		dbs = []config.Database{d}
	}

	st := c.styles()
	rows := make([][]string, 0, len(dbs))
	for _, d := range dbs {
		status := "ready"
		resolved, err := db.Resolve(d)
		target := d.SecretRef()
		if err != nil {
			status = "unresolved"
		} else {
			target = shownConn(resolved)
		}
		if !driverAvailable(c, resolved, d) {
			status = "no driver"
		}
		mode := "read-only"
		if !d.IsReadOnly() {
			mode = "writable"
		}
		rows = append(rows, []string{d.Label(), d.Driver, target, mode, status})
	}

	var b strings.Builder
	b.WriteString(st.Type.Render("databases") +
		st.Annot.Render(fmt.Sprintf("  %d configured", len(dbs))) + "\n")
	b.WriteString(dbTable(rows, st).String())
	if from := dbs[0].From; from != "" {
		b.WriteString("\n" + st.Annot.Render("  from "+underHome(from)))
	}
	return Result{Out: b.String()}
}

func dbTable(rows [][]string, st pretty.Styles) *table.Table {
	return table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(st.Border).
		Headers("name", "driver", "target", "mode", "").
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return st.Annot.Padding(0, 1)
			}
			switch col {
			case 0:
				return st.Type.Padding(0, 1)
			case 2:
				return st.Str.Padding(0, 1)
			default:
				return st.Annot.Padding(0, 1)
			}
		}).
		Rows(rows...)
}

// noDatabaseReport is the answer when nothing is configured.
//
// It reports what gluon found by reading the project, what it looked at, and
// what it noticed but could not use — because "no database" and "I did not
// look" are otherwise the same output, and a shrug is the thing this codebase
// refuses everywhere else.
func (c *Core) noDatabaseReport() string {
	st := c.styles()
	var b strings.Builder
	module := "this session"
	if h := c.ev.Host(); h != nil {
		module = h.Path
	}
	d := c.detect()

	switch {
	case d.Ambiguous:
		b.WriteString(st.Type.Render(module) +
			" — sources name different databases, and gluon will not guess:\n")
		for i, cand := range d.Candidates {
			b.WriteString(fmt.Sprintf("  %d  %-9s %s\n", i+1, cand.DSN.Driver,
				st.Str.Render(cand.DSN.Redacted())))
			for _, p := range cand.From {
				b.WriteString(st.Annot.Render("       "+shorten(p.String(), c, d.Ceiling)) + "\n")
			}
		}
		b.WriteString(st.Annot.Render("  gluon init records one — or both, under names"))
		return b.String()

	case d.Chosen != nil:
		b.WriteString(st.Type.Render(d.Chosen.DSN.Driver) + "  " +
			st.Str.Render(d.Chosen.DSN.Redacted()) + "\n")
		b.WriteString(st.Annot.Render("  from") + "\n")
		for _, p := range d.Chosen.From {
			b.WriteString("    " + st.Annot.Render(shorten(p.String(), c, d.Ceiling)) + "\n")
		}
		if d.Note != "" {
			b.WriteString("  " + st.Note.Render("note  "+d.Note) + "\n")
		}
		for _, con := range d.Chosen.Concerns {
			b.WriteString("  " + st.Note.Render("note  "+con) + "\n")
		}
		b.WriteString(c.driverLine(d))
		b.WriteString(st.Annot.Render("  not configured — gluon init records this. " +
			"It stores the name of a variable, never its value."))
		return b.String()
	}

	b.WriteString("no database found for " + st.Type.Render(module) + "\n")
	// Where it looked, not only what it read. With nothing found there are no
	// files to list, and that is exactly when "did not look" and "found
	// nothing" would otherwise be the same output.
	if len(d.Roots) > 0 {
		b.WriteString(st.Annot.Render("  looked in") + "\n")
		for _, dir := range d.Roots {
			b.WriteString(st.Annot.Render("    "+shorten(dir, c, d.Ceiling)) + "\n")
		}
	}
	if n := len(d.Searched); n > 0 {
		b.WriteString(st.Annot.Render("  read") + "\n")
		for _, p := range d.Searched[:min(n, 6)] {
			b.WriteString(st.Annot.Render("    "+shorten(p, c, d.Ceiling)) + "\n")
		}
		if n > 6 {
			b.WriteString(st.Annot.Render(fmt.Sprintf("    and %d more", n-6)) + "\n")
		}
	}
	if d.Ceiling != "" {
		b.WriteString(st.Annot.Render("  stopped at the repository root") + "\n")
	}
	b.WriteString(c.driverLine(d))
	b.WriteString(st.Annot.Render("  gluon init records where a database is. " +
		"It stores the name of a variable, never its value."))
	return b.String()
}

// driverLine reports the drivers the build list provides.
//
// It is deliberately separate from the candidates: a driver says which database
// gluon *could* open, never that one exists. A project requiring lib/pq and
// configuring nothing is a different answer from one with a DSN and no driver,
// and both halves are needed before :query can work.
func (c *Core) driverLine(d *db.Detection) string {
	if len(d.Drivers) == 0 {
		return ""
	}
	st := c.styles()
	var b strings.Builder
	b.WriteString(st.Annot.Render("  driver") + "\n")
	for _, drv := range d.Drivers {
		b.WriteString("    " + st.Str.Render(drv.Module) +
			st.Annot.Render("  a "+drv.Family+" driver, in the build list") + "\n")
	}
	return b.String()
}

// shownConn is a connection as a report prints it: redacted, and a database
// file under the home directory written from ~. An absolute path is the widest
// thing in :db's table and the longest in :query's footer, and the part of it
// that is the home directory says nothing but whose machine this is — which a
// transcript pasted into an issue should not. JSON keeps the whole path: a
// script opens it.
func shownConn(d dsn.DSN) string {
	if d.Shape == dsn.ShapeFile {
		return underHome(d.Redacted())
	}
	return d.Redacted()
}

// underHome writes a path under the home directory from ~, and any other path
// as it is.
func underHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || !filepath.IsAbs(p) {
		return p
	}
	if rel, err := filepath.Rel(home, p); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
		if rel == "." {
			return "~"
		}
		return "~/" + rel
	}
	return p
}

// shorten trims a path down to what is worth reading.
//
// A report full of absolute paths is a report nobody scans. Relative to the
// module is best; relative to the repository is next, because a compose file
// one level up is genuinely outside the module and saying so is the point; and
// ~ for anything else.
func shorten(p string, c *Core, ceiling string) string {
	// A Core with no evaluator is a real state — :env and :conf answer before
	// one exists, and the registry tests build one — so this asks rather than
	// assuming there is a host to be relative to.
	if h := c.host(); h != nil && h.Dir != "" {
		if rel, err := filepath.Rel(h.Dir, p); err == nil && !strings.HasPrefix(rel, "..") {
			if rel == "." {
				return "."
			}
			return rel
		}
	}
	if ceiling != "" {
		if rel, err := filepath.Rel(ceiling, p); err == nil && !strings.HasPrefix(rel, "..") {
			if rel == "." {
				return "<repo>"
			}
			return "<repo>/" + rel
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func driverAvailable(c *Core, d dsn.DSN, cfg config.Database) bool {
	reqs, err := c.ev.Requires()
	if err != nil {
		return false
	}
	family := d.Driver
	if family == "" {
		family = cfg.Driver
	}
	_, ok := db.DriverFor(family, db.DriversIn(reqs), cfg.DriverModule)
	return ok
}

// metaQuery is `:query`.
//
// It is a builtin rather than a plugin command because a plugin's only
// mechanism is Rewrite(arg string), which sees its argument and nothing else —
// it cannot reach the config, the resolved connection, or the evaluator. :use
// and :get are builtins for the same reason, and this is their sibling.
func (c *Core) metaQuery(arg string) Result {
	name, write, asJSON, sql := parseQueryArgs(arg)

	// The statement is checked before anything is resolved. :query with no
	// argument is a usage question, and answering it with "no database is
	// configured" would send somebody to fix a config when they had simply not
	// typed a query yet.
	if err := db.CheckStatement(sql, write); err != nil {
		return queryErr(asJSON, err.Error(), "")
	}

	cfg, err := pick(c.databases(), name)
	if err != nil {
		return queryErr(asJSON, err.Error(), "")
	}
	if !cfg.IsReadOnly() && !write {
		// A database marked writable in config still needs -w per statement.
		// One says "this connection may write", the other says "this line
		// means to".
		write = false
	}

	conn, err := db.Resolve(cfg)
	if err != nil {
		return queryErr(asJSON, err.Error(), "")
	}

	reqs, rerr := c.ev.Requires()
	if rerr != nil {
		return queryErr(asJSON, "could not read the build list: "+rerr.Error(), conn.Redacted())
	}
	family := conn.Driver
	if family == "" {
		family = cfg.Driver
	}
	drv, ok := db.DriverFor(family, db.DriversIn(reqs), cfg.DriverModule)
	if !ok {
		// The prose form is several lines and says what to do about it; the
		// envelope carries the same text with the prefix metaJSON also strips,
		// rather than a second, shorter sentence saying the same thing.
		msg := noDriverMessage(c, family)
		res := Result{Out: msg, Err: true}
		if asJSON {
			res.Query = &QueryData{
				Result: db.Result{Err: strings.TrimPrefix(msg, "error: ")},
				Target: conn.Redacted(),
			}
		}
		return res
	}

	st := db.Statement{SQL: sql, Write: write}
	if !cfg.IsReadOnly() {
		st.Write = write
	}
	src, err := db.Rewrite(st, drv)
	if err != nil {
		return queryErr(asJSON, err.Error(), conn.Redacted())
	}

	// The secret reaches the child through its environment and nothing else.
	// The source names a variable, so neither <tmp>/main.go, :src, :save nor a
	// build error can carry it — and mask covers the one surface left, which
	// is what the driver itself prints.
	env := []string{db.EnvDSN + "=" + conn.ConnectString()}
	redact := []string{conn.ConnectString()}
	if pw := conn.Password(); pw != "" {
		redact = append(redact, pw)
	}

	res, err := c.ev.EvalLive(c.sess,
		session.Entry{Kind: session.KindExpr, Src: src},
		db.Imports(drv), env, redact)
	if err != nil {
		return queryErr(asJSON, err.Error(), conn.Redacted())
	}

	_, vals := pretty.Parse(res.Output)
	if len(vals) == 0 {
		return queryErr(asJSON, "the query produced no answer", conn.Redacted())
	}
	return c.renderQuery(db.ParseResult(vals[0].Repr), conn, asJSON)
}

// parseQueryArgs splits the flags from the statement.
//
// Flags are read only while they lead, because SQL routinely contains a bare -
// and everything after the first non-flag word is the statement verbatim. The
// statement is never rewritten — no injected LIMIT — since a LIMIT added to a
// query that has one, or on a dialect that spells it TOP, is quiet wrongness.
func parseQueryArgs(arg string) (name string, write, asJSON bool, sql string) {
	rest := strings.TrimSpace(arg)
	for {
		switch {
		case strings.HasPrefix(rest, "-w "), rest == "-w":
			write = true
			rest = strings.TrimSpace(strings.TrimPrefix(rest, "-w"))
		case strings.HasPrefix(rest, "-json "), rest == "-json":
			asJSON = true
			rest = strings.TrimSpace(strings.TrimPrefix(rest, "-json"))
		case strings.HasPrefix(rest, "-d "):
			rest = strings.TrimSpace(strings.TrimPrefix(rest, "-d"))
			name, rest, _ = strings.Cut(rest, " ")
			rest = strings.TrimSpace(rest)
		default:
			return name, write, asJSON, rest
		}
	}
}

// queryJSONNote is what a terminal is told about -json.
//
// The flag is answered here rather than refused. Refusing would make one that
// works through a pipe an error at the prompt; :edit refuses because it
// genuinely needs a terminal to hand over, and this needs nothing at all. So
// the table is printed as always and one line says where the flag does
// something.
const queryJSONNote = "  -json applies to gluon -e and pipes"

// queryErr is a failure a script reading the envelope can still decode.
//
// A flag that changes the shape of the answer has to change the shape of the
// failures too, or a consumer decodes one envelope when the query worked and a
// different one whenever anything went wrong — which is the case it is least
// equipped to handle. target is the redacted connection string when there is
// one yet, and empty when the failure happened before a database was resolved.
func queryErr(asJSON bool, msg, target string) Result {
	res := Result{Out: "error: " + msg, Err: true}
	if asJSON {
		res.Query = &QueryData{Result: db.Result{Err: msg}, Target: target}
	}
	return res
}

func noDriverMessage(c *Core, family string) string {
	st := c.styles()
	mods := db.KnownDriverModules(family)
	module := "this session"
	if h := c.ev.Host(); h != nil {
		module = h.Path
	}
	var b strings.Builder
	b.WriteString("error: :query needs a " + family + " driver, and " + module +
		"'s build list has none.\n")
	b.WriteString(st.Annot.Render("      gluon builds against the host's own modules and never links a driver itself.\n"))
	if len(mods) > 0 {
		b.WriteString(st.Annot.Render("      add one to the project, or  :get " + mods[0] + "  for this session only"))
	}
	return b.String()
}

// renderQuery turns a result set into what scrollback shows.
//
// A large one goes to a modal, exactly as :inspect does, and Result.Out always
// carries the same rows linearly so a pipe loses nothing — invariant 19.
func (c *Core) renderQuery(r db.Result, conn dsn.DSN, asJSON bool) Result {
	st := c.styles()
	// A statement the database rejected is a failure with a target and no
	// rows, and under -json it is still the query envelope: the shape of the
	// answer is what the flag chose, and a consumer should not have to decode
	// two of them to find out that the statement did not run.
	if r.Err != "" {
		res := Result{Out: "error: " + r.Err + "\n" +
			st.Annot.Render("      "+conn.Redacted()), Err: true}
		if asJSON {
			res.Query = &QueryData{Result: r, Target: conn.Redacted()}
		}
		return res
	}

	var notes []string
	if r.More {
		notes = append(notes, fmt.Sprintf("more rows were not fetched — %d is the cap", db.MaxRows))
	}
	if r.Note != "" {
		notes = append(notes, r.Note)
	}

	linear := c.queryText(r, conn, notes)
	// A result that fits is printed; one that does not gets the same scrollable
	// view :inspect opens, and Out still carries every row linearly so a pipe
	// loses nothing. The threshold is pretty.MaxRows, so a result set and a
	// slice of the same length agree about what is too big to print.
	out := Result{Out: linear}
	if len(r.Rows) > pretty.MaxRows {
		out.Modal = &ModalSpec{
			Title:   fmt.Sprintf("%d rows · %s", len(r.Rows), shownConn(conn)),
			Summary: fmt.Sprintf("%d rows browsed · %s", len(r.Rows), shownConn(conn)),
			Headers: r.Cols,
			Rows:    displayRows(r),
			Note:    strings.Join(notes, " · "),
		}
	}
	// Set last and alongside the linear form, never instead of it: Out is what
	// every other driver reads, and this field is advisory the way Modal is.
	if asJSON {
		out.Query = &QueryData{Result: r, Target: conn.Redacted()}
	}
	return out
}

func (c *Core) queryText(r db.Result, conn dsn.DSN, notes []string) string {
	st := c.styles()
	var b strings.Builder
	if len(r.Rows) == 0 {
		b.WriteString(st.Annot.Render("no rows"))
	} else {
		b.WriteString(queryTable(r, displayRows(r), st).String())
	}
	b.WriteString("\n" + st.Annot.Render(fmt.Sprintf("  %s · %s", plural(len(r.Rows), "row"), shownConn(conn))))
	for _, n := range notes {
		b.WriteString("\n" + st.Note.Render("  "+n))
	}
	return b.String()
}

// displayRows disambiguates SQL NULL from the text "NULL".
//
// Colour alone cannot carry that distinction: a NULL is styled as a note, but
// styling is stripped through a pipe, and the linear form is what gluon -e and
// a script read. So a *text* cell that would be misread as NULL is quoted, and
// only that one — quoting every string would make an ordinary result set
// unreadable to save one ambiguous case.
//
// The two are different answers and one of them is usually what somebody is
// looking for, which is the whole reason the child sends a sentinel rather than
// the word.
func displayRows(r db.Result) [][]string {
	out := make([][]string, len(r.Rows))
	for i, row := range r.Rows {
		cells := make([]string, len(row))
		copy(cells, row)
		for j, c := range cells {
			isNull := i < len(r.Null) && j < len(r.Null[i]) && r.Null[i][j]
			if !isNull && c == "NULL" {
				cells[j] = `"NULL"`
			}
		}
		out[i] = cells
	}
	return out
}

func queryTable(r db.Result, rows [][]string, st pretty.Styles) *table.Table {
	return table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(st.Border).
		Headers(r.Cols...).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return st.Type.Padding(0, 1)
			}
			// A NULL is styled as a note, the same look the sql plugin already
			// gives sql.NullString — one NULL, one appearance, wherever it
			// comes from.
			if row < len(r.Null) && col < len(r.Null[row]) && r.Null[row][col] {
				return st.Note.Padding(0, 1)
			}
			return st.Str.Padding(0, 1)
		}).
		Rows(rows...)
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// styles is the palette, or the plain one when Core has none — a pipe, or a
// test.
//
// The plain branch used to hand back a *coloured* palette, and stayed invisible
// only because lipgloss's global renderer degrades on a non-TTY. It was
// reachable: Rich is set in exactly one place, newModel, so `gluon -e` from a
// terminal took this branch and `-json` then carried escape sequences inside a
// JSON string — a frozen envelope, invariant 21. Not rich now means not
// coloured, by construction rather than by the terminal's profile.
func (c *Core) styles() pretty.Styles {
	if c.Rich {
		return c.Styles
	}
	return pretty.PlainStyles()
}

// runDBQuery answers a plugin command whose answer is in the project's own
// database.
//
// It is metaQuery with the statement already decided. Everything that reaches a
// database is the same call in the same order — pick, db.Resolve, db.DriverFor,
// EvalLive with db.Imports(drv) — because a second route to a database would be
// a second place a DSN could reach generated source, and invariants 24 and 25
// are the two this codebase guards most carefully. Nothing here opens a
// connection; the child does, from an environment variable, exactly as :query's
// child does.
func (c *Core) runDBQuery(name string, q *plugin.DBQuery, arg string) Result {
	which, err := parseDBQueryArg(name, arg)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}

	// The statement is the plugin's, and it still goes through the check. The
	// read-only guarantee is then the allowlist that already exists rather
	// than a promise made where the statement was written — a plugin that
	// declared a DELETE would be refused here, by the same code that refuses
	// one typed at the prompt.
	if err := db.CheckStatement(q.SQL, false); err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}

	cfg, err := pick(c.databases(), which)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	conn, err := db.Resolve(cfg)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}

	reqs, rerr := c.ev.Requires()
	if rerr != nil {
		return Result{Out: "error: could not read the build list: " + rerr.Error(), Err: true}
	}
	family := conn.Driver
	if family == "" {
		family = cfg.Driver
	}
	drv, ok := db.DriverFor(family, db.DriversIn(reqs), cfg.DriverModule)
	if !ok {
		return Result{Out: noDriverMessage(c, family), Err: true}
	}

	src, err := db.Rewrite(db.Statement{SQL: q.SQL}, drv)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}

	env := []string{db.EnvDSN + "=" + conn.ConnectString()}
	redact := []string{conn.ConnectString()}
	if pw := conn.Password(); pw != "" {
		redact = append(redact, pw)
	}

	res, err := c.ev.EvalLive(c.sess,
		session.Entry{Kind: session.KindExpr, Src: src},
		db.Imports(drv), env, redact)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}

	_, vals := pretty.Parse(res.Output)
	if len(vals) == 0 {
		return Result{Out: "error: the query produced no answer", Err: true}
	}
	parsed := db.ParseResult(vals[0].Repr)
	if parsed.Err != "" {
		return Result{Out: dbQueryFailure(c, name, q, parsed.Err, conn), Err: true}
	}
	if len(parsed.Rows) == 0 && q.Empty != "" {
		// "no rows" is true and useless here. An empty tracking table means
		// either nothing has ever been applied or the table belongs to
		// another tool, and only the plugin knows which sentence to write.
		return Result{Out: c.styles().Note.Render(q.Empty) + "\n" +
			c.styles().Annot.Render("  "+q.Table+" · "+conn.Redacted())}
	}
	return c.renderQuery(parsed, conn, false)
}

// dbQueryFailure names the table.
//
// A tracking schema that has moved on surfaces as a driver error about a
// column, or about a relation that does not exist — neither of which says which
// table gluon went looking in, or that the statement was gluon's rather than
// something the session typed. Both halves are one step of diagnosis each.
func dbQueryFailure(c *Core, name string, q *plugin.DBQuery, msg string, conn dsn.DSN) string {
	st := c.styles()
	return "error: " + msg + "\n" +
		st.Annot.Render("      "+name+" read "+q.Table+" on "+conn.Redacted()) + "\n" +
		st.Annot.Render("      "+q.SQL)
}

// parseDBQueryArg reads which database the command should use.
//
// A bare name is the spelling :db uses and -d <name> is :query's; both work,
// because the two commands this one sits between spell it differently and
// guessing wrong is a refusal rather than a typo.
//
// A connection string is refused outright. `-dsn` and `:db connect <url>` are
// both on ROADMAP.md's permanently-rejected list for the same reason — either
// would put a password in ~/.local/state/gluon/history in plaintext — and a
// command that accepted one here would be the third spelling of the thing
// twice rejected.
func parseDBQueryArg(name, arg string) (string, error) {
	which := strings.TrimSpace(arg)
	if rest, ok := strings.CutPrefix(which, "-d"); ok {
		which = strings.TrimSpace(rest)
	}
	if which == "" {
		return "", nil
	}
	if looksLikeDSN(which) {
		return "", fmt.Errorf("%s takes the name of a configured database, not a connection string.\n"+
			"      A connection is configured rather than passed, so no password reaches\n"+
			"      your shell history — :db lists what is configured, and gluon init adds one",
			name)
	}
	if strings.ContainsAny(which, " \t") {
		return "", fmt.Errorf("%s takes one name — :db lists what is configured", name)
	}
	return which, nil
}

// looksLikeDSN reports whether an argument is a connection string rather than a
// configured name.
//
// It is deliberately broad. A configured name is an identifier somebody wrote
// in a config file; every character this rejects is one that cannot appear in
// one and does appear in some driver's connection syntax — a URL, a libpq
// keyword string, a MySQL DSN, a file path. Being wrong in this direction costs
// a clear message; being wrong in the other prints a password.
func looksLikeDSN(s string) bool {
	return strings.ContainsAny(s, "@=/\\:?&")
}
