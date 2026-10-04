// Package theme is the palette file format: what a colour is called, what the
// shipped themes say, and how a user's file overrides them.
//
// It exists as its own package because internal/ui does no I/O and should not
// start. Everything here resolves role names to colour *strings*; internal/ui
// is the only place a lipgloss.Style is built, and this package links neither
// lipgloss nor internal/config.
//
// Not importing internal/config is deliberate rather than tidy. config.validate
// has to reject a typo'd theme name at load time — the house stance is that a
// setting which silently does nothing is worse than an error — so config
// depends on theme, and theme therefore takes its directory as an argument.
// internal/plugin/toml.go resolves its own XDG path for the same reason.
package theme

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Default is the theme every other layer starts from. A named theme may set
// only the roles it cares about; the rest come from here.
const Default = "go"

// DefaultLight is the same thing for a light terminal, and it exists for one
// reason: five of gluon's fifteen roles describe a REPL rather than a
// highlighter, so no editor theme can answer them, so a conversion has to fall
// back to gluon's own — and gluon's own were chosen against black. `search`
// amber on white is a smear. This is where a light import gets its leftovers.
const DefaultLight = "go-light"

// None is the value that means "leave the terminal's own foreground alone".
//
// It is not the same as omitting a role. An omitted role inherits from the
// default theme; None says, positively, that this role is not painted — which
// is what `ident` needs in the terminal theme, where ANSI 7 is "white" and
// would be unreadable on a light background.
const None = "none"

// The grounds a theme can be drawn for. A palette is foreground colours only —
// gluon never paints a background, because the terminal owns that — so what a
// theme file cannot express, and therefore has to say, is the background it
// assumed while its colours were chosen.
//
// Either is not "unset". It is the positive answer terminal.toml gives: ANSI
// slots are whatever the terminal already resolved them to, so that palette
// follows you onto either ground rather than being drawn for one.
const (
	Dark   = "dark"
	Light  = "light"
	Either = "either"
)

var appearances = []string{Dark, Light, Either}

// A Palette is a resolved role→colour map. A value is anything lipgloss takes —
// an ANSI slot ("6"), a hex triple ("#4FC9EE") — or None.
type Palette map[string]string

// roles is every colour a theme file or a [theme] table may set.
//
// The four syntax roles beyond gluon's original ten arrived when the palette
// first coloured Go source on a page: showing code needs distinctions a value
// table never did.
var roles = []string{
	// The value renderer and the inspectors.
	"type", "annotation", "note", "string", "number", "border",
	// The REPL. `mode` is the prompt while normal mode has the keyboard, and
	// it is its own role rather than a reuse of `search` because it has to
	// contrast with `prompt` in particular: a palette whose prompt is already
	// amber would otherwise say "you are in normal mode" in the colour it says
	// "you are typing" with.
	"prompt", "mode", "error", "dim", "search",
	// Source.
	"keyword", "comment", "builtin", "punctuation", "ident",
}

var roleSet = func() map[string]bool {
	m := make(map[string]bool, len(roles))
	for _, r := range roles {
		m[r] = true
	}
	return m
}()

// Roles is every role name, sorted. config.validate and `gluon theme` both read
// it, so a role added here is offered and accepted everywhere at once.
func Roles() []string {
	out := append([]string(nil), roles...)
	sort.Strings(out)
	return out
}

// IsRole reports whether name is a colour a config or theme file may set.
func IsRole(name string) bool { return roleSet[name] }

// A File is one theme, as read.
type File struct {
	// Name is the basename it was found under, which is its identity. There is
	// deliberately no `name` key inside the file: two names for one theme is
	// one name too many, and they would eventually disagree.
	Name string
	// About is one line, printed by `gluon theme`.
	About string
	// Source records what a theme was converted from, when it was imported.
	Source string
	// Appearance is the ground the palette was drawn for: Dark, Light or
	// Either. Empty means the file did not say, which is allowed of a user's
	// own theme and not of a shipped one.
	Appearance string
	// Path is where it was read from. Empty means it is built in.
	Path string
	// Palette holds only the roles this file actually set.
	Palette Palette
}

// file is the on-disk shape. The table is called [theme] so that a block is
// copy-pasteable between a theme file and config.toml in either direction: one
// schema, learned once.
type file struct {
	About      string            `toml:"about"`
	Appearance string            `toml:"appearance"`
	Source     string            `toml:"source"`
	Theme      map[string]string `toml:"theme"`
}

// Dir is ~/.config/gluon/themes, following XDG the way config.Dir does.
func Dir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "gluon", "themes")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "gluon", "themes")
}

// Decode reads a theme from bytes. The strict-key stance is config's and
// plugin's: an unknown key is nearly always a typo, and a typo in a colour file
// is invisible unless something says so.
func Decode(name, path, data string) (File, error) {
	var f file
	md, err := toml.Decode(data, &f)
	if err != nil {
		return File{}, err
	}
	if und := md.Undecoded(); len(und) > 0 {
		keys := make([]string, len(und))
		for i, k := range und {
			keys[i] = k.String()
		}
		return File{}, fmt.Errorf("unknown setting %s", strings.Join(keys, ", "))
	}
	for role := range f.Theme {
		if !roleSet[role] {
			return File{}, fmt.Errorf("unknown role %q — try one of %s",
				role, strings.Join(Roles(), ", "))
		}
	}
	// A misspelled value is checked for the same reason a misspelled key is: it
	// would otherwise be a theme that quietly claims the wrong ground, and the
	// listing would say so in a place the author is not looking.
	if a := f.Appearance; a != "" && !slices.Contains(appearances, a) {
		return File{}, fmt.Errorf("appearance %q — try one of %s",
			a, strings.Join(appearances, ", "))
	}
	return File{Name: name, About: f.About, Appearance: f.Appearance,
		Source: f.Source, Path: path, Palette: f.Theme}, nil
}

// LoadFile reads one theme file. The basename without its extension is the
// theme's name.
func LoadFile(path string) (File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return File{}, err
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	f, err := Decode(name, path, string(data))
	if err != nil {
		return File{}, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// Named finds a theme by name: a file in dir first, so a user can fix a shipped
// theme without having to rename it, then the built-ins.
func Named(name, dir string) (File, error) {
	if name == "" {
		name = Default
	}
	if err := ValidName(name); err != nil {
		return File{}, err
	}
	if dir != "" {
		p := filepath.Join(dir, name+".toml")
		if _, err := os.Stat(p); err == nil {
			return LoadFile(p)
		}
	}
	if f, ok := Builtin(name); ok {
		return f, nil
	}
	return File{}, fmt.Errorf("no theme called %q — try one of %s",
		name, strings.Join(Names(dir), ", "))
}

// ValidName rejects anything that is not a plain filename. A theme name becomes
// a path under Dir(), so "../../etc/passwd" has to stop here rather than
// somewhere more interesting.
func ValidName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("a theme name may not be empty")
	case strings.ContainsAny(name, `/\`), strings.HasPrefix(name, "."):
		return fmt.Errorf("theme %q: a theme name is a plain name, not a path", name)
	}
	return nil
}

// Names is every theme that can be named, built-in and installed, sorted.
func Names(dir string) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range BuiltinNames() {
		seen[n] = true
		out = append(out, n)
	}
	if dir != "" {
		entries, err := os.ReadDir(dir)
		if err == nil {
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
					continue
				}
				n := strings.TrimSuffix(e.Name(), ".toml")
				if !seen[n] {
					seen[n] = true
					out = append(out, n)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// List is every nameable theme, with where it came from.
func List(dir string) ([]File, error) {
	var out []File
	var firstErr error
	for _, n := range Names(dir) {
		f, err := Named(n, dir)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		out = append(out, f)
	}
	return out, firstErr
}

// Resolve is the whole order: the default theme, then the named one, then the
// per-role overrides a config file wrote. Every role always has a value, so a
// caller never has to ask whether one is set.
func Resolve(name string, overrides map[string]string, dir string) (Palette, error) {
	base, ok := Builtin(Default)
	if !ok {
		// Unreachable: the built-ins are embedded and their test parses them.
		return nil, fmt.Errorf("the built-in %q theme is missing", Default)
	}
	p := make(Palette, len(roles))
	for role, v := range base.Palette {
		p[role] = v
	}
	// Named is consulted even when the name is the default, because Named looks
	// in dir before the built-ins: a user's own go.toml has to be able to shadow
	// the shipped one, which is the whole point of putting the defaults in a
	// file people can read.
	var err error
	{
		f, ferr := Named(name, dir)
		if ferr != nil {
			// Reported, not fatal — see ui.NewWithError. The palette returned
			// is complete either way, so a caller that only wants colour can
			// ignore this and a caller that reports setup can print it.
			err = ferr
		} else {
			for role, v := range f.Palette {
				p[role] = v
			}
		}
	}
	for role, v := range overrides {
		if v != "" && roleSet[role] {
			p[role] = v
		}
	}
	return p, err
}
