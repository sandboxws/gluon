package repl

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sandboxws/gluon/internal/syntax"
)

// A modal is the one place gluon takes the whole screen.
//
// internal/repl/tui.go says "no alt screen, ever", and the reason it gives is
// that tea.Println is a no-op while the alt screen is active — a REPL whose
// results stopped reaching scrollback would have lost the thing it is for. A
// modal keeps that intact by never printing while it is up: it renders through
// View, and on close it leaves exactly one summary line behind. Scrollback
// still reads as a complete transcript of the session, with one line saying a
// value was browsed.
//
// It is also why a modal is only ever opened for something the linear form
// could not have shown anyway — 4,812 rows, or a :src longer than the window.
type modal struct {
	spec ModalSpec

	// Exactly one of these is live, decided by whether spec has headers.
	table    table.Model
	viewport viewport.Model
	isTable  bool

	// filter is the / query applied to a table's rows.
	filtering bool
	filter    string
	all       []table.Row
	// shown maps a visible row back to the entry it came from: a filtered
	// table's cursor indexes the rows you can see, not the rows there are.
	shown []int

	// stack is the row being looked at, and whatever it was opened from. A
	// stack rather than one entry because theme.<role> is a row that opens a
	// list of roles, each of which opens a setting — backing out of that is a
	// pop, not a close.
	stack []entryView
	// run is the line the driver is being asked to submit, read once by the
	// model and cleared. The view changes nothing itself.
	run string
	// changing is the setting that line belongs to, remembered so the closing
	// summary can say which ones actually moved.
	changing string
	// waiting is a submitted change that has not been answered yet. Enter is
	// ignored while it stands: Core answers one line at a time, and a second
	// change queued behind the first would report against a table that no
	// longer describes the file.
	waiting bool
	// status is what the last change answered, shown in the footer rather than
	// printed — invariant 19, nothing reaches scrollback while this is up.
	status    string
	statusErr bool
	// changed is every setting this screen changed, in the order they were.
	changed []string
	// confirming marks the line in flight as the whole view's Confirm rather
	// than one row's change, so the closing line can be what that line
	// answered instead of the spec's own "nothing happened" summary.
	confirming bool
	// done is the answer a confirmed line gave, kept as the one line
	// scrollback gets when the screen closes.
	done string
	// fill is a line the reader chose to put on the prompt, and filledFrom the
	// row it came from. Read once by the model when the view closes.
	fill, filledFrom string
	// opened says the view was opened on a row, so backing out of that row
	// closes it.
	opened bool

	width, height int
	th            modalStyles
}

type modalStyles struct {
	title  lipgloss.Style
	footer lipgloss.Style
	note   lipgloss.Style
	key    lipgloss.Style
	sel    lipgloss.Style
	caret  lipgloss.Style
	err    lipgloss.Style
}

// currentModalStyles reads the package-level palette into the copies a view
// holds. Copies at all because a modal renders many lines per frame; re-read
// on a repaint because the settings screen can change the theme while it is
// open, and invariant 32 is that everything holding a copy repaints.
func currentModalStyles() modalStyles {
	return modalStyles{
		title:  theme.Heading,
		footer: theme.Dim,
		note:   theme.Note,
		key:    theme.Prompt,
		sel:    theme.Prompt.Reverse(true),
		// The block cursor is the prompt colour reversed, so the insert cursor
		// is the same colour underlined: one cursor in two shapes, and the
		// shape is what says the mode.
		caret: theme.Prompt.Underline(true),
		err:   theme.Err,
	}
}

func newModal(spec ModalSpec, width, height int) modal {
	m := modal{
		spec:    spec,
		isTable: len(spec.Headers) > 0,
		width:   width,
		height:  height,
		th:      currentModalStyles(),
	}

	body := m.bodyHeight()

	if m.isTable {
		m.all = make([]table.Row, len(spec.Rows))
		m.shown = make([]int, len(spec.Rows))
		for i, r := range spec.Rows {
			m.all[i] = table.Row(r)
			m.shown[i] = i
		}
		m.table = table.New(
			table.WithColumns(columnsFor(spec, width)),
			table.WithRows(m.all),
			table.WithHeight(body),
			table.WithWidth(width),
			table.WithFocused(true),
			table.WithStyles(modalTableStyles()),
		)
		if spec.Opened != "" {
			for i, e := range spec.Entries {
				if e.Title == spec.Opened {
					m.table.SetCursor(i)
					m = m.push(e)
					m.opened = true
					break
				}
			}
		}
		return m
	}

	m.viewport = viewport.New(width, body)
	// Highlight returns the text unchanged for an untagged spec or an unpainted
	// palette, so there is nothing to branch on. bubbles/viewport measures with
	// ansi.StringWidth and cuts with ansi.Cut, so paging over escapes is safe.
	m.viewport.SetContent(syntax.Highlight(spec.Lang, spec.Text, syntaxPal))
	return m
}

// columnsFor sizes each column to its widest cell, then shrinks proportionally
// if the total will not fit. A table that overflows the terminal wraps into
// unreadable pulp, which is worse than a truncated cell.
func columnsFor(spec ModalSpec, width int) []table.Column {
	cols := make([]table.Column, len(spec.Headers))
	for i, h := range spec.Headers {
		w := len([]rune(h))
		for _, row := range spec.Rows {
			if i < len(row) {
				if n := len([]rune(row[i])); n > w {
					w = n
				}
			}
		}
		cols[i] = table.Column{Title: h, Width: w + 2}
	}

	total := 0
	for _, c := range cols {
		total += c.Width
	}
	if total <= width || total == 0 {
		return cols
	}
	// Leave a column at least wide enough for its header plus an ellipsis.
	for i := range cols {
		scaled := cols[i].Width * width / total
		min := len([]rune(cols[i].Title)) + 2
		if scaled < min {
			scaled = min
		}
		cols[i].Width = scaled
	}
	return cols
}

func modalTableStyles() table.Styles {
	s := table.DefaultStyles()
	s.Header = theme.Header.BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(theme.Border.GetForeground()).BorderBottom(true).Bold(true)
	s.Cell = lipgloss.NewStyle().Padding(0, 1)
	s.Selected = theme.Prompt.Reverse(true)
	return s
}

func (m modal) Init() tea.Cmd { return nil }

// update returns the modal and whether it is still open. A closed modal hands
// control back to the prompt.
func (m modal) update(msg tea.Msg) (modal, bool, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		body := m.bodyHeight()
		if m.isTable {
			m.table.SetWidth(msg.Width)
			m.table.SetHeight(body)
		} else {
			m.viewport.Width, m.viewport.Height = msg.Width, body
		}
		return m, true, nil

	case tea.KeyMsg:
		// An open row owns the keyboard the way the modal owns it from the
		// prompt: q is a letter in a value being typed, and esc goes back one
		// level rather than throwing the whole screen away.
		if len(m.stack) > 0 {
			return m.updateEntry(msg)
		}
		if m.filtering {
			return m.updateFilter(msg)
		}
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, false, nil
		case "/":
			if m.isTable {
				m.filtering, m.filter = true, ""
				return m, true, nil
			}
		case "enter":
			if e, ok := m.entryAt(); ok {
				return m.push(e), true, nil
			}
			if cf := m.spec.Confirm; cf != nil {
				// The view stays open, the way it does for a setting changed
				// from inside it: what the line answered goes in the footer,
				// because invariant 19 means nothing may reach scrollback
				// until this closes.
				m.confirming = true
				return m.submit(cf.Run, cf.Label), true, nil
			}
		}
	}

	var cmd tea.Cmd
	if m.isTable {
		m.table, cmd = m.table.Update(msg)
	} else {
		m.viewport, cmd = m.viewport.Update(msg)
	}
	return m, true, cmd
}

func (m modal) updateFilter(msg tea.KeyMsg) (modal, bool, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.filtering, m.filter = false, ""
		return m.showMatching(), true, nil
	case tea.KeyEnter:
		m.filtering = false
		return m, true, nil
	case tea.KeyBackspace:
		if m.filter != "" {
			m.filter = m.filter[:len(m.filter)-1]
		}
	case tea.KeyRunes, tea.KeySpace:
		m.filter += string(msg.Runes)
		if msg.Type == tea.KeySpace {
			m.filter += " "
		}
	default:
		return m, true, nil
	}
	return m.showMatching(), true, nil
}

// showMatching puts the filtered rows in the table, and the map from a visible
// row back to its entry beside them. One function, because a table whose rows
// and whose index disagreed would open the wrong setting — and it would do it
// silently, which is the failure worth designing out rather than testing for.
func (m modal) showMatching() modal {
	rows, idx := m.matching()
	m.shown = idx
	m.table.SetRows(rows)
	return m
}

// matching is the rows containing the filter, case-insensitively. This is a
// human scanning for a substring, not gluon completing an identifier — the
// case-sensitivity rule that governs completion is about not corrupting a typed
// line, and nothing here is typed into the session.
func (m modal) matching() ([]table.Row, []int) {
	if m.filter == "" {
		idx := make([]int, len(m.all))
		for i := range idx {
			idx[i] = i
		}
		return m.all, idx
	}
	q := strings.ToLower(m.filter)
	var out []table.Row
	var idx []int
	for i, r := range m.all {
		for _, cell := range r {
			if strings.Contains(strings.ToLower(cell), q) {
				out = append(out, r)
				idx = append(idx, i)
				break
			}
		}
	}
	return out, idx
}

// entryAt is the entry behind the highlighted row, for a spec that brought any.
func (m modal) entryAt() (ModalEntry, bool) {
	if !m.isTable || len(m.spec.Entries) == 0 {
		return ModalEntry{}, false
	}
	cur := m.table.Cursor()
	if cur < 0 || cur >= len(m.shown) {
		return ModalEntry{}, false
	}
	if i := m.shown[cur]; i >= 0 && i < len(m.spec.Entries) {
		return m.spec.Entries[i], true
	}
	return ModalEntry{}, false
}

// bodyHeight is what is left for the table or the text once the title and the
// footer have taken theirs. Measured rather than assumed: the footer is two
// lines when there is a note or something just happened, and a body sized for
// one would push the first row off the top of the screen.
func (m modal) bodyHeight() int {
	h := m.height - 1 - lipgloss.Height(m.footer())
	if h < 3 {
		h = 3
	}
	return h
}

// restyle re-reads the palette into the copies this view holds.
//
// Invariant 32: the settings screen can change the theme from inside itself,
// and a view that kept the palette it opened with would be the one thing on
// screen that did not repaint.
func (m modal) restyle() modal {
	m.th = currentModalStyles()
	if m.isTable {
		m.table.SetStyles(modalTableStyles())
	}
	return m
}

// summary is the one line scrollback keeps.
//
// Browsing leaves the spec's own line. Changing something says what changed,
// because a transcript that reported "12 settings browsed" after two of them
// moved would have lost the only part worth keeping.
func (m modal) summary() string {
	// A view that asked one question and got an answer leaves the answer. The
	// spec's summary describes a view that was only read.
	if m.done != "" {
		return m.done
	}
	// A line put on the prompt says so, and says it was not run: the prompt
	// holds it and the transcript should not read as though it had happened.
	if m.fill != "" {
		return m.spec.Title + " " + m.filledFrom + "  the line is on the prompt, not run"
	}
	if len(m.changed) == 0 {
		return m.spec.Summary
	}
	what := m.changed
	if len(what) > 4 {
		what = append(append([]string{}, what[:4]...),
			fmt.Sprintf("%d more", len(m.changed)-4))
	}
	return m.spec.Title + "  " + strings.Join(what, ", ") + " changed"
}

func (m modal) View() string {
	var b strings.Builder
	// Already styled and already cut — rendering it a second time would nest
	// one escape sequence inside another for nothing.
	b.WriteString(m.title())
	b.WriteString("\n")
	switch {
	case len(m.stack) > 0:
		b.WriteString(strings.Join(m.entryBody(m.bodyHeight()), "\n"))
	case m.isTable:
		b.WriteString(m.table.View())
	default:
		b.WriteString(m.viewport.View())
	}
	b.WriteString("\n")
	b.WriteString(m.footer())
	return b.String()
}

// title is the screen, and the row inside it that is open. A view you can
// descend into has to say where you are, or backing out of two levels is two
// keystrokes into the dark.
func (m modal) title() string {
	line := m.spec.Title
	for _, e := range m.stack {
		line += " › " + e.entry.Title
	}
	return cut(m.th.title.Render(line), m.width)
}

func (m modal) footer() string {
	if m.filtering {
		return m.th.title.Render("/"+m.filter) + m.th.footer.Render("   enter accept · esc clear")
	}

	var parts []string
	switch {
	case len(m.stack) > 0:
		parts = m.entryKeys()
	case m.isTable:
		parts = append(parts, modalKey(m.th, "j/k", "move"), modalKey(m.th, "/", "filter"))
		// Only when there is something to open. A key named in the footer that
		// does nothing when pressed is worse than a key nobody knew about.
		if len(m.spec.Entries) > 0 {
			parts = append(parts, modalKey(m.th, "enter", "open"))
		} else if cf := m.spec.Confirm; cf != nil {
			parts = append(parts, modalKey(m.th, "enter", cf.Label))
		}
		if m.filter != "" {
			parts = append(parts, m.th.note.Render(fmt.Sprintf("%d of %d match %q",
				len(m.table.Rows()), len(m.all), m.filter)))
		}
	default:
		parts = append(parts, modalKey(m.th, "j/k", "scroll"), modalKey(m.th, "f/b", "page"))
		// A paged view asks a question when :share opens one: the content is
		// the whole point of the confirmation, so it is text rather than
		// rows. Enter already submits it — the key was simply not named, and
		// a confirmation whose key is not on screen is not one that was
		// asked.
		if cf := m.spec.Confirm; cf != nil {
			parts = append(parts, modalKey(m.th, "enter", cf.Label))
		}
	}
	if len(m.stack) == 0 {
		parts = append(parts, modalKey(m.th, "q", "close"))
	}

	line := cut(strings.Join(parts, m.th.footer.Render(" · ")), m.width)
	if second := m.footerNote(); second != "" {
		line += "\n" + second
	}
	return line
}

// modalKey is one key and what it does, in the two styles that say which is
// which.
func modalKey(th modalStyles, k, what string) string {
	return th.key.Render(k) + th.footer.Render(" "+what)
}

// footerNote is the second line: what just happened, or what the view is not
// showing. Never both — the body is sized around this line, and a status that
// pushed the note onto a third would take a row off the table to say something
// the reader has just watched happen.
//
// It is where a change reports, because invariant 19 says nothing may be
// printed while a modal is up: tea.Println is a no-op under the alt screen, so
// a confirmation printed here would be a confirmation lost.
func (m modal) footerNote() string {
	if m.status != "" {
		st := m.th.note
		if m.statusErr {
			st = m.th.err
		}
		// One line, whatever the command answered in: the writer's second
		// sentence about the next run is worth keeping, and it is not worth a
		// row of the table.
		return cut(st.Render(strings.Join(strings.Split(m.status, "\n"), " · ")), m.width)
	}
	if m.spec.Note != "" {
		return m.th.note.Render(wrapNote(m.spec.Note, m.width))
	}
	return ""
}

// wrapNote wraps a view's note to its width. The note is what the view says
// about what it is showing, and at eighty columns :since's is three lines
// long: cut to one, as the status line is, it would stop at "rather" and the
// reader would never see the rest. The body is sized from the footer's height,
// so a longer note costs a row of the table rather than its own words.
func wrapNote(note string, width int) string {
	if width <= 0 {
		return note
	}
	var out []string
	for _, l := range strings.Split(note, "\n") {
		out = append(out, wrapShown(l, width)...)
	}
	return strings.Join(out, "\n")
}
