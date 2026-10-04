package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestPalettesAreTheSites: a shot's background is the code well of the page
// it sits on, and its text and cursor are the theme's — read from the site's
// stylesheet, so the two cannot drift.
func TestPalettesAreTheSites(t *testing.T) {
	css, err := os.ReadFile("../../docs/assets/gluon.css")
	if err != nil {
		t.Skip("no stylesheet:", err)
	}
	s := string(css)
	gruv := strings.Index(s, `:root[data-theme="gruv"]`)
	if gruv < 0 {
		t.Fatal("the stylesheet has no gruv palette")
	}
	token := func(block, name string) string {
		m := regexp.MustCompile(`--` + regexp.QuoteMeta(name) + `:\s*(#[0-9A-Fa-f]{6})`).FindStringSubmatch(block)
		if m == nil {
			return ""
		}
		return strings.ToUpper(m[1])
	}
	blocks := map[string]string{"go": s[:gruv], "gruv": s[gruv:]}
	for _, p := range palettes {
		b := blocks[p.Name]
		for tok, want := range map[string]string{
			"well": p.BG, "cs-ident": p.FG, "cs-prompt": p.Cursor,
			"bg": p.Page, "accent": p.Accent, "ink": p.Ink, "ink-3": p.Ink3,
		} {
			if got := token(b, tok); got != strings.ToUpper(want) {
				t.Errorf("palette %s: --%s is %s in the site, %s here", p.Name, tok, got, want)
			}
		}
	}
}
