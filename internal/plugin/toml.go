package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/BurntSushi/toml"

	"github.com/sandboxws/gluon/internal/cmdspec"
)

// TOML plugins: the door for libraries gluon does not ship a plugin for.
//
// Three of the four capabilities are pure data — imports, aliases, a guide —
// and the fourth, a command, is a template that produces Go source. That source
// goes through the ordinary evaluator, so a TOML plugin has exactly the
// authority of a line the user could have typed, and no more. Nothing here can
// reach the filesystem, the network, or gluon's own process.
//
// Renderers still need Go, because a renderer is a function over a value tree
// and a template is not.

// ConfigDir is ~/.config/gluon/plugins.
func ConfigDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "gluon", "plugins")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "gluon", "plugins")
}

// file is the on-disk shape of a plugin.
type file struct {
	Name    string        `toml:"name"`
	Module  string        `toml:"module"`
	Summary string        `toml:"summary"`
	Imports []string      `toml:"imports"`
	Guide   string        `toml:"guide"`
	Command []fileCommand `toml:"command"`
	Alias   []fileAlias   `toml:"alias"`
}

type fileCommand struct {
	Name    string   `toml:"name"`
	Aliases []string `toml:"aliases"`
	Arg     string   `toml:"arg"`
	Summary string   `toml:"summary"`
	Detail  string   `toml:"detail"`
	Rewrite string   `toml:"rewrite"`
	Text    bool     `toml:"text"`

	// What the command takes, the way a builtin declares it. All optional: a
	// command that declares none is read off its arg, and its argument is Go.
	Kind    string        `toml:"kind"`
	See     []string      `toml:"see"`
	Param   []fileParam   `toml:"param"`
	Flag    []fileFlag    `toml:"flag"`
	Example []fileExample `toml:"example"`
}

type fileParam struct {
	Name     string   `toml:"name"`
	Optional bool     `toml:"optional"`
	Repeat   bool     `toml:"repeat"`
	Help     string   `toml:"help"`
	Values   []string `toml:"values"`
}

// A fileFlag is documentation, not grammar: the rewrite template receives the
// whole argument in {{.Arg}} and reads its own flags. Declaring one is what
// puts it on the command's page and in completion.
type fileFlag struct {
	Name   string   `toml:"name"`
	Value  string   `toml:"value"`
	Help   string   `toml:"help"`
	Repeat bool     `toml:"repeat"`
	Values []string `toml:"values"`
}

type fileExample struct {
	Line string `toml:"line"`
	Says string `toml:"says"`
}

// kinds are the spellings a plugin file may give its argument.
var kinds = map[string]cmdspec.Kind{
	"":      cmdspec.GoExpr,
	"go":    cmdspec.GoExpr,
	"name":  cmdspec.GoName,
	"words": cmdspec.Words,
	"sql":   cmdspec.SQL,
	"none":  cmdspec.NoArg,
}

// usage is a command's declaration, from the file's shape.
func (c fileCommand) usage() cmdspec.Spec {
	sp := cmdspec.Spec{Kind: kinds[c.Kind], See: c.See}
	for _, p := range c.Param {
		sp.Params = append(sp.Params, cmdspec.Param{
			Name: p.Name, Optional: p.Optional, Repeat: p.Repeat, Help: p.Help,
			Values: cmdspec.Values{Fixed: p.Values},
		})
	}
	for _, f := range c.Flag {
		sp.Flags = append(sp.Flags, cmdspec.Flag{
			Name: f.Name, Value: f.Value, Help: f.Help, Repeat: f.Repeat,
			Values: cmdspec.Values{Fixed: f.Values},
		})
	}
	for _, e := range c.Example {
		sp.Examples = append(sp.Examples, cmdspec.Example{Line: e.Line, Says: e.Says})
	}
	return sp
}

type fileAlias struct {
	Name   string `toml:"name"`
	Module string `toml:"module"`
}

// LoadDir reads every .toml in dir. A plugin that will not load is returned as
// an error rather than skipped: a file someone wrote and gluon silently ignored
// is the failure mode config.Load already refuses to have.
func LoadDir(dir string) ([]Plugin, []error) {
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, []error{fmt.Errorf("plugins: %w", err)}
	}

	var (
		out  []Plugin
		errs []error
		// Sorted, so two plugins claiming one command name resolve the same
		// way on every run.
		names []string
	)
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".toml") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		p, err := LoadFile(filepath.Join(dir, name))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, p)
	}
	return out, errs
}

// LoadFile reads one plugin file.
func LoadFile(path string) (Plugin, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f file
	md, err := toml.Decode(string(data), &f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	// Same stance as config.toml: an unknown key is nearly always a typo, and a
	// typo in a plugin file is otherwise a capability that silently never
	// appears.
	if und := md.Undecoded(); len(und) > 0 {
		keys := make([]string, len(und))
		for i, k := range und {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("%s: unknown setting %s", path, strings.Join(keys, ", "))
	}
	if err := f.validate(path); err != nil {
		return nil, err
	}

	tp := &tomlPlugin{meta: Meta{Name: f.Name, Module: f.Module, Summary: f.Summary}, dir: filepath.Dir(path)}
	for _, im := range f.Imports {
		tp.imports = append(tp.imports, Import{Name: packageName(im), Path: im})
	}
	for _, a := range f.Alias {
		tp.aliases = append(tp.aliases, Alias{Name: a.Name, Module: a.Module})
	}
	// A plugin with a module and no explicit alias still gets one for its own
	// name, since `:get <name>` is the obvious thing to type.
	if f.Module != "" && len(f.Alias) == 0 {
		tp.aliases = append(tp.aliases, Alias{Name: f.Name, Module: f.Module})
	}
	tp.guide = f.Guide

	for _, c := range f.Command {
		tmpl, err := template.New(c.Name).Parse(c.Rewrite)
		if err != nil {
			return nil, fmt.Errorf("%s: command %s: %w", path, c.Name, err)
		}
		tp.commands = append(tp.commands, Command{
			Name:    c.Name,
			Aliases: c.Aliases,
			Arg:     c.Arg,
			Summary: c.Summary,
			Detail:  c.Detail,
			Text:    c.Text,
			Usage:   c.usage(),
			Rewrite: rewriter(c, tmpl),
		})
	}
	return tp, nil
}

// rewriter turns the template into the function a command runs.
func rewriter(c fileCommand, tmpl *template.Template) func(string) (string, error) {
	required := strings.Contains(c.Arg, "<")
	usage := "usage: " + c.Name
	if c.Arg != "" {
		usage += " " + c.Arg
	}
	return func(arg string) (string, error) {
		arg = strings.TrimSpace(arg)
		if required && arg == "" {
			return "", fmt.Errorf("%s", usage)
		}
		var b strings.Builder
		if err := tmpl.Execute(&b, struct{ Arg string }{arg}); err != nil {
			return "", fmt.Errorf("%s: %w", c.Name, err)
		}
		return strings.TrimSpace(b.String()), nil
	}
}

func (f file) validate(path string) error {
	where := func(msg string) error { return fmt.Errorf("%s: %s", path, msg) }
	if f.Name == "" {
		return where("name is required")
	}
	if strings.ContainsAny(f.Name, " \t/") {
		return where(fmt.Sprintf("name %q must be a single word", f.Name))
	}
	for _, im := range f.Imports {
		if im == "" || strings.ContainsAny(im, ` "'`) {
			return where(fmt.Sprintf("imports: %q is not an import path", im))
		}
	}
	seen := map[string]bool{}
	for _, c := range f.Command {
		switch {
		case c.Name == "":
			return where("every command needs a name")
		case !strings.HasPrefix(c.Name, ":"):
			return where(fmt.Sprintf("command %q must start with a colon", c.Name))
		case c.Rewrite == "":
			return where(fmt.Sprintf("command %s has no rewrite", c.Name))
		case c.Summary == "":
			return where(fmt.Sprintf("command %s has no summary, so :help would show a blank line", c.Name))
		case seen[c.Name]:
			return where(fmt.Sprintf("command %s is defined twice", c.Name))
		}
		seen[c.Name] = true
		if err := c.validateUsage(); err != nil {
			return where(fmt.Sprintf("command %s: %v", c.Name, err))
		}
	}
	for _, a := range f.Alias {
		if a.Name == "" || a.Module == "" {
			return where("every alias needs a name and a module")
		}
	}
	return nil
}

// validateUsage checks what a command declares about itself. Each rule is one a
// reader would otherwise meet as a page that lies: an example for another
// command, a flag without its dash, a kind gluon has no reading of.
func (c fileCommand) validateUsage() error {
	if _, ok := kinds[c.Kind]; !ok {
		return fmt.Errorf("kind %q is not one of go, name, words, sql or none", c.Kind)
	}
	for _, p := range c.Param {
		if p.Name == "" {
			return fmt.Errorf("every param needs a name")
		}
	}
	for _, f := range c.Flag {
		if !strings.HasPrefix(f.Name, "-") || strings.ContainsAny(f.Name, " \t") {
			return fmt.Errorf("flag %q must be one word starting with a dash", f.Name)
		}
	}
	for _, e := range c.Example {
		first, _, _ := strings.Cut(strings.TrimSpace(e.Line), " ")
		if first != c.Name && !contains(c.Aliases, first) {
			return fmt.Errorf("example %q is not a line for %s", e.Line, c.Name)
		}
	}
	for _, s := range c.See {
		if !strings.HasPrefix(s, ":") {
			return fmt.Errorf("see %q must name a command, colon included", s)
		}
	}
	return nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// packageName is the qualifier an import path is known by, handling the two
// shapes that do not end in their own name.
func packageName(p string) string {
	parts := strings.Split(p, "/")
	last := parts[len(parts)-1]
	// A /vN suffix is a major version, not a package name.
	if len(parts) > 1 && len(last) > 1 && last[0] == 'v' && allDigits(last[1:]) {
		last = parts[len(parts)-2]
	}
	// gopkg.in/yaml.v3 is the yaml package. The path is a host's, not gluon's
	// — gluon reads YAML through go.yaml.in/yaml/v3 — and it is still handled,
	// because this guesses names for whatever the host's build list holds.
	if i := strings.LastIndex(last, "."); i > 0 && strings.HasPrefix(p, "gopkg.in/") {
		last = last[:i]
	}
	return last
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// tomlPlugin is a Plugin backed by a file. It implements every capability
// except Renderer, which needs Go.
type tomlPlugin struct {
	meta     Meta
	dir      string
	imports  []Import
	commands []Command
	aliases  []Alias
	guide    string
}

func (p *tomlPlugin) Meta() Meta          { return p.meta }
func (p *tomlPlugin) Imports() []Import   { return p.imports }
func (p *tomlPlugin) Commands() []Command { return p.commands }
func (p *tomlPlugin) Aliases() []Alias    { return p.aliases }

// HasGuide says whether the file names a guide, without reading it. Every
// tomlPlugin is a Guider, because the Go type has to be; this is what tells the
// ones that have something to show.
func (p *tomlPlugin) HasGuide() bool { return p.guide != "" }

// Guide reads the markdown file the plugin named, relative to itself.
func (p *tomlPlugin) Guide() string {
	if p.guide == "" {
		return ""
	}
	path := p.guide
	if !filepath.IsAbs(path) {
		path = filepath.Join(p.dir, path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "guide unavailable: " + err.Error()
	}
	return string(b)
}
