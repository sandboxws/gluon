package stdlib

import (
	"fmt"
	"strings"

	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/plugin"
)

// XML is the plugin for encoding/xml.
//
// It contributes no renderer, for the reason JSON's is a renderer only for
// json.RawMessage: a renderer sees the structure the child described, and an
// XML document is not in the structure. The element names, the attributes and
// the nesting all live in struct tags the value printer never reads, and the
// encoder that applies them runs in the child, where the value is still real.
type XML struct{}

func (XML) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "xml",
		Summary: ":xml to marshal anything the way encoding/xml writes it",
	}
}

func (XML) Imports() []plugin.Import {
	return []plugin.Import{{Name: "xml", Path: "encoding/xml"}}
}

func (XML) Commands() []plugin.Command {
	return []plugin.Command{{
		Name: ":xml",
		Arg:  "<exp>",
		Usage: cmdspec.Spec{
			Kind: cmdspec.GoExpr,
			Examples: []cmdspec.Example{
				{Line: ":xml order", Says: "elements, attributes and nesting, as the tags say"},
			},
			See: []string{":json"},
		},
		Text:    true,
		Summary: "the value as indented XML, with the tags applied",
		Detail: "The question :json answers, for the encoding whose tags do more: an xml tag\n" +
			"names the element, moves a field into an attribute with `,attr`, and nests\n" +
			"with `a>b>c`. None of that is visible in the printed value.\n\n" +
			"encoding/xml refuses a map — it has no element name for the keys — and the\n" +
			"error is returned as the answer rather than dropped, because finding out is\n" +
			"often why the command was run.",
		Rewrite: func(arg string) (string, error) {
			if strings.TrimSpace(arg) == "" {
				return "", fmt.Errorf("usage: :xml <expression>   e.g. :xml User{Name: \"Ada\"}")
			}
			return "func() string { __b, __err := xml.MarshalIndent(" + arg + ", \"\", \"  \"); " +
				"if __err != nil { return \"cannot marshal: \" + __err.Error() }; return string(__b) }()", nil
		},
	}}
}
