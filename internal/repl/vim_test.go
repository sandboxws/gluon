package repl

import "testing"

// run drives the pure machine over a string of normal-mode keys and returns the
// line and the cursor. It is what most of these tests are written against: the
// core is a pure function, so a test of it needs no terminal and no model.
func vimRun(t *testing.T, line string, pos int, keys string) (string, int, vimState) {
	t.Helper()
	s := vimState{mode: vimNormal}
	b := newVimBuf(line, pos).clampNormal()
	for _, k := range keys {
		var insert bool
		s, b, insert = s.step(k, b)
		if insert {
			s.mode = vimInsert
		}
	}
	return b.String(), b.pos, s
}

// TestTheZeroVimStateIsOffAndInserting is the whole of "nothing changes for
// anyone who does not turn it on". newModel(nil, …) — every existing model test
// — gets this without an assignment.
func TestTheZeroVimStateIsOffAndInserting(t *testing.T) {
	var s vimState
	if s.on() {
		t.Error("the zero vimState is on")
	}
	if s.normal() {
		t.Error("the zero vimState is in normal mode")
	}
	if s.mode != vimOff {
		t.Errorf("mode = %v, want vimOff", s.mode)
	}
}

// TestWordsSplitOnPunctuationAndBigWordsDoNot: textinput's own word movement
// splits on whitespace only — that is W, not w. On a qualified call, w must
// stop at the dot and at the paren.
func TestWordsSplitOnPunctuationAndBigWordsDoNot(t *testing.T) {
	const line = `strings.ToUpper(s)`
	// strings | . | ToUpper | ( | s | )
	wants := []int{7, 8, 15, 16, 17}
	pos := 0
	for i, want := range wants {
		pos = wordFwd([]rune(line), pos, false)
		if pos != want {
			t.Fatalf("w %d landed at %d (%q), want %d", i+1, pos, line[pos:], want)
		}
	}
	// W crosses all of it in one, because nothing there is a space.
	if got := wordFwd([]rune(line), 0, true); got != len(line) {
		t.Errorf("W from 0 landed at %d, want the end (%d)", got, len(line))
	}
	if got := wordFwd([]rune("a bb ccc"), 0, true); got != 2 {
		t.Errorf("W over spaces landed at %d, want 2", got)
	}
}

func TestNormalModeMotionsLandWhereVimLands(t *testing.T) {
	const line = `strings.ToUpper(s)`
	cases := []struct {
		keys string
		want int
	}{
		{"l", 1},
		{"llh", 1},
		{"w", 7},
		{"ww", 8},
		{"W", len(line) - 1}, // clamped off the end
		{"$", len(line) - 1},
		{"$b", 16}, // the ) is its own word; b goes to the s before it
		{"$0", 0},
		{"e", 6},
		{"ee", 7},
		{"$B", 0},
	}
	for _, tc := range cases {
		got, pos, _ := vimRun(t, line, 0, tc.keys)
		if got != line {
			t.Errorf("%q changed the line to %q", tc.keys, got)
		}
		if pos != tc.want {
			t.Errorf("%q landed at %d (%q), want %d", tc.keys, pos, line[pos:], tc.want)
		}
	}

	// ^ is the first non-blank, which is not 0 on an indented line.
	if _, pos, _ := vimRun(t, "   x := 1", 8, "^"); pos != 3 {
		t.Errorf("^ landed at %d, want 3", pos)
	}
}

func TestFindRepeatsForwardAndBackward(t *testing.T) {
	const line = "a.b.c.d"
	cases := []struct {
		keys string
		want int
	}{
		{"f.", 1},
		{"f.;", 3},
		{"f.;;", 5},
		{"f.;;,", 3},
		{"t.", 0},
		{"lt.", 2},
		{"$F.", 5},
		{"$T.", 6},
		// A find that matches nothing moves nothing at all, rather than moving
		// as far as it got.
		{"fz", 0},
	}
	for _, tc := range cases {
		_, pos, _ := vimRun(t, line, 0, tc.keys)
		if pos != tc.want {
			t.Errorf("%q landed at %d, want %d", tc.keys, pos, tc.want)
		}
	}
}

func TestACountRepeatsAMotion(t *testing.T) {
	const line = "one two three four five"
	if _, pos, _ := vimRun(t, line, 0, "3w"); pos != 14 {
		t.Errorf("3w landed at %d (%q), want 14", pos, line[pos:])
	}
	if _, pos, _ := vimRun(t, line, 0, "5l"); pos != 5 {
		t.Errorf("5l landed at %d, want 5", pos)
	}
	// A count larger than the line clamps rather than running off it.
	if _, pos, _ := vimRun(t, line, 0, "99l"); pos != len(line)-1 {
		t.Errorf("99l landed at %d, want %d", pos, len(line)-1)
	}
	if _, pos, _ := vimRun(t, "a.b.c.d", 0, "2f."); pos != 3 {
		t.Errorf("2f. landed at %d, want 3", pos)
	}
}

// TestInclusiveMotionsTakeTheCharacterUnderTheCursor is the exclusive/inclusive
// classification, which is the difference between de leaving the last character
// of a word behind and taking it.
func TestInclusiveMotionsTakeTheCharacterUnderTheCursor(t *testing.T) {
	cases := []struct {
		line, keys, want string
	}{
		{"one two", "de", " two"},    // inclusive: the 'e' goes
		{"one two", "dw", "two"},     // exclusive, but w's space goes with it
		{"one two", "d$", ""},        // inclusive to the last character
		{"a.b.c", "df.", "b.c"},      // f is inclusive
		{"a.b.c", "dt.", ".b.c"},     // t is not
		{"one two", "wdb", "two"},    // backwards, exclusive
		{"one two", "$dh", "one to"}, // h deletes the character before the cursor
	}
	for _, tc := range cases {
		got, _, _ := vimRun(t, tc.line, 0, tc.keys)
		if got != tc.want {
			t.Errorf("%q on %q = %q, want %q", tc.keys, tc.line, got, tc.want)
		}
	}
}

func TestDoubledOperatorsTakeTheWholeLine(t *testing.T) {
	if got, _, _ := vimRun(t, "x := 1", 3, "dd"); got != "" {
		t.Errorf("dd left %q", got)
	}
	if got, _, _ := vimRun(t, "x := 1", 3, "cc"); got != "" {
		t.Errorf("cc left %q", got)
	}
	got, _, s := vimRun(t, "x := 1", 3, "yy")
	if got != "x := 1" {
		t.Errorf("yy changed the line to %q", got)
	}
	if string(s.reg) != "x := 1" {
		t.Errorf("yy put %q in the register", string(s.reg))
	}
	// cc leaves the prompt in insert, which is the whole point of change.
	if _, _, s := vimRun(t, "x := 1", 0, "cc"); s.mode != vimInsert {
		t.Error("cc did not enter insert mode")
	}
}

// TestChangeWordActsLikeChangeToEndOfWord is vim's documented cw exception, and
// the one users notice within thirty seconds of not having it.
func TestChangeWordActsLikeChangeToEndOfWord(t *testing.T) {
	got, _, _ := vimRun(t, "one two", 0, "cw")
	if got != " two" {
		t.Errorf("cw = %q, want %q — cw must not swallow the space", got, " two")
	}
	// dw is unchanged: only change has the exception.
	if got, _, _ := vimRun(t, "one two", 0, "dw"); got != "two" {
		t.Errorf("dw = %q, want %q", got, "two")
	}
	// On a blank, cw is cw again.
	if got, _, _ := vimRun(t, "a   b", 1, "cw"); got != "ab" {
		t.Errorf("cw on a blank = %q, want %q", got, "ab")
	}
}

func TestYankLeavesTheLineAsItWas(t *testing.T) {
	got, pos, s := vimRun(t, "one two three", 4, "yw")
	if got != "one two three" {
		t.Errorf("yw changed the line to %q", got)
	}
	if string(s.reg) != "two " {
		t.Errorf("yw yanked %q, want %q", string(s.reg), "two ")
	}
	if pos != 4 {
		t.Errorf("yw left the cursor at %d, want 4", pos)
	}
	if s.mode != vimNormal {
		t.Error("yw left insert mode")
	}
}

// TestPutIsCharwiseBecauseThereIsNoLineBelow. Linewise put would need a line to
// put onto, and the prompt holds exactly one.
func TestPutIsCharwiseBecauseThereIsNoLineBelow(t *testing.T) {
	got, _, _ := vimRun(t, "ab", 0, "ylp")
	if got != "aab" {
		t.Errorf("ylp = %q, want %q — p puts after the cursor, on the same line", got, "aab")
	}
	if got, _, _ := vimRun(t, "ab", 1, "ylP"); got != "abb" {
		t.Errorf("ylP = %q, want %q", got, "abb")
	}
	// A count repeats the register.
	if got, _, _ := vimRun(t, "ab", 0, "yl3p"); got != "aaaab" {
		t.Errorf("yl3p = %q, want %q", got, "aaaab")
	}
	// An empty register does nothing at all.
	if got, _, _ := vimRun(t, "ab", 0, "p"); got != "ab" {
		t.Errorf("p with an empty register changed the line to %q", got)
	}
}

func TestReplaceRefusesToRunPastTheEndOfTheLine(t *testing.T) {
	if got, _, _ := vimRun(t, "abc", 1, "rz"); got != "azc" {
		t.Errorf("rz = %q, want %q", got, "azc")
	}
	// On an empty line there is no character under the cursor, so r writes
	// nothing rather than growing the line.
	if got, _, _ := vimRun(t, "", 0, "rz"); got != "" {
		t.Errorf("r on an empty line produced %q", got)
	}
	// The awaited character is taken literally, including one that is
	// otherwise a command.
	if got, _, _ := vimRun(t, "abc", 0, "rd"); got != "dbc" {
		t.Errorf("rd = %q, want %q", got, "dbc")
	}
}

func TestSingleKeyEditsDoWhatTheySay(t *testing.T) {
	cases := []struct {
		line, keys, want string
	}{
		{"abc", "x", "bc"},
		{"abc", "2x", "c"},
		{"abc", "$X", "ac"},
		{"abc", "s", "bc"},
		{"abc", "lD", "a"},
		{"abc", "lC", "a"},
		{"abc", "S", ""},
	}
	for _, tc := range cases {
		got, _, _ := vimRun(t, tc.line, 0, tc.keys)
		if got != tc.want {
			t.Errorf("%q on %q = %q, want %q", tc.keys, tc.line, got, tc.want)
		}
	}
	// s, C and S leave insert mode on; x, X and D do not.
	for keys, want := range map[string]vimMode{"s": vimInsert, "C": vimInsert, "S": vimInsert,
		"x": vimNormal, "X": vimNormal, "D": vimNormal} {
		if _, _, s := vimRun(t, "abc", 1, keys); s.mode != want {
			t.Errorf("%q left mode %v, want %v", keys, s.mode, want)
		}
	}
}

// TestAnOperatorTimesAMotionMultipliesTheCounts: 2d3w is d6w.
func TestAnOperatorTimesAMotionMultipliesTheCounts(t *testing.T) {
	const line = "a b c d e f g"
	got, _, _ := vimRun(t, line, 0, "2d3w")
	if got != "g" {
		t.Errorf("2d3w = %q, want %q", got, "g")
	}
	if got, _, _ := vimRun(t, line, 0, "d6w"); got != "g" {
		t.Errorf("d6w = %q, want %q — the two spellings must agree", got, "g")
	}
}

func TestUndoTakesBackAWholeInsertSession(t *testing.T) {
	// Entering insert records, so the step taken back is the visit and not a
	// keystroke — which is vim's granularity, and it comes for free because
	// insert typing never reaches this file.
	s := vimState{mode: vimNormal}
	b := newVimBuf("one", 0)
	s, b, _ = s.step('A', b) // record, enter insert
	s.mode = vimInsert
	// What insert typing does, done by the caller the way textinput does it.
	b = newVimBuf("one two", 7)
	s, b = s.enterNormal(b)

	s, b, ok := s.undoStep(b)
	if !ok {
		t.Fatal("there was nothing to undo after an insert session")
	}
	if b.String() != "one" {
		t.Errorf("undo left %q, want %q", b.String(), "one")
	}
}

func TestRedoPutsBackWhatUndoTook(t *testing.T) {
	s := vimState{mode: vimNormal}
	b := newVimBuf("one two", 0)
	s, b, _ = s.step('d', b)
	s, b, _ = s.step('w', b)
	if b.String() != "two" {
		t.Fatalf("dw left %q", b.String())
	}

	s, b, ok := s.undoStep(b)
	if !ok || b.String() != "one two" {
		t.Fatalf("undo = %q, %v", b.String(), ok)
	}
	s, b, ok = s.redoStep(b)
	if !ok || b.String() != "two" {
		t.Fatalf("redo = %q, %v", b.String(), ok)
	}
	// A new change clears redo, so redo can never reach a branch nobody is on.
	s, b, _ = s.step('x', b)
	if _, _, ok := s.redoStep(b); ok {
		t.Error("a change after an undo left the redo ring reachable")
	}
}

// TestUndoIsSilentWhenThereIsNothingToTakeBack. If u printed something like
// :undo's "nothing to undo", a user would conclude they are the same key — and
// printing from a keystroke puts a line in scrollback per keypress.
func TestUndoIsSilentWhenThereIsNothingToTakeBack(t *testing.T) {
	s := vimState{mode: vimNormal}
	b := newVimBuf("x := 1", 0)
	s2, b2, ok := s.undoStep(b)
	if ok {
		t.Error("undo claimed to take something back on a fresh line")
	}
	if b2.String() != b.String() || len(s2.undos) != 0 {
		t.Error("a no-op undo changed something")
	}
	// And through step, which is the path a keypress takes.
	got, _, _ := vimRun(t, "x := 1", 0, "u")
	if got != "x := 1" {
		t.Errorf("u on a fresh line produced %q", got)
	}
}

// TestTheCursorNeverRestsPastTheLastCharacter: normal mode's cursor sits *on* a
// character, and there is no character after the last one.
func TestTheCursorNeverRestsPastTheLastCharacter(t *testing.T) {
	for _, keys := range []string{"$", "99l", "w", "3w", "D", "x", "dd", "$x"} {
		got, pos, _ := vimRun(t, "one two", 0, keys)
		if pos > max(len([]rune(got))-1, 0) {
			t.Errorf("%q left the cursor at %d on %q (%d runes)", keys, pos, got, len([]rune(got)))
		}
		if pos < 0 {
			t.Errorf("%q left the cursor at %d", keys, pos)
		}
	}
}

// TestEscFromInsertStepsTheCursorLeft: insert's cursor sits after the character
// just typed; normal's sits on one.
func TestEscFromInsertStepsTheCursorLeft(t *testing.T) {
	s := vimState{mode: vimInsert}
	s, b := s.enterNormal(newVimBuf("abc", 3))
	if s.mode != vimNormal {
		t.Fatal("escape did not enter normal mode")
	}
	if b.pos != 2 {
		t.Errorf("cursor at %d, want 2", b.pos)
	}
	if b.String() != "abc" {
		t.Errorf("escape changed the line to %q", b.String())
	}
	// At the start of the line there is nowhere to step to.
	if _, b := (vimState{mode: vimInsert}).enterNormal(newVimBuf("abc", 0)); b.pos != 0 {
		t.Errorf("cursor at %d, want 0", b.pos)
	}
}

// TestAnUnknownNormalKeyTypesNothing. Without this a stray q falls through and
// gets typed into the line, which is the one behaviour a normal mode must not
// have.
func TestAnUnknownNormalKeyTypesNothing(t *testing.T) {
	for _, keys := range []string{"q", "z", "Q", "@", "v", "V", "o", "O", "gg", "%"} {
		got, pos, _ := vimRun(t, "x := 1", 0, keys)
		if got != "x := 1" {
			t.Errorf("%q changed the line to %q", keys, got)
		}
		if pos != 0 {
			t.Errorf("%q moved the cursor to %d", keys, pos)
		}
	}
}

// TestMotionsCountRunesNotBytes. inputView converts a rune position to a byte
// offset itself, so a byte offset produced here would be one nobody converts.
func TestMotionsCountRunesNotBytes(t *testing.T) {
	const line = "café := naïve" // two multi-byte runes
	if n := len([]rune(line)); n != 13 {
		t.Fatalf("the fixture is %d runes", n)
	}
	if _, pos, _ := vimRun(t, line, 0, "$"); pos != 12 {
		t.Errorf("$ landed at %d, want 12 (runes, not %d bytes)", pos, len(line))
	}
	got, _, _ := vimRun(t, line, 0, "dw")
	if got != ":= naïve" {
		t.Errorf("dw = %q, want %q", got, ":= naïve")
	}
	// x takes one rune, not one byte.
	got, _, _ = vimRun(t, "é!", 0, "x")
	if got != "!" {
		t.Errorf("x on a multi-byte rune = %q, want %q", got, "!")
	}
	// And a find over a multi-byte line lands on the rune.
	if _, pos, _ := vimRun(t, line, 0, "fï"); pos != 10 {
		t.Errorf("fï landed at %d, want 10", pos)
	}
}

// TestAnOperatorWaitsForItsMotion is the spec's own scenario: the line is
// unchanged until the motion arrives.
func TestAnOperatorWaitsForItsMotion(t *testing.T) {
	s := vimState{mode: vimNormal}
	b := newVimBuf("one two", 0)
	s, b, _ = s.step('d', b)
	if b.String() != "one two" {
		t.Errorf("the operator alone changed the line to %q", b.String())
	}
	if s.op != 'd' {
		t.Errorf("the operator was not held: %q", s.op)
	}
	s, b, _ = s.step('w', b)
	if b.String() != "two" {
		t.Errorf("the motion applied to %q", b.String())
	}
	// An operator followed by a second, different operator is abandoned rather
	// than half-applied: dc is not a command, and neither half of it runs.
	got, _, _ := vimRun(t, "one two", 0, "dc")
	if got != "one two" {
		t.Errorf("dc changed the line to %q", got)
	}
}
