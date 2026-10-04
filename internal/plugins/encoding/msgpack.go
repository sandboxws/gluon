package encoding

import (
	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/plugin"
)

// MsgPack is the plugin for github.com/vmihailenco/msgpack.
//
// The output is binary, so the answer is a hex dump rather than the bytes
// themselves: a terminal cannot show a msgpack document and a REPL must not
// write control bytes to one. The dump is the right form anyway — the type
// tags are what someone is asking about, and hex.Dump's ASCII column keeps the
// keys readable beside them.
//
// The module is matched exactly rather than by prefix, because the import path
// carries the major version: a v4 session would otherwise activate a plugin
// that preloads the v5 path and get a build error instead of a plain "not
// active".
type MsgPack struct{}

func (MsgPack) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "msgpack",
		Module:  "github.com/vmihailenco/msgpack/v5",
		Summary: ":msgpack shows the wire bytes a value encodes to, and how many",
	}
}

func (MsgPack) Imports() []plugin.Import {
	return []plugin.Import{
		{Name: "msgpack", Path: "github.com/vmihailenco/msgpack/v5"},
		{Name: "hex", Path: "encoding/hex"},
	}
}

func (MsgPack) Aliases() []plugin.Alias {
	return []plugin.Alias{{Name: "msgpack", Module: "github.com/vmihailenco/msgpack/v5"}}
}

func (MsgPack) Commands() []plugin.Command {
	return []plugin.Command{{
		Name: ":msgpack",
		Arg:  "<exp>",
		Usage: cmdspec.Spec{
			Kind: cmdspec.GoExpr,
			Examples: []cmdspec.Example{
				{Line: ":msgpack payload", Says: "the bytes it encodes to, as a hex dump, and how many"},
			},
			See: []string{":pb", ":json"},
		},
		Text:    true,
		Summary: "the msgpack bytes a value encodes to, as a hex dump",
		Detail: "The size is the first line because it is usually the question — msgpack is\n" +
			"chosen over JSON for what it costs on a wire, and a struct tag that changes\n" +
			"a key from \"identifier\" to \"id\" changes it.\n\n" +
			"The dump's right-hand column shows the keys as text, so the tags are\n" +
			"readable next to the type bytes that encode them.",
		Rewrite: func(arg string) (string, error) {
			if blank(arg) {
				return "", needsArg(":msgpack", "u")
			}
			return "func() string { __b, __err := msgpack.Marshal(" + arg + "); " +
				"if __err != nil { return \"cannot marshal: \" + __err.Error() }; " +
				"return fmt.Sprintf(\"%d bytes\\n\", len(__b)) + " +
				"strings.TrimRight(hex.Dump(__b), \"\\n\") }()", nil
		},
	}}
}
