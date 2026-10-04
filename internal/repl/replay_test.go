package repl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// fixtureHistory points the state directory at a temp one and writes lines into
// the history file, so a search runs against a file this test owns rather than
// against whatever the developer has typed this year.
func fixtureHistory(t *testing.T, lines ...string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "gluon"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := ""
	if len(lines) > 0 {
		body = strings.Join(lines, "\n") + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "gluon", "history"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// noHistory is a state directory with no history file in it at all.
func noHistory(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
}

func TestSearchHistoryKeepsTheFilesOrder(t *testing.T) {
	fixtureHistory(t,
		"x := []int{3, 1, 2}",
		"sort.Ints(x)",
		// A multi-line construct is stored as one recallable entry, so it must
		// come back as one line rather than as fragments.
		"func double(n int) int { return n * 2 }",
		"fmt.Println(x)",
		"y := 1",
	)

	got, err := searchHistory("x")
	if err != nil {
		t.Fatalf("searchHistory: %v", err)
	}
	want := []string{"x := []int{3, 1, 2}", "sort.Ints(x)", "fmt.Println(x)"}
	if len(got) != len(want) {
		t.Fatalf("got %d matches %q, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("match %d = %q, want %q", i, got[i], want[i])
		}
	}

	// The construct comes back whole.
	whole, err := searchHistory("double")
	if err != nil {
		t.Fatalf("searchHistory: %v", err)
	}
	if len(whole) != 1 || whole[0] != "func double(n int) int { return n * 2 }" {
		t.Errorf("the multi-line construct did not come back as one entry: %q", whole)
	}
}

func TestSearchHistoryIsCaseInsensitive(t *testing.T) {
	fixtureHistory(t, "fmt.Println(\"Hello\")")
	got, err := searchHistory("hello")
	if err != nil {
		t.Fatalf("searchHistory: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("case-insensitive search found %d, want 1", len(got))
	}
}

// TestSearchHistorySkipsMetaCommands: history holds them as typed, and a line
// beginning with a colon is not a session entry — replaying :reset would clear
// the session it was being replayed into.
func TestSearchHistorySkipsMetaCommands(t *testing.T) {
	fixtureHistory(t, "x := 1", ":reset", ":hist", ":replay x")
	got, err := searchHistory("x")
	if err != nil {
		t.Fatalf("searchHistory: %v", err)
	}
	if len(got) != 1 || got[0] != "x := 1" {
		t.Errorf("meta commands reached the matches: %q", got)
	}
}

func TestSearchHistoryReportsAnUnreadableFile(t *testing.T) {
	noHistory(t)
	if _, err := searchHistory("x"); err != errNoHistory {
		t.Errorf("searching with no history file: err = %v, want errNoHistory", err)
	}
}

func TestBareReplayReportsUsage(t *testing.T) {
	c := testCore(t)
	res := c.Submit(":replay")
	if !res.Err || !strings.HasPrefix(res.Out, "usage:") {
		t.Errorf("bare :replay: %q (err=%v)", res.Out, res.Err)
	}
	if res := c.Submit(":replay -run"); !res.Err || !strings.HasPrefix(res.Out, "usage:") {
		t.Errorf(":replay -run with no pattern: %q (err=%v)", res.Out, res.Err)
	}
}

// TestReplayNoMatchAndUnavailableAreDifferentAnswers: "there is no such line"
// and "gluon could not look" are different, and only the first is about what
// the user typed.
func TestReplayNoMatchAndUnavailableAreDifferentAnswers(t *testing.T) {
	c := testCore(t)

	fixtureHistory(t, "x := 1")
	before := len(c.sess.Entries)
	nomatch := c.Submit(":replay zzz")
	if !strings.Contains(nomatch.Out, "no history line matches") {
		t.Errorf("a pattern matching nothing: %q", nomatch.Out)
	}
	if len(c.sess.Entries) != before {
		t.Error("a pattern matching nothing still changed the session")
	}

	noHistory(t)
	missing := c.Submit(":replay zzz")
	if !missing.Err || !strings.Contains(missing.Out, "history is unavailable") {
		t.Errorf("with no history file: %q (err=%v)", missing.Out, missing.Err)
	}
	if strings.Contains(missing.Out, "no history line matches") {
		t.Error("an unavailable history is reported as if nothing matched")
	}
	if len(c.sess.Entries) != before {
		t.Error("an unavailable history still changed the session")
	}
}

// TestReplayShowsBeforeItRuns is the guarantee the command is built around: a
// history line can have written something, and the session replays every entry
// on every later line.
func TestReplayShowsBeforeItRuns(t *testing.T) {
	fixtureHistory(t, "a := 1", "b := 2", "c := a + b", "zz := 9")
	c := testCore(t)
	c.Rich = true

	res := c.Submit(":replay a")
	if res.Err {
		t.Fatalf(":replay a: %s", res.Out)
	}
	if len(c.sess.Entries) != 0 {
		t.Fatalf(":replay evaluated %d entries before being confirmed", len(c.sess.Entries))
	}
	if res.Modal == nil {
		t.Fatal(":replay at a terminal did not offer a confirmation view")
	}
	if res.Modal.Confirm == nil || res.Modal.Confirm.Run != ":replay -run a" {
		t.Errorf("the view has no line to confirm with: %+v", res.Modal.Confirm)
	}
	if len(res.Modal.Rows) != 2 {
		t.Errorf("the view shows %d rows, want the 2 matches", len(res.Modal.Rows))
	}
	// Invariant 19: Out carries the same information linearly, so a driver
	// that cannot go full-screen loses nothing.
	for _, want := range []string{"a := 1", "c := a + b"} {
		if !strings.Contains(res.Out, want) {
			t.Errorf("Out does not carry the match %q:\n%s", want, res.Out)
		}
	}
	if strings.Contains(res.Out, "zz := 9") {
		t.Errorf("Out carries a line that did not match:\n%s", res.Out)
	}
}

// TestReplayBringsLinesInInOrder: the file's order is the session's order, and
// c := a + b would not compile in any other.
func TestReplayBringsLinesInInOrder(t *testing.T) {
	fixtureHistory(t, "a := 1", "b := 2", "c := a + b")
	c := testCore(t)

	res := c.Submit(":replay -run := ")
	if res.Err {
		t.Fatalf(":replay -run: %s", res.Out)
	}
	if !strings.Contains(res.Out, "brought in 3 lines") {
		t.Errorf("the line does not say what was brought in: %q", res.Out)
	}
	want := []string{"a := 1", "b := 2", "c := a + b"}
	if len(c.sess.Entries) != len(want) {
		t.Fatalf("session has %d entries, want %d", len(c.sess.Entries), len(want))
	}
	for i, w := range want {
		if c.sess.Entries[i].Src != w {
			t.Errorf("entry %d is %q, want %q", i+1, c.sess.Entries[i].Src, w)
		}
	}
}

// TestReplayUnderThePipeListsAndDoesNotRun: :edit refuses without a terminal,
// but listing carries no risk and is most of the value, so the useful
// degradation here is different.
func TestReplayUnderThePipeListsAndDoesNotRun(t *testing.T) {
	fixtureHistory(t, "a := 1", "b := 2")
	c := testCore(t)
	c.Rich = false

	res := c.Submit(":replay :=")
	if res.Err {
		t.Fatalf(":replay under a pipe: %s", res.Out)
	}
	if res.Modal != nil {
		t.Error("a driver without a terminal was offered a full-screen view")
	}
	if len(c.sess.Entries) != 0 {
		t.Fatalf("the pipe form evaluated %d entries", len(c.sess.Entries))
	}
	for _, want := range []string{"a := 1", "b := 2", "needs a terminal", ":replay -run"} {
		if !strings.Contains(res.Out, want) {
			t.Errorf("the pipe form is missing %q:\n%s", want, res.Out)
		}
	}
}

// TestReplayConfirmationPrintsNothingUntilItCloses is invariant 19 through the
// real driver: nothing reaches scrollback while the view is up, and exactly one
// line does when it goes away — here, what the confirmed line answered.
func TestReplayConfirmationPrintsNothingUntilItCloses(t *testing.T) {
	spec := ModalSpec{
		Title:   `history matching "a"`,
		Summary: `2 lines matched "a" — nothing brought in`,
		Headers: []string{"#", "line"},
		Rows:    [][]string{{"1", "a := 1"}, {"2", "c := a + 1"}},
		Confirm: &ModalConfirm{Label: "bring these in", Run: ":replay -run a"},
	}
	m := newTestModel(t)
	m.winW, m.winH = 80, 24
	next, cmd := m.Update(resultMsg(Result{Modal: &spec}))
	m = next.(model)
	if m.modal == nil {
		t.Fatal("the confirmation did not open a view")
	}
	if lines := printedLines(flatten(cmd)); len(lines) != 0 {
		t.Errorf("printed while opening the view: %q", lines)
	}
	// The footer has to name what enter does, or the decision is taken on
	// trust rather than made.
	if view := m.View(); !strings.Contains(view, "bring these in") {
		t.Errorf("the view does not say what enter does:\n%s", view)
	}

	// Confirming. There is no Core behind this model, so nothing is submitted
	// — what matters here is that the keystroke printed nothing and left the
	// view up.
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if lines := printedLines(flatten(cmd)); len(lines) != 0 {
		t.Errorf("printed while the view was open: %q", lines)
	}
	if m.modal == nil {
		t.Fatal("confirming closed the view before it was answered")
	}

	// What the confirmed line answered becomes the one line scrollback keeps.
	mm := m.modal.settled(Result{Out: "brought in 2 lines from history"}, nil)
	m.modal = &mm
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = next.(model)
	if m.modal != nil {
		t.Fatal("q did not close the view")
	}
	lines := printedLines(flatten(cmd))
	if len(lines) != 1 {
		t.Fatalf("closing left %d lines in scrollback, want exactly 1: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "brought in 2 lines") {
		t.Errorf("the closing line is %q, want what the confirmed line answered", lines[0])
	}
}

// TestClosingAnUnconfirmedReplayLeavesTheSpecsSummary: a view that was only
// read says so, rather than claiming something was brought in.
func TestClosingAnUnconfirmedReplayLeavesTheSpecsSummary(t *testing.T) {
	spec := ModalSpec{
		Title:   `history matching "a"`,
		Summary: `2 lines matched "a" — nothing brought in`,
		Headers: []string{"#", "line"},
		Rows:    [][]string{{"1", "a := 1"}, {"2", "c := a + 1"}},
		Confirm: &ModalConfirm{Label: "bring these in", Run: ":replay -run a"},
	}
	m := newTestModel(t)
	m.winW, m.winH = 80, 24
	next, _ := m.Update(resultMsg(Result{Modal: &spec}))
	m = next.(model)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if next.(model).modal != nil {
		t.Fatal("q did not close the view")
	}
	lines := printedLines(flatten(cmd))
	if len(lines) != 1 || !strings.Contains(lines[0], "nothing brought in") {
		t.Errorf("closing an unconfirmed view left %q, want the spec's summary", lines)
	}
}
