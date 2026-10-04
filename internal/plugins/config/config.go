// Package config holds the plugins for configuration libraries — the ones
// whose central object is the answer to "what did this actually resolve to".
//
// :conf reads one file. This asks the library, which is a different and often
// surprising answer: a value can arrive from a file, an environment variable, a
// flag or a default, and only the library knows which of them won. gluon links
// neither library, so the question is asked in the session's own child process
// against the instance the user already has.
package config

import (
	"fmt"
	"strings"

	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/plugin"
)

// Both libraries expose every resolved key in dotted form, which is the shape
// :conf already prints, so the two commands read the same way. Both plugins
// want the name :config; normally only one is in a build list, and if both are,
// the registry order in internal/plugins is the tie-break and :plugins names
// the loser.

func configCommand(arg, detail string, rewrite func(string) (string, error)) plugin.Command {
	return plugin.Command{
		Name: ":config",
		Arg:  arg,
		Usage: cmdspec.Spec{
			Kind: cmdspec.GoExpr,
			Examples: []cmdspec.Example{
				{Line: ":config " + strings.Trim(arg, "<>"), Says: "every key and its value, secrets masked"},
			},
			See: []string{":conf", ":env"},
		},
		Text:    true,
		Redact:  true,
		Summary: "the settings this config library resolved",
		Detail:  detail,
		Rewrite: rewrite,
	}
}

func needsArg(example string) error {
	return fmt.Errorf("usage: :config <instance>   e.g. :config %s", example)
}

// Viper is the plugin for github.com/spf13/viper.
type Viper struct{}

func (Viper) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "viper",
		Module:  "github.com/spf13/viper",
		Summary: ":config lists every key viper resolved, from wherever it resolved it",
	}
}

func (Viper) Imports() []plugin.Import {
	return []plugin.Import{{Name: "viper", Path: "github.com/spf13/viper"}}
}

func (Viper) Aliases() []plugin.Alias {
	return []plugin.Alias{{Name: "viper", Module: "github.com/spf13/viper"}}
}

func (Viper) Commands() []plugin.Command {
	return []plugin.Command{configCommand("<v>",
		"AllKeys is every key viper knows about — from the config file, from Set,\n"+
			"from a bound flag, from an environment variable and from a default — and\n"+
			"Get answers with whichever of those won. That precedence is the thing a\n"+
			"config file cannot tell you, and the reason this is not :conf.\n\n"+
			"Values are redacted by shape on the way to the screen, by gluon and not\n"+
			"by the generated program: the same test :env and :conf use.",
		func(arg string) (string, error) {
			if strings.TrimSpace(arg) == "" {
				return "", needsArg("v")
			}
			return "func() string { __keys := " + arg + ".AllKeys(); " +
				"sort.Strings(__keys); var __out []string; " +
				"for _, __k := range __keys { " +
				"__out = append(__out, fmt.Sprintf(\"%s = %v\", __k, " + arg + ".Get(__k))) }; " +
				"if len(__out) == 0 { return \"no settings resolved\" }; " +
				"return strings.Join(__out, \"\\n\") }()", nil
		},
	)}
}

// Koanf is the plugin for github.com/knadh/koanf.
type Koanf struct{}

func (Koanf) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "koanf",
		Module:  "github.com/knadh/koanf",
		Summary: ":config lists every key koanf resolved, in the dotted form it stores",
	}
}

func (Koanf) Imports() []plugin.Import {
	return []plugin.Import{{Name: "koanf", Path: "github.com/knadh/koanf/v2"}}
}

func (Koanf) Aliases() []plugin.Alias {
	return []plugin.Alias{{Name: "koanf", Module: "github.com/knadh/koanf/v2"}}
}

func (Koanf) Commands() []plugin.Command {
	return []plugin.Command{configCommand("<k>",
		"All() returns koanf's whole flattened map, already in the dotted form it\n"+
			"stores keys in — so what you see is the merged result of every provider\n"+
			"loaded into the instance, in the order they were loaded.\n\n"+
			"Values are redacted by shape on the way to the screen, by gluon and not\n"+
			"by the generated program: the same test :env and :conf use.",
		func(arg string) (string, error) {
			if strings.TrimSpace(arg) == "" {
				return "", needsArg("k")
			}
			return "func() string { __all := " + arg + ".All(); " +
				"__keys := make([]string, 0, len(__all)); " +
				"for __k := range __all { __keys = append(__keys, __k) }; " +
				"sort.Strings(__keys); var __out []string; " +
				"for _, __k := range __keys { " +
				"__out = append(__out, fmt.Sprintf(\"%s = %v\", __k, __all[__k])) }; " +
				"if len(__out) == 0 { return \"no settings resolved\" }; " +
				"return strings.Join(__out, \"\\n\") }()", nil
		},
	)}
}
