package repl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/db"
)

// fixtureEnv is an environment stated rather than read, so these tests say what
// they mean and never depend on the machine running them.
var fixtureEnv = []string{
	"DATABASE_URL=postgres://ada:hunter2@db.internal:5432/app",
	"database_pool=25",
	"NOTE=postgres://ada:hunter2@db.internal:5432/app",
	"PASSWORD_HINT=ask the team",
	"PATH=/usr/bin:/bin",
	"AWS_KEY=AKIAIOSFODNN7EXAMPLE",
	"API_AUTH=Bearer sq7Kd0aMzX9vLpQr2TfY",
	"LOG_LEVEL=debug",
}

func rowFor(t *testing.T, rows [][]string, name string) []string {
	t.Helper()
	for _, r := range rows {
		if r[0] == name {
			return r
		}
	}
	t.Fatalf("%s is not in the listing", name)
	return nil
}

// TestEnvNeverRedactsAName is the half of the rule people notice when it is
// broken: a masked value beside a masked name says nothing at all.
func TestEnvNeverRedactsAName(t *testing.T) {
	rows, _ := envRows(fixtureEnv, "")
	for _, r := range rows {
		if strings.Contains(r[0], "***") {
			t.Errorf("a variable name was redacted: %q", r[0])
		}
	}
	for _, want := range []string{"DATABASE_URL", "PASSWORD_HINT", "AWS_KEY", "NOTE"} {
		rowFor(t, rows, want)
	}
}

// TestEnvSortsByName. The listing is read by scanning, so the order has to be
// the one a reader can predict.
func TestEnvSortsByName(t *testing.T) {
	rows, _ := envRows(fixtureEnv, "")
	for i := 1; i < len(rows); i++ {
		if rows[i-1][0] > rows[i][0] {
			t.Fatalf("out of order: %q before %q", rows[i-1][0], rows[i][0])
		}
	}
	if len(rows) != len(fixtureEnv) {
		t.Errorf("listed %d of %d variables", len(rows), len(fixtureEnv))
	}
}

// TestEnvFiltersCaseInsensitively: the pattern is a convenience, and a reader
// who types it in lower case means the upper-case names too.
func TestEnvFiltersCaseInsensitively(t *testing.T) {
	rows, _ := envRows(fixtureEnv, "database")
	if len(rows) != 2 {
		t.Fatalf("matched %d variables, want DATABASE_URL and database_pool: %v", len(rows), rows)
	}
	rowFor(t, rows, "DATABASE_URL")
	rowFor(t, rows, "database_pool")
}

// TestEnvKeyNameDoesNotDecide is invariant 23 in the position where breaking it
// costs something: NOTE is not a name anybody would put on a deny list, and
// PASSWORD_HINT is exactly the name a deny list would hide for nothing.
func TestEnvKeyNameDoesNotDecide(t *testing.T) {
	rows, _ := envRows(fixtureEnv, "")

	note := rowFor(t, rows, "NOTE")
	if strings.Contains(note[1], "hunter2") {
		t.Errorf("NOTE leaked its password: %q", note[1])
	}
	if note[2] != "redacted" {
		t.Errorf("NOTE was not marked redacted: %v", note)
	}
	if !strings.Contains(note[1], "db.internal") {
		t.Errorf("NOTE lost the host, so the redaction is not diagnosable: %q", note[1])
	}

	hint := rowFor(t, rows, "PASSWORD_HINT")
	if hint[1] != "ask the team" {
		t.Errorf("PASSWORD_HINT was redacted on its name alone: %q", hint[1])
	}
	if hint[2] != "" {
		t.Errorf("PASSWORD_HINT was marked redacted: %v", hint)
	}
}

// TestEnvRedactsByShape covers the shapes that are not connection strings.
func TestEnvRedactsByShape(t *testing.T) {
	rows, n := envRows(fixtureEnv, "")
	if n != 4 {
		t.Errorf("redacted %d values, want 4 (DATABASE_URL, NOTE, AWS_KEY, API_AUTH)", n)
	}
	for _, name := range []string{"DATABASE_URL", "NOTE", "AWS_KEY", "API_AUTH"} {
		if r := rowFor(t, rows, name); r[2] != "redacted" {
			t.Errorf("%s was not redacted: %q", name, r[1])
		}
	}
	for _, name := range []string{"PATH", "LOG_LEVEL", "database_pool"} {
		if r := rowFor(t, rows, name); r[2] != "" {
			t.Errorf("%s was redacted and should not be: %q", name, r[1])
		}
	}
}

// TestEnvMarksRedactionSeparately: a value gluon hid and a value that contains
// the marker on its own must not read the same.
func TestEnvMarksRedactionSeparately(t *testing.T) {
	rows, _ := envRows([]string{
		"LITERAL=***",
		"REAL=postgres://ada:hunter2@db.internal:5432/app",
	}, "")
	if r := rowFor(t, rows, "LITERAL"); r[2] != "" {
		t.Errorf("a value that merely contains the marker was called redacted: %v", r)
	}
	if r := rowFor(t, rows, "REAL"); r[2] != "redacted" {
		t.Errorf("a redacted value was not marked: %v", r)
	}
}

// TestEnvPatternThatMatchesNothingSaysSo, rather than printing an empty table
// that looks like an environment with nothing in it.
func TestEnvPatternThatMatchesNothingSaysSo(t *testing.T) {
	c := &Core{}
	res := c.metaEnv("zzz-no-such-variable")
	if res.Err {
		t.Errorf("a pattern matching nothing is not an error: %q", res.Out)
	}
	if !strings.Contains(res.Out, "zzz-no-such-variable") {
		t.Errorf("the report does not name the pattern: %q", res.Out)
	}
	if strings.Contains(res.Out, "─") {
		t.Errorf("an empty table was printed:\n%s", res.Out)
	}
}

// TestEnvDoesNotModifyTheEnvironment. The spec's "inspection never modifies",
// for the half of it a unit test can reach.
func TestEnvDoesNotModifyTheEnvironment(t *testing.T) {
	t.Setenv("GLUON_ENV_PROBE", "postgres://ada:hunter2@h:5432/app")
	before := os.Environ()
	c := &Core{}
	c.metaEnv("")
	c.metaEnv("GLUON")
	after := os.Environ()
	if len(before) != len(after) {
		t.Fatalf("the environment changed size: %d -> %d", len(before), len(after))
	}
	if got := os.Getenv("GLUON_ENV_PROBE"); got != "postgres://ada:hunter2@h:5432/app" {
		t.Errorf("a value was rewritten in place: %q", got)
	}
}

func writeFixture(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestConfReadsAllThreeFormats, into the same dotted keys. The formats differ
// and the answer must not.
func TestConfReadsAllThreeFormats(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"app.toml": "[database]\nurl = \"postgres://ada:hunter2@db.internal:5432/app\"\npool = 25\n",
		"app.yaml": "database:\n  url: postgres://ada:hunter2@db.internal:5432/app\n  pool: 25\n",
		"app.json": `{"database":{"url":"postgres://ada:hunter2@db.internal:5432/app","pool":25}}`,
	}
	c := &Core{}
	for name, body := range files {
		t.Run(name, func(t *testing.T) {
			p := writeFixture(t, dir, name, body)
			res := c.metaConf(p)
			if res.Err {
				t.Fatalf("%s: %s", name, res.Out)
			}
			if !strings.Contains(res.Out, "database.url") {
				t.Errorf("%s: no dotted key in the output:\n%s", name, res.Out)
			}
			if !strings.Contains(res.Out, "database.pool") {
				t.Errorf("%s: a non-string leaf was dropped:\n%s", name, res.Out)
			}
			if strings.Contains(res.Out, "hunter2") {
				t.Errorf("%s: the password reached the output:\n%s", name, res.Out)
			}
			if !strings.Contains(res.Out, "db.internal") {
				t.Errorf("%s: the redaction dropped the host:\n%s", name, res.Out)
			}
			if !strings.Contains(res.Out, "redacted") {
				t.Errorf("%s: the redaction was not marked:\n%s", name, res.Out)
			}
		})
	}
}

// TestConfKeyNameDoesNotDecide, in a file rather than an environment.
func TestConfKeyNameDoesNotDecide(t *testing.T) {
	dir := t.TempDir()
	p := writeFixture(t, dir, "app.toml",
		"note = \"postgres://ada:hunter2@db.internal:5432/app\"\n"+
			"password_hint = \"ask the team\"\n")
	res := (&Core{}).metaConf(p)
	if res.Err {
		t.Fatal(res.Out)
	}
	if strings.Contains(res.Out, "hunter2") {
		t.Errorf("note leaked its password:\n%s", res.Out)
	}
	if !strings.Contains(res.Out, "ask the team") {
		t.Errorf("password_hint was hidden on its name alone:\n%s", res.Out)
	}
}

// TestConfUnparseableShowsNoKeys, and says where the parser stopped. A
// configuration shown as half read hides the half that is missing.
func TestConfUnparseableShowsNoKeys(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"broken.toml": "[database\nurl = \"x\"\ngood = \"visible\"\n",
		"broken.yaml": "database:\n  url: x\n    pool: {[\ngood: visible\n",
		"broken.json": "{\"database\": {\"url\": \"x\",},\n\"good\": \"visible\"}",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			p := writeFixture(t, dir, name, body)
			res := (&Core{}).metaConf(p)
			if !res.Err {
				t.Fatalf("a file that does not parse was reported as read:\n%s", res.Out)
			}
			if strings.Contains(res.Out, "visible") {
				t.Errorf("keys from a partial parse were shown:\n%s", res.Out)
			}
			if !strings.Contains(res.Out, "line") {
				t.Errorf("the failure does not say where it happened:\n%s", res.Out)
			}
		})
	}
}

// TestConfUnsupportedFormatSaysWhatItReads. "cannot read this" without the list
// leaves the reader guessing which extension to try.
func TestConfUnsupportedFormatSaysWhatItReads(t *testing.T) {
	dir := t.TempDir()
	p := writeFixture(t, dir, "app.ini", "[database]\nurl=x\n")
	res := (&Core{}).metaConf(p)
	if !res.Err {
		t.Fatalf("an unsupported extension was read: %s", res.Out)
	}
	for _, want := range []string{".ini", ".toml", ".yaml", ".json"} {
		if !strings.Contains(res.Out, want) {
			t.Errorf("the message does not mention %s: %q", want, res.Out)
		}
	}
}

// TestConfMissingFileReports rather than showing an empty listing.
func TestConfMissingFileReports(t *testing.T) {
	res := (&Core{}).metaConf(filepath.Join(t.TempDir(), "absent.toml"))
	if !res.Err {
		t.Errorf("a missing file was reported as read: %s", res.Out)
	}
}

// TestConfDoesNotWrite is the spec's "inspection never modifies" for files: the
// fixture's bytes and modification time both survive being read.
func TestConfDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	body := "[database]\nurl = \"postgres://ada:hunter2@h:5432/app\"\n"
	p := writeFixture(t, dir, "app.toml", body)
	before, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	(&Core{}).metaConf(p)
	after, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) || before.Size() != after.Size() {
		t.Error("the file was written to")
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Errorf("the file's contents changed:\n%s", got)
	}
}

// TestConfSearchListsWhatItFound. The paths are what the reader will type into
// `:conf <file>` next, so they have to be there.
func TestConfSearchListsWhatItFound(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, dir, ".env", "DATABASE_URL=postgres://ada:hunter2@h:5432/app\n")
	writeFixture(t, filepath.Join(dir, "config"), "app.yaml", "database:\n  url: x\n")

	got := db.Detect(dir, nil)
	out := (&Core{}).renderConfSearch(&got)

	if !strings.Contains(out, ".env") {
		t.Errorf("the .env is not listed:\n%s", out)
	}
	if !strings.Contains(out, "app.yaml") {
		t.Errorf("the config file is not listed:\n%s", out)
	}
	if strings.Contains(out, "hunter2") {
		t.Errorf("the search report printed a secret:\n%s", out)
	}
}

// TestConfSearchDistinguishesNothingFoundFromNotLooking is the whole reason
// Detection carries Roots and Ceiling. An empty answer that does not say where
// it looked is a shrug.
func TestConfSearchDistinguishesNothingFoundFromNotLooking(t *testing.T) {
	dir := t.TempDir()
	// A repository root, so the search has a ceiling to stop at and says so.
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := db.Detect(dir, nil)
	out := (&Core{}).renderConfSearch(&got)

	if !strings.Contains(out, "no configuration file found") {
		t.Errorf("an empty search did not say so:\n%s", out)
	}
	if !strings.Contains(out, "looked in") {
		t.Errorf("an empty search did not name the directories it read:\n%s", out)
	}
	if len(got.Roots) == 0 {
		t.Fatal("detection reported no roots, so there is nothing to render")
	}
	if !strings.Contains(out, "stopped at the repository root") {
		t.Errorf("the boundary that stopped the search is not reported:\n%s", out)
	}
}

// TestConfSearchNeverWrites: the report is a read of the project, and a project
// with no configuration must not acquire one by being asked about.
func TestConfSearchNeverWrites(t *testing.T) {
	dir := t.TempDir()
	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := db.Detect(dir, nil)
	(&Core{}).renderConfSearch(&got)
	after, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Errorf("the search created something: %d entries -> %d", len(before), len(after))
	}
}

// TestRedactSettingsListing is task 4.4's other half: a settings map as a
// config library would print it, redacted on the way to the screen.
//
// The listing is the plugin's output, not a file, so this is the one place the
// `key = value` form and the shared secret test meet.
func TestRedactSettingsListing(t *testing.T) {
	in := strings.Join([]string{
		"database.url = postgres://ada:hunter2@db.internal:5432/app",
		"database.pool = 25",
		"note = postgres://ada:hunter2@db.internal:5432/app",
		"password_hint = ask the team",
		"api.token = Bearer sq7Kd0aMzX9vLpQr2TfY",
		"log.level = debug",
	}, "\n")

	got, n := redactLines(in)
	if n != 3 {
		t.Errorf("redacted %d values, want 3 (database.url, note, api.token):\n%s", n, got)
	}
	if strings.Contains(got, "hunter2") || strings.Contains(got, "sq7Kd0aMzX9vLpQr2TfY") {
		t.Errorf("a secret survived:\n%s", got)
	}
	for _, want := range []string{
		"database.url = postgres://ada:***@db.internal:5432/app",
		"database.pool = 25",
		"password_hint = ask the team",
		"api.token = Bearer ***",
		"log.level = debug",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// Every key survives, in order. A redactor that drops lines loses answers.
	if a, b := strings.Count(in, "\n"), strings.Count(got, "\n"); a != b {
		t.Errorf("line count changed: %d -> %d", a+1, b+1)
	}
}

// TestRedactLinesPassesThroughWhatItDoesNotUnderstand. A line that is not in
// the `key = value` form is output the plugin chose to print, and reformatting
// it would lose it.
func TestRedactLinesPassesThroughWhatItDoesNotUnderstand(t *testing.T) {
	in := "no settings resolved"
	got, n := redactLines(in)
	if got != in || n != 0 {
		t.Errorf("redactLines(%q) = %q, %d", in, got, n)
	}
}

// TestEnvSpellsOutControlCharacters: nushell exports its prompt pieces with the
// escapes already in them, so a real environment holds ESC. Printed as it is,
// the value would repaint the table around it and put escape sequences into
// Result.Out, which is plain bytes by invariant 30.
func TestEnvSpellsOutControlCharacters(t *testing.T) {
	rows, _ := envRows([]string{
		"PROMPT_MULTILINE_INDICATOR=\x1b[37m> \x1b[0m",
		"TWO_LINES=a\nb",
	}, "")
	if got := rowFor(t, rows, "PROMPT_MULTILINE_INDICATOR")[1]; got != `\x1b[37m> \x1b[0m` {
		t.Errorf("escape shown as %q, want it spelled out", got)
	}
	if got := rowFor(t, rows, "TWO_LINES")[1]; got != `a\nb` {
		t.Errorf("newline shown as %q, want it spelled out", got)
	}
	if got := printable("plain, and héllo"); got != "plain, and héllo" {
		t.Errorf("printable changed a value with nothing to spell out: %q", got)
	}
}
