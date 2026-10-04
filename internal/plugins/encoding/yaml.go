package encoding

import (
	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/plugin"
)

// yaml is published under two module paths: gopkg.in/yaml.v3, whose upstream
// repository is archived, and go.yaml.in/yaml/v3, the maintained line
// (ROADMAP.md records the split). They are the same library and produce the
// same document, and plugin.Meta carries one Module — so this is two plugin
// values sharing one rewrite, the shape web/routes.go already uses for the
// three routers. The maintained path is registered first in Builtin, so a
// session that somehow has both keeps its answer and :plugins names the other.
//
// Neither module is matched by a prefix, deliberately. A yaml import path
// carries its major version, so activating on go.yaml.in/yaml would fire for a
// v4 session too and then preload a v3 path that is not in its build list — a
// build error rather than a missing command.

func yamlCommand(detail string) plugin.Command {
	return plugin.Command{
		Name: ":yaml",
		Arg:  "<exp>",
		Usage: cmdspec.Spec{
			Kind: cmdspec.GoExpr,
			Examples: []cmdspec.Example{
				{Line: ":yaml cfg", Says: "the document its yaml tags produce"},
			},
			See: []string{":toml", ":json"},
		},
		Text:    true,
		Summary: "the value as YAML, with its yaml tags applied",
		Detail: detail + "\n\n" +
			"The yaml tags decide the key names, what omitempty drops and what inline\n" +
			"flattens, and none of that is visible in the printed value. The encoder\n" +
			"that applies them is the one already in your build list, running in the\n" +
			"child against the real value.",
		Rewrite: func(arg string) (string, error) {
			if blank(arg) {
				return "", needsArg(":yaml", "cfg")
			}
			return "func() string { __b, __err := yaml.Marshal(" + arg + "); " +
				"if __err != nil { return \"cannot marshal: \" + __err.Error() }; " +
				"return strings.TrimRight(string(__b), \"\\n\") }()", nil
		},
	}
}

// YAML is the plugin for go.yaml.in/yaml/v3, the maintained line.
type YAML struct{}

func (YAML) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "yaml",
		Module:  "go.yaml.in/yaml/v3",
		Summary: ":yaml shows what the yaml tags produce, through the maintained v3",
	}
}

func (YAML) Imports() []plugin.Import {
	return []plugin.Import{{Name: "yaml", Path: "go.yaml.in/yaml/v3"}}
}

func (YAML) Aliases() []plugin.Alias {
	return []plugin.Alias{{Name: "yaml", Module: "go.yaml.in/yaml/v3"}}
}

func (YAML) Commands() []plugin.Command {
	return []plugin.Command{yamlCommand(
		"Marshals through go.yaml.in/yaml/v3, the maintained line.")}
}

// YAMLv3 is the plugin for gopkg.in/yaml.v3, the archived path the same library
// is still imported under almost everywhere.
type YAMLv3 struct{}

func (YAMLv3) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "yaml.v3",
		Module:  "gopkg.in/yaml.v3",
		Summary: ":yaml for the archived gopkg.in path — the same library, the same output",
	}
}

func (YAMLv3) Imports() []plugin.Import {
	return []plugin.Import{{Name: "yaml", Path: "gopkg.in/yaml.v3"}}
}

func (YAMLv3) Aliases() []plugin.Alias {
	return []plugin.Alias{{Name: "yaml.v3", Module: "gopkg.in/yaml.v3"}}
}

func (YAMLv3) Commands() []plugin.Command {
	return []plugin.Command{yamlCommand(
		"Marshals through gopkg.in/yaml.v3, whose repository is archived; the\n" +
			"maintained line is go.yaml.in/yaml/v3 and the output is the same.")}
}
