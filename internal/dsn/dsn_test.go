package dsn

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseAcceptsEveryShape covers the four grammars gluon reads.
//
// Each is a real form somebody's project actually uses, and a missing one shows
// up as "gluon says my project has no database" with no further explanation.
func TestParseAcceptsEveryShape(t *testing.T) {
	for _, tc := range []struct {
		name  string
		in    string
		shape Shape
		want  DSN
	}{
		{
			name: "postgres url", in: "postgres://app:hunter2@db.local:5433/acme_dev?sslmode=disable",
			shape: ShapeURL,
			want:  DSN{Driver: Postgres, Host: "db.local", Port: 5433, User: "app", Database: "acme_dev"},
		},
		{
			name: "postgresql alias with default port", in: "postgresql://app@localhost/acme",
			shape: ShapeURL,
			want:  DSN{Driver: Postgres, Host: "localhost", Port: 5432, User: "app", Database: "acme"},
		},
		{
			name: "mysql url", in: "mysql://root:pw@127.0.0.1:3307/shop",
			shape: ShapeURL,
			want:  DSN{Driver: MySQL, Host: "127.0.0.1", Port: 3307, User: "root", Database: "shop"},
		},
		{
			name: "go mysql dsn", in: "root:secret@tcp(127.0.0.1:3306)/app?parseTime=true",
			shape: ShapeMySQLGo,
			want:  DSN{Driver: MySQL, Host: "127.0.0.1", Port: 3306, User: "root", Database: "app"},
		},
		{
			name: "go mysql dsn without a port", in: "root:secret@tcp(db)/app",
			shape: ShapeMySQLGo,
			want:  DSN{Driver: MySQL, Host: "db", Port: 3306, User: "root", Database: "app"},
		},
		{
			name: "libpq keyword value", in: "host=localhost port=5432 user=app dbname=acme sslmode=disable",
			shape: ShapeKeyValue,
			want:  DSN{Driver: Postgres, Host: "localhost", Port: 5432, User: "app", Database: "acme"},
		},
		{
			name: "sqlserver", in: "sqlserver://sa:pw@localhost:1433?database=master",
			shape: ShapeURL,
			want:  DSN{Driver: SQLServer, Host: "localhost", Port: 1433, User: "sa"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.in, "")
			if err != nil {
				t.Fatalf("Parse(%q) = %v", tc.in, err)
			}
			if got.Shape != tc.shape {
				t.Errorf("shape = %q, want %q", got.Shape, tc.shape)
			}
			if got.Driver != tc.want.Driver || got.Host != tc.want.Host ||
				got.Port != tc.want.Port || got.User != tc.want.User ||
				got.Database != tc.want.Database {
				t.Errorf("got %+v\nwant driver=%s host=%s port=%d user=%s db=%s",
					got, tc.want.Driver, tc.want.Host, tc.want.Port, tc.want.User, tc.want.Database)
			}
		})
	}
}

// TestParseRefusesWhatIsNotADatabase is invariant 23 in its testable form.
//
// Every input here is something that really does appear in a .env file. If any
// of them started parsing, gluon would report a mail server or a log path as
// the project's database — the value-level version of the name-matching failure
// invariant 12 records.
func TestParseRefusesWhatIsNotADatabase(t *testing.T) {
	for _, in := range []string{
		"https://api.example.com",
		"http://localhost:3000",
		"redis://localhost:6379",
		"rediss://cache.example.com:6380",
		"mongodb://localhost:27017/app",
		"amqp://guest:guest@localhost:5672/",
		"smtp://mail.example.com:587",
		"/var/log/app.log",
		"/tmp",
		"/app",
		"user=admin",
		"true",
		"postgres",
		"",
		"   ",
		"a message with spaces",
		"3600",
	} {
		if got, err := Parse(in, ""); err == nil {
			t.Errorf("Parse(%q) accepted it as %s %s — it is not a database", in, got.Driver, got.Shape)
		}
	}
}

// TestRefusalOfAKnownSchemeSaysWhy is why notOurs exists.
//
// "not a DSN" for redis:// is true and useless. The wizard shows this text to
// somebody who has probably pointed gluon at the wrong variable, and naming the
// wrong thing is what saves the round trip.
func TestRefusalOfAKnownSchemeSaysWhy(t *testing.T) {
	_, err := Parse("redis://localhost:6379", "")
	if err == nil {
		t.Fatal("redis:// was accepted")
	}
	if !strings.Contains(err.Error(), "redis is not SQL") {
		t.Errorf("refusal was %q, want it to name redis", err)
	}
}

// TestGoMySQLDSNIsNotAURL pins the trap.
//
// url.Parse does not fail on the Go MySQL DSN — it reads "root" as a scheme and
// hands back nonsense. A parser that reached for url.Parse first would report
// host "" and database "" for a perfectly ordinary connection string, and the
// only symptom would be a database gluon could not connect to.
func TestGoMySQLDSNIsNotAURL(t *testing.T) {
	const in = "root:secret@tcp(127.0.0.1:3306)/app"

	u, err := url.Parse(in)
	if err != nil {
		t.Fatalf("premise changed: url.Parse now rejects %q (%v)", in, err)
	}
	if u.Host != "" {
		t.Fatalf("premise changed: url.Parse now finds host %q", u.Host)
	}

	got, err := Parse(in, "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Host != "127.0.0.1" || got.Port != 3306 || got.Database != "app" {
		t.Errorf("got host=%q port=%d db=%q, want 127.0.0.1/3306/app", got.Host, got.Port, got.Database)
	}
}

// TestBareSlashMySQLFormIsRefused documents a deliberate narrowing.
//
// The Go MySQL driver accepts "/dbname" with everything else defaulted. So
// "/tmp" and "/app" are valid MySQL DSNs, and they are also just paths. gluon
// refuses the form entirely rather than report every path in a .env as a
// database; deleting this test is how that comes back.
func TestBareSlashMySQLFormIsRefused(t *testing.T) {
	for _, in := range []string{"/app", "/tmp", "/"} {
		if _, err := Parse(in, ""); err == nil {
			t.Errorf("Parse(%q) was accepted as a MySQL DSN", in)
		}
	}
}

// TestRedactedNeverCarriesThePassword is the one thing in this package that
// must never regress.
//
// A password reaching Redacted reaches :db, gluon doctor's -json envelope, and
// ~/.local/state/gluon/history. String is Redacted precisely so the careless
// paths — %v, %+v, json.Marshal — are safe by default; this asserts all of them
// at once, for every shape that can carry a secret.
func TestRedactedNeverCarriesThePassword(t *testing.T) {
	const pw = "hunter2SuperSecret"
	for _, in := range []string{
		"postgres://app:" + pw + "@localhost:5432/acme?sslmode=disable",
		"mysql://root:" + pw + "@127.0.0.1:3306/shop",
		"root:" + pw + "@tcp(127.0.0.1:3306)/app?parseTime=true",
		"host=localhost port=5432 user=app dbname=acme password=" + pw,
	} {
		d, err := Parse(in, "")
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
		if !d.HasPassword() {
			t.Fatalf("Parse(%q) did not find the password", in)
		}
		blob, err := json.Marshal(d)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		for name, out := range map[string]string{
			"Redacted()":    d.Redacted(),
			"String()":      d.String(),
			"%v":            fmt.Sprintf("%v", d),
			"%s":            fmt.Sprintf("%s", d),
			"%+v":           fmt.Sprintf("%+v", d),
			"json.Marshal":  string(blob),
			"in a slice":    fmt.Sprintf("%v", []DSN{d}),
			"in a struct":   fmt.Sprintf("%v", struct{ D DSN }{d}),
			"fmt.Errorf %v": fmt.Errorf("could not connect to %v", d).Error(),
		} {
			if strings.Contains(out, pw) {
				t.Errorf("%s leaked the password for %q:\n%s", name, in, out)
			}
		}
		// ...and the one method that is supposed to.
		if !strings.Contains(d.ConnectString(), pw) {
			t.Errorf("ConnectString() lost the password for %q", in)
		}
	}
}

// TestRedactedDistinguishesNoPasswordFromHiddenOne matters when a connection
// fails: "there is no password" and "the password is hidden" are different
// diagnoses, and a form that always printed *** could not tell them apart.
func TestRedactedDistinguishesNoPasswordFromHiddenOne(t *testing.T) {
	with, err := Parse("postgres://app:pw@localhost/acme", "")
	if err != nil {
		t.Fatal(err)
	}
	without, err := Parse("postgres://app@localhost/acme", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(with.Redacted(), "***") {
		t.Errorf("a password was not marked: %s", with.Redacted())
	}
	if strings.Contains(without.Redacted(), "***") {
		t.Errorf("a DSN with no password claims to hide one: %s", without.Redacted())
	}
}

// TestConnectStringPrefersTheOriginalBytes.
//
// A driver takes options gluon does not model. Rebuilding a connection string
// from parsed fields would silently drop them, and the failure would look like
// the database refusing a connection it actually never received.
func TestConnectStringPrefersTheOriginalBytes(t *testing.T) {
	const in = "postgres://app:pw@localhost:5432/acme?sslmode=verify-full&target_session_attrs=read-write"
	d, err := Parse(in, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := d.ConnectString(); got != in {
		t.Errorf("ConnectString() = %q, want the original %q", got, in)
	}
}

// TestTargetIgnoresCredentials is what makes corroboration distinguishable from
// ambiguity.
//
// A compose file names the superuser and the app's .env names the app user, for
// the same database. If Target included credentials those would be two answers
// gluon must refuse between, and the common case would be permanently broken.
func TestTargetIgnoresCredentials(t *testing.T) {
	a, err := Parse("postgres://postgres:root@127.0.0.1:5432/acme_dev", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Parse("postgres://app:apppw@127.0.0.1:5432/acme_dev?sslmode=disable", "")
	if err != nil {
		t.Fatal(err)
	}
	if a.Target() != b.Target() {
		t.Errorf("same database read as two targets:\n  %s\n  %s", a.Target(), b.Target())
	}
}

// TestSQLiteRequiresTheHeaderNotTheExtension is invariant 12 in its purest
// available form: a fact about the file rather than a guess about its name.
//
// Without it, gluon opens a BoltDB store, a zero-byte placeholder or a JSON
// fixture because they end in .db — and reports each as the project's database.
func TestSQLiteRequiresTheHeaderNotTheExtension(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	real := write("app.db", sqliteMagic+"the rest of a database")
	// A SQLite database that is not called one. It must still be found.
	disguised := write("notes.txt", sqliteMagic+"still a database")

	json := write("fixtures.db", `{"users": []}`)
	empty := write("placeholder.sqlite3", "")
	bolt := write("bolt.db", "\x00\x00\x00\x00\x00\x00\x00\x00 not sqlite")
	short := write("tiny.db", "SQLite")

	for _, p := range []string{real, disguised} {
		if !IsSQLiteFile(p) {
			t.Errorf("%s is a SQLite file and was not recognised", filepath.Base(p))
		}
		if _, err := Parse(p, ""); err != nil {
			t.Errorf("Parse(%s) = %v, want a sqlite DSN", filepath.Base(p), err)
		}
	}
	for _, p := range []string{json, empty, bolt, short} {
		if IsSQLiteFile(p) {
			t.Errorf("%s is not a SQLite file and was recognised as one", filepath.Base(p))
		}
		if _, err := Parse(p, ""); err == nil {
			t.Errorf("Parse(%s) accepted a file that is not a database", filepath.Base(p))
		}
	}
	if _, err := Parse(filepath.Join(dir, "does-not-exist.db"), ""); err == nil {
		t.Error("a missing file was accepted as a database")
	}
}

// TestRelativeSQLitePathResolvesAgainstTheFileThatNamedIt.
//
// `file = "./data/app.db"` in a config means next to the config. Resolving it
// against the process's working directory instead would make detection depend
// on where gluon was launched from, which is the kind of intermittent wrongness
// that takes an afternoon to see.
func TestRelativeSQLitePathResolvesAgainstTheFileThatNamedIt(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data", "app.db"), []byte(sqliteMagic+"x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Parse("./data/app.db", dir)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !strings.HasSuffix(got.File, filepath.Join("data", "app.db")) {
		t.Errorf("File = %q, want it under %s", got.File, dir)
	}
	if !filepath.IsAbs(got.File) {
		t.Errorf("File = %q, want an absolute path", got.File)
	}
	// The same value with no directory to resolve against must not find it.
	if _, err := Parse("./data/app.db", ""); err == nil {
		t.Error("a relative path resolved without a base directory")
	}
}

// TestSQLiteURLNeedsNoExistingFile.
//
// sqlite:///var/db/app.db states intent even before the file is created — a
// migration has not run yet. The scheme is the evidence there; the header check
// is what a bare path needs because it has none.
func TestSQLiteURLNeedsNoExistingFile(t *testing.T) {
	for _, in := range []string{
		"sqlite:///var/db/does-not-exist.db",
		"file:app.db?cache=shared",
		"sqlite://data/app.db",
	} {
		got, err := Parse(in, "")
		if err != nil {
			t.Errorf("Parse(%q) = %v, want a sqlite DSN", in, err)
			continue
		}
		if got.Driver != SQLite {
			t.Errorf("Parse(%q).Driver = %q, want sqlite", in, got.Driver)
		}
		if got.File == "" {
			t.Errorf("Parse(%q) found no file", in)
		}
	}
}

// TestKeyValueNeedsMoreThanOnePair.
//
// `user=admin` and `port=8080` are ordinary .env entries. Accepting a single
// pair would turn every assignment-shaped value in a project into a database.
func TestKeyValueNeedsMoreThanOnePair(t *testing.T) {
	for _, in := range []string{"user=admin", "dbname=acme", "host=localhost"} {
		if _, err := Parse(in, ""); err == nil {
			t.Errorf("Parse(%q) accepted a single pair", in)
		}
	}
	// An unknown key disqualifies the whole string, however many pairs there are.
	if _, err := Parse("host=localhost role=admin", ""); err == nil {
		t.Error("an unknown libpq key was accepted")
	}
	// Two known keys with an anchor is the real thing.
	if _, err := Parse("host=localhost dbname=acme", ""); err != nil {
		t.Errorf("a real keyword/value string was refused: %v", err)
	}
}
