package main

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// normalizeArgs rewrites gluon's historical single-dash long flags into the
// double-dash spelling pflag requires.
//
// Every flag gluon has ever documented is single-dash long form: -host, -json,
// -flat, -debug, -list, -no-edit. The standard library's flag package accepts
// that; pflag does not — it reads -json as the four shorthands -j -s -o -n and
// fails with "unknown shorthand flag". Without this pass, moving to cobra would
// silently break every invocation the documentation prints.
//
// The rewrite is deliberately narrow. A token is rewritten only when it has
// exactly one leading dash and the name before any '=' is longer than one
// character and is a registered long flag somewhere in the command tree. So:
//
//   - "-e" is left alone; it is a real shorthand for --eval.
//   - "-" is left alone; a lone dash is a value (stdin, by convention).
//   - "-flat" becomes "--flat"; "-host=/tmp/x" becomes "--host=/tmp/x".
//   - everything after a "--" terminator is left alone, so the arguments
//     `gluon run ./x -- -flag` are passed through to the scratch untouched.
//
// Both spellings work afterwards, which is the point: the docs stay true and
// --json is available to anyone who expects it.
func normalizeArgs(root *cobra.Command, args []string) []string {
	long := longFlagNames(root)
	out := make([]string, 0, len(args))
	for i, a := range args {
		if a == "--" {
			out = append(out, args[i:]...)
			return out
		}
		out = append(out, normalizeArg(a, long))
	}
	return out
}

func normalizeArg(a string, long map[string]bool) string {
	if len(a) < 3 || a[0] != '-' || a[1] == '-' {
		return a
	}
	name := a[1:]
	if eq := strings.IndexByte(name, '='); eq >= 0 {
		name = name[:eq]
	}
	if !long[name] {
		return a
	}
	return "-" + a
}

// longFlagNames is every long flag registered anywhere in the tree. A name is
// collected from the whole tree rather than the resolved command because
// resolving one first would mean parsing the very flags this pass exists to
// make parseable. The cost of the wider set is only that a flag belonging to
// another subcommand reports "unknown flag: --json" instead of "unknown
// shorthand flag" — a better message either way.
func longFlagNames(cmd *cobra.Command) map[string]bool {
	names := map[string]bool{}
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		collect := func(f *pflag.Flag) {
			if len(f.Name) > 1 {
				names[f.Name] = true
			}
		}
		c.Flags().VisitAll(collect)
		c.PersistentFlags().VisitAll(collect)
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(cmd)
	// fang adds --version to the root after this runs, and cobra adds --help to
	// every command lazily. Both are documented spellings.
	names["version"] = true
	names["help"] = true
	return names
}
