package repl

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sandboxws/gluon/internal/plugin"
	"github.com/sandboxws/gluon/internal/plugins"
	"github.com/sandboxws/gluon/internal/pretty"
	"github.com/sandboxws/gluon/internal/session"
)

// The plugin wiring.
//
// Everything here is arranged so that having plugins costs nothing per line.
// Activation reads the build list, which only changes at :get and :use — the
// same two points invariant 18 already requires the caches be dropped at — so
// nothing in this file runs while a line is being evaluated.

// initPlugins builds the set and activates it for the current session.
func (c *Core) initPlugins() {
	all := plugins.Builtin()
	if c.cfg != nil {
		loaded, errs := plugin.LoadDir(plugin.ConfigDir())
		all = append(all, loaded...)
		c.pluginErrs = errs
	}
	c.plugins = plugin.NewSet(all)
	c.refreshPlugins()
}

// refreshPlugins recomputes which plugins apply and everything derived from
// them. It must be called wherever the build list changes.
func (c *Core) refreshPlugins() {
	// Detection is deliberately not dropped here. The build list changing says
	// nothing about the project's files: a :get of an unrelated module, and a
	// :use -off followed by :use of the same directory, leave every file
	// detection read exactly as it was, and re-walking the ancestors and every
	// .env under the module to rediscover that was the whole cost. detect()
	// checks a stamp of what it read instead, so a changed .env still
	// invalidates and an unchanged project does not.
	if c.plugins == nil {
		return
	}
	// Requires reads go.mod. A failure here must not take the session down:
	// the honest fallback is "no third-party modules", which activates the
	// stdlib plugins and no others.
	reqs, _ := c.ev.Requires()

	disabled := map[string]bool{}
	if c.cfg != nil {
		for _, name := range c.cfg.Plugins.Disable {
			disabled[name] = true
		}
	}
	c.plugins.Activate(reqs, disabled)

	c.extra = pluginCommands(c.plugins)
	c.invalidateCommands()
	c.hooks = pluginHooks(c.plugins)
	c.applyPreload()
}

// applyPreload merges the config's imports with the active plugins'. A plugin
// import is not written into every program — it is a name→path map consulted
// only when a line actually names the qualifier (invariant 17), which is what
// makes a plugin that is never used cost nothing at all.
func (c *Core) applyPreload() {
	hostPath := ""
	if h := c.ev.Host(); h != nil {
		hostPath = h.Path
	}
	paths := c.cfg.ImportsFor(hostPath)
	if c.plugins != nil {
		for _, im := range c.plugins.Imports() {
			paths = append(paths, im.Path)
		}
	}
	c.ev.SetPreload(paths)
}

// pluginHooks is the type→formatter map the rich renderer consults.
func pluginHooks(set *plugin.Set) map[string]pretty.Hook {
	rs := set.Renders()
	if len(rs) == 0 {
		return nil
	}
	out := make(map[string]pretty.Hook, len(rs))
	for typ, r := range rs {
		out[typ] = pretty.Hook{Rich: r.Rich, Inline: r.Inline}
	}
	return out
}

// Hooks is the render hooks for the active plugins, for the driver to install.
func (c *Core) Hooks() map[string]pretty.Hook { return c.hooks }

// pluginCommands adapts each plugin command into a repl.Command.
//
// The adaptation is the whole safety argument: a plugin supplies a Rewrite that
// produces Go source, and this runs it through EvalTransient — the same path
// :bench, :err and :esc use. So a plugin command can do nothing a typed line
// could not, and it cannot mutate the session (invariant 14).
func pluginCommands(set *plugin.Set) []Command {
	var out []Command
	for _, tc := range set.Commands() {
		out = append(out, adapt(tc))
	}
	return out
}

func adapt(tc plugin.TaggedCommand) Command {
	rewrite, text, redacting, lang := tc.Rewrite, tc.Text, tc.Redact, tc.Lang
	from := tc.Plugin
	cmd := Command{
		Name:    tc.Name,
		Aliases: tc.Aliases,
		Arg:     tc.Arg,
		Group:   groupPlugin,
		Usage:   tc.Usage,
		Plugin:  from,
		Summary: tc.Summary,
		Detail:  strings.TrimRight(tc.Detail, "\n") + "\n\nFrom the " + from + " plugin.",
		// A plugin command runs code through EvalTransient, so as an MCP tool
		// it belongs to the eval tier, never the static one. The registry is
		// the tool manifest: a name, an argument spec and a summary is all a
		// tool definition needs.
		MCP: "go_" + strings.TrimPrefix(tc.Name, ":"),
		Run: func(c *Core, arg string) Result {
			src, err := rewrite(arg)
			if err != nil {
				return Result{Out: err.Error(), Err: true}
			}
			entry, err := session.Classify(src)
			if err != nil {
				return Result{Out: "error: " + from + " produced source gluon cannot parse: " +
					err.Error(), Err: true}
			}
			res, err := c.ev.EvalTransient(c.sess, entry)
			if err != nil {
				return Result{Out: "error: " + err.Error(), Err: true}
			}
			if text {
				if out, ok := textResult(res.Output); ok {
					// Redaction happens here rather than in the generated
					// source, so :src shows the question that was asked and
					// there is exactly one definition of "secret". See
					// plugin.Command.Redact.
					if redacting {
						var n int
						if out, n = redactLines(out); n > 0 {
							out += "\n" + redactionNote(n)
						}
					}
					// Redaction happens above, so what is tagged is what will
					// be printed: a colouriser never sees the secret.
					return sourceResult(tc.Name+" "+arg, out, lang)
				}
			}
			return Result{Out: c.format(res.Output)}
		},
	}
	// A command answered against the project's own database does not go
	// through Rewrite at all: the statement is declared, and the connection is
	// gluon's to own. See plugin.Command.Query.
	if q := tc.Query; q != nil {
		name := tc.Name
		cmd.Run = func(c *Core, arg string) Result { return c.runDBQuery(name, q, arg) }
	}
	// A command answered by dialling something does not go through Rewrite
	// either: the environment it reads and the values that must not appear in
	// its output are gluon's to resolve. See plugin.Command.Live.
	if lc := tc.Live; lc != nil {
		cmd.Run = func(c *Core, arg string) Result { return c.runLive(from, lc, arg) }
	}
	return cmd
}

// plugins is `:plugins` — what is loaded, what is active, and why.
//
// "Why" is the part worth printing. A command that is simply absent because its
// module is not in the build list is indistinguishable from a broken install
// unless something says which.
func (c *Core) pluginList() Result {
	if c.plugins == nil {
		return Result{Out: "no plugins"}
	}
	type row struct{ name, module, state, summary, adds string }
	var rows []row
	active := map[string]bool{}
	for _, p := range c.plugins.Active() {
		active[p.Meta().Name] = true
	}
	guides := map[string]bool{}
	for _, g := range c.plugins.Guides() {
		guides[g] = true
	}
	for _, p := range c.plugins.All() {
		m := p.Meta()
		state := "·"
		if active[m.Name] {
			state = "✓"
		}
		mod := m.Module
		if mod == "" {
			mod = "stdlib"
		}
		rows = append(rows, row{m.Name, mod, state, c.plugins.Why(m.Name), adds(p, guides[m.Name])})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })

	// The columns are as wide as their longest cell. A fixed width ran the
	// longest module paths into the column after them —
	// "github.com/shopspring/decimal github.com/…" — which is a table that
	// cannot be scanned in the one place it is long enough to need scanning.
	nameW, modW := 0, 0
	for _, r := range rows {
		nameW, modW = max(nameW, len(r.name)), max(modW, len(r.module))
	}
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "  %s %-*s %-*s  %s\n", r.state, nameW, r.name, modW, r.module, r.summary)
		// What the plugin adds goes under its name, on a line of its own, so
		// the row a reader scans for the plugin keeps its columns.
		if r.adds != "" {
			fmt.Fprintf(&b, "    %s\n", r.adds)
		}
	}
	for _, e := range c.pluginErrs {
		fmt.Fprintf(&b, "  ! %v\n", e)
	}
	for _, cf := range c.plugins.Conflicts() {
		fmt.Fprintf(&b, "  ! %s is %s's; %s also defines it\n", cf.Command, cf.Kept, cf.Dropped)
	}
	if len(rows) > 0 {
		b.WriteString("\n  ✓ active · · not active. :help <command> explains any of these commands,\n" +
			"  active or not, and says which plugin it came from.\n")
	}
	return Result{Out: strings.TrimRight(b.String(), "\n")}
}

// adds is the commands a plugin contributes, and its guide when it ships one.
func adds(p plugin.Plugin, guide bool) string {
	var parts []string
	if cm, ok := p.(plugin.Commander); ok {
		for _, c := range cm.Commands() {
			parts = append(parts, c.Name)
		}
	}
	if guide {
		parts = append(parts, "· :guide "+p.Meta().Name)
	}
	return strings.Join(parts, " ")
}

// Reference is every command gluon knows how to answer: the builtins, and
// every built-in plugin's commands adapted as they would
// be if the plugin were active, each carrying the plugin it came from. When
// several plugins provide a command — seven routers want :routes — every
// provider is here, in the registry's order.
//
// It is the docs generator's one reading of the registry, so what the docs
// print about a command and what :help prints are the same bytes' sources.
func Reference() []Command {
	out := append([]Command(nil), builtinCommands()...)
	for _, p := range plugins.Builtin() {
		cm, ok := p.(plugin.Commander)
		if !ok {
			continue
		}
		for _, pc := range cm.Commands() {
			out = append(out, adapt(plugin.TaggedCommand{Plugin: p.Meta().Name, Command: pc}))
		}
	}
	return out
}

// textResult unwraps a command whose value is the answer itself. The encoder
// sends a string's contents verbatim in Repr, so there is nothing to unquote.
func textResult(raw string) (string, bool) {
	userOut, vals := pretty.Parse(raw)
	if len(vals) != 1 || vals[0].Kind != "string" {
		return "", false
	}
	out := strings.TrimRight(userOut, "\n")
	if out == "" {
		return vals[0].Repr, true
	}
	return out + "\n" + vals[0].Repr, true
}

// guide is :guide — a plugin's cheatsheet, rendered as markdown when there is
// a terminal to render it in.
func (c *Core) guide(arg string) Result {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return Result{Out: "usage: :guide <plugin>   :plugins lists them", Err: true}
	}
	if c.plugins == nil {
		return Result{Out: "no plugins", Err: true}
	}
	text, ok := c.plugins.Guide(arg)
	if !ok {
		if !c.plugins.Known(arg) {
			return Result{Out: "no plugin named " + arg + " — :plugins lists them", Err: true}
		}
		return Result{Out: "the " + arg + " plugin ships no guide", Err: true}
	}
	return Result{Out: text, Modal: pageable("the "+arg+" plugin", text)}
}

// resolveGetAlias expands a plugin's `:get` shortcut. `:get uuid` is the whole
// point: the module is not in the build list yet, so the plugin that knows its
// path is by definition not active.
func (c *Core) resolveGetAlias(arg string) string {
	if arg == "" || strings.Contains(arg, "/") || c.plugins == nil {
		return arg
	}
	// A version suffix is kept: `:get uuid@v1.6.0`.
	name, version, hasVersion := strings.Cut(arg, "@")
	mod, ok := c.plugins.Aliases()[name]
	if !ok {
		return arg
	}
	if hasVersion {
		return mod + "@" + version
	}
	return mod
}
