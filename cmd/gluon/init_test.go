package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/config"
	"github.com/sandboxws/gluon/internal/db"
)

func initFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	// HOME must be outside the fixture: the go command writes its own env file
	// under $HOME/.config, and a test that counted that as gluon's doing would
	// fail for a reason that has nothing to do with gluon.
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"go.mod": "module example.com/acme/api\n\ngo 1.25.0\n\nrequire github.com/lib/pq v1.10.9\n",
		".env":   "DATABASE_URL=postgres://app:hunter2@127.0.0.1:5432/acme_dev\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestInitWritesAReferenceNeverAValue is the whole point of the feature.
//
// The detected DSN carries a password. What gets written is the name of the
// variable it lives in — so the config is safe to read, to share and to commit,
// and gluon never becomes a place secrets accumulate.
func TestInitWritesAReferenceNeverAValue(t *testing.T) {
	root := initFixture(t)
	det := db.Detect(root, []string{"github.com/lib/pq v1.10.9"})
	if det.Chosen == nil {
		t.Fatalf("fixture not detected: %+v", det)
	}
	entry := databaseFor("example.com/acme/api", *det.Chosen)
	block := config.Stanza(entry)

	if strings.Contains(block, "hunter2") {
		t.Fatalf("the stanza carries the password:\n%s", block)
	}
	if !strings.Contains(block, `dsn_env = "DATABASE_URL"`) {
		t.Errorf("the stanza does not name where the secret lives:\n%s", block)
	}
}

// TestNoPromptNeverWrites is the whole reconciliation with the permanently
// rejected _gluon/ entry.
//
// The objection recorded there is writing into a product repo *unasked*. A
// -no-prompt that wrote would reintroduce it exactly, and it would do so on the
// one code path nobody watches, because -no-prompt is what a script uses.
func TestNoPromptNeverWrites(t *testing.T) {
	root := initFixture(t)
	before := ls(t, root)

	if code := runInit(root, initOpts{noPrompt: true}); code != 0 {
		t.Fatalf("gluon init -no-prompt = %d, want 0", code)
	}
	if got := ls(t, root); got != before {
		t.Errorf("-no-prompt wrote something.\nbefore: %s\nafter:  %s", before, got)
	}
	if _, err := os.Stat(filepath.Join(root, config.ProjectFile)); err == nil {
		t.Error("-no-prompt created a gluon.toml")
	}
}

// TestWriteNeedsANamedDestination.
//
// -write alone is ambiguous between a private config and a file in somebody's
// repository, and those are very different acts. Naming it is the asking.
func TestWriteNeedsANamedDestination(t *testing.T) {
	root := initFixture(t)
	if code := runInit(root, initOpts{noPrompt: true, write: true}); code == 0 {
		t.Error("-write with no destination was accepted")
	}
	if _, err := os.Stat(filepath.Join(root, config.ProjectFile)); err == nil {
		t.Error("a file was written despite the refusal")
	}
}

// TestWriteLocalProducesACommittableFileWithNoSecret.
func TestWriteLocalProducesACommittableFile(t *testing.T) {
	root := initFixture(t)
	if code := runInit(root, initOpts{noPrompt: true, write: true, local: true}); code != 0 {
		t.Fatalf("gluon init -write -local = %d", code)
	}
	p := filepath.Join(root, config.ProjectFile)
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "hunter2") {
		t.Fatalf("a password was written into a committable file:\n%s", body)
	}
	// A project file applies to one project already, so scoping it by module
	// would be saying the same thing twice.
	if strings.Contains(string(body), "module =") {
		t.Errorf("a project-local file was scoped by module:\n%s", body)
	}
	proj, err := config.LoadProject(p)
	if err != nil {
		t.Fatalf("the written file does not load: %v", err)
	}
	if len(proj.Databases) != 1 || proj.Databases[0].DSNEnv != "DATABASE_URL" {
		t.Errorf("Databases = %+v", proj.Databases)
	}
}

// TestALocalFileNamesItsDatabaseRelatively: a gluon.toml travels with the
// project, so a SQLite file inside the project is written the way the loader
// reads a file = line — relative to the gluon.toml — and still opens after the
// project moves. The global config keeps the path this machine has.
func TestALocalFileNamesItsDatabaseRelatively(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(root, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"go.mod":       "module example.com/shop\n\ngo 1.25.0\n\nrequire modernc.org/sqlite v1.49.1\n",
		"data/shop.db": "SQLite format 3\x00 and then some",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	det := db.Detect(root, []string{"modernc.org/sqlite v1.49.1"})
	if det.Chosen == nil {
		t.Fatalf("fixture not detected: %+v", det)
	}
	entry := databaseFor("example.com/shop", *det.Chosen)

	local := forTarget(entry, filepath.Join(root, config.ProjectFile))
	if local.File != "data/shop.db" {
		t.Errorf("gluon.toml names the file %q, want data/shop.db", local.File)
	}
	if global := forTarget(entry, config.File()); !filepath.IsAbs(global.File) || global.Module == "" {
		t.Errorf("config.toml's entry = %+v, want an absolute file and a module", global)
	}
}

// TestInitJSONCannotCarryASecret.
//
// The envelope is what a script pipes to jq. dsnRef names a location and
// redacted is masked; there is deliberately no field that could hold a value,
// and this is what would notice if one were added.
func TestInitJSONCannotCarryASecret(t *testing.T) {
	root := initFixture(t)
	det := db.Detect(root, []string{"github.com/lib/pq v1.10.9"})
	rep := buildInitJSON("example.com/acme/api", root, det)

	blob, err := marshalIndent(rep)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(blob, "hunter2") {
		t.Fatalf("the -json envelope carries the password:\n%s", blob)
	}
	if !strings.Contains(blob, "env:DATABASE_URL") {
		t.Errorf("the envelope does not name where the secret lives:\n%s", blob)
	}
	if !strings.Contains(blob, "***") {
		t.Errorf("the envelope does not show a redacted form:\n%s", blob)
	}
}

// TestInitOnAProjectWithNoDatabaseExitsWithoutWriting — nothing to record is a
// fact about the project, not a crash, and it must not leave a file behind.
func TestInitOnAProjectWithNoDatabase(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := ls(t, root)
	code := runInit(root, initOpts{noPrompt: true})
	if code == 0 {
		t.Error("init reported success with nothing to record")
	}
	if got := ls(t, root); got != before {
		t.Errorf("something was written: %s", got)
	}
}

func ls(t *testing.T, dir string) string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return strings.Join(names, ",")
}
