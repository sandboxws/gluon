package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sandboxws/gluon/internal/config"
	"github.com/sandboxws/gluon/internal/theme"
	"github.com/sandboxws/gluon/internal/ui"
)

// `gluon theme` is the command for the half of theming that is not a colour:
// which palettes exist, which one is on, and how to turn one you already like
// into one gluon can read.
//
// This was written as a CLI subcommand *instead of* a REPL one, on the argument
// that choosing a palette is something you do once while setting gluon up, from
// the shell you were already in. That was wrong about how choosing actually
// goes: nobody knows which theme they want until they see their own code in it,
// which means switching, and switching from the shell means restarting the
// session you were working in. So `:theme` exists too (internal/repl/theme.go),
// and the overlap is deliberate rather than an oversight — this is the form for
// a shell, that is the form for a session.
//
// `import` stays here alone. It converts a file, which is not a thing to do at
// a prompt.
func newThemeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "theme",
		GroupID: groupDiagnose,
		Short:   "list gluon's colour themes, and import one from an editor",
		Long: "Themes live in ~/.config/gluon/themes as TOML, one colour per role.\n" +
			"gluon ships twenty-three: `go`, the default, and `go-light`, the same\n" +
			"palette for a white terminal; `terminal`, which is your own sixteen\n" +
			"ANSI colours — what gluon looked like before it had a palette of its\n" +
			"own; the five dark Gruvppuccin palettes; and fifteen converted from the\n" +
			"editor themes of the same names, six of them light.\n\n" +
			"gluon paints no background, because your terminal owns that — so a\n" +
			"theme says which ground it was drawn for and the list prints it.\n\n" +
			"Select one in ~/.config/gluon/config.toml:\n\n" +
			"    [theme]\n" +
			"    name = \"terminal\"\n\n" +
			"Individual roles still override whatever the theme said, so a theme you\n" +
			"almost like costs one line rather than a copy.",
		Example: "# what is installed, and what is on\n" +
			"gluon theme\n" +
			"# the colours a theme actually resolves to\n" +
			"gluon theme show terminal\n" +
			"# turn an editor theme into one of gluon's\n" +
			"gluon theme import ~/Downloads/Monokai.tmTheme",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          func(cmd *cobra.Command, _ []string) error { return runThemeList(cmd) },
	}
	cmd.AddCommand(newThemeShowCmd(), newThemeImportCmd())
	return cmd
}

func newThemeShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show [name]",
		Short: "the colours a theme resolves to, one role per line",
		Long: "Every role, its value, and a swatch drawn in it — which is also the way\n" +
			"to see what a hex colour becomes on a terminal that has fewer than\n" +
			"sixteen million of them.\n\n" +
			"With no name, the theme your config selects, including any per-role\n" +
			"overrides it sets.",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			return runThemeShow(cmd, name)
		},
	}
}

func newThemeImportCmd() *cobra.Command {
	var (
		name  string
		force bool
		print bool
	)
	cmd := &cobra.Command{
		Use:   "import <file>",
		Short: "convert a TextMate .tmTheme or VS Code theme into a gluon theme",
		Long: "TextMate and VS Code describe the same thing — scopes and the colours\n" +
			"they take — so both are read the same way and mapped onto gluon's roles.\n" +
			"An IntelliJ scheme names attributes instead, and colours a whole editor\n" +
			"rather than a language, so it decides three roles the other two cannot:\n" +
			"its caret becomes the prompt and its line numbers become the furniture.\n\n" +
			"The mapping is lossy in one direction and cannot be otherwise: about a\n" +
			"third of gluon's palette describes a terminal program rather than a\n" +
			"syntax highlighter, and no scope in any editor theme means \"the\n" +
			"reverse-i-search prompt\". Those keep gluon's own values, and the report\n" +
			"says which — so every guess is on screen, in a file you can edit.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runThemeImport(cmd, args[0], name, force, print)
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "the theme's name (default: the file's, slugified)")
	f.BoolVar(&force, "force", false, "overwrite a theme of the same name")
	f.BoolVar(&print, "print", false, "write to stdout instead of the themes directory")
	return cmd
}

func runThemeList(cmd *cobra.Command) error {
	cfg, _ := config.Load()
	th := ui.New(cfg, os.Stdout)
	out := cmd.OutOrStdout()

	files, err := theme.List(theme.Dir())
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), th.Fail.Render("warning: ")+err.Error())
	}
	active := theme.Default
	if cfg != nil && cfg.ThemeName != "" {
		active = cfg.ThemeName
	}

	fmt.Fprintln(out, th.Heading.Render("themes"))
	// The name column is as wide as the widest name. It was a constant 12 while
	// the only names were "go" and "terminal"; a theme imported from an editor
	// carries that editor's name, and one of the shipped ones is now twice
	// that long.
	width := 12
	for _, f := range files {
		if n := len([]rune(f.Name)); n > width {
			width = n
		}
	}
	seen := false
	for _, f := range files {
		mark, style := " ", th.Path
		if f.Name == active {
			mark, style, seen = "✓", th.OK, true
		}
		// The ground shares the second line with where the file came from,
		// because they are the two facts about a theme that the colours cannot
		// show you. gluon paints no background — the terminal owns it — so
		// "light" here is the difference between a palette and an unreadable one.
		where := "built in"
		if f.Path != "" {
			where = shorten(f.Path)
		}
		if f.Appearance != "" {
			where = f.Appearance + " · " + where
		}
		// Padded before styling. A lipgloss style makes len() lie, so %-*s on
		// a rendered string counts the escape sequences and the column walks.
		fmt.Fprintf(out, "  %s %s %s\n", style.Render(mark), style.Render(column(f.Name, width)),
			th.Annot.Render(f.About))
		fmt.Fprintf(out, "    %s\n", th.Annot.Render(where))
	}
	if !seen {
		fmt.Fprintln(cmd.ErrOrStderr(), th.Fail.Render(
			fmt.Sprintf("config selects %q, which is not installed", active)))
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, th.Annot.Render("select one in "+shorten(config.File())+":"))
	fmt.Fprintln(out, "  [theme]")
	fmt.Fprintln(out, `  name = "terminal"`)
	return nil
}

func runThemeShow(cmd *cobra.Command, name string) error {
	cfg, _ := config.Load()
	th := ui.New(cfg, os.Stdout)
	out := cmd.OutOrStdout()

	overrides := map[string]string(nil)
	if name == "" && cfg != nil {
		// No name given: show what this machine actually renders with, which
		// includes whatever roles the config overrides.
		name, overrides = cfg.ThemeName, cfg.Theme
	}
	pal, err := theme.Resolve(name, overrides, theme.Dir())
	if err != nil {
		return err
	}
	if name == "" {
		name = theme.Default
	}
	shown := ui.FromPalette(pal, th.Colour)

	fmt.Fprintln(out, th.Heading.Render(name))
	styles := shown.RoleStyles()
	for _, role := range theme.Roles() {
		// The swatch is drawn in the colour it names, which is the only honest
		// way to show what a hex value becomes on this terminal.
		fmt.Fprintf(out, "  %s %s %s\n",
			th.Path.Render(column(role, 12)), th.Annot.Render(column(pal[role], 9)),
			styles[role].Render("████ the quick brown fox"))
	}
	return nil
}

func runThemeImport(cmd *cobra.Command, path, name string, force, print bool) error {
	cfg, _ := config.Load()
	th := ui.New(cfg, os.Stdout)
	out := cmd.OutOrStdout()

	data, err := os.ReadFile(expandTilde(path))
	if err != nil {
		return err
	}
	if name == "" {
		name = slugify(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	}
	if err := theme.ValidName(name); err != nil {
		return err
	}
	f, rep, err := theme.Import(name, data)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	body := f.Encode()
	if print {
		fmt.Fprint(out, body)
		return nil
	}
	dest := filepath.Join(theme.Dir(), name+".toml")
	_, existed := os.Stat(dest)
	if existed == nil && !force {
		return fmt.Errorf("%s already exists — pass --force to overwrite it", shorten(dest))
	}
	if err := config.WriteAtomic(dest, []byte(body), existed == nil); err != nil {
		return err
	}

	fmt.Fprintln(out, "wrote "+th.Path.Render(shorten(dest)))
	fmt.Fprintln(out)
	for _, row := range rep.Rows {
		style := th.Annot
		if !row.Kept {
			style = th.OK
		}
		fmt.Fprintf(out, "  %s %s %s\n",
			th.Path.Render(column(row.Role, 12)), column(row.Value, 9), style.Render(row.From))
	}
	fmt.Fprintln(out)
	kept := len(rep.Rows) - rep.Mapped
	fmt.Fprintln(out, th.Annot.Render(fmt.Sprintf(
		"%d of %d roles came from %s; %d kept gluon's defaults.",
		rep.Mapped, len(rep.Rows), rep.Format, kept)))
	fmt.Fprintln(out)
	fmt.Fprintln(out, th.Annot.Render("use it:"))
	fmt.Fprintln(out, "  [theme]")
	fmt.Fprintf(out, "  name = %q\n", name)
	return nil
}

// pad widens a string to n columns before anything styles it.
//
// Padding after styling is the recurring mistake this codebase already records
// in internal/inspect and internal/repl/modal: %-12s counts the bytes of an
// escape sequence, so a styled column drifts by however much colour it carries.
func column(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

// slugify turns a downloaded theme's filename into a name someone can type.
func slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}

func expandTilde(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~"))
}

// shorten writes a path the way a person would say it.
func shorten(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+"/") {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}
