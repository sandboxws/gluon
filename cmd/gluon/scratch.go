package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/sandboxws/gluon/internal/host"
	"github.com/sandboxws/gluon/internal/repl"
	"github.com/sandboxws/gluon/internal/scratch"
)

// newNewCmd is `gluon new [topic]` — scaffold a scratch and open it.
//
// One directory per scratch with its own go.mod, because gopls greys out a
// //go:build ignore file entirely: no completion, no hover, no
// jump-to-definition. -flat is there for when a single file is what is wanted,
// and it costs exactly those things.
func newNewCmd() *cobra.Command {
	var (
		flat     bool
		noEdit   bool
		run      bool
		noPrompt bool
		debug    bool
		hostDir  string
	)

	cmd := &cobra.Command{
		Use:     "new [topic]",
		Short:   "Scaffold a scratch program and open it",
		GroupID: groupScratch,
		Long: "Write a self-contained scratch module and open it in your editor.\n\n" +
			"One directory per scratch, each with its own go.mod, so gopls stays fully\n" +
			"alive in the file — completion, hover, jump-to-definition. A single\n" +
			"//go:build ignore file works too, but gopls greys it out entirely, so that\n" +
			"form is -flat and not the default.",
		Example: "# an untitled scratch\n" +
			"gluon new\n" +
			"# named, so the directory says what it is\n" +
			"gluon new heap-sort\n" +
			"# one that can import the surrounding project\n" +
			"gluon new probe -host .\n" +
			"# a single //go:build ignore file (no gopls)\n" +
			"gluon new x -flat\n" +
			"# one a debugger can open, configuration included\n" +
			"gluon new parser-bug -debug",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			topic := strings.Join(args, " ")
			// With no topic and a terminal to ask in, offer the form. Enter
			// accepts the defaults, which is exactly what `gluon new` did
			// before, so the documented one-keystroke path still works.
			if topic == "" && !noPrompt && term.IsTerminal(int(os.Stdin.Fd())) {
				t, f, err := promptForScratch(flat)
				if err != nil {
					// A cancelled form is not a failure, it is a decision.
					fmt.Fprintln(os.Stderr, "gluon new: cancelled")
					return exitErr(1)
				}
				topic, flat = t, f
			}
			// Refused here as well as in scratch.New, for the exit status: this
			// is a usage conflict like -host with -flat below it, not a
			// scaffold that failed.
			if debug && flat {
				fmt.Fprintln(os.Stderr, "gluon new: "+scratch.ErrDebugFlat.Error())
				return exitErr(2)
			}
			opts := scratch.Options{Topic: topic, Flat: flat, Debug: debug}
			if hostDir != "" {
				if flat {
					// A flat file is not a module, so there is nothing to nest.
					fmt.Fprintln(os.Stderr, "gluon new: -host needs a module directory, so it cannot be combined with -flat")
					return exitErr(2)
				}
				h, err := host.Detect(hostDir)
				if err != nil {
					fmt.Fprintf(os.Stderr, "gluon new: %v\n", err)
					return exitErr(2)
				}
				opts.Host = h
			}

			path, err := scratch.New(opts)
			if err != nil {
				fmt.Fprintf(os.Stderr, "gluon new: %v\n", err)
				return exitErr(3)
			}
			fmt.Println(path)
			if opts.Host != nil {
				fmt.Fprintf(os.Stderr, "attached to %s\n", opts.Host)
			}
			if debug {
				// stderr, so `gluon new -debug x` still puts one path and
				// nothing else on stdout, the way every other form does.
				fmt.Fprintf(os.Stderr, "debug with %s\n", scratch.DebugCommand(filepath.Dir(path)))
			}

			if !noEdit {
				if code := openEditor(path); code != 0 {
					return exitErr(code)
				}
			}
			if run {
				return status(runScratch(target(path), nil, false))
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.BoolVar(&flat, "flat", false, "write one //go:build ignore file instead of a module directory (no gopls)")
	f.BoolVar(&noEdit, "no-edit", false, "create it but do not open an editor")
	f.BoolVar(&run, "run", false, "run it after the editor exits")
	f.StringVar(&hostDir, "host", "", "nest the scratch under the module at this directory, so it can import its packages")
	f.BoolVar(&noPrompt, "no-prompt", false, "do not ask for a topic; use the default as `gluon new` always has")
	f.BoolVar(&debug, "debug", false, "write a debugger launch configuration beside the module (not with -flat)")
	return cmd
}

// promptForScratch asks the two questions `gluon new` would otherwise answer
// for you. It runs only on a terminal: a form that blocked a script would be
// the worse failure, so every non-interactive path skips it entirely.
func promptForScratch(flat bool) (string, bool, error) {
	topic := ""
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("What is this scratch for?").
				Description("Names the directory. Leave it empty for an untitled one.").
				Placeholder("heap-sort").
				Value(&topic),
			huh.NewSelect[bool]().
				Title("Shape").
				Options(
					huh.NewOption("A module directory — gopls stays alive in it", false),
					huh.NewOption("One //go:build ignore file — no completion, no hover", true),
				).
				Value(&flat),
		),
	)
	if err := form.Run(); err != nil {
		return "", false, err
	}
	return strings.TrimSpace(topic), flat, nil
}

func openEditor(path string) int {
	cmd := repl.Editor(path)
	if cmd == nil {
		// Both are unset on a fresh macOS account, so this is a real path,
		// not a theoretical one.
		fmt.Fprintln(os.Stderr, "gluon: no editor found — set $EDITOR or $VISUAL")
		return 0
	}
	cmd.Args = append(cmd.Args, path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "gluon: %v\n", err)
		return 3
	}
	return 0
}

// newRunCmd is `gluon run [path] [-- args...]`.
func newRunCmd() *cobra.Command { return newRunOrWatchCmd("run", false) }

// newWatchCmd is `gluon watch [path]`.
func newWatchCmd() *cobra.Command { return newRunOrWatchCmd("watch", true) }

func newRunOrWatchCmd(name string, watch bool) *cobra.Command {
	// fang title-cases Short, and cases.Title capitalises after a hyphen, so
	// "Re-run" would render as "Re-Run".
	verb, short := "Run", "Run a scratch"
	if watch {
		verb, short = "Re-run", "Run a scratch again on every save"
	}
	var list bool

	cmd := &cobra.Command{
		Use:     name + " [path] [-- args...]",
		Short:   short,
		GroupID: groupScratch,
		Long: verb + " a scratch program — the most recent one by default, or the directory\n" +
			"or flat file named. Arguments after -- go to the program, not to gluon.",
		Example: fmt.Sprintf("# the most recent scratch\n"+
			"gluon %s\n"+
			"# a named one, directory or flat file\n"+
			"gluon %s ~/scratch/2026-08-28-heap-sort\n"+
			"# what there is to run\n"+
			"gluon %s -list", name, name, name),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Everything after -- belongs to the scratch, not to gluon. cobra
			// hands both halves over in args and reports where the dash was.
			own, passthrough := args, []string(nil)
			if n := cmd.ArgsLenAtDash(); n >= 0 {
				own, passthrough = args[:n], args[n:]
			}
			return status(runOrWatch(cmd, name, own, passthrough, watch, list))
		},
	}
	cmd.Flags().BoolVar(&list, "list", false, "list the scratches, newest first")
	return cmd
}

func runOrWatch(cmd *cobra.Command, name string, pos, passthrough []string, watch, list bool) int {
	if list {
		all := scratch.List()
		for _, p := range all {
			fmt.Println(p)
		}
		if len(all) == 0 {
			fmt.Fprintf(os.Stderr, "no scratches under %s — try `gluon new`\n", scratch.Root())
		}
		return 0
	}

	var path string
	var err error
	switch len(pos) {
	case 0:
		if path, err = scratch.Latest(); err != nil {
			fmt.Fprintf(os.Stderr, "gluon %s: %v\n", name, err)
			return 2
		}
		fmt.Fprintf(os.Stderr, "%s\n", path)
	case 1:
		path = pos[0]
	default:
		_ = cmd.Usage()
		return 2
	}

	t, err := resolveTarget(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gluon %s: %v\n", name, err)
		return 2
	}
	if watch {
		return watchScratch(t, passthrough)
	}
	return runScratch(t, passthrough, false)
}

// scratchTarget is what `go run` is pointed at: a package directory, or a
// single file.
type scratchTarget struct {
	// Dir is the working directory for the run.
	Dir string
	// Arg is what follows `go run`: "." for a module directory, or a file
	// name for the flat form.
	Arg string
	// Watch is the path whose changes should retrigger a run.
	Watch string
}

func target(mainGo string) scratchTarget {
	dir := filepath.Dir(mainGo)
	return scratchTarget{Dir: dir, Arg: ".", Watch: dir}
}

// resolveTarget accepts a scratch directory, a main.go inside one, or a flat
// //go:build ignore file.
//
// The flat form must be run as `go run <file>`, never `go run ./dir`: the
// file-list path is the only one that sets UseAllFiles, and it is what makes an
// ignore-tagged file build at all.
func resolveTarget(path string) (scratchTarget, error) {
	p, err := expandPath(path)
	if err != nil {
		return scratchTarget{}, err
	}
	fi, err := os.Stat(p)
	if err != nil {
		return scratchTarget{}, err
	}
	if fi.IsDir() {
		if _, err := os.Stat(filepath.Join(p, "go.mod")); err != nil {
			// A scratchpad is a directory in this tree that is deliberately not
			// a module until :save has run in it. "no go.mod" is true and
			// useless there; naming what it is, and the one command that gives
			// it a program, is the answer.
			if scratch.IsPad(p) {
				return scratchTarget{}, fmt.Errorf("%s is a scratchpad with no program yet — "+
					"open it with `gluon -scratch %s` and :save writes one",
					p, filepath.Base(p))
			}
			return scratchTarget{}, fmt.Errorf("%s has no go.mod — run the file directly if it is a flat scratch", p)
		}
		return scratchTarget{Dir: p, Arg: ".", Watch: p}, nil
	}
	if filepath.Base(p) == "main.go" {
		if _, err := os.Stat(filepath.Join(filepath.Dir(p), "go.mod")); err == nil {
			return target(p), nil
		}
	}
	return scratchTarget{Dir: filepath.Dir(p), Arg: filepath.Base(p), Watch: p}, nil
}

func expandPath(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, strings.TrimPrefix(p, "~")), nil
	}
	return p, nil
}

// runScratch builds one scratch to a temp path and runs it, streaming output.
//
// Build-then-exec rather than `go run`, for the exit code: `go run` reports a
// non-zero exit as "exit status N" on stderr and exits 1 itself, so a scratch
// that means something by its status cannot say so through gluon. Running the
// binary directly makes gluon's exit code the program's, which is what a
// justfile or a shell conditional needs.
//
// -o into a temp path is mandatory, and it is the reason this is safe to do at
// all: a bare `go build` once dropped a tracked 3.1 MB Mach-O into a
// repository's history. Nothing is ever written into the scratch directory.
func runScratch(t scratchTarget, args []string, quiet bool) int {
	bin, err := os.CreateTemp("", "gluon-scratch-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "gluon run: %v\n", err)
		return 3
	}
	bin.Close()
	defer os.Remove(bin.Name())

	// Replaced, not inherited: a monorepo's -mod=vendor in GOFLAGS would
	// otherwise apply to a scratch that has nothing to do with it.
	env := append(os.Environ(), "GOFLAGS=", "GOWORK=off", "GOTOOLCHAIN=local")

	start := time.Now()
	build := exec.Command("go", "build", "-o", bin.Name(), t.Arg)
	build.Dir = t.Dir
	build.Env = env
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		if !quiet {
			fmt.Fprintf(os.Stderr, "─ did not build (%v)\n", time.Since(start).Round(time.Millisecond))
		}
		var ee *exec.ExitError
		if asExit(err, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "gluon run: %v\n", err)
		return 3
	}

	cmd := exec.Command(bin.Name(), args...)
	cmd.Dir = t.Dir
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err = cmd.Run()
	if !quiet {
		fmt.Fprintf(os.Stderr, "─ %v\n", time.Since(start).Round(time.Millisecond))
	}
	if err != nil {
		var ee *exec.ExitError
		if asExit(err, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "gluon run: %v\n", err)
		return 3
	}
	return 0
}

// watchScratch re-runs on every change. It polls rather than subscribing to
// file events: the watched set is one directory, and a stat loop over it costs
// far less than the `go run` it triggers.
func watchScratch(t scratchTarget, args []string) int {
	fmt.Fprintf(os.Stderr, "watching %s — ctrl-c to stop\n\n", t.Watch)
	last := ""
	for {
		if now := fingerprint(t.Watch); now != last {
			last = now
			fmt.Fprintf(os.Stderr, "── %s ──\n", time.Now().Format("15:04:05"))
			runScratch(t, args, false)
			fmt.Fprintln(os.Stderr)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// fingerprint is the directory's files with their sizes and mtimes, which
// changes on any edit, add or delete. Dot-directories below the root are
// skipped, so a .git or an editor's state directory is not a change to the
// program.
func fingerprint(dir string) string {
	var b strings.Builder
	_ = filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if fi.IsDir() {
			if fi.Name() != filepath.Base(dir) && strings.HasPrefix(fi.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		fmt.Fprintf(&b, "%s %d %d\n", p, fi.Size(), fi.ModTime().UnixNano())
		return nil
	})
	return b.String()
}
