package encoding

import (
	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/plugin"
)

// Protobuf is the plugin for google.golang.org/protobuf.
//
// The command is :pb rather than :protobuf because :proto belongs to loading
// .proto files, which is a different mechanism entirely — this one only ever
// looks at a message value that already exists. Meta().Name stays protobuf, so
// :plugins and :get read as the library is named.
//
// The answer is the wire size and the text form, not a hex dump: a protobuf
// message's field numbers mean nothing without its descriptor, and prototext
// is the encoding that has one. The size is printed alongside because "how big
// is this on the wire" is the question the format exists to answer.
type Protobuf struct{}

func (Protobuf) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "protobuf",
		Module:  "google.golang.org/protobuf",
		Summary: ":pb shows what a message serialises to, in bytes and in text",
	}
}

func (Protobuf) Imports() []plugin.Import {
	return []plugin.Import{
		{Name: "proto", Path: "google.golang.org/protobuf/proto"},
		{Name: "prototext", Path: "google.golang.org/protobuf/encoding/prototext"},
	}
}

func (Protobuf) Aliases() []plugin.Alias {
	return []plugin.Alias{{Name: "protobuf", Module: "google.golang.org/protobuf"}}
}

func (Protobuf) Commands() []plugin.Command {
	return []plugin.Command{{
		Name: ":pb",
		Arg:  "<exp>",
		Usage: cmdspec.Spec{
			Kind: cmdspec.GoExpr,
			Examples: []cmdspec.Example{
				{Line: ":pb msg", Says: "its size on the wire, and its text form"},
			},
			See: []string{":msgpack", ":grpc"},
		},
		Text:    true,
		Summary: "the wire size of a proto message, and its text form",
		Detail: "A generated message prints as its Go struct — state, sizeCache and\n" +
			"unknownFields included — which is the one view that says nothing about the\n" +
			"message. :pb marshals it instead: the byte count is what the wire costs,\n" +
			"and the text form is the descriptor's own view of the same value.\n\n" +
			"It takes a proto.Message, which for generated code means the pointer.\n" +
			"Loading a .proto file is a different thing and not this command.",
		Rewrite: func(arg string) (string, error) {
			if blank(arg) {
				return "", needsArg(":pb", "msg")
			}
			return "func() string { __m, __ok := interface{}(" + arg + ").(proto.Message); " +
				"if !__ok { return \"not a proto.Message — :pb takes a generated message, " +
				"which for generated code means the pointer\" }; " +
				"__b, __err := proto.Marshal(__m); " +
				"if __err != nil { return \"cannot marshal: \" + __err.Error() }; " +
				"__t, __terr := prototext.MarshalOptions{Multiline: true, Indent: \"  \"}.Marshal(__m); " +
				"if __terr != nil { return \"cannot render the text form: \" + __terr.Error() }; " +
				"return fmt.Sprintf(\"%d bytes on the wire\\n\\n\", len(__b)) + " +
				"strings.TrimRight(string(__t), \"\\n\") }()", nil
		},
	}}
}
