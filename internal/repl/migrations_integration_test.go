//go:build integration

package repl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/plugin"
	plugindb "github.com/sandboxws/gluon/internal/plugins/db"
)

// gooseStatus is the goose plugin's own declared statement.
//
// These tests drive the handler rather than `:migrations` typed at the prompt,
// and the reason is worth stating: the command exists only when goose is in the
// build list, and requiring goose here would make the tests that matter most —
// the ones about where a connection string can reach — skip on any machine
// whose module cache lacks it. Activation is pinned in the fast tier
// (TestEveryMigrationLibraryActivatesOnItsPublishedModulePath), and dispatch is
// pinned by TestMigrationsAppearsWhenGooseReachesTheBuildList below. What is
// left is the path to the database, which is this, and it is the same call the
// dispatched command makes.
func gooseStatus(t *testing.T) *plugin.DBQuery {
	t.Helper()
	for _, c := range (plugindb.Goose{}).Commands() {
		if c.Query != nil {
			return c.Query
		}
	}
	t.Fatal("the goose plugin declares no query")
	return nil
}

// gooseCore is the sqlite fixture with a goose tracking table in it, holding
// one applied migration and one that is not.
func gooseCore(t *testing.T, root string) *Core {
	t.Helper()
	c := attachedCore(t, root)
	for _, stmt := range []string{
		"CREATE TABLE goose_db_version (id INTEGER PRIMARY KEY AUTOINCREMENT, " +
			"version_id INTEGER NOT NULL, is_applied INTEGER NOT NULL, tstamp TEXT)",
		"INSERT INTO goose_db_version (version_id, is_applied, tstamp) VALUES (0, 1, '2024-01-01')",
		"INSERT INTO goose_db_version (version_id, is_applied, tstamp) VALUES (20240101120000, 1, '2024-01-02')",
		"INSERT INTO goose_db_version (version_id, is_applied, tstamp) VALUES (20240202120000, 0, '2024-01-03')",
	} {
		if res := c.Submit(":query -w " + stmt); res.Err {
			t.Fatalf("fixture setup %q: %s", stmt, res.Out)
		}
	}
	return c
}

// TestMigrationsDoesNotWriteTheDSNIntoTheGeneratedSource is invariants 24 and
// 25 on the new path, asserted against the file on disk rather than against a
// return value.
//
// This is the failure the whole design of :migrations is arranged around. A
// connection string interpolated into the source lands in <tmp>/main.go, in
// :src, in what :save writes, and in the build cache keyed to that text —
// which is why the command takes no connection string and reuses :query's
// route rather than opening a second one.
func TestMigrationsDoesNotWriteTheDSNIntoTheGeneratedSource(t *testing.T) {
	root := sqliteFixture(t)
	c := gooseCore(t, root)

	if res := c.runDBQuery(":migrations", gooseStatus(t), ""); res.Err {
		t.Fatalf(":migrations failed: %s", res.Out)
	}
	dbPath := filepath.Join(root, "data", "app.db")

	src, err := os.ReadFile(filepath.Join(c.ev.Dir(), "main.go"))
	if err != nil {
		t.Fatalf("reading the generated program: %v", err)
	}
	if strings.Contains(string(src), dbPath) {
		t.Errorf("the connection string is in the generated source:\n%s", src)
	}
	if !strings.Contains(string(src), "os.Getenv") ||
		!strings.Contains(string(src), "GLUON_DB_DSN") {
		t.Errorf("the program does not name the environment variable:\n%s", src)
	}

	// :src is the surface a user actually reads, and the one :save writes.
	session, err := c.source()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(session, dbPath) {
		t.Errorf("the connection string reached :src:\n%s", session)
	}
}

// TestMigrationsIsLiveAcrossTwoIdenticalCalls.
//
// The result cache is keyed on program text alone, and :migrations renders the
// same program every time by construction — the statement is declared, so there
// is not even a typed argument to vary it. Served from the cache, the second
// call would report the migration state from the first, which is a status
// report that stops being a status report.
func TestMigrationsIsLiveAcrossTwoIdenticalCalls(t *testing.T) {
	c := gooseCore(t, sqliteFixture(t))
	q := gooseStatus(t)

	before := c.runDBQuery(":migrations", q, "")
	if before.Err {
		t.Fatalf(":migrations failed: %s", before.Out)
	}
	if !strings.Contains(before.Out, "20240101120000") {
		t.Fatalf("the first call did not report the applied migrations:\n%s", before.Out)
	}
	if strings.Contains(before.Out, "20240303120000") {
		t.Fatalf("the fixture already had the migration this test adds:\n%s", before.Out)
	}

	if res := c.Submit(":query -w INSERT INTO goose_db_version " +
		"(version_id, is_applied, tstamp) VALUES (20240303120000, 1, '2024-03-03')"); res.Err {
		t.Fatalf("applying a migration in the fixture: %s", res.Out)
	}

	after := c.runDBQuery(":migrations", q, "")
	if after.Err {
		t.Fatalf(":migrations failed after the change: %s", after.Out)
	}
	if !strings.Contains(after.Out, "20240303120000") {
		t.Errorf("the second call answered from the cache — the new migration is missing:\n%s", after.Out)
	}
	if before.Out == after.Out {
		t.Error("two calls against a changed database returned the same answer")
	}
}

// TestMigrationsLeavesPendingMigrationsUnapplied.
//
// The command reads a table. It does not call goose's own status API, which can
// create the tracking table or take a lock, and it does not apply anything —
// running a migration from a REPL is a write against a production-adjacent
// database triggered by a line in a history file.
func TestMigrationsLeavesPendingMigrationsUnapplied(t *testing.T) {
	c := gooseCore(t, sqliteFixture(t))

	const census = ":query SELECT version_id, is_applied FROM goose_db_version ORDER BY id"
	before := c.Submit(census)
	if before.Err {
		t.Fatalf("reading the fixture: %s", before.Out)
	}

	res := c.runDBQuery(":migrations", gooseStatus(t), "")
	if res.Err {
		t.Fatalf(":migrations failed: %s", res.Out)
	}
	// The pending one is reported, which is the point of reading the table.
	if !strings.Contains(res.Out, "20240202120000") {
		t.Errorf("the pending migration was not reported:\n%s", res.Out)
	}

	after := c.Submit(census)
	if after.Err {
		t.Fatalf("reading the fixture after: %s", after.Out)
	}
	if before.Out != after.Out {
		t.Errorf("the tracking table changed:\nbefore\n%s\nafter\n%s", before.Out, after.Out)
	}
}

// TestMigrationsLeavesTheSessionUnchanged is invariant 14 for a command that
// evaluates through EvalLive rather than EvalTransient — the same promise, over
// a different path. The driver arrives as a blank import no line names, and
// left in the session's import set it would fail the next ordinary line with
// "imported and not used".
func TestMigrationsLeavesTheSessionUnchanged(t *testing.T) {
	c := gooseCore(t, sqliteFixture(t))

	if res := c.Submit(`x := []int{1, 2, 3}`); res.Err {
		t.Fatalf("setup failed: %s", res.Out)
	}
	entriesBefore := len(c.sess.Entries)
	importsBefore := len(c.ev.Imports())

	if res := c.runDBQuery(":migrations", gooseStatus(t), ""); res.Err {
		t.Fatalf(":migrations failed: %s", res.Out)
	}

	if got := len(c.sess.Entries); got != entriesBefore {
		t.Errorf("the session gained %d entries", got-entriesBefore)
	}
	if got := len(c.ev.Imports()); got != importsBefore {
		t.Errorf("the session gained %d imports", got-importsBefore)
	}
	src, err := c.source()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(src, "modernc.org/sqlite") {
		t.Errorf("the driver import leaked into the session:\n%s", src)
	}
	if res := c.Submit(`x`); res.Err || !strings.Contains(res.Out, "[1 2 3]") {
		t.Errorf("session broken after :migrations: %q", res.Out)
	}
}

// TestMigrationsReportsTheTableWhenTheSchemaHasMoved.
//
// A tracking schema that does not match is the risk this command carries, and
// the mitigation is that it produces a failure rather than a wrong answer. What
// makes the failure one step to diagnose is that it names the table gluon read
// and shows the statement — neither of which the driver's own message carries.
func TestMigrationsReportsTheTableWhenTheSchemaHasMoved(t *testing.T) {
	c := attachedCore(t, sqliteFixture(t))
	// A goose_db_version from another tool, or another era: the table is there
	// and the columns are not.
	if res := c.Submit(":query -w CREATE TABLE goose_db_version (id INTEGER PRIMARY KEY)"); res.Err {
		t.Fatalf("fixture setup: %s", res.Out)
	}

	res := c.runDBQuery(":migrations", gooseStatus(t), "")
	if !res.Err {
		t.Fatalf(":migrations succeeded against a mismatched schema:\n%s", res.Out)
	}
	for _, want := range []string{"goose_db_version", ":migrations", "version_id"} {
		if !strings.Contains(res.Out, want) {
			t.Errorf("the failure does not report %q:\n%s", want, res.Out)
		}
	}
}

// TestMigrationsAppearsWhenGooseReachesTheBuildList is the dispatch half: the
// command must not exist without a migration library, must exist with one, and
// must be the one the plugin declared.
func TestMigrationsAppearsWhenGooseReachesTheBuildList(t *testing.T) {
	online(t)
	c, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	for _, cmd := range c.Commands() {
		if cmd.Name == ":migrations" {
			t.Fatal(":migrations exists in a session with no migration library")
		}
	}

	// The short name is the plugin's alias, and the plugin that knows the path
	// is by definition the one that is not active yet.
	if res := c.Submit(":get goose"); res.Err {
		t.Fatalf(":get goose failed: %s", res.Out)
	}
	found := false
	for _, cmd := range c.Commands() {
		if cmd.Name == ":migrations" {
			found = true
			if cmd.Group != groupPlugin {
				t.Errorf(":migrations is in group %q", cmd.Group)
			}
			if !strings.Contains(cmd.Detail, "goose") {
				t.Errorf(":migrations does not say it came from goose:\n%s", cmd.Detail)
			}
		}
	}
	if !found {
		t.Fatal(":migrations did not appear after its module reached the build list")
	}

	// With no database configured it refuses, and says so — not "no such
	// command", which is what an inactive plugin would produce.
	res := c.Submit(":migrations")
	if !res.Err || !strings.Contains(res.Out, "no database is configured") {
		t.Errorf(":migrations with nothing configured = %q (err=%v)", res.Out, res.Err)
	}
}
