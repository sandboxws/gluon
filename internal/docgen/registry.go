package docgen

import (
	"fmt"
	"html/template"
	"regexp"
	"sort"
	"strings"

	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/config"
	"github.com/sandboxws/gluon/internal/plugin"
	"github.com/sandboxws/gluon/internal/plugins"
	"github.com/sandboxws/gluon/internal/repl"
	"github.com/sandboxws/gluon/internal/theme"
)

// The reference pages are the registries, read. Everything a page says about a
// command, a plugin, a setting or a theme is taken from the value that defines
// it — the same value :help, :plugins, :settings and :theme print from — so a
// fact the code holds is never typed a second time.

// Registry is what the reference pages range over.
type Registry struct {
	Groups  []Group
	Plugins []PluginDoc
	// Families is the plugins by the kind of library they describe — the
	// package each lives in — in the registry's order.
	Families []Family
	Settings []SettingDoc
	Themes   []ThemeDoc
	// CLI and MCP come from package main, which owns the cobra tree and the MCP
	// server; cmd/gluon's TestDocs fills them in.
	CLI []CLICommand
	MCP MCPDoc
	// Counts are the numbers the landing page and the hubs print.
	Counts Counts
	// HelpText is :help's grouped listing, for the README.
	HelpText string
}

// Counts are how many of each thing there are.
type Counts struct {
	Builtins, PluginCommands, Plugins, Settings, Themes, StaticTools, EvalTools int
}

// Group is one group of commands, in :help's order.
type Group struct {
	Name, Anchor string
	Commands     []CommandDoc
}

// Family is one kind of plugin.
type Family struct {
	Name, Anchor string
	Plugins      []PluginDoc
}

// CommandDoc is one command, as the commands page shows it.
type CommandDoc struct {
	Name, Anchor, Synopsis, Summary, Kind string
	Aliases                               []string
	Operands, Flags                       []Row
	Examples                              []ExampleDoc
	Detail                                template.HTML
	See                                   []string
	Tool                                  string
	// Plugins is every plugin that provides the command, first the one that
	// wins when several are active.
	Plugins []string
	// Filter is what the page's filter matches against.
	Filter string
}

// Row is a label and its help: an operand, a flag, a setting's value.
type Row struct {
	Label, Anchor, Help string
}

// ExampleDoc is one example, painted.
type ExampleDoc struct {
	Line template.HTML
	Says string
}

// PluginDoc is one built-in plugin.
type PluginDoc struct {
	Name, Anchor, Module, Summary, Family string
	Aliases                               []string
	Imports                               []string
	Renders                               []string
	Commands                              []string
	Guide                                 string
}

// SettingDoc is one public setting.
type SettingDoc struct {
	Key, Anchor, Summary, Default, Allowed, Sample, Effect, Why string
	Detail                                                      template.HTML
	Values                                                      []Row
}

// ThemeDoc is one built-in theme.
type ThemeDoc struct {
	Name, About, Appearance, Source string
	Roles                           []Row
	// Style sets the --cs tokens on a sample, so it is drawn in this theme.
	Style template.CSS
}

// CLICommand is one `gluon` subcommand, from the cobra tree.
type CLICommand struct {
	Path, Anchor, Use, Short, Long, Example string
	Flags                                   []Row
}

// MCPDoc is the tool surface, as a client receives it.
type MCPDoc struct {
	Tiers              []Tier
	StaticInstructions string
	EvalInstructions   string
}

// Tier is one group of tools, and when a client gets them.
type Tier struct {
	Name, Anchor, Lead string
	Tools              []Tool
}

// Names is the tier's tool names, in order.
func (t Tier) Names() []string {
	out := make([]string, len(t.Tools))
	for i, tool := range t.Tools {
		out[i] = tool.Name
	}
	return out
}

// Tool is one MCP tool.
type Tool struct {
	Name, Command, Description, Schema string
}

// ReadRegistry reads the registries this package can reach. CLI and MCP are
// left for the caller.
func ReadRegistry() Registry {
	var r Registry
	ref := repl.Reference()

	// Commands: builtins by group, then every plugin command once, carrying
	// every plugin that provides it.
	providers := map[string][]string{}
	var pluginOrder []string
	firstOf := map[string]repl.Command{}
	byGroup := map[string][]CommandDoc{}
	var groupNames []string
	known := map[string]bool{}
	for _, c := range ref {
		known[c.Name] = true
	}
	for _, c := range ref {
		if c.Plugin != "" {
			if _, seen := firstOf[c.Name]; !seen {
				firstOf[c.Name] = c
				pluginOrder = append(pluginOrder, c.Name)
			}
			providers[c.Name] = append(providers[c.Name], c.Plugin)
			continue
		}
		if _, ok := byGroup[c.Group]; !ok {
			groupNames = append(groupNames, c.Group)
		}
		byGroup[c.Group] = append(byGroup[c.Group], commandDoc(c, nil, known))
		r.Counts.Builtins++
	}
	for _, g := range groupNames {
		r.Groups = append(r.Groups, Group{Name: g, Anchor: "g-" + slug(g), Commands: byGroup[g]})
	}
	var fromPlugins []CommandDoc
	for _, name := range pluginOrder {
		fromPlugins = append(fromPlugins, commandDoc(firstOf[name], providers[name], known))
		r.Counts.PluginCommands++
	}
	r.Groups = append(r.Groups, Group{Name: "from plugins", Anchor: "g-from-plugins", Commands: fromPlugins})

	// The README lists what a session with every plugin active would list:
	// every builtin, then each plugin command once.
	var listing []repl.Command
	for _, c := range ref {
		if c.Plugin == "" {
			listing = append(listing, c)
		}
	}
	for _, name := range pluginOrder {
		listing = append(listing, firstOf[name])
	}
	r.HelpText = repl.HelpText(listing)

	fam := map[string]int{}
	for _, p := range plugins.Builtin() {
		d := pluginDoc(p)
		r.Plugins = append(r.Plugins, d)
		i, ok := fam[d.Family]
		if !ok {
			i = len(r.Families)
			fam[d.Family] = i
			r.Families = append(r.Families, Family{Name: d.Family, Anchor: "f-" + slug(d.Family)})
		}
		r.Families[i].Plugins = append(r.Families[i].Plugins, d)
	}
	r.Counts.Plugins = len(r.Plugins)

	for _, o := range config.Options() {
		r.Settings = append(r.Settings, settingDoc(o, known))
	}
	r.Counts.Settings = len(r.Settings)

	for _, name := range theme.BuiltinNames() {
		f, ok := theme.Builtin(name)
		if !ok {
			continue
		}
		r.Themes = append(r.Themes, themeDoc(f))
	}
	r.Counts.Themes = len(r.Themes)
	return r
}

// command finds a command by its name or one of its aliases.
func (r Registry) command(name string) (CommandDoc, bool) {
	for _, g := range r.Groups {
		for _, c := range g.Commands {
			if c.Name == name {
				return c, true
			}
			for _, a := range c.Aliases {
				if a == name {
					return c, true
				}
			}
		}
	}
	return CommandDoc{}, false
}

// flag finds one of a command's visible flags by its name.
func (d CommandDoc) flag(name string) (Row, bool) {
	for _, f := range d.Flags {
		if n, _, _ := strings.Cut(f.Label, " "); n == name {
			return f, true
		}
	}
	return Row{}, false
}

// commandDoc reads one command.
func commandDoc(c repl.Command, providers []string, known map[string]bool) CommandDoc {
	sp := c.Usage
	anchor := slug(strings.TrimPrefix(c.Name, ":"))
	d := CommandDoc{
		Name:     c.Name,
		Anchor:   anchor,
		Synopsis: sp.Line(c.Name, c.Arg),
		Summary:  c.Summary,
		Kind:     sp.Kind.String(),
		Aliases:  c.Aliases,
		See:      sp.See,
		Plugins:  providers,
		Detail:   prose(strings.TrimSuffix(strings.TrimRight(c.Detail, "\n"), "\n\nFrom the "+c.Plugin+" plugin."), known),
	}
	for _, p := range sp.Operands(c.Arg) {
		help := p.Help
		if help == "" {
			help = valuesText(p.Values)
		}
		if help != "" {
			d.Operands = append(d.Operands, Row{Label: p.Name, Help: help})
		}
	}
	for _, f := range sp.Visible() {
		label := f.Name
		if f.Value != "" {
			label += " <" + f.Value + ">"
		}
		help := f.Help
		if help == "" {
			help = valuesText(f.Values)
		}
		d.Flags = append(d.Flags, Row{Label: label, Anchor: anchor + "-" + slug(f.Name), Help: help})
	}
	for _, ex := range sp.Examples {
		d.Examples = append(d.Examples, ExampleDoc{Line: paintLine(ex.Line, sp.Kind), Says: ex.Says})
	}
	if c.MCP != "" {
		switch {
		case c.Plugin != "":
			d.Tool = c.MCP + " · with --eval, while the plugin is active"
		case c.Static:
			d.Tool = c.MCP + " · static, served without --eval"
		default:
			d.Tool = c.MCP + " · with --eval"
		}
	}
	d.Filter = strings.ToLower(strings.Join(append(append([]string{c.Name, c.Summary, d.Tool},
		c.Aliases...), providers...), " "))
	for _, f := range sp.Visible() {
		d.Filter += " " + strings.ToLower(f.Name)
	}
	return d
}

// valuesText is how a value set reads on a page.
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

// pluginDoc reads one plugin.
func pluginDoc(p plugin.Plugin) PluginDoc {
	m := p.Meta()
	d := PluginDoc{Name: m.Name, Anchor: slug(m.Name), Module: m.Module, Summary: m.Summary, Family: family(p)}
	if a, ok := p.(plugin.Aliaser); ok {
		for _, al := range a.Aliases() {
			d.Aliases = append(d.Aliases, al.Name+" → "+al.Module)
		}
	}
	if im, ok := p.(plugin.Importer); ok {
		for _, i := range im.Imports() {
			d.Imports = append(d.Imports, i.Path)
		}
	}
	if rd, ok := p.(plugin.Renderer); ok {
		for _, r := range rd.Renders() {
			d.Renders = append(d.Renders, r.Type)
		}
		// A plugin may build its renderers from a map, and a page that
		// changed order between two renderings would never be current.
		sort.Strings(d.Renders)
	}
	if cm, ok := p.(plugin.Commander); ok {
		for _, c := range cm.Commands() {
			d.Commands = append(d.Commands, c.Name)
		}
	}
	if g, ok := p.(plugin.Guider); ok {
		d.Guide = g.Guide()
	}
	return d
}

// family is the plugin's package, which is how the plugins are organised: by
// what kind of library they describe.
func family(p plugin.Plugin) string {
	path := fmt.Sprintf("%T", p)
	path = strings.TrimPrefix(path, "*")
	pkg, _, _ := strings.Cut(path, ".")
	return map[string]string{
		"stdlib": "standard library", "ids": "identifiers and numbers", "db": "databases",
		"web": "web and command-line", "di": "dependency injection", "config": "configuration",
		"encoding": "encodings", "rpc": "RPC",
	}[pkg]
}

// settingDoc reads one setting.
func settingDoc(o config.Option, known map[string]bool) SettingDoc {
	d := SettingDoc{
		Key: o.Key, Anchor: slug(o.Key), Summary: o.Summary, Default: o.Default,
		Allowed: o.Allowed, Sample: o.Sample, Why: o.Why,
		Detail: prose(o.Detail, known),
	}
	switch o.Effect {
	case config.Live:
		d.Effect = "takes effect at once"
	case config.NextRun:
		d.Effect = "takes effect at the next start"
	}
	if o.Values != nil && !o.Family {
		for _, v := range o.Values() {
			about := ""
			if o.About != nil {
				about = o.About(v)
			}
			d.Values = append(d.Values, Row{Label: v, Help: about})
		}
	}
	return d
}

// themeDoc reads one theme.
func themeDoc(f theme.File) ThemeDoc {
	d := ThemeDoc{Name: f.Name, About: f.About, Appearance: f.Appearance, Source: f.Source}
	var css strings.Builder
	for _, role := range theme.Roles() {
		v, ok := f.Palette[role]
		if !ok {
			continue
		}
		d.Roles = append(d.Roles, Row{Label: role, Help: v})
		if strings.HasPrefix(v, "#") {
			fmt.Fprintf(&css, "--cs-%s:%s;", role, v)
		}
	}
	d.Style = template.CSS(css.String())
	return d
}

// slug is an anchor for a name: :bench becomes bench, value.form stays
// value.form, and a flag's dash goes.
var nonAnchor = regexp.MustCompile(`[^a-zA-Z0-9.]+`)

func slug(s string) string {
	return strings.Trim(nonAnchor.ReplaceAllString(s, "-"), "-")
}
