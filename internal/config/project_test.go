package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeProject(t *testing.T, dir, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, ProjectFile)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestProjectFileMayNotSetWhatIsYours is the whole reason Project is a separate
// type with a deliberately tiny schema.
//
// A gluon.toml arrives with `git clone`, written by whoever owns the repository.
// One that could set `editor` would run a command of their choosing on the
// machine of anyone who opens a REPL in it. That is not a decision a downloaded
// file gets to take, and neither is anything else that belongs to the person
// running gluon rather than to the checkout.
func TestProjectFileMayNotSetWhatIsYours(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"editor", "editor = \"/tmp/evil\"\n", "run a command"},
		{"theme", "[theme]\nstring = \"2\"\n", "Colour is yours"},
		{"plugins", "[plugins]\ndisable = [\"gorm\"]\n", "property of your gluon"},
		{"timeout", "timeout = \"600s\"\n", "yours to set"},
		{"hosts", "[hosts.\"x/y\"]\nimports = []\n", "already applies to one project"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := writeProject(t, t.TempDir(), tc.body)
			_, err := LoadProject(p)
			if err == nil {
				t.Fatalf("a project file set %s and was accepted", tc.name)
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.want)) {
				t.Errorf("error %q does not explain why (want %q)", err, tc.want)
			}
		})
	}
}

// TestProjectFileAcceptsWhatItIsFor.
func TestProjectFileAcceptsWhatItIsFor(t *testing.T) {
	p := writeProject(t, t.TempDir(), `
imports = ["strings"]

[[database]]
name = "primary"
driver = "postgres"
dsn_env = "DATABASE_URL"
`)
	got, err := LoadProject(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Databases) != 1 || got.Databases[0].Name != "primary" {
		t.Errorf("Databases = %v", got.Databases)
	}
	if len(got.Imports) != 1 {
		t.Errorf("Imports = %v", got.Imports)
	}
}

// TestProjectFileEnforcesTheSameSecretRules — a project file is the one most
// likely to be committed, so the no-password rule matters most here.
func TestProjectFileEnforcesTheSameSecretRules(t *testing.T) {
	p := writeProject(t, t.TempDir(), "[[database]]\ndriver = \"postgres\"\ndsn = \"postgres://a:pw@h/d\"\n")
	if _, err := LoadProject(p); err == nil {
		t.Fatal("a committable file accepted a password")
	}
}

// TestFindProjectStopsAtTheRepositoryRoot.
//
// A gluon.toml above the repository belongs to some other project. Following it
// would point a session at a database that has nothing to do with the code in
// front of it — invariant 12, in the form that costs you a wrong answer rather
// than a slow one.
func TestFindProjectStopsAtTheRepositoryRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "elsewhere"))

	// A decoy above the repository.
	writeProject(t, root, "[[database]]\ndriver=\"postgres\"\ndsn_env=\"DECOY\"\n")

	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	api := filepath.Join(repo, "apps", "api")
	if err := os.MkdirAll(api, 0o755); err != nil {
		t.Fatal(err)
	}

	if got, ok := FindProject(api); ok {
		t.Errorf("FindProject reached above the repository: %s", got)
	}

	// One inside the repository is found, from a directory below it.
	want := writeProject(t, repo, "[[database]]\ndriver=\"postgres\"\ndsn_env=\"REAL\"\n")
	got, ok := FindProject(api)
	if !ok {
		t.Fatal("FindProject missed a gluon.toml at the repository root")
	}
	if got != want {
		t.Errorf("FindProject = %q, want %q", got, want)
	}
}

// TestFindProjectPrefersTheNearest — a monorepo may configure per service, and
// the one next to the code you are standing in is the one that means you.
func TestFindProjectPrefersTheNearest(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "elsewhere"))
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeProject(t, root, "[[database]]\ndriver=\"postgres\"\ndsn_env=\"ROOT\"\n")
	api := filepath.Join(root, "apps", "api")
	want := writeProject(t, api, "[[database]]\ndriver=\"postgres\"\ndsn_env=\"API\"\n")

	got, ok := FindProject(api)
	if !ok {
		t.Fatal("nothing found")
	}
	if got != want {
		t.Errorf("FindProject = %q, want the nearest %q", got, want)
	}
}

// TestMissingProjectFileIsNotAnError. Most projects will never have one, and
// that has to be an ordinary answer rather than a failure.
func TestMissingProjectFileIsNotAnError(t *testing.T) {
	got, err := LoadProject(filepath.Join(t.TempDir(), ProjectFile))
	if err != nil {
		t.Fatalf("a missing project file was an error: %v", err)
	}
	if got != nil {
		t.Errorf("LoadProject invented a config: %v", got)
	}
}
