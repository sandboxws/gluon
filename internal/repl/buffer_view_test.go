package repl

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// feed drives the document the way bufferView.key does: normal and visual keys
// through docState.key, insert-mode keys through the three insert helpers, and
// \x1b for escape. It mirrors the view rather than calling it so these tests
// stay about the document and not about a screen.
func feed(st docState, d vimDoc, keys string) (docState, vimDoc) {
	for _, r := range keys {
		switch {
		case r == '\x1b':
			st.vs.mode = vimNormal
			st = st.clearPending()
			st.vis = visualNone
			d.col = max(d.col-1, 0)
			d = d.clampNormal()
		case st.vs.mode == vimInsert && r == '\n':
			st, d = st.insertNewline(d)
		case st.vs.mode == vimInsert:
			st, d = st.insert(d, r)
		default:
			st, d, _ = st.key(d, r)
		}
	}
	return st, d
}

func doc(t *testing.T, lines ...string) (docState, vimDoc) {
	t.Helper()
	return newDocState(), newVimDoc(strings.Join(lines, "\n"))
}

func wantText(t *testing.T, d vimDoc, want ...string) {
	t.Helper()
	got := d.String()
	if w := strings.Join(want, "\n") + "\n"; got != w {
		t.Errorf("document is\n%q\nwant\n%q", got, w)
	}
}

func wantAt(t *testing.T, d vimDoc, row, col int) {
	t.Helper()
	if d.row != row || d.col != col {
		t.Errorf("cursor at %d:%d, want %d:%d", d.row, d.col, row, col)
	}
}

// A document must not invent or lose a line on the round trip running in place
// makes: write it out, read it back, write it out again.
func TestADocumentRoundTripsThroughItsText(t *testing.T) {
	for _, src := range []string{
		"x := 1\n",
		"x := 1",
		"a\n\nb\n",
		"\n",
		"",
	} {
		once := newVimDoc(src).String()
		twice := newVimDoc(once).String()
		if once != twice {
			t.Errorf("%q: first pass %q, second %q", src, once, twice)
		}
	}
}

// The line-local motions are vim.go's, called on the row the cursor is on. The
// assertion is not that w works — vim_test.go owns that — but that the document
// reaches the same answer on a line that is not the first.
func TestTheDocumentReusesTheLineMotionsExactly(t *testing.T) {
	line := "strings.ToUpper(s)"
	st, d := doc(t, "first", line, "third")
	st, d = feed(st, d, "j")
	wantAt(t, d, 1, 0)

	for _, key := range []rune{'w', 'w', 'e', 'b', '$', '^'} {
		want, _, ok := vimMotion(vimBuf{rs: []rune(line), pos: d.col}, key, 0, 1)
		if !ok {
			t.Fatalf("vimMotion rejected %q", key)
		}
		st, d, _ = st.key(d, key)
		if d.row != 1 {
			t.Fatalf("%q left row %d", key, d.row)
		}
		// clampNormal is the one adjustment the document adds, and vimBuf owns
		// that too — so the expected column goes through it as well.
		wantCol := vimBuf{rs: []rune(line), pos: want}.clampNormal().pos
		if d.col != wantCol {
			t.Errorf("%q landed at column %d, vim.go says %d", key, d.col, wantCol)
		}
	}
}

func TestVerticalMotionsLandWhereVimLands(t *testing.T) {
	st, d := doc(t, "one", "two", "three", "four")

	st, d = feed(st, d, "j")
	wantAt(t, d, 1, 0)
	st, d = feed(st, d, "2j")
	wantAt(t, d, 3, 0)
	st, d = feed(st, d, "k")
	wantAt(t, d, 2, 0)
	st, d = feed(st, d, "gg")
	wantAt(t, d, 0, 0)
	st, d = feed(st, d, "G")
	wantAt(t, d, 3, 0)
	st, d = feed(st, d, "2G")
	wantAt(t, d, 1, 0)
	st, d = feed(st, d, "3gg")
	wantAt(t, d, 2, 0)

	// Past the end is the end, not a panic.
	_, d = feed(st, d, "99j")
	wantAt(t, d, 3, 0)
}

// vim remembers the column you were in and puts you back in it after a short
// line, rather than dragging you left permanently.
func TestTheColumnSurvivesAShortLine(t *testing.T) {
	st, d := doc(t, "aaaaaaaa", "bb", "cccccccc")
	st, d = feed(st, d, "$")
	wantAt(t, d, 0, 7)
	st, d = feed(st, d, "j")
	if d.col != 1 {
		t.Fatalf("on the short line the cursor is at %d, want its last character 1", d.col)
	}
	_, d = feed(st, d, "j")
	if d.col != 7 {
		t.Errorf("past the short line the column is %d, want the remembered 7", d.col)
	}
}

// o and O are the whole of what a prompt could not have: a line below and a
// line above. Both carry the indent, because Go is written with tabs and a new
// line at column zero is one the reader indents by hand every time.
func TestOpenAddsALineBelowAndAbove(t *testing.T) {
	st, d := doc(t, "\tif x {", "\t\treturn")
	st, d = feed(st, d, "jofmt.Println(1)\x1b")
	wantText(t, d, "\tif x {", "\t\treturn", "\t\tfmt.Println(1)")

	st, d = doc(t, "\tif x {", "\t\treturn")
	_, d = feed(st, d, "jOfmt.Println(2)\x1b")
	wantText(t, d, "\tif x {", "\t\tfmt.Println(2)", "\t\treturn")
}

func TestLinewisePutIsLinewise(t *testing.T) {
	st, d := doc(t, "one", "two", "three")
	st, d = feed(st, d, "dd")
	wantText(t, d, "two", "three")
	st, d = feed(st, d, "p")
	wantText(t, d, "two", "one", "three")

	// yy then P puts the copy above, still as a whole line.
	st, d = feed(st, d, "yyP")
	wantText(t, d, "two", "one", "one", "three")
}

func TestCharwisePutIsStillCharwise(t *testing.T) {
	st, d := doc(t, "alpha beta", "gamma")
	// yw takes "alpha " and p puts it back inside the line, not as a new one.
	st, d = feed(st, d, "ywjp")
	wantText(t, d, "alpha beta", "galpha amma")
	if len(d.lines) != 2 {
		t.Errorf("charwise put made %d lines, want 2", len(d.lines))
	}
}

func TestWordMotionsCrossLines(t *testing.T) {
	st, d := doc(t, "one two", "three four")
	st, d = feed(st, d, "www")
	if d.row != 1 || d.col != 0 {
		t.Fatalf("w stopped at %d:%d, want the first word of the next line 1:0", d.row, d.col)
	}
	st, d = feed(st, d, "b")
	if d.row != 0 {
		t.Errorf("b did not go back over the line end: row %d", d.row)
	}

	// e from the last word of a line lands on the end of the first word below.
	st, d = doc(t, "one", "three")
	_, d = feed(st, d, "ee")
	if d.row != 1 || d.col != 4 {
		t.Errorf("e crossed to %d:%d, want 1:4", d.row, d.col)
	}
}

func TestVisualOperatorsTakeTheSelection(t *testing.T) {
	st, d := doc(t, "alpha beta", "gamma")
	// v then two w, then d: from the start through the character the cursor
	// landed on, inclusive, which is what vd does in vim.
	st, d = feed(st, d, "vwd")
	wantText(t, d, "eta", "gamma")

	// Across a line end, the two lines join.
	st, d = doc(t, "alpha", "beta")
	_, d = feed(st, d, "lvjd")
	wantText(t, d, "ata")
}

func TestLineVisualTakesWholeLines(t *testing.T) {
	st, d := doc(t, "one", "two", "three", "four")
	st, d = feed(st, d, "jVjd")
	wantText(t, d, "one", "four")
	// And what it took is linewise, so p puts lines back rather than text.
	_, d = feed(st, d, "p")
	wantText(t, d, "one", "four", "two", "three")
}

// vim's granularity: a whole insert visit is one step, however much was typed.
func TestUndoTakesBackAWholeInsertVisitInADocument(t *testing.T) {
	st, d := doc(t, "start")
	st, d = feed(st, d, "ohello\nthere\x1b")
	wantText(t, d, "start", "hello", "there")
	st, d = feed(st, d, "u")
	wantText(t, d, "start")

	// And redo puts the whole visit back.
	_, d = feed(st, d, "")
	st2, d2, ok := st.redo(d)
	if !ok {
		t.Fatal("nothing to redo after an undo")
	}
	_ = st2
	wantText(t, d2, "start", "hello", "there")
}

// An undefined key changes nothing and records nothing. Without the second
// half, u would take back a step that never happened.
func TestAnUndefinedKeyRecordsNothing(t *testing.T) {
	st, d := doc(t, "one", "two")
	st, d = feed(st, d, "dd")
	wantText(t, d, "two")
	st, d = feed(st, d, "qqq")
	wantText(t, d, "two")
	_, d = feed(st, d, "u")
	wantText(t, d, "one", "two")
}

// dd on a document takes the line out of it. vim.go's doubled() empties the
// line instead, which is right for a prompt holding exactly one.
func TestDoubledOperatorsAreLinewiseOnADocument(t *testing.T) {
	st, d := doc(t, "one", "two", "three")
	st, d = feed(st, d, "2dd")
	wantText(t, d, "three")
	if len(d.lines) != 1 {
		t.Errorf("2dd left %d lines, want 1", len(d.lines))
	}

	st, d = doc(t, "one", "two")
	_, d = feed(st, d, "ccX\x1b")
	wantText(t, d, "X", "two")
}

// The last line is not a line. A document that grew by one every time it was
// written out and read back would grow by one on every run in place.
func TestRunningInPlaceDoesNotGrowTheDocument(t *testing.T) {
	d := newVimDoc("x := 1\n")
	for range 5 {
		d = newVimDoc(d.String())
	}
	wantText(t, d, "x := 1")
}

// A pipe — and tmux, and ssh — deliver `ofmt.Println(1)` as one message with
// fifteen runes in it. After the o, the rest of that message is typing and not
// commands, so the mode has to be re-read every rune rather than once.
func TestOneMessageCarryingAnInsertAndItsTyping(t *testing.T) {
	v := newBufferView(bufferSpec{Title: "t", Replaces: true}, "x := 1\n", 60, 12)
	v, open, _ := v.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ofmt.Println(1)")})
	if !open {
		t.Fatal("the view closed on a message that only typed into it")
	}
	wantText(t, v.doc, "x := 1", "fmt.Println(1)")
}

// step reports that insert was entered without setting the mode — at the prompt
// that is the caller's job. A document has no textinput to hand the typing to,
// so the state has to carry it, or i is a key that does nothing.
func TestTheInsertKeysEnterInsertMode(t *testing.T) {
	for _, key := range []rune{'i', 'a', 'I', 'A'} {
		st, d := doc(t, "abc")
		st, _, _ = st.key(d, key)
		if st.vs.mode != vimInsert {
			t.Errorf("%q left the document in mode %v, want insert", key, st.vs.mode)
		}
	}
	// And the ones that change text and then take typing.
	for _, keys := range []string{"cw", "s", "S", "C"} {
		st, d := doc(t, "one two")
		for _, r := range keys {
			st, d, _ = st.key(d, r)
		}
		if st.vs.mode != vimInsert {
			t.Errorf("%q left the document in mode %v, want insert", keys, st.vs.mode)
		}
	}
}

// The cursor is the mode, in the editor as at the prompt: a block standing on a
// character in normal mode, an underline under one in insert. Nothing else in
// the view says which mode has the keyboard, so this is the whole of it.
func TestTheEditorsCursorSaysWhichModeItIsIn(t *testing.T) {
	withAttributes(t)
	v := newBufferView(bufferSpec{Title: "t", Replaces: true}, "fmt.Println(x)\n", 60, 12)

	if got := underlined(v.renderLine(0)); got != "" {
		t.Errorf("normal mode underlined %q", got)
	}
	if col := v.caretAt(0); col != -1 {
		t.Errorf("normal mode wants an underline at column %d", col)
	}

	v, _, _ = v.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	if v.st.vs.mode != vimInsert {
		t.Fatal("i did not enter insert mode")
	}
	if got := underlined(v.renderLine(0)); got != "f" {
		t.Errorf("insert mode underlined %q, want the cell the cursor is in", got)
	}
	if col := v.caretAt(0); col != v.doc.col {
		t.Errorf("the underline is at column %d, the cursor at %d", col, v.doc.col)
	}
	// And on the cursor's row only.
	if col := v.caretAt(1); col != -1 {
		t.Errorf("a row the cursor is not on wants an underline at column %d", col)
	}
}

// The underline takes the cell it is in, so a document in insert mode is drawn
// exactly as normal mode draws it: every character there, every column where it
// was. A tab is where that matters most — an indent that moved by a column
// whenever the keyboard changed mode would take the whole block with it.
func TestTheEditorsInsertCursorTakesTheCell(t *testing.T) {
	withAttributes(t)
	// The gutter of a one-line document: a space, the number, two spaces.
	const gutter = " 1  "

	for _, tc := range []struct {
		line string
		col  int
		cell string
	}{
		{"fmt.Println(x)", 0, "f"},
		{"fmt.Println(x)", 4, "P"},
		{"x := 1", 6, " "}, // the end of the line, where insert mostly is
		{"\tfmt.Println(x)", 1, "f"},
		{"s := \"日本\"", 6, "日"},
		{"", 0, " "},
	} {
		v := newBufferView(bufferSpec{Title: "t", Replaces: true}, tc.line+"\n", 60, 12)
		v.st.vs.mode = vimInsert
		v.doc.col = tc.col

		want := gutter + tc.line
		if tc.cell == " " {
			want += " "
		}
		if got := stripEsc(v.renderLine(0)); got != want {
			t.Errorf("%q at %d rendered %q, want %q", tc.line, tc.col, got, want)
		}
		if got := underlined(v.renderLine(0)); got != tc.cell {
			t.Errorf("%q at %d underlined %q, want %q", tc.line, tc.col, got, tc.cell)
		}
		// Normal mode draws the line and nothing else: both cursors are styles
		// now, and a style is not a character. The one exception is a line with
		// no character to stand on, where a cell is drawn to reverse.
		normal := v
		normal.st.vs.mode = vimNormal
		normal.doc = normal.doc.clampNormal()
		plain := gutter + tc.line
		if tc.line == "" {
			plain += " "
		}
		if got := stripEsc(normal.renderLine(0)); got != plain {
			t.Errorf("%q in normal mode rendered %q, want %q", tc.line, got, plain)
		}
	}
}

// Nothing is drawn in the document while the keyboard is in the output pane: a
// cursor left behind would say the document still had the keys.
func TestNoCursorInTheDocumentWhileTheOutputHasTheKeyboard(t *testing.T) {
	v := newBufferView(bufferSpec{Title: "t", Replaces: true}, "x := 1\n", 60, 12)
	v.st.vs.mode, v.focusOut = vimInsert, true
	if col := v.caretAt(0); col != -1 {
		t.Errorf("the underline stayed in the document at column %d", col)
	}
	if len(v.spansFor(0)) != 0 {
		t.Errorf("the block stayed in the document: %v", v.spansFor(0))
	}
}
