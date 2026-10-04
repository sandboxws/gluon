package db

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/config"
)

// TestResolveReadsTheSecretAtConnectTimeNotFromTheConfig.
//
// This is the mechanism that makes "gluon never stores a password" structural
// rather than a promise: the config holds a variable name, and the value is
// fetched here. Break it and the wizard would have to start writing secrets.
func TestResolveReadsTheSecretAtConnectTime(t *testing.T) {
	t.Setenv("GLUON_TEST_DSN_VAR", "postgres://app:hunter2@localhost:5432/acme")
	got, err := Resolve(config.Database{Driver: "postgres", DSNEnv: "GLUON_TEST_DSN_VAR"})
	if err != nil {
		t.Fatal(err)
	}
	if !got.HasPassword() {
		t.Fatal("the password was not picked up")
	}
	if strings.Contains(got.Redacted(), "hunter2") {
		t.Errorf("the resolved DSN prints its password: %s", got.Redacted())
	}
	if !strings.Contains(got.ConnectString(), "hunter2") {
		t.Error("ConnectString lost the password")
	}
}

// TestResolveFallsBackToTheProjectsOwnEnvFile.
//
// dsn_env = "DATABASE_URL" has to work in a shell that never sourced anything,
// because that is the normal state of a terminal somebody opens a REPL in.
func TestResolveFallsBackToTheProjectsOwnEnvFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".env", "DATABASE_URL=postgres://app@localhost:5432/acme\n")
	got, err := Resolve(config.Database{Driver: "postgres", DSNEnv: "DATABASE_URL", Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if got.Database != "acme" {
		t.Errorf("Database = %q, want acme", got.Database)
	}
}

// TestResolvePrefersTheLiveEnvironmentOverTheFile.
//
// An exported variable is what the shell says right now; a .env is what the
// repository last committed. When they disagree the live one is the one the
// user meant, and silently preferring the file would connect somewhere they did
// not ask for.
func TestResolvePrefersTheLiveEnvironmentOverTheFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".env", "DATABASE_URL=postgres://app@localhost:5432/from_file\n")
	t.Setenv("DATABASE_URL", "postgres://app@localhost:5432/from_shell")

	got, err := Resolve(config.Database{Driver: "postgres", DSNEnv: "DATABASE_URL", Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if got.Database != "from_shell" {
		t.Errorf("Database = %q, want the live environment to win", got.Database)
	}
}

// TestResolveSaysWhatItLookedForWhenNothingIsSet.
//
// "could not connect" is a shrug. Naming the variable and the directory is what
// turns it into something somebody can act on.
func TestResolveSaysWhatItLookedFor(t *testing.T) {
	dir := t.TempDir()
	_, err := Resolve(config.Database{Driver: "postgres", DSNEnv: "NOT_SET_ANYWHERE_XYZ", Dir: dir})
	if err == nil {
		t.Fatal("an unset variable resolved")
	}
	if !strings.Contains(err.Error(), "NOT_SET_ANYWHERE_XYZ") {
		t.Errorf("the error does not name the variable: %v", err)
	}
	if !strings.Contains(err.Error(), ".env") {
		t.Errorf("the error does not say where it looked: %v", err)
	}
}

// TestResolveReadsAKeyOutOfEveryStructuredFormat.
//
// dsn_key is dotted because that is how the file is written and how somebody
// would describe where the value is.
func TestResolveReadsAKeyOutOfEveryStructuredFormat(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct{ name, body, key string }{
		{"config.yaml", "database:\n  url: postgres://app@localhost:5432/acme\n", "database.url"},
		{"config.toml", "[database]\nurl = \"postgres://app@localhost:5432/acme\"\n", "database.url"},
		{"config.json", `{"database":{"url":"postgres://app@localhost:5432/acme"}}`, "database.url"},
		{".env.local", "DATABASE_URL=postgres://app@localhost:5432/acme\n", "DATABASE_URL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := writeFile(t, dir, tc.name, tc.body)
			got, err := Resolve(config.Database{
				Driver: "postgres", DSNFile: p, DSNKey: tc.key, Dir: dir,
			})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got.Database != "acme" {
				t.Errorf("Database = %q, want acme", got.Database)
			}
		})
	}
}

// TestResolveReportsAMissingKeyRatherThanAnEmptyDSN — an empty connection
// string fails much later, with an error about the database rather than about
// the config that named nothing.
func TestResolveReportsAMissingKey(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "config.yaml", "database:\n  url: postgres://localhost/x\n")
	_, err := Resolve(config.Database{Driver: "postgres", DSNFile: p, DSNKey: "database.nope", Dir: dir})
	if err == nil {
		t.Fatal("a missing key resolved")
	}
	if !strings.Contains(err.Error(), "database.nope") {
		t.Errorf("the error does not name the key: %v", err)
	}
}

// TestResolveAssemblesFromFieldsWithAReferencedPassword — the config form that
// names host/port/user/database and where the password lives.
func TestResolveAssemblesFromFields(t *testing.T) {
	t.Setenv("GLUON_TEST_PGPASSWORD", "s3cret-value")
	got, err := Resolve(config.Database{
		Driver: "postgres", Host: "db.internal", Port: 5433,
		User: "app", DBName: "acme", PassEnv: "GLUON_TEST_PGPASSWORD",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "db.internal" || got.Port != 5433 || got.Database != "acme" {
		t.Errorf("got %+v", got)
	}
	if strings.Contains(got.Redacted(), "s3cret-value") {
		t.Errorf("the assembled DSN prints its password: %s", got.Redacted())
	}
	if !strings.Contains(got.ConnectString(), "s3cret-value") {
		t.Error("the assembled connection string lost the password")
	}
}

// TestResolveAcceptsNoPasswordAtAll — trust auth, a unix socket and sqlite all
// connect without one, and demanding a password would break every one of them.
func TestResolveAcceptsNoPassword(t *testing.T) {
	got, err := Resolve(config.Database{
		Driver: "postgres", Host: "localhost", User: "app", DBName: "acme",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.HasPassword() {
		t.Error("a password was invented")
	}
}

// TestResolveASQLiteFile — a path is the whole connection string, and the
// config's directory is what a relative one is relative to.
func TestResolveASQLiteFile(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "data/app.db", "")
	got, err := Resolve(config.Database{Driver: "sqlite", File: p, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if got.File != p {
		t.Errorf("File = %q, want %q", got.File, p)
	}
	if got.ConnectString() != p {
		t.Errorf("ConnectString = %q, want the path", got.ConnectString())
	}
	_ = filepath.Base(p)
}
