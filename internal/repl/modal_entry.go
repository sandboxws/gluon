package repl

import (
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// An entryView is one row of a modal, opened: the setting, and where the reader
// is inside it.
//
// This is the half of the settings screen that changes something, and it holds
// to the rule the picker set: it decides nothing. A choice carries the command
// that would make it true, enter hands that line to the model, and the model
// submits it exactly as though it had been typed at the prompt. So a value
// chosen here is validated by the same check, written by the same line edit and
// reported in the same words as `:settings value.form tree` — and there is no
// second implementation of what changing a setting means to keep in step.
type entryView struct {
	entry ModalEntry
	// idx is the highlighted choice, for a setting with a closed set.
	idx int
	// in is the value being typed, for one whose values are not a list.
	in textinput.Model
	// top is the first line of the explanation on screen, for one longer than
	// its room.
	top int
}

// typing reports whether this row is a line to write rather than a list to
// choose from. A duration, a path and an editor command line have no set to
// offer, and a screen that could only offer sets would leave four settings
// readable and unreachable.
func (v entryView) typing() bool { return v.entry.Prefix != "" }

// push opens a row.
func (m modal) push(e ModalEntry) modal {
	v := entryView{entry: e}
	// The value in force starts highlighted: the first question a settings
	// screen answers is "what is this now", and a cursor already on the answer
	// says it without being asked.
	for i, ch := range e.Choices {
		if ch.Label == e.Current {
			v.idx = i
			break
		}
	}
	if e.Prefix != "" {
		in := textinput.New()
		in.Prompt = ""
		in.CharLimit = 0
		in.Width = m.width - 12
		// It opens with what is in force, so 30s becomes 40s by moving one
		// rune rather than by retyping the value you were looking at.
		in.SetValue(e.Typed)
		in.CursorEnd()
		in.Focus()
		v.in = in
	}
	m.stack = append(m.stack, v)
	return m
}

// back is esc or q on an open row: one level up — unless the view was opened
// on this row, in which case the caller closes it, because the reader came in
// through the row and leaves the way they came. It never pops that last row:
// the closing view keeps what it was showing until it is gone.
func (m modal) back() modal {
	if m.opened && len(m.stack) == 1 {
		return m
	}
	return m.pop()
}

// scrollText pages an open row's explanation, forward or back, within it.
func (m modal) scrollText(forward bool) modal {
	v := m.stack[len(m.stack)-1]
	room := m.textHeight(v)
	step := room - 1
	if step < 1 {
		step = 1
	}
	if forward {
		v.top += step
	} else {
		v.top -= step
	}
	lines := strings.Count(strings.TrimRight(v.entry.Text, "\n"), "\n") + 1
	if max := lines - room; v.top > max {
		v.top = max
	}
	if v.top < 0 {
		v.top = 0
	}
	m.stack[len(m.stack)-1] = v
	return m
}

// pop backs out one level, to the row that opened this one or to the table.
func (m modal) pop() modal {
	if len(m.stack) > 0 {
		m.stack = m.stack[:len(m.stack)-1]
	}
	return m
}

// updateEntry is the keyboard while a row is open.
//
// esc is one level back rather than the whole screen away: a reader two levels
// down — theme.<role>, then keyword — meant to leave the colour, not the
// settings. ctrl-c is the one that abandons everything, which is what it does
// at the prompt too.
func (m modal) updateEntry(msg tea.KeyMsg) (modal, bool, tea.Cmd) {
	v := m.stack[len(m.stack)-1]

	switch msg.String() {
	case "ctrl+c":
		return m, false, nil
	case "esc":
		return m.back(), len(m.stack) > 1 || !m.opened, nil
	case "enter":
		return m.choose()
	case "pgdown", "pgup":
		return m.scrollText(msg.String() == "pgdown"), true, nil
	}
	// f and b page too where they are not letters being typed.
	if !v.typing() && (msg.String() == "f" || msg.String() == "b") {
		return m.scrollText(msg.String() == "f"), true, nil
	}

	if v.typing() {
		// Everything else is the value. q is a letter here, and so is j.
		var cmd tea.Cmd
		v.in, cmd = v.in.Update(msg)
		m.stack[len(m.stack)-1] = v
		return m, true, cmd
	}

	n := len(v.entry.Choices)
	if n == 0 {
		if msg.String() == "q" {
			return m.back(), len(m.stack) > 1 || !m.opened, nil
		}
		return m, true, nil
	}
	switch msg.String() {
	case "q":
		return m.back(), len(m.stack) > 1 || !m.opened, nil
	case "j", "down", "ctrl+n", "tab":
		v.idx = (v.idx + 1) % n
	case "k", "up", "ctrl+p", "shift+tab":
		v.idx = (v.idx - 1 + n) % n
	case "g", "home":
		v.idx = 0
	case "G", "end":
		v.idx = n - 1
	default:
		return m, true, nil
	}
	m.stack[len(m.stack)-1] = v
	return m, true, nil
}

// choose is enter: submit the highlighted value, open the setting behind it, or
// submit the line that was typed.
func (m modal) choose() (modal, bool, tea.Cmd) {
	// A change already submitted has not been answered yet. Core answers one
	// line at a time, and a second change queued behind the first would report
	// against a table that no longer describes the file.
	if m.waiting {
		return m, true, nil
	}
	v := m.stack[len(m.stack)-1]

	if v.typing() {
		value := strings.TrimSpace(v.in.Value())
		if value == "" {
			// Not an error and not a write: an empty line is somebody who
			// cleared the field and changed their mind. `-` is the way back
			// to the default, here exactly as at the prompt.
			return m, true, nil
		}
		return m.submit(v.entry.Prefix+value, v.entry.Title), true, nil
	}

	if v.idx < 0 || v.idx >= len(v.entry.Choices) {
		return m, true, nil
	}
	switch ch := v.entry.Choices[v.idx]; {
	case ch.Open != nil:
		return m.push(*ch.Open), true, nil
	case ch.Fill != "":
		// The view closes and the prompt takes the line; nothing runs.
		m.fill, m.filledFrom = ch.Fill, v.entry.Title
		return m, false, nil
	case ch.Run != "":
		return m.submit(ch.Run, v.entry.Title), true, nil
	}
	return m, true, nil
}

// submit hands one line to the driver and waits to be told what it did.
func (m modal) submit(line, key string) modal {
	m.run, m.changing, m.waiting = line, key, true
	m.status, m.statusErr = "", false
	return m
}

// settled takes what a change answered and the table as it stands after it.
//
// The answer goes in the footer rather than to scrollback, because invariant 19
// holds while this is open: tea.Println is a no-op under the alt screen, so a
// confirmation printed here is a confirmation lost. What scrollback gets is the
// one line summary names when the screen closes.
func (m modal) settled(res Result, spec *ModalSpec) modal {
	m.waiting = false
	m.status, m.statusErr = res.Out, res.Err
	switch {
	case m.confirming:
		// One line, whatever the command answered in — the same cut the footer
		// makes, for the same reason: exactly one line reaches scrollback.
		m.done = strings.Join(strings.Split(res.Out, "\n"), " · ")
	case !res.Err && m.changing != "":
		m.changed = appendOnce(m.changed, m.changing)
	}
	m.confirming, m.changing = false, ""
	if spec != nil {
		m = m.adopt(*spec)
	}
	return m
}

// adopt swaps in a freshly built table without moving the reader: the same
// filter, the same row under the cursor, the same rows open — reading what they
// now say rather than what they said when the screen opened.
func (m modal) adopt(spec ModalSpec) modal {
	cur := m.table.Cursor()
	m.spec = spec
	m.all = make([]table.Row, len(spec.Rows))
	for i, r := range spec.Rows {
		m.all[i] = table.Row(r)
	}
	m = m.showMatching()
	m.table.SetColumns(columnsFor(spec, m.width))
	m.table.SetCursor(cur)
	m.table.SetHeight(m.bodyHeight())
	return m.restack(spec.Entries)
}

// restack follows the open rows down the fresh entries by name.
//
// Without it the row you just changed would go on saying what it said when you
// opened it — the ✓ on the old value, "now 30s" under a file that reads 40s —
// which is the one lie a screen like this must not tell. A row that no longer
// exists ends the walk there, back at the table, which is the only honest thing
// left to show.
func (m modal) restack(entries []ModalEntry) modal {
	old := m.stack
	m.stack = nil
	level := entries
	for _, v := range old {
		e, ok := entryNamed(level, v.entry.Title)
		if !ok {
			break
		}
		m = m.push(e)
		top := &m.stack[len(m.stack)-1]
		// The highlight stays on the value it was on, by name rather than by
		// position: the fresh entry may have grown a choice.
		if was, ok := v.selected(); ok {
			for i, ch := range e.Choices {
				if ch.Label == was {
					top.idx = i
				}
			}
		}
		level = openEntries(e.Choices)
	}
	return m
}

// selected is the label the highlight was on.
func (v entryView) selected() (string, bool) {
	if v.idx < 0 || v.idx >= len(v.entry.Choices) {
		return "", false
	}
	return v.entry.Choices[v.idx].Label, true
}

// entryNamed finds one entry by title.
func entryNamed(entries []ModalEntry, title string) (ModalEntry, bool) {
	for _, e := range entries {
		if e.Title == title {
			return e, true
		}
	}
	return ModalEntry{}, false
}

// openEntries is the entries a level's choices lead to, which is the next level
// down for a row that stands for many settings.
func openEntries(choices []ModalChoice) []ModalEntry {
	var out []ModalEntry
	for _, ch := range choices {
		if ch.Open != nil {
			out = append(out, *ch.Open)
		}
	}
	return out
}

// entryKeys is the footer while a row is open.
func (m modal) entryKeys() []string {
	v := m.stack[len(m.stack)-1]
	if m.waiting {
		return []string{m.th.note.Render("applying…")}
	}
	switch {
	case v.typing():
		return []string{modalKey(m.th, "enter", "set"), modalKey(m.th, "esc", "back")}
	case len(v.entry.Choices) > 0:
		// A key says what it does: choosing an example does not run it.
		what := "choose"
		if ch, ok := v.choice(); ok {
			switch {
			case ch.Open != nil:
				what = "open"
			case ch.Fill != "":
				what = "put on the prompt"
			}
		}
		keys := []string{
			modalKey(m.th, "j/k", "move"),
			modalKey(m.th, "enter", what),
		}
		if m.overflows(v) {
			keys = append(keys, modalKey(m.th, "f/b", "page"))
		}
		return append(keys, modalKey(m.th, "esc", "back"))
	default:
		if m.overflows(v) {
			return []string{modalKey(m.th, "f/b", "page"), modalKey(m.th, "esc", "back")}
		}
		return []string{modalKey(m.th, "esc", "back")}
	}
}

// overflows says an open row's explanation is longer than its room.
func (m modal) overflows(v entryView) bool {
	return strings.Count(strings.TrimRight(v.entry.Text, "\n"), "\n")+1 > m.textHeight(v)
}

// choice is the highlighted one.
func (v entryView) choice() (ModalChoice, bool) {
	if v.idx < 0 || v.idx >= len(v.entry.Choices) {
		return ModalChoice{}, false
	}
	return v.entry.Choices[v.idx], true
}

// entryBody draws the open row: what the setting is, and what it may be.
//
// The values go in the room they need and the prose takes what is left, in that
// order. Which values there are is the question the screen exists to answer;
// the paragraph explaining the setting is the part a short window can lose, and
// `:settings <key>` prints all of it at the prompt either way.
func (m modal) entryBody(height int) []string {
	v := m.stack[len(m.stack)-1]
	listH := listHeight(v, height)

	var out []string
	if textH := height - listH - 1; textH > 0 {
		out = append(out, m.entryText(v, textH)...)
	}
	if listH == 0 {
		return out
	}
	out = append(out, "")
	if v.typing() {
		out = append(out,
			cut("  "+m.th.key.Render("value")+"  "+v.in.View(), m.width),
			cut(m.th.note.Render("  "+unsetMark+" puts it back to its default"), m.width))
		return out
	}
	return append(out, m.choiceLines(v, listH)...)
}

// listHeight is the room an open row's choices take: all of them, up to two
// thirds of the view. Twenty-three themes would otherwise take the whole window
// and leave the setting unexplained; the list scrolls with the selection, so a
// capped one loses nothing but the need to scroll.
func listHeight(v entryView, height int) int {
	listH := len(v.entry.Choices)
	if v.typing() {
		listH = 2 // the line, and the way back to the default under it
	}
	if max := height * 2 / 3; listH > max {
		listH = max
	}
	if listH < 0 {
		listH = 0
	}
	return listH
}

// textHeight is the room an open row's explanation has.
func (m modal) textHeight(v entryView) int {
	h := m.bodyRoom()
	if h -= listHeight(v, h) + 1; h < 1 {
		h = 1
	}
	return h
}

// bodyRoom is bodyHeight for an open row, measured without asking the footer
// for its keys. The keys name f/b only when the text overflows, and whether it
// overflows depends on this height — asking would be a cycle. It does not need
// to ask: the keys are one line whatever they say, cut to the width, so the
// footer's height is one line and the note under it when there is one.
func (m modal) bodyRoom() int {
	footer := 1
	if m.footerNote() != "" {
		footer = 2
	}
	h := m.height - 1 - footer
	if h < 3 {
		h = 3
	}
	return h
}

// entryText is the explanation, from the line the reader has paged to, cut to
// the room it has. The last line says there is more when there is, because
// prose that stops mid-sentence with no mark reads as a bug in the screen
// rather than as a window that is too short.
func (m modal) entryText(v entryView, height int) []string {
	lines := strings.Split(strings.TrimRight(v.entry.Text, "\n"), "\n")
	if v.top > 0 && v.top < len(lines) {
		lines = lines[v.top:]
	}
	cutoff := false
	if len(lines) > height {
		lines, cutoff = lines[:height], true
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, cut(m.th.footer.Render(l), m.width))
	}
	if cutoff && len(out) > 0 {
		more := v.entry.More
		if more == "" {
			more = ":settings " + v.entry.Title
		}
		out[len(out)-1] = cut(m.th.note.Render("  … f/b for the rest · "+more+
			" prints all of it"), m.width)
	}
	return out
}

// choiceLines is the closed set, windowed so the highlight is always on screen
// — picker.rows' arithmetic, for the same reason: the window is derived from
// the selection rather than remembered, so the view stays a function of the
// state and nothing else.
func (m modal) choiceLines(v entryView, n int) []string {
	if n < 1 {
		n = 1
	}
	top := 0
	if len(v.entry.Choices) > n {
		top = v.idx - n/2
		if limit := len(v.entry.Choices) - n; top > limit {
			top = limit
		}
		if top < 0 {
			top = 0
		}
	}

	width := 0
	for _, ch := range v.entry.Choices {
		if w := len([]rune(ch.Label)); w > width {
			width = w
		}
	}

	var out []string
	for i := top; i < len(v.entry.Choices) && i < top+n; i++ {
		ch := v.entry.Choices[i]
		// ✓ marks what the setting is now, which is what esc leaves it as.
		mark := " "
		if ch.Label == v.entry.Current {
			mark = "✓"
		}
		line := "  " + mark + " " + pad(ch.Label, width)
		if ch.About != "" {
			line += "   " + ch.About
		}
		line = cut(line, m.width)
		if i == v.idx {
			line = m.th.sel.Render(line)
		} else {
			line = m.th.footer.Render(line)
		}
		out = append(out, line)
	}
	return out
}

// appendOnce keeps the changed list a set: changing one setting twice is one
// setting changed, and a summary that said otherwise would be counting
// keystrokes rather than reporting what moved.
func appendOnce(xs []string, s string) []string {
	for _, x := range xs {
		if x == s {
			return xs
		}
	}
	return append(xs, s)
}
