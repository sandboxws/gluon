package repl

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sandboxws/gluon/internal/config"
	"github.com/sandboxws/gluon/internal/pretty"
)

// settingsCore is a Core with a config of its own and no evaluator, which is
// all :settings needs: it reads a file and writes a file, and never builds a
// program.
func settingsCore(t *testing.T, body string) (*Core, string) {
	t.Helper()
	path := tempConfig(t, body)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return &Core{cfg: cfg, Values: cfg.ValueOptions()}, path
}

// TestSettingsListsEverySetting. A setting the command does not mention looks
// like a setting gluon does not have, which is the silent omission the command
// registry's own doc comment is about.
func TestSettingsListsEverySetting(t *testing.T) {
	c, _ := settingsCore(t, "")
	res := c.settingsCmd("")
	if res.Err {
		t.Fatalf(":settings errored: %s", res.Out)
	}
	for _, o := range config.Options() {
		if !strings.Contains(res.Out, o.Key) {
			t.Errorf(":settings does not list %q", o.Key)
		}
	}
	if res.Modal == nil {
		t.Fatal(":settings offered no modal, so a narrow terminal gets a wrapped table")
	}
	// Invariant 19: the two say the same thing, because one row builder makes
	// both. Asserted rather than assumed, since that is the property a second
	// row builder would quietly break.
	if len(res.Modal.Rows) != len(config.Options()) {
		t.Errorf("the modal holds %d rows and there are %d settings",
			len(res.Modal.Rows), len(config.Options()))
	}
	for _, row := range res.Modal.Rows {
		if !strings.Contains(res.Out, row[0]) {
			t.Errorf("the modal holds %q and the linear form does not", row[0])
		}
	}
}

// TestSettingsMarksWhereAValueCameFrom: "it is 20s" and "you set it to 20s" are
// different facts, and a reader who cannot tell them apart cannot tell whether
// their config is being read at all.
func TestSettingsMarksWhereAValueCameFrom(t *testing.T) {
	c, _ := settingsCore(t, "timeout = \"20s\"\n")
	res := c.settingsCmd("")
	var timeout, editor string
	for _, row := range res.Modal.Rows {
		switch row[0] {
		case "timeout":
			timeout = row[5]
		case "editor":
			editor = row[5]
		}
	}
	if timeout != "config.toml" {
		t.Errorf("a configured setting reads as %q", timeout)
	}
	if editor != "default" {
		t.Errorf("an unset setting reads as %q", editor)
	}
}

// TestSettingsExplainsOneSetting. The single-key form is what the table cannot
// hold: the allowed values in full, and what the setting is actually for.
func TestSettingsExplainsOneSetting(t *testing.T) {
	c, _ := settingsCore(t, "")
	res := c.settingsCmd("value.form")
	if res.Err {
		t.Fatalf(":settings value.form errored: %s", res.Out)
	}
	for _, want := range []string{"value.form", "table", "tree", "columns", "literal", "default"} {
		if !strings.Contains(res.Out, want) {
			t.Errorf("the explanation does not mention %q:\n%s", want, res.Out)
		}
	}
}

// TestSettingsSetsAndKeeps, and keeps the file it found: the whole reason the
// writer is a line edit is that somebody's comments are in there.
func TestSettingsSetsAndKeeps(t *testing.T) {
	c, path := settingsCore(t, "# mine\ntimeout = \"20s\" # slow machine\n")
	res := c.settingsCmd("value.form tree")
	if res.Err {
		t.Fatalf(":settings value.form tree errored: %s", res.Out)
	}
	if res.Values == nil {
		t.Fatal("a form change told the driver nothing, so the session would not repaint")
	}
	if res.Values.Options.Form != pretty.FormTree {
		t.Errorf("the driver was told to install %v", res.Values.Options.Form)
	}

	src := readFile(t, path)
	for _, keep := range []string{"# mine", "# slow machine", "timeout = \"20s\""} {
		if !strings.Contains(src, keep) {
			t.Errorf("the write lost %q:\n%s", keep, src)
		}
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("the result does not load: %v", err)
	}
	if cfg.Value.DefaultForm != "tree" {
		t.Errorf("the file reads value.form as %q", cfg.Value.DefaultForm)
	}
}

// TestSettingsAcceptsBothSpellings: `key value` is what :theme taught, and
// `key=value` is what the help column has room to show.
func TestSettingsAcceptsBothSpellings(t *testing.T) {
	for _, arg := range []string{"value.form tree", "value.form=tree"} {
		c, _ := settingsCore(t, "")
		if res := c.settingsCmd(arg); res.Err {
			t.Errorf(":settings %s errored: %s", arg, res.Out)
		}
		cfg, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Value.DefaultForm != "tree" {
			t.Errorf(":settings %s did not write the value", arg)
		}
	}
}

// TestSettingsRefusesAnUnknownKeyAndAnUnknownValue, and writes nothing when it
// does. A refusal that half-applied would be worse than no command.
func TestSettingsRefusesAnUnknownKeyAndAnUnknownValue(t *testing.T) {
	cases := []struct{ name, arg, want string }{
		{"unknown key", "nope tree", "unknown setting"},
		{"unknown value", "value.form treee", "value.form"},
		{"bad duration", "timeout 1 fortnight", "timeout"},
		{"an array", "imports strings", "not settable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, path := settingsCore(t, "")
			res := c.settingsCmd(tc.arg)
			if !res.Err {
				t.Fatalf(":settings %s succeeded: %s", tc.arg, res.Out)
			}
			if !strings.Contains(res.Out, tc.want) {
				t.Errorf("the refusal does not mention %q: %s", tc.want, res.Out)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("a refused :settings wrote to the config anyway:\n%s", readFile(t, path))
			}
		})
	}
}

// TestSettingsPutsASettingBack. `-` rather than an empty argument, because a
// bare `:settings <key>` is the request to explain one.
func TestSettingsPutsASettingBack(t *testing.T) {
	c, path := settingsCore(t, "# mine\ntimeout = \"20s\"\n")
	res := c.settingsCmd("timeout -")
	if res.Err {
		t.Fatalf(":settings timeout - errored: %s", res.Out)
	}
	src := readFile(t, path)
	if strings.Contains(src, "timeout") {
		t.Errorf("timeout survived the unset:\n%s", src)
	}
	if !strings.Contains(src, "# mine") {
		t.Errorf("the unset lost a comment:\n%s", src)
	}
	// Twice is not an error: a setting that was never set is already at its
	// default, and reporting that as a failure would be gluon inventing one.
	if res := c.settingsCmd("timeout -"); res.Err {
		t.Errorf("unsetting twice errored: %s", res.Out)
	}
}

// TestSettingsThemeNameGoesThroughThemeSet. One path decides what switching a
// palette means — validate, save, repaint, report the line — which is the same
// argument the picker makes about handing the choice back to the command.
func TestSettingsThemeNameGoesThroughThemeSet(t *testing.T) {
	c, _ := settingsCore(t, "")
	res := c.settingsCmd("theme.name terminal")
	if res.Err {
		t.Fatalf(":settings theme.name errored: %s", res.Out)
	}
	if res.Theme == nil || res.Theme.Apply != "terminal" {
		t.Fatalf("the driver was not told to repaint: %+v", res.Theme)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ThemeName != "terminal" {
		t.Errorf("the file reads the theme as %q", cfg.ThemeName)
	}
}

// TestSettingsAThemeRoleDoesNotOpenThePicker.
//
// tui.go reads an empty ThemeSpec.Apply as "the user wants to choose" and opens
// the picker over the session. A role change is not that request: the palette
// stays and one colour in it moves, so Apply has to name the theme already on.
func TestSettingsAThemeRoleDoesNotOpenThePicker(t *testing.T) {
	c, _ := settingsCore(t, "[theme]\nname = \"terminal\"\n")
	res := c.settingsCmd("theme.keyword #ff0000")
	if res.Err {
		t.Fatalf(":settings theme.keyword errored: %s", res.Out)
	}
	if res.Theme == nil {
		t.Fatal("a role change told the driver nothing, so nothing would repaint")
	}
	if res.Theme.Apply == "" {
		t.Fatal("a role change left Apply empty, which opens the picker")
	}
	if res.Theme.Overrides["keyword"] != "#ff0000" {
		t.Errorf("the spec carries %q for keyword", res.Theme.Overrides["keyword"])
	}
}

// TestTheFormIsInstalledOnlyByADriver.
//
// A Core built from a config that names a form still renders with pretty.Plain,
// because Render is set by a driver and nothing else. That is what keeps a form
// out of a pipe, `gluon -e` and the -json envelopes, which are frozen surfaces
// — and it is the thing a reader will most reasonably assume is broken.
func TestTheFormIsInstalledOnlyByADriver(t *testing.T) {
	// Through the real constructor, not a hand-built Core: the claim is about
	// what production builds when the config names a form, and a helper that
	// set Render itself would be testing the helper.
	tempConfig(t, "[value.form]\ndefault = \"tree\"\n")
	c := testCore(t)
	if c.Values.Form != pretty.FormTree {
		t.Errorf("the configured form did not reach Core.Values: %v", c.Values.Form)
	}
	if c.Rich {
		t.Error("a Core with no driver reported itself rich")
	}
	vals := []pretty.Value{{Type: "[]int", Kind: "list", Items: []pretty.Value{
		{Type: "int", Kind: "scalar", Repr: "1"}}}}
	got := c.Render(vals)
	if want := pretty.Plain(vals); got != want {
		t.Errorf("a configured form reached the plain renderer\n got %q\nwant %q", got, want)
	}
	if strings.ContainsRune(got, 0x1b) {
		t.Errorf("the plain renderer emitted escapes: %q", got)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatal(err)
	}
	return string(b)
}

// TestSwitchingThemeKeepsTheForm is invariant 32's new half, and it is the
// likeliest bug in this whole feature.
//
// Core.Render holds a copy of the palette and now a copy of the form too.
// applyTheme rebuilds that closure, so a version of it that did not carry the
// form forward would silently put every value back in a bordered table the
// first time somebody changed colour — with nothing failing anywhere.
//
// It asserts the rendering and not just the field, because a stale closure
// leaves the field right and the output wrong, which is the half that matters.
func TestSwitchingThemeKeepsTheForm(t *testing.T) {
	c := testCore(t)
	m := newModel(c, "test")
	colourfulTheme(t, "terminal")

	m = m.applyValues(pretty.Options{Form: pretty.FormLine})
	before := renderProbe(m.core)
	if strings.Contains(before, "╭") {
		t.Fatalf("the line form drew a table: %q", before)
	}

	m, err := m.applyTheme("go", nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.core.Values.Form != pretty.FormLine {
		t.Errorf("a theme change put the form back to %v", m.core.Values.Form)
	}
	if after := renderProbe(m.core); strings.Contains(after, "╭") {
		t.Errorf("a theme change left a stale renderer, which drew a table: %q", after)
	}
}

// TestSwitchingTheFormKeepsTheTheme is the same argument the other way: a form
// change goes through the same constructor, so the palette in force survives
// it.
//
// Asserted on the styles rather than on escape sequences, the way the picker's
// tests are: lipgloss's global renderer degrades on a non-TTY, so under
// `go test` a painted palette renders unpainted and an escape-counting test
// would pass for the wrong reason.
func TestSwitchingTheFormKeepsTheTheme(t *testing.T) {
	c := testCore(t)
	m := newModel(c, "test")
	colourfulTheme(t, "terminal")
	m, err := m.applyTheme("terminal", map[string]string{"type": "#ff0000"})
	if err != nil {
		t.Fatal(err)
	}
	want := m.core.Styles.Type.GetForeground()
	if want == theme.Styles().Type.GetForeground() && want == nil {
		t.Fatal("the palette carried no colour to begin with")
	}

	m = m.applyValues(pretty.Options{Form: pretty.FormLine})
	if got := m.core.Styles.Type.GetForeground(); got != want {
		t.Errorf("a form change dropped the palette: type is %v, want %v", got, want)
	}
	if got := theme.Styles().Type.GetForeground(); m.core.Styles.Type.GetForeground() != got {
		t.Error("the value renderer's palette drifted from the theme")
	}
}

// renderProbe draws one list through whatever the model installed.
func renderProbe(c *Core) string {
	return c.Render([]pretty.Value{{Type: "[]int", Kind: "list", Items: []pretty.Value{
		{Type: "int", Kind: "scalar", Repr: "1"}}}})
}

// TestTheSettingsModalOpensAndClosesWithOneSummaryLine is invariant 19 for the
// settings view: nothing is printed while it is up, and exactly one line is
// left behind when it closes.
//
// At the model level rather than through a real program, following
// TestModalClosesWithOneSummaryLine: tea.Println is batched with
// tea.ExitAltScreen, so which reaches a captured byte stream first is not a
// property worth pinning — that the command asked for both is.
func TestTheSettingsModalOpensAndClosesWithOneSummaryLine(t *testing.T) {
	c, _ := settingsCore(t, "")
	res := c.settingsCmd("")
	if res.Modal == nil {
		t.Fatal(":settings offered no modal")
	}

	m := newTestModel(t)
	m.winW, m.winH = 80, 24
	next, cmd := m.Update(resultMsg(res))
	m = next.(model)
	if m.modal == nil {
		t.Fatal("the driver did not open the view")
	}
	opening := flatten(cmd)
	if !hasType(opening, "altscreen") {
		t.Errorf("opening did not enter the alt screen; got %v", typeNames(opening))
	}
	if lines := printedLines(opening); len(lines) != 0 {
		t.Errorf("opening printed %q — the linear form must not also reach scrollback", lines)
	}

	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = next.(model)
	if m.modal != nil {
		t.Fatal("q did not close the view")
	}
	closing := flatten(cmd)
	if !hasType(closing, "altscreen") {
		t.Errorf("closing must leave the alt screen; got %v", typeNames(closing))
	}
	lines := printedLines(closing)
	if len(lines) != 1 {
		t.Fatalf("want exactly one summary line, got %d: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "browsed") {
		t.Errorf("the summary line is %q", lines[0])
	}
}

// TestTheInputModeReachesTheRunningSession: the setting is Live, so the next
// keystroke reads the new way — with no restart, and without the driver having
// to notice on its own.
func TestTheInputModeReachesTheRunningSession(t *testing.T) {
	c, _ := settingsCore(t, "")
	res := c.settingsCmd("input.mode vim")
	if res.Err {
		t.Fatalf(":settings input.mode vim: %s", res.Out)
	}
	if res.Input == nil {
		t.Fatal("the change did not reach the driver")
	}
	if res.Input.Mode != config.InputVim {
		t.Errorf("Input.Mode = %q, want %q", res.Input.Mode, config.InputVim)
	}

	// Putting it back reads as emacs rather than as the empty string, which is
	// why applyLive goes through the accessor and not the raw value.
	res = c.settingsCmd("input.mode -")
	if res.Err {
		t.Fatalf(":settings input.mode -: %s", res.Out)
	}
	if res.Input == nil || res.Input.Mode != config.InputEmacs {
		t.Errorf("unsetting gave %+v, want %q", res.Input, config.InputEmacs)
	}
}
