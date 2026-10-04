//go:build integration

package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/db"
)

// The `:query -json` envelope, end to end through `gluon -e ... -json`.
//
// The unit tests pin the bytes for a fixture; these say the fixture is the
// shape a real driver actually produces. SQLite is what makes that possible at
// all — no server — and it is the host's driver that runs, which is the whole
// design.

const sqliteFixtureVersion = "v1.49.1"

// sqliteHostFixture builds a host module that requires a pure-Go SQLite driver
// and a database for it to open.
//
// A copy of internal/repl's fixture rather than a shared helper: both are
// behind //go:build integration in packages that cannot import each other, and
// exporting a test fixture from internal/repl to a main package would put a
// build-tagged helper on a package's public surface for one caller.
//
// It skips rather than fails when the driver cannot be resolved offline. gluon
// builds with GOPROXY=off, so a cold module cache is a property of the machine
// and not of the code.
func sqliteHostFixture(t *testing.T) string {
	t.Helper()
	cacheOut, cacheErr := exec.Command("go", "env", "GOMODCACHE").Output()

	// HOME is deliberately left alone: moving it makes the go command build a
	// second module cache inside the temp directory, populated with read-only
	// files that t.TempDir's cleanup then cannot remove. The fixture carries
	// its own .git, which is all the isolation the detector needs.
	root := t.TempDir()

	dbPath := filepath.Join(root, "data", "app.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		"go.mod": "module gluon.test/fixture\n\ngo 1.25.0\n\nrequire modernc.org/sqlite " + sqliteFixtureVersion + "\n",
		"store/driver.go": "package store\n\n// The project's own driver. gluon never links one.\n" +
			"import _ \"modernc.org/sqlite\"\n",
		"gluon.toml": "[[database]]\nname = \"primary\"\ndriver = \"sqlite\"\nfile = \"./data/app.db\"\n",
	}
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	if cacheErr != nil {
		t.Skip("cannot locate the module cache:", cacheErr)
	}
	proxy := "file://" + filepath.Join(strings.TrimSpace(string(cacheOut)), "cache", "download")

	// Pinned to what go.mod already names: a bare `go get` resolves to the
	// latest version, which is by definition the one least likely to be cached.
	get := exec.Command("go", "get", "modernc.org/sqlite@"+sqliteFixtureVersion)
	get.Dir = root
	get.Env = append(os.Environ(),
		"GOFLAGS=-mod=mod", "GOPROXY="+proxy,
		"GOSUMDB=off", "GONOSUMCHECK=1", "GOTOOLCHAIN=local")
	if out, err := get.CombinedOutput(); err != nil {
		t.Skipf("modernc.org/sqlite is not in the module cache, so the query path cannot be built offline: %v\n%s", err, out)
	}
	build := exec.Command("go", "build", "./...")
	build.Dir = root
	build.Env = append(os.Environ(), "GOFLAGS=", "GOPROXY=off", "GOTOOLCHAIN=local")
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("the fixture module does not build offline: %v\n%s", err, out)
	}
	return root
}

// captureOneShotIn is captureOneShot with a host attached, which every :query
// needs — the driver comes from the host's build list and nowhere else.
func captureOneShotIn(t *testing.T, lines []string, hostDir string, asJSON bool) (string, int) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	code := runOneShot(lines, hostDir, asJSON)
	os.Stdout = saved
	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out), code
}

// decoded is the envelope as a consumer reads it: one decode, no re-parsing.
type decodedQuery struct {
	OK      bool   `json:"ok"`
	Command string `json:"command"`
	Target  string `json:"target"`
	Columns []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"columns"`
	Rows      [][]*string `json:"rows"`
	Truncated bool        `json:"truncated"`
	Note      string      `json:"note"`
	Error     string      `json:"error"`
}

// decodeLast reads the final JSON object on stdout. Seeding runs its own
// -e lines first, and each meta command emits one object.
func decodeLast(t *testing.T, out string) decodedQuery {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(out))
	var last decodedQuery
	found := false
	for {
		var got decodedQuery
		if err := dec.Decode(&got); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("the envelope does not decode: %v\n%s", err, out)
		}
		last, found = got, true
	}
	if !found {
		t.Fatalf("no JSON object on stdout:\n%s", out)
	}
	return last
}

// TestQueryJSONReportsNullAsNull.
//
// The one thing metaJSON's rendered table could not say. A cell that was SQL
// NULL and a cell holding the four letters NULL look identical once a table has
// been drawn, and they are different answers — which is most of why this
// envelope exists.
func TestQueryJSONReportsNullAsNull(t *testing.T) {
	root := sqliteHostFixture(t)
	out, code := captureOneShotIn(t, []string{
		":query -w CREATE TABLE t (id INTEGER, label TEXT)",
		":query -w INSERT INTO t (id, label) VALUES (1, 'NULL'), (2, NULL)",
		":query -json SELECT id, label FROM t ORDER BY id",
	}, root, true)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}

	got := decodeLast(t, out)
	if !got.OK {
		t.Fatalf("ok = false: %s\n%s", got.Error, out)
	}
	if got.Command != ":query" {
		t.Errorf("command = %q, want :query", got.Command)
	}
	if len(got.Columns) != 2 || got.Columns[0].Name != "id" || got.Columns[1].Name != "label" {
		t.Fatalf("columns = %+v", got.Columns)
	}
	// The driver declares a type or it does not; either is allowed, and the
	// field being present is what the envelope promises.
	t.Logf("declared types: %q, %q", got.Columns[0].Type, got.Columns[1].Type)

	if len(got.Rows) != 2 {
		t.Fatalf("rows = %d, want 2:\n%s", len(got.Rows), out)
	}
	text := got.Rows[0][1]
	if text == nil || *text != "NULL" {
		t.Errorf("the text cell 'NULL' decoded as %v, want the string \"NULL\"", text)
	}
	if got.Rows[1][1] != nil {
		t.Errorf("the SQL NULL cell decoded as %q, want null", *got.Rows[1][1])
	}
	if got.Truncated {
		t.Error("truncated on a two-row result")
	}
	// The target is reported, and reported redacted. A SQLite file has no
	// password to hide, so this only says the field is populated.
	if got.Target == "" {
		t.Error("the envelope names no target")
	}
}

// TestQueryJSONReportsTheFetchCap.
//
// The cap is a policy, and a consumer that read db.MaxRows as the count would
// be reading a number the statement never produced. The envelope says both:
// exactly the rows fetched, and that there were more.
func TestQueryJSONReportsTheFetchCap(t *testing.T) {
	root := sqliteHostFixture(t)
	// One more row than the cap, generated in the database rather than
	// inserted: the point is the cap, not how the rows got there.
	over := db.MaxRows + 10
	out, code := captureOneShotIn(t, []string{
		":query -json WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < " +
			strconv.Itoa(over) + ") SELECT i FROM n",
	}, root, true)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}

	got := decodeLast(t, out)
	if !got.OK {
		t.Fatalf("ok = false: %s", got.Error)
	}
	if len(got.Rows) != db.MaxRows {
		t.Errorf("rows = %d, want exactly the cap %d", len(got.Rows), db.MaxRows)
	}
	if !got.Truncated {
		t.Error("the fetch cap bound and the envelope did not say so")
	}
}

// TestQueryJSONReportsARejectedStatement.
//
// The flag chose the shape of the answer, so it has to hold for the failures
// too: a consumer should not decode one envelope when the statement ran and a
// different one when it did not. And the exit status still says it failed —
// valid JSON on stdout is not the same as success.
func TestQueryJSONReportsARejectedStatement(t *testing.T) {
	root := sqliteHostFixture(t)
	out, code := captureOneShotIn(t, []string{
		":query -json SELECT * FROM there_is_no_such_table",
	}, root, true)
	if code == 0 {
		t.Errorf("a rejected statement exited 0\n%s", out)
	}

	got := decodeLast(t, out)
	if got.OK {
		t.Error("a rejected statement reported ok")
	}
	if got.Error == "" {
		t.Errorf("the envelope carries no error:\n%s", out)
	}
	if len(got.Rows) != 0 {
		t.Errorf("a failure carries rows: %v", got.Rows)
	}
	if got.Command != ":query" {
		t.Errorf("command = %q, want :query", got.Command)
	}
}
