package repl

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// flatten runs a command and returns every message it produced, descending into
// tea.Batch. bubbletea's alt-screen and print messages are unexported, so the
// assertions below match on the type name — which is the only handle there is,
// and a rename would be a bubbletea change worth noticing anyway.
func flatten(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, flatten(c)...)
		}
		return out
	}
	// tea.Sequence's message is an unexported []Cmd, and the modal opens and
	// closes through one — the alt screen has to be entered after a line is
	// printed and left before one is. Reflection because the slice type is
	// unexported while its element type, tea.Cmd, is not; the order of what
	// comes back is the property the tests below assert on.
	if v := reflect.ValueOf(msg); v.Kind() == reflect.Slice &&
		strings.Contains(fmt.Sprintf("%T", msg), "sequenceMsg") {
		var out []tea.Msg
		for i := 0; i < v.Len(); i++ {
			c, ok := v.Index(i).Interface().(tea.Cmd)
			if !ok {
				continue
			}
			out = append(out, flatten(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// indexOfType is where a message of some type first appears in what a command
// produced, or -1.
func indexOfType(msgs []tea.Msg, substr string) int {
	for i, n := range typeNames(msgs) {
		if strings.Contains(strings.ToLower(n), strings.ToLower(substr)) {
			return i
		}
	}
	return -1
}

func typeNames(msgs []tea.Msg) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = fmt.Sprintf("%T", m)
	}
	return out
}

func hasType(msgs []tea.Msg, substr string) bool {
	for _, n := range typeNames(msgs) {
		if strings.Contains(strings.ToLower(n), strings.ToLower(substr)) {
			return true
		}
	}
	return false
}

// printedLines is the text of every tea.Println in msgs.
func printedLines(msgs []tea.Msg) []string {
	var out []string
	for _, m := range msgs {
		if !strings.Contains(fmt.Sprintf("%T", m), "printLineMessage") {
			continue
		}
		v := reflect.ValueOf(m)
		if v.Kind() == reflect.Struct && v.NumField() > 0 {
			f := v.Field(0)
			if f.Kind() == reflect.String {
				out = append(out, f.String())
			}
		}
	}
	return out
}

func demoSpec() ModalSpec {
	return ModalSpec{
		Title:   "[]main.User  3 rows",
		Summary: "[]main.User  3 rows  browsed",
		Headers: []string{"#", "Name", "Age"},
		Rows: [][]string{
			{"0", "Ada", "36"},
			{"1", "Grace", "45"},
			{"2", "Alan", "41"},
		},
	}
}

func openTestModal(t *testing.T) (model, []tea.Msg) {
	t.Helper()
	m := newTestModel(t)
	m.winW, m.winH = 80, 24
	spec := demoSpec()
	next, cmd := m.Update(resultMsg(Result{Modal: &spec}))
	return next.(model), flatten(cmd)
}

// TestModalEntersAltScreen is half the rule the design turns on.
func TestModalEntersAltScreen(t *testing.T) {
	m, msgs := openTestModal(t)
	if m.modal == nil {
		t.Fatal("a Modal result did not open a modal")
	}
	if !hasType(msgs, "altscreen") {
		t.Errorf("opening a modal must enter the alt screen; got %v", typeNames(msgs))
	}
	if view := m.View(); !strings.Contains(view, "3 rows") {
		t.Errorf("modal view does not show its title:\n%s", view)
	}
}

// TestModalPrintsNothingWhileOpen is the other half, and the reason "no alt
// screen, ever" was the rule before: tea.Println is a no-op up there, so a line
// printed while a modal is open is a line the session transcript loses.
func TestModalPrintsNothingWhileOpen(t *testing.T) {
	m, msgs := openTestModal(t)
	if lines := printedLines(msgs); len(lines) != 0 {
		t.Errorf("printed while opening a modal, which the alt screen swallows: %q", lines)
	}
	// Drive some interaction; still nothing may be printed.
	for _, k := range []string{"j", "j", "k"} {
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
		m = next.(model)
		if lines := printedLines(flatten(cmd)); len(lines) != 0 {
			t.Errorf("printed while a modal was open: %q", lines)
		}
	}
}

// TestModalClosesWithOneSummaryLine is what keeps scrollback a complete record:
// the view is gone, but the transcript says it happened.
func TestModalClosesWithOneSummaryLine(t *testing.T) {
	m, _ := openTestModal(t)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = next.(model)
	if m.modal != nil {
		t.Fatal("q did not close the modal")
	}
	msgs := flatten(cmd)
	if !hasType(msgs, "altscreen") {
		t.Errorf("closing must leave the alt screen; got %v", typeNames(msgs))
	}
	lines := printedLines(msgs)
	if len(lines) != 1 {
		t.Fatalf("want exactly one summary line in scrollback, got %d: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "browsed") {
		t.Errorf("summary line = %q, want the spec's Summary", lines[0])
	}
	// And after the alt screen is left, not alongside it. Batched, the two
	// commands run concurrently and the line lands in whichever screen wins
	// the race — which, driven through a real terminal, was the alt screen
	// often enough that closing the view printed nothing at all.
	alt, printed := indexOfType(msgs, "altscreen"), indexOfType(msgs, "printLineMessage")
	if alt > printed {
		t.Errorf("the summary is printed before the alt screen is left: %v", typeNames(msgs))
	}
}

// TestModalOwnsTheKeyboard: a keystroke meant for the view must not also land
// in the prompt behind it.
func TestModalOwnsTheKeyboard(t *testing.T) {
	m, _ := openTestModal(t)
	m = typeStr(m, "hello")
	if got := m.in.Value(); got != "" {
		t.Errorf("input received %q while a modal was open", got)
	}
	if m.modal == nil {
		t.Error("typing closed the modal")
	}
}

// TestModalEscAlsoCloses, because esc is what everyone tries first.
func TestModalEscAlsoCloses(t *testing.T) {
	m, _ := openTestModal(t)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if next.(model).modal != nil {
		t.Error("esc did not close the modal")
	}
}

// TestModalFilterNarrowsRows covers the / path, including that esc returns the
// full set rather than leaving the view stuck on a filtered one.
func TestModalFilterNarrowsRows(t *testing.T) {
	md := newModal(demoSpec(), 80, 24)

	md, open, _ := md.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if !open || !md.filtering {
		t.Fatal("/ did not start a filter")
	}
	for _, r := range "Ada" {
		md, _, _ = md.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := len(md.table.Rows()); got != 1 {
		t.Errorf("filter %q matched %d rows, want 1", md.filter, got)
	}

	md, _, _ = md.update(tea.KeyMsg{Type: tea.KeyEsc})
	if md.filtering {
		t.Error("esc did not leave filter mode")
	}
	if got := len(md.table.Rows()); got != 3 {
		t.Errorf("esc left %d rows, want all 3 back", got)
	}
}

// TestAViewNoteWrapsToItsWidth. :since's note is three sentences long, and
// at eighty columns a note drawn as one line was cut at "rather" by the
// terminal: the reader never saw what — under language means. The note wraps
// to the view's width, every word of it kept, and the table gives up the rows
// it takes.
func TestAViewNoteWrapsToItsWidth(t *testing.T) {
	spec := demoSpec()
	spec.Note = sinceListNote("go1.27")
	md := newModal(spec, 80, 24)
	note := md.footerNote()
	for _, l := range strings.Split(note, "\n") {
		if w := lipgloss.Width(l); w > 80 {
			t.Errorf("a note line is %d cells wide: %q", w, l)
		}
	}
	if got, want := strings.Join(strings.Fields(note), " "), strings.Join(strings.Fields(spec.Note), " "); got != want {
		t.Errorf("the wrapped note lost words:\n got %q\nwant %q", got, want)
	}
	oneLine := newModal(demoSpec(), 80, 24).bodyHeight()
	if lines := strings.Count(note, "\n") + 1; lines < 2 || md.bodyHeight() != oneLine-lines {
		t.Errorf("a %d-line note left the body %d rows; with no note it has %d", lines, md.bodyHeight(), oneLine)
	}
}

// TestModalFilterIsCaseInsensitive: this is a person scanning for a substring,
// not gluon completing an identifier. The case-sensitivity rule that governs
// completion is about not corrupting a typed line, and nothing here is typed
// into the session.
func TestModalFilterIsCaseInsensitive(t *testing.T) {
	md := newModal(demoSpec(), 80, 24)
	md, _, _ = md.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for _, r := range "gRaCe" {
		md, _, _ = md.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := len(md.table.Rows()); got != 1 {
		t.Errorf("case-insensitive filter matched %d rows, want 1", got)
	}
}

// TestAModalWithoutEntriesOpensNothing. Every view but the settings screen is
// of something the session computed — 4,812 rows of a slice, a query result —
// and there is nothing to do to those but look. Enter must stay inert there, or
// a table would swallow a keystroke and answer with nothing.
func TestAModalWithoutEntriesOpensNothing(t *testing.T) {
	md := newModal(demoSpec(), 80, 24)
	md, open, _ := md.update(tea.KeyMsg{Type: tea.KeyEnter})
	if !open {
		t.Fatal("enter closed a view it has nothing to do in")
	}
	if len(md.stack) != 0 || md.run != "" {
		t.Errorf("enter opened %d rows and asked to run %q", len(md.stack), md.run)
	}
	if strings.Contains(md.View(), "enter") {
		t.Errorf("the footer offers a key that does nothing:\n%s", md.View())
	}
}

// TestModalTextPages covers the other shape: no headers means a viewport over
// text, which is what :src and :doc want.
func TestModalTextPages(t *testing.T) {
	spec := ModalSpec{
		Title:   "session source",
		Summary: "session source  browsed",
		Text:    strings.Repeat("line\n", 200),
	}
	md := newModal(spec, 80, 24)
	if md.isTable {
		t.Fatal("a spec with no headers must page text, not build a table")
	}
	if !strings.Contains(md.View(), "scroll") {
		t.Errorf("text modal footer should offer scrolling:\n%s", md.View())
	}
}

// TestColumnsFitTheWindow: a table wider than the terminal wraps into pulp,
// which is worse than a truncated cell.
func TestColumnsFitTheWindow(t *testing.T) {
	spec := ModalSpec{
		Headers: []string{"a", "b", "c"},
		Rows:    [][]string{{strings.Repeat("x", 200), strings.Repeat("y", 200), strings.Repeat("z", 200)}},
	}
	cols := columnsFor(spec, 80)
	total := 0
	for _, c := range cols {
		total += c.Width
	}
	if total > 80 {
		t.Errorf("columns total %d, wider than the 80-column window", total)
	}
}
