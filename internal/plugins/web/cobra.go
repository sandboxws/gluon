package web

import (
	"fmt"
	"strings"

	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/plugin"
)

// Cobra is the plugin for github.com/spf13/cobra.
//
// A *cobra.Command is a tree, and the printed form is one node's forty fields.
// :tree is the shape the thing actually has.
type Cobra struct{}

func (Cobra) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "cobra",
		Module:  "github.com/spf13/cobra",
		Summary: ":tree draws a command tree, which prints as forty struct fields",
	}
}

func (Cobra) Imports() []plugin.Import {
	return []plugin.Import{{Name: "cobra", Path: "github.com/spf13/cobra"}}
}

func (Cobra) Aliases() []plugin.Alias {
	return []plugin.Alias{{Name: "cobra", Module: "github.com/spf13/cobra"}}
}

func (Cobra) Commands() []plugin.Command {
	return []plugin.Command{{
		Name: ":tree",
		Arg:  "<command>",
		Usage: cmdspec.Spec{
			Kind: cmdspec.GoExpr,
			Examples: []cmdspec.Example{
				{Line: ":tree rootCmd", Says: "every subcommand beneath it, with its flags"},
			},
			See: []string{":routes"},
		},
		Text:    true,
		Summary: "a cobra command and everything under it",
		Detail: "Walks the subcommand tree depth-first, with each command's short\n" +
			"description. Hidden commands are marked rather than omitted — a tree that\n" +
			"quietly left them out would be the wrong answer to \"what can this run\".",
		Rewrite: func(arg string) (string, error) {
			if strings.TrimSpace(arg) == "" {
				return "", fmt.Errorf("usage: :tree <command>   e.g. :tree rootCmd")
			}
			return "func() string { var __b strings.Builder; " +
				"var __walk func(*cobra.Command, string); " +
				"__walk = func(__c *cobra.Command, __indent string) { " +
				"__mark := \"\"; if __c.Hidden { __mark = \"  (hidden)\" }; " +
				"fmt.Fprintf(&__b, \"%s%-24s %s%s\\n\", __indent, __c.Name(), __c.Short, __mark); " +
				"for _, __s := range __c.Commands() { __walk(__s, __indent+\"  \") } }; " +
				"__walk(" + arg + ", \"\"); " +
				"return strings.TrimRight(__b.String(), \"\\n\") }()", nil
		},
	}}
}
