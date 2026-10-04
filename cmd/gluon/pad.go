package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/sandboxws/gluon/internal/scratch"
)

// newScratchCmd is `gluon scratch` — the scratchpads, from outside the REPL.
//
// The three verbs here are the ones that make sense with no session: what there
// is, what one holds, and getting rid of one. Opening is not among them,
// because opening a scratchpad is starting a REPL in it — `gluon -scratch
// <name>` — and a second spelling of that would be two ways to do one thing.
func newScratchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "scratch",
		Short:   "List the scratchpads, or show and remove one",
		GroupID: groupScratch,
		Long: "A scratchpad is a named session on disk: the lines you typed, what is\n" +
			"pinned, and the module and host it takes to replay them. The REPL lands in\n" +
			"one and writes every line into it.\n\n" +
			"`gluon scratch show` prints what one holds without evaluating any of it,\n" +
			"which is the way to read a scratchpad you are not sure you want to run.",
		Example: "# what there is\n" +
			"gluon scratch\n" +
			"# read one without running it\n" +
			"gluon scratch show parser-bug\n" +
			"# start a REPL in one\n" +
			"gluon -scratch parser-bug",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE:          func(cmd *cobra.Command, _ []string) error { return status(listPads()) },
	}
	cmd.AddCommand(newScratchShowCmd(), newScratchRemoveCmd())
	return cmd
}

func listPads() int {
	pads := scratch.Pads()
	if len(pads) == 0 {
		fmt.Fprintf(os.Stderr, "no scratchpads under %s — the REPL creates `default` when it starts\n", scratch.Root())
		return 0
	}
	for _, p := range pads {
		line := fmt.Sprintf("%s\t%s", p.Name, plural(p.Entries, "entry"))
		if p.Pinned > 0 {
			line += fmt.Sprintf(" (%d pinned)", p.Pinned)
		}
		if p.Host != "" {
			line += "\t" + p.Host
		}
		fmt.Println(line)
	}
	return 0
}

func newScratchShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Print what a scratchpad holds, without running it",
		Long: "Prints the entries a scratchpad holds, in the order they were typed, and\n" +
			"evaluates none of them. Reopening a scratchpad replays it — an entry that\n" +
			"wrote a file writes it again unless it is pinned — so this is the\n" +
			"read-before-you-run path.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ExactArgs(1),
		RunE:          func(cmd *cobra.Command, args []string) error { return status(showPad(args[0])) },
	}
}

// showPad prints and evaluates nothing. There is no evaluator in this process
// at all, which is what makes that true rather than intended.
func showPad(name string) int {
	p, err := scratch.ReadPad(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gluon scratch show: %v\n", err)
		return 2
	}
	if !scratch.IsPad(p.Dir) {
		fmt.Fprintf(os.Stderr, "gluon scratch show: no scratchpad named %s\n", p.Name)
		return 2
	}
	if p.Host != "" {
		fmt.Fprintf(os.Stderr, "attached to %s\n", p.Host)
	}
	for _, r := range p.Requires {
		fmt.Fprintf(os.Stderr, "requires %s\n", r)
	}
	for _, e := range p.Sess.Entries {
		if e.Pinned {
			// The marker goes to stderr so stdout stays the lines themselves,
			// which is what a pipe into a file or an editor wants.
			fmt.Fprintln(os.Stderr, "# pinned:")
		}
		fmt.Println(e.Src)
	}
	return 0
}

func newScratchRemoveCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:     "rm <name>",
		Short:   "Remove a scratchpad",
		Long:    "Shows what the scratchpad holds and asks before removing it. Without a\nterminal to ask on, -force is required.",
		Aliases: []string{"remove"},

		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ExactArgs(1),
		RunE:          func(cmd *cobra.Command, args []string) error { return status(removePad(args[0], force)) },
	}
	cmd.Flags().BoolVar(&force, "force", false, "remove it without asking")
	return cmd
}

func removePad(name string, force bool) int {
	p, err := scratch.ReadPad(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gluon scratch rm: %v\n", err)
		return 2
	}
	if !scratch.IsPad(p.Dir) {
		fmt.Fprintf(os.Stderr, "gluon scratch rm: no scratchpad named %s\n", p.Name)
		return 2
	}

	if !force {
		// newNewCmd's shape: ask on a terminal, and refuse rather than assume
		// anywhere else. A prompt that blocked a script would be the worse
		// failure, and a removal that happened because nobody could be asked
		// would be worse still.
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			fmt.Fprintf(os.Stderr, "gluon scratch rm: %s holds %s — pass -force to remove it without asking\n",
				p.Name, plural(len(p.Sess.Entries), "entry"))
			return 2
		}
		fmt.Fprintf(os.Stderr, "%s holds %s:\n", p.Name, plural(len(p.Sess.Entries), "entry"))
		for _, e := range p.Sess.Entries {
			fmt.Fprintln(os.Stderr, "  "+strings.ReplaceAll(e.Src, "\n", "\n  "))
		}
		ok := false
		form := huh.NewForm(huh.NewGroup(
			huh.NewConfirm().Title("Remove scratchpad " + p.Name + "?").
				Description("This cannot be undone.").Value(&ok),
		))
		if err := form.Run(); err != nil || !ok {
			fmt.Fprintln(os.Stderr, "gluon scratch rm: cancelled")
			return 1
		}
	}

	if err := scratch.RemovePad(p.Name); err != nil {
		fmt.Fprintf(os.Stderr, "gluon scratch rm: %v\n", err)
		return 3
	}
	fmt.Fprintf(os.Stderr, "removed %s\n", p.Dir)
	return 0
}

// plural is the one-or-many form, spelled the way internal/repl spells it so
// the REPL's listing and the command line's read the same.
func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	if word == "entry" {
		return fmt.Sprintf("%d entries", n)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
