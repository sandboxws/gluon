package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadText(t *testing.T, body string) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return LoadFile(p)
}

// TestConfigRefusesAPasswordKey is the rule that earns its keep.
//
// md.Undecoded() would already catch `password` as an unknown setting, with a
// message that teaches nothing to somebody who has just written the obvious
// thing. Declaring the field and refusing it by name is what turns a shrug into
// an instruction — and the alternative is a password sitting in a file the user
// may well commit.
func TestConfigRefusesAPasswordKey(t *testing.T) {
	_, err := loadText(t, `
[[database]]
driver = "postgres"
host = "localhost"
database = "acme"
password = "hunter2"
`)
	if err == nil {
		t.Fatal("a password was accepted into a config file")
	}
	if !strings.Contains(err.Error(), "password_env") {
		t.Errorf("the refusal does not say what to do instead: %v", err)
	}
}

// TestConfigRefusesADSNCarryingAPassword closes the other door.
//
// Refusing the `password` key alone would be theatre: postgres://app:pw@host
// puts the same secret in the same file, one line up.
func TestConfigRefusesADSNCarryingAPassword(t *testing.T) {
	for _, dsn := range []string{
		"postgres://app:hunter2@localhost:5432/acme",
		"host=localhost dbname=acme password=hunter2",
		"root:hunter2@tcp(127.0.0.1:3306)/app",
	} {
		_, err := loadText(t, "[[database]]\ndriver = \"postgres\"\ndsn = \""+dsn+"\"\n")
		if err == nil {
			t.Errorf("a DSN with a password was accepted: %s", dsn)
			continue
		}
		if !strings.Contains(err.Error(), "dsn_env") {
			t.Errorf("the refusal does not name the alternative: %v", err)
		}
	}
}

// TestConfigAcceptsADSNWithoutAPassword — a sqlite path or a trust-auth
// connection carries no secret, and refusing those would be pedantry.
func TestConfigAcceptsADSNWithoutAPassword(t *testing.T) {
	for _, dsn := range []string{
		"postgres://app@localhost:5432/acme?sslmode=disable",
		"host=localhost dbname=acme user=app",
	} {
		if _, err := loadText(t, "[[database]]\ndriver = \"postgres\"\ndsn = \""+dsn+"\"\n"); err != nil {
			t.Errorf("a DSN with no password was refused: %s — %v", dsn, err)
		}
	}
}

// TestExactlyOneConnectionSource.
//
// Two sources is ambiguity gluon would have to resolve silently, and zero is an
// entry that looks configured and is not. Both are the kind of thing that only
// shows up as a connection failure much later.
func TestExactlyOneConnectionSource(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"none", "[[database]]\ndriver = \"postgres\"\n", "names no connection"},
		{"two", "[[database]]\ndriver = \"postgres\"\ndsn_env = \"A\"\nfile = \"x.db\"\n", "2 ways"},
		{"dsn_file without key", "[[database]]\ndriver = \"postgres\"\ndsn_file = \".env\"\n", "go together"},
		{"dsn_key without file", "[[database]]\ndriver = \"postgres\"\ndsn_key = \"X\"\n", "go together"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadText(t, tc.body)
			if err == nil {
				t.Fatalf("accepted: %s", tc.body)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

// TestUnknownDatabaseKeyIsAnError extends config's existing stance to the new
// table. A typo in a config file is invisible unless something says so.
func TestUnknownDatabaseKeyIsAnError(t *testing.T) {
	_, err := loadText(t, "[[database]]\ndriver = \"postgres\"\ndsn_env = \"X\"\ndsn_evn = \"X\"\n")
	if err == nil {
		t.Fatal("a misspelled key was accepted")
	}
	if !strings.Contains(err.Error(), "dsn_evn") {
		t.Errorf("the error does not name the typo: %v", err)
	}
}

// TestTwoDatabasesMayNotShareAName — invariant 13's rule applied to config: a
// name that means two things is one gluon would have to guess between.
func TestTwoDatabasesMayNotShareAName(t *testing.T) {
	_, err := loadText(t, `
[[database]]
name = "primary"
driver = "postgres"
dsn_env = "A"

[[database]]
name = "primary"
driver = "postgres"
dsn_env = "B"
`)
	if err == nil {
		t.Fatal("two databases share a name")
	}
}

// TestReadOnlyDefaultsToTrueAndFalseIsRespected is why ReadOnly is a pointer.
//
// Unset and false are different answers. A plain bool would make "the user did
// not say" indistinguishable from "the user said no", and the safe default
// would silently become the unsafe one.
func TestReadOnlyDefaultsToTrueAndFalseIsRespected(t *testing.T) {
	c, err := loadText(t, `
[[database]]
name = "a"
driver = "postgres"
dsn_env = "A"

[[database]]
name = "b"
driver = "postgres"
dsn_env = "B"
readonly = false
`)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Databases[0].IsReadOnly() {
		t.Error("an unset readonly did not default to true")
	}
	if c.Databases[1].IsReadOnly() {
		t.Error("readonly = false was not respected")
	}
}

// TestRelativePathsResolveAgainstTheConfig.
//
// file = "./data/app.db" means next to the config. Against the working
// directory instead, a session's database would depend on where gluon was
// launched from — wrong intermittently, which is the worst way to be wrong.
func TestRelativePathsResolveAgainstTheConfig(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte("[[database]]\ndriver = \"sqlite\"\nfile = \"./data/app.db\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "data", "app.db")
	if got := c.Databases[0].File; got != want {
		t.Errorf("File = %q, want %q", got, want)
	}
}

// TestDatabasesForPrefersTheModuleScopedEntry.
//
// A global default and a rule for one project must not be merged: a half-merged
// database configuration is untraceable, and the symptom is a connection to
// something nobody configured.
func TestDatabasesForPrefersTheModuleScopedEntry(t *testing.T) {
	c, err := loadText(t, `
[[database]]
name = "fallback"
driver = "postgres"
dsn_env = "GLOBAL"

[[database]]
module = "github.com/acme/api"
name = "scoped"
driver = "postgres"
dsn_env = "SCOPED"
`)
	if err != nil {
		t.Fatal(err)
	}
	got := c.DatabasesFor("github.com/acme/api")
	if len(got) != 1 || got[0].Name != "scoped" {
		t.Errorf("DatabasesFor(api) = %v, want only the scoped entry", got)
	}
	got = c.DatabasesFor("github.com/other/thing")
	if len(got) != 1 || got[0].Name != "fallback" {
		t.Errorf("DatabasesFor(other) = %v, want the global entry", got)
	}
}

// TestNoDatabaseSectionIsStillAValidConfig. gluon has to work with no config at
// all, because that is how it is first run.
func TestNoDatabaseSectionIsStillAValidConfig(t *testing.T) {
	c, err := loadText(t, "imports = [\"strings\"]\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Databases) != 0 {
		t.Errorf("Databases = %v, want none", c.Databases)
	}
	if len(c.DatabasesFor("anything")) != 0 {
		t.Error("DatabasesFor invented an entry")
	}
}
