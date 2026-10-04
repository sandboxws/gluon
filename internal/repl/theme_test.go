package repl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/config"
)

// tempConfig points the config and theme directories at a temp tree, so a test
// that switches a theme writes there rather than into whatever the person
// running it chose for themselves.
func tempConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	p := filepath.Join(dir, "gluon", "config.toml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if body != "" {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

// :theme lists what can be chosen and marks what is on. The linear form is
// what a pipe and `gluon -e` get, so it has to say everything the picker does.
func TestThemeListsAndMarksTheActiveOne(t *testing.T) {
	tempConfig(t, "[theme]\nname = \"terminal\"\n")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	c := &Core{cfg: cfg}

	res := c.themeCmd("")
	if res.Err {
		t.Fatalf(":theme errored: %s", res.Out)
	}
	if !strings.Contains(res.Out, "✓ terminal") {
		t.Errorf(":theme did not mark the active theme:\n%s", res.Out)
	}
	if !strings.Contains(res.Out, "go") {
		t.Errorf(":theme did not list the default theme:\n%s", res.Out)
	}
	if res.Theme == nil {
		t.Fatal(":theme returned no ThemeSpec, so a terminal could not offer the choice")
	}
	if res.Theme.Active != "terminal" || res.Theme.Apply != "" {
		t.Errorf("spec = %+v, want the active theme and no switch", res.Theme)
	}
	if len(res.Theme.Choices) < 2 {
		t.Errorf("spec offers %d themes, want at least the two built in", len(res.Theme.Choices))
	}
	for _, ch := range res.Theme.Choices {
		if ch.About == "" {
			t.Errorf("%s has no description, so the picker would show a blank line", ch.Name)
		}
	}
}

// Switching writes the choice down. A theme that forgets itself at the next
// prompt is a demonstration, not a setting.
func TestThemeSwitchesAndKeeps(t *testing.T) {
	p := tempConfig(t, "# mine\nimports = [\"fmt\"]\n")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	c := &Core{cfg: cfg}

	res := c.themeCmd("terminal")
	if res.Err {
		t.Fatalf(":theme terminal errored: %s", res.Out)
	}
	if res.Theme == nil || res.Theme.Apply != "terminal" {
		t.Fatalf("spec = %+v, want the driver told to apply it", res.Theme)
	}
	if c.cfg.ThemeName != "terminal" {
		t.Errorf("the session still thinks the theme is %q", c.cfg.ThemeName)
	}

	after, err := config.LoadFile(p)
	if err != nil {
		t.Fatalf("the config gluon wrote does not load: %v", err)
	}
	if after.ThemeName != "terminal" {
		t.Errorf("config selects %q", after.ThemeName)
	}
	if len(after.Imports) != 1 || after.Imports[0] != "fmt" {
		t.Errorf("the edit lost the imports: %+v", after.Imports)
	}
	body, _ := os.ReadFile(p)
	if !strings.Contains(string(body), "# mine") {
		t.Errorf("the edit lost a comment:\n%s", body)
	}
}

// A name that is not a theme is refused before anything is written, and the
// message names what there is instead.
func TestThemeRefusesAnUnknownName(t *testing.T) {
	p := tempConfig(t, "")
	c := &Core{cfg: &config.Config{}}

	res := c.themeCmd("nope")
	if !res.Err {
		t.Fatalf(":theme nope succeeded: %s", res.Out)
	}
	if !strings.Contains(res.Out, "terminal") {
		t.Errorf("the error does not say what there is instead:\n%s", res.Out)
	}
	if res.Theme != nil {
		t.Error("a refused name still told the driver to repaint")
	}
	if _, err := os.Stat(p); err == nil {
		t.Error("a refused name wrote a config file anyway")
	}
}

// The per-role overrides a config sets apply on top of whichever theme is
// chosen, so the driver has to be handed them — and handed a copy, because it
// reads them from its own goroutine.
func TestThemeSpecCarriesTheOverrides(t *testing.T) {
	tempConfig(t, "[theme]\nname = \"go\"\nerror = \"#FF0000\"\n")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	c := &Core{cfg: cfg}

	spec := c.themeSpec("")
	if spec.Overrides["error"] != "#FF0000" {
		t.Fatalf("overrides = %v", spec.Overrides)
	}
	spec.Overrides["error"] = "#00FF00"
	if c.cfg.Theme["error"] != "#FF0000" {
		t.Error("the spec shares the config's map, so a driver could rewrite it")
	}
	if !strings.Contains(c.themeCmd("").Out, "overrides 1 role") {
		t.Error(":theme did not say the config overrides a role on top of the theme")
	}
}
