//go:build integration

package repl

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/host"
)

// sqliteFixture builds a host module that requires a pure-Go SQLite driver, and
// a database for it to open.
//
// SQLite is what makes this path testable at all: it needs no server, so the
// query half of the feature can be exercised for real rather than mocked — and
// mocking it would prove nothing, since the whole design rests on the *host's*
// driver being the one that runs.
//
// It skips rather than fails when the driver cannot be resolved offline. gluon
// builds with GOPROXY=off, so a machine with a cold module cache cannot run
// this, and that is a property of the machine rather than of the code.
const sqliteVersion = "v1.49.1"

func sqliteFixture(t *testing.T) string {
	t.Helper()
	cacheOut, cacheErr := exec.Command("go", "env", "GOMODCACHE").Output()

	// HOME is deliberately left alone. Moving it would make the go command
	// build a second module cache inside the temp directory — populated with
	// read-only files that t.TempDir's cleanup then cannot remove. The fixture
	// carries its own .git, which is all the isolation the detector needs.
	root := t.TempDir()

	dbPath := filepath.Join(root, "data", "app.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	// A SQLite file, written by hand: the header plus an empty page is enough
	// for the driver to open and then create tables in.
	if err := os.WriteFile(dbPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		"go.mod": "module gluon.test/fixture\n\ngo 1.25.0\n\nrequire modernc.org/sqlite " + sqliteVersion + "\n",
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

	// Resolve go.sum from the module cache, used as a proxy.
	if cacheErr != nil {
		t.Skip("cannot locate the module cache:", cacheErr)
	}
	proxy := "file://" + filepath.Join(strings.TrimSpace(string(cacheOut)), "cache", "download")

	// Pinned, and pinned to what go.mod already names: a bare `go get` resolves
	// to the latest version, which is by definition the one least likely to be
	// in the cache.
	cmd := exec.Command("go", "get", "modernc.org/sqlite@"+sqliteVersion)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"GOFLAGS=-mod=mod", "GOPROXY="+proxy,
		"GOSUMDB=off", "GONOSUMCHECK=1", "GOFLAGS=-mod=mod", "GOTOOLCHAIN=local")
	if out, err := cmd.CombinedOutput(); err != nil {
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

func attachedCore(t *testing.T, root string) *Core {
	t.Helper()
	c, err := NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	t.Cleanup(func() { c.Close() })
	h, err := host.Detect(root)
	if err != nil {
		t.Fatalf("host.Detect: %v", err)
	}
	if err := c.ev.UseHost(h); err != nil {
		t.Fatalf("UseHost: %v", err)
	}
	c.refreshPlugins()
	return c
}

// TestQueryIsLiveAcrossTwoIdenticalStatements proves the cache bypass end to
// end, which is Problem (A).
//
// The result cache is keyed on program text alone, so two identical :query
// calls render byte-identical programs. Served from the cache, the second would
// return the first one's rows — a row count from ten minutes ago presented as
// current, which is the quiet wrongness this project rejected an interpreter to
// avoid. random() differs per call, so a cached answer repeats and a live one
// does not.
func TestQueryIsLiveAcrossTwoIdenticalStatements(t *testing.T) {
	c := attachedCore(t, sqliteFixture(t))

	var seen []string
	for i := 0; i < 3; i++ {
		res := c.Submit(":query SELECT random() AS r")
		if res.Err {
			t.Fatalf("run %d: %s", i, res.Out)
		}
		seen = append(seen, res.Out)
	}
	if seen[0] == seen[1] || seen[1] == seen[2] {
		t.Errorf("identical statements returned identical rows — the result was cached:\n%s", seen[0])
	}
}

// TestQueryDoesNotWriteTheDSNIntoTheGeneratedSource proves Problem (B) against
// the file on disk rather than against the rewrite's return value.
//
// A connection string interpolated into the source lands in <tmp>/main.go, in
// :src, in what :save writes, and in the build cache keyed to that text. The
// program reads an environment variable instead, and this is what says the
// mechanism actually held once a real query had run.
func TestQueryDoesNotWriteTheDSNIntoTheGeneratedSource(t *testing.T) {
	root := sqliteFixture(t)
	c := attachedCore(t, root)

	if res := c.Submit(":query SELECT 1 AS one"); res.Err {
		t.Fatalf("query failed: %s", res.Out)
	}
	dbPath := filepath.Join(root, "data", "app.db")

	src, err := os.ReadFile(filepath.Join(c.ev.Dir(), "main.go"))
	if err != nil {
		t.Fatalf("reading the generated program: %v", err)
	}
	if strings.Contains(string(src), dbPath) {
		t.Errorf("the connection string is in the generated source:\n%s", src)
	}
	if !strings.Contains(string(src), "os.Getenv") {
		t.Errorf("the generated program does not read its connection from the environment:\n%s", src)
	}
}

// TestQueryLeavesNoDriverImportBehind is invariant 14, in the form that costs
// the next line a doomed build.
//
// The driver arrives as a blank import that no line names. Left in the
// evaluator's cached import set, the next ordinary line would write an import
// it does not use and fail with "imported and not used" — recovering only by
// paying a full goimports pass, on a line that had nothing to do with the
// query.
func TestQueryLeavesNoDriverImportBehind(t *testing.T) {
	c := attachedCore(t, sqliteFixture(t))

	if res := c.Submit(":query SELECT 1 AS one"); res.Err {
		t.Fatalf("query failed: %s", res.Out)
	}
	// An ordinary line must still build, and must not carry the driver.
	res := c.Submit(`strings.ToUpper("after a query")`)
	if res.Err {
		t.Fatalf("an ordinary line failed after a query: %s", res.Out)
	}
	if !strings.Contains(res.Out, "AFTER A QUERY") {
		t.Errorf("unexpected output: %s", res.Out)
	}
	src, err := c.source()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(src, "modernc.org/sqlite") {
		t.Errorf("the driver import leaked into the session:\n%s", src)
	}
}

// TestQueryEnvironmentIsNotVisibleToAnOrdinaryLine.
//
// The connection string is supplied to one exec and nothing else. A line typed
// at the prompt must not be able to read it back, or the mechanism that keeps
// it out of the source would only have moved it somewhere less obvious.
func TestQueryEnvironmentIsNotVisibleToAnOrdinaryLine(t *testing.T) {
	c := attachedCore(t, sqliteFixture(t))

	if res := c.Submit(":query SELECT 1 AS one"); res.Err {
		t.Fatalf("query failed: %s", res.Out)
	}
	res := c.Submit(`os.Getenv("GLUON_DB_DSN")`)
	if res.Err {
		t.Fatalf("%s", res.Out)
	}
	if !strings.Contains(res.Out, `""`) {
		t.Errorf("an ordinary line could read the connection string: %s", res.Out)
	}
}

// TestWriteStatementNeedsTheFlagAgainstARealDatabase — the keyword check is
// client-side, and this is the half that proves a refused statement never
// reaches the database at all.
func TestWriteStatementNeedsTheFlag(t *testing.T) {
	c := attachedCore(t, sqliteFixture(t))

	if res := c.Submit(":query -w CREATE TABLE t (id INTEGER)"); res.Err {
		t.Fatalf("creating a table with -w failed: %s", res.Out)
	}
	if res := c.Submit(":query -w INSERT INTO t (id) VALUES (1)"); res.Err {
		t.Fatalf("insert with -w failed: %s", res.Out)
	}
	res := c.Submit(":query DELETE FROM t")
	if !res.Err {
		t.Fatal("a DELETE ran without -w")
	}
	after := c.Submit(":query SELECT count(*) AS n FROM t")
	if after.Err {
		t.Fatalf("%s", after.Out)
	}
	if !strings.Contains(after.Out, "1") {
		t.Errorf("the refused DELETE reached the database: %s", after.Out)
	}
}

// TestHostTreeIsUnchangedAfterAQuery is invariant 1's standing assertion,
// extended to this path: gluon must never write into the project it is
// attached to.
func TestHostTreeIsUnchangedAfterAQuery(t *testing.T) {
	root := sqliteFixture(t)
	before := treeSnapshot(t, root)

	c := attachedCore(t, root)
	if res := c.Submit(":query SELECT 1 AS one"); res.Err {
		t.Fatalf("query failed: %s", res.Out)
	}
	after := treeSnapshot(t, root)
	// The database file itself may legitimately change — SQLite writes a
	// journal on open. Everything else must not.
	for name := range after {
		if strings.HasPrefix(name, "data/") {
			continue
		}
		if before[name] != after[name] {
			t.Errorf("%s changed in the host tree", name)
		}
	}
	for name := range before {
		if strings.HasPrefix(name, "data/") {
			continue
		}
		if _, ok := after[name]; !ok {
			t.Errorf("%s disappeared from the host tree", name)
		}
	}
}

func treeSnapshot(t *testing.T, root string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = info.Size()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
