package docgen

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/theme"
)

// TestSitePaletteIsGluonsThemes: a transcript on the site depicts gluon's
// terminal, and the screenshot beside it was captured in the theme its palette
// pairs with — go under the default palette, gruvppuccin-mocha under gruv. So
// the site's code colours are those themes, role for role, and a theme that
// moves a colour moves the site's with it or fails here.
func TestSitePaletteIsGluonsThemes(t *testing.T) {
	css, err := os.ReadFile(filepath.Join("..", "..", "docs", "assets", "gluon.css"))
	if err != nil {
		t.Fatal(err)
	}
	blocks := map[string]string{
		"go":                block(t, string(css), ":root{"),
		"gruvppuccin-mocha": block(t, string(css), `:root[data-theme="gruv"]{`),
	}
	tok := regexp.MustCompile(`--cs-([a-z]+):\s*(#[0-9A-Fa-f]{6})`)
	for name, body := range blocks {
		f, ok := theme.Builtin(name)
		if !ok {
			t.Fatalf("no builtin theme %s", name)
		}
		got := map[string]string{}
		for _, m := range tok.FindAllStringSubmatch(body, -1) {
			got[m[1]] = strings.ToUpper(m[2])
		}
		for _, role := range theme.Roles() {
			want, ok := f.Palette[role]
			if !ok {
				continue
			}
			if got[role] != strings.ToUpper(want) {
				t.Errorf("%s: --cs-%s is %q, and the theme's %s is %s", name, role, got[role], role, want)
			}
		}
	}
}

// block is the body of the rule opening with head.
func block(t *testing.T, css, head string) string {
	t.Helper()
	i := strings.Index(css, head)
	if i < 0 {
		t.Fatalf("no %s block in gluon.css", head)
	}
	j := strings.Index(css[i:], "\n  }")
	if j < 0 {
		t.Fatalf("the %s block does not close", head)
	}
	return css[i : i+j]
}
