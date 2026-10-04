package repl

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/cursor"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sandboxws/gluon/internal/config"
	"github.com/sandboxws/gluon/internal/ui"
)

// vimModel is a model with modal editing on and a line already typed, which is
// where every one of these starts.
func vimModel(t *testing.T, line string) model {
	t.Helper()
	m := newTestModel(t)
	m = m.setInputMode(config.InputVim)
	m = typeStr(m, line)
	return m
}

// esc is the escape a human types: its own message, with nothing batched onto
// it.
func esc(m model) model {
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	return next.(model)
}

// TestVimOffLeavesEveryKeyWhereItWas is the promise made to everyone who does
// not turn this on.
func TestVimOffLeavesEveryKeyWhereItWas(t *testing.T) {
	m := newTestModel(t)
	if m.vi.on() {
		t.Fatal("a model built with no config has modal editing on")
	}
	m = typeStr(m, "x := 1")
	// Every one of these means something in normal mode and nothing here.
	m = typeStr(m, "dwip")
	if got := m.in.Value(); got != "x := 1dwip" {
		t.Errorf("value = %q, want the keys typed", got)
	}
	// Escape is unbound at the top level with vim off, so it changes nothing.
	m = esc(m)
	if got := m.in.Value(); got != "x := 1dwip" {
		t.Errorf("escape changed the line to %q", got)
	}
	if m.vi.normal() {
		t.Error("escape entered normal mode with vim off")
	}
}

func TestSwitchingToVimStartsInInsert(t *testing.T) {
	m := newTestModel(t).setInputMode(config.InputVim)
	if !m.vi.on() {
		t.Fatal("the mode was not installed")
	}
	if m.vi.normal() {
		t.Error("switching to vim landed in normal mode — a prompt that stopped accepting characters would be alarming")
	}
	m = typeStr(m, "x := 1")
	if got := m.in.Value(); got != "x := 1" {
		t.Errorf("value = %q, want the keys typed", got)
	}
	// The four alt bindings are off while it is on: a batched escape arrives
	// as Alt on the next key, and textinput would otherwise claim it first.
	for _, b := range []string{
		m.in.KeyMap.WordForward.Keys()[0], m.in.KeyMap.DeleteWordForward.Keys()[0],
	} {
		_ = b
	}
	if m.in.KeyMap.WordForward.Enabled() || m.in.KeyMap.WordBackward.Enabled() ||
		m.in.KeyMap.DeleteWordForward.Enabled() || m.in.KeyMap.DeleteWordBackward.Enabled() {
		t.Error("textinput's alt bindings are still live in vim mode")
	}
}

func TestSwitchingAwayFromVimUnlocksTheKeyboard(t *testing.T) {
	m := vimModel(t, "x := 1")
	m = esc(m)
	if !m.vi.normal() {
		t.Fatal("escape did not enter normal mode")
	}

	m = m.setInputMode(config.InputEmacs)
	if m.vi.on() {
		t.Error("switching away left modal editing on")
	}
	if !m.in.KeyMap.WordForward.Enabled() || !m.in.KeyMap.WordBackward.Enabled() ||
		!m.in.KeyMap.DeleteWordForward.Enabled() || !m.in.KeyMap.DeleteWordBackward.Enabled() {
		t.Error("the alt bindings were not put back")
	}
	if m.in.Cursor.Mode() != cursor.CursorBlink {
		t.Error("the blinking cursor was not put back")
	}
	// And the keys type again.
	m = typeStr(m, "dd")
	if got := m.in.Value(); !strings.Contains(got, "dd") {
		t.Errorf("value = %q, want the keys typed rather than commanded", got)
	}
}

func TestEscEntersNormalModeAndITakesItBack(t *testing.T) {
	m := vimModel(t, "x := 1")
	m = esc(m)
	if !m.vi.normal() {
		t.Fatal("escape did not enter normal mode")
	}
	if m.in.Cursor.Mode() != cursor.CursorStatic {
		t.Error("normal mode did not block the cursor")
	}
	// The cursor steps left: insert's sits after the character just typed.
	if m.in.Position() != 5 {
		t.Errorf("cursor at %d, want 5", m.in.Position())
	}
	if m.in.Value() != "x := 1" {
		t.Errorf("escape changed the line to %q", m.in.Value())
	}

	m = typeStr(m, "i")
	if m.vi.normal() {
		t.Fatal("i did not enter insert mode")
	}
	// i inserts before the cursor, which after escape is on the last character.
	m = typeStr(m, "0")
	if got := m.in.Value(); got != "x := 01" {
		t.Errorf("value = %q, want the 0 typed before the cursor", got)
	}
}

// TestRunesArrivingTogetherAreReadOneAtATime. bubbletea coalesces every
// consecutive rune in one read into a single KeyRunes, so dw typed quickly
// arrives as one message with two runes.
func TestRunesArrivingTogetherAreReadOneAtATime(t *testing.T) {
	m := vimModel(t, "one two")
	m = esc(m)
	m, _ = press(m, tea.KeyRunes) // no runes at all is a no-op, not a panic
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("0dw")})
	m = next.(model)
	if got := m.in.Value(); got != "two" {
		t.Errorf("value = %q, want %q — three runes in one message must be read in order", got, "two")
	}
}

// TestEscAndItsKeyInOneMessageStillEntersNormalMode. A lone escape followed by
// another byte in the same read is delivered as Alt on that key, not as KeyEsc
// then the key. An io.Pipe, ssh and tmux all produce it.
func TestEscAndItsKeyInOneMessageStillEntersNormalMode(t *testing.T) {
	m := vimModel(t, "one two")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("0dw"), Alt: true})
	m = next.(model)
	if !m.vi.normal() {
		t.Fatal("a batched escape did not enter normal mode")
	}
	if got := m.in.Value(); got != "two" {
		t.Errorf("value = %q, want %q — the batched keys must be read as commands", got, "two")
	}
}

func TestEnterSubmitsFromNormalModeAndComesBackInInsert(t *testing.T) {
	m := vimModel(t, "x := 1")
	m = esc(m)
	m, _ = press(m, tea.KeyEnter)
	if m.vi.normal() {
		t.Error("the next prompt is in normal mode — a mode left over from the last line would swallow the next one")
	}
	if !m.vi.on() {
		t.Error("submitting turned modal editing off")
	}
	if m.in.Value() != "" {
		t.Errorf("the line survived the submit: %q", m.in.Value())
	}
	// The rings are cleared on submit, so u on a fresh prompt reaches nothing —
	// precisely the boundary :undo starts at.
	if len(m.vi.undos) != 0 || len(m.vi.redos) != 0 {
		t.Error("the undo rings survived the submit")
	}
}

func TestCtrlCAbandonsTheLineAndComesBackInInsert(t *testing.T) {
	m := vimModel(t, "x := 1")
	m = esc(m)
	m, _ = press(m, tea.KeyCtrlC)
	if m.vi.normal() {
		t.Error("abandoning left the prompt in normal mode")
	}
	if m.in.Value() != "" {
		t.Errorf("the line was not cleared: %q", m.in.Value())
	}
	if len(m.vi.undos) != 0 {
		t.Error("the undo ring survived the abandon")
	}
}

// TestNormalModeUndoLeavesTheSessionAlone names the distinction between u and
// :undo, which share nothing but a name.
func TestNormalModeUndoLeavesTheSessionAlone(t *testing.T) {
	m := vimModel(t, "x := 1")
	m = esc(m)
	m = typeStr(m, "dd")
	if m.in.Value() != "" {
		t.Fatalf("dd left %q", m.in.Value())
	}
	m = typeStr(m, "u")
	if got := m.in.Value(); got != "x := 1" {
		t.Errorf("u = %q, want the line back", got)
	}
	// u on a fresh prompt does nothing at all, and says nothing: printing from
	// a keystroke would put a line in scrollback per keypress.
	m, _ = press(m, tea.KeyCtrlC)
	m = esc(m)
	m = typeStr(m, "u")
	if m.in.Value() != "" {
		t.Errorf("u reached back past the line being typed: %q", m.in.Value())
	}
}

func TestCtrlRIsRedoInNormalModeAndSearchInInsert(t *testing.T) {
	m := vimModel(t, "one two")
	m = esc(m)
	m = typeStr(m, "0dw")
	if got := m.in.Value(); got != "two" {
		t.Fatalf("0dw = %q", got)
	}
	m = typeStr(m, "u")
	if got := m.in.Value(); got != "one two" {
		t.Fatalf("u = %q", got)
	}

	m, _ = press(m, tea.KeyCtrlR)
	if m.searching {
		t.Fatal("ctrl-r opened reverse search from normal mode")
	}
	if got := m.in.Value(); got != "two" {
		t.Errorf("ctrl-r = %q, want the redo", got)
	}

	// In insert mode it is reverse search, exactly as the banner says.
	m = typeStr(m, "i")
	m, _ = press(m, tea.KeyCtrlR)
	if !m.searching {
		t.Error("ctrl-r in insert mode did not open reverse search")
	}
}

func TestKAndJWalkHistoryTheWayUpAndDownDo(t *testing.T) {
	m := newTestModel(t)
	m.hist.add("first := 1")
	m.hist.add("second := 2")
	m = m.setInputMode(config.InputVim)
	m = esc(m)

	m = typeStr(m, "k")
	if got := m.in.Value(); got != "second := 2" {
		t.Errorf("k = %q, want the newest history line", got)
	}
	m = typeStr(m, "k")
	if got := m.in.Value(); got != "first := 1" {
		t.Errorf("kk = %q, want the older line", got)
	}
	m = typeStr(m, "j")
	if got := m.in.Value(); got != "second := 2" {
		t.Errorf("j = %q, want forward through history", got)
	}
	// The cursor is inside the recalled line, never past its end.
	if m.in.Position() > len([]rune(m.in.Value()))-1 {
		t.Errorf("cursor at %d on a %d-rune line", m.in.Position(), len([]rune(m.in.Value())))
	}
	// And the arrows do the same thing.
	m2 := newTestModel(t)
	m2.hist.add("first := 1")
	m2 = m2.setInputMode(config.InputVim)
	m2 = esc(m2)
	m2, _ = press(m2, tea.KeyUp)
	if got := m2.in.Value(); got != "first := 1" {
		t.Errorf("up in normal mode = %q", got)
	}
}

// TestABracketedPasteIsTextEvenInNormalMode: dd arriving from a clipboard is
// two characters somebody wants in the line.
func TestABracketedPasteIsTextEvenInNormalMode(t *testing.T) {
	m := vimModel(t, "x := ")
	m = esc(m)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("dd"), Paste: true})
	m = next.(model)
	if got := m.in.Value(); !strings.Contains(got, "dd") {
		t.Errorf("value = %q, want the pasted characters in the line", got)
	}
}

func TestAMultiLinePasteStillSplitsInNormalMode(t *testing.T) {
	m := vimModel(t, "")
	m = esc(m)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a := 1\nb := 2"), Paste: true})
	m = next.(model)
	// The paste branch owns this: one line lands and the last is left in the
	// input, exactly as it is without modal editing.
	if m.in.Value() == "" && len(m.queue) == 0 && !m.busy {
		t.Error("a multi-line paste in normal mode did nothing")
	}
	if strings.Contains(m.in.Value(), "\n") {
		t.Errorf("the paste collapsed onto one line: %q", m.in.Value())
	}
	// A paste that submits starts a new line, and every new line begins in
	// insert.
	if m.vi.normal() {
		t.Error("the prompt is still in normal mode after a multi-line paste")
	}
}

// TestASingleLinePasteInNormalModeIsUndoable: it is text, and it is a change to
// the line, so u has to be able to take it back.
func TestASingleLinePasteInNormalModeIsUndoable(t *testing.T) {
	m := vimModel(t, "x := ")
	m = esc(m)
	before := m.in.Value()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("42"), Paste: true})
	m = next.(model)
	if got := m.in.Value(); !strings.Contains(got, "42") {
		t.Fatalf("the paste did not reach the line: %q", got)
	}
	if m.in.Position() > len([]rune(m.in.Value()))-1 {
		t.Errorf("the cursor rests past the last character: %d", m.in.Position())
	}
	m = typeStr(m, "u")
	if got := m.in.Value(); got != before {
		t.Errorf("u after a paste = %q, want %q", got, before)
	}
}

// TestTheSettingsScreenIsNotAVimBuffer: a view that is open owns the keyboard.
func TestTheSettingsScreenIsNotAVimBuffer(t *testing.T) {
	m := vimModel(t, "x := 1")
	m = esc(m)
	mm := newModal(ModalSpec{
		Title: "settings", Headers: []string{"key", "value"},
		Rows: [][]string{{"timeout", "30s"}, {"editor", "vi"}},
	}, 80, 24)
	m.modal = &mm

	before := m.in.Value()
	m = typeStr(m, "dd")
	if got := m.in.Value(); got != before {
		t.Errorf("a key meant for the view reached the line: %q", got)
	}
}

func TestNormalModeAsksForNoCompletion(t *testing.T) {
	m := newTestModel(t)
	// suggest returns early with no Core, and the clearing branch is what this
	// is about — so the model gets one, without newModel's theme side effects.
	m.core = testCore(t)
	m = m.setInputMode(config.InputVim)
	m = typeStr(m, "strings.To")
	// Insert mode's ghost and hint are whatever they were; what matters is that
	// normal mode withdraws both.
	m.in.SetSuggestions([]string{"strings.ToUpper"})
	m.hint = "func(s string) string"

	m = esc(m)
	if got := m.in.CurrentSuggestion(); got != "" {
		t.Errorf("a candidate survived into normal mode: %q", got)
	}
	if m.hint != "" {
		t.Errorf("a hint survived into normal mode: %q", m.hint)
	}
}

func TestInsertModeGetsItsGhostBack(t *testing.T) {
	m := newTestModel(t)
	m = m.setInputMode(config.InputVim)
	m = typeStr(m, "x")
	m = esc(m)
	if !m.vi.normal() {
		t.Fatal("escape did not enter normal mode")
	}
	m = typeStr(m, "a")
	if m.vi.normal() {
		t.Fatal("a did not enter insert mode")
	}
	// suggest ran on the way back in: with no Core there is nothing to offer,
	// so what is asserted is that the clearing condition is no longer in force.
	if m.vi.normal() {
		t.Error("still commanding")
	}
	m.in.SetSuggestions([]string{"xyz"})
	m = m.suggest()
	if got := m.in.CurrentSuggestion(); got == "" {
		t.Error("insert mode is still withholding candidates")
	}
}

func TestTheModeIsInThePrompt(t *testing.T) {
	m := vimModel(t, "x := 1")
	if got, _ := m.promptNow(); got != "gluon[i]> " {
		t.Errorf("insert prompt = %q, want the insert marker", got)
	}
	m = esc(m)
	if got, _ := m.promptNow(); got != "gluon[n]> " {
		t.Errorf("normal prompt = %q, want the normal marker", got)
	}
	if got := stripEsc(m.in.Prompt); got != "gluon[n]> " {
		t.Errorf("esc did not redraw the prompt: %q", got)
	}
}

// TestTheModeMarkerNeverMovesTheLine: the two markers are the same width, so
// esc and i change a letter and a colour and never the column the line starts
// in. A prompt that jumped sideways on every mode change would be worse than
// no marker at all.
func TestTheModeMarkerNeverMovesTheLine(t *testing.T) {
	m := vimModel(t, "x := 1")
	insert, _ := m.promptNow()
	normal, _ := esc(m).promptNow()
	if lipgloss.Width(insert) != lipgloss.Width(normal) {
		t.Errorf("insert %q and normal %q are different widths", insert, normal)
	}
	m.pending = []string{"func f() {"}
	cont, _ := m.promptNow()
	if lipgloss.Width(cont) != lipgloss.Width(insert) {
		t.Errorf("continuation %q does not line up under %q", cont, insert)
	}
}

// TestTheModeChangesTheColourOfThePrompt: the letter says it in every terminal,
// and the colour says it again wherever there is one. The two prompts must not
// resolve to the same style, or a palette that forgot `mode` would be saying it
// once.
func TestTheModeChangesTheColourOfThePrompt(t *testing.T) {
	prev := theme
	t.Cleanup(func() { setTheme(prev) })
	t2, _ := ui.Named("go", nil, true)
	setTheme(t2)

	m := vimModel(t, "x := 1")
	_, insert := m.promptNow()
	_, normal := esc(m).promptNow()
	if insert.GetForeground() == normal.GetForeground() {
		t.Errorf("both modes paint the prompt %v", insert.GetForeground())
	}
	if insert.GetBold() != normal.GetBold() {
		t.Error("the mode changed the weight of the prompt as well as its colour")
	}
}

// TestVimOffDrawsThePromptItAlwaysDrew is the promise to everyone who did not
// turn this on: not one byte of the prompt moves.
func TestVimOffDrawsThePromptItAlwaysDrew(t *testing.T) {
	m := newTestModel(t)
	if got, _ := m.promptNow(); got != prompt {
		t.Errorf("prompt = %q, want %q", got, prompt)
	}
	m.pending = []string{"func f() {"}
	if got, _ := m.promptNow(); got != contPrompt {
		t.Errorf("continuation = %q, want %q", got, contPrompt)
	}
}

// TestThePlainThemeStillSaysWhichModeItIsIn: colour is never the only signal.
// The prompt is drawn by textinput itself under a plain theme, so the marker
// arrives with it and needs no help from this package.
func TestThePlainThemeStillSaysWhichModeItIsIn(t *testing.T) {
	prev := theme
	t.Cleanup(func() { setTheme(prev) })
	setTheme(ui.Plain())

	m := esc(vimModel(t, "x := 1"))
	if got := m.in.Prompt; !strings.Contains(got, "[n]") {
		t.Errorf("a plain theme lost the mode: %q", got)
	}
	if got := m.in.Prompt; got != stripEsc(got) {
		t.Errorf("a plain theme emitted escapes: %q", got)
	}
}

// TestTheModeSurvivesAnEmptyLine — dd leaves one, and that is exactly when
// somebody needs to know which mode they are in. The old mark was drawn beside
// the line and had to be kept outside inputView's early returns for this; the
// prompt is drawn whether there is a line or not.
func TestTheModeSurvivesAnEmptyLine(t *testing.T) {
	m := esc(vimModel(t, "x := 1"))
	m = typeStr(m, "dd")
	if got := m.in.Value(); got != "" {
		t.Fatalf("dd left %q", got)
	}
	if got, _ := m.promptNow(); got != "gluon[n]> " {
		t.Errorf("an empty line lost the mode: %q", got)
	}
}

// TestNormalModeAsksForNoHint is a property rather than a layout rule: the hint
// is drawn beside the line and normal mode never wants one, so the two things
// competing for the right of the row never both exist.
func TestNormalModeAsksForNoHint(t *testing.T) {
	m := newTestModel(t)
	m.core = testCore(t)
	m = m.setInputMode(config.InputVim)
	m = typeStr(m, "strings.ToUpper(")
	m.hint = "func(s string) string"
	m = esc(m)
	if o := m.inputOpts(); o.Hint != "" {
		t.Fatalf("normal mode kept a hint: %q", o.Hint)
	}
	if !m.vi.normal() {
		t.Fatal("not in normal mode")
	}
}

// TestTheModeIsNotPartOfTheLine: it never reaches what is submitted, what is
// echoed into scrollback, or the history. The prompt drawn beside a submitted
// line is the plain one, whatever mode it was submitted from.
func TestTheModeIsNotPartOfTheLine(t *testing.T) {
	withPalette(t)
	m := esc(vimModel(t, "x := 1"))
	if got := m.in.Value(); got != "x := 1" {
		t.Errorf("Value() = %q", got)
	}
	if got := stripEsc(echoOf(nil, m.in.Value())); strings.Contains(got, "[n]") {
		t.Errorf("the marker reached scrollback: %q", got)
	}
}

func TestASpaceInReverseSearchIsOneSpace(t *testing.T) {
	m := newTestModel(t)
	m, _ = press(m, tea.KeyCtrlR)
	if !m.searching {
		t.Fatal("ctrl-r did not open reverse search")
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = next.(model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	m = next.(model)
	if m.query != "x " {
		t.Errorf("query = %q, want %q — KeySpace already carries its rune", m.query, "x ")
	}
}

// TestCtrlDQuitsOnAnEmptyLineAndIsSilentOtherwise. It passes through only where
// it means what gluon means by it: on a line with something in it the control
// switch does not return, so it would fall to textinput and delete a character
// the undo ring never saw.
func TestCtrlDQuitsOnAnEmptyLineAndIsSilentOtherwise(t *testing.T) {
	m := vimModel(t, "x := 1")
	m = esc(m)
	before := m.in.Value()
	m, cmd := press(m, tea.KeyCtrlD)
	if got := m.in.Value(); got != before {
		t.Errorf("ctrl-d changed the line to %q", got)
	}
	if cmd != nil {
		t.Error("ctrl-d quit on a line with something in it")
	}

	m, _ = press(m, tea.KeyCtrlC)
	m = esc(m)
	if _, cmd := press(m, tea.KeyCtrlD); cmd == nil {
		t.Error("ctrl-d on an empty line did not quit")
	}
}
