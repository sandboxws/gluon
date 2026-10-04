package repl

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sandboxws/gluon/internal/session"
)

// key builds the KeyMsg bubbletea would deliver for typed text.
func typeStr(m model, s string) model {
	for _, r := range s {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(model)
	}
	return m
}

func press(m model, t tea.KeyType) (model, tea.Cmd) {
	next, cmd := m.Update(tea.KeyMsg{Type: t})
	return next.(model), cmd
}

func newTestModel(t *testing.T) model {
	t.Helper()
	m := newModel(nil, "test")
	// Keep the test off the user's real history file.
	m.hist = &history{}
	return m
}

func TestMultiLineAccumulates(t *testing.T) {
	m := newTestModel(t)
	m = typeStr(m, "func f() int {")
	m, _ = press(m, tea.KeyEnter)

	if len(m.pending) != 1 {
		t.Fatalf("pending = %v, want the unclosed line held", m.pending)
	}
	if !strings.Contains(m.in.Prompt, strings.TrimSpace(contPrompt)) {
		t.Errorf("prompt = %q, want the continuation prompt", m.in.Prompt)
	}

	m = typeStr(m, "return 1")
	m, _ = press(m, tea.KeyEnter)
	if len(m.pending) != 2 {
		t.Fatalf("pending = %v, want two lines held", m.pending)
	}

	// Closing the brace completes the construct, which clears pending.
	m = typeStr(m, "}")
	m, _ = press(m, tea.KeyEnter)
	if len(m.pending) != 0 {
		t.Errorf("pending = %v, want cleared once complete", m.pending)
	}
	if !m.busy {
		t.Error("busy = false, want the completed construct to be evaluating")
	}
}

// An empty line must not submit an unclosed construct, or a typo is inescapable.
func TestEmptyLineDoesNotEscapeUnclosedConstruct(t *testing.T) {
	m := newTestModel(t)
	m = typeStr(m, "for i := range 3 {")
	m, _ = press(m, tea.KeyEnter)
	m, _ = press(m, tea.KeyEnter) // empty line
	if len(m.pending) != 2 || m.busy {
		t.Errorf("pending=%v busy=%v, want still accumulating", m.pending, m.busy)
	}
}

func TestCtrlCAbandonsPendingButNotSession(t *testing.T) {
	m := newTestModel(t)
	m = typeStr(m, "func f() {")
	m, _ = press(m, tea.KeyEnter)
	m, cmd := press(m, tea.KeyCtrlC)

	if len(m.pending) != 0 {
		t.Errorf("pending = %v, want abandoned", m.pending)
	}
	if cmd != nil {
		t.Error("ctrl-c with a pending construct must not quit")
	}
}

func TestHistoryNavigation(t *testing.T) {
	m := newTestModel(t)
	m.hist.add("first")
	m.hist.add("second")

	m, _ = press(m, tea.KeyUp)
	if got := m.in.Value(); got != "second" {
		t.Errorf("first Up = %q, want %q", got, "second")
	}
	m, _ = press(m, tea.KeyUp)
	if got := m.in.Value(); got != "first" {
		t.Errorf("second Up = %q, want %q", got, "first")
	}
	m, _ = press(m, tea.KeyDown)
	if got := m.in.Value(); got != "second" {
		t.Errorf("Down = %q, want %q", got, "second")
	}
}

// Down past the newest entry restores whatever was being typed.
func TestHistoryRestoresDraft(t *testing.T) {
	m := newTestModel(t)
	m.hist.add("old")
	m = typeStr(m, "draft")

	m, _ = press(m, tea.KeyUp)
	if m.in.Value() != "old" {
		t.Fatalf("Up = %q", m.in.Value())
	}
	m, _ = press(m, tea.KeyDown)
	if got := m.in.Value(); got != "draft" {
		t.Errorf("Down = %q, want the draft %q restored", got, "draft")
	}
}

func TestReverseSearch(t *testing.T) {
	m := newTestModel(t)
	m.hist.add("x := 1")
	m.hist.add("slices.Sort(x)")
	m.hist.add("y := 2")

	m, _ = press(m, tea.KeyCtrlR)
	if !m.searching {
		t.Fatal("ctrl-r did not enter search")
	}
	m = typeStr(m, "sort")
	if m.match != "slices.Sort(x)" {
		t.Errorf("match = %q, want the case-insensitive hit", m.match)
	}
	m, _ = press(m, tea.KeyEnter)
	if m.searching {
		t.Error("enter should leave search")
	}
	if m.in.Value() != "slices.Sort(x)" {
		t.Errorf("input = %q, want the accepted match", m.in.Value())
	}
}

// Typing ahead during a ~270ms evaluation must queue, not vanish. Dropping the
// Enter used to leave the runes in the input, merging the line into the next
// one ("slices.Sort(x)x").
func TestTypeAheadIsQueuedNotDropped(t *testing.T) {
	m := newTestModel(t)
	m.busy = true

	m = typeStr(m, "second line")
	m, _ = press(m, tea.KeyEnter)

	if got := m.in.Value(); got != "" {
		t.Errorf("input = %q, want cleared so the next line cannot merge into it", got)
	}
	if len(m.queue) != 1 || len(m.queue[0]) != 1 || m.queue[0][0] != "second line" {
		t.Errorf("queue = %v, want the construct held for later", m.queue)
	}
}

func TestQueueDrainsWhenResultArrives(t *testing.T) {
	m := newTestModel(t)
	m.core = nil // startEval only captures it; no evaluation runs in this test
	m.busy = true
	m.queue = [][]string{{"1+1"}}

	next, cmd := m.Update(resultMsg(Result{Out: "(int) 0"}))
	nm := next.(model)
	if len(nm.queue) != 0 {
		t.Errorf("queue = %v, want drained", nm.queue)
	}
	if !nm.busy {
		t.Error("busy = false, want the queued construct to have started")
	}
	if cmd == nil {
		t.Error("want commands for the printed result and the queued evaluation")
	}
}

func TestResultClearsBusy(t *testing.T) {
	m := newTestModel(t)
	m.busy = true
	next, _ := m.Update(resultMsg(Result{Out: "(int) 2"}))
	if next.(model).busy {
		t.Error("busy should clear when a result arrives")
	}
}

func TestQuitResultQuits(t *testing.T) {
	m := newTestModel(t)
	_, cmd := m.Update(resultMsg(Result{Quit: true}))
	if cmd == nil {
		t.Fatal("a Quit result must produce a command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("want tea.Quit")
	}
}

// Guards the assumption the whole multi-line design rests on.
func TestIncompleteDetectionMatchesPrompt(t *testing.T) {
	if !session.IsIncomplete("func f() {") {
		t.Error("unclosed brace should be incomplete")
	}
	if session.IsIncomplete("func f() {}") {
		t.Error("closed construct should be complete")
	}
}

// A bracketed paste is delivered as one KeyMsg with the newlines inside
// Runes; textinput is single-line and would flatten the whole block.
func TestMultiLinePasteIsSplit(t *testing.T) {
	m := newTestModel(t)
	next, _ := m.Update(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Paste: true,
		Runes: []rune("a := 1\nb := 2\na + b"),
	})
	nm := next.(model)

	// The last line stays in the input so a pasted block can be read first.
	if got := nm.in.Value(); got != "a + b" {
		t.Errorf("input = %q, want the trailing line held for review", got)
	}
	// Two complete constructs were pasted: they run as ONE batch — one build,
	// not two — so the batch is evaluating and nothing waits behind it.
	if !nm.busy {
		t.Error("the pasted batch should have started evaluating")
	}
	if len(nm.queue) != 0 {
		t.Errorf("queue = %v, want both lines in the running batch", nm.queue)
	}
}

// A meta command inside a paste flushes the code batch collected so far and
// runs on its own: it answers against the session as the lines before it left
// it.
func TestPastedMetaFlushesTheBatch(t *testing.T) {
	m := newTestModel(t)
	next, _ := m.Update(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Paste: true,
		Runes: []rune("a := 1\nb := 2\n:t a\nc := 3\n"),
	})
	nm := next.(model)

	if !nm.busy {
		t.Error("the first batch should have started evaluating")
	}
	// The two leading constructs are one running batch; the meta and the
	// construct after it wait their turn, in order.
	want := [][]string{{":t a"}, {"c := 3"}}
	if len(nm.queue) != len(want) {
		t.Fatalf("queue = %v, want %v", nm.queue, want)
	}
	for i := range want {
		if len(nm.queue[i]) != len(want[i]) || nm.queue[i][0] != want[i][0] {
			t.Errorf("queue[%d] = %v, want %v", i, nm.queue[i], want[i])
		}
	}
}

func TestPasteEndingInNewlineSubmitsEverything(t *testing.T) {
	m := newTestModel(t)
	next, _ := m.Update(tea.KeyMsg{
		Type: tea.KeyRunes, Paste: true, Runes: []rune("x := 1\ny := 2\n"),
	})
	nm := next.(model)
	if got := nm.in.Value(); got != "" {
		t.Errorf("input = %q, want empty", got)
	}
	if !nm.busy || len(nm.queue) != 0 {
		t.Errorf("busy=%v queue=%v, want both lines running as one batch", nm.busy, nm.queue)
	}
}

// TestAPasteEchoesInTheOrderItWasPasted. Every pasted line is echoed by its
// own tea.Println, and tea.Batch prints in whatever order its commands finish:
// the first screenshot of a paste showed p.Dist2() above the p := Point{3, 4}
// it needs. The echoes go out as a sequence, before the batch that evaluates
// them.
func TestAPasteEchoesInTheOrderItWasPasted(t *testing.T) {
	lines := []string{
		"type Point struct{ X, Y int }",
		"func (p Point) Dist2() int { return p.X*p.X + p.Y*p.Y }",
		"p := Point{3, 4}",
		"p.Dist2()",
	}
	_, cmd := newTestModel(t).paste(strings.Join(lines, "\n") + "\n")
	msg := cmd()
	if !strings.Contains(fmt.Sprintf("%T", msg), "sequenceMsg") {
		t.Fatalf("a paste answers with %T: its echoes would print in any order", msg)
	}
	var echoed []string
	seq := reflect.ValueOf(msg)
	for i := 0; i < seq.Len(); i++ {
		// A print answers with its line; the evaluation's batch answers with
		// the batch, which is not run here.
		out := seq.Index(i).Interface().(tea.Cmd)()
		if strings.Contains(fmt.Sprintf("%T", out), "printLineMessage") {
			echoed = append(echoed, fmt.Sprint(out))
		} else if i != seq.Len()-1 {
			t.Errorf("step %d of the sequence is %T, not an echo — the evaluation comes last", i, out)
		}
	}
	if len(echoed) != len(lines) {
		t.Fatalf("echoed %d lines, want %d: %q", len(echoed), len(lines), echoed)
	}
	for i, l := range lines {
		if !strings.Contains(echoed[i], l) {
			t.Errorf("echo %d is %q, want the pasted %q", i, echoed[i], l)
		}
	}
}

// A pasted multi-line construct must accumulate, not evaluate line by line.
func TestPastedConstructAccumulates(t *testing.T) {
	m := newTestModel(t)
	next, _ := m.Update(tea.KeyMsg{
		Type: tea.KeyRunes, Paste: true,
		Runes: []rune("func double(n int) int {\n\treturn n * 2\n}\n"),
	})
	nm := next.(model)
	// All three lines are one construct, so exactly one submission happened
	// and nothing was left pending.
	if !nm.busy {
		t.Error("the completed construct should have started evaluating")
	}
	if len(nm.queue) != 0 {
		t.Errorf("queue = %v, want the construct submitted as a single unit", nm.queue)
	}
	if len(nm.pending) != 0 {
		t.Errorf("pending = %v, want cleared once the construct closed", nm.pending)
	}
}

// A single-line paste falls through to textinput so the cursor is respected.
func TestSingleLinePasteFallsThrough(t *testing.T) {
	m := newTestModel(t)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Paste: true, Runes: []rune("hello")})
	nm := next.(model)
	if got := nm.in.Value(); got != "hello" {
		t.Errorf("input = %q, want hello", got)
	}
	if len(nm.queue) != 0 {
		t.Errorf("queue = %v, want nothing submitted", nm.queue)
	}
}

// TestTerminalPrintsTheTableAndSaysWhereJSONApplies.
//
// :query -json at the prompt is answered, not refused: the flag needs nothing
// a terminal cannot give, and a flag that works through a pipe but errors at
// the prompt is the more annoying kind of wrong. What the terminal owes is a
// line saying where it does something — and the table's own bytes, which every
// other driver reads out of Out, must be exactly what they were.
func TestTerminalPrintsTheTableAndSaysWhereJSONApplies(t *testing.T) {
	const table = "┌────┐\n│ id │\n└────┘\n  1 row · postgres://app@localhost:5432/acme"

	plain := terminalOut(resultMsg{Out: table})
	if plain != table {
		t.Fatalf("a result without the flag was altered:\n%q", plain)
	}

	annotated := terminalOut(resultMsg{Out: table, Query: &QueryData{Target: "x"}})
	if annotated == plain {
		t.Fatal("-json at the prompt said nothing about where the flag applies")
	}
	if !strings.HasPrefix(annotated, table) {
		t.Errorf("the table's bytes changed:\n%q", annotated)
	}
	if !strings.Contains(annotated, "gluon -e") || !strings.Contains(annotated, "pipes") {
		t.Errorf("the annotation does not say where -json applies:\n%q", annotated)
	}
	// One line, appended. Two would be a second answer to the same question.
	if got := strings.Count(strings.TrimPrefix(annotated, table), "\n"); got != 1 {
		t.Errorf("the annotation is %d lines, want 1:\n%q", got, annotated)
	}
}
