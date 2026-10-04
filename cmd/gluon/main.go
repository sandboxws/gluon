// Command gluon is a REPL and scratch runner for Go, backed by the real
// toolchain rather than an interpreter.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"

	"github.com/sandboxws/gluon/internal/pretty"
	"github.com/sandboxws/gluon/internal/repl"
	"github.com/sandboxws/gluon/internal/session"
)

// version is stamped by release builds via -ldflags "-X main.version=...".
// When it is empty — `go install ...@latest`, or a plain build in a clone —
// resolveVersion falls back to the module version Go itself recorded, so all
// three install paths report something truthful instead of a hardcoded lie.
var version = ""

func resolveVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return strings.TrimPrefix(bi.Main.Version, "v")
	}
	return "dev"
}

// Command groups, so `gluon --help` reads as jobs rather than one list.
const (
	groupScratch  = "scratch"
	groupDiagnose = "diagnose"
)

// exitErr carries a command's exit status out through cobra without asking it
// to also carry a message. Every command in gluon already reports its own
// failure in its own words — "gluon new: -host needs a module directory ..." —
// and fang would otherwise print a second, generic line underneath.
type exitErr int

func (e exitErr) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// status wraps a legacy int-returning command body for RunE.
func status(code int) error {
	if code == 0 {
		return nil
	}
	return exitErr(code)
}

func main() {
	root := newRootCmd()
	root.SetArgs(normalizeArgs(root, os.Args[1:]))
	err := fang.Execute(
		context.Background(),
		root,
		fang.WithVersion(resolveVersion()),
		fang.WithErrorHandler(errorHandler),
	)
	if err == nil {
		return
	}
	var code exitErr
	if errors.As(err, &code) {
		os.Exit(int(code))
	}
	// Anything else is a usage error cobra raised and fang has now printed.
	os.Exit(2)
}

// errorHandler keeps fang's styling for usage errors while staying silent for
// a failure the command itself has already described.
func errorHandler(w io.Writer, styles fang.Styles, err error) {
	var code exitErr
	if errors.As(err, &code) {
		return
	}
	fang.DefaultErrorHandler(w, styles, err)
}

func newRootCmd() *cobra.Command {
	var (
		evalLines []string
		hostDir   string
		asJSON    bool
		padName   string
		noPad     bool
	)

	root := &cobra.Command{
		Use:   "gluon",
		Short: "A REPL and scratch runner for Go",
		Long: "gluon is a REPL and scratch runner for Go, for when you want to check what\n" +
			"s[i] prints without writing a main package first.\n\n" +
			"It compiles with your real Go toolchain, so the semantics are the compiler's:\n" +
			"every line re-renders the session into a genuine Go program and builds it.",
		Example: "# start the REPL\n" +
			"gluon\n" +
			"# evaluate and print, the ruby -e shape\n" +
			"gluon -e 'strings.ToUpper(\"hi\")'\n" +
			"# the same engine, no banner\n" +
			"echo 'x := 1' | gluon\n" +
			"# attach to the module you are standing in\n" +
			"gluon -host .",
		SilenceUsage:  true,
		SilenceErrors: true,
		// A bare positional is neither a subcommand nor something the REPL can
		// take, so it is a typo worth naming rather than silently ignoring.
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath())
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if len(evalLines) > 0 {
				return status(runOneShot(evalLines, hostDir, asJSON))
			}
			if err := repl.Run(repl.Start{
				Version: resolveVersion(),
				HostDir: hostDir,
				Pad:     padName,
				NoPad:   noPad,
			}); err != nil {
				fmt.Fprintf(os.Stderr, "gluon: %v\n", err)
				return exitErr(3)
			}
			return nil
		},
	}

	f := root.Flags()
	f.StringArrayVarP(&evalLines, "eval", "e", nil,
		"evaluate a line of Go and print the result (repeatable)")
	f.StringVar(&hostDir, "host", "",
		"attach the session to the Go module at this directory (use . for the working directory)")
	f.BoolVar(&asJSON, "json", false,
		"with -e, print one JSON object instead of the plain rendering")
	// Both are long flags, so longFlagNames collects them and the single-dash
	// spelling works without normalizeArgs being told about either —
	// invariant 20.
	f.StringVar(&padName, "scratch", "",
		"open this scratchpad instead of the configured one (interactive sessions only)")
	f.BoolVar(&noPad, "no-scratch", false,
		"do not open a scratchpad, so nothing about this session is written down")

	// `gluon -version` predates this migration, and scripts spell it that way.
	root.SetVersionTemplate("gluon {{.Version}}\n")

	root.AddGroup(
		&cobra.Group{ID: groupScratch, Title: "Scratch"},
		&cobra.Group{ID: groupDiagnose, Title: "Diagnose"},
	)
	root.AddCommand(
		newNewCmd(),
		newRunCmd(),
		newScratchCmd(),
		newWatchCmd(),
		newDoctorCmd(),
		newInitCmd(),
		newMCPCmd(),
		newThemeCmd(),
	)
	return root
}

// runOneShot is the `ruby -e` shape: evaluate, print, exit. Each -e argument
// may be Go code or a meta command — `gluon -e ':t http.Handler'` answers the
// way the REPL would, where it used to be rejected while the same line piped
// to stdin worked. The code between meta commands lands as one batch, one
// build, and every printed expression prints.
func runOneShot(lines []string, hostDir string, asJSON bool) int {
	core, err := repl.NewCore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gluon: %v\n", err)
		return 2
	}
	defer core.Close()

	if hostDir != "" {
		msg, aerr := core.Attach(hostDir)
		if aerr != nil {
			fmt.Fprintf(os.Stderr, "gluon: %v\n", aerr)
			return 3
		}
		// stderr, like the piped driver: stdout is for what evaluation prints.
		fmt.Fprintln(os.Stderr, msg)
	}

	// Split the -e arguments into complete constructs, accumulating
	// multi-line ones the way every other driver does.
	type item struct {
		src  string
		meta bool
	}
	var items []item
	var pending []string
	for _, raw := range lines {
		for _, line := range strings.Split(raw, "\n") {
			pending = append(pending, line)
			buf := strings.Join(pending, "\n")
			if strings.TrimSpace(buf) == "" {
				pending = nil
				continue
			}
			if session.IsIncomplete(buf) {
				continue
			}
			pending = nil
			src := strings.TrimSpace(buf)
			items = append(items, item{src: src, meta: strings.HasPrefix(src, ":")})
		}
	}
	if left := strings.TrimSpace(strings.Join(pending, "\n")); left != "" {
		// An unclosed construct can never complete here; let Classify say why.
		items = append(items, item{src: left})
	}

	runBatch := func(srcs []string) int {
		// A construct that does not classify is reported the way it always
		// was, before anything is evaluated.
		for _, src := range srcs {
			if _, cerr := session.Classify(src); cerr != nil {
				if asJSON {
					return emit(evalJSON{Error: cerr.Error(), ExitCode: 2}, false)
				}
				fmt.Fprintf(os.Stderr, "gluon: %v\n", cerr)
				return 2
			}
		}
		shot, err := core.EvalBatch(srcs)
		if err != nil {
			if asJSON {
				return emit(evalJSON{Error: err.Error(), ExitCode: 1}, false)
			}
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 1
		}
		if asJSON {
			out := evalJSON{OK: shot.ExitCode == 0, Stdout: shot.UserOut, ExitCode: shot.ExitCode}
			for _, g := range shot.Groups {
				for _, v := range g {
					out.Values = append(out.Values, newValueJSON(v))
				}
			}
			return emit(out, out.OK)
		}
		fmt.Print(shot.UserOut)
		for _, g := range shot.Groups {
			fmt.Println(pretty.Plain(g))
		}
		return shot.ExitCode
	}

	// runMeta returns the exit code and whether to stop regardless of it.
	runMeta := func(src string) (int, bool) {
		res := core.Submit(src)
		if res.Quit {
			return 0, true
		}
		if out := repl.EditRefusal(res); out != "" {
			fmt.Fprintln(os.Stderr, out)
			return 1, true
		}
		if asJSON {
			// :query -json is the one meta command with its own envelope. It
			// is picked off the result rather than off the source text, so the
			// flag's spelling and the flag's effect cannot drift apart.
			if res.Query != nil {
				return emit(queryJSON(repl.NewQueryJSON(res.Query)), res.Query.Err == ""), false
			}
			mj := metaJSON{OK: !res.Err, Command: strings.Fields(src)[0]}
			if res.Err {
				mj.Error = strings.TrimPrefix(res.Out, "error: ")
			} else {
				mj.Text = res.Out
			}
			return emit(mj, mj.OK), false
		}
		if res.Out != "" {
			w := os.Stdout
			if res.Err {
				w = os.Stderr
			}
			fmt.Fprintln(w, res.Out)
		}
		if res.Err {
			return 1, false
		}
		return 0, false
	}

	var batch []string
	for _, it := range items {
		if !it.meta {
			batch = append(batch, it.src)
			continue
		}
		if len(batch) > 0 {
			if code := runBatch(batch); code != 0 {
				return code
			}
			batch = nil
		}
		code, stop := runMeta(it.src)
		if stop || code != 0 {
			return code
		}
	}
	if len(batch) > 0 {
		return runBatch(batch)
	}
	return 0
}

// asExit is errors.As for an exit status, so a scratch's own exit code is
// gluon's rather than being flattened to a generic failure.
func asExit(err error, target **exec.ExitError) bool { return errors.As(err, target) }
