package repl

import (
	"slices"
	"strings"
)

// The builtin editor's document.
//
// internal/repl/vim.go holds a complete modal editor for one line, and its
// whole lower half is pure: (state, buffer, key) -> (state, buffer), over a
// []rune that knows nothing about a prompt. What it does not have is a second
// axis, for the reason 2026-09-05-add-vim-input wrote down three times — the
// prompt holds one physical line, so j, k and o have nowhere to go.
//
// A document has lines. So the vertical axis lives here and every line-local
// motion is still vim.go's: wordFwd, findChar, operate and the rest are called
// unchanged on lines[row], and a fix to what `w` means fixes it in both places.
// Nothing in this file is reachable from the prompt, and vimBuf gains no field.

// vimDoc is the document: lines, and a cursor in them.
type vimDoc struct {
	lines [][]rune
	row   int
	col   int
}

func newVimDoc(text string) vimDoc {
	// A trailing newline is a terminator, not an empty last line. Keeping it
	// as one would grow the document by a line every time it was written out
	// and read back, which is exactly the round trip running in place makes.
	text = strings.TrimSuffix(text, "\n")
	raw := strings.Split(text, "\n")
	lines := make([][]rune, len(raw))
	for i, l := range raw {
		lines[i] = []rune(l)
	}
	return vimDoc{lines: lines}
}

func (d vimDoc) String() string {
	out := make([]string, len(d.lines))
	for i, l := range d.lines {
		out[i] = string(l)
	}
	// Written back with a terminating newline, because a Go file has one and
	// SplitConstructs is happier for it.
	return strings.Join(out, "\n") + "\n"
}

// clone is a deep copy. The undo ring holds documents, and a shallow copy would
// share the line slices an edit is about to write through.
func (d vimDoc) clone() vimDoc {
	lines := make([][]rune, len(d.lines))
	for i, l := range d.lines {
		lines[i] = slices.Clone(l)
	}
	d.lines = lines
	return d
}

func (d vimDoc) sameText(o vimDoc) bool {
	if len(d.lines) != len(o.lines) {
		return false
	}
	for i := range d.lines {
		if !slices.Equal(d.lines[i], o.lines[i]) {
			return false
		}
	}
	return true
}

// line is the row the cursor is on. A document always has at least one line, so
// this never has to answer for an empty one.
func (d vimDoc) line() []rune {
	if d.row < 0 || d.row >= len(d.lines) {
		return nil
	}
	return d.lines[d.row]
}

// buf hands the current line to vim.go's machinery, and withBuf takes it back.
// The pair is the whole seam between the two files.
func (d vimDoc) buf() vimBuf { return vimBuf{rs: d.line(), pos: d.col} }

func (d vimDoc) withBuf(b vimBuf) vimDoc {
	if d.row >= 0 && d.row < len(d.lines) {
		d.lines[d.row] = b.rs
	}
	d.col = b.pos
	return d
}

// clampRow keeps the cursor on a line that exists, and clampNormal keeps it on
// a character that does — normal mode's cursor sits *on* one, which is vimBuf's
// rule and is why that method is reused rather than restated.
func (d vimDoc) clampRow() vimDoc {
	if len(d.lines) == 0 {
		d.lines = [][]rune{{}}
	}
	d.row = min(max(d.row, 0), len(d.lines)-1)
	return d
}

func (d vimDoc) clampNormal() vimDoc {
	d = d.clampRow()
	return d.withBuf(d.buf().clampNormal())
}

func (d vimDoc) clampInsert() vimDoc {
	d = d.clampRow()
	d.col = min(max(d.col, 0), len(d.line()))
	return d
}

// docReg is the unnamed register, which a document needs in a shape vimState's
// cannot have: dd takes lines and dw takes characters, and p has to put back
// whichever it was. There is still exactly one register, for vim.go's reason —
// nine that all behave the same would be a lie about what is implemented.
type docReg struct {
	lines    []string
	linewise bool
}

func (r docReg) empty() bool { return len(r.lines) == 0 }

// visual is the selection mode, and its zero value is "no selection".
type visual int

const (
	visualNone visual = iota
	visualChar
	visualLine
)

// docRingMax bounds the undo ring, as vimRingMax bounds the prompt's. A
// document is bigger than a line, so the ring is shorter: a couple of hundred
// whole-document snapshots of a long session is memory nobody asked for.
const docRingMax = 100

// docState is everything the document remembers between keystrokes.
//
// vimState is embedded whole rather than reimplemented: mode, the pending
// operator, both counts, the await for f/t/F/T and r, and the last find all
// mean here exactly what they mean at the prompt. Its undo ring is the one part
// that cannot be shared — it holds vimBufs, and a document's steps are
// documents — so it is cleared after every delegation and the ring below is the
// authority.
type docState struct {
	vs vimState
	// pendingG is the g of gg. It is here rather than in vimAwait because
	// vimAwait belongs to the prompt, which has no gg to wait for.
	pendingG bool
	// vis is the selection, and anchorRow/anchorCol where it started.
	vis       visual
	anchorRow int
	anchorCol int

	// wantCol is the column vertical motion aims for — vim's curswant. Without
	// it a short line passed over drags the cursor left permanently, because
	// the clamp that keeps it on a character would be the only memory of where
	// it was. keptWant is how toRow says it has already had its say.
	wantCol  int
	keptWant bool

	reg   docReg
	undos []vimDoc
	redos []vimDoc
}

func newDocState() docState {
	return docState{vs: vimState{mode: vimNormal}}
}

func (st docState) record(d vimDoc) docState {
	next := make([]vimDoc, 0, len(st.undos)+1)
	next = append(next, st.undos...)
	next = append(next, d)
	if len(next) > docRingMax {
		next = next[len(next)-docRingMax:]
	}
	st.undos, st.redos = next, nil
	return st
}

func (st docState) undo(cur vimDoc) (docState, vimDoc, bool) {
	if len(st.undos) == 0 {
		return st, cur, false
	}
	prev := st.undos[len(st.undos)-1]
	st.undos = slices.Clone(st.undos[:len(st.undos)-1])
	st.redos = append(slices.Clone(st.redos), cur)
	if len(st.redos) > docRingMax {
		st.redos = st.redos[len(st.redos)-docRingMax:]
	}
	return st, prev.clone().clampNormal(), true
}

func (st docState) redo(cur vimDoc) (docState, vimDoc, bool) {
	if len(st.redos) == 0 {
		return st, cur, false
	}
	next := st.redos[len(st.redos)-1]
	st.redos = slices.Clone(st.redos[:len(st.redos)-1])
	st.undos = append(slices.Clone(st.undos), cur)
	if len(st.undos) > docRingMax {
		st.undos = st.undos[len(st.undos)-docRingMax:]
	}
	return st, next.clone().clampNormal(), true
}

// key reads one key in normal or visual mode and answers with the new state,
// the new document, and whether the caller should switch to insert.
//
// Undo is decided here rather than at each edit, because there are two
// granularities and only one of them is visible to the edits themselves: a
// whole insert-mode visit is one step, recorded when insert is entered, and
// everything else is one step per change. Diffing is what makes the second
// rule free — an undefined key changes nothing and so records nothing, with no
// list of which keys are edits to keep in step with the ones that are.
func (st docState) key(d vimDoc, key rune) (docState, vimDoc, bool) {
	// Undo navigates the ring rather than changing the document, so it is
	// answered before the recorder sees it. Recorded, it would push what it
	// just took back onto the ring it took it from, and clear the redo ring it
	// had just filled — u once, and redo gone.
	if key == 'u' && st.vs.mode == vimNormal && st.vis == visualNone &&
		st.vs.op == 0 && st.vs.await == awaitNothing && !st.pendingG {
		st = st.clearPending()
		st, d, _ = st.undo(d)
		st.wantCol = d.col
		return st, d, false
	}

	wasInsert := st.vs.mode == vimInsert
	before := d.clone()

	next, nd, ins := st.dispatch(d, key)
	// step reports that insert was entered without setting the mode: at the
	// prompt that is the caller's job, because the caller is the one that owns
	// a textinput to hand the typing to. Here the document is the only thing
	// there is, so the state it belongs to is this one.
	if ins {
		next.vs.mode = vimInsert
	}
	if next.keptWant {
		next.keptWant = false
	} else {
		next.wantCol = nd.col
	}
	nowInsert := ins || next.vs.mode == vimInsert

	switch {
	case !wasInsert && nowInsert:
		// The visit is about to begin, so the document as it stands is the
		// step. Insert typing records nothing, which is vim's granularity and
		// the reason u after twenty typed characters takes back all twenty.
		next = next.record(before)
	case wasInsert && nowInsert:
		// Inside the visit. Nothing to record.
	default:
		if !before.sameText(nd) {
			next = next.record(before)
		}
	}
	return next, nd, ins
}

// dispatch is key without the bookkeeping: the document-level keys are answered
// here and everything else is handed to vim.go's line machine.
func (st docState) dispatch(d vimDoc, key rune) (docState, vimDoc, bool) {
	// A half-typed f/t/F/T or r is waiting for a character. All four are
	// line-local, so the line machine answers them — including in visual mode,
	// where it moves the cursor and the selection follows.
	if st.vs.await != awaitNothing {
		return st.online(d, key)
	}

	if st.pendingG {
		st.pendingG = false
		n := st.vs.pendingCount()
		hadCount := st.vs.count > 0 || st.vs.opCount > 0
		st.vs = st.vs.clearPending()
		if key != 'g' {
			return st, d, false
		}
		row := 0
		if hadCount {
			row = n - 1
		}
		st, d = st.toRow(d, row)
		return st, d, false
	}

	if st.vis != visualNone {
		return st.visualKey(d, key)
	}

	switch key {
	case 'g':
		if st.vs.op != 0 {
			// dg and yg are not commands here: gg as a motion for an operator
			// is linewise, and linewise operators over a motion are out of
			// scope. Abandoning is what vim does with a command it cannot
			// complete.
			return st.clearPending(), d, false
		}
		st.pendingG = true
		return st, d, false

	case 'j', 'k':
		if st.vs.op != 0 {
			return st.clearPending(), d, false
		}
		n := st.vs.pendingCount()
		st.vs = st.vs.clearPending()
		if key == 'k' {
			n = -n
		}
		st, d = st.toRow(d, d.row+n)
		return st, d, false

	case 'G':
		if st.vs.op != 0 {
			return st.clearPending(), d, false
		}
		row := len(d.lines) - 1
		if st.vs.count > 0 || st.vs.opCount > 0 {
			row = st.vs.pendingCount() - 1
		}
		st.vs = st.vs.clearPending()
		st, d = st.toRow(d, row)
		return st, d, false

	case 'o', 'O':
		if st.vs.op != 0 {
			return st.clearPending(), d, false
		}
		st.vs = st.vs.clearPending()
		at := d.row
		if key == 'o' {
			at++
		}
		d = d.clone()
		d.lines = slices.Insert(d.lines, at, indentOf(d.line()))
		d.row, d.col = at, len(d.lines[at])
		st.vs.mode = vimInsert
		return st, d, true

	case 'v', 'V':
		if st.vs.op != 0 {
			return st.clearPending(), d, false
		}
		st.vs = st.vs.clearPending()
		st.vis = visualChar
		if key == 'V' {
			st.vis = visualLine
		}
		st.anchorRow, st.anchorCol = d.row, d.col
		return st, d, false

	case 'p', 'P':
		if st.vs.op != 0 {
			return st.clearPending(), d, false
		}
		n := st.vs.pendingCount()
		st.vs = st.vs.clearPending()
		if st.reg.empty() {
			return st, d, false
		}
		return st, st.put(d, n, key == 'p'), false

	}

	// A second identical operator is the doubled form, and on a document that
	// is linewise: dd takes the line out of the document rather than emptying
	// it, which is the one place vim.go's answer is the wrong one.
	if st.vs.op != 0 && key == st.vs.op {
		op := st.vs.op
		n := st.vs.pendingCount()
		st.vs = st.vs.clearPending()
		return st.lineOp(d, op, d.row, d.row+n-1)
	}

	return st.online(d, key)
}

// clearPending abandons a half-typed command, at both levels.
func (st docState) clearPending() docState {
	st.vs = st.vs.clearPending()
	st.pendingG = false
	return st
}

// online hands the key to vim.go, on the line the cursor is on.
//
// Two things are reconciled afterwards. vimState's own undo ring is cleared,
// because the ring in docState is the authority and a line snapshot taken
// mid-document would restore one line of an older document. And a register the
// line machine filled is copied out as charwise, so yw and dd leave the one
// register in the two shapes p has to tell apart.
func (st docState) online(d vimDoc, key rune) (docState, vimDoc, bool) {
	regBefore := st.vs.reg
	b := d.buf()
	vs, nb, ins := st.vs.step(key, b)
	st.vs = vs.clearHistory()
	d = d.withBuf(nb)
	if !slices.Equal(regBefore, st.vs.reg) && len(st.vs.reg) > 0 {
		st.reg = docReg{lines: []string{string(st.vs.reg)}}
	}
	// A word motion that could not move is at the end (or start) of the line,
	// and on a document the next word is on the next line. vim.go cannot know
	// that; it is the whole of what "crossing a line end" means.
	if !ins && st.vs.op == 0 && st.vs.await == awaitNothing {
		d = st.crossLine(d, key, b)
	}
	return st, d, ins
}

// crossLine carries w, b and e over a line boundary when the line-local motion
// had nowhere left to go.
func (st docState) crossLine(d vimDoc, key rune, was vimBuf) vimDoc {
	// The line machine cannot move between lines, so a cursor that did not
	// move at all is one that ran out of line.
	if d.col != was.pos {
		return d
	}
	switch key {
	case 'w', 'W':
		if d.row >= len(d.lines)-1 {
			return d
		}
		d.row++
		d.col = firstNonBlank(d.line())
		return d.clampNormal()
	case 'b', 'B', 'e', 'E':
		if key == 'e' || key == 'E' {
			if d.row >= len(d.lines)-1 {
				return d
			}
			d.row++
			d.col = 0
			nb := d.buf()
			nb.pos = wordEnd(nb.rs, 0, key == 'E')
			return d.withBuf(nb).clampNormal()
		}
		if d.row == 0 {
			return d
		}
		d.row--
		d.col = max(len(d.line())-1, 0)
		nb := d.buf()
		nb.pos = wordBack(nb.rs, len(nb.rs), key == 'B')
		return d.withBuf(nb).clampNormal()
	}
	return d
}

// toRow moves the cursor to a row, keeping the column vim keeps: the one you
// were in, not the one a short line on the way past cut you down to.
func (st docState) toRow(d vimDoc, row int) (docState, vimDoc) {
	d.row = min(max(row, 0), len(d.lines)-1)
	d.col = st.wantCol
	st.keptWant = true
	return st, d.clampNormal()
}

// lineOp is dd, yy and cc, and the linewise half of a visual operator.
func (st docState) lineOp(d vimDoc, op rune, from, to int) (docState, vimDoc, bool) {
	from = max(min(from, to), 0)
	to = min(max(from, to), len(d.lines)-1)
	if from > to {
		return st, d, false
	}
	taken := make([]string, 0, to-from+1)
	for _, l := range d.lines[from : to+1] {
		taken = append(taken, string(l))
	}
	st.reg = docReg{lines: taken, linewise: true}

	d = d.clone()
	if op == 'c' {
		// cc keeps the line and empties it, indent and all, because the next
		// thing that happens is typing on it.
		indent := indentOf(d.lines[from])
		d.lines = slices.Delete(d.lines, from, to+1)
		d.lines = slices.Insert(d.lines, from, indent)
		d.row, d.col = from, len(indent)
		st.vs.mode = vimInsert
		return st, d.clampRow(), true
	}
	if op == 'y' {
		d.row = from
		return st, d.clampNormal(), false
	}
	d.lines = slices.Delete(d.lines, from, to+1)
	if len(d.lines) == 0 {
		d.lines = [][]rune{{}}
	}
	d.row = min(from, len(d.lines)-1)
	d.col = firstNonBlank(d.line())
	return st, d.clampNormal(), false
}

// put is p and P, in whichever shape the register holds.
func (st docState) put(d vimDoc, n int, after bool) vimDoc {
	d = d.clone()
	if st.reg.linewise {
		at := d.row
		if after {
			at++
		}
		ins := make([][]rune, 0, len(st.reg.lines)*n)
		for range n {
			for _, l := range st.reg.lines {
				ins = append(ins, []rune(l))
			}
		}
		d.lines = slices.Insert(d.lines, min(at, len(d.lines)), ins...)
		d.row = min(at, len(d.lines)-1)
		d.col = firstNonBlank(d.line())
		return d.clampNormal()
	}
	text := strings.Join(st.reg.lines, "\n")
	if !strings.Contains(text, "\n") {
		// One line and no newlines: vim.go's charwise put is the answer, and
		// using it keeps p after yw identical at the prompt and here.
		return d.withBuf(vimPut(d.buf(), []rune(text), after, n)).clampNormal()
	}
	at := d.col
	if after && len(d.line()) > 0 {
		at++
	}
	return insertTextAt(d, at, strings.Repeat(text, n)).clampNormal()
}

// insertTextAt splices text into the document at a column of the current line,
// splitting the line wherever the text has a newline. The cursor lands on the
// last character put, which is where vim leaves it.
func insertTextAt(d vimDoc, at int, text string) vimDoc {
	d = d.clone()
	line := d.line()
	at = min(max(at, 0), len(line))
	head, tail := slices.Clone(line[:at]), slices.Clone(line[at:])

	parts := strings.Split(text, "\n")
	first := append(head, []rune(parts[0])...)
	if len(parts) == 1 {
		d.lines[d.row] = append(first, tail...)
		d.col = max(at+len([]rune(parts[0]))-1, 0)
		return d
	}
	d.lines[d.row] = first
	rest := make([][]rune, 0, len(parts)-1)
	for _, p := range parts[1:] {
		rest = append(rest, []rune(p))
	}
	last := len(rest) - 1
	d.col = max(len(rest[last])-1, 0)
	rest[last] = append(rest[last], tail...)
	d.lines = slices.Insert(d.lines, d.row+1, rest...)
	d.row += len(rest)
	return d
}

// indentOf is the leading whitespace of a line, which o and O carry onto the
// line they open. Go is written with tabs and a new line at column zero is one
// the reader has to indent by hand every time.
func indentOf(line []rune) []rune {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return slices.Clone(line[:i])
}

// visualKey is v and V: motions move the far end of the selection, and an
// operator takes what lies between.
//
// The motions are the ones a selection can be dragged with — h l 0 ^ $ w b e,
// and the vertical axis this file adds. f, t and the finds are not among them:
// they are line-local and go through the line machine, which in visual mode
// would apply a pending operator to a range the selection does not know about.
// An undefined key in visual mode does nothing, exactly as in normal mode.
func (st docState) visualKey(d vimDoc, key rune) (docState, vimDoc, bool) {
	switch key {
	case 'v', 'V':
		want := visualChar
		if key == 'V' {
			want = visualLine
		}
		if st.vis == want {
			// The same key again leaves visual mode, which is what it does in
			// vim. The other key switches between the two shapes.
			st.vis = visualNone
			return st.clearPending(), d.clampNormal(), false
		}
		st.vis = want
		return st, d, false

	case 'j', 'k':
		n := st.vs.pendingCount()
		st.vs = st.vs.clearPending()
		if key == 'k' {
			n = -n
		}
		st, d = st.toRow(d, d.row+n)
		return st, d, false

	case 'G':
		row := len(d.lines) - 1
		if st.vs.count > 0 {
			row = st.vs.pendingCount() - 1
		}
		st.vs = st.vs.clearPending()
		st, d = st.toRow(d, row)
		return st, d, false

	case 'g':
		st.pendingG = true
		return st, d, false

	case 'o':
		// The other end. A selection you can only grow from one side is one you
		// have to restart every time you overshoot the beginning.
		st.anchorRow, d.row = d.row, st.anchorRow
		st.anchorCol, d.col = d.col, st.anchorCol
		return st, d.clampNormal(), false

	case 'd', 'x', 'y', 'c', 's':
		op := key
		switch op {
		case 'x':
			op = 'd'
		case 's':
			op = 'c'
		}
		vis := st.vis
		st.vis = visualNone
		st.vs = st.vs.clearPending()
		return st.operateVisual(d, op, vis)

	case 'h', 'l', '0', '^', '$', 'w', 'W', 'b', 'B', 'e', 'E':
		n := st.vs.pendingCount()
		st.vs = st.vs.clearPending()
		to, _, ok := vimMotion(d.buf(), key, 0, n)
		if !ok {
			return st, d, false
		}
		// crossLine asks whether the motion moved at all, so it is handed the
		// buffer as it was. Handing it the destination would make every motion
		// look like one that had run out of line.
		was := d.buf()
		d.col = to
		d = st.crossLine(d.clampNormal(), key, was)
		st.wantCol = d.col
		return st, d, false
	}

	if key >= '1' && key <= '9' || (key == '0' && st.vs.count > 0) {
		st.vs.count = st.vs.count*10 + int(key-'0')
		return st, d, false
	}
	return st, d, false
}

// operateVisual applies an operator over the selection and leaves visual mode.
func (st docState) operateVisual(d vimDoc, op rune, vis visual) (docState, vimDoc, bool) {
	fromRow, toRow := st.anchorRow, d.row
	fromCol, toCol := st.anchorCol, d.col
	if fromRow > toRow || (fromRow == toRow && fromCol > toCol) {
		fromRow, toRow = toRow, fromRow
		fromCol, toCol = toCol, fromCol
	}
	fromRow = min(max(fromRow, 0), len(d.lines)-1)
	toRow = min(max(toRow, 0), len(d.lines)-1)

	if vis == visualLine {
		return st.lineOp(d, op, fromRow, toRow)
	}

	// Charwise, and inclusive of the character under the cursor — which is
	// what makes vd on a single character take that character rather than
	// nothing at all.
	if fromRow == toRow {
		b := vimBuf{rs: d.line(), pos: fromCol}
		nb, taken := operate(b, op, fromCol, min(toCol+1, len(b.rs)))
		st.reg = docReg{lines: []string{string(taken)}}
		d = d.withBuf(nb)
		if op == 'c' {
			st.vs.mode = vimInsert
			return st, d.clampInsert(), true
		}
		return st, d.clampNormal(), false
	}

	first, last := d.lines[fromRow], d.lines[toRow]
	fromCol = min(max(fromCol, 0), len(first))
	toCol = min(max(toCol+1, 0), len(last))

	taken := make([]string, 0, toRow-fromRow+1)
	taken = append(taken, string(first[fromCol:]))
	for _, l := range d.lines[fromRow+1 : toRow] {
		taken = append(taken, string(l))
	}
	taken = append(taken, string(last[:toCol]))
	st.reg = docReg{lines: taken}

	if op == 'y' {
		d.row, d.col = fromRow, fromCol
		return st, d.clampNormal(), false
	}

	d = d.clone()
	joined := append(slices.Clone(d.lines[fromRow][:fromCol]), d.lines[toRow][toCol:]...)
	d.lines = slices.Delete(d.lines, fromRow, toRow+1)
	d.lines = slices.Insert(d.lines, fromRow, joined)
	d.row, d.col = fromRow, fromCol
	if op == 'c' {
		st.vs.mode = vimInsert
		return st, d.clampInsert(), true
	}
	return st, d.clampNormal(), false
}

// insert is one key in insert mode. There is no textinput here to delegate to —
// that is the prompt's, and it holds one line — so the four editing keys a
// document needs are spelled out.
func (st docState) insert(d vimDoc, key rune) (docState, vimDoc) {
	d = d.clone()
	line := d.line()
	at := min(max(d.col, 0), len(line))
	d.lines[d.row] = slices.Insert(slices.Clone(line), at, key)
	d.col = at + 1
	return st, d
}

// insertNewline is Enter in insert mode: the line splits, and the new one opens
// under the indent of the one it came from.
func (st docState) insertNewline(d vimDoc) (docState, vimDoc) {
	d = d.clone()
	line := d.line()
	at := min(max(d.col, 0), len(line))
	head := slices.Clone(line[:at])
	indent := indentOf(head)
	tail := append(slices.Clone(indent), line[at:]...)
	d.lines[d.row] = head
	d.lines = slices.Insert(d.lines, d.row+1, tail)
	d.row++
	d.col = len(indent)
	return st, d
}

// insertBackspace is Backspace in insert mode, including at column zero, where
// it joins this line to the one above.
func (st docState) insertBackspace(d vimDoc) (docState, vimDoc) {
	if d.col == 0 && d.row == 0 {
		return st, d
	}
	d = d.clone()
	if d.col == 0 {
		prev := d.lines[d.row-1]
		d.col = len(prev)
		d.lines[d.row-1] = append(slices.Clone(prev), d.line()...)
		d.lines = slices.Delete(d.lines, d.row, d.row+1)
		d.row--
		return st, d
	}
	line := d.line()
	at := min(d.col, len(line))
	d.lines[d.row] = slices.Delete(slices.Clone(line), at-1, at)
	d.col = at - 1
	return st, d
}
