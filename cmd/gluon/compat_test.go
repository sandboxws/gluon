package main

import (
	"strings"
	"testing"
)

// TestNormalizeArgsRewritesHistoricalFlags pins the single-dash long form that
// every invocation in the documentation uses. pflag reads -json as four shorthands, so
// without this pass the move to cobra would silently break the documented CLI.
func TestNormalizeArgsRewritesHistoricalFlags(t *testing.T) {
	root := newRootCmd()
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		// Invocations the documentation has printed.
		{"eval shorthand is left alone",
			[]string{"-e", `strings.ToUpper("hi")`},
			[]string{"-e", `strings.ToUpper("hi")`}},
		{"host",
			[]string{"-host", "~/src/shop"},
			[]string{"--host", "~/src/shop"}},
		{"new flat",
			[]string{"new", "x", "-flat"},
			[]string{"new", "x", "--flat"}},
		{"new no-edit keeps its own dash",
			[]string{"new", "-no-edit"},
			[]string{"new", "--no-edit"}},
		{"new debug",
			[]string{"new", "parser-bug", "-debug"},
			[]string{"new", "parser-bug", "--debug"}},
		{"version",
			[]string{"-version"},
			[]string{"--version"}},

		// The narrowness of the rewrite is the point.
		{"a lone dash is a value, not a flag",
			[]string{"new", "x", "-host", "-"},
			[]string{"new", "x", "--host", "-"}},
		{"an =value is preserved",
			[]string{"new", "-host=/tmp/x"},
			[]string{"new", "--host=/tmp/x"}},
		{"an unknown single-dash token is not rewritten",
			[]string{"new", "-nosuchflag"},
			[]string{"new", "-nosuchflag"}},
		{"double dash is already correct",
			[]string{"new", "--flat"},
			[]string{"new", "--flat"}},
		{"nothing after a -- terminator is touched",
			[]string{"run", "./x", "--", "-list", "-json"},
			[]string{"run", "./x", "--", "-list", "-json"}},
		{"a positional that looks like nothing is left alone",
			[]string{"new", "heap:sort"},
			[]string{"new", "heap:sort"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeArgs(root, tc.in)
			if strings.Join(got, "\x00") != strings.Join(tc.want, "\x00") {
				t.Errorf("normalizeArgs(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestDocumentedFlagsExist asserts that every flag name the docs spell is
// registered on the command they say owns it. A rename would otherwise
// only surface as a runtime "unknown flag" on a path no test walks.
func TestDocumentedFlagsExist(t *testing.T) {
	documented := map[string][]string{
		"":      {"eval", "host", "json"},
		"new":   {"flat", "no-edit", "run", "host", "debug"},
		"run":   {"list"},
		"watch": {"list"},
	}

	root := newRootCmd()
	for path, flags := range documented {
		cmd := root
		if path != "" {
			found, _, err := root.Find([]string{path})
			if err != nil {
				t.Fatalf("subcommand %q not found: %v", path, err)
			}
			cmd = found
		}
		for _, name := range flags {
			if cmd.Flags().Lookup(name) == nil {
				t.Errorf("gluon %s: flag --%s is documented but not registered", path, name)
			}
		}
	}
}

// TestSubcommandsAreGrouped keeps `gluon --help` readable: an ungrouped command
// is silently listed under "Additional Commands", away from its siblings.
func TestSubcommandsAreGrouped(t *testing.T) {
	root := newRootCmd()
	for _, c := range root.Commands() {
		if c.GroupID == "" {
			t.Errorf("subcommand %q has no GroupID", c.Name())
		}
	}
}

// TestRootRejectsAPositional keeps `gluon frobnicate` an error rather than a
// REPL that silently ignored the word.
func TestRootRejectsAPositional(t *testing.T) {
	root := newRootCmd()
	err := root.Args(root, []string{"frobnicate"})
	if err == nil {
		t.Fatal("a bare positional was accepted; it should be an unknown command")
	}
	if !strings.Contains(err.Error(), "frobnicate") {
		t.Errorf("error should name the offending word, got %q", err)
	}
}
