package db

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// project builds a fixture tree and returns the module directory.
func project(t *testing.T, files map[string]string) (root, module string) {
	t.Helper()
	root = t.TempDir()
	// Keep the real home out of it: find.Repo refuses $HOME as a ceiling, and a
	// test must not depend on where it happens to be run.
	t.Setenv("HOME", filepath.Join(root, "not-home"))
	for p, body := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root, root
}

// TestDetectFindsADSNInAnEnvFileByShape.
func TestDetectFindsADSNInAnEnvFileByShape(t *testing.T) {
	_, mod := project(t, map[string]string{
		"go.mod": "module example.com/api\n",
		".env":   "PORT=3000\nDATABASE_URL=postgres://app@localhost:5432/acme\n",
	})
	got := Detect(mod, nil)
	if got.Chosen == nil {
		t.Fatalf("nothing chosen; candidates=%v searched=%v", got.Candidates, got.Searched)
	}
	if got.Chosen.DSN.Database != "acme" {
		t.Errorf("Database = %q", got.Chosen.DSN.Database)
	}
	if got.Chosen.Secret.Env != "DATABASE_URL" {
		t.Errorf("Secret.Env = %q, want the variable name", got.Chosen.Secret.Env)
	}
	if len(got.Chosen.From) == 0 || got.Chosen.From[0].Line != 2 {
		t.Errorf("provenance = %v, want .env line 2", got.Chosen.From)
	}
}

// TestDetectIgnoresAKeyNamedLikeADatabaseHoldingSomethingElse.
//
// Invariant 23, and the whole reason Parse is never told the key's name.
// DATABASE_URL pointing at an HTTP API is a real misconfiguration, and
// reporting it as the project's database would send somebody debugging a
// database that does not exist.
func TestDetectIgnoresAKeyNamedLikeADatabaseHoldingSomethingElse(t *testing.T) {
	_, mod := project(t, map[string]string{
		"go.mod": "module example.com/api\n",
		".env": strings.Join([]string{
			"DATABASE_URL=https://api.example.com",
			"DB_HOST=localhost",
			"REDIS_URL=redis://localhost:6379",
			"LOG_PATH=/var/log/app.log",
			"TIMEOUT=30",
		}, "\n") + "\n",
	})
	got := Detect(mod, nil)
	if got.Chosen != nil {
		t.Errorf("chose %v from a file with no database in it", got.Chosen.DSN.Target())
	}
	if len(got.Candidates) != 0 {
		t.Errorf("candidates = %v, want none", got.Candidates)
	}
	// ...and it must still say it looked, or "found nothing" and "did not look"
	// are the same output.
	if len(got.Searched) == 0 {
		t.Error("Searched is empty, so the report cannot say what was read")
	}
}

// TestDetectFindsADSNUnderAnUnremarkableKey — the other half of shape-not-name.
func TestDetectFindsADSNUnderAnUnremarkableKey(t *testing.T) {
	_, mod := project(t, map[string]string{
		"go.mod": "module example.com/api\n",
		".env":   "STORAGE_BACKEND=postgres://app@localhost:5432/acme\n",
	})
	got := Detect(mod, nil)
	if got.Chosen == nil {
		t.Fatal("a database under an unusual key was missed")
	}
	if got.Chosen.Secret.Env != "STORAGE_BACKEND" {
		t.Errorf("Secret.Env = %q", got.Chosen.Secret.Env)
	}
}

// TestExampleFileContributesTheNameNeverTheValue.
//
// .env.example holds placeholders. postgres://user:password@localhost/dbname is
// a parseable connection string and a fiction, and reporting it would be
// confidently wrong in the most misleading way available.
func TestExampleFileContributesTheNameNeverTheValue(t *testing.T) {
	root, mod := project(t, map[string]string{
		"go.mod":       "module example.com/api\n",
		".env.example": "DATABASE_URL=postgres://user:password@localhost:5432/dbname\n",
	})
	got := Detect(mod, nil)
	if got.Chosen != nil {
		t.Errorf("an example file was reported as a real database: %v", got.Chosen.DSN.Target())
	}
	names := ExampleKeys(root)
	if len(names) != 1 || names[0] != "DATABASE_URL" {
		t.Errorf("ExampleKeys = %v, want the variable name", names)
	}
}

// TestComposeRequiresAServicesMappingAndAKnownImage.
//
// A Kubernetes manifest named docker-compose.yml is a real thing in a real
// repository. The shape rule — a services mapping — is what rejects it, and the
// image rule is what stops a service called "db" running redis from being
// reported as SQL.
func TestComposeRequiresAServicesMappingAndAKnownImage(t *testing.T) {
	_, mod := project(t, map[string]string{
		"go.mod": "module example.com/api\n",
		"docker-compose.yml": `apiVersion: v1
kind: Deployment
metadata:
  name: services
`,
	})
	if got := Detect(mod, nil); got.Chosen != nil {
		t.Errorf("a k8s manifest was read as compose: %v", got.Chosen.DSN.Target())
	}

	_, mod2 := project(t, map[string]string{
		"go.mod": "module example.com/api\n",
		"compose.yaml": `services:
  db:
    image: redis:7
    ports: ["6379:6379"]
  cache:
    image: memcached
`,
	})
	if got := Detect(mod2, nil); got.Chosen != nil {
		t.Errorf("a service named db running redis was reported as SQL: %v", got.Chosen.DSN.Target())
	}
}

// TestComposeUsesTheHostPortNeverTheServiceName.
//
// A service hostname resolves inside the compose network and nowhere else. A
// REPL that used it fails with a DNS error blaming the wrong thing — and the
// published port is the only address that works from this machine.
func TestComposeUsesTheHostPortNeverTheServiceName(t *testing.T) {
	_, mod := project(t, map[string]string{
		"go.mod": "module example.com/api\n",
		"compose.yaml": `services:
  warehouse:
    image: postgres:16
    ports:
      - "5433:5432"
    environment:
      POSTGRES_USER: app
      POSTGRES_DB: acme_dev
      POSTGRES_PASSWORD: secret
`,
	})
	got := Detect(mod, nil)
	if got.Chosen == nil {
		t.Fatalf("compose service not found; candidates=%v", got.Candidates)
	}
	d := got.Chosen.DSN
	if d.Host != "127.0.0.1" {
		t.Errorf("Host = %q, want 127.0.0.1 — a service name resolves only inside compose", d.Host)
	}
	if d.Port != 5433 {
		t.Errorf("Port = %d, want the published 5433", d.Port)
	}
	if d.Database != "acme_dev" || d.User != "app" {
		t.Errorf("got user=%q db=%q", d.User, d.Database)
	}
	if got.Chosen.Secret.Env != "POSTGRES_PASSWORD" {
		t.Errorf("Secret.Env = %q, want the password's location", got.Chosen.Secret.Env)
	}
}

// TestComposePortForms — compose accepts four spellings and projects use all
// of them; reading one wrong points the REPL at a port nothing is listening on.
func TestComposePortForms(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ports string
		want  int
	}{
		{"short", `["5432:5432"]`, 5432},
		{"remapped", `["5433:5432"]`, 5433},
		{"with host", `["127.0.0.1:5434:5432"]`, 5434},
		{"with proto", `["5435:5432/tcp"]`, 5435},
		{"long form", "[{target: 5432, published: \"5436\"}]", 5436},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, mod := project(t, map[string]string{
				"go.mod": "module example.com/api\n",
				"compose.yaml": "services:\n  db:\n    image: postgres:16\n    ports: " +
					tc.ports + "\n    environment:\n      POSTGRES_DB: acme\n",
			})
			got := Detect(mod, nil)
			if got.Chosen == nil {
				t.Fatalf("not found; candidates=%v", got.Candidates)
			}
			if got.Chosen.DSN.Port != tc.want {
				t.Errorf("Port = %d, want %d", got.Chosen.DSN.Port, tc.want)
			}
		})
	}
}

// TestComposeAnchorAndMergeKey is the test that justifies a real YAML parser.
//
// Merge keys are resolved by Decode and are not present in the raw node tree,
// so a walker that did not follow `<<: *anchor` — and a hand-rolled line
// scanner certainly would not — sees no environment at all here. Nothing fails;
// the answer is just quietly missing its database name.
func TestComposeAnchorAndMergeKey(t *testing.T) {
	_, mod := project(t, map[string]string{
		"go.mod": "module example.com/api\n",
		"compose.yaml": `x-common: &common
  environment:
    POSTGRES_USER: app
    POSTGRES_DB: acme_from_anchor
    POSTGRES_PASSWORD: secret

services:
  db:
    <<: *common
    image: postgres:16
    ports: ["5432:5432"]
`,
	})
	got := Detect(mod, nil)
	if got.Chosen == nil {
		t.Fatalf("not found; candidates=%v", got.Candidates)
	}
	if got.Chosen.DSN.Database != "acme_from_anchor" {
		t.Errorf("Database = %q — the merge key was not followed", got.Chosen.DSN.Database)
	}
}

// TestComposeServiceWithNoPublishedPortIsNeverChosen.
//
// It is still reported, because "there is a database here you cannot reach" is
// useful. It can never be the answer, because gluon could not open it.
func TestComposeServiceWithNoPublishedPortIsNeverChosen(t *testing.T) {
	_, mod := project(t, map[string]string{
		"go.mod": "module example.com/api\n",
		"compose.yaml": `services:
  db:
    image: postgres:16
    expose: ["5432"]
    environment:
      POSTGRES_DB: acme
`,
	})
	got := Detect(mod, nil)
	if len(got.Candidates) == 0 {
		t.Fatal("an unreachable service was not reported at all")
	}
	if len(got.Candidates[0].Concerns) == 0 {
		t.Error("no concern was recorded for a service publishing no port")
	}
}

// TestSQLiteFoundByHeaderNotExtension, at the detection level.
func TestSQLiteFoundByHeaderNotExtension(t *testing.T) {
	_, mod := project(t, map[string]string{
		"go.mod":            "module example.com/api\n",
		"data/real.db":      sqliteMagicLen + "a real database",
		"data/fixtures.db":  `{"users":[]}`,
		"data/empty.sqlite": "",
	})
	got := Detect(mod, nil)
	if got.Chosen == nil {
		t.Fatalf("the real database was not found; candidates=%v", got.Candidates)
	}
	if !strings.HasSuffix(got.Chosen.DSN.File, "real.db") {
		t.Errorf("chose %q, want real.db", got.Chosen.DSN.File)
	}
	if len(got.Candidates) != 1 {
		t.Errorf("candidates = %v, want only the real database", got.Candidates)
	}
}

// TestConfigFileFieldGroupNeedsMoreThanAName.
//
// `database: myapp` is a word. A host, a port and a database name together is a
// connection block. The threshold is what keeps every service definition in a
// config from being reported as a database.
func TestConfigFileFieldGroup(t *testing.T) {
	_, mod := project(t, map[string]string{
		"go.mod": "module example.com/api\n",
		"config/config.yaml": `service:
  name: api
  port: 8080
database:
  host: db.internal
  port: 5432
  user: app
  dbname: acme
`,
	})
	got := Detect(mod, nil)
	if got.Chosen == nil {
		t.Fatalf("a connection block was not recognised; candidates=%v", got.Candidates)
	}
	if got.Chosen.DSN.Database != "acme" || got.Chosen.DSN.Host != "db.internal" {
		t.Errorf("got %+v", got.Chosen.DSN)
	}
	if len(got.Candidates) != 1 {
		t.Errorf("candidates = %v — the service block was read as a database too", got.Candidates)
	}
}

// TestConfigFileDSNString — the other config shape.
func TestConfigFileDSNString(t *testing.T) {
	_, mod := project(t, map[string]string{
		"go.mod":      "module example.com/api\n",
		"config.toml": "[database]\nurl = \"postgres://app@localhost:5432/acme\"\n",
	})
	got := Detect(mod, nil)
	if got.Chosen == nil {
		t.Fatalf("not found; candidates=%v", got.Candidates)
	}
	if got.Chosen.Secret.Key != "database.url" {
		t.Errorf("Secret.Key = %q, want the dotted path", got.Chosen.Secret.Key)
	}
}

// TestCorroborationIsNotAmbiguity.
//
// A compose file names the superuser and the app's .env names the app user, for
// the same database. Two provenances, one answer. Refusing here would break the
// single most common project layout there is.
func TestCorroborationIsNotAmbiguity(t *testing.T) {
	_, mod := project(t, map[string]string{
		"go.mod": "module example.com/api\n",
		".env":   "DATABASE_URL=postgres://app:apppw@127.0.0.1:5432/acme_dev\n",
		"compose.yaml": `services:
  db:
    image: postgres:16
    ports: ["5432:5432"]
    environment:
      POSTGRES_USER: postgres
      POSTGRES_DB: acme_dev
`,
	})
	got := Detect(mod, nil)
	if got.Ambiguous {
		t.Fatal("two sources describing one database were refused as ambiguous")
	}
	if got.Chosen == nil {
		t.Fatal("nothing chosen")
	}
	if len(got.Candidates) != 1 {
		t.Fatalf("candidates = %d, want them merged into one", len(got.Candidates))
	}
	if len(got.Chosen.From) < 2 {
		t.Errorf("the merged candidate lists %d sources, want both", len(got.Chosen.From))
	}
}

// TestTwoDifferentDatabasesInTheSameTierAreRefused — invariant 13. The wrong
// guess is a query against the wrong database.
func TestTwoDifferentDatabasesInTheSameTierAreRefused(t *testing.T) {
	_, mod := project(t, map[string]string{
		"go.mod": "module example.com/api\n",
		".env": "PRIMARY_URL=postgres://app@localhost:5432/acme\n" +
			"WAREHOUSE_URL=postgres://app@localhost:5433/metrics\n",
	})
	got := Detect(mod, nil)
	if !got.Ambiguous {
		t.Errorf("two different databases were not refused; chose %v", got.Chosen)
	}
	if got.Chosen != nil {
		t.Error("a choice was made between two databases")
	}
	if len(got.Candidates) != 2 {
		t.Errorf("candidates = %d, want both listed", len(got.Candidates))
	}
}

// TestTheOneDocumentedTieBreakFiresAndSaysSo.
//
// DATABASE_URL beside TEST_DATABASE_URL is the ordinary case, and refusing
// every time would make the feature feel broken rather than careful. It is
// invariant 13's one kind of exception: exactly one name-based demotion, and it
// announces itself.
func TestTheOneDocumentedTieBreakFiresAndSaysSo(t *testing.T) {
	_, mod := project(t, map[string]string{
		"go.mod": "module example.com/api\n",
		".env": "DATABASE_URL=postgres://app@localhost:5432/acme\n" +
			"TEST_DATABASE_URL=postgres://app@localhost:5432/acme_test\n",
	})
	got := Detect(mod, nil)
	if got.Chosen == nil {
		t.Fatalf("the tie-break did not fire; ambiguous=%v", got.Ambiguous)
	}
	if got.Chosen.DSN.Database != "acme" {
		t.Errorf("chose %q, want acme", got.Chosen.DSN.Database)
	}
	if got.Note == "" {
		t.Error("the tie-break fired silently — it must say so")
	}
	if !strings.Contains(got.Note, "TEST_DATABASE_URL") {
		t.Errorf("the note does not name what was passed over: %q", got.Note)
	}
}

// TestSecondaryMatchesWholeElementsOnly — LATEST_URL is not TEST_URL, and a
// substring match would silently demote it.
func TestSecondaryMatchesWholeElementsOnly(t *testing.T) {
	if _, is := isSecondary("LATEST_DATABASE_URL"); is {
		t.Error("LATEST matched TEST")
	}
	if _, is := isSecondary("TEST_DATABASE_URL"); !is {
		t.Error("TEST_DATABASE_URL was not recognised")
	}
	if _, is := isSecondary("SHADOW_URL"); !is {
		t.Error("SHADOW_URL was not recognised")
	}
}

// TestDetectNeverLooksAboveTheRepository — the home-directory failure
// (invariant 12), in the form this feature could reproduce it.
func TestDetectNeverLooksAboveTheRepository(t *testing.T) {
	outer := t.TempDir()
	t.Setenv("HOME", filepath.Join(outer, "not-home"))
	if err := os.WriteFile(filepath.Join(outer, ".env"),
		[]byte("DATABASE_URL=postgres://app@localhost:5432/SOMEONE_ELSES\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(outer, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/api\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := Detect(repo, nil)
	if got.Chosen != nil {
		t.Errorf("detection reached above the repository and found %v", got.Chosen.DSN.Target())
	}
	for _, p := range got.Searched {
		if strings.HasPrefix(p, outer) && !strings.HasPrefix(p, repo) {
			t.Errorf("read %q, which is above the repository root %q", p, repo)
		}
	}
}

// TestDetectDoesNotReadASiblingService — a monorepo's other service is the
// nearest lookalike there is.
func TestDetectDoesNotReadASiblingService(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "not-home"))
	for p, body := range map[string]string{
		".git/HEAD":       "ref: refs/heads/main\n",
		"apps/api/go.mod": "module example.com/api\n",
		"apps/web/go.mod": "module example.com/web\n",
		"apps/web/.env":   "DATABASE_URL=postgres://app@localhost:5432/WEB_DB\n",
	} {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := Detect(filepath.Join(root, "apps", "api"), nil)
	if got.Chosen != nil {
		t.Errorf("found a sibling service's database: %v", got.Chosen.DSN.Target())
	}
}

// TestDetectReadsAnAncestorsComposeFile — the other half of the same walk. A
// compose file at the repository root does belong to the module below it.
func TestDetectReadsAnAncestorsComposeFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "not-home"))
	for p, body := range map[string]string{
		".git/HEAD":       "ref: refs/heads/main\n",
		"apps/api/go.mod": "module example.com/api\n",
		"compose.yaml": "services:\n  db:\n    image: postgres:16\n    ports: [\"5432:5432\"]\n" +
			"    environment:\n      POSTGRES_DB: acme\n",
	} {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := Detect(filepath.Join(root, "apps", "api"), nil)
	if got.Chosen == nil {
		t.Fatalf("a compose file at the repository root was not read; searched=%v", got.Searched)
	}
	if got.Chosen.DSN.Database != "acme" {
		t.Errorf("Database = %q", got.Chosen.DSN.Database)
	}
}

// TestDriversAreEvidenceNotCandidates.
//
// A project that requires lib/pq and configures nothing is a different answer
// from one with a DSN and no driver, and :query needs both halves. Reporting a
// driver as a database would claim a connection nobody described.
func TestDriversAreEvidenceNotCandidates(t *testing.T) {
	_, mod := project(t, map[string]string{"go.mod": "module example.com/api\n"})
	got := Detect(mod, []string{"github.com/lib/pq v1.10.9"})
	if got.Chosen != nil {
		t.Errorf("a driver in the build list was reported as a database: %v", got.Chosen)
	}
	if len(got.Drivers) != 1 {
		t.Fatalf("Drivers = %v, want lib/pq", got.Drivers)
	}
	if got.Drivers[0].Family != "postgres" {
		t.Errorf("Drivers[0] = %v", got.Drivers[0])
	}
}

// TestSourceLiteralIsFound — sql.Open("sqlite3", "./data/app.db") is the
// project telling you exactly what it opens.
func TestSourceLiteralIsFound(t *testing.T) {
	root, mod := project(t, map[string]string{
		"go.mod":      "module example.com/api\n",
		"data/app.db": sqliteMagicLen + "real",
		"store/db.go": "package store\n\nimport \"database/sql\"\n\n" +
			"func Open() { sql.Open(\"sqlite3\", \"../data/app.db\") }\n",
	})
	_ = root
	got := Detect(mod, nil)
	if got.Chosen == nil {
		t.Fatalf("nothing found; candidates=%v", got.Candidates)
	}
	var fromSource bool
	for _, c := range got.Candidates {
		for _, p := range c.From {
			if p.Kind == KindGoSource {
				fromSource = true
			}
		}
	}
	if !fromSource {
		t.Errorf("the sql.Open literal was not read; candidates=%v", got.Candidates)
	}
}

// TestDetectSaysWhereItLookedEvenWhenItFoundNothing.
//
// With nothing found there are no files to list, and that is exactly when
// "found nothing" and "did not look" would otherwise be the same output. Roots
// is what separates them, and a report that cannot tell them apart is the shrug
// this codebase refuses everywhere else.
func TestDetectSaysWhereItLookedEvenWhenItFoundNothing(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "not-home"))
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	mod := filepath.Join(root, "apps", "api")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := Detect(mod, nil)
	if got.Chosen != nil {
		t.Fatalf("found %v in an empty project", got.Chosen.DSN.Target())
	}
	if len(got.Searched) != 0 {
		t.Errorf("Searched = %v, want nothing read", got.Searched)
	}
	if len(got.Roots) == 0 {
		t.Fatal("Roots is empty, so the report cannot say it looked at all")
	}
	// It must have walked up to the repository, and stopped there.
	if got.Ceiling != root {
		t.Errorf("Ceiling = %q, want %q", got.Ceiling, root)
	}
	var sawRepo bool
	for _, r := range got.Roots {
		if r == root {
			sawRepo = true
		}
		if !strings.HasPrefix(r, root) {
			t.Errorf("looked in %q, which is outside the repository", r)
		}
	}
	if !sawRepo {
		t.Errorf("Roots = %v, want the repository root among them", got.Roots)
	}
}

// TestDetectReportsEachSourceOnce.
//
// The up-pass resolves ancestors to absolute paths and the down-pass walks from
// whatever root it was handed. A relative root therefore produced two spellings
// of one file, defeating the dedup and reporting a single compose file as two
// independent sources — corroboration invented out of nothing.
func TestDetectReportsEachSourceOnce(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"go.mod": "module example.com/api\n",
		"compose.yaml": "services:\n  db:\n    image: postgres:16\n    ports: [\"5432:5432\"]\n" +
			"    environment:\n      POSTGRES_DB: acme\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Both spellings of the same directory must give the same answer.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}

	for _, spelling := range []string{".", root} {
		got := Detect(spelling, nil)
		if got.Chosen == nil {
			t.Fatalf("%s: nothing found", spelling)
		}
		if n := len(got.Chosen.From); n != 1 {
			t.Errorf("%s: the compose file is reported %d times: %v", spelling, n, got.Chosen.From)
		}
		seen := map[string]bool{}
		for _, p := range got.Searched {
			if seen[filepath.Base(p)] {
				t.Errorf("%s: %s was read twice: %v", spelling, filepath.Base(p), got.Searched)
			}
			seen[filepath.Base(p)] = true
		}
	}
}

// TestStampChangesWithWhatWasRead.
//
// The stamp is what lets a detection outlive a :get, so it has to be wrong in
// neither direction: a file the search read and somebody then edited must move
// it, a file appearing in a directory the search looked in must move it, and
// churn anywhere else must not — or the cache is either stale or pointless.
func TestStampChangesWithWhatWasRead(t *testing.T) {
	dir := t.TempDir()
	read := filepath.Join(dir, ".env")
	if err := os.WriteFile(read, []byte("DATABASE_URL=postgres://localhost/a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(elsewhere, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	searched, roots := []string{read}, []string{dir}
	base := Stamp(searched, roots)
	if base == "" {
		t.Fatal("Stamp produced nothing")
	}
	if again := Stamp(searched, roots); again != base {
		t.Fatal("Stamp is not stable across two calls on an unchanged tree")
	}
	// The order it is asked in is not a change: Detect appends in walk order,
	// and a walk that visits two files in the other order found the same
	// project.
	if swapped := Stamp([]string{read}, []string{dir}); swapped != base {
		t.Error("Stamp depends on the order of its input")
	}

	// A file that was read, edited.
	if err := os.WriteFile(read, []byte("DATABASE_URL=postgres://localhost/b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	touch(t, read, time.Now().Add(2*time.Second))
	edited := Stamp(searched, roots)
	if edited == base {
		t.Error("editing a file the detection read did not change the stamp")
	}

	// A file appearing in a directory the search looked in — the case the file
	// list cannot see, because a file that was not there was never searched.
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	touch(t, dir, time.Now().Add(4*time.Second))
	created := Stamp(searched, roots)
	if created == edited {
		t.Error("a new file in a searched directory did not change the stamp")
	}

	// Churn in a file nothing read.
	if err := os.WriteFile(elsewhere, []byte("hello again, at length\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	touch(t, elsewhere, time.Now().Add(6*time.Second))
	if unrelated := Stamp(searched, roots); unrelated != created {
		t.Error("a file the detection never read changed the stamp")
	}

	// And a file that was read, removed: as much a change as an edit.
	if err := os.Remove(read); err != nil {
		t.Fatal(err)
	}
	if gone := Stamp(searched, roots); gone == created {
		t.Error("removing a file the detection read did not change the stamp")
	}
}

// touch pins an mtime rather than trusting the clock. Two writes inside one
// filesystem tick share a timestamp, and this test is about the stamp's
// definition, not about how fast the disk is.
func touch(t *testing.T, path string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

// TestDetectStampsWhatItRead — the stamp Detect records is the one a caller
// recomputes, or the cache never hits at all.
func TestDetectStampsWhatItRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"),
		[]byte("DATABASE_URL=postgres://localhost/a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Detect(dir, nil)
	if got.Stamp == "" {
		t.Fatal("Detect recorded no stamp")
	}
	if want := Stamp(got.Searched, got.Roots); got.Stamp != want {
		t.Errorf("Stamp = %q, want %q — a caller recomputing it would never match", got.Stamp, want)
	}
}

// BenchmarkDetect is the number that decides whether a startup screen may name
// the database.
//
// Detection reads the project's files: an up-pass stat-ing every candidate name
// in every ancestor to the repository root, a depth-3 walk of the module, and a
// parse of whatever that turns up. Invariant 22 permits that at a transition —
// opening a scratchpad is one of the three it names — but permitting it is not
// the same as it being free, and gluon's whole argument is about what a thing
// costs. The startup screen reaches detection only where no [[database]] was
// configured, so this is the worst case rather than the usual one.
//
// The budget is 15ms. Boot already spends a subprocess on `go env GOVERSION`
// and a full toolchain build on any non-empty scratchpad, so a walk inside that
// budget is noise against what is already being paid. Over it, the row falls
// back to naming configured entries only.
func BenchmarkDetect(b *testing.B) {
	root := b.TempDir()
	// A repository ceiling, so the up-pass has ancestors to climb.
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		b.Fatal(err)
	}
	mod := filepath.Join(root, "svc")
	write := func(p, body string) {
		b.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	write(filepath.Join(mod, "go.mod"), "module example.com/svc\n\ngo 1.25\n")
	write(filepath.Join(mod, ".env"), "DATABASE_URL=postgres://app@127.0.0.1:5432/acme\n")
	write(filepath.Join(root, "compose.yaml"),
		"services:\n  db:\n    image: postgres:16\n    environment:\n      POSTGRES_DB: acme\n")

	// Three tree sizes, because the answer is not one number: the walk is
	// bounded by depth and by maxFiles, so what it costs is a function of how
	// many directories sit inside depth 3 — and that is the thing that varies
	// between a tool and a monorepo.
	for _, n := range []int{2, 6, 20} {
		b.Run(fmt.Sprintf("%ddirs", n*n*3), func(b *testing.B) {
			for i := range 3 {
				for j := range n {
					for k := range n {
						p := filepath.Join(mod,
							fmt.Sprintf("a%d", i), fmt.Sprintf("b%d", j), fmt.Sprintf("c%d", k), "x.go")
						write(p, "package c\n")
					}
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				_ = Detect(mod, nil)
			}
		})
	}
}
