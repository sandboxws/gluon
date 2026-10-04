package repl

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/config"
	"github.com/sandboxws/gluon/internal/db"
	"github.com/sandboxws/gluon/internal/dsn"
	"github.com/sandboxws/gluon/internal/plugin"
	plugindb "github.com/sandboxws/gluon/internal/plugins/db"
)

// TestQueryAndQuitDoNotCollide.
//
// :q is quit, and its handler ignores its argument — so a :q that also meant
// "query" would exit the REPL the moment somebody typed :q SELECT ... The
// registry's first-match walk makes that silent, which is why the query command
// is :query and this is what holds the two apart.
func TestQueryAndQuitDoNotCollide(t *testing.T) {
	c := &Core{}
	quit, ok := c.lookup(":q")
	if !ok {
		t.Fatal(":q is not registered")
	}
	if !quit.Run(c, "SELECT 1").Quit {
		t.Error(":q stopped meaning quit")
	}
	query, ok := c.lookup(":query")
	if !ok {
		t.Fatal(":query is not registered")
	}
	if query.Name == quit.Name {
		t.Error(":query resolves to :q")
	}
	// The alias must reach the same place, and must not be :q either.
	alias, ok := c.lookup(":qq")
	if !ok {
		t.Fatal(":qq is not registered")
	}
	if alias.Name != ":query" {
		t.Errorf(":qq resolves to %s, want :query", alias.Name)
	}
}

// TestParseQueryArgsReadsFlagsOnlyWhileTheyLead.
//
// SQL routinely contains a bare -, and everything after the first non-flag word
// is the statement verbatim. A parser that kept scanning would mangle
// `SELECT a -1 FROM t`, and the statement is never rewritten precisely so that
// what runs is what was typed.
func TestParseQueryArgsReadsFlagsOnlyWhileTheyLead(t *testing.T) {
	for _, tc := range []struct {
		in     string
		name   string
		write  bool
		asJSON bool
		sql    string
	}{
		{"SELECT 1", "", false, false, "SELECT 1"},
		{"-w DELETE FROM users", "", true, false, "DELETE FROM users"},
		{"-d analytics SELECT 1", "analytics", false, false, "SELECT 1"},
		{"-w -d analytics DELETE FROM t", "analytics", true, false, "DELETE FROM t"},
		{"-d analytics -w DELETE FROM t", "analytics", true, false, "DELETE FROM t"},
		// -json in every position a leading flag can hold, and beside each of
		// the other two: a flag order that works is not a property anyone
		// should have to discover.
		{"-json SELECT 1", "", false, true, "SELECT 1"},
		{"-json -w DELETE FROM t", "", true, true, "DELETE FROM t"},
		{"-w -json DELETE FROM t", "", true, true, "DELETE FROM t"},
		{"-json -d analytics SELECT 1", "analytics", false, true, "SELECT 1"},
		{"-d analytics -json SELECT 1", "analytics", false, true, "SELECT 1"},
		{"-d analytics -w -json DELETE FROM t", "analytics", true, true, "DELETE FROM t"},
		{"-json", "", false, true, ""},
		// A dash inside the statement is part of the statement.
		{"SELECT a -1 FROM t", "", false, false, "SELECT a -1 FROM t"},
		{"SELECT '-w' FROM t", "", false, false, "SELECT '-w' FROM t"},
		// The text -json past the leading run is the statement's, not gluon's.
		{"SELECT '-json' FROM t", "", false, false, "SELECT '-json' FROM t"},
		{"SELECT -json FROM t", "", false, false, "SELECT -json FROM t"},
		{"", "", false, false, ""},
	} {
		name, write, asJSON, sql := parseQueryArgs(tc.in)
		if name != tc.name || write != tc.write || asJSON != tc.asJSON || sql != tc.sql {
			t.Errorf("parseQueryArgs(%q) = (%q, %v, %v, %q), want (%q, %v, %v, %q)",
				tc.in, name, write, asJSON, sql, tc.name, tc.write, tc.asJSON, tc.sql)
		}
	}
}

// TestPickRefusesToGuessBetweenTwoDatabases.
//
// Invariant 13. The wrong guess here is a statement against the wrong database,
// which is the one failure mode where being helpful is worse than being unable.
func TestPickRefusesToGuessBetweenTwoDatabases(t *testing.T) {
	two := []config.Database{
		{Name: "primary", Driver: "postgres", DSNEnv: "A"},
		{Name: "analytics", Driver: "postgres", DSNEnv: "B"},
	}
	_, err := pick(two, "")
	if err == nil {
		t.Fatal("pick chose between two databases")
	}
	for _, want := range []string{"primary", "analytics", "-d"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}

	// Named, it is not a guess.
	got, err := pick(two, "analytics")
	if err != nil || got.Name != "analytics" {
		t.Errorf("pick(analytics) = %v, %v", got.Name, err)
	}
	// One is the answer without being asked.
	got, err = pick(two[:1], "")
	if err != nil || got.Name != "primary" {
		t.Errorf("pick with one entry = %v, %v", got.Name, err)
	}
}

// TestPickNamesTheAlternativesWhenTheNameIsWrong — a typo'd name should not
// read the same as no databases at all.
func TestPickNamesTheAlternativesWhenTheNameIsWrong(t *testing.T) {
	_, err := pick([]config.Database{{Name: "primary", DSNEnv: "A"}}, "primry")
	if err == nil {
		t.Fatal("a wrong name was accepted")
	}
	if !strings.Contains(err.Error(), "primary") {
		t.Errorf("the error does not list what is configured: %v", err)
	}
}

// TestSQLNullIsDistinguishableFromTheTextNULLWithoutColour.
//
// The child sends a sentinel so the two can be told apart, and the renderer
// styles a NULL as a note — but styling is stripped through a pipe, and the
// linear form is what gluon -e and a script read. Without quoting the ambiguous
// text cell, the sentinel buys nothing exactly where it is needed most.
//
// Only the ambiguous cell is quoted: quoting every string would make an
// ordinary result set unreadable to disambiguate one case.
func TestSQLNullIsDistinguishableFromTheTextNULLWithoutColour(t *testing.T) {
	r := db.ParseResult(strings.Join([]string{
		"cols\x1fid\x1fnote",
		"row\x1f1\x1f\x00",
		"row\x1f2\x1fNULL",
		"row\x1f3\x1fordinary",
	}, "\n"))

	rows := displayRows(r)
	if rows[0][1] != "NULL" {
		t.Errorf("a SQL NULL renders as %q, want NULL", rows[0][1])
	}
	if rows[1][1] != `"NULL"` {
		t.Errorf("the text NULL renders as %q, want it quoted", rows[1][1])
	}
	if rows[0][1] == rows[1][1] {
		t.Error("SQL NULL and the text NULL are indistinguishable in plain output")
	}
	if rows[2][1] != "ordinary" {
		t.Errorf("an ordinary string was quoted: %q", rows[2][1])
	}
}

// TestDisplayRowsDoesNotMutateTheResult — the parsed result is also what the
// modal and any future -json form read; rewriting it in place would make the
// two disagree.
func TestDisplayRowsDoesNotMutateTheResult(t *testing.T) {
	r := db.ParseResult("cols\x1fnote\nrow\x1fNULL\n")
	_ = displayRows(r)
	if r.Rows[0][0] != "NULL" {
		t.Errorf("displayRows rewrote the parsed result: %q", r.Rows[0][0])
	}
}

// TestQueryWithNoArgumentReportsUsage.
//
// Ordering matters here: :query with nothing typed is a usage question, and
// answering it with "no database is configured" would send somebody to fix a
// config when they had simply not written a statement yet.
func TestQueryWithNoArgumentReportsUsage(t *testing.T) {
	c := testCore(t)
	res := c.Submit(":query")
	if !res.Err || !strings.Contains(res.Out, "usage:") {
		t.Errorf(":query bare = %q (err=%v), want a usage line", res.Out, res.Err)
	}
}

// TestDBWithNoConfigurationSaysSoWithoutFailing.
//
// Most projects will never configure one, and that has to be an ordinary answer
// rather than an error — the same stance gluon doctor takes about not being
// inside a module.
func TestDBWithNoConfigurationSaysSoWithoutFailing(t *testing.T) {
	c := testCore(t)
	res := c.Submit(":db")
	if res.Err {
		t.Errorf(":db with nothing configured reported an error: %q", res.Out)
	}
	if !strings.Contains(res.Out, "no database found") {
		t.Errorf(":db said %q", res.Out)
	}
	// It must point somewhere, rather than only reporting an absence.
	if !strings.Contains(res.Out, "gluon init") {
		t.Errorf(":db reported an absence with no next step: %q", res.Out)
	}
}

// gooseQuery is the goose plugin's declared statement, used by the tests that
// cover the path a plugin command takes to a database rather than the plugin
// itself.
func gooseQuery(t *testing.T) *plugin.DBQuery {
	t.Helper()
	for _, c := range (plugindb.Goose{}).Commands() {
		if c.Query != nil {
			return c.Query
		}
	}
	t.Fatal("the goose plugin declares no query")
	return nil
}

// TestMigrationsRefusesAConnectionString.
//
// `-dsn` and `:db connect <url>` are both on the permanently-rejected list, for
// one reason: either would put a password in ~/.local/state/gluon/history in
// plaintext. A command that took one here would be the third spelling of the
// same rejected thing, so the refusal has to say what the alternative is rather
// than only that the argument is wrong.
func TestMigrationsRefusesAConnectionString(t *testing.T) {
	for _, arg := range []string{
		"postgres://user:secret@localhost:5432/app",
		"-d postgres://user:secret@localhost/app",
		"host=localhost user=app password=secret dbname=app",
		"user:secret@tcp(127.0.0.1:3306)/app",
		"./data/app.db",
	} {
		_, err := parseDBQueryArg(":migrations", arg)
		if err == nil {
			t.Errorf("a connection string was accepted: %q", arg)
			continue
		}
		if !strings.Contains(err.Error(), "configured") {
			t.Errorf("the refusal of %q does not say a connection is configured: %v", arg, err)
		}
		// The message must not echo what was typed, or refusing the secret
		// would be the thing that records it.
		if strings.Contains(err.Error(), "secret") {
			t.Errorf("the refusal of %q repeated the password back: %v", arg, err)
		}
	}

	// A configured name is what it does take, in either spelling.
	for arg, want := range map[string]string{
		"":            "",
		"primary":     "primary",
		"-d primary":  "primary",
		"  analytics": "analytics",
	} {
		got, err := parseDBQueryArg(":migrations", arg)
		if err != nil {
			t.Errorf("parseDBQueryArg(%q): %v", arg, err)
		}
		if got != want {
			t.Errorf("parseDBQueryArg(%q) = %q, want %q", arg, got, want)
		}
	}
}

// TestMigrationsRefusesToGuessBetweenTwoDatabases is invariant 13 on this path.
// The wrong guess is a query against the wrong database, and reading a
// migration table on production because two were configured is exactly the
// failure pick exists to prevent.
func TestMigrationsRefusesToGuessBetweenTwoDatabases(t *testing.T) {
	c := testCore(t)
	c.cfg.Databases = []config.Database{
		{Name: "primary", Driver: "postgres", DSNEnv: "GLUON_TEST_A"},
		{Name: "analytics", Driver: "postgres", DSNEnv: "GLUON_TEST_B"},
	}
	res := c.runDBQuery(":migrations", gooseQuery(t), "")
	if !res.Err {
		t.Fatalf(":migrations chose between two databases: %s", res.Out)
	}
	for _, want := range []string{"primary", "analytics"} {
		if !strings.Contains(res.Out, want) {
			t.Errorf("the refusal does not list %q: %s", want, res.Out)
		}
	}
}

// TestMigrationsReportsTheMissingDriver, through the same noDriverMessage
// :query uses. gluon links no driver, so "which module would provide one" is
// the only useful thing to say — and saying nothing looks like a broken
// install.
func TestMigrationsReportsTheMissingDriver(t *testing.T) {
	c := testCore(t)
	t.Setenv("GLUON_TEST_DSN", "postgres://app@localhost:5432/app")
	c.cfg.Databases = []config.Database{
		{Name: "primary", Driver: "postgres", DSNEnv: "GLUON_TEST_DSN"},
	}
	res := c.runDBQuery(":migrations", gooseQuery(t), "")
	if !res.Err {
		t.Fatalf(":migrations ran with no driver in the build list: %s", res.Out)
	}
	if !strings.Contains(res.Out, ":get ") {
		t.Errorf("the report does not name a module to add: %s", res.Out)
	}
	if !strings.Contains(res.Out, "never links a driver") {
		t.Errorf("the report does not say why gluon has none: %s", res.Out)
	}
}

// TestMigrationsStatementPassesTheReadOnlyAllowlist.
//
// The statement is the plugin's, and it still goes through db.CheckStatement
// with the write flag unset. That is the point: read-only here is the allowlist
// that already exists, enforced by the same code that refuses a DELETE typed at
// the prompt, rather than a second promise made where the statement was
// written.
func TestMigrationsStatementPassesTheReadOnlyAllowlist(t *testing.T) {
	c := testCore(t)
	c.cfg.Databases = []config.Database{
		{Name: "primary", Driver: "sqlite", File: "/tmp/does-not-matter.db"},
	}
	writes := &plugin.DBQuery{
		Table: "goose_db_version",
		SQL:   "DELETE FROM goose_db_version",
	}
	res := c.runDBQuery(":migrations", writes, "")
	if !res.Err {
		t.Fatal("a write statement was accepted from a plugin")
	}
	if !strings.Contains(res.Out, "not a read-only statement") {
		t.Errorf("the refusal does not come from the allowlist: %s", res.Out)
	}
	// It is refused before anything is resolved, so a plugin that declared one
	// cannot reach a database even by accident.
	if strings.Contains(res.Out, "driver") {
		t.Errorf("the statement was checked after the connection was chosen: %s", res.Out)
	}
}

// TestMigrationsNamesTheTableItRead.
//
// A tracking schema that has moved on surfaces as a driver error about a
// column. "no such column: is_applied" says nothing about which table gluon
// went looking in, or that the statement was gluon's rather than something the
// session typed — and both are one step of diagnosis each.
func TestMigrationsNamesTheTableItRead(t *testing.T) {
	c := testCore(t)
	q := gooseQuery(t)
	conn, err := dsn.Parse("postgres://app:hunter2@localhost:5432/app", "")
	if err != nil {
		t.Fatal(err)
	}
	out := dbQueryFailure(c, ":migrations", q, "no such column: is_applied", conn)
	for _, want := range []string{"no such column: is_applied", "goose_db_version", ":migrations"} {
		if !strings.Contains(out, want) {
			t.Errorf("the failure does not report %q:\n%s", want, out)
		}
	}
	// The statement is shown so the mismatch is visible, and the connection is
	// named — redacted, because it is the one surface a password could reach.
	if !strings.Contains(out, q.SQL) {
		t.Errorf("the failure does not show the statement:\n%s", out)
	}
	if strings.Contains(out, "hunter2") {
		t.Errorf("the failure printed the password:\n%s", out)
	}
}

// TestOnlyQueryUnderJSONCarriesStructuredData.
//
// Result.Query is advisory the way Modal is, and every driver decides what to
// do with it by testing for nil. A command that set it by accident would make
// `gluon -e -json` answer that command with :query's envelope instead of the
// one every meta command shares — which is exactly the frozen surface this
// change is not allowed to move.
func TestOnlyQueryUnderJSONCarriesStructuredData(t *testing.T) {
	c := testCore(t)
	for _, src := range []string{
		":help", ":plugins", ":ls", ":env", ":conf", ":hist", ":src",
		":db", ":theme", ":settings",
		// :query without the flag is the case most likely to regress: the
		// whole path runs and only the last field is meant to differ.
		":query SELECT 1",
		":query -w DELETE FROM t",
	} {
		if res := c.Submit(src); res.Query != nil {
			t.Errorf("%s set Result.Query to %+v, want nil", src, res.Query)
		}
	}
}

// TestQueryDataCarriesNoSecret.
//
// Invariants 24 and 25. The resolved DSN reaches the child through its
// environment and nothing else, so the one place a password could still escape
// is a field somebody added to the envelope holding the string it was resolved
// from. The redacted form is the only spelling of the target that may leave
// this process, and this is what says so.
func TestQueryDataCarriesNoSecret(t *testing.T) {
	const raw = "postgres://app:hunter2@db.internal:5432/acme"
	conn, err := dsn.Parse(raw, "")
	if err != nil {
		t.Fatal(err)
	}
	if conn.Password() != "hunter2" {
		t.Fatalf("the fixture has no password to leak: %q", conn.Password())
	}

	c := &Core{}
	got := c.renderQuery(db.Result{
		Cols: []string{"id"},
		Rows: [][]string{{"1"}},
		Null: [][]bool{{false}},
	}, conn, true)
	if got.Query == nil {
		t.Fatal("-json produced no structured answer")
	}
	if got.Query.Target != conn.Redacted() {
		t.Errorf("Target = %q, want the redacted form %q", got.Query.Target, conn.Redacted())
	}

	var buf bytes.Buffer
	if err := WriteQueryJSON(&buf, got.Query); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"hunter2", raw, conn.ConnectString()} {
		if strings.Contains(buf.String(), secret) {
			t.Errorf("the envelope carries %q:\n%s", secret, buf.String())
		}
	}

	// The same for a failure, which is the path that carries text the driver
	// wrote rather than text gluon wrote.
	failed := c.renderQuery(db.Result{Err: "connection refused"}, conn, true)
	if failed.Query == nil {
		t.Fatal("a failed statement produced no structured answer")
	}
	if len(failed.Query.Rows) != 0 {
		t.Errorf("a failure carries rows: %v", failed.Query.Rows)
	}
	buf.Reset()
	if err := WriteQueryJSON(&buf, failed.Query); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"hunter2", raw, conn.ConnectString()} {
		if strings.Contains(buf.String(), secret) {
			t.Errorf("the failure envelope carries %q:\n%s", secret, buf.String())
		}
	}
}

// TestAReportWritesHomeAsTilde. :db's target and :query's footer name a
// database file, and under the home directory they write it from ~: shorter
// to scan, and nothing about whose machine it is in a pasted transcript. A
// path outside home, one that merely shares its prefix, and a connection
// string that is not a file are left as they are.
func TestAReportWritesHomeAsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, tc := range []struct{ in, want string }{
		{filepath.Join(home, "src", "shop", "data", "shop.db"), "~/src/shop/data/shop.db"},
		{home, "~"},
		{home + "x/shop.db", home + "x/shop.db"},
		{"/srv/shop.db", "/srv/shop.db"},
		{"data/shop.db", "data/shop.db"},
	} {
		if got := underHome(tc.in); got != tc.want {
			t.Errorf("underHome(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	file := dsn.DSN{Shape: dsn.ShapeFile, File: filepath.Join(home, "shop.db")}
	if got := shownConn(file); got != "~/shop.db" {
		t.Errorf("a file under home is shown as %q", got)
	}
}
