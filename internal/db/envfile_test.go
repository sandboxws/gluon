package db

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestEnvFileHandlesTheFormsRealFilesUse.
//
// There is no standard for .env, so every rule here is a case that appears in
// somebody's project. Getting any of them wrong produces a value that is almost
// right, which then fails to connect for a reason nobody can see.
func TestEnvFileHandlesTheFormsRealFilesUse(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, ".env", "\ufeff"+`# a comment
EMPTY=
PLAIN=hello
EXPORTED=yes
SPACED = padded
DQ="a\tb"
SQ='no \t escapes'
TRAILING=value # this is a comment
HASHPW=pa#ss
CRLF=windows
not an assignment
if [ -z "$X" ]; then
`)
	// The line above with `export` needs to be real; rewrite with CRLF too.
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	fixed := string(body)
	fixed = fixed[:len(fixed)-len("if [ -z \"$X\" ]; then\n")]
	fixed += "export EXPORTED_KEY=exported\r\n"
	if err := os.WriteFile(p, []byte(fixed), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ReadEnvFile(p)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, a := range got {
		m[a.Key] = a.Value
	}

	for _, tc := range []struct{ key, want string }{
		{"EMPTY", ""},
		{"PLAIN", "hello"},
		{"SPACED", "padded"},
		{"DQ", "a\tb"},
		{"SQ", `no \t escapes`},
		{"TRAILING", "value"},
		// The rule that matters: a # with no whitespace before it is part of
		// the value. Without it a password becomes a prefix of itself.
		{"HASHPW", "pa#ss"},
		{"CRLF", "windows"},
		{"EXPORTED_KEY", "exported"},
	} {
		if got, ok := m[tc.key]; !ok {
			t.Errorf("%s was not read", tc.key)
		} else if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.key, got, tc.want)
		}
	}
	// A BOM must not become part of the first key.
	if _, ok := m["EMPTY"]; !ok {
		t.Error("a leading BOM was read into the first key name")
	}
}

// TestEnvFileIgnoresWhatIsNotAnAssignment.
//
// A .env that is really a shell script is common enough to matter, and every
// line of one contains characters that could be mistaken for a key.
func TestEnvFileIgnoresWhatIsNotAnAssignment(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, ".env", `if [ -z "$X" ]; then
  echo "hello"
fi
KEY-WITH-DASH=nope
1LEADING=nope
GOOD=yes
`)
	got, err := ReadEnvFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Key != "GOOD" {
		t.Errorf("read %v, want only GOOD", got)
	}
}

// TestEnvFileReportsLineNumbers.
//
// :db says ".env:3 DATABASE_URL" so somebody can open the file and see the
// thing gluon read. A report without that is an assertion.
func TestEnvFileReportsLineNumbers(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, ".env", "# comment\n\nDATABASE_URL=postgres://localhost/x\n")
	got, err := ReadEnvFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Line != 3 {
		t.Errorf("got %v, want DATABASE_URL on line 3", got)
	}
}

// TestExpandEnvResolvesAndReportsWhatItCannot.
//
// postgres://${DB_USER}:${DB_PASS}@localhost/app is everywhere. Reporting that
// string verbatim would be reporting a template as a database; silently
// expanding a missing variable to "" would produce a DSN that looks fine and
// cannot connect.
func TestExpandEnvResolvesAndReportsWhatItCannot(t *testing.T) {
	local := map[string]string{"DB_USER": "app", "DB_PASS": "s3cret"}
	got, missing := ExpandEnv("postgres://${DB_USER}:${DB_PASS}@localhost/app", local)
	if got != "postgres://app:s3cret@localhost/app" {
		t.Errorf("ExpandEnv = %q", got)
	}
	if len(missing) != 0 {
		t.Errorf("missing = %v, want none", missing)
	}

	_, missing = ExpandEnv("postgres://${NOPE_NOT_SET}@localhost/app", nil)
	if len(missing) != 1 || missing[0] != "NOPE_NOT_SET" {
		t.Errorf("missing = %v, want the unresolved name", missing)
	}
}

// TestLookupEnvFileExpandsWithinTheSameFile — DB_URL built from DB_HOST defined
// three lines above it is the ordinary shape of these files.
func TestLookupEnvFileExpandsWithinTheSameFile(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, ".env", "DB_HOST=db.internal\nDATABASE_URL=postgres://app@${DB_HOST}:5432/acme\n")
	got, ok := LookupEnvFile(p, "DATABASE_URL")
	if !ok {
		t.Fatal("DATABASE_URL not found")
	}
	if got != "postgres://app@db.internal:5432/acme" {
		t.Errorf("got %q", got)
	}
}
