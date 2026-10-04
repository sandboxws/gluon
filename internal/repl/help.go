package repl

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/plugin"
)

// This file answers what a command takes. There is one page per command and
// one builder for it: :help <command>, <command> --help, a pipe, `gluon -e`,
// an MCP tool and the help view all print the same bytes, because the day two
// of them disagree is the day one of them is wrong.

// helpWidth is the column the summaries in :help's listing line up on. Four
// builtins are exactly this wide — :test [-table] <exp>, :scratch [flag|name],
// :query [flags] <sql> and :bench [flags] a[,b] — so there is no headroom
// left, and a flag that does not fit in Arg belongs in the command's Usage,
// where its page lists it.
const helpWidth = 20

// helpLabel is the left column of a page's tables: an operand or a flag with
// its value, like `-t <duration>`. The help beside it starts at column
// helpLabel+4, which is helpWidth — the same gutter the listing uses.
const helpLabel = 16

// pageWidth is where a page's prose wraps. A page is read in a terminal and
// through a pipe, and eighty columns is the narrowest either is likely to be.
const pageWidth = 78

// help renders `:help`, or `:help <command>`.
//
// Bare, it is the listing in linear form — what a pipe, `gluon -e` and an MCP
// caller read — and, for a driver that can go full-screen, a view of the same
// commands to browse (invariant 19: Out says everything the view does).
func (c *Core) help(arg string) Result {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return Result{Out: c.helpAll(), Modal: c.helpView("")}
	}
	if !strings.HasPrefix(arg, ":") {
		arg = ":" + arg
	}
	cmd, ok := c.lookup(arg)
	if !ok {
		// A command an inactive plugin provides has a page too. It is exactly
		// the command a reader cannot try, so it is the one most worth reading
		// about before fetching anything.
		cmd, ok = c.dormant(arg)
	}
	if !ok {
		return Result{Out: "unknown command " + arg + " — :help lists them", Err: true}
	}
	return c.helpResult(cmd)
}

// helpResult is a command's page. One longer than a screen opens the help view
// on its row rather than a pager, so its examples can still be chosen — a
// pager could only show them.
func (c *Core) helpResult(cmd Command) Result {
	page := c.helpPage(cmd)
	var view *ModalSpec
	if pageable("", page) != nil {
		view = c.helpView(cmd.Name)
	}
	return Result{Out: page, Modal: view}
}

// helpPage is the page: the long synopsis, what the command does, its
// operands and flags, examples, the detail, and where to go next.
func (c *Core) helpPage(cmd Command) string { return c.page(cmd, true) }

// page builds a command's page. The help view leaves the examples out of its
// text because they are its choices, below it; everything else is the same
// builder, so the view and the page cannot say different things.
func (c *Core) page(cmd Command, examples bool) string {
	sp := cmd.Usage
	var b strings.Builder
	b.WriteString("  " + sp.Line(cmd.Name, cmd.Arg) + "\n\n")
	b.WriteString("  " + cmd.Summary + "\n")
	if st := c.pluginStatus(cmd); st != "" {
		writeWrapped(&b, "  ", st)
	}

	if rows := usageRows(sp, cmd.Arg); len(rows) > 0 {
		b.WriteString("\n")
		for _, r := range rows {
			writeRow(&b, r[0], r[1])
		}
	}

	if examples && len(sp.Examples) > 0 {
		b.WriteString("\n  examples\n")
		for _, ex := range sp.Examples {
			// An example is never wrapped: it is a line to type, and a
			// wrapped one would be two lines that are neither.
			b.WriteString("    " + ex.Line + "\n")
			if ex.Says != "" {
				writeWrapped(&b, "        ", ex.Says)
			}
		}
	}

	if d := strings.TrimRight(cmd.Detail, "\n"); d != "" {
		b.WriteString("\n")
		for _, line := range strings.Split(d, "\n") {
			if line == "" {
				b.WriteString("\n")
				continue
			}
			b.WriteString("  " + line + "\n")
		}
	}

	var tail [][2]string
	if len(sp.See) > 0 {
		tail = append(tail, [2]string{"see also", strings.Join(sp.See, "  ")})
	}
	if len(cmd.Aliases) > 0 {
		tail = append(tail, [2]string{"also", strings.Join(cmd.Aliases, "  ")})
	}
	if t := toolLine(cmd); t != "" {
		tail = append(tail, [2]string{"tool", t})
	}
	if len(tail) > 0 {
		b.WriteString("\n")
		for _, r := range tail {
			writeRow(&b, r[0], r[1])
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// helpView is every command as a table to browse: the ones in this session and
// the ones an inactive plugin would add. Opening a row shows its page; its
// examples are choices that put the line on the prompt, and its neighbours
// open in turn. opened is the command to land on, for a page too long to print.
func (c *Core) helpView(opened string) *ModalSpec {
	cmds := append([]Command(nil), c.Commands()...)
	for _, name := range c.dormantNames() {
		if d, ok := c.dormant(name); ok {
			cmds = append(cmds, d)
		}
	}
	spec := &ModalSpec{
		Title:   "help",
		Summary: fmt.Sprintf("help  %d commands browsed", len(cmds)),
		Headers: []string{"command", "takes", "flags", "what it does", "group"},
		Note:    "enter opens a command · / filters · an example you choose goes on the prompt, unrun",
		Opened:  opened,
	}
	if opened != "" {
		spec.Summary = "help " + opened + "  read"
	}
	for _, cmd := range cmds {
		var flags []string
		for _, f := range cmd.Usage.Visible() {
			flags = append(flags, f.Name)
		}
		spec.Rows = append(spec.Rows, []string{
			cmd.Name, cmd.Arg, strings.Join(flags, " "), cmd.Summary, c.groupOf(cmd),
		})
		spec.Entries = append(spec.Entries, c.helpEntry(cmd, true))
	}
	return spec
}

// groupOf is the group column: the builtin's group, or the plugin a command
// came from and whether it is active.
func (c *Core) groupOf(cmd Command) string {
	if cmd.Plugin == "" {
		return cmd.Group
	}
	if _, active := c.lookup(cmd.Name); active {
		return cmd.Plugin + " plugin"
	}
	return cmd.Plugin + " plugin · not active"
}

// helpEntry is one command opened in the view: its page, its examples as lines
// for the prompt, its neighbours to open, and for an inactive plugin the :get
// that would turn it on. Neighbours open one level deep — a see-also of a
// see-also is a table row away, and an entry that held the whole graph would
// hold it in every row.
func (c *Core) helpEntry(cmd Command, neighbours bool) ModalEntry {
	e := ModalEntry{Title: cmd.Name, Text: c.page(cmd, false), More: ":help " + cmd.Name}
	for _, ex := range cmd.Usage.Examples {
		e.Choices = append(e.Choices, ModalChoice{Label: ex.Line, About: ex.Says, Fill: ex.Line})
	}
	if neighbours {
		for _, s := range cmd.Usage.See {
			n, ok := c.lookup(s)
			if !ok {
				n, ok = c.dormant(s)
			}
			if !ok {
				continue
			}
			sub := c.helpEntry(n, false)
			e.Choices = append(e.Choices, ModalChoice{Label: s, About: n.Summary, Open: &sub})
		}
	}
	if cmd.Plugin != "" && c.plugins != nil {
		if _, active := c.lookup(cmd.Name); !active {
			for _, p := range c.plugins.All() {
				if m := p.Meta(); m.Name == cmd.Plugin && m.Module != "" {
					e.Choices = append(e.Choices, ModalChoice{
						Label: ":get " + m.Module,
						About: "turns the " + m.Name + " plugin on — this one fetches",
						Fill:  ":get " + m.Module,
					})
				}
			}
		}
	}
	return e
}

// usageRows is the operands worth a row, then the visible flags.
//
// An operand is worth a row when there is something to say beyond its name —
// help, or the values it takes. `<exp>` alone would be a row that repeats the
// synopsis.
func usageRows(sp cmdspec.Spec, arg string) [][2]string {
	var rows [][2]string
	for _, p := range sp.Operands(arg) {
		h := p.Help
		if h == "" {
			h = valuesText(p.Values)
		}
		if h != "" {
			rows = append(rows, [2]string{p.Name, h})
		}
	}
	for _, f := range sp.Visible() {
		label := f.Name
		if f.Value != "" {
			label += " <" + f.Value + ">"
		}
		h := f.Help
		if h == "" {
			h = valuesText(f.Values)
		}
		rows = append(rows, [2]string{label, h})
	}
	return rows
}

// valuesText is what a closed or session-known set reads as on a page.
func valuesText(v cmdspec.Values) string {
	switch {
	case len(v.Fixed) > 0:
		s := strings.Join(v.Fixed, " ")
		if v.Fold {
			s += ", any case"
		}
		return s
	case v.Source != "":
		return string(v.Source)
	}
	return ""
}

// toolLine names the MCP tool a command is, and the tier that serves it.
func toolLine(cmd Command) string {
	if cmd.MCP == "" {
		return ""
	}
	if !cmd.Static {
		return cmd.MCP + ", with gluon mcp --eval"
	}
	line := cmd.MCP + ", served without --eval"
	var runs []string
	for _, f := range cmd.Usage.Visible() {
		if f.Runs {
			runs = append(runs, f.Name)
		}
	}
	if len(runs) > 0 {
		// The tier is static because the tool never builds; a flag that does
		// is the prompt's alone (invariant 28).
		line += "; " + strings.Join(runs, ", ") + " only at the prompt"
	}
	return line
}

// pluginStatus is the line an inactive plugin's command carries: which plugin
// provides it, and what would turn it on. An active one's Detail already ends
// by naming its plugin, so it says nothing here.
func (c *Core) pluginStatus(cmd Command) string {
	if cmd.Plugin == "" || c.plugins == nil {
		return ""
	}
	if _, active := c.lookup(cmd.Name); active {
		return ""
	}
	st := "from the " + cmd.Plugin + " plugin"
	if others := c.providers(cmd.Name, cmd.Plugin); len(others) > 0 {
		st += " (also " + strings.Join(others, ", ") + ")"
	}
	return st + ", " + c.whyInactive(cmd.Plugin)
}

// whyInactive is Set.Why for a plugin that is not active, or a plain "not
// active" when activation has not run to say more.
func (c *Core) whyInactive(name string) string {
	if w := c.plugins.Why(name); w != "" {
		return w
	}
	return "not active"
}

// dormant finds a command an inactive plugin provides, adapted as it would be
// if the plugin were active. When several plugins provide it — seven routers
// want :routes — the first in the registry's order answers, which is the one
// that would win if they were all active.
func (c *Core) dormant(name string) (Command, bool) {
	if c.plugins == nil {
		return Command{}, false
	}
	for _, p := range c.plugins.All() {
		cm, ok := p.(plugin.Commander)
		if !ok {
			continue
		}
		for _, pc := range cm.Commands() {
			if pc.Name == name || contains(pc.Aliases, name) {
				return adapt(plugin.TaggedCommand{Plugin: p.Meta().Name, Command: pc}), true
			}
		}
	}
	return Command{}, false
}

// providers is every plugin that provides the command, but one.
func (c *Core) providers(name, except string) []string {
	var out []string
	for _, p := range c.plugins.All() {
		cm, ok := p.(plugin.Commander)
		if !ok || p.Meta().Name == except {
			continue
		}
		for _, pc := range cm.Commands() {
			if pc.Name == name {
				out = append(out, p.Meta().Name)
				break
			}
		}
	}
	return out
}

// dormantNames is every command an inactive plugin provides and no active one
// does, sorted, for the line :help ends its listing with.
func (c *Core) dormantNames() []string {
	if c.plugins == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range c.plugins.All() {
		cm, ok := p.(plugin.Commander)
		if !ok {
			continue
		}
		for _, pc := range cm.Commands() {
			if seen[pc.Name] {
				continue
			}
			seen[pc.Name] = true
			if _, active := c.lookup(pc.Name); !active {
				out = append(out, pc.Name)
			}
		}
	}
	sort.Strings(out)
	return out
}

// unknownCommand is the answer to a name the registry does not have. A name an
// inactive plugin provides is not unknown, and saying so would make an absent
// library look like a typo — the confusion :plugins exists to end.
func (c *Core) unknownCommand(name string) Result {
	if d, ok := c.dormant(name); ok {
		return Result{Out: name + " comes from the " + d.Plugin + " plugin, which is " +
			c.whyInactive(d.Plugin), Err: true}
	}
	return Result{Out: "unknown command " + name + " — try :help", Err: true}
}

func (c *Core) helpAll() string {
	var b strings.Builder
	b.WriteString(HelpText(c.Commands()))
	b.WriteString("\n")
	if names := c.dormantNames(); len(names) > 0 {
		b.WriteString("\n  not active here — :plugins says what turns each on\n")
		writeWrapped(&b, "  ", strings.Join(names, " "))
	}
	b.WriteString(helpTail)
	return strings.TrimRight(b.String(), "\n")
}

// HelpText is the grouped one-line listing of cmds — what :help prints above
// its tail, and what the README's command list is generated from, so the two
// cannot drift the way the hand-kept README table did.
func HelpText(cmds []Command) string {
	byGroup := map[string][]Command{}
	var seen []string
	for _, cmd := range cmds {
		if _, ok := byGroup[cmd.Group]; !ok {
			seen = append(seen, cmd.Group)
		}
		byGroup[cmd.Group] = append(byGroup[cmd.Group], cmd)
	}

	// Known groups first, in their declared order, then anything a plugin
	// invented, in the order it first appeared.
	var order []string
	for _, g := range groupOrder {
		if _, ok := byGroup[g]; ok {
			order = append(order, g)
		}
	}
	for _, g := range seen {
		if !contains(groupOrder, g) {
			order = append(order, g)
		}
	}

	var b strings.Builder
	for i, g := range order {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("  " + g + "\n")
		for _, cmd := range byGroup[g] {
			name := cmd.Name
			if cmd.Arg != "" {
				name += " " + cmd.Arg
			}
			fmt.Fprintf(&b, "  %-*s %s\n", helpWidth, name, cmd.Summary)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// writeRow writes one row of a page's table: a label in the left column and
// its help wrapped beside it. A label too wide for the column takes its own
// line, and the help starts under the gutter.
func writeRow(b *strings.Builder, label, help string) {
	indent := strings.Repeat(" ", 2+helpLabel+2)
	if help == "" {
		b.WriteString("  " + label + "\n")
		return
	}
	if utf8.RuneCountInString(label) > helpLabel {
		b.WriteString("  " + label + "\n")
		writeWrapped(b, indent, help)
		return
	}
	lines := wrap(help, pageWidth-len(indent))
	fmt.Fprintf(b, "  %-*s  %s\n", helpLabel, label, lines[0])
	for _, l := range lines[1:] {
		b.WriteString(indent + l + "\n")
	}
}

// writeWrapped writes text wrapped at pageWidth, every line indented.
func writeWrapped(b *strings.Builder, indent, text string) {
	for _, l := range wrap(text, pageWidth-utf8.RuneCountInString(indent)) {
		b.WriteString(indent + l + "\n")
	}
}

// wrap breaks text at spaces so no line is wider than width, counting runes.
// A word wider than width is left whole on its own line: breaking a URL or a
// flag in the middle would print something nobody could type.
func wrap(text string, width int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	line := words[0]
	for _, w := range words[1:] {
		if utf8.RuneCountInString(line)+1+utf8.RuneCountInString(w) > width {
			lines = append(lines, line)
			line = w
			continue
		}
		line += " " + w
	}
	return append(lines, line)
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// helpTail is the part of :help that is not a command list.
const helpTail = `
  it   the value the previous line printed  (irb's _)
  _1   the first value this session printed; :ls lists them

  tab     accept completion  ctrl-n/p  cycle completions
  ctrl-r  search history     ctrl-l    clear screen
  ctrl-c  cancel line        ctrl-d    quit

  :help <command>, or <command> --help, for its flags and examples

  The whole session replays on every line, so side effects repeat: an HTTP
  call fires again, time.Now() advances, rand differs. Replayed output is
  muted, but the re-execution is real. :undo or :save when that bites.
`
