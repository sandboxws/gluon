package encoding

import (
	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/plugin"
	"github.com/sandboxws/gluon/internal/syntax"
)

// TOML is the plugin for github.com/BurntSushi/toml.
//
// The encoder is the whole answer here: TOML is a format with opinions about
// what may be a table, what may be an array of tables and what may not be
// encoded at all, so the surprise is usually not the key names but that a value
// comes out somewhere else in the document than the struct suggests. The
// library's own error is returned as the answer for the same reason.
type TOML struct{}

func (TOML) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "toml",
		Module:  "github.com/BurntSushi/toml",
		Summary: ":toml shows the document a config struct actually writes",
	}
}

func (TOML) Imports() []plugin.Import {
	return []plugin.Import{{Name: "toml", Path: "github.com/BurntSushi/toml"}}
}

func (TOML) Aliases() []plugin.Alias {
	return []plugin.Alias{{Name: "toml", Module: "github.com/BurntSushi/toml"}}
}

func (TOML) Commands() []plugin.Command {
	return []plugin.Command{{
		Name: ":toml",
		Lang: syntax.TOML,
		Arg:  "<exp>",
		Usage: cmdspec.Spec{
			Kind: cmdspec.GoExpr,
			Examples: []cmdspec.Example{
				{Line: ":toml cfg", Says: "the document its toml tags produce"},
			},
			See: []string{":yaml", ":json"},
		},
		Text:    true,
		Summary: "the value as a TOML document, with its toml tags applied",
		Detail: "TOML orders a document by its own rules — every scalar of a table comes\n" +
			"before the first sub-table — so where a key lands is a property of the\n" +
			"encoder rather than of the struct, and that is the part reading the value\n" +
			"cannot tell you.\n\n" +
			"Only a struct or a map encodes at the top level. Anything else returns the\n" +
			"library's own refusal as the answer.",
		Rewrite: func(arg string) (string, error) {
			if blank(arg) {
				return "", needsArg(":toml", "cfg")
			}
			return "func() string { var __b strings.Builder; " +
				"if __err := toml.NewEncoder(&__b).Encode(" + arg + "); __err != nil { " +
				"return \"cannot marshal: \" + __err.Error() }; " +
				"return strings.TrimRight(__b.String(), \"\\n\") }()", nil
		},
	}}
}
