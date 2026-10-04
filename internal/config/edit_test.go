package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAppendPreservesTheFileByteForByte is the reason the schema is an array of
// tables at all.
//
// BurntSushi's encoder serializes a whole document from a struct: comments
// dropped, keys reordered, inline tables expanded. The day somebody replaces
// this append with a re-encode, a hand-written config loses every comment in it
// and nothing fails — the file still parses, and the settings still work. This
// is what would notice.
func TestAppendPreservesTheFileByteForByte(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	original := `# gluon, configured by hand.
# The comment above is the thing an encoder would silently drop.

imports = ["strings", "slices"]   # preloaded; an unused one costs nothing
editor  = "nvim"

[value]
items = "500"   # long slices, shown whole

[theme]
string = "2"   # green
error  = "1"
`
	if err := os.WriteFile(p, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := AppendDatabase(p, Database{
		Name: "primary", Driver: "postgres", DSNEnv: "DATABASE_URL",
	}, false); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), original) {
		t.Fatalf("the original was not preserved byte for byte.\n--- was ---\n%s\n--- now ---\n%s",
			original, got)
	}
	for _, comment := range []string{
		"# gluon, configured by hand.",
		"# preloaded; an unused one costs nothing",
		"# green",
	} {
		if !strings.Contains(string(got), comment) {
			t.Errorf("comment %q was lost", comment)
		}
	}
	// ...and the result still loads, with everything intact.
	c, err := LoadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Databases) != 1 || c.Databases[0].Name != "primary" {
		t.Errorf("Databases = %v", c.Databases)
	}
	if c.Editor != "nvim" || len(c.Imports) != 2 || c.Theme["string"] != "2" {
		t.Errorf("an existing setting changed: editor=%q imports=%v theme=%v",
			c.Editor, c.Imports, c.Theme)
	}
}

// TestAppendCreatesAMissingFileWithATightMode.
//
// A file gluon creates names where secrets live, even though it holds none. 0600
// is the honest default for that; inheriting 0644 from nothing would be a
// choice nobody made.
func TestAppendCreatesAMissingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "config.toml")
	if _, err := AppendDatabase(p, Database{Name: "a", Driver: "sqlite", File: "x.db"}, false); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 0600", st.Mode().Perm())
	}
	if _, err := LoadFile(p); err != nil {
		t.Errorf("the created file does not load: %v", err)
	}
}

// TestAppendKeepsAnExistingFilesMode — a config somebody made group-readable on
// purpose should stay that way.
func TestAppendKeepsAnExistingFilesMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("imports = []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := AppendDatabase(p, Database{Name: "a", Driver: "sqlite", File: "x.db"}, false); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o644 {
		t.Errorf("mode = %o, want the file's own 0644", st.Mode().Perm())
	}
}

// TestAppendRefusesADuplicateWithoutForce.
//
// gluon does not rewrite a block in place, because that is where the comments
// inside a block die. Refusing and saying which lines to change is the honest
// answer, and it is better than a clever editor that loses a comment once a
// year.
func TestAppendRefusesADuplicateWithoutForce(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	entry := Database{Name: "primary", Driver: "postgres", DSNEnv: "A"}
	if _, err := AppendDatabase(p, entry, false); err != nil {
		t.Fatal(err)
	}
	_, err := AppendDatabase(p, entry, false)
	if err == nil {
		t.Fatal("a duplicate was appended")
	}
	if !strings.Contains(err.Error(), "-force") {
		t.Errorf("the refusal does not say how to proceed: %v", err)
	}
	// The file must be untouched by the refusal.
	got, _ := os.ReadFile(p)
	if strings.Count(string(got), "[[database]]") != 1 {
		t.Errorf("the refused append still wrote something:\n%s", got)
	}
}

// TestAppendRefusesToWriteToAConfigThatDoesNotParse.
//
// Appending to a broken file would produce a file that is broken in two places,
// and the second one would look like gluon's fault.
func TestAppendRefusesToWriteToABrokenConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("imports = [\"unterminated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(p)
	if _, err := AppendDatabase(p, Database{Name: "a", Driver: "sqlite", File: "x.db"}, false); err == nil {
		t.Fatal("appended to a config that does not parse")
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Error("the file was modified despite the refusal")
	}
}

// TestAppendRefusesWhenTheResultWouldNotParse.
//
// The edit is checked rather than trusted: decode the result and compare, the
// same stance the differential test that holds invariant 6 takes. A value that
// needed escaping and did not get it would otherwise produce a config that
// silently stops loading on the next run.
func TestAppendRefusesWhenTheResultWouldNotParse(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	// A name carrying a quote and a newline would break a naive renderer.
	entry := Database{Name: "a\"b\nname = \"oops", Driver: "sqlite", File: "x.db"}
	if _, err := AppendDatabase(p, entry, false); err != nil {
		// Refusing is correct.
		if _, statErr := os.Stat(p); statErr == nil {
			got, _ := os.ReadFile(p)
			if len(got) > 0 {
				t.Errorf("nothing should have been written:\n%s", got)
			}
		}
		return
	}
	// If it was accepted, it must genuinely load and round-trip.
	c, err := LoadFile(p)
	if err != nil {
		t.Fatalf("a file was written that does not load: %v", err)
	}
	if len(c.Databases) != 1 || c.Databases[0].Name != entry.Name {
		t.Errorf("the name did not survive quoting: %q", c.Databases[0].Name)
	}
}

// TestStanzaIsWhatGetsWritten.
//
// The wizard shows this text before it writes anything. If the rendered block
// and the written bytes could differ, a confirmation would be promising one
// thing and doing another.
func TestStanzaIsWhatGetsWritten(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	entry := Database{
		Module: "example.com/api", Name: "primary",
		Driver: "postgres", DSNEnv: "DATABASE_URL",
	}
	shown := Stanza(entry)
	written, err := AppendDatabase(p, entry, false)
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != shown {
		t.Errorf("shown:\n%s\nwritten:\n%s", shown, written)
	}
	got, _ := os.ReadFile(p)
	if !strings.Contains(string(got), shown) {
		t.Errorf("the file does not contain what was shown:\n%s", got)
	}
}

// TestStanzaNeverRendersAPassword — the type has no password to render, and
// this is what would notice if one were ever added.
func TestStanzaNeverRendersAPassword(t *testing.T) {
	s := Stanza(Database{
		Name: "a", Driver: "postgres", Host: "h", Port: 5432,
		DBName: "d", User: "u", PassEnv: "PGPASSWORD",
		Password: "this-should-never-appear",
	})
	if strings.Contains(s, "this-should-never-appear") {
		t.Fatalf("Stanza rendered a password:\n%s", s)
	}
	if !strings.Contains(s, "password_env") {
		t.Errorf("Stanza dropped the reference to where the password lives:\n%s", s)
	}
}

// TestAppendedBlocksStayReadable — one blank line between blocks, whatever the
// file ended with. A config that grows a run of blank lines every time gluon
// touches it is a config somebody stops trusting.
func TestAppendedBlocksStayReadable(t *testing.T) {
	for _, ending := range []string{"", "\n", "\n\n", "\n\n\n"} {
		p := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(p, []byte("imports = []"+ending), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := AppendDatabase(p, Database{Name: "a", Driver: "sqlite", File: "x.db"}, false); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(p)
		if strings.Contains(string(got), "\n\n\n\n") {
			t.Errorf("ending %q produced a run of blank lines:\n%q", ending, got)
		}
		if _, err := LoadFile(p); err != nil {
			t.Errorf("ending %q produced a file that does not load: %v", ending, err)
		}
	}
}

// TestSetThemeNameKeepsTheFileItFound is the property the whole line-edit
// design exists for: a config somebody wrote, with their comments and their
// settings, comes back with one line different.
func TestSetThemeNameKeepsTheFileItFound(t *testing.T) {
	const src = `# my gluon config
imports = ["fmt", "strings"]

[theme]
# violet is too much on this monitor
keyword = "#7C7C84"
name = "go" # was terminal until Tuesday

[[database]]
name = "app"
driver = "sqlite"
file = "app.db"
`
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	line, err := SetThemeName(p, "terminal")
	if err != nil {
		t.Fatalf("SetThemeName: %v", err)
	}
	if line != `name = "terminal"` {
		t.Errorf("wrote %q", line)
	}

	got, _ := os.ReadFile(p)
	for _, want := range []string{
		"# my gluon config",
		"# violet is too much on this monitor",
		`keyword = "#7C7C84"`,
		`name = "terminal" # was terminal until Tuesday`,
		`file = "app.db"`,
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("the edit lost %q:\n%s", want, got)
		}
	}

	after, err := LoadFile(p)
	if err != nil {
		t.Fatalf("the result does not load: %v", err)
	}
	if after.ThemeName != "terminal" {
		t.Errorf("ThemeName = %q", after.ThemeName)
	}
	if after.Theme["keyword"] != "#7C7C84" || len(after.Databases) != 1 {
		t.Errorf("the edit changed something else: %+v", after)
	}
}

// The three shapes a config can be in when the choice is made: no file at all,
// a file with no [theme] table, and a [theme] table that sets roles but no
// name. All three end up selecting the theme, and none of them lose a byte.
func TestSetThemeNameInEveryShape(t *testing.T) {
	cases := []struct {
		name string
		src  string
		keep string
	}{
		{"no file", "", ""},
		{"no theme table", "imports = [\"fmt\"]\n", "imports"},
		{"theme table with no name", "[theme]\nerror = \"1\"\n", `error = "1"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "config.toml")
			if tc.src != "" {
				if err := os.WriteFile(p, []byte(tc.src), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := SetThemeName(p, "terminal"); err != nil {
				t.Fatalf("SetThemeName: %v", err)
			}
			after, err := LoadFile(p)
			if err != nil {
				t.Fatalf("the result does not load: %v", err)
			}
			if after.ThemeName != "terminal" {
				t.Errorf("ThemeName = %q", after.ThemeName)
			}
			got, _ := os.ReadFile(p)
			if tc.keep != "" && !strings.Contains(string(got), tc.keep) {
				t.Errorf("the edit lost %q:\n%s", tc.keep, got)
			}
			if strings.Contains(string(got), "\n\n\n") {
				t.Errorf("the edit left a run of blank lines:\n%q", got)
			}
		})
	}
}

// A `name` under some other table is not the theme's, and neither is one in a
// [theme.something] that TOML would read as a different table.
func TestSetThemeNameOnlyTouchesItsOwnTable(t *testing.T) {
	const src = `[[database]]
name = "app"
driver = "sqlite"
file = "app.db"
`
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SetThemeName(p, "go"); err != nil {
		t.Fatalf("SetThemeName: %v", err)
	}
	after, err := LoadFile(p)
	if err != nil {
		t.Fatalf("the result does not load: %v", err)
	}
	if len(after.Databases) != 1 || after.Databases[0].Name != "app" {
		t.Fatalf("the database's own name was rewritten: %+v", after.Databases)
	}
	if after.ThemeName != "go" {
		t.Errorf("ThemeName = %q", after.ThemeName)
	}
}

// A config that does not parse is not a config gluon edits — the same stance
// AppendDatabase takes, for the same reason.
func TestSetThemeNameRefusesABrokenConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("imports = [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SetThemeName(p, "go"); err == nil {
		t.Fatal("SetThemeName rewrote a config that does not parse")
	}
	got, _ := os.ReadFile(p)
	if string(got) != "imports = [\n" {
		t.Errorf("the file was touched anyway:\n%q", got)
	}
}

// setOpt is the option-by-key form the tests below read better with.
func setOpt(t *testing.T, path, key, value string) string {
	t.Helper()
	o, ok := Lookup(key)
	if !ok {
		t.Fatalf("%s is not a setting", key)
	}
	line, err := SetOption(path, o, value)
	if err != nil {
		t.Fatalf("SetOption(%s, %q): %v", key, value, err)
	}
	return line
}

// TestTopLevelKeyDoesNotLandInsideATable is the trap the generalisation opens.
//
// setThemeName appended a new block at the end of the file, which is right for
// a table and catastrophic for a top-level key: everything after the first
// header belongs to a table, so `timeout = "45s"` appended to a file ending in
// [theme] becomes a theme role. changedOneLine would not object — it is a
// one-line insert — and only the parse proof behind it would catch the result,
// by luck rather than by design. topLevelInsert is what puts it above the
// tables instead.
func TestTopLevelKeyDoesNotLandInsideATable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	src := "# my config\n\n# the palette I settled on\n[theme]\nname = \"go\"\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	setOpt(t, path, "timeout", "45s")

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("the result does not load: %v", err)
	}
	if cfg.Timeout != "45s" {
		t.Errorf("timeout is %q, want 45s", cfg.Timeout)
	}
	if cfg.ThemeName != "go" {
		t.Errorf("the theme changed to %q", cfg.ThemeName)
	}
	got := mustReadFile(t, path)
	if i, j := strings.Index(got, "timeout"), strings.Index(got, "[theme]"); i > j {
		t.Errorf("timeout landed after [theme], so it is a theme role:\n%s", got)
	}
	// The comment above [theme] is somebody's note about [theme]; the new key
	// must not be wedged between them.
	if !strings.Contains(got, "# the palette I settled on\n[theme]") {
		t.Errorf("the insert split a comment from the table it describes:\n%s", got)
	}
}

// TestSetOptionInEveryShape is TestSetThemeNameInEveryShape generalised: the
// same cascade has to hold for a table that is not [theme] and for a key that
// is under no table at all.
func TestSetOptionInEveryShape(t *testing.T) {
	cases := []struct {
		name, src, key, value, keep string
	}{
		{"no file", "", "timeout", "45s", ""},
		{"no table, top-level key", "imports = [\"fmt\"]\n", "timeout", "45s", "imports"},
		{"no table, nested key", "imports = [\"fmt\"]\n", "value.form", "tree", "imports"},
		{"table without the key", "[value]\nitems = \"500\"\n", "value.depth", "5", "items = \"500\""},
		{"key with a trailing comment", "[value.form]\ndefault = \"table\" # for now\n",
			"value.form", "tree", "# for now"},
		{"another table before the target", "[plugins]\ndisable = [\"gorm\"]\n\n[value.form]\ndefault = \"table\"\n",
			"value.form", "tree", "disable = [\"gorm\"]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			if tc.src != "" {
				if err := os.WriteFile(path, []byte(tc.src), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			setOpt(t, path, tc.key, tc.value)

			cfg, err := LoadFile(path)
			if err != nil {
				t.Fatalf("the result does not load: %v", err)
			}
			o, _ := Lookup(tc.key)
			probe := &Config{}
			o.Apply(probe, tc.value)
			if want, got := o.Get(probe), o.Get(cfg); got != want {
				t.Errorf("%s reads %q, want %q", tc.key, got, want)
			}
			if tc.keep != "" && !strings.Contains(mustReadFile(t, path), tc.keep) {
				t.Errorf("the edit lost %q:\n%s", tc.keep, mustReadFile(t, path))
			}
		})
	}
}

// TestUnsetRemovesOneLine, and leaves the header: removing an empty table would
// be a two-line edit, and one line is what the byte proof can hold.
func TestUnsetRemovesOneLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	src := "# mine\ntimeout = \"45s\"\n\n[theme]\nname = \"go\"\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	o, _ := Lookup("timeout")
	line, found, err := UnsetOption(path, o)
	if err != nil || !found {
		t.Fatalf("UnsetOption = %q, %v, %v", line, found, err)
	}
	if line != "timeout = \"45s\"" {
		t.Errorf("the line reported was %q", line)
	}
	got := mustReadFile(t, path)
	if strings.Contains(got, "timeout") {
		t.Errorf("timeout survived:\n%s", got)
	}
	for _, keep := range []string{"# mine", "[theme]", "name = \"go\""} {
		if !strings.Contains(got, keep) {
			t.Errorf("the unset lost %q:\n%s", keep, got)
		}
	}
}

// TestUnsetOfAKeyThatIsNotThereWritesNothing. A setting that was never set is
// already at its default, which is not a failure to report.
func TestUnsetOfAKeyThatIsNotThereWritesNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	src := "[theme]\nname = \"go\"\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	o, _ := Lookup("timeout")
	if _, found, err := UnsetOption(path, o); err != nil || found {
		t.Fatalf("UnsetOption reported %v, %v", found, err)
	}
	if got := mustReadFile(t, path); got != src {
		t.Errorf("the file changed:\n%s", got)
	}
}

// TestSetOptionRefusesAnArray: the value column shows what it holds, and the
// refusal says what to do instead. Silently succeeding at nothing, or claiming
// the key does not exist, are the two worse answers.
func TestSetOptionRefusesAnArray(t *testing.T) {
	o, ok := Lookup("imports")
	if !ok {
		t.Fatal("imports is not a setting")
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if _, err := SetOption(path, o, "strings"); err == nil {
		t.Fatal("SetOption wrote an array")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("a refused write created the file anyway")
	}
}

// TestSetOptionRefusesABrokenConfig: gluon does not edit a file it cannot read,
// because the edit would be made against a shape it guessed at.
func TestSetOptionRefusesABrokenConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	src := "timeout = \n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	o, _ := Lookup("timeout")
	if _, err := SetOption(path, o, "45s"); err == nil {
		t.Fatal("SetOption wrote into a config that does not parse")
	}
	if got := mustReadFile(t, path); got != src {
		t.Errorf("the file changed:\n%s", got)
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
