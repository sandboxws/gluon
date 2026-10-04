package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sandboxws/gluon/internal/config"
	"github.com/sandboxws/gluon/internal/gluonrt"
	"github.com/sandboxws/gluon/internal/host"
	"github.com/sandboxws/gluon/internal/plugin"
	"github.com/sandboxws/gluon/internal/plugins"
	"github.com/sandboxws/gluon/internal/scratch"
	"github.com/sandboxws/gluon/internal/theme"
	"github.com/sandboxws/gluon/internal/ui"
)

// collectPlugins reports what would apply in a standalone session. doctor does
// not build a temp module, so it answers for the build list gluon starts with
// rather than one :get has added to — which is the honest answer to "what does
// gluon see here".
func collectPlugins(cfg *config.Config) []doctorPluginJSON {
	all := plugins.Builtin()
	loaded, _ := plugin.LoadDir(plugin.ConfigDir())
	all = append(all, loaded...)

	disabled := map[string]bool{}
	if cfg != nil {
		for _, n := range cfg.Plugins.Disable {
			disabled[n] = true
		}
	}
	set := plugin.NewSet(all)
	set.Activate(nil, disabled)

	active := map[string]bool{}
	for _, p := range set.Active() {
		active[p.Meta().Name] = true
	}
	out := make([]doctorPluginJSON, 0, len(all))
	for _, p := range all {
		m := p.Meta()
		out = append(out, doctorPluginJSON{
			Name: m.Name, Module: m.Module, Active: active[m.Name],
			Why: set.Why(m.Name), Summary: m.Summary,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// newDoctorCmd prints what gluon detects about the current directory. It exists
// because every failure mode of -host is invisible otherwise: whether a
// directory is in a module at all, which go directive that module pins, and
// which of its packages the internal/ rule leaves reachable are three separate
// questions, and answering them by watching a REPL line fail is guesswork.
func newDoctorCmd() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:     "doctor [dir]",
		Short:   "Report what gluon detects here",
		GroupID: groupDiagnose,
		Long: "Report what gluon can see from this directory: the Go toolchain it will use,\n" +
			"the config it read, the module it would attach to, and every package that\n" +
			"module leaves importable from a session.\n\n" +
			"On darwin it also times a freshly linked binary, because Gatekeeper\n" +
			"assessment is ~105ms of every line that has to run and `spctl --status`\n" +
			"cannot tell you whether this terminal is exempt.",
		Example: "# what gluon sees here\n" +
			"gluon doctor\n" +
			"# what it would see from another project\n" +
			"gluon doctor ~/src/shop\n" +
			"# is this terminal exempt from Gatekeeper assessment?\n" +
			"gluon doctor -json | jq .gatekeeper",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) > 0 {
				dir = args[0]
			}
			rep := collectDoctor(dir)
			if asJSON {
				return status(emit(rep, rep.OK))
			}
			return status(renderDoctor(rep, ui.New(rep.cfg, os.Stdout)))
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print one JSON object instead of a report")
	return cmd
}

// collectDoctor gathers every answer. It is separate from rendering so the text
// and -json forms cannot report different things.
func collectDoctor(dir string) *doctorJSON {
	rep := &doctorJSON{
		OK:          true,
		Version:     resolveVersion(),
		Go:          goEnv("GOVERSION"),
		GoToolchain: goEnv("GOTOOLCHAIN"),
		GoFlags:     goEnv("GOFLAGS"),
		MaxItems:    os.Getenv(gluonrt.EnvMaxItems),
		MaxDepth:    os.Getenv(gluonrt.EnvMaxDepth),
		Editor:      editor(),
	}

	if runtime.GOOS == "darwin" {
		rep.Gatekeeper = measureGatekeeper()
	}

	cfg, cerr := config.Load()
	if cerr != nil {
		rep.OK = false
		rep.Config = &doctorConfigJSON{Path: config.File(), Error: cerr.Error()}
	} else {
		rep.cfg = cfg
		c := &doctorConfigJSON{Path: cfg.Path, Imports: cfg.Imports, Timeout: cfg.Timeout, Editor: cfg.Editor}
		c.Hosts = map[string][]string{}
		for path, h := range cfg.Hosts {
			c.Hosts[path] = h.Imports
		}
		c.Theme = cfg.Theme
		c.ThemeName = cfg.ThemeName
		if c.ThemeName == "" {
			c.ThemeName = theme.Default
		}
		// A theme gluon cannot find does not stop it starting — it falls back
		// and says so here, which is the difference between that and a
		// misspelled role. See ui.NewWithError.
		if _, err := theme.Resolve(cfg.ThemeName, nil, theme.Dir()); err != nil {
			c.ThemeError = err.Error()
		}
		rep.Config = c
	}

	rep.Scratch = &doctorScratchJSON{Root: scratch.Root(), Pads: len(scratch.Pads())}

	rep.Plugins = collectPlugins(rep.cfg)

	h, err := host.Detect(dir)
	if err != nil {
		rep.Host = &doctorHostJSON{Error: err.Error()}
		// Not being inside a module does not end the report. A directory with a
		// compose file and a .env has a database whether or not it has a
		// go.mod, and returning here would answer "no database" to a question
		// nobody asked.
		rep.Database = collectDatabase(dir, "", nil, rep.cfg)
		return rep
	}
	hj := &doctorHostJSON{
		Module:      h.Path,
		Dir:         h.Dir,
		GoDirective: h.Go,
		SessionPath: h.SessionPath(),
	}
	rep.Host = hj

	reqs, _ := h.Requires()
	rep.Database = collectDatabase(h.Dir, h.Path, reqs, rep.cfg)

	ix, err := h.Index()
	if err != nil {
		rep.OK = false
		hj.IndexError = err.Error()
		return rep
	}
	hj.Importable = ix.Total()
	for _, p := range ix.All() {
		hj.Packages = append(hj.Packages, doctorPkgJSON{Name: p.Name, Path: p.Path})
	}
	return rep
}

// renderDoctor is the terminal form. It returns the exit code, which is
// non-zero only when something gluon depends on is actually broken.
func renderDoctor(rep *doctorJSON, th ui.Theme) int {
	section := func(s string) { fmt.Printf("\n%s\n", th.Heading.Render(s)) }
	// The padding sits outside the style: colouring trailing spaces shows up
	// the moment anyone selects a line to copy it.
	row := func(k, v string) { fmt.Printf("  %s%s  %s\n", th.Path.Render(k), pad(k), v) }

	fmt.Printf("%s %s\n", th.Heading.Render("gluon"), rep.Version)

	section("toolchain")
	row("go", rep.Go)
	row("GOTOOLCHAIN", rep.GoToolchain)
	// Reported because gluon replaces rather than inherits it: a monorepo's
	// -mod=vendor would otherwise apply silently to every evaluated line.
	row("GOFLAGS", orNone(rep.GoFlags)+th.Path.Render("  (gluon clears this)"))
	// Reported only when set, and beside GOFLAGS for the same reason it is:
	// gluon passes it to the child, so a value from the user's own shell
	// silently outranks the setting that names the same bound.
	if rep.MaxItems != "" {
		row(gluonrt.EnvMaxItems, rep.MaxItems+th.Path.Render("  (over value.items)"))
	}
	if rep.MaxDepth != "" {
		row(gluonrt.EnvMaxDepth, rep.MaxDepth+th.Path.Render("  (over value.depth)"))
	}
	row("editor", rep.Editor)
	if g := rep.Gatekeeper; g != nil {
		row("gatekeeper", renderGatekeeper(g, th))
	}

	section("config")
	c := rep.Config
	switch {
	case c.Error != "":
		row("file", c.Path+th.Fail.Render("  BROKEN: "+c.Error))
	case c.Path == "":
		row("file", th.Path.Render("none ("+config.File()+")"))
	default:
		row("file", c.Path)
		if len(c.Imports) > 0 {
			row("imports", strings.Join(c.Imports, ", "))
		}
		if c.Timeout != "" {
			row("timeout", c.Timeout)
		}
		if c.Editor != "" {
			row("editor", c.Editor)
		}
		for _, path := range sortedKeys(c.Hosts) {
			row("host "+path, strings.Join(c.Hosts[path], ", "))
		}
		if c.ThemeName != "" {
			row("theme", c.ThemeName)
		}
		if c.ThemeError != "" {
			// Reported, and deliberately not an exit code: this report exits
			// non-zero only when something gluon depends on is broken, and a
			// colour is not that. gluon fell back to its own palette and
			// started; this is how the user finds out why it looks different.
			row("theme error", th.Fail.Render(c.ThemeError))
		}
		for _, role := range sortedKeys(c.Theme) {
			row("theme "+role, c.Theme[role])
		}
	}

	if sc := rep.Scratch; sc != nil {
		section("scratchpads")
		row("root", sc.Root)
		row("pads", fmt.Sprintf("%d", sc.Pads))
	}

	if len(rep.Plugins) > 0 {
		section("plugins")
		for _, p := range rep.Plugins {
			mark, style := "·", th.Path
			if p.Active {
				mark, style = "✓", th.OK
			}
			fmt.Printf("  %s %s%s  %s\n", style.Render(mark), th.Path.Render(p.Name),
				pad(p.Name+"  "), th.Path.Render(p.Why))
		}
	}

	code := renderHost(rep, th, row, pad)

	// The database section renders whether or not there is a module: a
	// directory with a compose file and a .env has a database either way, and
	// the host section's early exits must not swallow it.
	if rep.Database != nil {
		section("database")
		renderDatabase(rep.Database, th, row)
	}
	return code
}

func renderHost(rep *doctorJSON, th ui.Theme, row func(k, v string), pad func(string) string) int {
	fmt.Printf("\n%s\n", th.Heading.Render("host"))
	h := rep.Host
	if h.Error != "" {
		row("module", th.Path.Render("none — "+h.Error))
		return 0
	}
	row("module", h.Module)
	row("dir", h.Dir)
	row("go directive", h.GoDirective+th.Path.Render("  (copied verbatim into the session)"))
	row("session path", h.SessionPath)
	if h.IndexError != "" {
		row("importable", th.Fail.Render("could not scan: "+h.IndexError))
		return 1
	}
	row("importable", fmt.Sprintf("%d package(s)", h.Importable))
	for _, p := range h.Packages {
		fmt.Printf("  %s  %-14s %s\n", pad(""), p.Name, th.Path.Render(p.Path))
	}
	return 0
}

func renderGatekeeper(g *doctorGatekeeperJSON, th ui.Theme) string {
	if g.Error != "" {
		return fmt.Sprintf("%s (could not measure: %s)", g.Status, g.Error)
	}
	ms := time.Duration(g.FreshExecMS) * time.Millisecond
	if g.Assessed {
		return fmt.Sprintf("%s — %s\n%s",
			g.Status,
			th.Fail.Render(fmt.Sprintf("a fresh binary took %v to start, so it is being assessed.", ms)),
			th.Path.Render("                Add this terminal to System Settings → Privacy & Security →\n"+
				"                Developer Tools to get that back on every line that runs."))
	}
	return fmt.Sprintf("%s — %s", g.Status,
		th.OK.Render(fmt.Sprintf("a fresh binary started in %v, so this terminal is exempt", ms)))
}

// pad is the spaces that carry a key out to the label column.
func pad(s string) string {
	if n := 13 - len([]rune(s)); n > 0 {
		return strings.Repeat(" ", n)
	}
	return ""
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// measureGatekeeper reports whether macOS is assessing freshly linked binaries,
// by timing one rather than by asking.
//
// It is worth knowing because it is ~105ms of every line that has to run — the
// single largest remaining cost in an evaluation, and not a Go cost at all.
// `spctl --status` says whether assessment is enabled globally, but not whether
// this terminal is exempt through Developer Tools, and that exemption is the
// thing that actually matters. So gluon builds a trivial program and times its
// first execution: a never-before-seen Mach-O that runs in a millisecond was
// not assessed.
func measureGatekeeper() *doctorGatekeeperJSON {
	g := &doctorGatekeeperJSON{Status: "unknown"}
	if out, err := exec.Command("spctl", "--status").Output(); err == nil {
		g.Status = strings.TrimSpace(string(out))
	}

	d, err := timeFreshExec()
	if err != nil {
		g.Error = err.Error()
		return g
	}
	g.FreshExecMS = d.Round(time.Millisecond).Milliseconds()
	g.Assessed = d > 40*time.Millisecond
	return g
}

// timeFreshExec builds a do-nothing program and times its first run. The build
// is excluded; only the exec is measured, because that is where assessment
// happens.
func timeFreshExec() (time.Duration, error) {
	dir, err := os.MkdirTemp("", "gluon-doctor-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(dir)

	for name, body := range map[string]string{
		"go.mod":  "module gluon.local/doctor\n\ngo " + goMinorOrDefault() + "\n",
		"main.go": "package main\n\nfunc main() {}\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			return 0, err
		}
	}

	bin := filepath.Join(dir, "prog")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = dir
	build.Env = append(os.Environ(), "GOFLAGS=", "GOWORK=off", "GOPROXY=off", "GOTOOLCHAIN=local")
	if out, err := build.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}

	start := time.Now()
	if err := exec.Command(bin).Run(); err != nil {
		return 0, err
	}
	return time.Since(start), nil
}

// goMinorOrDefault is the installed toolchain's major.minor, for the throwaway
// module's directive.
func goMinorOrDefault() string {
	v := goEnv("GOVERSION")
	v = strings.TrimPrefix(v, "go")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return "1.25"
	}
	return parts[0] + "." + parts[1]
}

func goEnv(name string) string {
	out, err := exec.Command("go", "env", name).Output()
	if err != nil {
		return "?"
	}
	return strings.TrimSpace(string(out))
}

func orNone(s string) string {
	if s == "" {
		return "(unset)"
	}
	return s
}

// editor mirrors what :edit resolves, since an unset $EDITOR is the reason
// :edit appears to do nothing — and on a fresh macOS account both are unset.
func editor() string {
	for _, k := range []string{"VISUAL", "EDITOR"} {
		if v := os.Getenv(k); v != "" {
			return fmt.Sprintf("%s (%s)", v, k)
		}
	}
	for _, c := range []string{"nvim", "vim", "vi"} {
		if p, err := exec.LookPath(c); err == nil {
			return fmt.Sprintf("%s (fallback)", p)
		}
	}
	return "none found"
}
