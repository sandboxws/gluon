package db

import (
	"go/parser"
	"strconv"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/dsn"
)

func testDriver() Driver {
	return Driver{Module: "github.com/lib/pq", Import: "github.com/lib/pq", Name: "postgres", Family: dsn.Postgres}
}

// TestRewriteNeverEmbedsTheDSN is the load-bearing test of the whole feature.
//
// A connection string interpolated into the generated source lands in
// <tmpdir>/main.go, in what :src prints, in the scratch module :save writes,
// and in the build cache keyed to that text. Four places, none of which anybody
// would think to look at. The program reads an environment variable instead, so
// there is no secret in it to leak — and this is what says so.
func TestRewriteNeverEmbedsTheDSN(t *testing.T) {
	const secret = "postgres://app:hunter2@localhost:5432/acme"
	got, err := Rewrite(Statement{SQL: "SELECT 1"}, testDriver())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, secret) || strings.Contains(got, "hunter2") {
		t.Fatalf("the generated source contains the connection string:\n%s", got)
	}
	if !strings.Contains(got, `os.Getenv("`+EnvDSN+`")`) {
		t.Errorf("the generated source does not read %s:\n%s", EnvDSN, got)
	}
}

// TestRewriteParses — the rewrite produces Go, and a rewrite that produces
// almost-Go fails with a parse error pointing at a file the user never wrote.
// Same test the gorm plugin's rewrite carries, for the same reason.
func TestRewriteParses(t *testing.T) {
	for _, sql := range []string{
		"SELECT 1",
		"SELECT id, email FROM users WHERE name = 'O''Brien' LIMIT 5",
		"WITH t AS (SELECT 1) SELECT * FROM t",
		"SELECT `order`.id FROM `order`",
		"SELECT '\"quoted\"', 'tab\there'",
		`SELECT "col" FROM "table"`,
	} {
		src, err := Rewrite(Statement{SQL: sql}, testDriver())
		if err != nil {
			t.Errorf("Rewrite(%q) = %v", sql, err)
			continue
		}
		if _, err := parser.ParseExpr(src); err != nil {
			t.Errorf("Rewrite(%q) did not parse: %v\n%s", sql, err, src)
		}
	}
}

// TestRewriteQuotesSQLContainingBackticks pins a five-minute bug.
//
// MySQL quotes identifiers with backticks. A raw string literal in the
// generated Go would end early on the first one, producing a Go syntax error
// about a statement the user wrote in a different language entirely.
func TestRewriteQuotesSQLContainingBackticks(t *testing.T) {
	const sql = "SELECT `order`.`id` FROM `order`"
	src, err := Rewrite(Statement{SQL: sql}, testDriver())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.ParseExpr(src); err != nil {
		t.Fatalf("backticked SQL broke the generated source: %v\n%s", err, src)
	}
	// The property is that the SQL sits in a double-quoted literal, where a
	// backtick is an ordinary character. Backticks appearing in the source is
	// fine and expected; the SQL appearing between backticks is what would
	// break, so assert the quoted form is what was emitted.
	if !strings.Contains(src, strconv.Quote(sql)) {
		t.Errorf("the SQL was not emitted as a quoted literal:\n%s", src)
	}
	if strings.Contains(src, "`"+sql+"`") {
		t.Error("the SQL was embedded in a raw string literal")
	}
}

// TestWriteStatementsNeedTheFlag.
//
// :query points at a real database. The first-word check is what catches the
// paste nobody meant — and it is honest about being a keyword check rather than
// a parse, because claiming to prove a statement safe would be worse than
// admitting it does not.
func TestWriteStatementsNeedTheFlag(t *testing.T) {
	for _, sql := range []string{
		"DROP TABLE users",
		"DELETE FROM users",
		"UPDATE users SET admin = true",
		"INSERT INTO users VALUES (1)",
		"TRUNCATE users",
		"ALTER TABLE users ADD COLUMN x int",
		"GRANT ALL ON users TO PUBLIC",
	} {
		if err := CheckStatement(sql, false); err == nil {
			t.Errorf("%q ran without -w", sql)
		}
		if err := CheckStatement(sql, true); err != nil {
			t.Errorf("%q was refused even with -w: %v", sql, err)
		}
	}
}

// TestReadOnlyStatementsRunWithoutTheFlag — including the ones people forget
// are reads. WITH and EXPLAIN being refused would make the check feel broken.
func TestReadOnlyStatementsRunWithoutTheFlag(t *testing.T) {
	for _, sql := range []string{
		"SELECT 1",
		"select lower(email) from users",
		"  WITH t AS (SELECT 1) SELECT * FROM t",
		"EXPLAIN SELECT * FROM users",
		"SHOW TABLES",
		"PRAGMA table_info(users)",
		"DESCRIBE users",
		"(SELECT 1) UNION (SELECT 2)",
	} {
		if err := CheckStatement(sql, false); err != nil {
			t.Errorf("%q was refused: %v", sql, err)
		}
	}
}

// TestEmptyStatementReportsUsage — every command whose Arg contains < must say
// usage when called bare; command_test.go asserts that for the registry, and
// this is the half that lives here.
func TestEmptyStatementReportsUsage(t *testing.T) {
	err := CheckStatement("   ", false)
	if err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Errorf("CheckStatement(\"\") = %v, want a usage line", err)
	}
}

// TestAWriteIsNotWrappedInATransactionThatRollsBack.
//
// This is a bug that shipped in the first version and was caught by an
// integration test: both paths opened a transaction and deferred a Rollback,
// with only the ReadOnly flag tracking -w. So every write was silently
// discarded — CREATE TABLE reported success and the table did not exist.
//
// A read is wrapped, because a read-only transaction is a guarantee the server
// enforces rather than one gluon merely claims. A write is not wrapped at all:
// the wrapper exists only for the guarantee -w has explicitly opted out of, and
// a transaction the user did not ask for is behaviour that would then need
// explaining.
func TestAWriteIsNotWrappedInATransactionThatRollsBack(t *testing.T) {
	ro, err := Rewrite(Statement{SQL: "SELECT 1"}, testDriver())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ro, "ReadOnly: true") {
		t.Error("a read statement did not open a read-only transaction")
	}
	if !strings.Contains(ro, "Rollback()") {
		t.Error("a read statement's transaction is never rolled back")
	}

	rw, err := Rewrite(Statement{SQL: "DELETE FROM users", Write: true}, testDriver())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rw, "Rollback()") {
		t.Fatalf("a -w statement is rolled back, so the write would be discarded:\n%s", rw)
	}
	if strings.Contains(rw, "BeginTx") {
		t.Errorf("a -w statement was wrapped in a transaction it did not ask for:\n%s", rw)
	}
	if !strings.Contains(rw, "__db.QueryContext") {
		t.Errorf("a -w statement does not reach the database directly:\n%s", rw)
	}
}

// TestParseResultReadsBackWhatTheChildWrote, including the two cases the wire
// format exists for.
func TestParseResultReadsBackWhatTheChildWrote(t *testing.T) {
	out := strings.Join([]string{
		tagCols + sep + "id" + sep + "email",
		tagRow + sep + "1" + sep + "ada@example.com",
		tagRow + sep + "2" + sep + nullCell,
		tagRow + sep + "3" + sep + "NULL",
		tagMore + sep,
		tagNote + sep + "something worth saying",
	}, "\n")

	got := ParseResult(out)
	if len(got.Cols) != 2 || got.Cols[0] != "id" {
		t.Fatalf("Cols = %v", got.Cols)
	}
	if len(got.Rows) != 3 {
		t.Fatalf("Rows = %v", got.Rows)
	}
	if !got.More {
		t.Error("More was not reported")
	}
	if got.Note != "something worth saying" {
		t.Errorf("Note = %q", got.Note)
	}
	// The case the sentinel exists for.
	if !got.Null[1][1] {
		t.Error("a SQL NULL was not marked as one")
	}
	if got.Null[2][1] {
		t.Error("the literal text \"NULL\" was reported as SQL NULL")
	}
	if got.Rows[2][1] != "NULL" {
		t.Errorf("the text NULL was altered: %q", got.Rows[2][1])
	}
}

// TestParseResultCarriesAnError — a failed query must not read as an empty
// result set. "no rows" and "could not connect" are different answers.
func TestParseResultCarriesAnError(t *testing.T) {
	got := ParseResult(tagErr + sep + "connection refused")
	if got.Err != "connection refused" {
		t.Errorf("Err = %q", got.Err)
	}
	if len(got.Rows) != 0 {
		t.Errorf("an error produced rows: %v", got.Rows)
	}
}

// TestImportsCoverEveryQualifierTheProgramUses.
//
// The imports are supplied rather than resolved by goimports, which costs
// ~135ms. That trade only works if the list is exact: a missing one fails the
// build, and a surplus one fails it too, with "imported and not used".
func TestImportsCoverEveryQualifierTheProgramUses(t *testing.T) {
	drv := testDriver()
	src, err := Rewrite(Statement{SQL: "SELECT 1"}, drv)
	if err != nil {
		t.Fatal(err)
	}
	imps := Imports(drv)

	have := map[string]bool{}
	for _, im := range imps {
		if im.Name == "_" {
			continue
		}
		p := im.Path
		if i := strings.LastIndex(p, "/"); i >= 0 {
			p = p[i+1:]
		}
		have[p] = true
	}
	for _, q := range []string{"sql", "context", "fmt", "os", "strings", "time"} {
		if !have[q] {
			t.Errorf("Imports does not supply %q", q)
		}
		if !strings.Contains(src, q+".") {
			t.Errorf("Imports supplies %q but the program never uses it — "+
				"that is an \"imported and not used\" build failure", q)
		}
	}
	var blank int
	for _, im := range imps {
		if im.Name == "_" {
			blank++
			if im.Path != drv.Import {
				t.Errorf("blank import is %q, want the driver %q", im.Path, drv.Import)
			}
		}
	}
	if blank != 1 {
		t.Errorf("got %d blank imports, want exactly the driver", blank)
	}
}

// TestDriversInReadsTheBuildList, including the gorm case: a project using gorm
// has the real driver transitively, which is what makes :query work there with
// no extra dependency.
func TestDriversInReadsTheBuildList(t *testing.T) {
	got := DriversIn([]string{"github.com/lib/pq v1.10.9", "gorm.io/gorm v1.25.0"})
	if len(got) != 1 || got[0].Name != "postgres" {
		t.Fatalf("DriversIn = %v, want lib/pq", got)
	}
	got = DriversIn([]string{"gorm.io/driver/mysql v1.5.0"})
	if len(got) != 1 || got[0].Family != dsn.MySQL {
		t.Errorf("a gorm dialect did not imply its driver: %v", got)
	}
	if len(DriversIn(nil)) != 0 {
		t.Error("DriversIn invented a driver from an empty build list")
	}
}

// TestDriverForIsDeterministicWhenTwoCouldServe.
//
// A project may require both lib/pq and pgx. Declared order is the tie-break —
// the same rule internal/plugins uses — so the outcome is the same on every
// run rather than depending on map iteration.
func TestDriverForIsDeterministicWhenTwoCouldServe(t *testing.T) {
	avail := DriversIn([]string{"github.com/jackc/pgx/v5 v5.7.2", "github.com/lib/pq v1.10.9"})
	if len(avail) != 2 {
		t.Fatalf("expected both drivers, got %v", avail)
	}
	for i := 0; i < 20; i++ {
		got, ok := DriverFor(dsn.Postgres, avail, "")
		if !ok || got.Module != "github.com/lib/pq" {
			t.Fatalf("DriverFor picked %v, want the first in declared order", got)
		}
	}
	// ...and a pin overrides it.
	got, ok := DriverFor(dsn.Postgres, avail, "github.com/jackc/pgx/v5")
	if !ok || got.Name != "pgx" {
		t.Errorf("a pinned driver was ignored: %v", got)
	}
}

// TestPgxBlankImportIsTheStdlibSubpackage.
//
// pgx registers its database/sql driver from github.com/jackc/pgx/v5/stdlib,
// not from the module root. Blank-importing the root compiles and registers
// nothing, so sql.Open fails at run time with `unknown driver "pgx"`.
func TestPgxBlankImportIsTheStdlibSubpackage(t *testing.T) {
	avail := DriversIn([]string{"github.com/jackc/pgx/v5 v5.7.2"})
	if len(avail) != 1 {
		t.Fatalf("got %v", avail)
	}
	if !strings.HasSuffix(avail[0].Import, "/stdlib") {
		t.Errorf("pgx blank import is %q, want the stdlib subpackage", avail[0].Import)
	}
}

// TestGeneratedSourceHasNoRawControlBytes.
//
// The wire separator is \x1f, and it has to reach the generated program as an
// escape sequence. A raw control byte in an interpreted string literal is still
// valid Go and still compiles — which is exactly why this needs asserting: the
// only symptoms are a :src listing with invisible characters in it and a
// dependence on gofmt choosing to leave them alone.
func TestGeneratedSourceHasNoRawControlBytes(t *testing.T) {
	src, err := Rewrite(Statement{SQL: "SELECT 1"}, testDriver())
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range src {
		if r < 0x20 && r != '\n' && r != '\t' {
			t.Fatalf("raw control byte %#x at offset %d in the generated source", r, i)
		}
	}
	if !strings.Contains(src, `"cols\x1f"`) {
		t.Errorf("the separator is not written as an escape sequence:\n%s", src)
	}
}

// TestTimeoutIsRealGo pins the other half of the same class of bug.
//
// A duration spelled the way a config file spells it — "15s" — is not a Go
// expression, and pasting one into generated source produces a parse error
// against a program the user never wrote.
func TestTimeoutIsRealGo(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", "15*time.Second"},
		{"30s", "30*time.Second"},
		{"2m", "120*time.Second"},
		{"1500ms", "time.Duration(1500000000)"},
		{"garbage", "15*time.Second"},
		{"-5s", "15*time.Second"},
	} {
		if got := goDuration(tc.in); got != tc.want {
			t.Errorf("goDuration(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	src, err := Rewrite(Statement{SQL: "SELECT 1", Timeout: "30s"}, testDriver())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.ParseExpr(src); err != nil {
		t.Fatalf("a custom timeout broke the source: %v", err)
	}
}

// TestGeneratedProgramReportsColumnTypes.
//
// The type a column was declared with is the one thing a JSON consumer cannot
// recover from the cells: every cell arrives as text, so "42" is an INTEGER or
// a VARCHAR depending on something only the driver knows. The child is the only
// place that can be asked.
func TestGeneratedProgramReportsColumnTypes(t *testing.T) {
	src, err := Rewrite(Statement{SQL: "SELECT 1"}, testDriver())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.ParseExpr(src); err != nil {
		t.Fatalf("the types line did not parse: %v\n%s", err, src)
	}
	if !strings.Contains(src, "__rows.ColumnTypes()") {
		t.Errorf("the program never asks for column types:\n%s", src)
	}
	if !strings.Contains(src, "DatabaseTypeName()") {
		t.Errorf("the program asks for types but sends none:\n%s", src)
	}
	if !strings.Contains(src, `"types\x1f"`) {
		t.Errorf("the types tag is not written as an escape sequence:\n%s", src)
	}
	// The whole types block is inside `if __cterr == nil`, which is what keeps
	// a driver with no metadata support from failing a query that worked.
	if !strings.Contains(src, "__cterr == nil") {
		t.Errorf("a ColumnTypes failure is not tolerated:\n%s", src)
	}
	if strings.Contains(src, "return __fail(__cterr)") {
		t.Errorf("a ColumnTypes failure aborts the query; the rows are still the answer:\n%s", src)
	}
	// Nothing here needs an import the list does not already carry —
	// TestImportsCoverEveryQualifierTheProgramUses asserts the list is exact,
	// so a new qualifier would fail it. This says which one was added.
	if strings.Contains(src, "reflect.") {
		t.Errorf("the types line reaches outside the supplied imports:\n%s", src)
	}
}

// TestParseResultReadsColumnTypes, both ways round.
//
// The tag is add-only on a wire the old parser already tolerated: its default
// arm ignores what it does not know, so a child that sends types to a gluon
// that does not want them is not an error. This pins both directions.
func TestParseResultReadsColumnTypes(t *testing.T) {
	with := ParseResult(strings.Join([]string{
		tagCols + sep + "id" + sep + "email" + sep + "seen",
		tagTypes + sep + "INTEGER" + sep + "TEXT" + sep + "",
		tagRow + sep + "1" + sep + "ada@example.com" + sep + nullCell,
	}, "\n"))
	if got, want := len(with.Types), 3; got != want {
		t.Fatalf("Types = %v, want %d entries", with.Types, want)
	}
	if with.Types[0] != "INTEGER" || with.Types[1] != "TEXT" {
		t.Errorf("Types = %v", with.Types)
	}
	// A driver that reports no name for a column is reported as saying nothing,
	// not as having no column.
	if with.Types[2] != "" {
		t.Errorf("an unnamed type became %q, want the empty string", with.Types[2])
	}

	without := ParseResult(strings.Join([]string{
		tagCols + sep + "id",
		tagRow + sep + "1",
	}, "\n"))
	if without.Types != nil {
		t.Errorf("Types = %v with no types line, want nil", without.Types)
	}
	if len(without.Rows) != 1 {
		t.Errorf("the rows were lost: %v", without.Rows)
	}
}
