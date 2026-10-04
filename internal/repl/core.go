// Package repl drives the interactive session. The session logic lives in
// Core, which is driver-agnostic: the Bubble Tea UI and the piped-input loop
// both feed it complete constructs and render whatever it returns.
package repl

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/config"
	"github.com/sandboxws/gluon/internal/db"
	"github.com/sandboxws/gluon/internal/eval"
	"github.com/sandboxws/gluon/internal/gluonrt"
	"github.com/sandboxws/gluon/internal/host"
	"github.com/sandboxws/gluon/internal/inspect"
	"github.com/sandboxws/gluon/internal/plugin"
	"github.com/sandboxws/gluon/internal/pretty"
	"github.com/sandboxws/gluon/internal/render"
	"github.com/sandboxws/gluon/internal/scratch"
	"github.com/sandboxws/gluon/internal/session"
	"github.com/sandboxws/gluon/internal/syntax"
	"github.com/sandboxws/gluon/internal/ui"
)

const (
	prompt     = "gluon> "
	contPrompt = "  ...> "
)

// Core owns the session and the evaluator. It is not safe for concurrent use;
// the UI runs at most one Submit at a time.
type Core struct {
	ev   *eval.Evaluator
	sess *session.Session
	// Render turns the values a program described into display text. The TUI
	// swaps in the colour-and-tables renderer; pipes keep the plain one, whose
	// shape scripts depend on.
	Render func([]pretty.Value) string
	// Rich reports that output goes to a terminal, so the inspector may draw
	// tables and colour. Pipes get the plain form, whose shape scripts depend
	// on just as much as they do on Render's.
	Rich bool
	// Styles is the palette the inspector shares with the value renderer.
	Styles pretty.Styles
	// Values is the shape values are drawn in — a copy of what the driver
	// installed, so :settings can report it and applyTheme can carry it
	// forward. Core does not act on it: Render is the only thing that draws,
	// and only a driver sets Render. That is what keeps a form out of
	// pretty.Plain, which is what a pipe, `gluon -e` and the -json envelopes
	// read.
	Values pretty.Options
	// cfg is the loaded configuration. It is never nil.
	cfg *config.Config
	// comp caches what completion needs between keystrokes.
	comp completions
	// gen counts session mutations, so the completion caches know when the
	// scope they were built from has gone.
	gen int
	// timing reports where each line's latency went, for :time.
	timing bool
	// unsaved holds a setting that is in force but did not reach the file, so
	// :settings reports it as the session's rather than as configured. A write
	// that failed is not undone — the value you asked for is the value you get
	// — but it must not then be reported as something the next session will
	// see.
	unsaved map[string]string

	// extra is the commands active plugins contribute. It is rebuilt only when
	// the build list changes — :get and :use — never per line.
	extra []Command
	// metaNames caches the completion list derived from extra plus builtins.
	metaNames []string
	// plugins is the library-aware layer: renderers, commands and preloads
	// that apply only when the session can actually see the library.
	plugins *plugin.Set
	// hooks is the type -> renderer map the rich renderer consults.
	hooks map[string]pretty.Hook
	// pluginErrs are the config plugins that would not load, reported by
	// :plugins rather than swallowed.
	pluginErrs []error

	// detected caches what :db found by reading the project's files.
	//
	// Detection is file I/O, so like plugin activation it must never run per
	// line — invariant 22. It is computed on the first :db or :query and
	// dropped where refreshPlugins already runs, which is :get and :use: the
	// two points where what the session can see actually changes.
	detected     *db.Detection
	detectedFrom string

	// tests are the tests :test has generated this session, held rather than
	// written. :test prints; :save -test is what puts them on disk, because a
	// command that dropped files under the user's data directory unasked is
	// the posture invariant 25 rules out.
	tests []generated

	// marks are the snapshots :bookmark and :branch have taken, for this
	// process only. Held on Core rather than in a package variable because two
	// Cores in one process — the tests build several — are two sessions, and a
	// snapshot of one is not a snapshot of the other.
	marks bookmarks

	// pending is a share that has been shown and not yet answered — the exact
	// bytes and the token the view submits to publish them. Held on Core
	// rather than in a package variable for the reason marks is: two Cores in
	// one process are two sessions, and a confirmation given in one is not a
	// confirmation given in the other.
	pending *pendingShare

	// pad is the scratchpad this session is written to, or nil for a session
	// nothing writes down — `gluon -e`, the MCP server and the piped loop, none
	// of which install one. Held on Core rather than in a package variable for
	// the reason marks and pending are: two Cores in one process — the tests
	// build several — are two sessions, and the pad one of them is in is not
	// the pad the other is in.
	pad *scratchpad
	// padDepth counts how deep in a submission the flush is, so a pasted batch
	// writes the pad once rather than once per line. SubmitBatch calls Submit
	// for the single case and submitEach calls it N times, so the count is the
	// only thing that can tell the outermost defer from the inner ones.
	//
	// No mutex: invariant 15 says one goroutine touches Core, and persist runs
	// in that goroutine's defer — after evaluation, and never on the keystroke
	// path.
	padDepth int

	// buf is the :buf buffer: Go source the user is composing, which has not
	// been evaluated and may never be.
	//
	// Not on session.Session, and deliberately. A Session is what has run, and
	// session.Marshal writes it to a scratchpad; a text buffer in there would
	// give invariant 34's worst failure — open, replay, empty session, forty
	// lines replaced by one — a second way to happen, against text that never
	// compiled in the first place. On Core rather than in a package variable
	// for the reason marks and pending are: two Cores in one process are two
	// sessions, and one of them is not editing the other's buffer.
	buf string
	// bufDir is the module :buf scaffolds for an editor to open, one per
	// process, created on first use and removed by Close.
	//
	// One directory rather than one per open because that is most of what "the
	// buffer persists" means to the hands: nvim's undofile, its marks and `:e#`
	// all key on the path. gluon never builds it — the user's language server
	// does, in their editor — so invariant 1 is not in play here.
	bufDir string
	// bufScaffold records that the file now in bufDir is the scaffolded shape,
	// with the two marked regions in it, so reading it back knows whether to
	// insist on finding them.
	//
	// Remembered rather than sniffed from the file, because the two answers a
	// sniff could give are both wrong when it guesses: refusing a file that was
	// never scaffolded loses the buffer, and accepting a scaffolded file whose
	// markers somebody deleted takes the whole rendered session — line
	// directives, printer wrappers and all — as the thing they meant to write.
	bufScaffold bool
	// bufWritten is the exact file last handed to an editor, kept so that
	// reading it back can tell a region edit from a context edit by comparing
	// against what was given rather than against a preamble rendered again.
	bufWritten string
	// bufErrLine is the buffer line the last check or run reported, so the next
	// open lands on it. Zero when the last answer was clean, which is what
	// makes the loop terminate on its own.
	bufErrLine int
	// bufCodeLine is the first line of the statements region in the file last
	// written, so an open with nothing to correct still lands the cursor
	// somewhere typing works.
	bufCodeLine int

	// watcher polls the attached host for source changes, when :watch is on.
	// It is nil the rest of the time: a loop that can never fire is not
	// started.
	watcher *watcher
	// changes is what the watcher writes and a driver with an event loop
	// reads. Created once, never closed and never replaced, so a driver that
	// took the channel at start-up keeps receiving across :watch off and on.
	changes chan hostChange
}

// host is the attached module, or nil — including when there is no evaluator
// at all, which is what a Core built without NewCore looks like.
func (c *Core) host() *host.Host {
	if c == nil || c.ev == nil {
		return nil
	}
	return c.ev.Host()
}

// Result is what one submitted construct produced.
type Result struct {
	Out  string
	Err  bool
	Quit bool
	// Clear asks the UI to wipe the screen. A terminal-side clear (Ghostty's
	// cmd+k) fights Bubble Tea's relative repainting, so gluon owns its own.
	Clear bool
	// Edit is a file the driver should open in the user's editor, then hand
	// back through Reload. Only a driver attached to a terminal can do that,
	// so each one answers for itself rather than dropping it silently.
	Edit string
	// EditThen is the line the driver submits once that editor exits. Empty
	// keeps :edit exactly as it is: hand the file back through Reload.
	//
	// A line rather than a second boolean, and a line rather than a callback,
	// for the reason ModalSpec.Refresh and ModalConfirm.Run are lines: Core
	// belongs to the evaluation goroutine, so a driver holding a closure into
	// it would be reading the session from the wrong one. It is also a line
	// the user could have typed, which is what keeps one path deciding what
	// running a buffer means.
	EditThen string
	// EditLine is the line to open that editor on, or 0 for the top of the
	// file. It is how a buffer that would not compile reopens on the construct
	// that would not, rather than leaving the reader to find it again.
	EditLine int
	// Watch reports that the command asked for host watching, which only a
	// driver with an event loop can deliver. A driver without one answers for
	// itself the way it answers Edit, rather than the command silently
	// succeeding at nothing.
	Watch bool
	// Modal is a view the driver may open full-screen. It is advisory: Out is
	// always set to the same information in linear form, so a driver that
	// cannot go full-screen — a pipe, gluon -e — prints that instead and
	// nothing is lost. Core never opens anything itself; it runs on a
	// background goroutine and must not touch the terminal.
	Modal *ModalSpec
	// Lang names the language Out is written in, when Out is source rather
	// than prose.
	//
	// It is a tag, and it changes no bytes. Out stays exactly what a pipe,
	// gluon -e, the -json envelopes and an MCP tool read; only the TUI — the
	// one driver that knows the terminal takes colour — paints it on the way
	// to tea.Println. Carrying a second, coloured string instead would be a
	// second definition of what the command answered, which is the shape
	// invariants 19 and 21 exist to refuse.
	//
	// Set it only when the whole of Out is one language.
	Lang syntax.Lang
	// Theme asks the driver to change gluon's colours, or offers it the
	// choice. Advisory in the same way Modal is: Out already says the same
	// thing in linear form, so a pipe, `gluon -e` and an MCP caller lose
	// nothing by ignoring it — they were never going to paint anything.
	Theme *ThemeSpec
	// Values reports that the shape values are drawn in changed, so a driver
	// that installed a renderer has to install another. Advisory in the same
	// way Theme and Modal are: Out already says what changed in linear form,
	// so a pipe and `gluon -e` lose nothing by ignoring it — they were never
	// going to redraw anything.
	Values *ValueSpec
	// Input reports that the way the prompt reads keys changed, so a driver
	// that owns a keyboard has to install the new one. Advisory in the same way
	// Theme and Values are: Out already says what changed in linear form, so a
	// pipe, `gluon -e` and the MCP server lose nothing by ignoring it — none of
	// them has a keyboard to read.
	Input *InputSpec
	// Query is the structured answer `:query -json` produces, for a driver
	// that emits JSON rather than a table. nil for every other command, and
	// for :query without the flag.
	//
	// Advisory in the same way Modal is: Out already carries the same rows
	// linearly, so the TUI and an MCP caller lose nothing by ignoring it. What
	// it spares the drivers that do want it is parsing the table back out of
	// Out, which would tie the envelope to the table's layout — the one thing
	// the flag exists to avoid.
	Query *QueryData
}

// QueryData is a result set in the shape a program reads.
//
// db.Result is what the child sent and is carried whole rather than copied
// field by field: the column names, the type each was declared with, the cells,
// which of them were SQL NULL, whether the fetch cap bound, and any note the
// layer attached are all already there, and a second spelling of them would be
// a second definition of what a query answered.
type QueryData struct {
	db.Result
	// Target is the redacted connection string, and the only form of it that
	// leaves this process. The resolved DSN reaches the child through its
	// environment and appears in no field here — invariants 24 and 25.
	Target string
}

// InputSpec is the way the prompt should read keys, for a driver that has one.
//
// A string rather than the mode type, because Core has no business holding a
// keyboard state machine: it names what config named, and the driver decides
// what that means for its own key handling.
type InputSpec struct{ Mode string }

// ValueSpec is the new shape, for a driver that holds a renderer.
type ValueSpec struct {
	// Options is copied rather than shared: a driver installs it from its own
	// goroutine and Core belongs to the evaluation one — invariant 15.
	Options pretty.Options
}

// ModalSpec describes a full-screen view without naming a widget. Core stays
// free of bubbles this way, and the two drivers can disagree about what to do
// with it.
//
// The rule the TUI implements: enter the alt screen on open, print nothing
// while it is up — tea.Println is a no-op there — and on close leave exactly
// one Summary line in scrollback, so the session transcript stays complete.
type ModalSpec struct {
	Title string
	// Summary is the single line scrollback keeps after the view closes.
	Summary string
	// Headers and Rows make a table. When Headers is empty, Text is paged
	// instead.
	Headers []string
	Rows    [][]string
	Text    string
	// Note is shown in the footer: what the view is not showing, and why.
	Note string
	// Lang is Result.Lang for the paged text. It applies only when Headers is
	// empty: a table's cells are values, never source.
	Lang syntax.Lang
	// Entries, when set, is one per row: what opening that row shows, and what
	// choosing something inside it would run. A spec without them is the
	// browsable table gluon has always drawn, which is every other view.
	Entries []ModalEntry
	// Confirm is the decision the whole view exists to ask, for a view that
	// asks one. Entries are per row — open this setting, choose that value —
	// and a question about everything on screen is not a row.
	//
	// It is a line the driver submits, for the reason Entries gives: Core
	// belongs to the evaluation goroutine, so a view holding a callback into
	// it would be reading the session from the wrong one. A driver that cannot
	// go full-screen ignores it and prints Out, which says the same thing and
	// names the same line.
	Confirm *ModalConfirm
	// Refresh is the command that builds this view again, for a driver that
	// changed something from inside it. It is how the screen ends up reporting
	// the file rather than what it hoped the file now says — and it is a
	// command rather than a callback because Core belongs to the evaluation
	// goroutine, and a driver holding a closure into it would be reading the
	// session from the wrong one. Empty for a view of something the session
	// computed, which nothing can change while it is open.
	Refresh string
	// Opened is the row, by title, the view opens on: a question about one
	// row lands on it rather than on the table. Backing out of that row
	// closes the view, because the reader came in through it — esc leaves the
	// way they came.
	Opened string
}

// A ModalConfirm is the whole view's yes.
type ModalConfirm struct {
	// Label is what the footer calls it — "bring these in", not "confirm": a
	// key that says what it does is the difference between a decision made and
	// one taken on trust.
	Label string
	// Run is the line the driver submits when the reader accepts.
	Run string
}

// A ModalEntry is one row of a table, opened.
//
// It is the difference between a screen that reports settings and one that
// changes them, and it is deliberately made of strings: the command a choice
// runs is decided here, by the thing that knows what a setting is, and the
// driver submits that line as though it had been typed. One path still decides
// what changing a setting means — validate, write, apply, report the line it
// wrote — which is the argument internal/repl/picker.go makes about handing a
// theme back to `:theme` rather than writing the config from inside a widget.
type ModalEntry struct {
	// Title names the row this belongs to. Entries and Rows are parallel.
	Title string
	// Text is the explanation, in the words `:settings <key>` prints — one
	// builder, so a screen and a prompt cannot say different things about the
	// same setting.
	Text string
	// Choices is the closed set, when there is one.
	Choices []ModalChoice
	// Current is the label of the choice in force, marked in the list.
	Current string
	// Prefix is set when the value is open rather than chosen: the command a
	// typed value completes, ":settings timeout ". Empty means there is
	// nothing to type.
	Prefix string
	// Typed is what that input opens with — the value in force, so a small
	// edit is an edit rather than a retype.
	Typed string
	// Why is the reason this row cannot be changed at all, when it cannot. A
	// row that opened onto nothing, with no reason given, would read as a
	// broken screen rather than as a setting gluon keeps in a file.
	Why string
	// More is the command that prints all of Text at the prompt, named where
	// the text is longer than its room.
	More string
}

// A ModalChoice is one value a setting may take, or one setting behind a row
// that stands for many.
type ModalChoice struct {
	// Label is the value, as it would be typed.
	Label string
	// About is the line it describes itself by, when it has one.
	About string
	// Run is the line the driver submits when this is chosen.
	Run string
	// Open is another entry to open instead of running anything: theme.<role>
	// is one row and fifteen settings, and a chooser that could not reach them
	// would be a screen that lists a setting it cannot open.
	Open *ModalEntry
	// Fill is a line to put on the prompt, unrun, closing the view. It is how
	// an example leaves the help view: an example may fetch, write or publish,
	// so the reader's Enter at the prompt is the submission, as it is for a
	// line Ctrl-R finds. Invariant 19 holds by construction — nothing is
	// submitted from inside the view.
	Fill string
}

// ThemeSpec is what a driver needs to repaint itself, and what it needs to let
// somebody choose. Core decides which themes exist and what the config says;
// which of them the terminal can show, and what a preview of one looks like, is
// the driver's business and stays there.
type ThemeSpec struct {
	// Apply names the palette to switch to now. Empty means the command only
	// listed them, and a driver that can offer a choice may.
	Apply string
	// Choices is every theme that can be chosen, sorted, each with the line
	// it describes itself by.
	Choices []ThemeChoice
	// Active is the theme in force when the command ran.
	Active string
	// Overrides is the per-role colours the config sets on top of whichever
	// theme is chosen, copied rather than shared. A driver previewing a
	// palette does it from its own goroutine, and Core belongs to the
	// evaluation one — invariant 15 — so it gets a copy or it gets a race.
	Overrides map[string]string
}

// A ThemeChoice is one selectable palette: its name, and the line it describes
// itself by. Where the file lives is deliberately not here — `gluon theme` is
// the report about files, and this is a list to move a highlight through.
type ThemeChoice struct {
	Name  string
	About string
	// Appearance is the ground the theme was drawn for: "dark", "light" or
	// "either". gluon paints no background, so this is the one thing a list has
	// to say that looking at the palette cannot tell you.
	Appearance string
}

func NewCore() (*Core, error) {
	cfg, err := config.Load()
	if err != nil {
		// A config that does not parse is reported, not ignored: a preloaded
		// import that silently did nothing would be the harder bug.
		return nil, err
	}
	return newCore(cfg)
}

func newCore(cfg *config.Config) (*Core, error) {
	ev, err := eval.New()
	if err != nil {
		return nil, err
	}
	ev.SetTimeout(cfg.EvalTimeout(eval.DefaultTimeout))
	ev.SetLimits(cfg.ValueLimits(gluonrt.DefaultMaxItems, gluonrt.DefaultMaxDepth))
	ev.SetPreload(cfg.ImportsFor(""))
	c := &Core{
		ev: ev, sess: &session.Session{}, Render: pretty.Plain, cfg: cfg,
		Values: cfg.ValueOptions(),
		// Buffered by one, so a change observed while the driver is between
		// events is held rather than making the watcher wait a full tick.
		changes: make(chan hostChange, 1),
	}
	c.initPlugins()
	c.startStdIndex()
	return c, nil
}

// Config is what the session was configured with, for :help and gluon doctor.
func (c *Core) Config() *config.Config { return c.cfg }

// Boot is what a startup screen has to say about the session it is starting.
//
// Every field is empty or zero where there is nothing to say, because the
// screen draws a row only where there is. That rule is the difference between a
// screen and a form: "database  none" repeated every morning is furniture, and
// furniture is what the banner this replaces was made of.
type Boot struct {
	// Host is the attached module's path, and empty when standalone.
	Host string
	// Database is a driver and where its connection string is named — never
	// the string. Invariant 25 is a property of the type rather than of anyone
	// remembering it: a password cannot reach a field built from a driver name
	// and a file name.
	Database string
	// Plugins names the active plugins that are not the standard library's.
	// The stdlib ones are active in every session, so counting them would
	// print a number that never varies and therefore says nothing; what is
	// worth a row is that this project's build list activated something.
	Plugins string
	// Theme is the palette's name, and ThemeErr is why it is not the one that
	// was asked for. Both are empty on a session that chose nothing and got
	// what everybody gets.
	Theme, ThemeErr string
}

// Boot gathers the startup screen's facts in one call.
//
// One call rather than four accessors because each is Core's to answer and a
// driver reaching in four times is four chances to read a session the
// evaluation goroutine owns. It is called from runTUI, in the same window
// before tea.NewProgram that Attach and OpenPad already run in.
//
// What may appear here is bounded by what costs neither a build nor a walk.
// The host is whatever is already attached. The plugins are a slice the set
// already holds — refreshPlugins ran at NewCore and again inside the open that
// just finished. The database is the configured entry where there is one, and
// only otherwise the detected one, which is the same order :db resolves in and
// means a project that configured its database pays nothing to have it named.
func (c *Core) Boot() Boot {
	var b Boot
	if h := c.ev.Host(); h != nil {
		b.Host = h.Path
	}
	b.Plugins = pluginsFact(c.plugins)
	// Configured entries only, and deliberately not Core.detect().
	//
	// Detection reads the project's files, and invariant 22 permits that here —
	// opening a scratchpad is one of the three points it names. What stopped it
	// was not the rule but the measurement: BenchmarkDetect puts the walk at
	// 2ms over twelve directories, 13.5ms over a hundred and 66ms over twelve
	// hundred, because it is bounded by depth and by maxFiles rather than by
	// anything that stays small. A budget of 15ms breaks at about a hundred and
	// twenty directories, which is an ordinary service repository, and spending
	// that in front of a prompt to name a database nobody asked about is the
	// trade gluon makes nowhere else.
	//
	// So the row says what is configured, which is free, and :db still detects
	// when it is asked — which is what detect's own comment has always said it
	// is for.
	b.Database = configuredDB(c.databases())
	if c.cfg != nil {
		name := c.cfg.ThemeName
		_, err := ui.NewWithError(c.cfg, nil)
		b.Theme = themeFact(name, err)
		if err != nil {
			b.ThemeErr = err.Error()
		}
	}
	return b
}

// configuredDB names a [[database]] entry the way a row wants it: the driver,
// and where the connection string is named rather than what it says.
func configuredDB(dbs []config.Database) string {
	switch len(dbs) {
	case 0:
		return ""
	case 1:
		d := dbs[0]
		out := d.Driver
		if out == "" {
			out = "database"
		}
		switch {
		case d.DSNEnv != "":
			out += " · from $" + d.DSNEnv
		case d.DSNFile != "":
			out += " · from " + shortPath(d.DSNFile)
		case d.File != "":
			out += " · from " + shortPath(d.File)
		}
		return out
	default:
		// Invariant 13: gluon does not pick between two databases, and a
		// startup screen is not the place to start. It says how many there
		// are, and :db is where choosing happens.
		return plural(len(dbs), "database") + " configured"
	}
}

func (c *Core) Close() error {
	// The goroutine holds only a channel and a directory, but a session that
	// has gone should not still be stat-ing a tree — nor running a go command.
	c.StopWatch()
	c.stopStdIndex()
	if c.bufDir != "" {
		// The buffer's text is the session's; the module around it was only
		// ever scaffolding for an editor to open.
		os.RemoveAll(c.bufDir)
		c.bufDir = ""
	}
	return c.ev.Close()
}

// Attach points the session at the module containing dir. It is what the -host
// flag does, and it is :use minus the package index.
//
// The difference is the point, and use's own comment is where it is argued: the
// index is a ~0.3s walk of the host's whole tree, built on first use, and ":use
// is one" because the count and the names are half of what somebody typing it
// asked for. -host is not typed at a prompt — it is on the command line of a
// session that has not started — so it is exactly the "startup that attaches
// without being typed" that comment says stops paying. Behind eval's indexOnce
// the walk still happens once, on the first line that needs it; this only moves
// it off the startup screen, where it would have been 0.3s of a screen with
// nothing yet to say.
//
// It returns :use's first line and not its second. The first names the module,
// its go directive and where it is, which is what the piped driver writes to
// stderr and what a reader of that stream needs. The second is the package
// count, and there is no count without the walk — which is the point.
func (c *Core) Attach(dir string) (string, error) {
	h, err := host.Detect(dir)
	if err != nil {
		return "", err
	}
	if err := c.ev.UseHost(h); err != nil {
		return "", err
	}
	// A host rule can add imports, and the host's own requirements may activate
	// a plugin — the same two reasons use refreshes, and invariant 22's second
	// point.
	c.refreshPlugins()
	return "attached to " + h.String(), nil
}

// Submit handles one complete construct: a meta command or Go source. It is
// called from a goroutine, so it must not touch UI state.
func (c *Core) Submit(src string) (out Result) {
	src = strings.TrimSpace(src)
	if src == "" {
		return Result{}
	}
	c.padDepth++
	defer c.flushPad(&out)
	// Every path from here can change the session, and a completion built from
	// the old one would offer a name that is no longer bound.
	c.gen++
	if strings.HasPrefix(src, ":") {
		return c.meta(src)
	}
	c.clock()
	res := c.eval(src)
	if c.timing {
		if p := c.phases(); p != "" {
			if res.Out == "" {
				res.Out = p
			} else {
				res.Out += "\n" + p
			}
		}
	}
	return res
}

func (c *Core) eval(src string) Result {
	entry, err := session.Classify(src)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	c.sess.Append(entry)

	res, err := c.ev.Eval(c.sess)
	if err != nil {
		// A line that does not compile must not poison the session.
		c.sess.Pop()
		return Result{Out: "error: " + err.Error() + c.pinNote(err.Error()), Err: true}
	}

	out := c.format(res.Output)
	if res.ExitCode != 0 {
		// A non-zero exit means the program panicked or called os.Exit, and
		// unlike a compile error that entry is not rolled back. Because the
		// whole session replays, it then happens on every later line — with
		// the message itself muted as replayed output, so nothing at all would
		// be printed. Saying so is the difference between an obvious problem
		// and a REPL that has quietly stopped answering.
		if out == "" {
			return Result{
				Out: fmt.Sprintf("error: the session exits with status %d before reaching this line — "+
					"an earlier entry panics or calls os.Exit. :undo it, or :reset", res.ExitCode),
				Err: true,
			}
		}
		return Result{Out: out + fmt.Sprintf("\n[exit status %d]", res.ExitCode), Err: true}
	}
	return Result{Out: out}
}

// SubmitBatch handles several complete code constructs that arrived together
// — a paste, a piped script — as one evaluation: ten pasted statements cost
// one build, not ten, and every one of them prints. Meta commands do not
// belong here; drivers run those through Submit, flushing the batch collected
// so far, because a meta answers against the session as the lines before it
// left it.
func (c *Core) SubmitBatch(srcs []string) (done Result) {
	c.padDepth++
	defer c.flushPad(&done)

	clean := make([]string, 0, len(srcs))
	for _, src := range srcs {
		if s := strings.TrimSpace(src); s != "" {
			clean = append(clean, s)
		}
	}
	if len(clean) == 0 {
		return Result{}
	}
	if len(clean) == 1 {
		return c.Submit(clean[0])
	}
	c.gen++

	c.clock()
	first, err := c.appendBatch(clean)
	if err != nil {
		// One unclassifiable construct means the batch cannot be appended
		// wholesale. Land them one at a time instead, exactly as typing
		// would have: the good ones stay, the bad one reports.
		return c.submitEach(clean)
	}
	res, err := c.ev.EvalFrom(c.sess, first)
	if err != nil {
		// The batch does not compile as a whole. Roll all of it back and land
		// the constructs one at a time, so the failure surfaces on the entry
		// that owns it and the others still run.
		c.sess.Entries = c.sess.Entries[:first]
		return c.submitEach(clean)
	}

	out := c.format(res.Output)
	var result Result
	switch {
	case res.ExitCode != 0 && out == "":
		result = Result{
			Out: fmt.Sprintf("error: the session exits with status %d before reaching this line — "+
				"an earlier entry panics or calls os.Exit. :undo it, or :reset", res.ExitCode),
			Err: true,
		}
	case res.ExitCode != 0:
		result = Result{Out: out + fmt.Sprintf("\n[exit status %d]", res.ExitCode), Err: true}
	default:
		result = Result{Out: out}
	}
	if c.timing {
		if p := c.phases(); p != "" {
			if result.Out == "" {
				result.Out = p
			} else {
				result.Out += "\n" + p
			}
		}
	}
	return result
}

// appendBatch classifies srcs and appends them as one run, returning the
// index the batch starts at. Classification happens for all of them before
// any is appended, so a failure leaves the session untouched.
func (c *Core) appendBatch(srcs []string) (first int, err error) {
	entries := make([]session.Entry, 0, len(srcs))
	for _, src := range srcs {
		e, err := session.Classify(src)
		if err != nil {
			return 0, err
		}
		entries = append(entries, e)
	}
	first = len(c.sess.Entries)
	for _, e := range entries {
		c.sess.Append(e)
	}
	return first, nil
}

// OneShot is the raw outcome of one batched evaluation. gluon -e formats its
// own output — its plain and -json shapes predate Core and are compatibility
// surfaces — so it needs the parsed pieces, not Submit's rendering. Groups
// holds one value list per printed entry, in entry order.
type OneShot struct {
	UserOut  string
	Groups   [][]pretty.Value
	ExitCode int
}

// EvalBatch evaluates code constructs as one batch and returns the raw
// outcome. On any error the whole batch is rolled back and the error
// returned; the caller owns how to report it.
func (c *Core) EvalBatch(srcs []string) (OneShot, error) {
	// The depth is kept even though nothing here can report what the flush
	// says: `gluon -e` installs no pad, so the flush is a decrement and a
	// return — and a path that skipped the counter would leave it wrong for
	// whatever ran next.
	c.padDepth++
	defer c.flushPad(nil)

	c.gen++
	first, err := c.appendBatch(srcs)
	if err != nil {
		return OneShot{}, err
	}
	res, err := c.ev.EvalFrom(c.sess, first)
	if err != nil {
		c.sess.Entries = c.sess.Entries[:first]
		return OneShot{}, err
	}
	userOut, groups := pretty.ParseGroups(res.Output)
	return OneShot{UserOut: userOut, Groups: groups, ExitCode: res.ExitCode}, nil
}

// submitEach is the degraded batch path: constructs land one at a time, each
// with the semantics typing it would have had. Errors do not stop the ones
// after — a paste is not a transaction — and the per-line error text points
// at the line that owns it.
func (c *Core) submitEach(srcs []string) Result {
	var outs []string
	anyErr := false
	for _, src := range srcs {
		res := c.Submit(src)
		if res.Out != "" {
			outs = append(outs, res.Out)
		}
		anyErr = anyErr || res.Err
	}
	return Result{Out: strings.Join(outs, "\n"), Err: anyErr}
}

// format splits what the program printed itself from the values it described,
// then renders each value group on its own line: a batch's entries are
// separate lines, while one entry returning a tuple stays on one.
func (c *Core) format(raw string) string {
	userOut, groups := pretty.ParseGroups(raw)
	out := strings.TrimRight(userOut, "\n")
	if len(groups) == 0 {
		return out
	}
	rendered := make([]string, 0, len(groups))
	for _, g := range groups {
		rendered = append(rendered, c.Render(g))
	}
	joined := strings.Join(rendered, "\n")
	if out == "" {
		return joined
	}
	return out + "\n" + joined
}

func (c *Core) meta(src string) Result {
	fields := strings.Fields(src)
	name := fields[0]
	arg := strings.TrimSpace(strings.TrimPrefix(src, name))

	cmd, ok := c.lookup(name)
	if !ok {
		// A command whose plugin is not active still has a page, and asking
		// for it with --help is asking for that page — the same one :help
		// prints — rather than a line saying the command cannot run.
		if d, dormant := c.dormant(name); dormant && cmdspec.AsksForHelp(d.Usage.Kind, arg) {
			return c.helpResult(d)
		}
		return c.unknownCommand(name)
	}
	// Help is answered here, before Run, once for every command. Asking has to
	// be safe, and it was not: :scratch -h opened a pad named h, :undo -h
	// dropped an entry and :q -h quit, because each command read the token as
	// its own argument. Invariant 35.
	if cmdspec.AsksForHelp(cmd.Usage.Kind, arg) {
		return c.helpResult(cmd)
	}
	res := cmd.Run(c, arg)
	if res.Err && strings.HasPrefix(res.Out, "usage:") {
		// A usage line is the moment somebody is looking for the rest of a
		// command's grammar, so it says where the rest is.
		res.Out += "\n" + cmd.Name + " --help lists its flags and examples"
	}
	if res.Err && cmd.Usage.Kind == cmdspec.GoExpr && (arg == "-h" || arg == "-help") {
		// -h was Go here — the negation of h — and it did not compile. The
		// likelier reading is the one that was not taken, so say where it is.
		res.Out += "\n" + cmd.Name + " --help is its help; " + arg + " was read as Go"
	}
	return res
}

// undo is :undo — drop the last entry and re-render.
func (c *Core) undo() Result {
	if len(c.sess.Entries) == 0 {
		return Result{Out: "error: nothing to undo", Err: true}
	}
	c.sess.Pop()
	if _, err := c.ev.Eval(c.sess); err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	n := len(c.sess.Entries)
	return Result{Out: fmt.Sprintf("undone (%d %s left)", n, plurals(n, "entry", "entries"))}
}

// get is :get — add a third-party module to the session.
//
// It is a command and not a fallback because an automatic `go get` would mutate
// go.mod, reach the network and take seconds without being asked, on a line the
// user may simply have mistyped. Nothing else gluon does leaves the machine.
func (c *Core) get(arg string) Result {
	arg = strings.TrimSpace(arg)
	// -rm is :get because removal changes the build list, and the build list
	// is what decides which plugins are active — invariant 22 puts that change
	// at :get and :use and nowhere else. A separate command would be a third
	// place activation could move.
	if rest, ok := cutFlag(arg, "-rm"); ok {
		return c.remove(rest)
	}
	if arg == "" {
		reqs, err := c.ev.Requires()
		if err != nil {
			return Result{Out: "error: " + err.Error(), Err: true}
		}
		if len(reqs) == 0 {
			return Result{Out: "no modules added — :get <module> to add one"}
		}
		return Result{Out: strings.Join(reqs, "\n")}
	}

	// A plugin may know this name: `:get uuid` is github.com/google/uuid. The
	// plugin that knows it is by definition not active — the module is not in
	// the build list yet — so aliases come from every known plugin, not the
	// active ones.
	arg = c.resolveGetAlias(arg)

	before, _ := c.ev.Requires()
	out, err := c.ev.Get(arg)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	// The build list changed, so the completion caches describe a session that
	// no longer exists.
	c.gen++
	// ...and a plugin whose module just arrived becomes active.
	c.refreshPlugins()
	// The module's root package is the one `uuid.` means, and the moment a
	// user most wants to see what arrived is the moment nothing is loaded yet.
	// Reading its export data costs a `go list -deps -export` — ~150ms, which
	// is a stall on the keystroke path and nothing at all off it.
	c.ev.Warm(modulePath(arg))

	// Report the requirements that appeared, with their resolved versions —
	// which is the thing worth knowing and the thing the argument does not say.
	after, _ := c.ev.Requires()
	added := diffRequires(before, after)
	switch {
	case len(added) > 0:
		return Result{Out: "added " + strings.Join(added, "\n      ")}
	case out != "":
		return Result{Out: strings.TrimSpace(out)}
	default:
		return Result{Out: arg + " was already required"}
	}
}

// modulePath is a :get argument without its version suffix, which is what the
// import path of the module's root package is.
func modulePath(arg string) string {
	if i := strings.Index(arg, "@"); i >= 0 {
		return arg[:i]
	}
	return arg
}

// remove is :get -rm — drop a requirement the session added.
//
// Nothing is fetched: the refusal is read off the session gluon already has,
// and the removal itself runs with the proxy off. :get remains the only
// command that leaves the machine.
func (c *Core) remove(mod string) Result {
	if mod == "" {
		return Result{Out: "usage: :get -rm <module>", Err: true}
	}
	// The same alias table :get adds by. `:get -rm uuid` has to undo what
	// `:get uuid` did, under the name it was typed with.
	// A version says which one to fetch; there is only ever one to remove.
	mod = modulePath(c.resolveGetAlias(mod))

	// Refuse before anything is written, so a refusal can be fixed with :drop
	// and the command retried against a session that is still exactly as it
	// was.
	if ns := c.ev.Importers(c.sess, mod); len(ns) > 0 {
		return Result{Out: fmt.Sprintf(
			"error: %s is imported by entry %s — :drop %s first, or :reset",
			mod, joinInts(ns), joinInts(ns)), Err: true}
	}

	changed, err := c.ev.Remove(mod)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	// The build list changed, so the completion caches describe a session that
	// no longer exists...
	c.gen++
	// ...and a plugin whose module just left is no longer active.
	c.refreshPlugins()

	if len(changed) == 0 {
		return Result{Out: mod + " is no longer required"}
	}
	return Result{Out: strings.Join(changed, "\n")}
}

// reset is :reset — clear the session, and with -deps its modules too.
//
// Bare :reset leaves the requirements in place deliberately: a session cleared
// to try the same library a different way should not have to fetch it again.
// -deps is for the other case, where the modules are what you are clearing.
func (c *Core) reset(arg string) Result {
	arg = strings.TrimSpace(arg)
	deps := arg == "-deps"
	if arg != "" && !deps {
		return Result{Out: "usage: :reset [-deps]", Err: true}
	}
	c.sess.Reset()
	if !deps {
		return Result{Out: "session cleared"}
	}

	// Removal goes through the one path :get -rm uses rather than rewriting
	// go.mod, so the standalone and attached cases stay identical and go.sum
	// stays the toolchain's to keep consistent.
	reqs, err := c.ev.Requires()
	if err != nil {
		return Result{Out: "session cleared\nerror: " + err.Error(), Err: true}
	}
	var removed, failed []string
	for _, r := range reqs {
		mod, _, _ := strings.Cut(r, " ")
		changed, err := c.ev.Remove(mod)
		switch {
		case errors.Is(err, eval.ErrHostRequires), errors.Is(err, eval.ErrNotRequired):
			// The host's requirements are the host's, and a module an earlier
			// removal already took with it is not a failure.
		case err != nil:
			failed = append(failed, mod+": "+err.Error())
		default:
			removed = append(removed, changed...)
		}
	}
	c.gen++
	c.refreshPlugins()

	out := "session cleared"
	if len(removed) > 0 {
		out += "\n" + strings.Join(removed, "\n")
	}
	if len(failed) > 0 {
		return Result{Out: out + "\n" + strings.Join(failed, "\n"), Err: true}
	}
	return Result{Out: out}
}

// cutFlag reports whether arg begins with the flag as a whole word, and
// returns what follows it.
//
// Prefix matching alone would read `:get -rmdir/x` as a removal of `dir/x`,
// which is a module path a fetch could plausibly be asked for.
func cutFlag(arg, flag string) (rest string, ok bool) {
	if arg == flag {
		return "", true
	}
	if r, found := strings.CutPrefix(arg, flag+" "); found {
		return strings.TrimSpace(r), true
	}
	return arg, false
}

// diffRequires is the requirements in after that were not in before.
func diffRequires(before, after []string) []string {
	had := make(map[string]bool, len(before))
	for _, r := range before {
		had[r] = true
	}
	var out []string
	for _, r := range after {
		if !had[r] {
			out = append(out, r)
		}
	}
	return out
}

// use is :use — attach the session to a surrounding Go module so lines can
// import its packages, including the ones under internal/.
//
// It is opt-in rather than automatic because attaching changes what a bare
// qualifier means. `trace.New(...)` in an attached session is the host's
// tracer; in a standalone one it is whatever goimports finds. A REPL that
// silently reached into the surrounding repo would make that indistinguishable.
func (c *Core) use(arg string) Result {
	if arg == "-off" || arg == "off" {
		if c.ev.Host() == nil {
			return Result{Out: "not attached to a host"}
		}
		if err := c.ev.UseHost(nil); err != nil {
			return Result{Out: "error: " + err.Error(), Err: true}
		}
		// The build list changed with the module, so which plugins apply may
		// have changed too.
		c.refreshPlugins()
		return Result{Out: "detached — the session is standalone again" + c.recheck()}
	}

	h, err := host.Detect(arg)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	if err := c.ev.UseHost(h); err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	// The index is built on first use, and :use is one: the count and the
	// names are half of what this command is for, so it asks straight away and
	// pays exactly what it always paid. A startup that attaches without being
	// typed is what stops paying.
	ix, err := c.ev.Index()
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	// A host rule can add imports, so the preload set is host-dependent — and
	// the host's own requirements may activate a plugin.
	c.refreshPlugins()

	out := fmt.Sprintf("attached to %s\n  %d importable package(s)", h, ix.Total())
	if names := ix.Names(); len(names) > 0 {
		out += ": " + strings.Join(names, ", ")
	}
	return Result{Out: out + c.recheck()}
}

// recheck re-runs a non-empty session against the module that just changed, so
// :use reports a break immediately instead of leaving it for the next line.
// The attach is not rolled back on failure: the user asked for it, and :use
// -off is right there.
func (c *Core) recheck() string {
	if len(c.sess.Entries) == 0 {
		return ""
	}
	if _, err := c.ev.Eval(c.sess); err != nil {
		return "\n  the existing session no longer builds: " + err.Error()
	}
	return ""
}

// describe is :t — Ruby's .class. It type-checks and never builds, so it
// answers in about a millisecond.
//
// -d is the exception and the only thing here that costs a run: what is inside
// an interface value is not in the type checker's gift, which is what the hint
// under a bare :t has been admitting since v2. It is asked for explicitly for
// that reason.
func (c *Core) describe(arg string) Result {
	verbose, dynamic, arg, err := parseTypeArgs(arg)
	if err != nil {
		return Result{Out: err.Error(), Err: true}
	}

	target, err := c.resolve(arg)
	if err != nil {
		if !dynamic {
			return Result{Out: "error: " + err.Error(), Err: true}
		}
		// Invariant 5: a checker that could not answer does not get to decide
		// whether the run happens. The static half says unavailable and the
		// measured half is reported anyway.
		return c.dynamicType(arg, "", false)
	}
	if !dynamic {
		return Result{Out: target.Describe(verbose, c.Styles)}
	}

	// Refused before anything runs. All three are known statically, and the
	// build failure each would produce — a type used as a value,
	// multiple-value in single-value context — names the rewrite rather than
	// the mistake.
	switch {
	case target.IsType:
		return Result{Out: "error: " + arg + " is a type, and a type has no dynamic type — " +
			"ask -d of a value of it", Err: true}
	case len(target.Tuple) > 1:
		return Result{Out: "error: :t -d takes one value; " + arg + " returns " +
			strconv.Itoa(len(target.Tuple)), Err: true}
	case target.Void:
		return Result{Out: "error: " + arg + " returns nothing, so there is no value to look inside",
			Err: true}
	}
	return c.dynamicType(arg, target.Name(), !target.IsInterface())
}

// leadingFlag matches one of the given flags at the front of arg, either alone
// or followed by a space.
//
// The word boundary is the point: a Go expression routinely begins with a
// unary minus, and `:t -dx` is a negation of dx rather than -d applied to x.
// parseBenchArgs reads :bench's flags by the same rule and for the same
// reason.
func leadingFlag(arg string, flags ...string) (flag, rest string, ok bool) {
	arg = strings.TrimSpace(arg)
	for _, f := range flags {
		if arg == f {
			return f, "", true
		}
		if strings.HasPrefix(arg, f+" ") {
			return f, strings.TrimSpace(arg[len(f):]), true
		}
	}
	return "", arg, false
}

// typeUsage is :t's usage line.
const typeUsage = "usage: :t [-v|-d] <expression>"

// parseTypeArgs reads :t's flags off the front of its argument and hands back
// the expression verbatim. It does nothing else, so the tests can hold it to
// what :t declares without a session.
func parseTypeArgs(arg string) (verbose, dynamic bool, expr string, err error) {
	for {
		flag, rest, ok := leadingFlag(arg, "-v", "-d")
		if !ok {
			break
		}
		arg = rest
		if flag == "-v" {
			verbose = true
		} else {
			dynamic = true
		}
	}
	switch {
	case arg == "":
		return false, false, "", errors.New(typeUsage)
	case verbose && dynamic:
		return false, false, "", errors.New("error: -v describes the static type and -d runs for " +
			"the dynamic one; give one\n" + typeUsage)
	}
	return verbose, dynamic, arg, nil
}

// dynamicType runs the expression once, transiently, and reports what was
// actually inside it beside what the checker said statically.
//
// static is "" when the checker could not answer, and concrete says the two
// halves cannot differ. Neither decides whether the run happens.
func (c *Core) dynamicType(arg, static string, concrete bool) Result {
	entry := session.Entry{Kind: session.KindExpr, Src: dynamicSource(arg)}
	// EvalLive, not EvalTransient, for the reason evalOpts.live records. The
	// cache is keyed on program text alone, so asking the same question twice
	// in an unchanged session would answer the second from the first run — and
	// -d exists to report what a run found, not what a run once found. It
	// snapshots imports, resolved and healthy and pops the entry exactly as
	// EvalTransient does, so invariant 14 holds unchanged.
	res, err := c.ev.EvalLive(c.sess, entry, nil, nil, nil)

	// The static half cost nothing and is still half the answer, so a failure
	// to build or a panic on the way is reported under it rather than instead
	// of it.
	head := ""
	if static != "" {
		head = c.Styles.Annot.Render("static ") + c.Styles.Type.Render(static) + "\n"
	}
	if err != nil {
		return Result{Out: head + "error: " + err.Error(), Err: true}
	}

	userOut, vals := pretty.Parse(res.Output)
	if len(vals) != 1 {
		// A panic leaves the traceback here rather than in err — the program
		// built and ran, it just did not get as far as describing anything.
		out := strings.TrimRight(userOut, "\n")
		if out == "" {
			out = "error: no type came back"
		}
		return Result{Out: head + out, Err: true}
	}
	d := parseDynamic(vals[0].Repr)

	var b strings.Builder
	// Whatever the expression itself printed comes first, as it does on any
	// other line: -d ran it, and suppressing its output would hide that.
	if out := strings.TrimRight(userOut, "\n"); out != "" {
		b.WriteString(out + "\n")
	}
	b.WriteString(inspect.DescribeDynamic(static, concrete, d, c.Styles))
	return Result{Out: b.String()}
}

// dynamicSource is :t -d's rewrite — one Sprintf over the expression, which is
// exactly the line the hint tells the reader to type.
//
// The value is bound to a name first so an expression with a side effect runs
// once rather than twice. reflect.ValueOf(...).Kind() rather than
// reflect.TypeOf(...).Kind() because TypeOf returns a nil Type for a nil
// interface and calling Kind on it panics, while ValueOf reports Invalid —
// which is the nil check, without an `== nil` the compiler rejects for a value
// type. The fields travel \x1f-separated the way :err's chain and :trace's
// frames do, which keeps the child describing and gluon formatting.
func dynamicSource(arg string) string {
	return "func() string {\n" +
		"\t__gluonD := any(" + arg + ")\n" +
		"\treturn fmt.Sprintf(\"%T\\x1f%s\", __gluonD, reflect.ValueOf(__gluonD).Kind())\n" +
		"}()"
}

// parseDynamic reads the pair the child sent back.
//
// reflect reports a nil interface as the Invalid kind and fmt prints its %T as
// <nil>; both mean the same thing, and it is not a type. reflect also spells a
// pointer "ptr", where every other inspector in gluon says pointer.
func parseDynamic(repr string) inspect.Dynamic {
	name, kind, _ := strings.Cut(repr, "\x1f")
	if kind == "invalid" || name == "<nil>" {
		return inspect.Dynamic{Nil: true}
	}
	if kind == "ptr" {
		kind = "pointer"
	}
	return inspect.Dynamic{Type: pretty.GoTypeName(name), Kind: kind}
}

// methods is :m — Ruby's .methods, plus which interfaces the type satisfies.
func (c *Core) methods(arg string) Result {
	if arg == "" {
		return Result{Out: "usage: :m <expression or type>", Err: true}
	}
	target, err := c.resolve(arg)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	if target.Type == nil {
		return Result{Out: "error: no type to take a method set of", Err: true}
	}
	if inspect.Uninstantiated(target.Type) {
		return needsInstantiation(arg, "its method set keeps T until it is instantiated",
			":m "+inspect.InstantiationForm(target.Pkg, target.Type))
	}

	res, err := c.ev.Analyze(c.sess, arg)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	ifaces := inspect.Interfaces(res, c.ev.Lookup)
	ms, sat, ptrSat := target.Methods(ifaces)

	if c.Rich {
		return Result{Out: inspect.RenderMethods(target, ms, sat, ptrSat, c.Styles)}
	}
	return Result{Out: inspect.PlainMethods(target, ms, sat, ptrSat)}
}

// benchOpts is what :bench's leading flags asked for. The zero value is not
// valid — count is 1 for a bare :bench — so it is only ever built by
// parseBenchArgs.
type benchOpts struct {
	// count is how many times to run the benchmark at each parallelism value.
	count int
	// cpus are the GOMAXPROCS values to run at. nil leaves it alone, which is
	// what every :bench that named no -cpu list does.
	cpus []int
	// profile keeps the run's CPU profile instead of discarding it.
	profile bool
}

// plain reports whether these options ask for exactly what :bench did before
// any of them existed: one run, at whatever parallelism the child starts with.
// That case must render byte for byte as it always has.
func (o benchOpts) plain() bool { return o.count == 1 && len(o.cpus) == 0 }

// runs is how many measurements the generated program will take.
func (o benchOpts) runs() int {
	if len(o.cpus) == 0 {
		return o.count
	}
	return o.count * len(o.cpus)
}

// parseBenchArgs splits :bench's leading flags from the expression.
//
// Flags are read only while they lead, exactly as parseQueryArgs does for SQL
// (db.go): a Go expression routinely contains a bare -, and everything from
// the first non-flag word on is the expression verbatim. Nothing is rewritten.
//
// The error names the flag rather than only the value, because the flag is
// what the reader has to go back and look at.
func parseBenchArgs(arg string) (benchOpts, string, error) {
	opts := benchOpts{count: 1}
	rest := strings.TrimSpace(arg)
	for {
		flag, tail, ok := leadingBenchFlag(rest)
		if !ok {
			return opts, rest, nil
		}
		rest = tail
		switch flag {
		case "-profile":
			opts.profile = true
		case "-count":
			var v string
			v, rest = cutWord(rest)
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return benchOpts{}, "", errors.New("-count wants a positive whole number, not " + shown(v))
			}
			opts.count = n
		case "-cpu":
			var v string
			v, rest = cutWord(rest)
			cpus, err := parseCPUList(v)
			if err != nil {
				return benchOpts{}, "", err
			}
			opts.cpus = cpus
		}
	}
}

// leadingBenchFlag matches a flag at the front of rest, either alone or
// followed by a space. Matching the bare form too is what turns `:bench -count`
// into an error naming -count rather than a build failure on an expression
// nobody wrote.
func leadingBenchFlag(rest string) (flag, tail string, ok bool) {
	for _, f := range []string{"-count", "-cpu", "-profile"} {
		if rest == f {
			return f, "", true
		}
		if strings.HasPrefix(rest, f+" ") {
			return f, strings.TrimSpace(rest[len(f):]), true
		}
	}
	return "", rest, false
}

// cutWord takes the next whitespace-delimited word and returns the remainder.
func cutWord(s string) (word, rest string) {
	i := strings.IndexFunc(s, unicode.IsSpace)
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimSpace(s[i:])
}

// shown quotes a flag value for an error, and names the empty one rather than
// printing "" at the reader.
func shown(v string) string {
	if v == "" {
		return "nothing"
	}
	return strconv.Quote(v)
}

// parseCPUList reads -cpu's comma-separated list, the same spelling go test
// uses. A list with no spaces is the documented form because the value is one
// word and the expression follows it.
func parseCPUList(v string) ([]int, error) {
	bad := errors.New("-cpu wants a comma-separated list of positive whole numbers, not " + shown(v))
	if v == "" {
		return nil, bad
	}
	var out []int
	for _, f := range strings.Split(v, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil || n < 1 {
			return nil, bad
		}
		out = append(out, n)
	}
	return out, nil
}

// benchSource is the expression :bench evaluates.
//
// It returns [][5]int64 — one row per run, holding the parallelism it ran at
// and the four numbers testing.BenchmarkResult has already divided.
// inspect.BenchRuns is the other half of that contract. Four already-divided
// scalars come back rather than the BenchmarkResult itself, whose T field is a
// time.Duration the encoder renders as "1.19s" — a string gluon would have to
// parse back. This keeps the child describing and gluon formatting.
//
// Three details in the measured closure each matter, and none of them changed
// when the flags arrived or when the second expression did:
//
//   - the sink is `s := expr` then `s = expr`, not a var of type any.
//     Assigning a non-pointer to an interface allocates, which would corrupt
//     the very allocs/op being measured; reusing the expression's own type
//     avoids having to name it. Two expressions get two sinks, one per
//     closure, for exactly that reason: a shared sink would have to be an
//     interface to hold both, which is the trap this comment exists for.
//   - b.ResetTimer() after the priming evaluation, because at b.N == 1 an
//     uncounted extra evaluation is a 100% error.
//   - everything outside that closure — the loop, the results slice, the
//     GOMAXPROCS calls — is outside the measured region, and the slice is
//     sized up front so no append of a result can allocate between two runs.
//
// Several expressions are measured in one evaluation rather than several. Two
// evaluations would build twice and replay the session twice, and a
// measurement is more sensitive to that than :diff's comparison is. Their rows
// come back grouped, opts.runs() of them per expression in the order given,
// which is the contract (*Core).bench splits on.
func benchSource(exprs []string, opts benchOpts, profilePath string) string {
	var b strings.Builder
	b.WriteString("func() [][5]int64 {\n")
	total := strconv.Itoa(opts.count * len(exprs))
	if len(opts.cpus) > 0 {
		b.WriteString("\tcpus := []int{" + joinInts(opts.cpus) + "}\n")
		b.WriteString("\tout := make([][5]int64, 0, len(cpus)*" + total + ")\n")
	} else {
		b.WriteString("\tout := make([][5]int64, 0, " + total + ")\n")
	}

	if profilePath != "" {
		// os.Create's error is carried rather than returned: a benchmark that
		// ran is worth reporting even when the profile could not be written,
		// and the caller checks for the file rather than trusting the flag.
		b.WriteString("\tpf, perr := os.Create(" + strconv.Quote(profilePath) + ")\n")
		b.WriteString("\tif perr == nil {\n")
		b.WriteString("\t\tpprof.StartCPUProfile(pf)\n")
		b.WriteString("\t}\n")
	}
	if len(opts.cpus) > 0 {
		// GOMAXPROCS is restored before returning, because the entry is
		// transient and the process that outlives it replays the session.
		// Hoisted above the per-expression blocks so two of them do not
		// redeclare it; with one expression the emitted text is what it was.
		b.WriteString("\tprev := runtime.GOMAXPROCS(0)\n")
	}

	for _, arg := range exprs {
		indent := "\t"
		if len(opts.cpus) > 0 {
			b.WriteString("\tfor _, p := range cpus {\n")
			b.WriteString("\t\truntime.GOMAXPROCS(p)\n")
			indent = "\t\t"
		}
		b.WriteString(indent + "for k := 0; k < " + strconv.Itoa(opts.count) + "; k++ {\n")
		in := indent + "\t"
		b.WriteString(in + "r := testing.Benchmark(func(b *testing.B) {\n")
		b.WriteString(in + "\ts := " + arg + "\n")
		b.WriteString(in + "\tb.ReportAllocs()\n")
		b.WriteString(in + "\tb.ResetTimer()\n")
		b.WriteString(in + "\tfor i := 0; i < b.N; i++ {\n")
		b.WriteString(in + "\t\ts = " + arg + "\n")
		b.WriteString(in + "\t}\n")
		b.WriteString(in + "\truntime.KeepAlive(s)\n")
		b.WriteString(in + "})\n")
		cpu := "0"
		if len(opts.cpus) > 0 {
			cpu = "int64(p)"
		}
		b.WriteString(in + "out = append(out, [5]int64{" + cpu +
			", r.NsPerOp(), r.AllocedBytesPerOp(), r.AllocsPerOp(), int64(r.N)})\n")
		b.WriteString(indent + "}\n")
		if len(opts.cpus) > 0 {
			b.WriteString("\t}\n")
		}
	}
	if len(opts.cpus) > 0 {
		b.WriteString("\truntime.GOMAXPROCS(prev)\n")
	}

	if profilePath != "" {
		b.WriteString("\tif perr == nil {\n")
		b.WriteString("\t\tpprof.StopCPUProfile()\n")
		b.WriteString("\t\tpf.Close()\n")
		b.WriteString("\t}\n")
	}
	b.WriteString("\treturn out\n}()")
	return b.String()
}

func joinInts(ns []int) string {
	parts := make([]string, 0, len(ns))
	for _, n := range ns {
		parts = append(parts, strconv.Itoa(n))
	}
	return strings.Join(parts, ", ")
}

// pprofImports are what a profiling rewrite needs — :bench -profile's and the
// one :profile and :memprof share. They are named rather than left to
// goimports because `pprof` resolves to two packages in the standard library,
// and net/http/pprof is the one that would compile and do nothing.
var pprofImports = []render.ImportSpec{{Path: "os"}, {Path: "runtime/pprof"}}

// bench is :bench — the expression through testing.B, reporting ns/op, B/op
// and allocs/op. Ruby has no equivalent; Go developers think in these numbers.
//
// It runs as one transient expression, so no new session machinery is needed.
// The flags say how many times, at what parallelism, and whether to keep the
// profile — none of them changes what is measured, only how often and under
// what conditions. See benchSource for why that separation is load-bearing.
func (c *Core) bench(arg string) Result {
	const usage = "usage: :bench [-count n] [-cpu list] [-profile] <expression>[, <expression>]   " +
		"e.g. :bench -count 5 fmt.Sprint(1)"
	opts, arg, err := parseBenchArgs(arg)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	if arg == "" {
		return Result{Out: usage, Err: true}
	}
	// splitTop, the same splitter :diff and :impl use, rather than a second
	// one: a top-level comma separates the pair and a comma inside brackets,
	// parentheses, braces or a literal does not. A single Go expression has no
	// top-level comma to lose — a tuple is the only shape that would, and
	// :bench has always refused those below.
	exprs := splitTop(arg)
	if len(exprs) > 2 {
		return Result{Out: "error: :bench measures one expression or two; got " +
			strconv.Itoa(len(exprs)) + " — " + usage, Err: true}
	}
	for _, arg := range exprs {
		target, err := c.resolve(arg)
		if err != nil {
			return Result{Out: "error: " + err.Error(), Err: true}
		}
		switch {
		case target.IsType:
			return Result{Out: "error: that is a type, not something to run", Err: true}
		case target.Void:
			return Result{Out: "error: " + arg + " has no value to benchmark — " +
				"wrap it in a func literal if you want to time it", Err: true}
		case len(target.Tuple) > 1:
			return Result{Out: "error: :bench takes one value; " + arg + " returns " +
				strconv.Itoa(len(target.Tuple)), Err: true}
		}
	}

	// The path is unique per invocation, and named here rather than in the
	// child so gluon can report it without parsing the child's output. It goes
	// in the evaluator's own temp directory: invariant 1 is that gluon never
	// writes into the project, and a profile is exactly the kind of build
	// artifact that gets committed by accident.
	profilePath := ""
	if opts.profile {
		profilePath = filepath.Join(c.ev.Dir(),
			fmt.Sprintf("bench-%d.pprof", time.Now().UnixNano()))
	}

	entry := session.Entry{Kind: session.KindExpr, Src: benchSource(exprs, opts, profilePath)}
	var res eval.Result
	if opts.profile {
		// EvalLive, not EvalTransient, for two reasons that both come from the
		// file. The result cache is keyed on program text, so a cached hit
		// would report a path nothing had written; and `pprof` needs naming
		// rather than guessing. Invariant 14 still holds — EvalLive snapshots
		// and pops exactly as EvalTransient does.
		res, err = c.ev.EvalLive(c.sess, entry, pprofImports, nil, nil)
	} else {
		res, err = c.ev.EvalTransient(c.sess, entry)
	}
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	userOut, vals := pretty.Parse(res.Output)
	runs, ok := parseBenchRuns(vals)
	if !ok || len(runs) != opts.runs()*len(exprs) {
		out := strings.TrimRight(userOut, "\n")
		if out == "" {
			out = "error: the benchmark produced no numbers"
		}
		return Result{Out: out, Err: true}
	}

	out := inspect.RenderBench(runs, c.Styles, c.Rich)
	if len(exprs) == 2 {
		// benchSource emits opts.runs() rows per expression, in the order it
		// was given them, so the split is arithmetic rather than a marker in
		// the data — one fewer thing the child has to describe.
		n := opts.runs()
		out = inspect.RenderBenchPair(exprs[0], exprs[1], runs[:n], runs[n:], c.Styles, c.Rich)
	}
	if opts.profile {
		out += "\n" + c.profileNote("cpu profile", profilePath)
	}
	return Result{Out: out}
}

// parseBenchRuns reads what the child described. One value comes back — the
// table of runs — and anything else means the program printed something
// instead, which the caller shows rather than swallowing.
func parseBenchRuns(vals []pretty.Value) ([]inspect.BenchRun, bool) {
	if len(vals) != 1 {
		return nil, false
	}
	return inspect.BenchRuns(vals[0])
}

// profileNote says where the profile went and what opens it. gluon does not
// open it: go tool pprof is the tool for this, and a REPL that shelled into an
// interactive pager would be taking over the terminal it was handed.
//
// label names the profile — "cpu profile" for :bench -profile and :profile,
// "mem profile" for :memprof. The continuation line is indented under it, so
// the two spellings being the same width is what keeps the command aligned
// beneath the path.
func (c *Core) profileNote(label, path string) string {
	st := c.styles()
	if _, err := os.Stat(path); err != nil {
		return st.Note.Render("the profile could not be written: " + err.Error())
	}
	return st.Annot.Render(label+"  "+path) + "\n" +
		st.Annot.Render(strings.Repeat(" ", len(label)+2)+"go tool pprof "+path)
}

// inspect is :inspect — open a value in a view that scrolls, for the case the
// printed form cannot serve: a collection with more rows than a terminal.
//
// It evaluates transiently, like :bench and :err, so browsing a value never
// becomes part of the session. It returns both a linear rendering and a modal
// spec: a driver that cannot go full-screen prints the former and loses
// nothing, which is what keeps `echo ':inspect x' | gluon` meaningful.
// inspectUsage is the one spelling of the usage line, so the bare invocation
// and a bad -n say the same thing about what the command takes.
const inspectUsage = "usage: :inspect [-n k] <expression>   e.g. :inspect users, :inspect -n 500 rows"

// cutInspectItems takes a leading -n off the argument. It is deliberately not
// a flag package: the rest of the line is a Go expression, and a parser that
// looked past the first token would find a - inside one and treat it as a flag.
//
// It returns 0 for items when there is no -n, which SetLimits reads as "leave
// the bound alone".
func cutInspectItems(arg string) (items int, rest string, err error) {
	// The flag is only a flag when a space follows it. Matching the prefix
	// alone would take the -n off `-nums`, which is the negation of a variable
	// and a perfectly good thing to inspect. `-n * 2` is the case this gets
	// wrong, and parenthesising it is the way out.
	tail, ok := strings.CutPrefix(arg, "-n")
	if !ok || (tail != "" && !unicode.IsSpace(rune(tail[0]))) {
		return 0, arg, nil
	}
	tail = strings.TrimSpace(tail)
	value := tail
	if i := strings.IndexFunc(tail, unicode.IsSpace); i >= 0 {
		value, rest = tail[:i], strings.TrimSpace(tail[i:])
	}
	n, err := config.ParseValueLimit(value)
	if err != nil {
		return 0, "", err
	}
	return n, rest, nil
}

func (c *Core) inspect(arg string) Result {
	arg = strings.TrimSpace(arg)
	items, arg, err := cutInspectItems(arg)
	if err != nil {
		// Before anything is resolved or evaluated: a limit gluon cannot read
		// is a question it has not been asked yet.
		return Result{Out: inspectUsage + "\n-n takes " + err.Error(), Err: true}
	}
	if arg == "" {
		return Result{Out: inspectUsage, Err: true}
	}
	// One evaluation at a wider bound, and then the session's own again. The
	// transient's cache key carries the raised limit, so this cannot leave the
	// ordinary line's entry holding a -n answer.
	if items > 0 && c.ev != nil {
		was, _ := c.ev.Limits()
		c.ev.SetLimits(items, 0)
		defer c.ev.SetLimits(was, 0)
	}
	target, err := c.resolve(arg)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	switch {
	case target.IsType:
		return Result{Out: "error: that is a type, not a value — :layout " + arg + " shows its fields", Err: true}
	case target.Void:
		return Result{Out: "error: " + arg + " has no value to inspect", Err: true}
	case len(target.Tuple) > 1:
		return Result{Out: "error: :inspect takes one value; " + arg + " returns " +
			strconv.Itoa(len(target.Tuple)), Err: true}
	}

	res, err := c.ev.EvalTransient(c.sess, session.Entry{Kind: session.KindExpr, Src: arg})
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	_, vals := pretty.Parse(res.Output)
	if len(vals) == 0 {
		return Result{Out: "error: " + arg + " produced no value to inspect", Err: true}
	}

	v := vals[0]
	headers, rows, omitted, ok := pretty.Browse(v)
	linear := c.format(res.Output)
	if !ok {
		// A scalar has nothing to scroll. Saying so beats opening an empty
		// pane over the answer the user can already see.
		return Result{Out: linear}
	}

	spec := &ModalSpec{
		Title:   inspectTitle(v, len(rows)),
		Summary: inspectTitle(v, len(rows)) + "  browsed",
		Headers: headers,
		Rows:    rows,
	}
	if omitted > 0 {
		// The view is honest about holding a prefix rather than implying it
		// holds everything. The bound is now a setting, so the note says how
		// to move it rather than reporting it as a fact of the encoder.
		spec.Note = fmt.Sprintf("showing %d of %d — the rest was never sent: the child describes %d items of a collection (:inspect -n k, or :settings value.items)",
			len(rows), len(rows)+omitted, len(rows))
	}
	return Result{Out: linear, Modal: spec}
}

func inspectTitle(v pretty.Value, n int) string {
	t := v.Type
	if t == "" {
		t = "value"
	}
	return fmt.Sprintf("%s  %d rows", t, n)
}

// edit is :edit — pry's edit. The session is written out as the program it
// actually became, opened in the editor, and reloaded from what comes back.
//
// The driver runs the editor, not Core: Submit runs on a background command
// and must never touch the terminal.
func (c *Core) edit() Result {
	// What you typed, not what it rendered into. The rendered program carries
	// __gluonPrint around every expression, and there is no way to write a
	// bare `len(x)` back as valid Go — so :src shows the real program and
	// :edit shows the session. Editing your own lines is also what pry's edit
	// does.
	var b strings.Builder
	for _, e := range c.sess.Entries {
		b.WriteString(e.Src)
		b.WriteString("\n")
	}
	src := b.String()

	f, err := os.CreateTemp("", "gluon-edit-*.go")
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	if _, err := f.WriteString(src); err != nil {
		f.Close()
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	f.Close()
	return Result{Edit: f.Name()}
}

// Reload replaces the session with the file :edit handed to the editor.
//
// A session that no longer compiles is worse than the one being replaced, so
// the old entries go back if anything fails.
func (c *Core) Reload(path string) (out Result) {
	c.padDepth++
	defer c.flushPad(&out)

	c.gen++
	// The session is being replaced from a file the user edited outside gluon,
	// so anything derived from reading the host is as stale as the checker —
	// same reason :reload drops it.
	c.dropDetection()
	data, err := os.ReadFile(path)
	// :edit writes a temp file and this is where it is cleaned up. A pad's own
	// session file is not a temp file: :scratch -edit hands over the durable
	// thing itself, and removing it here would delete the session the user was
	// editing.
	isPad := filepath.Base(path) == scratch.PadName
	if !isPad {
		os.Remove(path)
	}
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}

	build := func() Result { return c.submitGrouped(string(data)) }
	if isPad {
		// Through the pad reader, so the directives survive the round trip.
		// submitAll would classify each construct from its source alone, and a
		// pin is a decision the session holds that Classify has never seen — so
		// editing a pad through :edit's path would silently unpin everything in
		// it.
		build = func() Result {
			// Through the pad parser and not session.Unmarshal: the file opens
			// with a header — //gluon:pad, //gluon:go, the requires — that the
			// entry reader is right to refuse and that this path has to eat.
			// Reading it here as a bare entry stream is what made :scratch
			// -edit answer `unknown directive "//gluon:pad 1"` for every pad
			// that had ever been written.
			pad, perr := scratch.ParsePad(data)
			if perr != nil {
				return Result{Out: "error: " + perr.Error(), Err: true}
			}
			c.sess.Entries = pad.Sess.Entries
			// EvalFrom(…, 0) rather than Eval: Eval mutes everything before
			// the newest entry, so a reloaded pad would answer with its last
			// line and nothing else. It is one build either way — the unmute
			// boundary is the only difference — and what the pad prints is
			// what the reader edited it to print.
			res, eerr := c.ev.EvalFrom(c.sess, 0)
			if eerr != nil {
				return Result{Out: "error: " + eerr.Error() + c.pinNote(eerr.Error()), Err: true}
			}
			return Result{Out: c.format(res.Output)}
		}
	}
	res := c.swapSession(build)
	if res.Err {
		return res
	}
	if isPad && c.pad != nil {
		// The file on disk is the one gluon just handed to an editor, so it is
		// the new baseline. Without this the very next persist stats a pad that
		// has changed size and mtime since the open, concludes that a second
		// gluon wrote it, and stands down — and a session that quietly stopped
		// being written down is the failure the guard exists to prevent, not
		// one it is allowed to cause.
		c.rememberFile()
	}
	// What the session printed on the way back, then the receipt. The output
	// first because it is the answer and the count is only the confirmation.
	return Result{Out: joinLines(res.Out,
		fmt.Sprintf("reloaded (%d entries)", len(c.sess.Entries)))}
}

// swapSession empties the session, runs build, and puts the old entries back if
// build failed.
//
// This is the one restore-on-failure path. A session that no longer compiles is
// worse than the one being replaced, and a second implementation of that would
// be a second place to get it wrong — where getting it wrong means destroying
// the session the user was trying to leave. :edit's reload builds the new
// entries from a file; :restore installs them from a snapshot; only the source
// of the entries differs, so only that is the argument.
//
// The failed replay is reported with " — session unchanged" appended, unless
// even putting the old entries back would not run: that session is not
// unchanged in any sense worth claiming, so the claim is dropped rather than
// made falsely.
func (c *Core) swapSession(build func() Result) Result {
	before := c.sess.Entries
	c.sess.Reset()
	res := build()
	if res.Err {
		c.sess.Entries = before
		if _, rerr := c.ev.Eval(c.sess); rerr != nil {
			return Result{Out: res.Out, Err: true}
		}
		return Result{Out: res.Out + " — session unchanged", Err: true}
	}
	// The successful replay's own output is returned rather than dropped.
	// A caller with a summary of its own ignores it — :restore says what it
	// restored and opening a pad says how many entries came back — but
	// :edit's reload has nothing else to say, and an editor that runs the
	// session without closing itself has nothing else to show.
	return res
}

// SubmitScript feeds text through the session line by line, accumulating
// multi-line constructs, stopping at the first error — the shape a scripted
// caller wants. It is Reload's engine, exported for gluon mcp's eval tool.
func (c *Core) SubmitScript(text string) Result { return c.submitAll(text) }

// submitAll feeds text through Submit line by line, accumulating multi-line
// constructs the way the piped driver does.
func (c *Core) submitAll(text string) Result {
	var outs []string
	// The trailing partial is dropped rather than submitted, which is what this
	// loop did when it spelled the split itself: a file ending in an unclosed
	// brace has no construct to run there, and submitting the fragment would
	// report a parse error against text the user may still be writing.
	constructs, _ := session.SplitConstructs(text)
	for _, src := range constructs {
		res := c.Submit(src)
		if res.Err {
			return Result{Out: strings.Join(append(outs, res.Out), "\n"), Err: true}
		}
		if res.Out != "" {
			outs = append(outs, res.Out)
		}
	}
	return Result{Out: strings.Join(outs, "\n")}
}

// submitGrouped feeds text through the session the way a file of it should be
// run: consecutive code constructs go as one batch, and a meta command flushes
// whatever has been collected before it runs.
//
// It is submitAll's sibling rather than its replacement because the two answer
// different questions. submitAll is SubmitScript's engine — a scripted caller
// wants line-by-line semantics and a stop at the first failure. This one is
// Reload's, where the file *is* the session and replaying it a construct at a
// time costs one build per entry: measured at 4.2s against 0.64s for eight
// constructs, which is the difference between an editor that can run what you
// wrote and one you would not press the key in.
//
// The grouping is the shape runOneShot already uses for `gluon -e`: a meta
// answers against the session as the lines before it left it, which is exactly
// why it cannot travel inside a batch.
func (c *Core) submitGrouped(text string) Result {
	var outs []string
	var batch []string

	// ok runs one Result into the accumulator and reports whether the walk
	// may continue. The stop at the first failure is submitAll's rule kept:
	// a file whose third line does not compile has not described a session,
	// and swapSession is about to put the old one back anyway.
	ok := func(res Result) bool {
		if res.Out != "" {
			outs = append(outs, res.Out)
		}
		return !res.Err
	}
	// flush is named rather than inlined twice: the tail flush and the flush
	// before a meta command are the same act, and two spellings of it would be
	// two places for the batch to be dropped.
	flush := func() bool {
		if len(batch) == 0 {
			return true
		}
		src := batch
		batch = nil
		return ok(c.SubmitBatch(src))
	}

	constructs, _ := session.SplitConstructs(text)
	for _, src := range constructs {
		if !strings.HasPrefix(strings.TrimSpace(src), ":") {
			batch = append(batch, src)
			continue
		}
		if !flush() || !ok(c.Submit(src)) {
			return Result{Out: strings.Join(outs, "\n"), Err: true}
		}
	}
	if !flush() {
		return Result{Out: strings.Join(outs, "\n"), Err: true}
	}
	return Result{Out: strings.Join(outs, "\n")}
}

// builtinEditor is the one value of the `editor` setting that does not name a
// program: gluon's own full-screen view, which needs no process and no
// terminal handover.
const builtinEditor = "builtin"

// BuiltinEditor reports whether the reader asked for gluon's own editor.
//
// Config only, and deliberately not through EditorAt's chain: there the
// environment wins, because $VISUAL exported for this shell is more specific
// than a file. Here the question is not *which* program but *whether* one — a
// choice nobody expresses by exporting an environment variable, and one that
// an already-exported $EDITOR would otherwise silently veto on the machines
// where almost everyone has one.
func BuiltinEditor() bool {
	cfg, err := config.Load()
	return err == nil && strings.TrimSpace(cfg.Editor) == builtinEditor
}

// Editor is the command that opens path. $VISUAL and $EDITOR are both commonly
// unset, so the fallback chain matters; GUI editors need --wait or the process
// returns before the file has been saved.
//
// Config sits between the environment and the built-in fallbacks: someone who
// wrote it down there means it, but a $VISUAL exported for this shell is more
// specific still.
func Editor(path string) *exec.Cmd { return EditorAt(path, 0) }

// EditorAt is Editor, opened on a line.
//
// A line rather than nothing because the buffer reopens on the construct that
// would not compile, and an editor that lands at the top of the file leaves the
// reader to find it again. How to say it is per-editor and short: `+N` is the
// convention every vi and emacs descendant honours, and the two GUI editors
// that already need a --wait switch here want --goto instead. Anything
// unrecognised is opened at the top, which is what it does today.
//
// line <= 0 is Editor exactly: the same argv, byte for byte.
func EditorAt(path string, line int) *exec.Cmd {
	ed := os.Getenv("VISUAL")
	if ed == "" {
		ed = os.Getenv("EDITOR")
	}
	if ed == "" {
		if cfg, err := config.Load(); err == nil {
			ed = cfg.Editor
		}
	}
	// "builtin" names gluon's own view, not a program. A caller that got here
	// wants something to exec, so fall through to the terminal editors rather
	// than handing exec.Command a name that is not on anyone's $PATH.
	if ed == builtinEditor {
		ed = ""
	}
	if ed == "" {
		for _, cand := range []string{"nvim", "vim", "vi"} {
			if _, err := exec.LookPath(cand); err == nil {
				ed = cand
				break
			}
		}
	}
	if ed == "" {
		return nil
	}
	fields := strings.Fields(ed)
	args := fields[1:]
	base := filepath.Base(fields[0])
	switch base {
	case "code", "cursor", "subl", "zed":
		args = append(args, "--wait")
	}
	if line > 0 {
		switch base {
		case "vi", "vim", "nvim", "view", "emacs", "emacsclient", "nano", "gedit", "kak", "hx":
			args = append(args, "+"+strconv.Itoa(line))
		case "code", "cursor":
			args = append(args, "--goto")
			return exec.Command(fields[0], append(args, path+":"+strconv.Itoa(line))...)
		case "subl", "zed":
			return exec.Command(fields[0], append(args, path+":"+strconv.Itoa(line))...)
		}
	}
	return exec.Command(fields[0], append(args, path)...)
}

// time is :time — show where each line's latency went. GLUON_TIMING does the
// same thing to stderr, which interleaves with the TUI's own rendering.
func (c *Core) time(arg string) Result {
	switch strings.TrimSpace(arg) {
	case "", "on":
		c.timing = true
		eval.Timing(true)
		return Result{Out: "timing on"}
	case "off":
		c.timing = false
		eval.Timing(false)
		return Result{Out: "timing off"}
	}
	return Result{Out: "usage: :time [on|off]", Err: true}
}

// clock starts a line's :time account. Phases are collected for the whole
// package, and the checker runs between lines as well as in them — a
// completion or a signature hint asks it on every keystroke — so what it
// measured then belongs to no line, and would otherwise be reported as the
// next one's: a call typed a character at a time ended in a row of gofmt 0s.
func (c *Core) clock() {
	if c.timing {
		eval.TakePhases()
	}
}

// phases formats what the last evaluation spent, for :time.
func (c *Core) phases() string {
	ps := eval.TakePhases()
	if len(ps) == 0 {
		return ""
	}
	var parts []string
	var total time.Duration
	for _, p := range ps {
		parts = append(parts, fmt.Sprintf("%s %v", p.Name, p.D.Round(100*time.Microsecond)))
		total += p.D
	}
	line := "  [" + strings.Join(parts, " · ") +
		fmt.Sprintf(" · total %v]", total.Round(100*time.Microsecond))
	if c.Rich {
		return c.Styles.Note.Render(line)
	}
	return line
}

// hist numbers the session's entries, so :drop has something to name. This is
// the session, not the history file: what is in the program right now.
func (c *Core) hist() string {
	if len(c.sess.Entries) == 0 {
		return "nothing in the session yet"
	}
	ord := render.Ordinals(c.sess)
	var b strings.Builder
	for i, e := range c.sess.Entries {
		b.WriteString(fmt.Sprintf("%3d  %s", i+1, flattenForHistory(e.Src)))
		if ord[i] != 0 {
			b.WriteString("   " + render.OrdName(ord[i]))
		}
		// A pinned entry is in the transcript but not in the program, and a
		// transcript that did not say so would be the one place gluon let the
		// two drift apart silently.
		if e.Pinned {
			b.WriteString("   pinned")
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// drop removes one entry by its :hist number and re-evaluates, the way :undo
// does for the last one.
//
// It refuses when a later entry addresses a value the drop would renumber.
// Renumbering silently would change what _3 means underneath the user, which
// is worse than making them undo it themselves.
func (c *Core) drop(arg string) Result {
	n, err := strconv.Atoi(strings.TrimSpace(arg))
	if err != nil || n < 1 || n > len(c.sess.Entries) {
		return Result{Out: "usage: :drop <n>   the number :hist shows", Err: true}
	}
	i := n - 1

	ord := render.Ordinals(c.sess)
	if ord[i] != 0 {
		for _, e := range c.sess.Entries[i+1:] {
			ords, usesIt := render.Uses(e.Src)
			if usesIt {
				return Result{Out: fmt.Sprintf(
					"error: a later line says %s, and dropping entry %d changes which value that is — "+
						":undo back to it instead", render.ItName, n), Err: true}
			}
			for _, u := range ords {
				if u >= ord[i] {
					return Result{Out: fmt.Sprintf(
						"error: a later line refers to %s, which dropping entry %d would renumber — "+
							":undo back to it instead", render.OrdName(u), n), Err: true}
				}
			}
		}
	}

	dropped := c.sess.Entries[i]
	c.sess.Remove(i)
	if _, err := c.ev.Eval(c.sess); err != nil {
		// Put it back: a session that no longer compiles is worse than one
		// with an unwanted line in it.
		c.sess.Entries = append(c.sess.Entries[:i],
			append([]session.Entry{dropped}, c.sess.Entries[i:]...)...)
		return Result{Out: "error: " + err.Error() + " — entry kept", Err: true}
	}
	left := len(c.sess.Entries)
	return Result{Out: fmt.Sprintf("dropped %d (%d %s left)", n, left, plurals(left, "entry", "entries"))}
}

// undefinedRe pulls the name out of the compiler's own wording.
var undefinedRe = regexp.MustCompile(`undefined: ([A-Za-z_][A-Za-z0-9_]*)`)

// pinNote explains an `undefined` that a pin caused. A pinned entry is still
// in the transcript, so the name is visibly right there in :hist and the error
// reads like a bug in gluon rather than a consequence of something the user
// asked for. Naming the pin is the difference.
func (c *Core) pinNote(msg string) string {
	m := undefinedRe.FindStringSubmatch(msg)
	if m == nil {
		return ""
	}
	name := m[1]
	ord := render.Ordinals(c.sess)
	for i, e := range c.sess.Entries {
		if !e.Pinned {
			continue
		}
		if ord[i] != 0 && name == render.OrdName(ord[i]) {
			return fmt.Sprintf("\n  %s is the value of entry %d, which is pinned — :unpin %d to use it",
				name, i+1, i+1)
		}
		for _, b := range e.Binds {
			if b == name {
				return fmt.Sprintf("\n  %s is bound by entry %d, which is pinned — :unpin %d to use it",
					name, i+1, i+1)
			}
		}
	}
	return ""
}

// pin marks an entry so replay stops re-running it.
//
// The whole session is re-rendered on every line, so a line that fetched a URL
// or wrote a file does it again on every subsequent line — the one property of
// the replay model that has no defence inside it. Pinning is the user saying
// "this already happened": the entry contributes no code at all from here on.
//
// It is refused whenever something later depends on what the entry bound.
// gluon does not reconstruct the value to fill the gap, because the encoded
// form is a display string rather than a literal, and because a value that has
// been through the printer has already lost the aliasing the printer exists to
// be truthful about. Refusing and saying why is the honest half.
func (c *Core) pin(arg string) Result {
	if strings.TrimSpace(arg) == "" {
		return Result{Out: c.pinned()}
	}
	i, res, ok := c.entryArg(arg, "pin")
	if !ok {
		return res
	}
	if c.sess.Entries[i].Pinned {
		return Result{Out: fmt.Sprintf("entry %d is already pinned", i+1)}
	}
	if why := render.PinBlockers(c.sess, i); len(why) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "error: entry %d cannot be pinned", i+1)
		for _, w := range why {
			b.WriteString("\n  " + w)
		}
		return Result{Out: b.String(), Err: true}
	}
	return c.setPinned(i, true, fmt.Sprintf("pinned %d — it will not run again", i+1))
}

// unpin puts an entry back into the program, which means running it again.
func (c *Core) unpin(arg string) Result {
	i, res, ok := c.entryArg(arg, "unpin")
	if !ok {
		return res
	}
	if !c.sess.Entries[i].Pinned {
		return Result{Out: fmt.Sprintf("entry %d is not pinned", i+1)}
	}
	return c.setPinned(i, false, fmt.Sprintf("unpinned %d — it runs again from here", i+1))
}

// setPinned flips one entry and re-evaluates, restoring the old state if the
// session no longer builds. :drop takes the same care for the same reason.
func (c *Core) setPinned(i int, to bool, msg string) Result {
	was := c.sess.Entries[i].Pinned
	c.sess.Entries[i].Pinned = to
	if _, err := c.ev.Eval(c.sess); err != nil {
		c.sess.Entries[i].Pinned = was
		return Result{Out: "error: " + err.Error() + " — left as it was", Err: true}
	}
	return Result{Out: msg}
}

// pinned lists what is pinned, for a bare :pin.
func (c *Core) pinned() string {
	var b strings.Builder
	for i, e := range c.sess.Entries {
		if e.Pinned {
			fmt.Fprintf(&b, "%3d  %s\n", i+1, flattenForHistory(e.Src))
		}
	}
	if b.Len() == 0 {
		return "nothing is pinned — :pin <n> stops an entry re-running on replay"
	}
	return strings.TrimRight(b.String(), "\n")
}

// entryArg parses the :hist number both pin commands take. A number that is
// simply out of range gets its own answer: a line that failed to compile was
// rolled back, so the number the user just read off :hist may already be one
// past the end, and "usage" would not explain that.
func (c *Core) entryArg(arg, verb string) (int, Result, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(arg))
	if err != nil {
		return 0, Result{Out: "usage: :" + verb + " <n>   the number :hist shows", Err: true}, false
	}
	if n < 1 || n > len(c.sess.Entries) {
		return 0, Result{Out: fmt.Sprintf("error: there is no entry %d — the session has %d",
			n, len(c.sess.Entries)), Err: true}, false
	}
	return n - 1, Result{}, true
}

// load reads a file and submits it line by line, accumulating multi-line
// constructs the way the piped driver does.
func (c *Core) load(arg string) Result {
	if arg == "" {
		return Result{Out: "usage: :load <file>", Err: true}
	}
	path, err := expandPath(arg)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}

	res := c.submitAll(string(data))
	if res.Out == "" && !res.Err {
		return Result{Out: "loaded " + path}
	}
	return res
}

// errChain is :err — the wrapped error chain, with the concrete type at each
// level.
//
// fmt.Errorf("%w", …) is the Go error idiom and printing the result only ever
// showed the outermost message. The value printer now describes the chain
// structurally; this lays it out flat, which is what you want when the
// question is "what can I errors.Is against".
//
// It walks Unwrap() error and Unwrap() []error, the second being what
// errors.Join returns. No runtime code is needed: the whole walk is one
// expression, and goimports resolves errors and fmt for it.
func (c *Core) errChain(arg string) Result {
	if arg == "" {
		return Result{Out: "usage: :err <expression>   e.g. :err err", Err: true}
	}
	target, err := c.resolve(arg)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	if target.Type == nil || !inspect.IsError(target.Type) {
		return Result{Out: "error: " + arg + " is not an error", Err: true}
	}

	entry := session.Entry{
		Kind: session.KindExpr,
		Src: "func() []string {\n" +
			"\tvar out []string\n" +
			"\tvar walk func(error, int)\n" +
			"\twalk = func(e error, d int) {\n" +
			"\t\tfor e != nil {\n" +
			"\t\t\tout = append(out, fmt.Sprintf(\"%d\\x1f%T\\x1f%s\", d, e, e.Error()))\n" +
			"\t\t\tif m, ok := e.(interface{ Unwrap() []error }); ok {\n" +
			"\t\t\t\tfor _, sub := range m.Unwrap() {\n" +
			"\t\t\t\t\twalk(sub, d+1)\n" +
			"\t\t\t\t}\n" +
			"\t\t\t\treturn\n" +
			"\t\t\t}\n" +
			"\t\t\te = errors.Unwrap(e)\n" +
			"\t\t}\n" +
			"\t}\n" +
			"\twalk(" + arg + ", 0)\n" +
			"\treturn out\n" +
			"}()",
	}
	res, err := c.ev.EvalTransient(c.sess, entry)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	_, vals := pretty.Parse(res.Output)
	if len(vals) != 1 || len(vals[0].Items) == 0 {
		return Result{Out: "error: no chain came back", Err: true}
	}
	return Result{Out: inspect.RenderErrChain(vals[0].Items, c.Styles, c.Rich)}
}

// escape is :esc — whether an expression's value reaches the heap.
//
// It builds and never runs, so it costs a build but not the exec. The answer
// is context-dependent and says so: &T{} assigned to a local that dies at the
// end of the block does not escape, while the same expression returned from a
// function does. For the flatter question "does this allocate", :bench's
// allocs/op is the better tool — it is measured, and context-free.
func (c *Core) escape(arg string) Result {
	if arg == "" {
		return Result{Out: "usage: :esc <expression>   e.g. :esc &Point{1, 2}", Err: true}
	}
	target, err := c.resolve(arg)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	switch {
	case target.IsType:
		return Result{Out: "error: a type allocates nothing — try :esc on a value", Err: true}
	case len(target.Tuple) > 1:
		return Result{Out: "error: :esc takes one value; " + arg + " returns " +
			strconv.Itoa(len(target.Tuple)), Err: true}
	}

	entry := session.Entry{Kind: session.KindExpr, Src: arg, NoValue: target.Void}
	res, err := c.ev.EscapeAnalysis(c.sess, entry)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	return Result{Out: inspect.RenderEscape(res.Here, res.Elsewhere, c.Styles, c.Rich)}
}

// layout is :layout — where a struct's fields sit, and what the holes between
// them cost. go/types knows the sizing rules, so like :t and :m this answers
// without building or running anything.
func (c *Core) layout(arg string) Result {
	if arg == "" {
		return Result{Out: "usage: :layout <struct type or value>", Err: true}
	}
	target, err := c.resolve(arg)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	l, ok := target.Layout()
	if !ok {
		return Result{Out: "error: not a struct — :layout shows field offsets and padding", Err: true}
	}
	if c.Rich {
		return Result{Out: inspect.RenderLayout(l, c.Styles)}
	}
	return Result{Out: inspect.PlainLayout(l)}
}

// resolve type-checks the session with the expression appended, so the
// inspector sees every name the session has bound and every import goimports
// resolved for it.
func (c *Core) resolve(expr string) (*inspect.Target, error) {
	res, err := c.ev.Analyze(c.sess, expr)
	if err != nil {
		return nil, err
	}
	return inspect.Resolve(res)
}

// slice is :slice — the runtime triple behind a slice, and whether two slices
// are views on the same memory.
//
// Unlike :t and :m this one has to run: a pointer only exists at run time. The
// call is appended, evaluated and popped, so the session is unchanged.
func (c *Core) slice(arg string) Result {
	names := splitExprs(arg)
	if len(names) == 0 {
		return Result{Out: "usage: :slice <expr>[, <expr>...]   e.g. :slice x, x[1:3]", Err: true}
	}

	// NoValue is set rather than discovered: the header printer writes its own
	// payload and returns nothing, so wrapping it in the value printer would
	// cost a doomed type check to learn what is already known here.
	entry := session.Entry{
		Kind:    session.KindExpr,
		Src:     render.HdrFunc + "(" + strings.Join(names, ", ") + ")",
		NoValue: true,
	}
	res, err := c.ev.EvalTransient(c.sess, entry)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}

	userOut, vals := pretty.Parse(res.Output)
	if len(vals) == 0 {
		out := strings.TrimRight(userOut, "\n")
		if out == "" {
			out = "error: no header came back"
		}
		return Result{Out: out, Err: true}
	}
	return Result{Out: inspect.RenderSlices(names, vals, c.Styles, c.Rich)}
}

// splitExprs cuts a comma-separated argument list, falling back to whitespace
// when there are no commas so `:slice x y` works for plain names. Commas are
// the documented form because an expression can contain spaces.
func splitExprs(arg string) []string {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return nil
	}
	fields := strings.Split(arg, ",")
	if len(fields) == 1 {
		fields = strings.Fields(arg)
	}
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// doc is :doc — shells out to go doc, which already knows how to find and
// format documentation and needs no help from gluon. With -src it prints the
// implementation instead, which is pry's show-source.
//
// The other three flags are the forms `go doc` has no answer for: the examples
// it does not report, the web address the terminal is sometimes the wrong
// medium for, and the statement that a name is a package. Every one of them is
// a file read or a string build, which is what keeps :doc static and go_doc an
// MCP tool that needs no --eval (invariant 28).
func (c *Core) doc(arg string) Result {
	mode, arg, err := parseDocArgs(arg)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	if arg == "" {
		return Result{Out: docUsage, Err: true}
	}
	switch mode {
	case docExamples:
		return c.docExamples(arg)
	case docURL:
		return c.docURL(arg)
	}

	var out string
	if mode == docPkg {
		out, err = c.ev.DocPackage(arg)
	} else {
		out, err = c.ev.Doc(arg, mode == docSrc)
	}
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	text := strings.TrimRight(out, "\n")
	what := "doc " + arg
	// Only -src is Go. `go doc` without it emits indented English, where `for`,
	// `type` and `string` are words in a sentence rather than keywords.
	lang := syntax.None
	if mode == docSrc {
		what, lang = "source of "+arg, syntax.Go
	}
	return sourceResult(what, text, lang)
}

const docUsage = "usage: :doc [-src|-examples|-url|-pkg] <symbol>   e.g. :doc strings.Builder"

// docExamples is :doc -examples — the runnable examples `go doc` never shows,
// read from the sources already on disk.
//
// "Found none" and "did not look" are different answers, the way
// db.Detection's Searched and Roots are: a module in the build list that was
// never downloaded has no sources here, and calling that "no examples" would
// be a claim gluon has not checked.
func (c *Core) docExamples(pkg string) Result {
	unavailable := func(err error) Result {
		return Result{Out: "error: the sources for " + pkg +
			" are not available locally: " + err.Error(), Err: true}
	}
	dir, err := c.ev.PackageDir(pkg)
	if err != nil {
		return unavailable(err)
	}
	name, exs, err := packageExamples(dir)
	if err != nil {
		return unavailable(err)
	}
	if len(exs) == 0 {
		return Result{Out: pkg + " has no examples"}
	}
	if name == "" {
		name = path.Base(pkg)
	}
	return sourceResult("examples of "+pkg, renderExamples(name, exs), syntax.Go)
}

// docURL is :doc -url — the pkg.go.dev address, printed and nothing else.
//
// Opening a browser is a side effect on the user's desktop from a line that is
// also in the history file, and it cannot be undone; the URL is one click away
// in any terminal that linkifies. Nothing here runs a process: Requires reads
// go.mod, and the rest is string building.
func (c *Core) docURL(arg string) Result {
	// A build list gluon cannot read is a session with no versions, which is
	// the unknown-version path — not a reason to refuse an address.
	reqs, _ := c.ev.Requires()
	url, versioned := docAddress(reqs, arg)
	if versioned {
		return Result{Out: url}
	}
	return Result{Out: url + "\nthe version is unknown, so this address is the latest " +
		"published rather than what the session has"}
}

// pageable returns a view spec when text is long enough that scrollback would
// swallow its beginning, and nil when it is not. Short output stays inline,
// for the same reason a scalar does not get a table: a pane over three lines
// is worse than three lines.
//
// The threshold is a whole terminal's worth. Below it, scrolling back is
// enough; above it, the top has already gone.
func pageable(title, text string) *ModalSpec { return pageableIn(title, text, syntax.None) }

// pageableIn is pageable for text that is source in a known language.
//
// The language is carried, never applied. Core runs on a background goroutine
// and must not decide what a terminal it cannot see is capable of — and Text is
// the same bytes either way, so a driver that ignores Lang loses nothing.
func pageableIn(title, text string, lang syntax.Lang) *ModalSpec {
	const enoughToScroll = 24
	if strings.Count(text, "\n") < enoughToScroll {
		return nil
	}
	lines := strings.Count(text, "\n") + 1
	return &ModalSpec{
		Title:   title,
		Summary: fmt.Sprintf("%s  %d lines  browsed", title, lines),
		Text:    text,
		Lang:    lang,
	}
}

// sourceResult is a command whose whole answer is source. Out carries it in the
// linear form every driver reads, Modal pages it when it is long, and both
// halves name the same language — so the two cannot disagree about it, which is
// the only way this could go wrong.
func sourceResult(title, text string, lang syntax.Lang) Result {
	return Result{Out: text, Lang: lang, Modal: pageableIn(title, text, lang)}
}

// source renders the session the way a person would read it. It deliberately
// does not build: :src and :save want the text, not an execution.
func (c *Core) source() (string, error) {
	src, err := c.ev.Render(c.sess)
	if err != nil {
		var be *eval.BuildError
		if asBuildError(err, &be) && be.Source != "" {
			// A syntax error still has source worth showing.
			return render.Display(be.Source), nil
		}
		return "", err
	}
	return render.Display(src), nil
}

// ls is :ls — pry's ls. Everything the session has in scope, with the types
// the checker already worked out. It never builds.
func (c *Core) ls() Result {
	sc, err := c.scope()
	if err != nil {
		// Without the checker there are still names, just no types.
		return Result{Out: c.vars()}
	}
	sc.Values = c.values()
	if sc.Empty() {
		return Result{Out: "nothing bound yet"}
	}
	if c.Rich {
		return Result{Out: inspect.RenderScope(sc, c.Styles)}
	}
	return Result{Out: inspect.PlainScope(sc)}
}

func (c *Core) scope() (*inspect.Scope, error) {
	res, err := c.ev.Analyze(c.sess, "")
	if err != nil {
		return nil, err
	}
	return inspect.Bindings(res), nil
}

// values lists the ordinals the session has printed, newest last, so :ls can
// say what _2 actually was.
func (c *Core) values() []inspect.Binding {
	ord := render.Ordinals(c.sess)
	var out []inspect.Binding
	for i, e := range c.sess.Entries {
		if ord[i] == 0 {
			continue
		}
		name := e.Src
		if len(name) > 40 {
			name = name[:39] + "…"
		}
		out = append(out, inspect.Binding{Name: name, Ord: ord[i]})
	}
	return out
}

// vars is the fallback when the checker is unavailable: names only, which is
// what :vars always showed.
func (c *Core) vars() string {
	seen := map[string]bool{}
	var names []string
	for _, e := range c.sess.Entries {
		for _, b := range e.Binds {
			if !seen[b] {
				seen[b] = true
				names = append(names, b)
			}
		}
	}
	if len(names) == 0 {
		return "no variables bound yet"
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// save writes the rendered program as a self-contained scratch module. A
// directory with its own go.mod (rather than a flat //go:build ignore file)
// keeps gopls fully alive in the file, which matters most when learning — and
// with -debug it is what a debugger can open, which is the answer here to
// "I need to step through this": leave the REPL for a real one.
func (c *Core) save(arg string) Result {
	debug, wantTests, topic := parseSaveArgs(arg)
	// Branching on the parsed topic, before it is defaulted: a bare :save in a
	// pad refreshes that pad's own program, so `gluon run <name>`, gopls and dlv
	// look at what has been typed rather than at a dated copy of it. :save with
	// a topic is what it has always been — a new scratch, in the tree, named.
	inPad := topic == "" && c.pad != nil && c.pad.readOnly == ""
	if topic == "" {
		topic = "session"
	}
	if inPad {
		topic = c.pad.name
	}
	src, err := c.source()
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}

	// Held rather than written until asked for, so the flag is the only thing
	// that ever puts a test file on disk.
	tests := ""
	if wantTests {
		tests = c.TestFile()
	}

	// The same scaffolding `gluon new` writes, so a saved session and a fresh
	// scratch are the same kind of thing in the same place. The rendered
	// program calls the injected printer, so it has to travel with the file.
	opts := scratch.Options{
		Topic:   topic,
		Body:    src,
		Runtime: true,
		Debug:   debug,
		Tests:   tests,
		Host:    c.ev.Host(),
	}
	// scratch.Write is New's second half, so the module a pad gets is
	// byte-for-byte the module any other save writes — one scaffolder, and the
	// only difference is the directory it lands in.
	var path string
	if inPad {
		path, err = scratch.Write(c.pad.dir, opts)
	} else {
		path, err = scratch.New(opts)
	}
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	dir := filepath.Dir(path)

	out := "saved → " + path
	switch {
	case tests != "":
		out += "\ntests → " + scratch.TestPath(dir)
	case wantTests:
		// An empty test file would look like a save that worked and a test
		// suite that found nothing to say.
		out += "\ntests → nothing to write: :test <exp> generates one from a value you have seen"
	}
	if debug {
		// The command is named as well as configured. gluon does not run a
		// debugger, and an editor whose integration does not read launch.json
		// would otherwise leave the developer looking it up.
		out += "\ndebug → " + scratch.DebugCommand(dir) +
			"\n        or open the directory — " + debugConfigNote
	}
	return Result{Out: out}
}

// debugConfigNote names the file written for an editor, relative to the
// scratch. The absolute path is already on the line above it.
const debugConfigNote = ".vscode/launch.json is written beside the module"

// parseSaveArgs splits :save's leading flags from its topic.
//
// It follows parseQueryArgs: a flag is read only while it leads, and everything
// from the first non-flag word on is the topic verbatim. The cost is that a
// topic literally named -debug becomes unreachable, which is affordable —
// :save slugs its topic, and a leading dash is not slug-like.
//
// One parse for both flags rather than one each: they are independent, they
// may appear in either order, and a second parse beside this one is how the
// two would come to disagree about what counts as a topic.
func parseSaveArgs(arg string) (debug, tests bool, topic string) {
	rest := strings.TrimSpace(arg)
	for {
		switch {
		case strings.HasPrefix(rest, "-debug "), rest == "-debug":
			debug = true
			rest = strings.TrimSpace(strings.TrimPrefix(rest, "-debug"))
		case strings.HasPrefix(rest, "-test "), rest == "-test":
			tests = true
			rest = strings.TrimSpace(strings.TrimPrefix(rest, "-test"))
		default:
			return debug, tests, rest
		}
	}
}

// flattenForHistory collapses a multi-line construct onto one line so history
// holds one recallable entry rather than fragments that are useless alone. The
// flattened form is used only if it still parses the same way — a trailing
// line comment, for instance, would swallow the rest.
func flattenForHistory(src string) string {
	if !strings.Contains(src, "\n") {
		return src
	}
	var parts []string
	for _, line := range strings.Split(src, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			parts = append(parts, t)
		}
	}
	flat := strings.Join(parts, " ")
	if session.IsIncomplete(flat) {
		return src
	}
	orig, err1 := session.Classify(src)
	round, err2 := session.Classify(flat)
	if err1 != nil || err2 != nil || orig.Kind != round.Kind {
		return src
	}
	return flat
}

func isQuit(src string) bool {
	switch strings.TrimSpace(src) {
	case ":q", ":quit", ":exit":
		return true
	}
	return false
}
