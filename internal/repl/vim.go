package repl

import (
	"slices"
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/cursor"
	// keybind rather than key: picker_test.go already has a local named key,
	// and a package name that collides with an identifier in the same package
	// is a rename waiting to happen.
	keybind "github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sandboxws/gluon/internal/config"
)

// Modal editing at the prompt.
//
// The core below is pure: (state, buffer, key) -> (state, buffer), with no
// bubbletea and no model in it. The glue is the second half of this file, the
// way picker.go keeps both halves together.
//
// The state is UI state, exactly like m.searching and m.query. It must not go
// on Core: Core runs on the evaluation goroutine, is not safe for concurrent
// use (invariant 15), and knows nothing about a keyboard. It reaches the model
// the way a theme and a value form do — as an advisory Result field only a
// driver installs.

type vimMode int

const (
	// vimOff is the zero value, and it is the whole of "nothing changes for
	// anyone who did not turn it on": newModel(nil, …) — every existing model
	// test — gets this without an assignment.
	vimOff vimMode = iota
	vimInsert
	vimNormal
)

// vimAwait is what a half-typed command is still waiting for.
type vimAwait int

const (
	awaitNothing vimAwait = iota
	// awaitFind is f, t, F or T waiting for its target character.
	awaitFind
	// awaitReplace is r waiting for the character to write.
	awaitReplace
)

// vimBuf is the line, rune-indexed.
//
// Runes rather than bytes end to end: textinput's own mutators are rune-indexed
// and inputView converts a rune position to a byte offset itself, so a byte
// offset produced here would be one nobody converts. A line with non-ASCII
// identifiers is what pins it.
type vimBuf struct {
	rs  []rune
	pos int
}

func newVimBuf(s string, pos int) vimBuf {
	rs := []rune(s)
	return vimBuf{rs: rs, pos: min(max(pos, 0), len(rs))}
}

func (b vimBuf) String() string { return string(b.rs) }

// clampNormal keeps the cursor off the end of the line, which is where normal
// mode's cursor lives: it sits *on* a character, and there is no character
// after the last one.
func (b vimBuf) clampNormal() vimBuf {
	if b.pos >= len(b.rs) {
		b.pos = max(len(b.rs)-1, 0)
	}
	if b.pos < 0 {
		b.pos = 0
	}
	return b
}

// vimState is everything the prompt remembers between keystrokes.
//
// Held by value on model, because model is copied by value through every Update
// and a pointer would alias between the copies tests keep. The undo ring
// therefore reallocates rather than appending in place — see record.
type vimState struct {
	mode vimMode
	// op is the pending operator: 'd', 'c' or 'y', or 0.
	op rune
	// opCount and count are vim's two counts. 2d3w is d6w, so they multiply.
	opCount int
	count   int
	// await is the character a half-typed f/t/F/T/r is waiting for.
	await vimAwait
	// findCmd and findTarget are the last f/t/F/T, for ; and ,.
	findCmd    rune
	findTarget rune
	// reg is the unnamed register. There is exactly one: named, numbered and
	// small-delete registers are out of scope, and one register with no way to
	// name it is honest where nine that all behave the same would not be.
	reg []rune
	// undos and redos are the rings. An entire insert-mode visit is one step,
	// recorded when insert is entered rather than per keystroke — insert typing
	// goes through textinput and this file never sees it, which happens to give
	// vim's own granularity for free.
	undos []vimBuf
	redos []vimBuf
}

// vimRingMax bounds both rings. A prompt line is one line; a couple of hundred
// steps is more than anybody takes back, and the cap is what stops a long
// session growing a slice nobody empties.
const vimRingMax = 200

func (s vimState) on() bool     { return s.mode != vimOff }
func (s vimState) normal() bool { return s.mode == vimNormal }

// pendingCount folds the two counts. Absent counts are 1, and vim multiplies
// them: 2d3w deletes six words.
func (s vimState) pendingCount() int {
	n := 1
	if s.opCount > 0 {
		n *= s.opCount
	}
	if s.count > 0 {
		n *= s.count
	}
	return n
}

// clearPending drops everything a half-typed command was accumulating, so an
// aborted command cannot leak a count or an operator into the next one.
func (s vimState) clearPending() vimState {
	s.op, s.opCount, s.count, s.await = 0, 0, 0, awaitNothing
	return s
}

// record pushes b onto the undo ring and drops the redo ring, which is what
// makes redo mean "what undo took back" rather than "something from a branch
// nobody is on any more".
//
// The ring is reallocated rather than appended in place. model is copied by
// value through every Update, so an append that reused the backing array would
// write into the ring a copy of the model is still holding — the class of bug
// where history from a discarded branch reappears. At a couple of hundred
// snapshots the copy is free, and this comment is why nobody optimises it back.
func (s vimState) record(b vimBuf) vimState {
	next := make([]vimBuf, 0, len(s.undos)+1)
	next = append(next, s.undos...)
	next = append(next, b)
	if len(next) > vimRingMax {
		next = next[len(next)-vimRingMax:]
	}
	s.undos, s.redos = next, nil
	return s
}

// undoStep takes back one change, pushing what it replaced onto the redo ring.
// ok is false when there is nothing to take back, and the caller says nothing:
// u printing "nothing to undo" would teach the user that u and :undo are the
// same key, and printing from a keystroke puts a line in scrollback per press.
func (s vimState) undoStep(cur vimBuf) (vimState, vimBuf, bool) {
	if len(s.undos) == 0 {
		return s, cur, false
	}
	prev := s.undos[len(s.undos)-1]
	s.undos = slices.Clone(s.undos[:len(s.undos)-1])
	s.redos = append(slices.Clone(s.redos), cur)
	if len(s.redos) > vimRingMax {
		s.redos = s.redos[len(s.redos)-vimRingMax:]
	}
	return s, prev.clampNormal(), true
}

// redoStep puts back what undoStep took.
func (s vimState) redoStep(cur vimBuf) (vimState, vimBuf, bool) {
	if len(s.redos) == 0 {
		return s, cur, false
	}
	next := s.redos[len(s.redos)-1]
	s.redos = slices.Clone(s.redos[:len(s.redos)-1])
	s.undos = append(slices.Clone(s.undos), cur)
	if len(s.undos) > vimRingMax {
		s.undos = s.undos[len(s.undos)-vimRingMax:]
	}
	return s, next.clampNormal(), true
}

// clearHistory empties both rings, for a line that has been submitted or
// abandoned. It is what keeps u from reaching back past the line being typed —
// precisely the boundary :undo starts at.
func (s vimState) clearHistory() vimState {
	s.undos, s.redos = nil, nil
	return s
}

// A rune's class, for word motions.
//
// textinput's own word movement splits on whitespace only — that is W, not w.
// On strings.ToUpper(s), w must stop at the '.', at ToUpper and at the '(', so
// the three-way split is written here.
type class int

const (
	classSpace class = iota
	classWord
	classPunct
)

func runeClass(r rune) class {
	switch {
	case unicode.IsSpace(r):
		return classSpace
	case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_':
		return classWord
	default:
		return classPunct
	}
}

// bigClass is W/B/E's view of the same line: everything that is not a space is
// one class, so a qualified call is one big word.
func bigClass(r rune) class {
	if unicode.IsSpace(r) {
		return classSpace
	}
	return classWord
}

func classOf(rs []rune, i int, big bool) class {
	if big {
		return bigClass(rs[i])
	}
	return runeClass(rs[i])
}

// wordFwd is w and W: the start of the next word, or the end of the line.
func wordFwd(rs []rune, pos int, big bool) int {
	n := len(rs)
	if pos >= n {
		return n
	}
	start := classOf(rs, pos, big)
	i := pos
	if start != classSpace {
		for i < n && classOf(rs, i, big) == start {
			i++
		}
	}
	for i < n && classOf(rs, i, big) == classSpace {
		i++
	}
	return i
}

// wordBack is b and B: the start of the word before the cursor.
func wordBack(rs []rune, pos int, big bool) int {
	i := min(pos, len(rs)) - 1
	for i >= 0 && classOf(rs, i, big) == classSpace {
		i--
	}
	if i < 0 {
		return 0
	}
	c := classOf(rs, i, big)
	for i > 0 && classOf(rs, i-1, big) == c {
		i--
	}
	return i
}

// wordEnd is e and E: the last character of the current or next word. It is
// inclusive, which is why d e takes the character it lands on.
func wordEnd(rs []rune, pos int, big bool) int {
	n := len(rs)
	i := pos + 1
	for i < n && classOf(rs, i, big) == classSpace {
		i++
	}
	if i >= n {
		return max(n-1, 0)
	}
	c := classOf(rs, i, big)
	for i+1 < n && classOf(rs, i+1, big) == c {
		i++
	}
	return i
}

// findChar is f, t, F and T. before is t/T, which stop one short of the target.
//
// The search starts at the character next to the cursor for all four, which is
// what makes `t.` with the cursor already beside a dot a successful no-move
// rather than a jump to the one after it. `;` is where that case is special,
// and it is handled where the repeat is.
func findChar(rs []rune, pos int, target rune, forward, before bool) (int, bool) {
	if forward {
		from := pos + 1
		for i := from; i < len(rs); i++ {
			if rs[i] == target {
				if before {
					return i - 1, true
				}
				return i, true
			}
		}
		return pos, false
	}
	from := pos - 1
	for i := from; i >= 0; i-- {
		if rs[i] == target {
			if before {
				return i + 1, true
			}
			return i, true
		}
	}
	return pos, false
}

// firstNonBlank is ^.
func firstNonBlank(rs []rune) int {
	for i, r := range rs {
		if !unicode.IsSpace(r) {
			return i
		}
	}
	return 0
}

// vimMotion resolves one motion to a destination.
//
// inclusive is vim's classification, and it is the difference between de
// leaving the last character behind and taking it: the operated range is
// [min, max) with +1 when inclusive. Exclusive: h l w W b B 0 ^ F T.
// Inclusive: e E $ f t.
//
// t is inclusive, which is the one that reads wrong and is right: `:h t` says
// the cursor lands on the character left of the target "(inclusive)", and it is
// what makes `dt.` on a.b.c delete the a rather than nothing at all.
func vimMotion(b vimBuf, cmd, target rune, n int) (to int, inclusive, ok bool) {
	if n < 1 {
		n = 1
	}
	rs, pos := b.rs, b.pos
	switch cmd {
	case 'h':
		return max(pos-n, 0), false, true
	case 'l':
		return min(pos+n, len(rs)), false, true
	case 'w', 'W':
		big := cmd == 'W'
		for range n {
			pos = wordFwd(rs, pos, big)
		}
		return pos, false, true
	case 'b', 'B':
		big := cmd == 'B'
		for range n {
			pos = wordBack(rs, pos, big)
		}
		return pos, false, true
	case 'e', 'E':
		big := cmd == 'E'
		for range n {
			pos = wordEnd(rs, pos, big)
		}
		return pos, true, true
	case '0':
		return 0, false, true
	case '^':
		return firstNonBlank(rs), false, true
	case '$':
		return max(len(rs)-1, 0), true, true
	case 'f', 't', 'F', 'T':
		forward := cmd == 'f' || cmd == 't'
		before := cmd == 't' || cmd == 'T'
		at := pos
		for range n {
			next, found := findChar(rs, at, target, forward, before)
			if !found {
				// vim moves nothing at all when a find fails, rather than
				// moving as far as it got.
				return pos, false, false
			}
			at = next
		}
		return at, forward, true
	}
	return pos, false, false
}

// operate applies a pending operator over [from, to), returning the new buffer
// and what was taken.
//
// y is here too, and takes nothing out: one function decides what a range is,
// so d and y can never disagree about where a word ended.
func operate(b vimBuf, op rune, from, to int) (vimBuf, []rune) {
	from, to = min(from, to), max(from, to)
	from = max(from, 0)
	to = min(to, len(b.rs))
	if from >= to {
		return b, nil
	}
	taken := slices.Clone(b.rs[from:to])
	if op == 'y' {
		// The line is what it was; only the cursor moves, to the start of what
		// was yanked, which is what vim does.
		b.pos = from
		return b, taken
	}
	rest := slices.Clone(b.rs[to:])
	b.rs = append(slices.Clone(b.rs[:from]), rest...)
	b.pos = from
	return b, taken
}

// changeWordEnd is vim's documented cw exception: on a non-blank, cw behaves as
// ce, so changing a word does not swallow the space after it. It is the
// exception users notice within thirty seconds of not having it.
func changeWordEnd(b vimBuf, big bool, n int) int {
	pos := b.pos
	if pos >= len(b.rs) || runeClass(b.rs[pos]) == classSpace {
		return -1
	}
	for range n {
		pos = wordEnd(b.rs, pos, big)
	}
	return pos + 1
}

// vimPut is p and P.
//
// Charwise always: linewise put would need a line below or above, and there is
// exactly one line. p places after the cursor, P before, which is charwise
// vim's own rule.
func vimPut(b vimBuf, reg []rune, after bool, n int) vimBuf {
	if len(reg) == 0 || n < 1 {
		return b
	}
	at := b.pos
	if after && len(b.rs) > 0 {
		at++
	}
	at = min(max(at, 0), len(b.rs))

	ins := make([]rune, 0, len(reg)*n)
	for range n {
		ins = append(ins, reg...)
	}
	out := make([]rune, 0, len(b.rs)+len(ins))
	out = append(out, b.rs[:at]...)
	out = append(out, ins...)
	out = append(out, b.rs[at:]...)
	b.rs = out
	// The cursor lands on the last character put, which is where vim leaves it.
	b.pos = at + len(ins) - 1
	return b
}

// step reads one key in normal mode and answers with the new state and buffer.
//
// It is the whole of the pure half's entry point: everything above is reachable
// only from here, and nothing here knows what a terminal is. `enterInsert`
// reports that the caller should put the prompt back into insert mode, which is
// the one thing this function cannot do for itself — insert typing goes through
// textinput and never reaches this file.
//
// An undefined key does nothing. That is not an omission: without it a stray q
// falls through and gets typed into the line, which is the one behaviour a
// normal mode must not have.
func (s vimState) step(key rune, b vimBuf) (vimState, vimBuf, bool) {
	// A half-typed f/t/F/T or r is waiting for a character, and any character
	// answers it — including one that is otherwise a command.
	switch s.await {
	case awaitFind:
		n := s.pendingCount()
		op := s.op
		cmd := s.findCmd
		s.findTarget = key
		s = s.clearPending()
		return s.applyMotion(b, cmd, key, n, op)
	case awaitReplace:
		s = s.clearPending()
		if b.pos < len(b.rs) {
			s = s.record(b)
			b.rs = slices.Clone(b.rs)
			b.rs[b.pos] = key
		}
		return s, b.clampNormal(), false
	}

	// A count is built one digit at a time, and a leading 0 is the motion
	// rather than a digit — vim's rule, and the reason 0 is not in this branch
	// unless a count is already being typed.
	if key >= '1' && key <= '9' || (key == '0' && s.count > 0) {
		s.count = s.count*10 + int(key-'0')
		return s, b, false
	}

	// An operator waiting for its motion: a second identical operator is the
	// doubled form, and anything else is resolved as a motion below.
	if s.op != 0 && key == s.op {
		return s.doubled(b)
	}

	switch key {
	case 'i', 'a', 'I', 'A':
		if s.op != 0 {
			// An operator has no text object to apply these to — text objects
			// are out of scope — so the half-typed command is abandoned rather
			// than half-applied.
			return s.clearPending(), b, false
		}
		s = s.clearPending().record(b)
		switch key {
		case 'a':
			b.pos = min(b.pos+1, len(b.rs))
		case 'I':
			b.pos = firstNonBlank(b.rs)
		case 'A':
			b.pos = len(b.rs)
		}
		return s, b, true

	case 'd', 'c', 'y':
		if s.op != 0 {
			// dc, cy and friends are not commands. Abandoning is what vim does.
			return s.clearPending(), b, false
		}
		s.op, s.opCount, s.count = key, s.count, 0
		return s, b, false

	case 'x', 'X', 's', 'D', 'C', 'S':
		return s.edit(key, b)

	case 'r':
		if s.op != 0 {
			return s.clearPending(), b, false
		}
		s.await = awaitReplace
		return s, b, false

	case 'p', 'P':
		if s.op != 0 {
			return s.clearPending(), b, false
		}
		n := s.pendingCount()
		reg := s.reg
		s = s.clearPending()
		if len(reg) == 0 {
			return s, b, false
		}
		s = s.record(b)
		return s, vimPut(b, reg, key == 'p', n).clampNormal(), false

	case 'u':
		if s.op != 0 {
			return s.clearPending(), b, false
		}
		s = s.clearPending()
		s, b, _ = s.undoStep(b)
		return s, b, false
	}

	// Everything left is either a motion — on its own, or the one the pending
	// operator was waiting for — or undefined, which is silent.
	if key == 'f' || key == 't' || key == 'F' || key == 'T' {
		s.findCmd, s.await = key, awaitFind
		return s, b, false
	}
	if key == ';' || key == ',' {
		if s.findCmd == 0 {
			return s.clearPending(), b, false
		}
		cmd := s.findCmd
		if key == ',' {
			cmd = flipFind(cmd)
		}
		n, op := s.pendingCount(), s.op
		target := s.findTarget
		s = s.clearPending()

		to, inclusive, ok := vimMotion(b, cmd, target, n)
		if !ok {
			return s, b, false
		}
		if to == b.pos && (cmd == 't' || cmd == 'T') {
			// A repeated till would otherwise never leave the character it is
			// already sitting beside. vim's default cpo does not include ';',
			// so ; after t goes to the next one; the nudge is here rather than
			// in vimMotion because it belongs to the repeat and not to t.
			probe := b
			if cmd == 't' {
				probe.pos++
			} else {
				probe.pos--
			}
			if next, inc, ok2 := vimMotion(probe, cmd, target, n); ok2 {
				to, inclusive = next, inc
			}
		}
		return s.applyTo(b, to, inclusive, op)
	}

	n, op := s.pendingCount(), s.op
	s = s.clearPending()
	return s.applyMotion(b, key, 0, n, op)
}

// flipFind is what , does: the last find, the other way.
func flipFind(cmd rune) rune {
	switch cmd {
	case 'f':
		return 'F'
	case 'F':
		return 'f'
	case 't':
		return 'T'
	default:
		return 't'
	}
}

// applyMotion moves the cursor, or applies the pending operator over what the
// motion covered. One path for both, so d and a bare motion can never disagree
// about where a word ended.
func (s vimState) applyMotion(b vimBuf, cmd, target rune, n int, op rune) (vimState, vimBuf, bool) {
	// cw is ce on a non-blank — vim's documented exception.
	if op == 'c' && (cmd == 'w' || cmd == 'W') {
		if to := changeWordEnd(b, cmd == 'W', n); to >= 0 {
			s = s.record(b)
			nb, taken := operate(b, 'c', b.pos, to)
			s.reg = taken
			return s, nb, true
		}
	}

	to, inclusive, ok := vimMotion(b, cmd, target, n)
	if !ok {
		return s, b, false
	}
	return s.applyTo(b, to, inclusive, op)
}

// applyTo is the second half of applyMotion, split out so the ; and , repeat
// can decide the destination for itself — a repeated t has to step past the
// target it is already sitting beside — while the operator still measures from
// where the cursor actually is.
func (s vimState) applyTo(b vimBuf, to int, inclusive bool, op rune) (vimState, vimBuf, bool) {
	if op == 0 {
		b.pos = to
		return s, b.clampNormal(), false
	}

	from, end := b.pos, to
	if inclusive {
		// The range is [min, max) with +1 when inclusive, which is what makes
		// de take the last character of the word rather than leave it.
		if end >= from {
			end++
		} else {
			from++
		}
	}
	s = s.record(b)
	nb, taken := operate(b, op, from, end)
	s.reg = taken
	if op == 'c' {
		return s, nb, true
	}
	return s, nb.clampNormal(), false
}

// doubled is dd, cc and yy: the whole line.
func (s vimState) doubled(b vimBuf) (vimState, vimBuf, bool) {
	op := s.op
	s = s.clearPending()
	if len(b.rs) == 0 && op != 'c' {
		return s, b, false
	}
	s = s.record(b)
	nb, taken := operate(b, op, 0, len(b.rs))
	s.reg = taken
	if op == 'c' {
		return s, nb, true
	}
	return s, nb.clampNormal(), false
}

// edit is the single-key edits, each of which is its own undo step.
func (s vimState) edit(key rune, b vimBuf) (vimState, vimBuf, bool) {
	if s.op != 0 {
		return s.clearPending(), b, false
	}
	n := s.pendingCount()
	s = s.clearPending()

	switch key {
	case 'x':
		if len(b.rs) == 0 {
			return s, b, false
		}
		s = s.record(b)
		nb, taken := operate(b, 'd', b.pos, min(b.pos+n, len(b.rs)))
		s.reg = taken
		return s, nb.clampNormal(), false

	case 'X':
		if b.pos == 0 {
			return s, b, false
		}
		s = s.record(b)
		nb, taken := operate(b, 'd', max(b.pos-n, 0), b.pos)
		s.reg = taken
		return s, nb.clampNormal(), false

	case 's':
		s = s.record(b)
		nb, taken := operate(b, 'c', b.pos, min(b.pos+n, len(b.rs)))
		s.reg = taken
		return s, nb, true

	case 'S':
		s = s.record(b)
		nb, taken := operate(b, 'c', 0, len(b.rs))
		s.reg = taken
		return s, nb, true

	case 'D':
		s = s.record(b)
		nb, taken := operate(b, 'd', b.pos, len(b.rs))
		s.reg = taken
		return s, nb.clampNormal(), false

	default: // 'C'
		s = s.record(b)
		nb, taken := operate(b, 'c', b.pos, len(b.rs))
		s.reg = taken
		return s, nb, true
	}
}

// enterNormal is escape from insert. The cursor steps left, because insert's
// cursor sits *after* the character just typed and normal's sits *on* one.
func (s vimState) enterNormal(b vimBuf) (vimState, vimBuf) {
	s.mode = vimNormal
	s = s.clearPending()
	b.pos = max(b.pos-1, 0)
	return s, b.clampNormal()
}

// ---------------------------------------------------------------------------
// The glue. Everything above this line is pure; everything below knows what a
// model is, and nothing below decides what a key means.
// ---------------------------------------------------------------------------

// vimApply is the only thing in the vim code that writes the line.
//
// textinput.setValueInternal moves the cursor to the end whenever the new value
// is shorter than the current position — which is every deletion. So the order
// is SetValue, then SetCursor, then suggest: fifteen edit paths each
// remembering to re-set the cursor afterwards would be fifteen chances to
// forget, and the failure is silent.
func (m model) vimApply(b vimBuf) model {
	m.in.SetValue(b.String())
	m.in.SetCursor(b.pos)
	return m.suggest()
}

// vimBufOf is the line as the pure half sees it.
func (m model) vimBufOf() vimBuf { return newVimBuf(m.in.Value(), m.in.Position()) }

// setInputMode installs a way of reading keys, and everything that has to move
// with it.
//
// The four alt bindings go with it because a batched escape arrives as Alt on
// the next key, and textinput would otherwise claim alt+f, alt+b, alt+d and
// alt+backspace before vim ever saw them. The cost is nil: somebody who asked
// for vim editing has w, b, dw and db for exactly those four operations.
// m.in.KeyMap is a per-model copy, so this is local and reversible.
func (m model) setInputMode(mode string) model {
	on := mode == config.InputVim
	for _, bind := range []*keybind.Binding{
		&m.in.KeyMap.WordForward, &m.in.KeyMap.WordBackward,
		&m.in.KeyMap.DeleteWordForward, &m.in.KeyMap.DeleteWordBackward,
	} {
		bind.SetEnabled(!on)
	}
	if !on {
		// Switching away resets everything: insert bindings back, blinking
		// cursor back, and the rings dropped so nothing survives into a mode
		// that cannot reach them.
		m.vi = vimState{}
		m.in.Cursor.SetMode(cursor.CursorBlink)
		return m.setPrompt().suggest()
	}
	// Switching to vim starts in insert. A prompt that suddenly stopped
	// accepting characters would be alarming, and every line starts in insert
	// anyway.
	m.vi = vimState{mode: vimInsert}
	m.in.Cursor.SetMode(cursor.CursorStatic)
	return m.setPrompt()
}

// vimSetMode moves between insert and normal, carrying the prompt and the
// cursor with it. The mode is said twice: by the prompt, and by the shape of
// the cursor sitting in it — a block standing on a character in normal mode, an
// underline under one in insert, which is the pair vim itself draws.
//
// Both shapes take one cell, which is what keeps the line still: esc and i move
// the cursor and never the text. Both are also still — the underline is gluon's
// own (inputView's cursorUnder), so telling a textinput to blink a cursor it is
// no longer the one drawing would schedule a repaint every half second to
// animate nothing.
func (m model) vimSetMode(mode vimMode) model {
	m.vi.mode = mode
	m.in.Cursor.SetMode(cursor.CursorStatic)
	return m.setPrompt()
}

// setPrompt draws the prompt this model currently owes: the main one or the
// continuation, and — with modal editing on — the letter and the colour of
// whichever mode has the keyboard.
//
// Every place that used to assign m.in.Prompt calls this instead, because the
// prompt now depends on two things rather than one, and a site that remembered
// the pending lines and forgot the mode would leave the wrong letter on screen
// until the next keystroke.
func (m model) setPrompt() model {
	text, style := m.promptNow()
	m.in.Prompt = style.Render(text)
	return m
}

// promptNow is the prompt as text and style, for setPrompt and for the tests
// that would otherwise have to strip escapes to ask what the prompt says.
//
// The mode is *in* the prompt rather than beside the line for three reasons: a
// mark that appears only in normal mode says nothing at all about insert, which
// is the mode people are in nearly all the time; the prompt is the one piece of
// furniture already on screen in both modes; and a prompt is not the line, so
// the mode still cannot reach what is submitted, echoed or kept in history.
//
// `[i]` and `[n]` are the same width, so esc and i move the cursor and never
// the line, and the continuation prompt takes the marker in the same column —
// `  ...[n]> ` is exactly as wide as `gluon[n]> `, which is what keeps a
// continued construct aligned under the line that opened it.
func (m model) promptNow() (string, lipgloss.Style) {
	base, style := prompt, promptStyle
	if len(m.pending) > 0 {
		base, style = contPrompt, contStyle
	}
	switch m.vi.mode {
	case vimInsert:
		return markPrompt(base, "i"), style
	case vimNormal:
		if len(m.pending) > 0 {
			return markPrompt(base, "n"), modeContStyle
		}
		return markPrompt(base, "n"), modeStyle
	}
	// vimOff: the prompt gluon has always drawn, byte for byte. Somebody who
	// never turned modal editing on must not be able to tell it exists.
	return base, style
}

// markPrompt puts the mode letter before the prompt's terminator, so `gluon> `
// becomes `gluon[i]> `. Both prompts end in "> " by construction; a prompt that
// did not would come back unmarked rather than mangled.
func markPrompt(base, letter string) string {
	const term = "> "
	if !strings.HasSuffix(base, term) {
		return base
	}
	return strings.TrimSuffix(base, term) + "[" + letter + "]" + term
}

// vimKey is the dispatcher. It answers "handled" for everything normal mode
// owns, and false for what has to fall through to the existing key switch.
//
// It loops over msg.Runes because bubbletea coalesces every consecutive rune in
// one read into a single KeyRunes: dw typed quickly, or written by a test in
// one Write, arrives as one message with two runes. A dispatcher reading only
// the first would pass every model test and fail intermittently under a real
// program, which is the worst failure shape available.
func (m model) vimKey(msg tea.KeyMsg) (model, tea.Cmd, bool) {
	if !m.vi.on() {
		return m, nil, false
	}

	// A lone escape followed by another byte in the same read is delivered as
	// Alt on that key rather than as KeyEsc then the key. A human cannot type
	// that fast; an io.Pipe, ssh and tmux can. Treating any Alt key as
	// escape-then-key makes the batched case correct rather than survivable.
	if msg.Alt {
		if m.vi.mode != vimNormal {
			b := m.vimBufOf()
			m.vi, b = m.vi.enterNormal(b)
			m = m.vimSetMode(vimNormal).vimApply(b)
		}
		msg.Alt = false
	}

	if msg.Type == tea.KeyEsc {
		if m.vi.mode == vimNormal {
			// Already there: escape clears a half-typed command, which is what
			// it does in vim and the way out of a count typed by accident.
			m.vi = m.vi.clearPending()
			return m, nil, true
		}
		b := m.vimBufOf()
		m.vi, b = m.vi.enterNormal(b)
		m = m.vimSetMode(vimNormal).vimApply(b)
		return m, nil, true
	}

	if m.vi.mode != vimNormal {
		// Insert mode is textinput's, entirely. Ctrl-R still opens reverse
		// search, tab still accepts a ghost, and nothing here interferes.
		return m, nil, false
	}

	switch msg.Type {
	case tea.KeyCtrlC, tea.KeyCtrlL, tea.KeyEnter:
		// The three that pass through unconditionally. Ctrl-C and Enter both
		// end the line and both come back in insert — they are already
		// reflexes, which is what makes them the way out of a mode somebody
		// got into by accident.
		if msg.Type != tea.KeyCtrlL {
			m.vi = m.vi.clearHistory()
			m = m.vimSetMode(vimInsert)
		}
		return m, nil, false

	case tea.KeyCtrlD:
		// Ctrl-D passes through only where it means what gluon means by it:
		// quit on an empty line. On a line with something in it the control
		// switch does not return, so it would fall all the way to textinput and
		// delete a character — a change to the line that never reached the undo
		// ring, which would leave u restoring to a snapshot taken before it.
		if m.in.Value() == "" && len(m.pending) == 0 {
			return m, nil, false
		}
		return m, nil, true

	case tea.KeyCtrlR:
		// Redo in normal mode; reverse search is one i away, and a session
		// starts in insert, so the banner stays true as written.
		s, b, ok := m.vi.redoStep(m.vimBufOf())
		m.vi = s
		if ok {
			m = m.vimApply(b)
		}
		return m, nil, true

	case tea.KeyUp, tea.KeyDown:
		return m.vimHistory(msg.Type == tea.KeyUp, 1), nil, true

	case tea.KeyRunes, tea.KeySpace:
		rs := msg.Runes
		if msg.Type == tea.KeySpace && len(rs) == 0 {
			rs = []rune{' '}
		}
		for _, r := range rs {
			// k and j walk history where vim would move a line: bash and zsh
			// vi-mode both bind them that way, and a one-line prompt has no
			// line above. Counts apply, and they go through the same
			// hist.prev/next the arrows do.
			if (r == 'k' || r == 'j') && m.vi.op == 0 && m.vi.await == awaitNothing {
				m = m.vimHistory(r == 'k', m.vi.pendingCount())
				m.vi = m.vi.clearPending()
				continue
			}
			b := m.vimBufOf()
			var insert bool
			m.vi, b, insert = m.vi.step(r, b)
			m = m.vimApply(b)
			if insert {
				m = m.vimSetMode(vimInsert)
			}
		}
		return m, nil, true
	}

	// Everything else in normal mode is swallowed, including keys nothing
	// defines. Swallowing them *is* normal mode: without it a stray key falls
	// through to m.in.Update and gets typed into the line.
	return m, nil, true
}

// vimHistory is k, j and the arrows in normal mode: one recall, and one cursor
// clamp, in one place.
func (m model) vimHistory(back bool, n int) model {
	if n < 1 {
		n = 1
	}
	for range n {
		var v string
		var ok bool
		if back {
			v, ok = m.hist.prev(m.in.Value())
		} else {
			v, ok = m.hist.next()
		}
		if !ok {
			break
		}
		m.in.SetValue(v)
	}
	// A recall is a change to the line, so u takes back an accidental one.
	m.vi = m.vi.record(m.vimBufOf())
	return m.vimApply(m.vimBufOf().clampNormal())
}
