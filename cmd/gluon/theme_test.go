package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/theme"
)

// runTheme drives the subcommand against a temporary config home, so a theme
// the developer happens to have installed cannot change what these see.
func runTheme(t *testing.T, args ...string) (string, error) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cmd := newThemeCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestThemeListNamesBuiltinsAndMarksActive(t *testing.T) {
	out, err := runTheme(t)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"go", "terminal", "[theme]", "name ="} {
		if !strings.Contains(out, want) {
			t.Errorf("`gluon theme` does not mention %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "✓") {
		t.Errorf("no theme was marked active:\n%s", out)
	}
}

func TestThemeShowPrintsEveryRole(t *testing.T) {
	out, err := runTheme(t, "show", "terminal")
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range theme.Roles() {
		if !strings.Contains(out, role) {
			t.Errorf("`gluon theme show` does not print %q", role)
		}
	}
}

func TestThemeShowRefusesAnUnknownName(t *testing.T) {
	if _, err := runTheme(t, "show", "nosuchtheme"); err == nil {
		t.Error("an unknown theme name was shown anyway")
	}
}

const tmFixture = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>name</key><string>Fixture</string>
  <key>settings</key><array>
    <dict><key>settings</key><dict><key>foreground</key><string>#F8F8F2</string></dict></dict>
    <dict><key>scope</key><string>keyword</string>
      <key>settings</key><dict><key>foreground</key><string>#FF79C6</string></dict></dict>
  </array>
</dict></plist>
`

func writeFixture(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "My Theme.tmTheme")
	if err := os.WriteFile(p, []byte(tmFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestThemeImportWritesAFileTheLoaderReads(t *testing.T) {
	src := writeFixture(t)
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)

	cmd := newThemeCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"import", src})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	// The filename is slugified, so a theme downloaded as "My Theme.tmTheme"
	// gets a name someone can type into a config file.
	dest := filepath.Join(home, "gluon", "themes", "my-theme.toml")
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("nothing was written to %s: %v\n%s", dest, err, out.String())
	}
	f, err := theme.LoadFile(dest)
	if err != nil {
		t.Fatalf("the importer wrote a file the loader refuses: %v", err)
	}
	if f.Palette["keyword"] != "#FF79C6" {
		t.Errorf("keyword = %q, want the fixture's", f.Palette["keyword"])
	}
	if !strings.Contains(out.String(), "roles came from") {
		t.Errorf("no provenance report was printed:\n%s", out.String())
	}
}

func TestThemeImportRefusesToOverwrite(t *testing.T) {
	src := writeFixture(t)
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	run := func(args ...string) error {
		cmd := newThemeCmd()
		cmd.SetOut(new(bytes.Buffer))
		cmd.SetErr(new(bytes.Buffer))
		cmd.SetArgs(args)
		return cmd.Execute()
	}
	if err := run("import", src); err != nil {
		t.Fatal(err)
	}
	if err := run("import", src); err == nil {
		t.Error("a second import overwrote the first without --force")
	}
	if err := run("import", "--force", src); err != nil {
		t.Errorf("--force did not overwrite: %v", err)
	}
}

// TestThemeImportPrintWritesNothing: --print is the way to look before
// installing, so it must not install.
func TestThemeImportPrintWritesNothing(t *testing.T) {
	src := writeFixture(t)
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	cmd := newThemeCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"import", "--print", src})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "gluon", "themes")); err == nil {
		t.Error("--print created the themes directory")
	}
	if !strings.Contains(out.String(), "[theme]") {
		t.Errorf("--print did not print a theme file:\n%s", out.String())
	}
}

func TestThemeImportRefusesJunk(t *testing.T) {
	p := filepath.Join(t.TempDir(), "notatheme.txt")
	if err := os.WriteFile(p, []byte("keyword = red\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runTheme(t, "import", p); err == nil {
		t.Error("a file that is neither format was imported anyway")
	}
}

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{
		"My Theme":      "my-theme",
		"Monokai":       "monokai",
		"Dark+ (v2)":    "dark-v2",
		"night__owl":    "night-owl",
		"--leading--":   "leading",
		"Solarized.Sat": "solarized-sat",
	} {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}
