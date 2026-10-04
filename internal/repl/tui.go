package repl

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sandboxws/gluon/internal/config"
	"github.com/sandboxws/gluon/internal/pretty"
	"github.com/sandboxws/gluon/internal/session"
	"github.com/sandboxws/gluon/internal/syntax"
	"github.com/sandboxws/gluon/internal/ui"
)

// theme is the palette for the whole UI, resolved once from config. The REPL
// owns a terminal by construction — runTUI is only reached when stdin is one —
// so the destination it asks about is stdin.
var theme = ui.Plain()

var (
	promptStyle lipgloss.Style
	contStyle   lipgloss.Style
	errStyle    lipgloss.Style
	dimStyle    lipgloss.Style
	searchStyle lipgloss.Style
	// modeStyle and modeContStyle are the two prompts again, in the colour a
	// palette gives normal mode. They are separate variables rather than a
	// lookup because the prompt is redrawn on every key that changes the mode.
	modeStyle, modeContStyle lipgloss.Style
	// syntaxPal is the source palette, derived once from the same theme.
	// A plain theme yields the zero Palette, which every Highlight call in this
	// package treats as "return the text" — so colour cannot leak from here
	// even if a call site forgets to ask whether it should.
	syntaxPal syntax.Palette
)

// setTheme installs a palette and the package-level styles derived from it.
func setTheme(t ui.Theme) {
	theme = t
	promptStyle, contStyle = t.Prompt, t.Cont
	modeStyle, modeContStyle = t.Mode, t.ModeCont
	errStyle, dimStyle, searchStyle = t.Err, t.Dim, t.Search
	syntaxPal = t.Syntax()
}

func init() { setTheme(ui.New(nil, os.Stdin)) }

// resultMsg carries a finished evaluation back to the UI thread.
type resultMsg Result

// modalEditMsg carries back what a line submitted from inside a modal answered,
// and the view as it stands afterwards. Its own message because the answer must
// not take the ordinary path: that one prints, and printing under the alt
// screen is printing into a void.
type modalEditMsg struct {
	res  Result
	spec *ModalSpec
}

// bufferRunMsg carries back what the builtin editor asked for: a run of the
// document, or a check of it. Its own message for modalEditMsg's reason — the
// ordinary path prints, and printing under the alt screen is printing into a
// void — and it carries marks rather than text because a gutter needs line
// numbers and parsing them back out of a diagnostic would tie the view's
// coordinates to the shape of a sentence.
type bufferRunMsg struct {
	res   Result
	marks []bufMark
	check bool
}

// changeMsg carries a watcher observation to the UI thread. The watcher never
// touches Core; this is how its work reaches the one goroutine allowed to.
type changeMsg hostChange

// waitForChange blocks on the watcher's stream so a change arrives as an
// ordinary message, serialized with keystrokes and results like everything
// else the event loop sees. A nil channel — a Core that will never watch —
// blocks forever, which costs one parked goroutine and nothing else.
func waitForChange(ch <-chan hostChange) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return changeMsg(ev)
	}
}

type model struct {
	core    *Core
	in      textinput.Model
	sp      spinner.Model
	hist    *history
	version string

	// pending holds a partially typed multi-line construct.
	pending []string
	// pendingReload is a watcher observation that arrived mid-evaluation,
	// named the way it will be printed. Empty means none. Invariant 15: one
	// thing touches Core at a time, so the reload waits exactly as a
	// typed-ahead line does.
	pendingReload string
	// queue holds work submitted while an evaluation was still running.
	// Dropping it would silently merge the next line into the current input.
	// Each item is one evaluation: a typed line is a one-element item, a
	// pasted run of code constructs is one many-element item.
	queue [][]string
	busy  bool

	// Ctrl-R reverse search.
	searching bool
	query     string
	match     string

	// modal is the full-screen view, when one is open. While it is non-nil the
	// UI must not tea.Println: the alt screen swallows it. Whatever needed
	// saying is said once, on close, as ModalSpec.Summary.
	modal *modal
	// bufview is the builtin editor, when the reader has asked for one. It is
	// a view under the same alt-screen rule as modal and picker, and like them
	// it owns the keyboard entirely while it is up.
	bufview *bufferView
	// bufAsk is a run or check the open editor asked for that could not start
	// yet, because Core was busy. It waits exactly as modalEdit does.
	bufAsk *bufAsk
	// picker is the theme chooser, under the same alt-screen rule as modal.
	// The two are never open at once: each is opened from a Result, and Core
	// answers one line at a time.
	picker *picker
	// modalEdit is a change the open view asked for that could not start yet,
	// because Core was busy with a reload. It waits exactly as a typed-ahead
	// line does — invariant 15 is that one thing touches Core at a time.
	modalEdit string
	winW      int
	winH      int

	// hint is the callee's signature while the cursor is inside a call, drawn
	// beside the line. It goes nowhere near the suggestion list: it is never
	// accepted and never appended, which is what keeps it a hint.
	hint string

	// vi is modal editing, when it is on. It is UI state exactly like
	// searching, query and match: it lives on the goroutine that owns the
	// keyboard, and Core — which runs on the evaluation one and is not safe for
	// concurrent use — knows nothing about it. The zero value is off, so every
	// model built without a Core behaves exactly as it did before.
	vi vimState
}

func newModel(core *Core, version string) model {
	in := textinput.New()
	in.Prompt = promptStyle.Render(prompt)
	in.Focus()
	// The input is one line; multi-line constructs accumulate across submits.
	in.CharLimit = 0
	// Inline ghost text, accepted with tab. Up and down are intercepted below
	// for history and never reach the input, so the suggestion cycle keeps
	// ctrl-n and ctrl-p to itself.
	in.ShowSuggestions = true
	in.CompletionStyle = dimStyle

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = dimStyle

	// The terminal gets colour and tables; pipes keep Core's plain default.
	if core != nil {
		setTheme(ui.New(core.Config(), os.Stdin))
		in.Prompt = promptStyle.Render(prompt)
		in.CompletionStyle = dimStyle
		sp.Style = dimStyle
		// The terminal is the only place plugin renderers apply. Plain is a
		// compatibility surface that pipes and tests read — invariant 19.
		installRender(core, theme.Styles(), core.Config().ValueOptions())
	}

	m := model{core: core, in: in, sp: sp, hist: loadHistory(), version: version}
	if core != nil {
		// Only here: a model built with no Core is every existing model test,
		// and the zero vimState is what keeps those untouched.
		m = m.setInputMode(core.Config().InputMode())
	}
	return m
}

func (m model) Init() tea.Cmd {
	if m.core == nil {
		return textinput.Blink
	}
	return tea.Batch(textinput.Blink, waitForChange(m.core.Changes()))
}

// terminalOut is a result's bytes as a terminal shows them.
//
// Colouring is exclusive on purpose: an error message is prose, whatever the
// command that failed would have answered in. This is also the only place a
// Result's own bytes are ever coloured — msg.Out itself is untouched, and it is
// the field a pipe, gluon -e, the -json envelopes and every MCP tool read.
func terminalOut(msg resultMsg) string {
	out := msg.Out
	switch {
	case msg.Err:
		out = errStyle.Render(out)
	default:
		out = syntax.Highlight(msg.Lang, out, syntaxPal)
	}
	// :query -json is answered here, not refused: the table is what a terminal
	// can show, and one line says where the flag does something. Appended to
	// the styled form, so the table's own bytes are exactly what every other
	// driver prints.
	if msg.Query != nil {
		out += "\n" + dimStyle.Render(queryJSONNote)
	}
	return out
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case resultMsg:
		m.busy = false
		if msg.Quit {
			return m, tea.Quit
		}
		if msg.Clear {
			return m, tea.ClearScreen
		}
		if msg.Edit != "" {
			// The reader asked for gluon's own editor rather than a program.
			// Checked before EditorAt, which answers a different question —
			// *which* program — and would resolve an exported $EDITOR over a
			// choice written down in the config file.
			if BuiltinEditor() {
				return m.openBufferView(specForEdit(Result(msg)), nil)
			}
			cmd := EditorAt(msg.Edit, msg.EditLine)
			if cmd == nil {
				return m, tea.Println(errStyle.Render(
					"error: no editor found — set $EDITOR or $VISUAL"))
			}
			// ExecProcess hands the terminal over and takes it back, which is
			// the only safe way to run a full-screen editor under Bubble Tea.
			then := msg.EditThen
			return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
				if err != nil {
					return resultMsg{Out: "error: " + err.Error(), Err: true}
				}
				// A command that named a line to run afterwards gets that line
				// submitted, exactly as though it had been typed. Naming none
				// is :edit, which means reload — the behaviour every caller had
				// before there was a second thing an editor could be opened
				// for.
				if then != "" {
					return resultMsg(m.core.Submit(then))
				}
				return resultMsg(m.core.Reload(msg.Edit))
			})
		}
		// Typing while an evaluation ran produced no suggestions — Core cannot
		// be asked then. Now that it can, catch up on whatever was typed
		// ahead, or the line sits uncompletable until the next keystroke.
		m = m.suggest()
		var cmds []tea.Cmd
		// A palette the command chose is installed before anything is
		// printed, so the line saying the theme changed is the first thing
		// painted in it.
		if msg.Theme != nil && msg.Theme.Apply != "" {
			var err error
			m, err = m.applyTheme(msg.Theme.Apply, msg.Theme.Overrides)
			if err != nil {
				cmds = append(cmds, tea.Println(errStyle.Render("error: "+err.Error())))
			}
		}
		// A form the command chose is installed before anything is printed too,
		// so the value the next line answers with is drawn in the shape that
		// was just asked for rather than one line later.
		if msg.Values != nil {
			m = m.applyValues(msg.Values.Options)
		}
		// And the way keys are read, for the same reason: the next line is
		// typed with the bindings that were just asked for, not the ones the
		// line before it had.
		if msg.Input != nil {
			m = m.setInputMode(msg.Input.Mode)
		}
		// The picker supersedes the linear listing the same way a modal does,
		// and for the same reason.
		picking := msg.Theme != nil && msg.Theme.Apply == ""
		// A modal supersedes the linear rendering rather than adding to it:
		// Out exists so a pipe loses nothing, and the view's own Summary is
		// what scrollback keeps. Printing both would say the same thing twice.
		if msg.Out != "" && msg.Modal == nil && !picking && m.bufview == nil {
			// Println writes above the program and persists across renders,
			// which is what keeps a REPL's scrollback intact.
			cmds = append(cmds, tea.Println(terminalOut(msg)))
		}
		if msg.Modal != nil {
			return m.openModal(*msg.Modal, cmds)
		}
		if picking {
			return m.openPicker(*msg.Theme, cmds)
		}
		// Drain anything typed ahead while this was running.
		if len(m.queue) > 0 {
			next := m.queue[0]
			m.queue = m.queue[1:]
			var cmd tea.Cmd
			m, cmd = m.startEval(next, nil)
			cmds = append(cmds, cmd)
		}
		// A change the open view asked for while a reload was running starts
		// now, for the same reason and in the same place. Whether the view is
		// still open or not: the reader asked for it, and closing the screen
		// they asked from is not a way to take it back.
		if !m.busy && m.modalEdit != "" {
			line := m.modalEdit
			m.modalEdit = ""
			var cmd tea.Cmd
			m, cmd = m.startModalEdit(line)
			cmds = append(cmds, cmd)
		}
		// A change seen mid-evaluation reloads now that nothing is running.
		// It goes behind whatever was typed ahead: those lines were submitted
		// against the session as it stood, and the reload is what changes it.
		if !m.busy && m.pendingReload != "" {
			what := m.pendingReload
			m.pendingReload = ""
			var cmd tea.Cmd
			m, cmd = m.startReload(what)
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case modalEditMsg:
		m.busy = false
		// The palette and the form are installed before the view redraws, so
		// the screen that changed one is drawn in it — invariant 32, from the
		// one place a settings screen can move a colour without going through
		// the picker.
		if msg.res.Theme != nil && msg.res.Theme.Apply != "" {
			m, _ = m.applyTheme(msg.res.Theme.Apply, msg.res.Theme.Overrides)
		}
		if msg.res.Values != nil {
			m = m.applyValues(msg.res.Values.Options)
		}
		if msg.res.Input != nil {
			m = m.setInputMode(msg.res.Input.Mode)
		}
		var cmds []tea.Cmd
		if m.modal == nil {
			// The view was closed while its change was in flight. The change
			// still happened — it went through Core like any other line — and
			// scrollback is owed the sentence the view would have shown.
			if out := msg.res.Out; out != "" {
				if msg.res.Err {
					out = errStyle.Render(out)
				}
				cmds = append(cmds, tea.Println(out))
			}
		} else {
			mm := m.modal.restyle().settled(msg.res, msg.spec)
			m.modal = &mm
		}
		if m.modalEdit != "" {
			line := m.modalEdit
			m.modalEdit = ""
			var cmd tea.Cmd
			m, cmd = m.startModalEdit(line)
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case bufferRunMsg:
		m.busy = false
		var cmds []tea.Cmd
		if m.bufview == nil {
			// ZZ: the editor closed and asked for the document to be run on the
			// way out, which is what the external editor has always done —
			// ExecProcess's callback submits EditThen and the answer lands in
			// the transcript. So it prints, exactly as a typed line would.
			if out := msg.res.Out; out != "" {
				cmds = append(cmds, tea.Println(terminalOut(resultMsg(msg.res))))
			}
		} else {
			vv := m.bufview.restyle().settled(msg.res, msg.marks, msg.check)
			m.bufview = &vv
		}
		if m.bufAsk != nil {
			ask := *m.bufAsk
			m.bufAsk = nil
			var cmd tea.Cmd
			m, cmd = m.startBufferAsk(ask)
			cmds = append(cmds, cmd)
		}
		// Drain anything typed ahead, exactly as the ordinary result does.
		// A run holds Core for as long as a build takes, and a :q typed into
		// the prompt the moment the editor closed would otherwise wait for a
		// result that is never coming — this is the last message the session
		// sends, so nothing after it would drain the queue.
		if !m.busy && len(m.queue) > 0 {
			next := m.queue[0]
			m.queue = m.queue[1:]
			var cmd tea.Cmd
			m, cmd = m.startEval(next, nil)
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case changeMsg:
		// Re-arm first, whatever happens below: the watcher blocks until this
		// is read, so a listener not put back is a session that notices one
		// change and then goes deaf.
		var rearm tea.Cmd
		if m.core != nil {
			rearm = waitForChange(m.core.Changes())
		}
		if m.busy {
			m.pendingReload = msg.What
			return m, rearm
		}
		mm, cmd := m.startReload(msg.What)
		return mm, tea.Batch(cmd, rearm)

	case spinner.TickMsg:
		if !m.busy {
			return m, nil
		}
		var cmd tea.Cmd
		m.sp, cmd = m.sp.Update(msg)
		return m, cmd

	case tea.WindowSizeMsg:
		m.winW, m.winH = msg.Width, msg.Height
		if m.bufview != nil {
			vv, _, cmd := m.bufview.update(msg)
			m.bufview = &vv
			return m, cmd
		}
		if m.modal != nil {
			mm, _, cmd := m.modal.update(msg)
			m.modal = &mm
			return m, cmd
		}
		if m.picker != nil {
			pp, _ := m.picker.update(msg)
			m.picker = &pp
			return m, nil
		}
		return m, nil

	case tea.KeyMsg:
		// A view owns the keyboard entirely while it is open. The builtin
		// editor is first because it is the only one whose keys are modal ones:
		// a j that reached the prompt from in here would walk history.
		if m.bufview != nil {
			return m.updateBufferView(msg)
		}
		// A modal owns the keyboard entirely while it is open, and so does the
		// picker.
		if m.modal != nil {
			return m.updateModal(msg)
		}
		if m.picker != nil {
			return m.updatePicker(msg)
		}
		if m.searching {
			return m.updateSearch(msg)
		}
		// A bracketed paste arrives as ONE KeyMsg whose Runes include the
		// newlines — bubbletea does not interpret them further. textinput is
		// single-line, so without this the whole block collapses onto one
		// line. Single-line pastes fall through so the cursor position is
		// respected.
		if msg.Paste && strings.ContainsAny(string(msg.Runes), "\n\r") {
			mm, cmd := m.paste(string(msg.Runes))
			m = mm
			if m.vi.on() {
				// A multi-line paste submits lines and leaves the tail in the
				// input, so it starts a new line — and every new line begins in
				// insert, or a mode left over from the last one swallows the
				// beginning of the next.
				m.vi = m.vi.clearHistory()
				m = m.vimSetMode(vimInsert)
			}
			return m, cmd
		}
		if msg.Paste && m.vi.normal() {
			// A single-line paste is text whatever the mode, and it falls
			// through to textinput like any other paste. What it also is, in
			// normal mode, is a change to the line: recorded so u takes it
			// back, and clamped afterwards because normal mode's cursor sits
			// on a character rather than after one.
			m.vi = m.vi.record(m.vimBufOf())
			var cmd tea.Cmd
			m.in, cmd = m.in.Update(msg)
			return m.vimApply(m.vimBufOf().clampNormal()), cmd
		}
		// After the paste branch, deliberately: dd arriving from a clipboard is
		// two characters somebody wants in the line, and the paste branch is
		// the existing proof that a paste is distinguishable from typing. What
		// vimKey does not handle falls through to the switch below, so what
		// normal mode does *not* own stays readable in one place.
		if !msg.Paste {
			mm, cmd, handled := m.vimKey(msg)
			m = mm
			if handled {
				return m, cmd
			}
		}

		switch msg.Type {
		case tea.KeyCtrlD:
			if m.in.Value() == "" && len(m.pending) == 0 {
				return m, tea.Quit
			}

		case tea.KeyCtrlC:
			// Abandon what is being typed; never kill the session.
			if len(m.pending) > 0 || m.in.Value() != "" {
				m.pending = nil
				m.in.SetValue("")
				m.in.SetSuggestions(nil)
				m.hint = ""
				m.hist.reset()
				return m.setPrompt(), nil
			}
			return m, tea.Quit

		case tea.KeyCtrlL:
			// The universal terminal clear key, and the one that cooperates
			// with the renderer instead of corrupting its cursor arithmetic.
			return m, tea.ClearScreen

		case tea.KeyCtrlR:
			m.searching = true
			m.query, m.match = "", ""
			return m, nil

		case tea.KeyUp:
			if v, ok := m.hist.prev(m.in.Value()); ok {
				m.in.SetValue(v)
				m.in.CursorEnd()
			}
			return m.suggest(), nil

		case tea.KeyDown:
			if v, ok := m.hist.next(); ok {
				m.in.SetValue(v)
				m.in.CursorEnd()
			}
			return m.suggest(), nil

		case tea.KeyEnter:
			mm, cmd := m.submit()
			return mm, cmd
		}
	}

	var cmd tea.Cmd
	m.in, cmd = m.in.Update(msg)
	m = m.suggest()
	return m, cmd
}

// suggest refreshes the completion list from the current input.
//
// Two conditions, both load-bearing. Nothing is offered while an evaluation is
// running, because Core is not safe for concurrent use and completion
// type-checks the session. And nothing is offered unless the cursor is at the
// end of the line, because textinput appends an accepted suggestion there
// regardless of where the cursor actually is — completing mid-line would move
// text the user did not touch.
func (m model) suggest() model {
	if m.busy || m.searching || m.core == nil {
		return m
	}
	val := m.in.Value()
	// Normal mode is not typing, so there is nothing to complete: a candidate
	// beside a cursor that is being moved rather than extended would preview a
	// line that accepting it does not produce. It goes here rather than in the
	// busy branch above, which returns *without* clearing on purpose — getting
	// that wrong would leave a stale ghost on screen for a whole normal-mode
	// session.
	if m.vi.normal() || m.in.Position() != len([]rune(val)) {
		m.in.SetSuggestions(nil)
		m.hint = ""
		return m
	}
	m.in.SetSuggestions(m.core.Complete(val))
	// Complete first, so the callee is already resolved and this is the cache
	// hit the shared lookup was built for.
	m.hint = m.core.Hint(val)
	return m
}

// submit takes the current input line and hands it to submitLine.
func (m model) submit() (model, tea.Cmd) {
	line := m.in.Value()
	m.in.SetValue("")
	// The old list describes the line just submitted, and the session it was
	// built from is about to change. The hint describes it too.
	m.in.SetSuggestions(nil)
	m.hint = ""
	m.hist.reset()
	return m.submitLine(line)
}

// submitLine echoes a line into scrollback and either keeps reading (an
// unclosed construct) or hands the whole thing to Core.
func (m model) submitLine(line string) (model, tea.Cmd) {
	shown, style := prompt, promptStyle
	if len(m.pending) > 0 {
		shown, style = contPrompt, contStyle
	}
	echo := tea.Println(style.Render(shown) + echoOf(m.pending, line))

	m.pending = append(m.pending, line)
	buf := strings.Join(m.pending, "\n")

	// An empty line must not submit an unclosed construct, or a typo would be
	// inescapable. Ctrl-C is the way out.
	if session.IsIncomplete(buf) {
		return m.setPrompt(), echo
	}
	m.pending = nil
	m = m.setPrompt()

	src := strings.TrimSpace(buf)
	if src == "" {
		return m, echo
	}
	if !isQuit(src) {
		m.hist.add(flattenForHistory(src))
	}

	// Typing ahead during a ~270ms evaluation is normal. Queue rather than
	// drop: ignoring the Enter would leave the runes in the input and merge
	// this line into the next one.
	if m.busy {
		m.queue = append(m.queue, []string{src})
		return m, echo
	}
	mm, cmd := m.startEval([]string{src}, echo)
	return mm, cmd
}

// startEval kicks off an evaluation on a background command so the UI keeps
// accepting input while the toolchain runs. A one-element batch is an
// ordinary Submit; a longer one is a pasted run of code constructs.
func (m model) startEval(batch []string, extra tea.Cmd) (model, tea.Cmd) {
	m.busy = true
	core := m.core
	run := func() tea.Msg {
		if len(batch) == 1 {
			return resultMsg(core.Submit(batch[0]))
		}
		return resultMsg(core.SubmitBatch(batch))
	}
	work := tea.Batch(run, m.sp.Tick)
	if extra != nil {
		// The echo first, always. tea.Batch runs its commands at once, and a
		// line the checker answers in a millisecond could otherwise print its
		// answer above itself.
		return m, tea.Sequence(extra, work)
	}
	return m, work
}

// startReload runs the reload a watcher asked for, on the same background
// command an ordinary evaluation uses. It sets busy for the same reason: while
// it runs, Core is not to be asked anything else.
func (m model) startReload(what string) (model, tea.Cmd) {
	m.busy = true
	core := m.core
	run := func() tea.Msg {
		if core == nil {
			return resultMsg(Result{})
		}
		return resultMsg(core.ReloadOnChange(what))
	}
	return m, tea.Batch(run, m.sp.Tick)
}

// paste splits a multi-line paste into complete constructs and evaluates each
// run of code as one batch — ten pasted statements cost one build, not ten,
// and every one of them prints. A meta command flushes the run collected so
// far and executes on its own, because it answers against the session as the
// lines before it left it. The final line stays in the input unless the paste
// ended with a newline, so a pasted block can be read before it runs.
func (m model) paste(text string) (model, tea.Cmd) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")

	// Anything already typed prefixes the first pasted line.
	lines[0] = m.in.Value() + lines[0]
	m.in.SetValue("")
	m.hist.reset()

	tail := lines[len(lines)-1]
	lines = lines[:len(lines)-1]

	var cmds []tea.Cmd
	var run []string
	var items [][]string
	flush := func() {
		if len(run) > 0 {
			items = append(items, run)
			run = nil
		}
	}

	for _, line := range lines {
		// Echo with the same prompts typing would have shown, so the
		// transcript reads the same either way.
		shown, style := prompt, promptStyle
		if len(m.pending) > 0 {
			shown, style = contPrompt, contStyle
		}
		cmds = append(cmds, tea.Println(style.Render(shown)+echoOf(m.pending, line)))

		m.pending = append(m.pending, line)
		buf := strings.Join(m.pending, "\n")
		if session.IsIncomplete(buf) {
			m = m.setPrompt()
			continue
		}
		m.pending = nil
		m = m.setPrompt()

		src := strings.TrimSpace(buf)
		if src == "" {
			continue
		}
		if !isQuit(src) {
			m.hist.add(flattenForHistory(src))
		}
		if strings.HasPrefix(src, ":") {
			flush()
			items = append(items, []string{src})
			continue
		}
		run = append(run, src)
	}
	flush()

	if tail != "" {
		m.in.SetValue(tail)
		m.in.CursorEnd()
	}

	// In order: every echo is its own tea.Println, and tea.Batch prints them
	// in whatever order its commands finish — a pasted p.Dist2() above the
	// p := Point{3, 4} it needs, in the first screenshot of a paste.
	if len(items) == 0 {
		return m, tea.Sequence(cmds...)
	}
	m.queue = append(m.queue, items...)
	if !m.busy {
		next := m.queue[0]
		m.queue = m.queue[1:]
		var cmd tea.Cmd
		m, cmd = m.startEval(next, nil)
		cmds = append(cmds, cmd)
	}
	return m, tea.Sequence(cmds...)
}

func (m model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC, tea.KeyCtrlG:
		m.searching = false
		return m, nil
	case tea.KeyEnter:
		m.searching = false
		if m.match != "" {
			m.in.SetValue(m.match)
			m.in.CursorEnd()
		}
		return m, nil
	case tea.KeyCtrlR:
		// Repeated Ctrl-R walks to the next older match.
		m.match = m.hist.searchFrom(m.query, m.match)
		return m, nil
	case tea.KeyBackspace:
		if m.query != "" {
			m.query = m.query[:len(m.query)-1]
			m.match = m.hist.search(m.query)
		}
		return m, nil
	case tea.KeyRunes, tea.KeySpace:
		// KeySpace already carries Runes: []rune{' '}, so appending a literal
		// space as well typed every space twice. Taking the runes and nothing
		// else is the whole fix.
		m.query += string(msg.Runes)
		m.match = m.hist.search(m.query)
		return m, nil
	}
	return m, nil
}

// inputOpts is the palette the input line paints with, gathered from the
// package theme so that inputView itself stays a pure function of its
// arguments and a test can hand it whatever it likes.
func (m model) inputOpts() inputOpts {
	return inputOpts{
		Theme: theme, Pal: syntaxPal, Ghost: dimStyle,
		Pending: m.pending, Hint: m.hint, Width: m.winW,
		// Insert mode's cursor is gluon's own, so the mode decides it here
		// rather than the textinput deciding it once. Off and normal both leave
		// it false, and false is the cursor the prompt has always drawn.
		Insert: m.vi.mode == vimInsert,
	}
}

func (m model) View() string {
	if m.bufview != nil {
		return m.bufview.View()
	}
	if m.modal != nil {
		return m.modal.View()
	}
	if m.picker != nil {
		return m.picker.View()
	}
	if m.searching {
		return fmt.Sprintf("%s %s\n",
			searchStyle.Render(fmt.Sprintf("(reverse-i-search)`%s':", m.query)), m.match)
	}
	view := inputView(m.in, m.inputOpts())
	if m.busy {
		view += "\n" + m.sp.View() + dimStyle.Render(" evaluating")
	}
	return view + "\n"
}

// openModal enters the alt screen and hands the keyboard to the view. Nothing
// is printed from here until it closes.
func (m model) openModal(spec ModalSpec, cmds []tea.Cmd) (model, tea.Cmd) {
	w, h := m.winW, m.winH
	if w == 0 {
		// No WindowSizeMsg has arrived yet — tea sends one on start, but a
		// modal opened from a queued line can beat it.
		w, h = 80, 24
	}
	mm := newModal(spec, w, h)
	m.modal = &mm
	cmds = append(cmds, tea.EnterAltScreen)
	// In order, not in parallel. tea.Batch runs its commands concurrently and
	// their messages arrive in whatever order they finish, so a line printed
	// alongside EnterAltScreen is a coin flip on whether the alt screen has
	// already swallowed it. tea.Sequence puts them through the one channel the
	// event loop reads, in the order written here.
	return m, tea.Sequence(cmds...)
}

// updateModal drives the open view and, when it closes, leaves the single line
// that keeps scrollback a complete record of the session.
//
// A view that asked for a change hands back the line to run rather than running
// it: the model owns the goroutine Core may be touched from, and the command is
// submitted there exactly as a typed line is.
func (m model) updateModal(msg tea.Msg) (model, tea.Cmd) {
	mm, open, cmd := m.modal.update(msg)
	if open {
		if line := mm.run; line != "" {
			mm.run = ""
			m.modal = &mm
			var edit tea.Cmd
			m, edit = m.startModalEdit(line)
			return m, tea.Batch(cmd, edit)
		}
		m.modal = &mm
		return m, cmd
	}
	// The view as the closing key left it, not as it was before: the key that
	// closed it may be the one that chose a line for the prompt.
	summary, fill := mm.summary(), mm.fill
	m.modal = nil
	cmds := []tea.Cmd{tea.ExitAltScreen}
	if summary != "" {
		cmds = append(cmds, tea.Println(summary))
	}
	if fill != "" {
		// Ctrl-R's accept, exactly: the line replaces what was there, the
		// cursor goes to its end, and nothing is submitted. The reader's
		// Enter is the submission, as it is for a line found in history.
		m.in.SetValue(fill)
		m.in.CursorEnd()
		m.hist.reset()
		if m.vi.normal() {
			m = m.vimSetMode(vimInsert)
		}
		m = m.suggest()
	}
	// Sequence, for the reason openModal gives, and here it is the whole of
	// invariant 19's second half: batched, the summary reached the alt screen
	// about as often as it reached scrollback, and a screen that changed a
	// setting would leave a session with no record that anything had.
	return m, tea.Sequence(cmds...)
}

// openBufferView enters the alt screen with gluon's own editor in it.
//
// The document comes off disk rather than out of the Result, because the file
// is what every command already produces: :edit writes the session to a temp
// file, :scratch -edit hands over the pad itself, and :buf writes a scaffolded
// module. Reading it back here is what keeps one definition of what each of
// those means — the builtin editor and an external one open the same bytes.
func (m model) openBufferView(spec bufferSpec, cmds []tea.Cmd) (model, tea.Cmd) {
	data, err := os.ReadFile(spec.Path)
	if err != nil {
		return m, tea.Println(errStyle.Render("error: " + err.Error()))
	}
	w, h := m.winW, m.winH
	if w == 0 {
		// No WindowSizeMsg has arrived yet; openModal says why.
		w, h = 80, 24
	}
	vv := newBufferView(spec, string(data), w, h)
	m.bufview = &vv
	cmds = append(cmds, tea.EnterAltScreen)
	// Sequence and not Batch, for openModal's reason: a line printed alongside
	// EnterAltScreen is a coin flip on whether the alt screen swallowed it.
	return m, tea.Sequence(cmds...)
}

// updateBufferView drives the open editor and, when it closes, leaves the one
// line that keeps scrollback a complete record.
//
// The view hands back work rather than doing it, which is modal.run's rule and
// invariant 15's: Core belongs to the evaluation goroutine.
func (m model) updateBufferView(msg tea.Msg) (model, tea.Cmd) {
	vv, open, cmd := m.bufview.update(msg)
	ask := vv.ask
	vv.ask = nil

	if open {
		m.bufview = &vv
		if ask == nil {
			return m, cmd
		}
		var run tea.Cmd
		m, run = m.startBufferAsk(*ask)
		return m, tea.Batch(cmd, run)
	}

	summary := vv.summary()
	m.bufview = nil
	cmds := []tea.Cmd{tea.ExitAltScreen}
	if summary != "" {
		cmds = append(cmds, tea.Println(summary))
	}
	if ask == nil {
		// Closed without running. A file gluon wrote for the occasion is
		// gluon's to remove — which is the same line Reload draws, and by the
		// same test: a pad's session file is the durable thing itself and is
		// never a temp file.
		if vv.spec.Temp {
			os.Remove(vv.spec.Path)
		}
		return m, tea.Sequence(cmds...)
	}
	var run tea.Cmd
	m, run = m.startBufferAsk(*ask)
	// After ExitAltScreen, so the answer reaches scrollback rather than a
	// screen that is about to be torn down.
	return m, tea.Sequence(append(cmds, run)...)
}

// startBufferAsk writes the document and then runs or checks it, both on the
// goroutine evaluations already run on.
//
// The write is here rather than in the view for the same reason the evaluation
// is: it is I/O whose failure is an answer the view has to be told about, and
// the view's own goroutine is the one holding the keyboard.
func (m model) startBufferAsk(ask bufAsk) (model, tea.Cmd) {
	if m.core == nil {
		// No session to run anything in — a driver the tests build and nothing
		// production runs. Stashing the ask would leave it waiting for a Core
		// that is never coming.
		return m, nil
	}
	if m.busy {
		// Something else is already touching Core. This waits for it, and the
		// view sits on "running…" until it lands.
		m.bufAsk = &ask
		return m, nil
	}
	m.busy = true
	core := m.core
	return m, func() tea.Msg {
		if ask.check {
			// Nothing is written for a check: it reads the document it was
			// handed, and a file on disk is not part of the question.
			marks, err := core.CheckDocument(ask.text, ask.then == "")
			if err != nil {
				// Invariant 5. The checker declining is not a verdict, and the
				// view says so rather than claiming the document is good.
				return bufferRunMsg{check: true, res: Result{
					Out: "the checker could not say — " + err.Error(), Err: true}}
			}
			return bufferRunMsg{check: true, marks: marks}
		}
		if err := os.WriteFile(ask.path, []byte(ask.text), 0o644); err != nil {
			return bufferRunMsg{res: Result{Out: "error: " + err.Error(), Err: true}}
		}
		if ask.then != "" {
			return bufferRunMsg{res: core.Submit(ask.then)}
		}
		return bufferRunMsg{res: core.Reload(ask.path)}
	}
}

// startModalEdit runs a change the open view asked for, and rebuilds the view
// from what the config says afterwards.
//
// Two submissions on one background command, in that order: the change, and
// then the command the spec names as the way to build itself again. Both from
// the goroutine evaluations already run on, one at a time, so the rule Core is
// built on is the rule this follows. The second is where the new value in the
// table comes from — the screen reports the file, not what it hoped the file
// now says.
func (m model) startModalEdit(line string) (model, tea.Cmd) {
	if m.core == nil {
		// No session to change anything in, which is a driver the tests build
		// and nothing production runs. Stashing the line would leave it
		// waiting for a Core that is never coming.
		return m, nil
	}
	if m.busy {
		// Something else is already touching Core. This waits for it, and the
		// view sits on "applying…" until it lands.
		m.modalEdit = line
		return m, nil
	}
	m.busy = true
	core := m.core
	refresh := ""
	if m.modal != nil {
		refresh = m.modal.spec.Refresh
	}
	return m, func() tea.Msg {
		msg := modalEditMsg{res: core.Submit(line)}
		if refresh != "" {
			if fresh := core.Submit(refresh); fresh.Modal != nil {
				msg.spec = fresh.Modal
			}
		}
		return msg
	}
}

// applyTheme repaints a running session.
//
// Every style in this package is package-level and was set once at startup,
// which was the right shape while a palette could not change and is what makes
// changing one a single function now. The three places that hold a copy rather
// than reading those variables are the input, the spinner and Core's renderer,
// so those are re-set here — the prompt through setPrompt, which knows which of
// the prompts is owed and which mode's colour it is owed in.
//
// A theme that will not load is applied anyway and returned as an error: the
// palette ui.Named hands back is complete either way, so the session is never
// left half-painted, and the caller decides where to say so — a line in
// scrollback, or the picker's footer while the alt screen is up.
func (m model) applyTheme(name string, overrides map[string]string) (model, error) {
	t, err := ui.Named(name, overrides, theme.Colour)
	setTheme(t)

	m = m.setPrompt()
	m.in.CompletionStyle = dimStyle
	m.sp.Style = dimStyle
	if m.core != nil {
		// Carrying the form forward is the point: a theme change must not put
		// the shape of a value back to the default, which is exactly what a
		// second render-closure constructor would eventually do.
		installRender(m.core, theme.Styles(), m.core.Values)
	}
	return m, err
}

// installRender is the one place a render closure is built.
//
// Invariant 32 names four things that hold a copy of the palette rather than
// reading the package-level styles, and the renderer is one of them. It now
// holds a copy of the value form as well, so there are two things to carry and
// two constructors would be two places to forget one of them — which is how
// switching a theme would silently put every value back in a bordered table.
func installRender(c *Core, st pretty.Styles, values pretty.Options) {
	if c == nil {
		return
	}
	// The hooks are read when a value is drawn, never when the closure is
	// built. They change whenever a plugin activates — at -host, at :use, at
	// :get, when a scratchpad restores its modules — and every one of those
	// happens after the renderer is first installed, so a copy taken here
	// kept the renderers of a session with no third-party plugins for good.
	// Render is called on the evaluation goroutine, which is the one that
	// writes c.hooks, so reading it there is the ownership invariant 15 asks.
	values.Hooks = nil
	c.Render = func(v []pretty.Value) string {
		opts := values
		opts.Hooks = c.Hooks()
		return pretty.RichIn(v, st, opts)
	}
	c.Rich, c.Styles, c.Values = true, st, values
}

// RenderAsTerminal makes c draw what it draws for a terminal — values in the
// given form, the plugins' renderers, tables — in the styles st. The TUI does
// this with the theme's colours, from the config at start and from a result's
// Values when a command changes the form. The docs' session recorder does the
// same with pretty.PlainStyles, so a guide's transcript is the terminal's text
// without its colour rather than the one-line form a pipe gets.
func (c *Core) RenderAsTerminal(st pretty.Styles, values pretty.Options) {
	installRender(c, st, values)
}

// applyValues redraws in another shape. It goes through installRender for the
// reason above: the palette in force has to survive a form change exactly as
// the form has to survive a theme change.
func (m model) applyValues(values pretty.Options) model {
	installRender(m.core, theme.Styles(), values)
	return m
}

// openPicker enters the alt screen with the theme chooser. Like a modal it
// prints nothing while it is up.
func (m model) openPicker(spec ThemeSpec, cmds []tea.Cmd) (model, tea.Cmd) {
	w, h := m.winW, m.winH
	if w == 0 {
		w, h = 80, 24
	}
	pp := newPicker(spec, w, h)
	m.picker = &pp
	cmds = append(cmds, tea.EnterAltScreen)
	return m, tea.Sequence(cmds...)
}

// updatePicker drives the chooser: moving repaints, enter keeps, esc puts back.
//
// Keeping is done by running `:theme <name>` as though it had been typed,
// rather than by writing the config from here. One path decides what switching
// means — validate, save, report the line it wrote — and the transcript ends up
// with the same line either way, which is what makes the picker a shortcut for
// the command rather than a second implementation of it.
func (m model) updatePicker(msg tea.Msg) (model, tea.Cmd) {
	pp, action := m.picker.update(msg)
	m.picker = &pp

	switch action {
	case pickPreview:
		var err error
		m, err = m.applyTheme(pp.selected(), pp.spec.Overrides)
		pp.warn = ""
		if err != nil {
			pp.warn = err.Error()
		}
		m.picker = &pp
		return m, nil

	case pickKeep:
		name := pp.selected()
		m.picker = nil
		cmds := []tea.Cmd{tea.ExitAltScreen}
		if name == "" {
			return m, tea.Batch(cmds...)
		}
		return m.startEval([]string{":theme " + name}, tea.Batch(cmds...))

	case pickCancel:
		start := pp.start
		m.picker = nil
		m, _ = m.applyTheme(start, pp.spec.Overrides)
		// The one line the alt screen owes scrollback: a session that was
		// repainted four times while somebody looked around should say where
		// it ended up.
		return m, tea.Sequence(tea.ExitAltScreen,
			tea.Println(dimStyle.Render("theme unchanged · "+start)))
	}
	return m, nil
}

func runTUI(start Start) error {
	core, err := NewCore()
	if err != nil {
		return err
	}
	defer core.Close()

	// newModel first, because it is what resolves the palette from config
	// (setTheme, above) — and a startup screen drawn before the theme is the
	// unstyled one this replaced.
	m := newModel(core, start.Version)
	if err := writeStartup(os.Stdout, core, start); err != nil {
		return err
	}

	// No alt screen: a REPL's value is the normal screen's scrollback, and
	// tea.Println is a no-op while the alt screen is active.
	p := tea.NewProgram(m)
	final, err := p.Run()
	if fm, ok := final.(model); ok {
		fm.hist.save()
	}
	return err
}

// padName resolves which scratchpad to open, or "" for none.
//
// The command line beats the file, and both can say none: -no-scratch for this
// run, `pad = "-"` for every run. gluon's own default is the pad called
// default, which is what makes "start typing and come back tomorrow" the
// behaviour somebody gets without having configured anything.
func padName(start Start, cfg *config.Config) string {
	if start.NoPad {
		return ""
	}
	if start.Pad != "" {
		return start.Pad
	}
	name := ""
	if cfg != nil {
		name = strings.TrimSpace(cfg.Scratch.Pad)
	}
	switch name {
	case "":
		return "default"
	case "-", "off":
		return ""
	}
	return name
}

// writeStartup draws the screen a session opens with, and does the work it
// reports on the way.
//
// The order is the order the facts become true, which is also the order they
// cause each other, and it is why the wordmark is printed before any of them:
// attaching walks a go.mod and opening a scratchpad replays it, so a screen
// that waited to know everything before drawing anything would be a blank
// terminal for as long as the slowest of them took. The masthead goes out
// first, the scratchpad row says what it is doing, and the row becomes the
// answer where it stands.
//
// It is here rather than in banner.go because everything in that file is a
// function of facts alone; this is the one that has the Core.
func writeStartup(w io.Writer, core *Core, start Start) error {
	form := core.Config().BannerForm()
	width := termWidth()
	if form == bannerCompact {
		// bannerHead draws the one-line masthead when it is told there is no
		// room, which is the same thing compact asks for at any width. Saying
		// it this way means there is one narrow layout rather than two that
		// have to be kept looking like each other.
		width = 1
	}
	// full is the only form with a fact block, and narrowness does not change
	// that. A terminal too narrow for the wordmark loses the wordmark — that is
	// bannerHead's own decision, made from the width — and keeps the rows,
	// because the rows are the half that is about this session and they are
	// thirty columns wide. Dropping to compact instead would answer a question
	// about a picture by throwing the text away.
	rows := form == bannerFull
	loud := form != bannerOff

	if loud {
		f := bootFacts{Version: shortVersion(start.Version), Go: goVersion()}
		fmt.Fprint(w, bannerHead(theme, f, width)+"\n")
	}

	// -host attaches without building the package index: see Core.Attach. That
	// is what makes this step quiet enough to need no row of its own.
	if start.HostDir != "" {
		if _, err := core.Attach(start.HostDir); err != nil {
			return err
		}
	}

	// printed tracks whether any row actually landed, because the blank line
	// under the block belongs to the block. A pad that will not open withdraws
	// the row it drew, and a blank left under nothing would be the same kind of
	// leftover as the progress line this screen exists to stop leaving.
	printed := false

	// The pad is opened here, synchronously, before the program starts. Core
	// runs on a background command and cannot print, and a replay costs a
	// build — so a session with forty entries in it would otherwise sit at an
	// empty prompt for a second with nothing to say for itself.
	var res Result
	var po PadOpen
	if name := padName(start, core.Config()); name != "" {
		finish := func(string) {}
		if rows {
			finish = pending(w, theme, "scratchpad", "opening "+name+"…", width)
		}
		res, po = core.OpenPad(name)
		value := padValue(theme, po)
		finish(value)
		printed = rows && value != ""
	}

	if rows {
		b := core.Boot()
		var rest bootFacts
		rest.add("host", hostValue(theme, po, b))
		rest.add("database", b.Database)
		rest.add("plugins", b.Plugins)
		rest.add("theme", themeValue(theme, b))
		for _, r := range rest.Rows {
			fmt.Fprint(w, bannerRow(theme, r.label, r.value, width)+"\n")
			printed = true
		}
	}

	if loud {
		if printed {
			fmt.Fprint(w, "\n")
		}
		fmt.Fprint(w, bannerHints(theme)+"\n")
	}

	// A pad that would not open is reported whatever the setting says. A
	// session that believes it is being saved and is not is the failure the
	// scratchpad exists to avoid — invariant 34 — and it is not decoration to
	// turn down. Out carries the whole reason, sometimes a path and a line to
	// type, so it is printed as it stands rather than folded into a row.
	if po.Failed {
		fmt.Fprintf(w, "%s\n\n", res.Out)
	}
	return nil
}

// padValue is the scratchpad row: the pad's name, then what opening it did.
// The name is not repeated inside the clause, because the label already said
// "scratchpad" and saying it twice on consecutive lines was the complaint.
func padValue(t ui.Theme, po PadOpen) string {
	if po.Name == "" || po.Failed {
		return ""
	}
	out := po.Name
	if po.State != "" {
		out += t.Dim.Render(" · " + po.State)
	}
	for _, n := range po.Notes {
		out += t.Dim.Render(" · " + n)
	}
	return out
}

// hostValue prefers what the pad just attached over what was already there:
// they are the same module whenever both are set, and the pad is the one that
// knows why.
func hostValue(t ui.Theme, po PadOpen, b Boot) string {
	path := po.Host
	if path == "" {
		path = b.Host
	}
	if path == "" {
		return ""
	}
	if po.HostWhy != "" {
		return path + t.Dim.Render(" · "+po.HostWhy)
	}
	return path
}

// themeValue names the palette, and says so in the error colour when the one
// that was asked for is not the one in use.
func themeValue(t ui.Theme, b Boot) string {
	if b.Theme == "" {
		return ""
	}
	if b.ThemeErr != "" {
		return t.Err.Render(b.Theme)
	}
	return b.Theme
}
