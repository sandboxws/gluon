package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/sandboxws/gluon/internal/config"
	"github.com/sandboxws/gluon/internal/db"
	"github.com/sandboxws/gluon/internal/host"
	"github.com/sandboxws/gluon/internal/ui"
)

// newInitCmd is `gluon init`, doctor's sibling: both answer "what does gluon
// see here", and this one writes the answer down.
//
// Nothing is written without being asked. -no-prompt prints the block it would
// have written and exits; writing without a form takes -write plus -local or
// -global, said explicitly. That is the whole reconciliation with the
// permanently-rejected _gluon/ entry in ROADMAP.md — the objection there was
// writing into a product repo *unasked*, and a -no-prompt that wrote would
// reintroduce it exactly.
func newInitCmd() *cobra.Command {
	var (
		noPrompt bool
		write    bool
		global   bool
		local    bool
		force    bool
		asJSON   bool
	)

	cmd := &cobra.Command{
		Use:     "init [dir]",
		Short:   "Record where this project's database is",
		GroupID: groupDiagnose,
		Long: "Detect the database a project uses, and write down where it is so :db and\n" +
			":query can find it again.\n\n" +
			"gluon never stores a password. What it records is the name of the environment\n" +
			"variable, or the file and key, that a connection string lives in — so the\n" +
			"config it writes is safe to read, to share, and to commit.\n\n" +
			"Nothing is written without being asked: -no-prompt prints the block it would\n" +
			"have written and stops there.",
		Example: "# look, ask, and write\n" +
			"gluon init\n" +
			"# what would it write?\n" +
			"gluon init -no-prompt\n" +
			"# write it without asking, to the global config\n" +
			"gluon init -no-prompt -write -global\n" +
			"# what did it detect?\n" +
			"gluon init -json | jq .detected",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) > 0 {
				dir = args[0]
			}
			return status(runInit(dir, initOpts{
				noPrompt: noPrompt, write: write,
				global: global, local: local, force: force, asJSON: asJSON,
			}))
		},
	}
	f := cmd.Flags()
	f.BoolVar(&noPrompt, "no-prompt", false, "print what would be written and stop")
	f.BoolVar(&write, "write", false, "write without asking (needs -local or -global)")
	f.BoolVar(&global, "global", false, "write to ~/.config/gluon/config.toml")
	f.BoolVar(&local, "local", false, "write to ./gluon.toml, which travels with the project")
	f.BoolVar(&force, "force", false, "append even when an entry of this name exists")
	f.BoolVar(&asJSON, "json", false, "print one JSON object instead of a report")
	return cmd
}

type initOpts struct {
	noPrompt, write, global, local, force, asJSON bool
}

func runInit(dir string, opts initOpts) int {
	abs, err := filepath.Abs(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gluon init: %v\n", err)
		return 3
	}

	var module string
	root := abs
	h, herr := host.Detect(abs)
	if herr == nil && h != nil {
		module, root = h.Path, h.Dir
	}

	det := db.Detect(root, requiresOf(h))
	rep := buildInitJSON(module, root, det)

	if opts.asJSON {
		return emit(rep, true)
	}

	th := ui.New(orConfigOrNil(), os.Stdout)
	fmt.Print(renderDetection(module, root, det, th))

	entry, ok := entryFrom(module, det)
	if !ok {
		if opts.noPrompt || !interactive() {
			fmt.Fprintln(os.Stderr, "gluon init: nothing to record — no database was detected, "+
				"and there is no terminal to ask on")
			return 1
		}
		var perr error
		entry, perr = promptForDatabase(module, det, th)
		if perr != nil {
			fmt.Fprintln(os.Stderr, "gluon init: cancelled")
			return 1
		}
	}

	if opts.write && !opts.local && !opts.global {
		fmt.Fprintln(os.Stderr, "gluon init: -write needs a destination — "+
			"-local writes ./gluon.toml, -global writes ~/.config/gluon/config.toml.\n"+
			"            Naming it is the asking: gluon does not put a file in a "+
			"repository without being told to.")
		return 2
	}

	target, chosen := initTarget(root, opts)
	entry = forTarget(entry, target)

	// -no-prompt prints and stops. Writing without a form takes -write and a
	// named destination, because the objection to the rejected _gluon/ entry
	// was writing into a product repo *unasked*, and a -no-prompt that wrote
	// would reintroduce it exactly.
	if !opts.write && (opts.noPrompt || !interactive()) {
		fmt.Println()
		fmt.Println(th.Heading.Render("would append to " + shortenHome(target)))
		fmt.Println()
		fmt.Print(indentBlock(config.Stanza(entry)))
		fmt.Println()
		fmt.Println(th.Dim.Render("nothing was written — run `gluon init` to be asked where this " +
			"should go, or `gluon init -write -global` to append it."))
		return 0
	}

	if !opts.write {
		var err error
		entry, target, err = confirmWrite(entry, root, chosen, th)
		if err != nil {
			fmt.Fprintln(os.Stderr, "gluon init: cancelled")
			return 1
		}
	}

	block, werr := config.AppendDatabase(target, entry, opts.force)
	if werr != nil {
		fmt.Fprintf(os.Stderr, "gluon init: %v\n", werr)
		return 1
	}
	fmt.Println()
	fmt.Println(th.OK.Render("wrote " + shortenHome(target)))
	fmt.Println()
	fmt.Print(indentBlock(string(block)))

	if isLocal(target) {
		warnIfTracked(target, th)
	}
	fmt.Println()
	fmt.Println(th.Dim.Render("  gluon -host .    then :db"))
	return 0
}

// requiresOf reads the host's own build list, which is what says a driver is
// available. It is evidence, never a candidate.
func requiresOf(h *host.Host) []string {
	if h == nil {
		return nil
	}
	reqs, err := h.Requires()
	if err != nil {
		return nil
	}
	return reqs
}

// entryFrom turns an unambiguous detection into the entry to write.
//
// Ambiguity is not resolved here. Invariant 13: gluon lists the candidates and
// asks, because the wrong guess is a config pointing at the wrong database and
// nothing would ever say so.
func entryFrom(module string, det db.Detection) (config.Database, bool) {
	if det.Chosen == nil {
		return config.Database{}, false
	}
	return databaseFor(module, *det.Chosen), true
}

func databaseFor(module string, c db.Candidate) config.Database {
	d := config.Database{
		Module: module,
		Name:   "primary",
		Driver: c.DSN.Driver,
	}
	switch {
	case c.DSN.Driver == "sqlite" && c.DSN.File != "":
		d.File = c.DSN.File
	case c.Secret.Env != "":
		d.DSNEnv = c.Secret.Env
	case c.Secret.File != "" && c.Secret.Key != "":
		d.DSNFile, d.DSNKey = c.Secret.File, c.Secret.Key
	default:
		d.Host, d.Port = c.DSN.Host, c.DSN.Port
		d.User, d.DBName = c.DSN.User, c.DSN.Database
	}
	return d
}

func initTarget(root string, opts initOpts) (path string, chosen bool) {
	switch {
	case opts.local:
		return filepath.Join(root, config.ProjectFile), true
	case opts.global:
		return config.File(), true
	default:
		return config.File(), false
	}
}

func isLocal(path string) bool { return filepath.Base(path) == config.ProjectFile }

// forTarget shapes an entry for the file it will be written to. A project file
// already applies to one project, so scoping the entry by module would be
// saying the same thing twice; and it travels with the project, so a SQLite
// file inside the project is named relative to it — which is how the loader
// reads a file = line — rather than by the path it has on this machine.
func forTarget(entry config.Database, target string) config.Database {
	if !isLocal(target) {
		return entry
	}
	entry.Module = ""
	if entry.File != "" && filepath.IsAbs(entry.File) {
		if rel, err := filepath.Rel(filepath.Dir(target), entry.File); err == nil && filepath.IsLocal(rel) {
			entry.File = filepath.ToSlash(rel)
		}
	}
	return entry
}

// warnIfTracked reports a gluon.toml that git already follows.
//
// The permanently-rejected _gluon/ entry names the failure exactly: a file in a
// product repo "would eventually be caught by a stray `git add -A`". The fix
// for a named failure mode is to report it, not to hope.
func warnIfTracked(path string, th ui.Theme) {
	cmd := exec.Command("git", "ls-files", "--error-unmatch", filepath.Base(path))
	cmd.Dir = filepath.Dir(path)
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Run(); err != nil {
		return
	}
	fmt.Println()
	fmt.Println(th.Dim.Render("  " + filepath.Base(path) + " is tracked by git. It holds no secret — " +
		"only the name of one — but it will travel with the repository."))
}

func interactive() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

func indentBlock(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		b.WriteString("  " + line + "\n")
	}
	return b.String()
}

func shortenHome(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// orConfigOrNil loads config for the palette only. A broken config must not
// stop `gluon init` — recording a database is plausibly how someone is trying
// to fix things.
func orConfigOrNil() *config.Config {
	c, err := config.Load()
	if err != nil {
		return &config.Config{}
	}
	return c
}
