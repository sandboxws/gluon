package repl

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sandboxws/gluon/internal/config"
)

// keyOf builds the KeyMsg bubbletea would deliver for a named key.
func keyOf(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// settingsModal is the screen `:settings` opens, in a window with room for it.
func settingsModal(t *testing.T, c *Core) modal {
	t.Helper()
	res := c.settingsCmd("")
	if res.Modal == nil {
		t.Fatal(":settings offered no modal")
	}
	return newModal(*res.Modal, 100, 40)
}

// press sends one named key to the view.
func pressModal(md modal, keys ...string) modal {
	for _, k := range keys {
		md, _, _ = md.update(keyOf(k))
	}
	return md
}

// openRow puts the cursor on a named row and opens it.
func openRow(t *testing.T, md modal, key string) modal {
	t.Helper()
	for i, r := range md.table.Rows() {
		if r[0] == key {
			md.table.SetCursor(i)
			md = pressModal(md, "enter")
			if len(md.stack) == 0 {
				t.Fatalf("enter did not open %s", key)
			}
			return md
		}
	}
	t.Fatalf("no row called %q", key)
	return md
}

// TestEveryRowHasAnEntry. Rows and Entries are parallel, and the driver pairs
// them by index — a listing that grew a row without an entry would open the
// wrong setting, or none, with nothing failing anywhere but on screen.
func TestEveryRowHasAnEntry(t *testing.T) {
	c, _ := settingsCore(t, "")
	res := c.settingsCmd("")
	if len(res.Modal.Entries) != len(res.Modal.Rows) {
		t.Fatalf("%d rows and %d entries", len(res.Modal.Rows), len(res.Modal.Entries))
	}
	for i, row := range res.Modal.Rows {
		if got := res.Modal.Entries[i].Title; got != row[0] {
			t.Errorf("row %d is %q and its entry is %q", i, row[0], got)
		}
	}
	for _, e := range res.Modal.Entries {
		if e.Text == "" {
			t.Errorf("%s opens onto nothing", e.Title)
		}
		if e.Choices == nil && e.Prefix == "" && e.Why == "" {
			t.Errorf("%s can neither be chosen from, typed into, nor explain why not", e.Title)
		}
	}
}

// TestAFilteredRowOpensItsOwnSetting.
//
// The cursor indexes the rows you can see and the entries are all the rows
// there are, so a filtered table needs the map between them. This is the bug
// that map exists to prevent, and it is one that fails silently: the wrong
// setting opens, correctly rendered.
func TestAFilteredRowOpensItsOwnSetting(t *testing.T) {
	c, _ := settingsCore(t, "")
	md := settingsModal(t, c)

	md = pressModal(md, "/")
	for _, r := range "theme.name" {
		md = pressModal(md, string(r))
	}
	md = pressModal(md, "enter") // accept the filter
	if got := len(md.table.Rows()); got != 1 {
		t.Fatalf("the filter left %d rows, want only theme.name", got)
	}
	md = pressModal(md, "enter") // open the row
	if len(md.stack) == 0 {
		t.Fatal("enter did not open the filtered row")
	}
	if got := md.stack[0].entry.Title; got != "theme.name" {
		t.Errorf("the filtered row opened %q", got)
	}
}

// TestChoosingAValueSubmitsTheCommand: the view writes nothing. It hands back
// the line `:settings value.form tree` would have been, which is what keeps one
// path deciding what changing a setting means.
func TestChoosingAValueSubmitsTheCommand(t *testing.T) {
	c, path := settingsCore(t, "")
	md := openRow(t, settingsModal(t, c), "value.form")

	var found bool
	for i, ch := range md.stack[0].entry.Choices {
		if ch.Label == "tree" {
			md.stack[0].idx, found = i, true
		}
	}
	if !found {
		t.Fatal("value.form offered no tree")
	}
	md = pressModal(md, "enter")

	if md.run != ":settings value.form tree" {
		t.Errorf("the view asked the driver to run %q", md.run)
	}
	if !md.waiting {
		t.Error("the view is not waiting for an answer, so a second enter would queue another")
	}
	if got := readFile(t, path); got != "" {
		t.Errorf("the view wrote the config itself:\n%s", got)
	}
	// A second enter while the first is unanswered must not queue a change
	// against a table that no longer describes the file.
	md.run = ""
	if md = pressModal(md, "enter"); md.run != "" {
		t.Errorf("a second enter submitted %q while the first was in flight", md.run)
	}
}

// TestTypingAValueSubmitsTheCommand covers the settings a closed set cannot
// describe — a duration, a path, an editor command line. Four settings would
// otherwise be readable on this screen and reachable only from the prompt.
func TestTypingAValueSubmitsTheCommand(t *testing.T) {
	c, _ := settingsCore(t, "timeout = \"20s\"\n")
	md := openRow(t, settingsModal(t, c), "timeout")

	if !md.stack[0].typing() {
		t.Fatal("timeout opened a list, and it has no closed set to list")
	}
	// It opens with what is in force, so an edit is an edit.
	if got := md.stack[0].in.Value(); got != "20s" {
		t.Errorf("the line opened with %q, not the value in force", got)
	}
	md = pressModal(md, "2", "m")
	// The typed value replaces rather than appends: this is a field, and the
	// test drives it the way a person does.
	md.stack[0].in.SetValue("2m")
	md = pressModal(md, "enter")
	if md.run != ":settings timeout 2m" {
		t.Errorf("the view asked the driver to run %q", md.run)
	}
}

// TestQIsALetterWhileTypingAValue. Every other view in gluon closes on q, and
// a value being typed is the one place that would be wrong: an editor command
// or a path can contain one, and losing the screen mid-word would be a keypress
// that discarded work.
func TestQIsALetterWhileTypingAValue(t *testing.T) {
	c, _ := settingsCore(t, "")
	md := openRow(t, settingsModal(t, c), "editor")
	md.stack[0].in.SetValue("")
	md, open, _ := md.update(keyOf("q"))
	if !open {
		t.Fatal("q closed the screen while a value was being typed")
	}
	if got := md.stack[0].in.Value(); got != "q" {
		t.Errorf("the line reads %q, and q was typed into it", got)
	}
}

// TestTheDefaultIsAChoice. Putting a setting back is one of the things it can
// be, so it is in the list with the mark on it when nothing has been chosen —
// rather than a footnote about a `-` the reader has to know to type.
func TestTheDefaultIsAChoice(t *testing.T) {
	c, _ := settingsCore(t, "")
	md := openRow(t, settingsModal(t, c), "value.form")
	e := md.stack[0].entry
	if e.Current != defaultChoice {
		t.Errorf("an unset setting is marked %q", e.Current)
	}
	var run string
	for _, ch := range e.Choices {
		if ch.Label == defaultChoice {
			run = ch.Run
		}
	}
	if want := ":settings value.form " + unsetMark; run != want {
		t.Errorf("the default choice runs %q, want %q", run, want)
	}
}

// TestTheThemeOffersNoDefault: unsetting it is refused, because a palette is
// chosen and never left blank. A choice the command would refuse is worse than
// no choice at all.
func TestTheThemeOffersNoDefault(t *testing.T) {
	c, _ := settingsCore(t, "")
	md := openRow(t, settingsModal(t, c), "theme.name")
	for _, ch := range md.stack[0].entry.Choices {
		if ch.Label == defaultChoice {
			t.Fatal("theme.name offers a default, and :settings theme.name - is refused")
		}
	}
}

// TestARowThatCannotBeSetOpensAnyway. `imports` is an array gluon will not
// rewrite; the explanation says so and says where to edit it. A row that did
// nothing when opened would read as a broken screen rather than as an answer.
func TestARowThatCannotBeSetOpensAnyway(t *testing.T) {
	c, _ := settingsCore(t, "")
	md := openRow(t, settingsModal(t, c), "imports")
	e := md.stack[0].entry
	if e.Why == "" {
		t.Error("imports opened without saying why it cannot be set")
	}
	if len(e.Choices) > 0 || e.Prefix != "" {
		t.Error("imports offered a way to set it, and :settings would refuse")
	}
	md = pressModal(md, "enter")
	if md.run != "" {
		t.Errorf("enter on an unsettable row submitted %q", md.run)
	}
}

// TestARoleIsTwoLevelsDown. theme.<role> is one row and fifteen settings, and a
// screen that could not reach them would list a setting it cannot open.
func TestARoleIsTwoLevelsDown(t *testing.T) {
	c, _ := settingsCore(t, "")
	md := openRow(t, settingsModal(t, c), "theme.<role>")
	md.stack[0].idx = 0
	role := md.stack[0].entry.Choices[0].Label
	md = pressModal(md, "enter")
	if len(md.stack) != 2 {
		t.Fatalf("choosing a role left %d levels open", len(md.stack))
	}
	if want := "theme." + role; md.stack[1].entry.Title != want {
		t.Errorf("the open row is %q, want %q", md.stack[1].entry.Title, want)
	}
	if !md.stack[1].typing() {
		t.Error("a colour is a value to type, and the row offered no line to type it in")
	}
	if !strings.Contains(md.View(), "theme.<role> › theme."+role) {
		t.Errorf("the title does not say where the reader is:\n%s", md.View())
	}

	// esc is one level back, not the whole screen away.
	md = pressModal(md, "esc")
	if len(md.stack) != 1 {
		t.Fatalf("esc left %d levels open, want the roles still listed", len(md.stack))
	}
	md = pressModal(md, "esc")
	if len(md.stack) != 0 {
		t.Fatal("a second esc did not return to the table")
	}
	_, open, _ := md.update(keyOf("esc"))
	if open {
		t.Error("esc at the table did not close the screen")
	}
}

// TestTheOpenRowIsRebuiltAfterAChange.
//
// The row you just changed must not go on saying what it said when you opened
// it — the mark on the old value, "now 20s" under a file that reads 2m. It is
// the one lie a screen like this must not tell, and the only thing that catches
// it is asserting on the view after the answer comes back.
func TestTheOpenRowIsRebuiltAfterAChange(t *testing.T) {
	c, _ := settingsCore(t, "")
	md := openRow(t, settingsModal(t, c), "value.form")
	for i, ch := range md.stack[0].entry.Choices {
		if ch.Label == "tree" {
			md.stack[0].idx = i
		}
	}
	md = pressModal(md, "enter")

	// What the driver does with the line, in the order it does it.
	res := c.Submit(md.run)
	md.run = ""
	fresh := c.Submit(":settings")
	md = md.settled(res, fresh.Modal)

	if md.waiting {
		t.Error("the view is still waiting after it was answered")
	}
	if md.stack[0].entry.Current != "tree" {
		t.Errorf("the mark is on %q", md.stack[0].entry.Current)
	}
	if !strings.Contains(md.stack[0].entry.Text, "tree") {
		t.Errorf("the explanation was not rebuilt:\n%s", md.stack[0].entry.Text)
	}
	if !strings.Contains(md.View(), "value.form is tree") {
		t.Errorf("the footer does not report the change:\n%s", md.View())
	}
	// The row behind it too: the table is what the reader comes back to.
	md = pressModal(md, "esc")
	var value string
	for _, r := range md.table.Rows() {
		if r[0] == "value.form" {
			value = r[2]
		}
	}
	if value != "tree" {
		t.Errorf("the table still reads value.form as %q", value)
	}
}

// TestARefusedChangeIsSaidOnScreen. Nothing may be printed while a modal is up,
// so a refusal that only reached scrollback would be a refusal nobody saw, and
// a screen that silently ignored a keystroke.
func TestARefusedChangeIsSaidOnScreen(t *testing.T) {
	c, _ := settingsCore(t, "")
	md := openRow(t, settingsModal(t, c), "timeout")
	md.stack[0].in.SetValue("1 fortnight")
	md = pressModal(md, "enter")

	res := c.Submit(md.run)
	md.run = ""
	fresh := c.Submit(":settings")
	md = md.settled(res, fresh.Modal)

	if !md.statusErr {
		t.Error("a refusal was recorded as an ordinary answer")
	}
	if !strings.Contains(md.View(), "timeout") || !strings.Contains(md.View(), "fortnight") {
		t.Errorf("the refusal is not on screen:\n%s", md.View())
	}
	if len(md.changed) != 0 {
		t.Errorf("a refused change counted as a change: %v", md.changed)
	}
}

// TestTheClosingLineSaysWhatChanged. Invariant 19 is one line on close, and
// "16 settings browsed" after two of them moved is a transcript that lost the
// only part worth keeping.
func TestTheClosingLineSaysWhatChanged(t *testing.T) {
	c, _ := settingsCore(t, "")
	md := settingsModal(t, c)
	if !strings.Contains(md.summary(), "browsed") {
		t.Errorf("a screen that changed nothing summarises as %q", md.summary())
	}
	for _, key := range []string{"value.form", "timeout"} {
		md.changing = key
		md = md.settled(Result{Out: key + " changed"}, nil)
	}
	// Twice is once: this counts settings, not keystrokes.
	md.changing = "timeout"
	md = md.settled(Result{Out: "timeout changed"}, nil)

	got := md.summary()
	for _, want := range []string{"value.form", "timeout", "changed"} {
		if !strings.Contains(got, want) {
			t.Errorf("the closing line %q does not mention %q", got, want)
		}
	}
	if strings.Count(got, "timeout") != 1 {
		t.Errorf("the closing line counts a setting twice: %q", got)
	}
	if strings.Contains(got, "\n") {
		t.Errorf("the closing line is more than one line: %q", got)
	}
}

// TestSettingsSplitsOnTheFirstSeparator.
//
// `key=value` and `key value` are both accepted, and an editor command line is
// full of `=`. Splitting on the `=` first read `editor code --wait=1` as a
// request to set a setting called "editor code --wait" — a refusal for a line
// that is entirely correct, and now reachable by typing one into the screen.
func TestSettingsSplitsOnTheFirstSeparator(t *testing.T) {
	c, _ := settingsCore(t, "")
	res := c.settingsCmd("editor code --wait=1")
	if res.Err {
		t.Fatalf(":settings editor code --wait=1 errored: %s", res.Out)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Editor != "code --wait=1" {
		t.Errorf("the file reads the editor as %q", cfg.Editor)
	}
}

// TestTheScreenChangesASettingWithoutPrinting is the driver half, and invariant
// 19 at the point it is hardest: the screen submits a line, Core answers it,
// the table is rebuilt — and none of it may reach scrollback, because
// tea.Println is a no-op under the alt screen. What the session transcript gets
// is one line, on close, saying what moved.
func TestTheScreenChangesASettingWithoutPrinting(t *testing.T) {
	c, path := settingsCore(t, "")
	m := newModel(c, "test")
	m.hist = &history{}
	m.winW, m.winH = 100, 40

	next, cmd := m.Update(resultMsg(c.settingsCmd("")))
	m = next.(model)
	if m.modal == nil {
		t.Fatal(":settings did not open the screen")
	}
	if lines := printedLines(flatten(cmd)); len(lines) != 0 {
		t.Fatalf("opening printed %q", lines)
	}

	for i, r := range m.modal.table.Rows() {
		if r[0] == "value.form" {
			m.modal.table.SetCursor(i)
		}
	}
	next, cmd = m.Update(keyOf("enter"))
	m = next.(model)
	if len(m.modal.stack) != 1 {
		t.Fatal("enter did not open the row")
	}
	for i, ch := range m.modal.stack[0].entry.Choices {
		if ch.Label == "tree" {
			m.modal.stack[0].idx = i
		}
	}

	next, cmd = m.Update(keyOf("enter"))
	m = next.(model)
	if !m.busy {
		t.Error("the change did not start, so the view would wait forever")
	}
	msgs := flatten(cmd)
	if lines := printedLines(msgs); len(lines) != 0 {
		t.Fatalf("changing a setting printed %q into the alt screen", lines)
	}
	var edit *modalEditMsg
	for _, msg := range msgs {
		if em, ok := msg.(modalEditMsg); ok {
			edit = &em
		}
	}
	if edit == nil {
		t.Fatalf("no change was submitted; got %v", typeNames(msgs))
	}
	if edit.spec == nil {
		t.Fatal("the change came back without a fresh table, so the screen would go stale")
	}

	next, cmd = m.Update(*edit)
	m = next.(model)
	if m.busy {
		t.Error("the model is still busy after the change landed")
	}
	if lines := printedLines(flatten(cmd)); len(lines) != 0 {
		t.Fatalf("the answer printed %q into the alt screen", lines)
	}
	if src := readFile(t, path); !strings.Contains(src, `default = "tree"`) {
		t.Errorf("the config does not hold the change:\n%s", src)
	}
	if !strings.Contains(m.modal.View(), "value.form is tree") {
		t.Errorf("the screen does not report the change:\n%s", m.modal.View())
	}

	// The row stays open with the mark on the new value, which is what says
	// the change took. Backing out of it is what closes the screen.
	next, _ = m.Update(keyOf("esc"))
	m = next.(model)
	if len(m.modal.stack) != 0 {
		t.Fatal("esc did not return to the table")
	}
	next, cmd = m.Update(keyOf("q"))
	m = next.(model)
	if m.modal != nil {
		t.Fatal("q did not close the screen")
	}
	lines := printedLines(flatten(cmd))
	if len(lines) != 1 {
		t.Fatalf("want exactly one line in scrollback, got %d: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "value.form changed") {
		t.Errorf("the closing line is %q", lines[0])
	}
}

// TestAChangeThatOutlivesTheScreenIsStillReported. Closing the view while its
// change is in flight does not cancel it — the line went to Core like any
// other. Scrollback is owed the sentence the footer would have shown, or the
// session would have a setting that changed and said nothing.
func TestAChangeThatOutlivesTheScreenIsStillReported(t *testing.T) {
	c, _ := settingsCore(t, "")
	m := newModel(c, "test")
	m.hist = &history{}
	m.busy = true

	res := Result{Out: "timeout is 2m · wrote it"}
	next, cmd := m.Update(modalEditMsg{res: res})
	m = next.(model)
	if m.busy {
		t.Error("the model stayed busy after the change landed")
	}
	lines := printedLines(flatten(cmd))
	if len(lines) != 1 || !strings.Contains(lines[0], "timeout is 2m") {
		t.Errorf("scrollback got %q", lines)
	}
}
