// Package config reads ~/.config/gluon/config.toml.
//
// Everything here is optional and everything has a working default: gluon has
// to be usable with no config at all, because that is how it is first run. A
// malformed file is reported rather than ignored — a preloaded import that
// silently did nothing would be worse than an error naming the line.
//
// The most useful setting is `imports`. Preloaded imports are not written into
// every program; they are a name-to-path map consulted only when a line
// actually names the qualifier, exactly as the host package index is. So an
// import that goes unused costs nothing, and one that is used skips the ~135ms
// goimports pass it would otherwise have paid for.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/sandboxws/gluon/internal/pretty"
	"github.com/sandboxws/gluon/internal/theme"
)

// Config is the parsed file. The zero value is the default configuration.
type Config struct {
	// Imports are preloaded: `strings` here means a line may say
	// strings.ToUpper without gluon having to run goimports to discover it.
	Imports []string `toml:"imports"`
	// Editor overrides $VISUAL and $EDITOR, which are both commonly unset.
	Editor string `toml:"editor"`
	// Timeout bounds one evaluation, as a Go duration ("45s").
	Timeout string `toml:"timeout"`
	// Banner is how much the startup screen says: "full", "compact" or "off".
	Banner string `toml:"banner"`
	// Hosts are per-host rules keyed on module path, for imports that the
	// host's own package index cannot offer — a third-party dependency of the
	// host, say.
	Hosts map[string]Host `toml:"hosts"`
	// Plugins controls which library plugins apply.
	Plugins Plugins `toml:"plugins"`
	// Databases are the [[database]] entries: how to reach a project's
	// database, and never the password for it. An array of tables rather than
	// nested keys so that adding one is an append — see AppendDatabase.
	Databases []Database `toml:"database"`
	// Theme overrides gluon's colours, one role at a time. A value is anything
	// lipgloss takes: an ANSI number ("6"), or a hex triple ("#5fafd7").
	// Unset roles keep whatever the selected theme says.
	//
	// The reserved key `name` inside [theme] selects a whole palette instead —
	// a built-in, or a file in ~/.config/gluon/themes. It is lifted out into
	// ThemeName at load, so this map stays a pure role→colour table and `name`
	// can never be mistaken for a role.
	Theme map[string]string `toml:"theme"`
	// ThemeName is the palette selected by [theme] name. Empty means gluon's
	// default; "terminal" restores the ANSI 0-15 palette gluon shipped before.
	ThemeName string `toml:"-"`
	// Value is [value]: the shape a value is drawn in, as opposed to the
	// colour it is drawn in, which is [theme] above.
	Value Value `toml:"value"`
	// Scratch is [scratch]: which scratchpad an interactive session lands in.
	Scratch Scratch `toml:"scratch"`
	// Input is [input]: how the prompt reads keys.
	Input Input `toml:"input"`

	// Path is where this was read from, for `gluon doctor`. Empty means
	// defaults were used.
	Path string `toml:"-"`
}

// Input is the [input] table: how the prompt reads keys.
//
// A table with one key rather than a top-level `input`, because `editor` is
// already a top-level string and TOML cannot have one key be a string and a
// table at once — the same collision `[value] form` already documents.
type Input struct {
	// Mode is "emacs" or "vim". Empty means gluon's default, which is emacs —
	// the readline subset bubbles/textinput has always bound.
	Mode string `toml:"mode"`
}

// The two ways the prompt can read keys. emacs is honest rather than
// aspirational: textinput's default keymap really is the readline emacs subset,
// and naming it after what it is makes the pair a closed set with no aliases.
const (
	InputEmacs = "emacs"
	InputVim   = "vim"
)

// InputModes is the closed set, for the setting's chooser and its validator.
func InputModes() []string { return []string{InputEmacs, InputVim} }

// ParseInputMode is the one rule for the value — validate refuses what it
// rejects and the accessor falls back on it, which is ParseValueLimit's
// precedent. Two copies would disagree about an edge exactly once, in the
// direction of a config that loads and does nothing.
func ParseInputMode(v string) (string, error) {
	switch v {
	case InputEmacs, InputVim:
		return v, nil
	}
	return "", fmt.Errorf("%q is not a way of reading keys — try %s",
		v, strings.Join(InputModes(), " or "))
}

// InputMode is the mode in force, which is emacs unless the file says
// otherwise. It goes through ParseInputMode so a value that would have been
// refused at load can never be returned here either.
func (c *Config) InputMode() string {
	if c == nil {
		return InputEmacs
	}
	if mode, err := ParseInputMode(c.Input.Mode); err == nil {
		return mode
	}
	return InputEmacs
}

// The three forms the startup screen takes. It is also what gluon falls back
// to on a terminal too narrow for the wordmark, so that "small" is described
// once rather than twice.
const (
	BannerFull    = "full"
	BannerCompact = "compact"
	BannerOff     = "off"
)

// BannerForms is the closed set, for the setting's chooser and its validator.
func BannerForms() []string { return []string{BannerFull, BannerCompact, BannerOff} }

// ParseBanner is the one rule for the value, as ParseInputMode is for its own.
func ParseBanner(v string) (string, error) {
	switch v {
	case BannerFull, BannerCompact, BannerOff:
		return v, nil
	}
	return "", fmt.Errorf("%q is not a startup screen — try %s",
		v, strings.Join(BannerForms(), ", "))
}

// BannerForm is the form in force, which is the full one unless the file says
// otherwise.
func (c *Config) BannerForm() string {
	if c == nil {
		return BannerFull
	}
	if form, err := ParseBanner(c.Banner); err == nil {
		return form
	}
	return BannerFull
}

// Scratch is the [scratch] table.
//
// One key, and it exists because a scratchpad puts what you typed on disk. Some
// sessions are not for keeping and some machines are not yours; `pad = "-"` is
// the persistent way to say so, which a flag typed every time is not.
type Scratch struct {
	// Pad is the scratchpad to land in. Empty means gluon's default, which is
	// the one named "default"; "-" or "off" means none.
	Pad string `toml:"pad"`
}

// Value is the [value] table: how a value is drawn.
//
// The reserved key `default` inside [value.form] selects the form every kind
// takes; it is lifted out into DefaultForm at load, exactly as [theme] name is
// and for the same reason. Without the lift, `default` would be a kind — and
// `[value] form = "tree"` beside `[value.form]` is not even expressible, since
// TOML cannot have one key be a string and a table at once.
type Value struct {
	// Form is the kind→form table: "list", "map" or "struct", each naming a
	// form. A kind absent here takes DefaultForm.
	Form map[string]string `toml:"form"`
	// DefaultForm is [value.form] default. Empty means gluon's default, which
	// is the bordered table it has always drawn.
	DefaultForm string `toml:"-"`
	// Items and Depth bound how much of a value the child describes: how many
	// elements of one collection, and how many nested levels. Empty means
	// gluon's defaults, the numbers it used before either was settable.
	//
	// They are strings for the reason every other scalar setting is one:
	// setScalar writes a quoted string and afterValue parses one, which is
	// what lets a trailing comment survive an edit.
	Items string `toml:"items"`
	Depth string `toml:"depth"`
}

// MaxValueLimit is the largest either bound may be set to. Past it the setting
// is refused rather than silently doing nothing: the whole-value node budget
// stops the child long before, so a number this large is a misunderstanding
// rather than a preference.
const MaxValueLimit = 100000

// ParseValueLimit is the one rule for both bounds — validate refuses what it
// rejects, and the accessor falls back on it. Two copies would disagree about
// an edge exactly once, in the direction of a config that loads and does
// nothing.
func ParseValueLimit(v string) (int, error) {
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%q is not a whole number", v)
	}
	if n < 1 {
		return 0, fmt.Errorf("%q must be positive", v)
	}
	if n > MaxValueLimit {
		return 0, fmt.Errorf("%q is above the %d gluon accepts", v, MaxValueLimit)
	}
	return n, nil
}

// Plugins is the [plugins] table. Everything is on by default: a plugin is
// inert until the session actually names its module, so an enable list would
// only be a second thing to keep in sync with the build list.
type Plugins struct {
	// Disable names plugins that must not activate, whatever the build list
	// says.
	Disable []string `toml:"disable"`
}

// Host is a rule that applies when the session is attached to this module path.
type Host struct {
	Imports []string `toml:"imports"`
}

// Dir is ~/.config/gluon, following XDG.
func Dir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "gluon")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "gluon")
}

// File is the path Load reads.
func File() string {
	d := Dir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "config.toml")
}

// Load reads the config file. A missing file is not an error; a malformed one
// is, because the alternative is a setting that silently does nothing.
func Load() (*Config, error) {
	p := File()
	if p == "" {
		return &Config{}, nil
	}
	return LoadFile(p)
}

// LoadFile reads a specific path.
func LoadFile(p string) (*Config, error) {
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	md, err := toml.Decode(string(data), &c)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	// An unknown key is nearly always a typo, and a typo in a config file is
	// invisible unless something says so.
	if und := md.Undecoded(); len(und) > 0 {
		keys := make([]string, len(und))
		for i, k := range und {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("%s: unknown setting %s", p, strings.Join(keys, ", "))
	}
	c.Path = p
	// Relative paths in a [[database]] resolve against the config that named
	// them, not the working directory, so a session's database does not depend
	// on where gluon was launched from.
	dir := filepath.Dir(p)
	for i := range c.Databases {
		c.Databases[i].Dir = dir
		c.Databases[i].From = p
	}
	// Lifted before validate, or `name` reads as a misspelled role. Because
	// Theme is a map, BurntSushi marks the key decoded and the strict-key check
	// above has already let it through — so this is the only place that has to
	// know the key is reserved.
	if n, ok := c.Theme["name"]; ok {
		c.ThemeName = n
		delete(c.Theme, "name")
	}
	// The same lift, for the same reason: `default` names a form rather than a
	// kind, and leaving it in the map would make it one.
	if f, ok := c.Value.Form["default"]; ok {
		c.Value.DefaultForm = f
		delete(c.Value.Form, "default")
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	c.expand()
	return &c, nil
}

func (c *Config) validate() error {
	if c.Timeout != "" {
		d, err := time.ParseDuration(c.Timeout)
		if err != nil {
			return fmt.Errorf("timeout %q: %w", c.Timeout, err)
		}
		if d <= 0 {
			return fmt.Errorf("timeout %q must be positive", c.Timeout)
		}
	}
	for _, im := range c.Imports {
		if im == "" || strings.ContainsAny(im, ` "'`) {
			return fmt.Errorf("imports: %q is not an import path", im)
		}
	}
	if err := validateDatabases(c.Databases); err != nil {
		return err
	}
	// A misspelled role is invisible otherwise: the colour it was meant to
	// change simply stays default, which reads as "gluon ignored my config".
	for role := range c.Theme {
		if !theme.IsRole(role) {
			return fmt.Errorf("theme: unknown role %q — try one of %s (or name, to pick a whole theme)",
				role, strings.Join(ThemeRoles(), ", "))
		}
	}
	// Only the shape. Whether a theme of that name exists is a fact about a
	// directory rather than about this file, and it changes independently of
	// it — a theme deleted, a config synced from another machine. gluon does
	// not refuse to start over a colour: internal/ui falls back and reports,
	// and `gluon doctor` names it. What must be caught here is a name that is
	// not a name, because it becomes a path under the themes directory.
	if c.ThemeName != "" {
		if err := theme.ValidName(c.ThemeName); err != nil {
			return fmt.Errorf("theme: %w", err)
		}
	}
	// Unlike a theme name, which is checked for shape only because a theme is a
	// file that may appear or vanish independently of this config, a form is a
	// closed set gluon compiles in. So membership is checkable here, and a
	// misspelling is caught at load rather than silently drawing tables.
	if c.Value.DefaultForm != "" {
		if _, err := pretty.ParseForm(c.Value.DefaultForm); err != nil {
			return fmt.Errorf("value.form: %w", err)
		}
	}
	// A closed set gluon compiles in, so membership is checkable here — the
	// argument value.form makes. A misspelling caught at load is one the user
	// is told about; one caught nowhere is a prompt that quietly ignored them.
	if c.Input.Mode != "" {
		if _, err := ParseInputMode(c.Input.Mode); err != nil {
			return fmt.Errorf("input.mode: %w", err)
		}
	}
	if c.Banner != "" {
		if _, err := ParseBanner(c.Banner); err != nil {
			return fmt.Errorf("banner: %w", err)
		}
	}
	if c.Value.Items != "" {
		if _, err := ParseValueLimit(c.Value.Items); err != nil {
			return fmt.Errorf("value.items: %w", err)
		}
	}
	if c.Value.Depth != "" {
		if _, err := ParseValueLimit(c.Value.Depth); err != nil {
			return fmt.Errorf("value.depth: %w", err)
		}
	}
	for kind, form := range c.Value.Form {
		if !pretty.IsFormKind(kind) {
			return fmt.Errorf("value.form: %q is not a kind that has a form of its own — try one of %s (or default, for every kind)",
				kind, strings.Join(pretty.FormKinds(), ", "))
		}
		if _, err := pretty.ParseForm(form); err != nil {
			return fmt.Errorf("value.form.%s: %w", kind, err)
		}
	}
	return nil
}

// ValueOptions is the [value] table as the renderer needs it.
//
// It cannot fail: validate refuses a file naming a form that does not exist, so
// nothing unparseable reaches here. A caller that built a Config by hand rather
// than by loading one gets the default, which is what gluon has always drawn.
func (c *Config) ValueOptions() pretty.Options {
	opts := pretty.Options{}
	if f, err := pretty.ParseForm(c.Value.DefaultForm); err == nil && c.Value.DefaultForm != "" {
		opts.Form = f
	}
	for kind, name := range c.Value.Form {
		f, err := pretty.ParseForm(name)
		if err != nil {
			continue
		}
		if opts.Kinds == nil {
			opts.Kinds = make(map[string]pretty.Form, len(c.Value.Form))
		}
		opts.Kinds[kind] = f
	}
	return opts
}

// ThemeRoles is the sorted role list, for error messages and for the test that
// pins it against internal/ui.
//
// internal/theme owns the list. It used to be duplicated here, which meant a
// role added to one and not the other was a config key that validated and then
// coloured nothing; delegating makes that impossible rather than tested-for.
func ThemeRoles() []string { return theme.Roles() }

// expand resolves a leading ~ in every path, so config files can be written the
// way people type paths.
func (c *Config) expand() {
	c.Editor = expandPath(c.Editor)
	for i := range c.Databases {
		d := &c.Databases[i]
		for _, p := range []*string{&d.DSNFile, &d.File, &d.PassFile} {
			*p = expandPath(*p)
		}
		c.Databases[i] = d.resolvePaths()
	}
}

func expandPath(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~"))
}

// EvalTimeout is the configured timeout, or fallback when unset.
func (c *Config) EvalTimeout(fallback time.Duration) time.Duration {
	if c.Timeout == "" {
		return fallback
	}
	d, err := time.ParseDuration(c.Timeout)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

// ValueLimits is the [value] bounds as the evaluator needs them: how many
// elements of one collection and how many nested levels the child describes.
//
// Like EvalTimeout it cannot fail. validate refuses a file whose bounds do not
// parse, so anything unparseable here belongs to a Config built by hand, and
// the answer for that is the fallback rather than a panic.
func (c *Config) ValueLimits(items, depth int) (int, int) {
	return valueLimitOr(c.Value.Items, items), valueLimitOr(c.Value.Depth, depth)
}

func valueLimitOr(v string, fallback int) int {
	if v == "" {
		return fallback
	}
	n, err := ParseValueLimit(v)
	if err != nil {
		return fallback
	}
	return n
}

// ImportsFor is the preloaded import set for a session, with any rules for the
// attached host's module path appended.
func (c *Config) ImportsFor(hostPath string) []string {
	out := append([]string(nil), c.Imports...)
	if hostPath != "" {
		if h, ok := c.Hosts[hostPath]; ok {
			out = append(out, h.Imports...)
		}
	}
	return out
}
