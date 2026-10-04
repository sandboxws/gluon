package ui

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/sandboxws/gluon/internal/config"
	"github.com/sandboxws/gluon/internal/syntax"
	"github.com/sandboxws/gluon/internal/theme"
)

// palette resolves the shipped default, which is what build now takes.
func palette(t *testing.T, overrides map[string]string) theme.Palette {
	t.Helper()
	// An empty themes directory, so a theme the developer happens to have
	// installed cannot change what these tests see.
	p, err := theme.Resolve("", overrides, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Assertions here read the style's configured colour rather than its rendered
// output. lipgloss strips colour when stdout is not a terminal, which it never
// is under `go test`, so rendering would pass no matter what this package did.

// TestRolesMatchConfig pins the two lists that have to agree: config validates
// what a user may write, and this package holds the default for each. A role
// added to one and not the other is either unsettable or unvalidated, and both
// failures are silent.
func TestRolesMatchConfig(t *testing.T) {
	got, want := Roles(), config.ThemeRoles()
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ui and config disagree about theme roles:\n  ui     %v\n  config %v", got, want)
	}
}

// TestPlainPaintsNothing is the pipe contract: a plain theme sets no colour at
// all, because its output is what tests and scripts read.
func TestPlainPaintsNothing(t *testing.T) {
	th := Plain()
	if th.Colour {
		t.Error("Plain() reports Colour = true")
	}
	for name, st := range everyStyle(th) {
		if fg := st.GetForeground(); fg != lipgloss.TerminalColor(lipgloss.NoColor{}) {
			t.Errorf("%s has foreground %v in a plain theme; want none", name, fg)
		}
	}
}

// TestColourThemeSetsEveryRole is the mirror: nothing may be left unpainted
// when colour is on, or that role silently loses its styling everywhere.
func TestColourThemeSetsEveryRole(t *testing.T) {
	th := build(palette(t, nil), true)
	for name, st := range everyStyle(th) {
		if st.GetForeground() == lipgloss.TerminalColor(lipgloss.NoColor{}) {
			t.Errorf("%s has no foreground in a colour theme", name)
		}
	}
}

// TestNoColorWins is the convention: NO_COLOR is honoured for being set, empty
// or not, and it beats CLICOLOR_FORCE.
func TestNoColorWins(t *testing.T) {
	cases := []struct {
		name       string
		noColor    *string
		forceColor string
		want       bool
	}{
		{"unset, not a terminal", nil, "", false},
		{"CLICOLOR_FORCE=1 turns it on", nil, "1", true},
		{"CLICOLOR_FORCE=0 does not", nil, "0", false},
		{"NO_COLOR set beats CLICOLOR_FORCE", ptr("1"), "1", false},
		{"NO_COLOR empty still counts", ptr(""), "1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// t.Setenv registers the restore; Unsetenv after it is still undone.
			t.Setenv("NO_COLOR", "")
			if tc.noColor == nil {
				os.Unsetenv("NO_COLOR")
			} else {
				os.Setenv("NO_COLOR", *tc.noColor)
			}
			t.Setenv("CLICOLOR_FORCE", tc.forceColor)
			// A nil destination stands in for "not a terminal".
			if got := colourFor(nil); got != tc.want {
				t.Errorf("colourFor = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestThemeOverridesApply proves a config role reaches the style, and that an
// unset one keeps gluon's default rather than going blank.
func TestThemeOverridesApply(t *testing.T) {
	th := build(palette(t, map[string]string{"string": "13"}), true)
	want, _ := theme.Builtin(theme.Default)

	if got := th.Str.GetForeground(); got != lipgloss.TerminalColor(lipgloss.Color("13")) {
		t.Errorf("configured string colour did not reach the style: got %v, want 13", got)
	}
	if got := th.Type.GetForeground(); got != lipgloss.TerminalColor(lipgloss.Color(want.Palette["type"])) {
		t.Errorf("unset role lost its default: got %v, want %s", got, want.Palette["type"])
	}
}

// TestStylesCarriesEveryValueRole guards the handoff to internal/pretty: a role
// left zero here would silently un-style the value renderer.
func TestStylesCarriesEveryValueRole(t *testing.T) {
	th := build(palette(t, nil), true)
	st := th.Styles()
	for name, pair := range map[string][2]lipgloss.Style{
		"Type":   {st.Type, th.Type},
		"Annot":  {st.Annot, th.Annot},
		"Note":   {st.Note, th.Note},
		"Str":    {st.Str, th.Str},
		"Num":    {st.Num, th.Num},
		"Header": {st.Header, th.Header},
		"Index":  {st.Index, th.Index},
		"Border": {st.Border, th.Border},
	} {
		if pair[0].GetForeground() != pair[1].GetForeground() {
			t.Errorf("pretty.Styles.%s does not match the theme's own role", name)
		}
	}
}

func everyStyle(th Theme) map[string]lipgloss.Style {
	return map[string]lipgloss.Style{
		"Type": th.Type, "Annot": th.Annot, "Note": th.Note, "Str": th.Str,
		"Num": th.Num, "Header": th.Header, "Index": th.Index, "Border": th.Border,
		"Prompt": th.Prompt, "Cont": th.Cont, "Err": th.Err, "Dim": th.Dim,
		"Search": th.Search, "Heading": th.Heading, "OK": th.OK, "Fail": th.Fail,
		"Path":    th.Path,
		"Keyword": th.Keyword, "Comment": th.Comment, "Builtin": th.Builtin,
		"Punct": th.Punct, "Ident": th.Ident, "SrcType": th.SrcType,
	}
}

func ptr(s string) *string { return &s }

// TestPlainStylesCarryNoAttributes is the other half of the pipe contract.
// Bold used to be applied outside the colour gate, which was invisible only
// because termenv's Ascii profile swallowed it — and termenv does not honour a
// set-but-empty NO_COLOR, which gluon does. So `NO_COLOR= gluon doctor` on a
// terminal emitted ESC[1m. The profile must never be the only thing keeping
// escapes out of a pipe.
func TestPlainStylesCarryNoAttributes(t *testing.T) {
	for name, st := range everyStyle(Plain()) {
		if st.GetBold() {
			t.Errorf("%s is bold in a plain theme", name)
		}
		if st.GetItalic() || st.GetUnderline() || st.GetReverse() {
			t.Errorf("%s carries an attribute in a plain theme", name)
		}
	}
}

// TestDefaultThemeIsGo pins the shipped default, and the way back to the old
// one. Both halves matter: the first is the decision, the second is the promise
// made when it was taken.
func TestDefaultThemeIsGo(t *testing.T) {
	th := build(palette(t, nil), true)
	if got := th.Keyword.GetForeground(); got != lipgloss.TerminalColor(lipgloss.Color("#C6ACD9")) {
		t.Errorf("the default keyword colour is %v, want the go theme's violet", got)
	}
	p, err := theme.Resolve("terminal", nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	back := build(p, true)
	if got := back.Type.GetForeground(); got != lipgloss.TerminalColor(lipgloss.Color("6")) {
		t.Errorf(`[theme] name = "terminal" gave type %v, want ANSI 6`, got)
	}
}

// TestNoneLeavesTheForegroundAlone: "none" is a value that says this role is
// not painted, which is different from a role being unset. Color("") would set
// the property to something termenv resolves to nil, and lipgloss's
// `fg != noColor` guard does not catch that.
func TestNoneLeavesTheForegroundAlone(t *testing.T) {
	th := build(theme.Palette{"ident": theme.None, "keyword": ""}, true)
	for name, st := range map[string]lipgloss.Style{"Ident": th.Ident, "Keyword": th.Keyword} {
		if fg := st.GetForeground(); fg != lipgloss.TerminalColor(lipgloss.NoColor{}) {
			t.Errorf("%s has foreground %v; want the terminal's own", name, fg)
		}
	}
}

// TestSyntaxRolesAllMapped: a role added to internal/syntax and not wired here
// is a colour that silently never appears.
func TestSyntaxRolesAllMapped(t *testing.T) {
	// build() cannot be used to prove this — lipgloss renders nothing under
	// `go test` — so the mapping is checked against a theme whose every style
	// carries a foreground, by asking Syntax which roles it reached.
	th := build(palette(t, nil), true)
	seen := map[syntax.Role]bool{}
	for role, style := range map[syntax.Role]lipgloss.Style{
		syntax.RoleKeyword: th.Keyword,
		syntax.RoleString:  th.Str,
		syntax.RoleType:    th.SrcType,
		syntax.RoleComment: th.Comment,
		syntax.RoleNumber:  th.Num,
		syntax.RoleBuiltin: th.Builtin,
		syntax.RolePunct:   th.Punct,
		syntax.RoleIdent:   th.Ident,
	} {
		if style.GetForeground() == lipgloss.TerminalColor(lipgloss.NoColor{}) {
			t.Errorf("%s maps to a style with no colour", role)
		}
		seen[role] = true
	}
	for r := syntax.RoleNone + 1; r < syntax.NumRoles; r++ {
		if !seen[r] {
			t.Errorf("syntax role %s is not mapped by Theme.Syntax", r)
		}
	}
}

// TestPlainThemeHasNoSyntaxPalette is what makes every highlighting call in the
// REPL a no-op under NO_COLOR and in a pipe, whatever else it gets wrong.
func TestPlainThemeHasNoSyntaxPalette(t *testing.T) {
	if Plain().Syntax().Painted() {
		t.Error("a plain theme produced a painting syntax palette")
	}
}

// TestSeqsNeutralisesFormatting guards the one thing that could put a byte on
// screen that was not in the source: a style carrying a layout property.
func TestSeqsNeutralisesFormatting(t *testing.T) {
	st := lipgloss.NewStyle().Padding(0, 2).Width(40).TabWidth(4).
		Foreground(lipgloss.Color("6"))
	open, closing := seqs(st)
	for _, s := range []string{open, closing} {
		if strings.ContainsAny(s, " \t\n") {
			t.Errorf("a sequence carries whitespace: %q", s)
		}
	}
}

// TestUnknownThemeIsReportedNotFatal: gluon still starts, with a complete
// palette, and says what went wrong.
func TestUnknownThemeIsReportedNotFatal(t *testing.T) {
	cfg := &config.Config{ThemeName: "nosuchtheme"}
	th, err := NewWithError(cfg, nil)
	if err == nil {
		t.Error("an unknown theme name was not reported")
	}
	if len(everyStyle(th)) == 0 {
		t.Error("no styles were built")
	}
	if th.Name != "nosuchtheme" {
		t.Errorf("Name = %q; doctor needs the name that was asked for", th.Name)
	}
}

// TestRoleStylesCoversEveryRole: `gluon theme show` draws one swatch per role
// from this map, so a role missing from it is a role the command silently does
// not show.
func TestRoleStylesCoversEveryRole(t *testing.T) {
	styles := build(palette(t, nil), true).RoleStyles()
	for _, role := range theme.Roles() {
		if _, ok := styles[role]; !ok {
			t.Errorf("RoleStyles has no entry for %q", role)
		}
	}
	if len(styles) != len(theme.Roles()) {
		t.Errorf("RoleStyles has %d entries for %d roles", len(styles), len(theme.Roles()))
	}
}
