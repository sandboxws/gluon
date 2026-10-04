package repl

import (
	"fmt"
	"strings"

	"github.com/sandboxws/gluon/internal/config"
	// Aliased because this package already has a `theme`: the one variable
	// holding the palette in force. What this file needs is the other thing —
	// the catalogue of palettes that could be in force — and it gets a name
	// that says which is which.
	palettes "github.com/sandboxws/gluon/internal/theme"
)

// `:theme` is the half of theming that is not a colour: which palettes exist,
// which one is on, and switching between them while a session is running.
//
// cmd/gluon/theme.go argues that this belongs on the CLI rather than in the
// REPL, because it is something you ask once while setting gluon up. That was
// wrong about how choosing a colour scheme actually goes: you do not know
// which one you want, you know it when you see your own code in it. Seeing it
// means switching, and switching from the shell means restarting the session
// you were working in. So the two now overlap deliberately — `gluon theme` is
// still there for the shell, and this is the one you use while looking at Go.
//
// The CLI command keeps `import`, which is a file conversion and has no
// business at a prompt.

func (c *Core) themeCmd(arg string) Result {
	if name := strings.TrimSpace(arg); name != "" {
		return c.themeSet(name)
	}
	return c.themeList()
}

// themeList answers what is installed and what is on. The ThemeSpec beside it
// is what lets a terminal offer the choice instead of just printing it.
func (c *Core) themeList() Result {
	spec := c.themeSpec("")
	var b strings.Builder
	b.WriteString("themes\n")
	// As wide as the widest name: a theme imported from an editor carries that
	// editor's name for it, which is longer than anything gluon ships under.
	width := 12
	for _, ch := range spec.Choices {
		if n := len([]rune(ch.Name)); n > width {
			width = n
		}
	}
	for _, ch := range spec.Choices {
		mark := " "
		if ch.Name == spec.Active {
			mark = "✓"
		}
		// The ground goes before the sentence rather than after it, because it is
		// the one column somebody scanning this list is scanning *for*: gluon
		// paints no background, so a light theme on a black terminal is the
		// user's mistake to avoid and gluon's job to signpost.
		fmt.Fprintf(&b, "  %s %-*s  %-6s %s\n", mark, width, ch.Name, ch.Appearance, ch.About)
	}
	b.WriteString("\n:theme <name> switches, and keeps it")
	if n := len(spec.Overrides); n > 0 {
		fmt.Fprintf(&b, "\nconfig.toml overrides %s on top of whichever theme is on",
			plural(n, "role"))
	}
	return Result{Out: b.String(), Theme: &spec}
}

// themeSet switches, and writes the choice down.
//
// It saves rather than asking, because a theme that forgets itself at the next
// prompt is a demonstration rather than a setting, and because the line it
// writes is one line in a file the user can read — the same one `[theme] name`
// was always set by hand. The failure to save is reported without undoing the
// switch: the colours you asked for are the colours you get, and the message
// says what did not stick.
func (c *Core) themeSet(name string) Result {
	if _, err := palettes.Named(name, palettes.Dir()); err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	spec := c.themeSpec(name)
	// In force from here whatever the config write does: the session is
	// repainted either way, and the message below is what says which.
	spec.Active = name

	path := config.File()
	line, err := config.SetThemeName(path, name)
	if err != nil {
		return Result{
			Out:   fmt.Sprintf("theme is %s for this session — could not save: %v", name, err),
			Err:   true,
			Theme: &spec,
		}
	}
	if c.cfg != nil {
		c.cfg.ThemeName = name
	}
	return Result{
		Out:   fmt.Sprintf("theme is %s · %s in %s", name, line, shortenPath(path, c)),
		Theme: &spec,
	}
}

// themeSpec is the catalogue as a driver needs it: every choice, the one in
// force, and the per-role overrides that apply on top of whichever is chosen.
func (c *Core) themeSpec(apply string) ThemeSpec {
	spec := ThemeSpec{Apply: apply, Active: palettes.Default}
	if c.cfg != nil {
		if c.cfg.ThemeName != "" {
			spec.Active = c.cfg.ThemeName
		}
		if len(c.cfg.Theme) > 0 {
			spec.Overrides = make(map[string]string, len(c.cfg.Theme))
			for k, v := range c.cfg.Theme {
				spec.Overrides[k] = v
			}
		}
	}
	// A theme that will not load is skipped rather than fatal — List reports
	// the first error and returns everything else, and a picker offering every
	// theme that works is more use than one refusing to open over the one that
	// does not. `gluon doctor` is where a broken theme file gets named.
	files, _ := palettes.List(palettes.Dir())
	for _, f := range files {
		spec.Choices = append(spec.Choices, ThemeChoice{
			Name: f.Name, About: f.About, Appearance: f.Appearance})
	}
	return spec
}
