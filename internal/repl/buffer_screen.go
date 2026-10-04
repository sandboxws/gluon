package repl

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sandboxws/gluon/internal/scratch"
	"github.com/sandboxws/gluon/internal/syntax"
)

// The builtin editor's screen.
//
// It is a view under invariant 19, exactly as modal and picker are: it takes
// the alt screen, prints nothing while it is up, and leaves at most one line
// behind. What it adds to that chassis is the reason the setting exists — a key
// that runs what you wrote and shows the answer *here*, in a pane, rather than
// in a scrollback the alt screen is covering.
//
// It never touches Core. The model owns the goroutine evaluations run on
// (invariant 15), so the view names the work and hands it back, which is
// modal.run's contract with one more field: a document has to be written to
// disk before anything can read it, and the model does that too, off the
// keyboard's goroutine.

// bufferSpec is what opening the view needs to know, all of it derived from the
// Result that asked for an editor. Nothing here is new: Edit, EditThen and
// EditLine already say, per command, what a file is and what running it means.
type bufferSpec struct {
	// Path is the file the document is written back to, and Then the line to
	// submit once it has been. An empty Then means Reload(Path), which is
	// :edit's contract and the scratchpad's.
	Path string
	Then string
	// Line is the document line to open on, or 0 for the top.
	Line int
	// Title is what the view calls itself.
	Title string
	// Replaces reports that running the document replaces the session rather
	// than adding to it. It decides two things: what a check is checked
	// against, and whether running in place is offered at all — a document
	// that appends would append again on the second press.
	Replaces bool
	// Temp reports that Path is gluon's own scratch file rather than a file
	// the reader owns, so abandoning the view may remove it.
	Temp bool
}

// specForEdit reads a spec out of the Result that asked for an editor.
func specForEdit(res Result) bufferSpec {
	spec := bufferSpec{
		Path:     res.Edit,
		Then:     res.EditThen,
		Line:     res.EditLine,
		Replaces: res.EditThen == "",
	}
	isPad := filepath.Base(res.Edit) == scratch.PadName
	// A pad's session file is the durable thing itself — :scratch -edit hands
	// it over rather than a copy — so it is never removed. Reload draws the
	// same line, for the same reason and by the same test.
	spec.Temp = spec.Replaces && !isPad
	switch {
	case isPad:
		spec.Title = "scratchpad " + filepath.Base(filepath.Dir(res.Edit))
	case spec.Replaces:
		spec.Title = "the session"
	default:
		spec.Title = "the buffer"
	}
	return spec
}

// bufAsk is work the view wants done: write the document, then either run it or
// check it. Read once by the model and cleared, as modal.run is.
type bufAsk struct {
	check bool
	text  string
	path  string
	then  string
}

// bufferView is the document, the panes it is drawn in, and what the last
// answer was.
type bufferView struct {
	spec bufferSpec
	doc  vimDoc
	st   docState

	// top is the first visible document row.
	top int
	// pendingZ is the Z of ZZ and ZQ. A view command rather than a document
	// one, which is why it is here and not on docState.
	pendingZ bool

	out      viewport.Model
	showOut  bool
	outText  string
	focusOut bool

	// marks is the last check's diagnostics, by document line. A line with no
	// entry is a line the check had nothing to say about.
	marks   map[int]string
	checked bool

	status    string
	statusErr bool
	// waiting is a run or check that has not answered yet. A second press is
	// ignored while it stands: Core answers one thing at a time, and a second
	// run queued behind the first would report against a document that has
	// moved on.
	waiting bool
	// ran reports that something was evaluated from in here, so the closing
	// line can say whether the session was left changed.
	ran bool

	// ask is handed to the model and cleared. The view changes nothing itself.
	ask *bufAsk

	width, height int
	th            modalStyles
}

func newBufferView(spec bufferSpec, text string, width, height int) bufferView {
	v := bufferView{
		spec:   spec,
		doc:    newVimDoc(text),
		st:     newDocState(),
		width:  width,
		height: height,
		th:     currentModalStyles(),
		marks:  map[int]string{},
	}
	if spec.Line > 0 {
		v.doc.row = min(spec.Line-1, len(v.doc.lines)-1)
	}
	v.doc = v.doc.clampNormal()
	v.out = viewport.New(width, 1)
	return v.scroll()
}

// runnable reports whether running in place is offered.
//
// Only where running replaces the session. :buf appends — SubmitBatch is what
// :buf -run calls — so a second press would leave two copies of everything, and
// a key that silently duplicates the session is worse than one that is not
// there. ZZ still runs it, once, on the way out.
func (v bufferView) runnable() bool { return v.spec.Replaces }

func (v bufferView) Init() tea.Cmd { return nil }

// update drives the view and reports whether it is still open.
func (v bufferView) update(msg tea.Msg) (bufferView, bool, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		return v.scroll(), true, nil

	case tea.KeyMsg:
		return v.key(msg)
	}
	return v, true, nil
}

func (v bufferView) key(msg tea.KeyMsg) (bufferView, bool, tea.Cmd) {
	// A lone escape followed by another byte in the same read arrives as Alt on
	// that key. vimKey says why — an io.Pipe, ssh and tmux all do it, and every
	// test that drives this view is an io.Pipe.
	if msg.Alt {
		if v.st.vs.mode != vimNormal {
			v = v.toNormal()
		}
		msg.Alt = false
	}

	// The view's own keys, before the document's, so they mean the same thing
	// in insert mode as in normal mode. None of them is a vim binding: Ctrl-E
	// and Ctrl-K scroll and delete in vim, and neither is reachable here.
	switch msg.Type {
	case tea.KeyCtrlC:
		return v, false, nil
	case tea.KeyCtrlE:
		return v.startRun(), true, nil
	case tea.KeyCtrlK:
		return v.startCheck(), true, nil
	case tea.KeyCtrlO:
		v.showOut = !v.showOut
		if !v.showOut {
			v.focusOut = false
		}
		return v.scroll(), true, nil
	}

	if v.focusOut {
		return v.outKey(msg)
	}

	if v.st.vs.mode == vimInsert {
		return v.insertKey(msg)
	}
	return v.normalKey(msg)
}

// outKey is the output pane with the focus: it scrolls, and hands the keyboard
// back. Nothing here edits, so the document cannot be changed by reading it.
func (v bufferView) outKey(msg tea.KeyMsg) (bufferView, bool, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyTab:
		v.focusOut = false
		return v, true, nil
	case tea.KeyRunes:
		switch string(msg.Runes) {
		case "q":
			v.focusOut = false
			return v, true, nil
		case "j":
			v.out.LineDown(1)
			return v, true, nil
		case "k":
			v.out.LineUp(1)
			return v, true, nil
		case "f":
			v.out.ViewDown()
			return v, true, nil
		case "b":
			v.out.ViewUp()
			return v, true, nil
		case "g":
			v.out.GotoTop()
			return v, true, nil
		case "G":
			v.out.GotoBottom()
			return v, true, nil
		}
		return v, true, nil
	}
	var cmd tea.Cmd
	v.out, cmd = v.out.Update(msg)
	return v, true, cmd
}

func (v bufferView) insertKey(msg tea.KeyMsg) (bufferView, bool, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		return v.toNormal(), true, nil
	case tea.KeyEnter:
		v.st, v.doc = v.st.insertNewline(v.doc)
		return v.scroll(), true, nil
	case tea.KeyBackspace:
		v.st, v.doc = v.st.insertBackspace(v.doc)
		return v.scroll(), true, nil
	case tea.KeyTab:
		v.st, v.doc = v.st.insert(v.doc, '\t')
		return v.scroll(), true, nil
	case tea.KeySpace:
		v.st, v.doc = v.st.insert(v.doc, ' ')
		return v.scroll(), true, nil
	case tea.KeyRunes:
		for _, r := range msg.Runes {
			v.st, v.doc = v.st.insert(v.doc, r)
		}
		return v.scroll(), true, nil
	}
	return v, true, nil
}

func (v bufferView) normalKey(msg tea.KeyMsg) (bufferView, bool, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		v.pendingZ = false
		v.st = v.st.clearPending()
		v.st.vis = visualNone
		return v, true, nil

	case tea.KeyTab:
		if v.showOut {
			v.focusOut = true
		}
		return v, true, nil

	case tea.KeyCtrlR:
		var ok bool
		v.st, v.doc, ok = v.st.redo(v.doc)
		_ = ok
		return v.scroll(), true, nil

	case tea.KeyEnter:
		// Enter is j in normal mode, as it is in vim, and it is the one key a
		// reader coming from the prompt will press first.
		v.st, v.doc, _ = v.st.key(v.doc, 'j')
		return v.scroll(), true, nil

	case tea.KeyRunes, tea.KeySpace:
		rs := msg.Runes
		if msg.Type == tea.KeySpace && len(rs) == 0 {
			rs = []rune{' '}
		}
		for _, r := range rs {
			// One message can carry several runes — `ofmt.Println(x)` arrives
			// whole from a pipe, and `dd` can too (ROADMAP, "Testing the
			// UI"). So the mode is re-read every rune rather than once per
			// message: after the o, the rest of that message is typing and
			// not commands.
			if v.st.vs.mode == vimInsert {
				v.st, v.doc = v.st.insert(v.doc, r)
				continue
			}
			if v.pendingZ {
				v.pendingZ = false
				switch r {
				case 'Z':
					return v.closeRunning()
				case 'Q':
					return v, false, nil
				}
				continue
			}
			if r == 'Z' && v.st.vs.mode == vimNormal && v.st.vis == visualNone && v.st.vs.op == 0 {
				v.pendingZ = true
				continue
			}
			v.st, v.doc, _ = v.st.key(v.doc, r)
		}
		return v.scroll(), true, nil
	}
	// Everything else is swallowed. Swallowing *is* normal mode: a key that
	// fell through would be one the document did not decide about.
	return v, true, nil
}

func (v bufferView) toNormal() bufferView {
	v.st.vs.mode = vimNormal
	v.st = v.st.clearPending()
	v.doc.col = max(v.doc.col-1, 0)
	v.doc = v.doc.clampNormal()
	return v.scroll()
}

// closeRunning is ZZ: close, having asked for the document to be run.
//
// The view closes first and the answer prints into scrollback as an ordinary
// result, which is what the external editor has always done — tea.ExecProcess's
// callback submits EditThen and the answer lands in the transcript. So ZZ
// leaves no line of its own: the evaluation's own receipt is the record that
// the session changed, and a second line saying so would say it twice.
func (v bufferView) closeRunning() (bufferView, bool, tea.Cmd) {
	v.ran = true
	v.ask = &bufAsk{text: v.doc.String(), path: v.spec.Path, then: v.spec.Then}
	return v, false, nil
}

func (v bufferView) startRun() bufferView {
	if v.waiting {
		return v
	}
	if !v.runnable() {
		// Named rather than silently ignored. The reason is real and short, and
		// a key that does nothing without saying why is the one thing a footer
		// cannot fix.
		v.status = "running the buffer adds to the session — ZZ runs it once, on the way out"
		v.statusErr = true
		return v
	}
	v.waiting = true
	v.status = "running…"
	v.statusErr = false
	v.ask = &bufAsk{text: v.doc.String(), path: v.spec.Path, then: v.spec.Then}
	return v
}

func (v bufferView) startCheck() bufferView {
	if v.waiting {
		return v
	}
	v.waiting = true
	v.status = "checking…"
	v.statusErr = false
	v.ask = &bufAsk{check: true, text: v.doc.String(), path: v.spec.Path}
	return v
}

// settled takes the answer to the last ask. It is the only place the view
// learns anything from Core, and it learns it as a value rather than by asking.
func (v bufferView) settled(res Result, marks []bufMark, check bool) bufferView {
	v.waiting = false
	if check {
		v.checked = true
		v.marks = map[int]string{}
		for _, m := range marks {
			if m.Line > 0 {
				v.marks[m.Line] = m.Msg
			}
		}
		switch {
		case res.Err:
			v.status, v.statusErr = res.Out, true
		case len(marks) == 0:
			v.status, v.statusErr = "checks out", false
		default:
			v.status, v.statusErr = marks[0].Msg, true
			if len(marks) > 1 {
				v.status += fmt.Sprintf("  (+%d more)", len(marks)-1)
			}
		}
		// A check reports into the status line, never into the pane: the pane
		// holds what the session printed, and overwriting it with a type error
		// would lose the output the reader is checking against.
		return v
	}

	v.ran = true
	v.outText = res.Out
	v.showOut = res.Out != ""
	v.out.SetContent(res.Out)
	v.out.GotoTop()
	if res.Err {
		v.status, v.statusErr = "did not run — see the output", true
	} else {
		v.status, v.statusErr = "", false
	}
	return v.scroll()
}

// summary is the one line scrollback keeps, and it is empty for a view that ran
// something: the evaluation's own answer is already the record, and invariant
// 19's point is that the transcript stays complete rather than that a line is
// always added.
func (v bufferView) summary() string {
	if v.ran {
		return ""
	}
	return v.spec.Title + " — closed, nothing run"
}

// ---------------------------------------------------------------------------
// Drawing.
// ---------------------------------------------------------------------------

// gutterWidth is the line-number column, the one cell a diagnostic marks, and a
// space — without the last of those a marked line reads as `2!fmt.Println`, the
// mark touching the code it is about.
func (v bufferView) gutterWidth() int {
	n := len(strconv.Itoa(max(len(v.doc.lines), 1)))
	return n + 3
}

// outHeight is how much of the screen the output pane takes when it is open:
// as much as it needs, up to a third, and never so much that the document is
// down to three lines.
func (v bufferView) outHeight() int {
	if !v.showOut || v.outText == "" {
		return 0
	}
	want := strings.Count(v.outText, "\n") + 1
	most := max(v.height/3, 3)
	room := v.height - 2 - lipgloss.Height(v.footer()) - 3
	return max(min(min(want, most), room), 0)
}

func (v bufferView) bodyHeight() int {
	h := v.height - 1 - lipgloss.Height(v.footer()) - v.outHeight()
	if v.outHeight() > 0 {
		h-- // the divider
	}
	return max(h, 1)
}

// scroll keeps the cursor on screen and the output pane sized to its content.
func (v bufferView) scroll() bufferView {
	body := v.bodyHeight()
	if v.doc.row < v.top {
		v.top = v.doc.row
	}
	if v.doc.row >= v.top+body {
		v.top = v.doc.row - body + 1
	}
	v.top = max(min(v.top, max(len(v.doc.lines)-body, 0)), 0)
	v.out.Width = v.width
	v.out.Height = max(v.outHeight(), 1)
	return v
}

func (v bufferView) restyle() bufferView {
	v.th = currentModalStyles()
	return v
}

func (v bufferView) View() string {
	var b strings.Builder
	b.WriteString(v.title())
	b.WriteString("\n")

	body := v.bodyHeight()
	for i := range body {
		row := v.top + i
		if row >= len(v.doc.lines) {
			b.WriteString(v.th.footer.Render("~"))
		} else {
			b.WriteString(v.renderLine(row))
		}
		b.WriteString("\n")
	}
	if h := v.outHeight(); h > 0 {
		b.WriteString(v.th.footer.Render(strings.Repeat("─", max(v.width, 1))))
		b.WriteString("\n")
		b.WriteString(v.out.View())
		b.WriteString("\n")
	}
	b.WriteString(v.footer())
	return b.String()
}

func (v bufferView) title() string {
	line := v.spec.Title
	if v.spec.Replaces {
		line += "  " + plural(len(v.doc.lines), "line")
	}
	return cut(v.th.title.Render(line), v.width)
}

// renderLine draws one document row: the gutter, then the text with the cursor
// and any selection reversed over whatever colour the tokenizer gave it.
func (v bufferView) renderLine(row int) string {
	num := strconv.Itoa(row + 1)
	pad := strings.Repeat(" ", max(v.gutterWidth()-len(num)-2, 0))
	gutter := v.th.footer.Render(pad + num + "  ")
	if _, bad := v.marks[row+1]; bad {
		gutter = v.th.footer.Render(pad+num) + v.th.err.Render("!") + v.th.footer.Render(" ")
	}

	text := string(v.doc.lines[row])
	runs := []run{{text: text}}
	if syntaxPal.Painted() {
		// The lines above are the pending construct this one continues, which
		// is exactly what lineRuns was written to take: a line closing a raw
		// string opened three lines up is string content, and a scanner
		// starting at the line alone would call it two identifiers.
		runs = lineRuns(v.linesBefore(row), text)
	}
	return gutter + paintSpans(runs, syntaxPal, v.spansFor(row), v.caretAt(row), v.th)
}

// linesBefore is the document above a row, as lineRuns wants it.
func (v bufferView) linesBefore(row int) []string {
	out := make([]string, 0, row)
	for _, l := range v.doc.lines[:row] {
		out = append(out, string(l))
	}
	return out
}

// span is a rune range to draw reversed: the cursor, or the selection.
type span struct{ from, to int }

func (v bufferView) spansFor(row int) []span {
	var out []span
	if sel, ok := v.selectionOn(row); ok {
		out = append(out, sel)
	}
	// The cursor's cell is reversed only where the cursor is a block. In insert
	// it is an underline, drawn by paintSpans on the same cell — reversing it
	// as well would put a block back under the underline.
	if row == v.cursorRow() && v.st.vs.mode != vimInsert {
		out = append(out, span{v.doc.col, v.doc.col + 1})
	}
	return out
}

// cursorRow is the row the cursor is drawn on, or -1 while the keyboard is
// somewhere else. A cursor left behind in the document would say the document
// still had the keys.
func (v bufferView) cursorRow() int {
	if v.focusOut {
		return -1
	}
	return v.doc.row
}

// caretAt is the column the insert cursor underlines on a row, or -1 for none.
//
// Insert mode is the only mode that has one: every other mode's cursor is a
// block on the same cell, and vim says the difference with those two shapes.
func (v bufferView) caretAt(row int) int {
	if row != v.cursorRow() || v.st.vs.mode != vimInsert {
		return -1
	}
	return v.doc.col
}

// selectionOn is the part of the visual selection that lies on a row.
func (v bufferView) selectionOn(row int) (span, bool) {
	if v.st.vis == visualNone {
		return span{}, false
	}
	fromRow, toRow := v.st.anchorRow, v.doc.row
	fromCol, toCol := v.st.anchorCol, v.doc.col
	if fromRow > toRow || (fromRow == toRow && fromCol > toCol) {
		fromRow, toRow = toRow, fromRow
		fromCol, toCol = toCol, fromCol
	}
	if row < fromRow || row > toRow {
		return span{}, false
	}
	width := len(v.doc.lines[row])
	if v.st.vis == visualLine {
		return span{0, max(width, 1)}, true
	}
	from, to := 0, width
	if row == fromRow {
		from = fromCol
	}
	if row == toRow {
		to = min(toCol+1, width)
	}
	if to <= from {
		return span{}, false
	}
	return span{from, to}, true
}

// paintSpans renders runs with a palette, reversing the cells the spans cover
// and underlining the one the insert cursor marks, if there is one.
//
// Splitting the runs rather than reversing the rendered string, because the
// rendered string is escape sequences and a cell is not a byte. Everything a
// run carries survives: the role is re-rendered on each piece, so a cursor in
// the middle of an identifier leaves both halves the colour they were.
func paintSpans(runs []run, pal syntax.Palette, spans []span, caret int, th modalStyles) string {
	// NoTabConversion, because lipgloss.Render turns a tab into four spaces and
	// a terminal turns it into however many the next stop is. The cursor's cell
	// is the only one on the line that goes through Render at all, so without
	// this an indented line jumps sideways the moment the cursor reaches its
	// indent — and every line of Go a session holds is indented with tabs. The
	// prompt's own cursor says the same thing at input.go's cursorOver.
	sel := th.sel.TabWidth(lipgloss.NoTabConversion)
	under := th.caret.TabWidth(lipgloss.NoTabConversion)

	var b strings.Builder
	at := 0
	for _, r := range runs {
		rs := []rune(r.text)
		i := 0
		for i < len(rs) {
			// The underline takes the cell it is in, so the cell is its own
			// piece: one character, drawn by the cursor's style instead of the
			// role's, which is why the run is split at the caret as well.
			if at+i == caret {
				b.WriteString(under.Render(string(rs[i])))
				i++
				continue
			}
			on := covered(spans, at+i)
			j := i + 1
			for j < len(rs) && at+j != caret && covered(spans, at+j) == on {
				j++
			}
			piece := string(rs[i:j])
			if on {
				b.WriteString(sel.Render(piece))
			} else {
				b.WriteString(pal.Span(r.role, piece))
			}
			i = j
		}
		at += len(rs)
	}
	// A cursor past the last character — an empty line, a line of nothing in
	// normal mode, or insert at the end of one, which is where insert nearly
	// always is — has no character to mark, so it takes the cell after it.
	switch {
	case at == caret:
		b.WriteString(under.Render(" "))
	case covered(spans, at):
		b.WriteString(sel.Render(" "))
	}
	return b.String()
}

func covered(spans []span, i int) bool {
	for _, s := range spans {
		if i >= s.from && i < s.to {
			return true
		}
	}
	return false
}

func (v bufferView) footer() string {
	var parts []string
	if v.focusOut {
		parts = append(parts,
			modalKey(v.th, "j/k", "scroll"),
			modalKey(v.th, "f/b", "page"),
			modalKey(v.th, "tab", "back to the document"))
	} else {
		if v.runnable() {
			parts = append(parts, modalKey(v.th, "^e", "run"))
		}
		parts = append(parts,
			modalKey(v.th, "^k", "check"),
			modalKey(v.th, "ZZ", v.closeLabel()),
			modalKey(v.th, "ZQ", "close"))
		if v.outText != "" {
			parts = append(parts, modalKey(v.th, "^o", "output"))
		}
	}
	line := cut(strings.Join(parts, v.th.footer.Render(" · ")), v.width)
	if note := v.footerNote(); note != "" {
		line += "\n" + note
	}
	return line
}

func (v bufferView) closeLabel() string {
	if v.spec.Replaces {
		return "close, reloading"
	}
	return "close, running"
}

func (v bufferView) footerNote() string {
	if v.status == "" {
		return ""
	}
	st := v.th.note
	if v.statusErr {
		st = v.th.err
	}
	// One line. The body is sized around this note, and a status that wrapped
	// onto a second would take a row off the document to say something the
	// reader has just watched happen.
	return cut(st.Render(strings.ReplaceAll(v.status, "\n", " ")), v.width)
}
