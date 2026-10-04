package repl

import (
	"strings"
	"testing"
)

// helpModal is the view a bare :help opens, in a window with room for it.
func helpModal(t *testing.T, c *Core, opened string) modal {
	t.Helper()
	spec := c.helpView(opened)
	if spec == nil {
		t.Fatal("no help view")
	}
	return newModal(*spec, 100, 40)
}

// TestHelpViewRowsAndEntriesAreParallel: the driver pairs a row with its entry
// by index, so a row without its own entry would open another command's page.
func TestHelpViewRowsAndEntriesAreParallel(t *testing.T) {
	spec := withPlugins().helpView("")
	if len(spec.Rows) != len(spec.Entries) {
		t.Fatalf("%d rows and %d entries", len(spec.Rows), len(spec.Entries))
	}
	for i, r := range spec.Rows {
		if spec.Entries[i].Title != r[0] {
			t.Errorf("row %d is %s and its entry is %s", i, r[0], spec.Entries[i].Title)
		}
	}
	// The inactive plugins' commands are rows too, marked so.
	found := false
	for _, r := range spec.Rows {
		if r[0] == ":sql" && strings.Contains(r[4], "not active") {
			found = true
		}
	}
	if !found {
		t.Error("an inactive plugin's command is not in the view")
	}
}

// TestHelpViewFiltersOnFlags: the flags are a column, so / finds a command by
// a flag it takes.
func TestHelpViewFiltersOnFlags(t *testing.T) {
	md := pressModal(helpModal(t, withPlugins(), ""), "/", "-json", "enter")
	var names []string
	for _, r := range md.table.Rows() {
		names = append(names, r[0])
	}
	if len(names) == 0 || names[0] != ":query" {
		t.Errorf("/-json matched %v, want :query", names)
	}
}

// TestChoosingAnExampleFillsThePromptAndRunsNothing is the driver half, and
// invariant 19 for a view that hands a line back without submitting it: the
// view closes, the prompt holds the example, nothing is evaluated, and
// scrollback gets exactly one line saying so.
func TestChoosingAnExampleFillsThePromptAndRunsNothing(t *testing.T) {
	// A real session: putting a line on the prompt asks for its completions,
	// which type-check against the session like any keystroke's.
	c := testCore(t)
	m := newModel(c, "test")
	m.hist = &history{}
	m.winW, m.winH = 100, 40

	next, cmd := m.Update(resultMsg(c.help("")))
	m = next.(model)
	if m.modal == nil {
		t.Fatal(":help did not open the view")
	}
	if lines := printedLines(flatten(cmd)); len(lines) != 0 {
		t.Fatalf("opening printed %q", lines)
	}
	for i, r := range m.modal.table.Rows() {
		if r[0] == ":http" {
			m.modal.table.SetCursor(i)
		}
	}
	next, _ = m.Update(keyOf("enter"))
	m = next.(model)
	if len(m.modal.stack) != 1 {
		t.Fatal("enter did not open :http")
	}
	example := m.modal.stack[0].entry.Choices[0]
	if example.Fill == "" {
		t.Fatalf("the first choice is not an example to fill: %+v", example)
	}
	if !strings.Contains(m.modal.View(), "put on the prompt") {
		t.Error("the footer does not say what choosing does")
	}

	next, cmd = m.Update(keyOf("enter"))
	m = next.(model)
	if m.modal != nil {
		t.Fatal("choosing an example left the view open")
	}
	if m.busy {
		t.Error("choosing an example started an evaluation")
	}
	if got := m.in.Value(); got != example.Fill {
		t.Errorf("the prompt holds %q, want %q", got, example.Fill)
	}
	lines := printedLines(flatten(cmd))
	if len(lines) != 1 || !strings.Contains(lines[0], "on the prompt, not run") {
		t.Errorf("want one line saying the example was not run, got %q", lines)
	}
}

// TestAnOpenedViewClosesFromItsRow: a page too long to print opens the view on
// its row, and backing out of that row leaves the view — the reader came in
// through it.
func TestAnOpenedViewClosesFromItsRow(t *testing.T) {
	c := withPlugins()
	res := c.help(":scratch")
	if res.Modal == nil || res.Modal.Opened != ":scratch" {
		t.Fatalf(":help :scratch is long and did not open the view on its row: %+v", res.Modal)
	}
	md := newModal(*res.Modal, 100, 40)
	if len(md.stack) != 1 || md.stack[0].entry.Title != ":scratch" {
		t.Fatal("the view did not open on :scratch")
	}
	if _, open, _ := md.update(keyOf("esc")); open {
		t.Error("esc on the row the view opened on did not close it")
	}
	// A short page prints; it opens nothing.
	if res := c.help(":q"); res.Modal != nil {
		t.Error(":help :q is short and opened a view")
	}
}

// TestSeeAlsoOpensTheOtherCommand, one level down, and esc comes back.
func TestSeeAlsoOpensTheOtherCommand(t *testing.T) {
	md := openRow(t, helpModal(t, withPlugins(), ""), ":http")
	for i, ch := range md.stack[0].entry.Choices {
		if ch.Label == ":env" {
			md.stack[0].idx = i
		}
	}
	md = pressModal(md, "enter")
	if len(md.stack) != 2 || md.stack[1].entry.Title != ":env" {
		t.Fatalf("the neighbour did not open: %d levels", len(md.stack))
	}
	md = pressModal(md, "esc")
	if len(md.stack) != 1 {
		t.Error("esc did not come back to :http")
	}
}

// TestEntryTextPages: an explanation longer than its room scrolls, and the
// footer names the keys only when there is somewhere to go.
func TestEntryTextPages(t *testing.T) {
	md := newModal(*withPlugins().helpView(":grpc"), 100, 24)
	if !strings.Contains(md.View(), "f/b") {
		t.Fatal("a long entry does not say it pages")
	}
	before := md.stack[0].top
	md = pressModal(md, "f")
	if md.stack[0].top <= before {
		t.Error("f did not page forward")
	}
	md = pressModal(md, "b")
	if md.stack[0].top != before {
		t.Error("b did not page back")
	}
}
