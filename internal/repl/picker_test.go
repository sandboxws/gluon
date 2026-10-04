package repl

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sandboxws/gluon/internal/ui"
)

// colourfulTheme installs a painted palette and puts back whatever the process
// had when the test is done. Every test here needs one: the picker's whole
// claim is that moving the selection changes what the session looks like, and
// an unpainted theme looks like every other unpainted theme.
func colourfulTheme(t *testing.T, name string) {
	t.Helper()
	th, err := ui.Named(name, nil, true)
	if err != nil {
		t.Fatalf("ui.Named(%q): %v", name, err)
	}
	setTheme(th)
	t.Cleanup(func() { setTheme(ui.New(nil, os.Stdin)) })
}

func testPicker(t *testing.T, active string) model {
	t.Helper()
	colourfulTheme(t, active)
	m := newModel(nil, "test")
	m, _ = m.applyTheme(active, nil)
	spec := ThemeSpec{
		Active: active,
		Choices: []ThemeChoice{
			{Name: "go", About: "gluon's own palette"},
			{Name: "terminal", About: "your terminal's own sixteen colours"},
		},
	}
	m, _ = m.openPicker(spec, nil)
	return m
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// The picker opens on the theme that is on. Anything else and the first thing
// it does is lie about what you are looking at.
func TestThePickerOpensOnTheActiveTheme(t *testing.T) {
	m := testPicker(t, "terminal")
	if m.picker == nil {
		t.Fatal("the picker did not open")
	}
	if got := m.picker.selected(); got != "terminal" {
		t.Errorf("opened on %q, want the active theme", got)
	}
}

// Moving the selection repaints the session. This is the whole design: the
// preview is not a swatch drawn beside the list, it is the palette actually
// installed — so the prompt, the input and the value renderer all follow.
func TestMovingTheSelectionRepaintsTheSession(t *testing.T) {
	m := testPicker(t, "go")
	before := promptStyle.GetForeground()

	m, _ = m.updatePicker(key("j"))
	if got := m.picker.selected(); got != "terminal" {
		t.Fatalf("j moved to %q", got)
	}
	if theme.Name != "terminal" {
		t.Errorf("the installed theme is %q, so nothing was previewed", theme.Name)
	}
	if promptStyle.GetForeground() == before {
		t.Error("the prompt kept its old colour while a different theme was previewed")
	}
	if m.in.Prompt != promptStyle.Render(prompt) {
		t.Error("the input is still drawing the prompt in the theme that was replaced")
	}
	if m.core == nil && theme.Styles().Type.GetForeground() != theme.Type.GetForeground() {
		t.Error("the value renderer's palette drifted from the theme")
	}
}

// esc puts back the theme the picker opened in, whatever was previewed on the
// way. Cancelling has to cancel the previews too, or looking is dangerous.
func TestEscapePutsBackTheThemeItOpenedIn(t *testing.T) {
	m := testPicker(t, "go")
	want := promptStyle.GetForeground()

	m, _ = m.updatePicker(key("j"))
	m, cmd := m.updatePicker(key("esc"))
	if m.picker != nil {
		t.Fatal("esc left the picker open")
	}
	if theme.Name != "go" {
		t.Errorf("the session was left in %q", theme.Name)
	}
	if promptStyle.GetForeground() != want {
		t.Error("the prompt did not go back to the colour it started in")
	}
	if cmd == nil {
		t.Error("closing the alt screen left nothing in scrollback")
	}
}

// Enter closes the view and runs the command, rather than writing the config
// from the UI. One path decides what switching means; this is a shortcut to it.
func TestEnterHandsTheChoiceToTheCommand(t *testing.T) {
	m := testPicker(t, "go")
	m, _ = m.updatePicker(key("j"))
	m, cmd := m.updatePicker(key("enter"))
	if m.picker != nil {
		t.Fatal("enter left the picker open")
	}
	if !m.busy {
		t.Error("enter did not start the evaluation that saves the choice")
	}
	if cmd == nil {
		t.Fatal("enter produced no command")
	}
}

// A view that draws past its own window corrupts the screen it is drawn on.
// The preview is the risk: a line of Go is longer than a narrow pane.
func TestThePickerFitsItsWindow(t *testing.T) {
	sizes := []struct{ w, h int }{{40, 12}, {64, 20}, {80, 24}, {120, 40}, {30, 8}}
	for _, sz := range sizes {
		m := testPicker(t, "go")
		mm, _ := m.picker.update(tea.WindowSizeMsg{Width: sz.w, Height: sz.h})
		view := mm.View()
		for _, line := range strings.Split(view, "\n") {
			if w := lipgloss.Width(line); w > sz.w {
				t.Errorf("%dx%d: a line is %d wide:\n%s", sz.w, sz.h, w, line)
				break
			}
		}
		if n := len(strings.Split(view, "\n")); n > sz.h {
			t.Errorf("%dx%d: the view is %d lines tall", sz.w, sz.h, n)
		}
	}
}

// The preview has to reach every role, or choosing a theme by looking at it
// misses the ones that only appear when something goes wrong.
func TestThePreviewShowsEveryRole(t *testing.T) {
	m := testPicker(t, "go")
	mm, _ := m.picker.update(tea.WindowSizeMsg{Width: 100, Height: 40})
	view := mm.View()
	for _, role := range swatchOrder {
		if !strings.Contains(view, role) {
			t.Errorf("the preview never names %q", role)
		}
	}
	for _, want := range []string{"gluon>", "User", "admin", "error:"} {
		if !strings.Contains(view, want) {
			t.Errorf("the preview is missing %q — it should read as a session", want)
		}
	}
}

// The list marks where the session started, so esc has something to point at.
func TestTheListMarksTheConfiguredTheme(t *testing.T) {
	m := testPicker(t, "terminal")
	mm, _ := m.picker.update(tea.WindowSizeMsg{Width: 80, Height: 24})
	rows := mm.rows(10)
	if len(rows) != 2 {
		t.Fatalf("listed %d themes", len(rows))
	}
	if strings.Contains(rows[0], "✓") || !strings.Contains(rows[1], "✓") {
		t.Errorf("the mark is on the wrong row:\n%s\n%s", rows[0], rows[1])
	}
}

// A list longer than the window still shows the selection, wherever it is.
// The window is computed rather than remembered, so this is the property that
// says the arithmetic is right.
func TestTheListScrollsWithTheSelection(t *testing.T) {
	colourfulTheme(t, "go")
	var choices []ThemeChoice
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"} {
		choices = append(choices, ThemeChoice{Name: n, About: n + " theme"})
	}
	p := newPicker(ThemeSpec{Active: "a", Choices: choices}, 80, 24)

	for i := range choices {
		p.idx = i
		rows := p.rows(5)
		if len(rows) != 5 {
			t.Fatalf("idx %d: %d rows, want 5", i, len(rows))
		}
		found := false
		for _, r := range rows {
			if strings.Contains(r, choices[i].Name+" ") {
				found = true
			}
		}
		if !found {
			t.Errorf("idx %d (%s) is not on screen:\n%s", i, choices[i].Name, strings.Join(rows, "\n"))
		}
	}
}
