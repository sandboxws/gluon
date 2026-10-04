package repl

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/sandboxws/gluon/internal/session"
)

// Start is what a driver is started with.
//
// A struct rather than a third positional argument because the pad is the first
// thing here that only one of the two drivers may act on, and a signature that
// made it look like an ordinary parameter would invite the piped loop to honour
// it. Nothing here is a default: NewCore installs no pad, and only runTUI opens
// one.
type Start struct {
	Version string
	// HostDir is the module to attach to, from -host.
	HostDir string
	// Pad names the scratchpad to open, from -scratch. Empty means the
	// configured one.
	Pad string
	// NoPad suppresses opening one at all, from -no-scratch.
	NoPad bool
}

// Run picks a driver: the Bubble Tea UI for a terminal, a plain loop for
// `echo ... | gluon` and `gluon < script.txt`.
func Run(start Start) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return runPipe(start.HostDir)
	}
	return runTUI(start)
}

// runPipe takes only the host directory, and that is the whole of the
// scratchpad claim for this driver: the pad fields are not ignored by a check
// it could stop making, they are not passed in at all. A piped script is
// somebody else's input, and a session that wrote it into the user's durable
// scratchpad would be a pipeline editing work it was never shown.
func runPipe(hostDir string) error {
	core, err := NewCore()
	if err != nil {
		return err
	}
	defer core.Close()

	if hostDir != "" {
		msg, err := core.Attach(hostDir)
		if err != nil {
			return err
		}
		// stderr, not stdout: a pipeline reads this stream, and a banner in it
		// would be indistinguishable from a value the session printed.
		fmt.Fprintln(os.Stderr, msg)
	}

	var pending []string
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		pending = append(pending, sc.Text())
		buf := strings.Join(pending, "\n")
		if session.IsIncomplete(buf) {
			continue
		}
		pending = nil

		res := core.Submit(strings.TrimSpace(buf))
		if res.Quit {
			return nil
		}
		if msg := noTerminal(core, res); msg != "" {
			fmt.Fprintln(os.Stderr, msg)
			continue
		}
		// :query -json answers a pipe with the envelope instead of the table.
		// stdout even when the statement failed, for the reason cmd/gluon's
		// emit gives: a caller piping into jq should not have to distinguish
		// "gluon printed an object" from "gluon printed an error somewhere
		// else". Out still holds the table, and nothing here reads it.
		if res.Query != nil {
			if err := WriteQueryJSON(os.Stdout, res.Query); err != nil {
				fmt.Fprintln(os.Stderr, "error: "+err.Error())
			}
			continue
		}
		if res.Out != "" {
			out := os.Stdout
			if res.Err {
				out = os.Stderr
			}
			fmt.Fprintln(out, res.Out)
		}
	}
	return sc.Err()
}

// noTerminal answers the results only a driver attached to a terminal can
// serve, and undoes what the command set up for one.
//
// Both are commands that hand the driver something to do rather than doing it
// themselves — Core runs on a background goroutine and must never touch the
// terminal — so the driver without a terminal is the one that has to say so.
// It returns the message to print, or "" when there is nothing to refuse.
func noTerminal(core *Core, res Result) string {
	if out := EditRefusal(res); out != "" {
		return out
	}
	if res.Watch {
		// The poll loop is already running: it starts before the driver sees
		// the result, exactly as :edit's temp file is already written. Stop it
		// rather than leaving a goroutine watching for an event nothing here
		// can deliver.
		core.StopWatch()
		return "error: :watch needs a terminal"
	}
	return ""
}

// EditRefusal is what a driver with no terminal has to say about a result that
// asked for one, and the cleanup that goes with it. It is "" when the result
// asked for no editor.
//
// One spelling because there are two such drivers — the piped loop and
// `gluon -e` — and they were saying the same sentence twice. It names the
// command that asked rather than a fixed one: more than one command opens an
// editor now, and a refusal naming the wrong one sends the reader to the wrong
// help.
//
// The temp file is removed only for the command that has no other copy of what
// is in it. :edit writes the session to a temp path that Reload would have
// cleaned up, and nothing else will now; a buffer's file is inside a directory
// the session owns and holds text the session still has, so deleting it here
// would be a driver throwing away the user's work to report that it could not
// show it to them.
func EditRefusal(res Result) string {
	if res.Edit == "" {
		return ""
	}
	what := ":edit"
	if res.EditThen == "" {
		os.Remove(res.Edit)
	} else if fields := strings.Fields(res.EditThen); len(fields) > 0 {
		what = fields[0]
	}
	return "error: " + what + " needs a terminal"
}
