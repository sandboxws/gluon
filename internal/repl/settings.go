package repl

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"

	"github.com/sandboxws/gluon/internal/config"
	"github.com/sandboxws/gluon/internal/eval"
	"github.com/sandboxws/gluon/internal/gluonrt"
	"github.com/sandboxws/gluon/internal/pretty"
)

// `:settings` is the other half of `:theme`: colour is a setting you can
// already change at the prompt, and until now it was the only one.
//
// Everything else lived in a file you had to know existed, spelled in a way you
// had to know, with defaults recorded nowhere gluon could show you. That is a
// bad trade for a tool whose whole argument is that a question about your code
// should be answerable from the line you are already typing on.
//
// It is called :settings rather than :config because :config is the viper and
// koanf plugins' — that command answers "what did this library resolve, and
// from where", which is a question about the user's program. Builtins are
// looked up before plugin commands, so taking the name would have made a
// shipped command unreachable without anything failing.

// unsetMark is what puts a setting back to its default. `-` rather than an
// empty argument, because a bare `:settings <key>` is the request to explain
// one and a command cannot read the same input two ways. Nothing gluon settles
// takes `-` as a value: not a duration, a form, a theme name, a colour or a
// path worth having.
const unsetMark = "-"

func (c *Core) settingsCmd(arg string) Result {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return c.settingsList()
	}
	key, value, hasValue := cutSetting(arg)
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)

	opt, ok := config.Lookup(key)
	if !ok {
		return Result{Out: "unknown setting " + key + " — :settings lists them", Err: true}
	}
	if !hasValue || value == "" {
		return Result{Out: c.settingsExplain(opt)}
	}
	if value == unsetMark {
		return c.settingsUnset(opt)
	}
	return c.settingsSet(opt, unquote(value))
}

// cutSetting splits `key value` from `key=value`.
//
// Both spellings, because `key value` is what :theme taught and `key=value` is
// what the help column has room to show — and whichever separator comes first
// is the one that separates. A key is never spelled with a space and a value
// very often holds an `=`: splitting on the `=` first read
// `editor code --wait=1` as a request to set a setting called
// "editor code --wait", which is a refusal for a line that is entirely correct.
func cutSetting(arg string) (key, value string, ok bool) {
	space := strings.Index(arg, " ")
	equals := strings.Index(arg, "=")
	switch {
	case space < 0 && equals < 0:
		return arg, "", false
	case space >= 0 && (equals < 0 || space < equals):
		return arg[:space], arg[space+1:], true
	default:
		return arg[:equals], arg[equals+1:], true
	}
}

// unquote strips one layer of quotes the user typed, so `:settings editor "code
// --wait"` and `:settings editor code --wait` write the same line. The writer
// re-quotes, which is where the escaping is decided.
func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// settingsList answers what gluon can be told, what it currently is, and where
// that came from.
//
// Out and Modal are built from one row builder, so invariant 19 holds by
// construction rather than by care: the two cannot say different things because
// there is one place that decides what they say.
func (c *Core) settingsList() Result {
	headers, rows := c.settingRows()
	var b strings.Builder
	b.WriteString("settings\n")
	b.WriteString(settingsTable(headers, rows, c.styles()).Render())
	b.WriteString("\n" + c.settingsFooter())

	return Result{
		Out: b.String(),
		Modal: &ModalSpec{
			Title:   "settings",
			Summary: fmt.Sprintf("%s  browsed", plural(len(rows), "setting")),
			Headers: headers,
			Rows:    rows,
			Entries: c.settingEntries(),
			Refresh: ":settings",
			Note:    c.settingsFooter(),
		},
	}
}

// settingEntries is what opening a row shows: the explanation `:settings <key>`
// prints, and the values the setting would actually take.
//
// A table you can only read is the wrong shape for this particular table. Every
// other modal in gluon is a view of something the session computed — 4,812 rows
// of a slice, a query result — and there is nothing to do to those but look.
// A setting is the opposite: the whole reason to be looking at it is to change
// it, and a screen that made you close it, remember the key and retype it as a
// command would be a listing wearing a screen's clothes.
//
// One entry per row and in the same order, so the driver pairs them by index
// without either side knowing what the other's list is made of.
func (c *Core) settingEntries() []ModalEntry {
	opts := config.Options()
	out := make([]ModalEntry, 0, len(opts))
	for _, o := range opts {
		out = append(out, c.settingEntry(o))
	}
	return out
}

// defaultChoice is the label of the way back. It is a choice rather than a fact
// the footer mentions because it is one of the things a setting can be, and
// because the mark has to have somewhere to sit when nothing has been chosen.
const defaultChoice = "default"

// settingEntry is one setting, opened: what it is for, what it is now, and
// either the closed set to pick from or the line to type.
//
// Nothing here writes anything. Each choice carries the command that would make
// it true, and the driver submits that line — so a change made from the screen
// and a change typed at the prompt are the same code path, checked the same way
// and reported in the same words.
func (c *Core) settingEntry(o config.Option) ModalEntry {
	// Without its heading: the view puts the key in the title bar, and a
	// screen that named the setting twice in four lines would be spending the
	// two lines a short window has on saying nothing new.
	text := strings.TrimPrefix(c.settingsExplain(o), "  "+o.Key+"\n\n")
	e := ModalEntry{Title: o.Key, Text: text, More: ":settings " + o.Key}
	value, _ := c.settingValue(o, c.Config())

	switch {
	case o.Kind != config.Scalar:
		// Listed, not settable. It still opens: the explanation is worth
		// reading, and a row that did nothing when you pressed enter would
		// read as a broken screen rather than as an array gluon will not
		// rewrite for you.
		e.Why = o.Why

	case o.Family:
		// One row, fifteen settings. Each role opens its own entry, built the
		// same way, so a colour is typed where every other open value is.
		prefix := strings.TrimSuffix(o.Key, "<role>")
		for _, member := range o.Values() {
			sub, ok := config.Lookup(prefix + member)
			if !ok {
				continue
			}
			entry := c.settingEntry(sub)
			e.Choices = append(e.Choices, ModalChoice{
				Label: member, About: entry.Typed, Open: &entry})
		}

	case o.Values != nil:
		e.Current = value
		// theme.name has no way back: unsetting it is refused, because a
		// palette is chosen and never left blank. Offering a choice the
		// command would refuse is worse than not offering it.
		if o.Key != "theme.name" {
			e.Choices = append(e.Choices, ModalChoice{
				Label: defaultChoice, About: o.Default,
				Run: ":settings " + o.Key + " " + unsetMark,
			})
			if value == "" {
				e.Current = defaultChoice
			}
		}
		for _, v := range o.Values() {
			ch := ModalChoice{Label: v, Run: ":settings " + o.Key + " " + v}
			if o.About != nil {
				ch.About = o.About(v)
			}
			e.Choices = append(e.Choices, ch)
		}

	default:
		// An open value: a duration, a path, a command line. It opens with
		// what is in force, so changing 30s to 40s is an edit rather than a
		// retype, and `-` — the same mark the command takes — puts it back.
		e.Prefix = ":settings " + o.Key + " "
		e.Typed = value
	}
	return e
}

// settingRows is the table both renderings draw.
//
// `from` is a column rather than a decoration on the value, for the reason
// :env's redaction mark is one: "it is table" and "you chose table" are
// different facts, and a reader who cannot tell them apart cannot tell whether
// their config is being read at all.
func (c *Core) settingRows() (headers []string, rows [][]string) {
	headers = []string{"setting", "what it does", "value", "default", "allowed", "from"}
	cfg := c.Config()
	for _, o := range config.Options() {
		value, from := c.settingValue(o, cfg)
		rows = append(rows, []string{o.Key, o.Summary, value, o.Default, o.Allowed, from})
	}
	return headers, rows
}

// settingValue is what a setting is now, and where that came from.
func (c *Core) settingValue(o config.Option, cfg *config.Config) (value, from string) {
	if v, ok := c.unsaved[o.Key]; ok {
		return v, "session, not saved"
	}
	v := o.Get(cfg)
	if v == "" || v == "none" {
		if o.Kind == config.Scalar {
			return "", "default"
		}
		return "none", "default"
	}
	return v, "config.toml"
}

// settingsFooter is what the table cannot hold: where the file is, and the
// caveats that are true right now.
func (c *Core) settingsFooter() string {
	lines := []string{":settings <key> <value> changes one, :settings <key> " + unsetMark +
		" puts it back · " + shortenPath(config.File(), c)}
	cfg := c.Config()
	for _, o := range config.Options() {
		if o.Note == nil {
			continue
		}
		if n := o.Note(cfg); n != "" {
			lines = append(lines, o.Key+": "+n)
		}
	}
	return strings.Join(lines, "\n")
}

// settingsExplain is one setting, expanded. Shaped like `:help <command>`,
// because it answers the same kind of question.
func (c *Core) settingsExplain(o config.Option) string {
	cfg := c.Config()
	value, from := c.settingValue(o, cfg)
	if value == "" {
		value = "—"
	}

	var b strings.Builder
	b.WriteString("  " + o.Key + "\n\n  " + o.Summary + "\n\n")
	row := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "  %-10s %s\n", k, v)
		}
	}
	row("now", value+"    "+from)
	row("default", o.Default)
	row("accepts", o.Allowed)
	// The closed set, when naming it adds something the one-line description
	// did not. For a form the two are the same sentence, and printing it twice
	// reads as a bug in the command rather than as emphasis.
	if o.Values != nil {
		if vs := strings.Join(o.Values(), ", "); vs != "" && vs != o.Allowed {
			row("", vs)
		}
	}
	row("effect", o.Effect.String())
	if o.Note != nil {
		row("note", o.Note(cfg))
	}
	if o.Detail != "" {
		b.WriteString("\n")
		for _, l := range strings.Split(o.Detail, "\n") {
			b.WriteString(strings.TrimRight("  "+l, " ") + "\n")
		}
	}
	b.WriteString("\n")
	if o.Kind != config.Scalar {
		b.WriteString("  listed, not settable: " + o.Why + "\n")
	} else if o.Sample != "" {
		fmt.Fprintf(&b, "  :settings %s %s   writes  %s = %q\n",
			o.Key, o.Sample, o.Name, o.Sample)
	}
	return strings.TrimRight(b.String(), "\n")
}

// settingsSet changes one setting, and writes the choice down.
//
// The order is validate, write, apply — and the stance on a failed write is
// themeSet's, for its reason: the value you asked for is the value you get, and
// the message says what did not stick. A setting that silently reverted at the
// next prompt would be a demonstration rather than a setting.
func (c *Core) settingsSet(o config.Option, value string) Result {
	if o.Kind != config.Scalar {
		return Result{Out: "error: " + o.Key + " is listed, not settable — " + o.Why, Err: true}
	}
	if o.Values != nil && !contains(o.Values(), value) {
		return Result{Out: fmt.Sprintf("error: %s does not take %q — try one of %s",
			o.Key, value, strings.Join(o.Values(), ", ")), Err: true}
	}
	if o.Validate != nil {
		if err := o.Validate(value); err != nil {
			return Result{Out: "error: " + o.Key + " " + err.Error(), Err: true}
		}
	}

	// theme.name goes through :theme, so exactly one path decides what
	// switching a palette means — validate, save, repaint, report the line.
	// The same argument the picker makes about handing the choice back to the
	// command rather than writing the config itself.
	if o.Key == "theme.name" {
		return c.themeSet(value)
	}

	path := config.File()
	line, err := config.SetOption(path, o, value)
	// In force from here whatever the write did.
	o.Apply(c.cfg, value)
	res := c.applyLive(o)
	if err != nil {
		c.rememberUnsaved(o.Key, value)
		res.Out = fmt.Sprintf("%s is %s for this session — could not save: %v", o.Key, value, err)
		res.Err = true
		return res
	}
	delete(c.unsaved, o.Key)
	res.Out = fmt.Sprintf("%s is %s · %s in %s", o.Key, value, line, shortenPath(path, c))
	if o.Effect == config.NextRun {
		res.Out += "\n  " + o.Effect.String() + " reads it; this session is already running"
	}
	return res
}

// settingsUnset puts a setting back to its default.
func (c *Core) settingsUnset(o config.Option) Result {
	if o.Kind != config.Scalar {
		return Result{Out: "error: " + o.Key + " is listed, not settable — " + o.Why, Err: true}
	}
	if o.Key == "theme.name" {
		return Result{Out: "error: unset the theme by choosing one — :theme lists them", Err: true}
	}
	path := config.File()
	line, found, err := config.UnsetOption(path, o)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	o.Apply(c.cfg, "")
	delete(c.unsaved, o.Key)
	res := c.applyLive(o)
	if !found {
		res.Out = fmt.Sprintf("%s was not set — it is already %s", o.Key, o.Default)
		return res
	}
	res.Out = fmt.Sprintf("%s is back to %s · dropped %s from %s",
		o.Key, o.Default, line, shortenPath(path, c))
	return res
}

// applyLive puts a change into the running session and returns whatever the
// driver has to be told about.
//
// Only two settings need the driver at all: a form, because Core.Render holds a
// copy of it, and a theme role, because every style in internal/repl does.
// Everything else is either read fresh by whoever reads it next — :edit calls
// config.Load on every invocation — or belongs to Core, like the evaluator's
// timeout.
func (c *Core) applyLive(o config.Option) Result {
	switch {
	case strings.HasPrefix(o.Key, "value.form"):
		opts := c.cfg.ValueOptions()
		c.Values = opts
		return Result{Values: &ValueSpec{Options: opts}}
	case o.Key == "input.mode":
		// Read back through the accessor rather than from the raw value, so
		// putting the setting back to its default yields emacs rather than the
		// empty string — the same reason the theme case re-derives the spec.
		return Result{Input: &InputSpec{Mode: c.cfg.InputMode()}}
	case o.Table == "theme":
		spec := c.themeSpec("")
		// Apply the theme that is already on: the roles changed, not the
		// palette. An empty Apply would read as "the user wants to choose"
		// and open the picker over a session they did not ask to leave.
		spec.Apply = spec.Active
		return Result{Theme: &spec}
	case o.Key == "timeout":
		// Guarded like c.cfg is: a Core without an evaluator is what the tests
		// and `gluon doctor` build, and a setting they can write but not apply
		// is not a reason to panic.
		if c.ev != nil {
			c.ev.SetTimeout(c.cfg.EvalTimeout(eval.DefaultTimeout))
		}
	case o.Key == "value.items" || o.Key == "value.depth":
		// Both are passed every time rather than only the one that moved:
		// SetLimits takes the pair, and reading both from the config is how a
		// return to the default (`:settings value.items -`) puts the default
		// back rather than leaving the raised bound in force.
		if c.ev != nil {
			c.ev.SetLimits(c.cfg.ValueLimits(gluonrt.DefaultMaxItems, gluonrt.DefaultMaxDepth))
		}
	}
	return Result{}
}

// rememberUnsaved records a value that is in force but did not reach the file,
// so the `from` column says so rather than reporting it as configured.
func (c *Core) rememberUnsaved(key, value string) {
	if c.unsaved == nil {
		c.unsaved = map[string]string{}
	}
	c.unsaved[key] = value
}

// settingsTable is the printed form. One more lipgloss/table builder beside
// kvTable, dbTable and queryTable, built through Core.styles so a pipe gets no
// escapes and a terminal gets colour — invariant 30 by construction.
//
// The two columns that must survive a narrow window go leftmost, because the
// modal's columnsFor shrinks proportionally: what a setting is called and what
// it currently is are the answer, and the rest is context.
func settingsTable(headers []string, rows [][]string, st pretty.Styles) *table.Table {
	return table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(st.Border).
		Headers(headers...).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return st.Annot.Padding(0, 1)
			}
			switch col {
			case 0:
				return st.Type.Padding(0, 1)
			case 2:
				return st.Str.Padding(0, 1)
			default:
				return st.Annot.Padding(0, 1)
			}
		}).
		Rows(rows...)
}
