package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/pretty"
	"time"
)

func write(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadFull(t *testing.T) {
	p := write(t, `
imports = ["strings", "slices"]
editor = "nvim"
timeout = "45s"

[hosts."example.com/proj"]
imports = ["example.com/proj/internal/trace"]
`)
	c, err := LoadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.Imports, ",") != "strings,slices" {
		t.Errorf("imports = %v", c.Imports)
	}
	if c.EvalTimeout(time.Second) != 45*time.Second {
		t.Errorf("timeout = %v", c.EvalTimeout(time.Second))
	}
	// A host rule adds to the global set rather than replacing it.
	got := c.ImportsFor("example.com/proj")
	if strings.Join(got, ",") != "strings,slices,example.com/proj/internal/trace" {
		t.Errorf("ImportsFor = %v", got)
	}
	if strings.Join(c.ImportsFor("other.com/x"), ",") != "strings,slices" {
		t.Errorf("an unrelated host picked up a rule: %v", c.ImportsFor("other.com/x"))
	}
}

func TestMissingFileIsNotAnError(t *testing.T) {
	c, err := LoadFile(filepath.Join(t.TempDir(), "nothing.toml"))
	if err != nil {
		t.Fatalf("a missing config was an error: %v", err)
	}
	// The zero value has to be a working configuration, because that is how
	// gluon is first run.
	if c.Path != "" || len(c.Imports) != 0 {
		t.Errorf("defaults = %+v", c)
	}
	if c.EvalTimeout(30*time.Second) != 30*time.Second {
		t.Error("an unset timeout did not fall back")
	}
}

func TestMalformedIsReported(t *testing.T) {
	// A setting that silently does nothing is worse than one that says why,
	// so every one of these has to be an error rather than a default.
	cases := map[string]string{
		"not toml":      "imports = [",
		"bad duration":  `timeout = "45 fortnights"`,
		"zero duration": `timeout = "0s"`,
		"unknown key":   `improts = ["strings"]`,
		"unknown table": "[plugin]\ndisable = [\"gorm\"]",
		"empty import":  `imports = [""]`,
		"quoted import": `imports = ["\"strings\""]`,
	}
	for name, body := range cases {
		if _, err := LoadFile(write(t, body)); err == nil {
			t.Errorf("%s: accepted %q", name, body)
		}
	}
}

func TestTildeExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	c, err := LoadFile(write(t, "editor = \"~/bin/ed\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Editor != filepath.Join(home, "bin", "ed") {
		t.Errorf("editor = %q, want %q", c.Editor, filepath.Join(home, "bin", "ed"))
	}
}

func TestDirFollowsXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/somewhere")
	if got := Dir(); got != filepath.Join("/somewhere", "gluon") {
		t.Errorf("Dir = %q", got)
	}
	if got := File(); got != filepath.Join("/somewhere", "gluon", "config.toml") {
		t.Errorf("File = %q", got)
	}
}

// TestUnknownThemeRoleIsAnError follows the same stance as an unknown setting:
// a misspelled role would otherwise just leave that colour at its default,
// which reads as gluon having ignored the config.
func TestUnknownThemeRoleIsAnError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte("[theme]\nstirng = \"2\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFile(p)
	if err == nil {
		t.Fatal("a misspelled theme role was accepted")
	}
	if !strings.Contains(err.Error(), "stirng") {
		t.Errorf("error should name the bad role, got %q", err)
	}
}

// TestKnownThemeRolesLoad is the other half: every role internal/ui knows about
// has to actually parse.
func TestKnownThemeRolesLoad(t *testing.T) {
	var b strings.Builder
	b.WriteString("[theme]\n")
	for _, role := range ThemeRoles() {
		fmt.Fprintf(&b, "%s = \"5\"\n", role)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFile(p)
	if err != nil {
		t.Fatalf("a config setting every known role failed to load: %v", err)
	}
	if len(c.Theme) != len(ThemeRoles()) {
		t.Errorf("got %d roles, want %d", len(c.Theme), len(ThemeRoles()))
	}
}

// TestUnknownValueFormIsAnError is the same stance as an unknown theme role: a
// misspelled form would otherwise leave every value in the default shape, which
// reads as gluon having ignored the config.
//
// Unlike a theme name, which is checked for shape only because a theme is a
// file that may appear or vanish independently of this config, a form is a
// closed set gluon compiles in — so membership is checkable at load.
func TestUnknownValueFormIsAnError(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"the default", "[value.form]\ndefault = \"treee\"\n", "treee"},
		{"a kind", "[value.form]\nlist = \"treee\"\n", "treee"},
		{"an unknown kind", "[value.form]\nscalar = \"tree\"\n", "scalar"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "config.toml")
			if err := os.WriteFile(p, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := LoadFile(p)
			if err == nil {
				t.Fatalf("%q was accepted", tc.body)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the error does not name %q: %v", tc.want, err)
			}
		})
	}
}

// TestDefaultIsLiftedOutOfTheFormTable. `default` names a form rather than a
// kind, so it is lifted out at load exactly as [theme] name is — otherwise the
// validation above would have to know it is not a kind, in a second place.
func TestDefaultIsLiftedOutOfTheFormTable(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	body := "[value.form]\ndefault = \"tree\"\nlist = \"columns\"\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Value.DefaultForm != "tree" {
		t.Errorf("the default form is %q", c.Value.DefaultForm)
	}
	if _, ok := c.Value.Form["default"]; ok {
		t.Error("`default` stayed in the kind table, where it reads as a kind")
	}
	if c.Value.Form["list"] != "columns" {
		t.Errorf("the list override is %q", c.Value.Form["list"])
	}

	opts := c.ValueOptions()
	if opts.Form != pretty.FormTree {
		t.Errorf("ValueOptions gave the default form as %v", opts.Form)
	}
	if opts.Kinds["list"] != pretty.FormColumns {
		t.Errorf("ValueOptions gave the list form as %v", opts.Kinds["list"])
	}
}

// TestNoValueTableMeansTheDefaultForm: the zero Config is the default
// configuration, and that has to include shape.
func TestNoValueTableMeansTheDefaultForm(t *testing.T) {
	if got := (&Config{}).ValueOptions(); got.Form != pretty.FormTable || len(got.Kinds) != 0 {
		t.Errorf("the zero Config asks for %+v", got)
	}
}

// TestAnInputModeThatIsNotAModeIsRefusedAtLoad. The set is closed and compiled
// in, so a misspelling is checkable here — and a prompt that quietly kept
// reading keys the old way is exactly the silent nothing validate exists to
// prevent. The message names both members, because a refusal that does not say
// what would have worked is half an answer.
func TestAnInputModeThatIsNotAModeIsRefusedAtLoad(t *testing.T) {
	_, err := LoadFile(write(t, "[input]\nmode = \"nano\"\n"))
	if err == nil {
		t.Fatal("an unknown input mode was accepted")
	}
	for _, want := range []string{InputEmacs, InputVim} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message does not name %q: %v", want, err)
		}
	}

	// And both real values load, with the accessor answering for an unset one.
	for _, mode := range InputModes() {
		c, err := LoadFile(write(t, "[input]\nmode = \""+mode+"\"\n"))
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if c.InputMode() != mode {
			t.Errorf("InputMode() = %q, want %q", c.InputMode(), mode)
		}
	}
	c, err := LoadFile(write(t, "timeout = \"5s\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.InputMode() != InputEmacs {
		t.Errorf("an unconfigured InputMode() = %q, want %q", c.InputMode(), InputEmacs)
	}
	if (*Config)(nil).InputMode() != InputEmacs {
		t.Error("a nil config does not read as the default")
	}
}
