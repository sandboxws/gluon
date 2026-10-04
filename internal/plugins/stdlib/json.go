package stdlib

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/plugin"
	"github.com/sandboxws/gluon/internal/pretty"
	"github.com/sandboxws/gluon/internal/syntax"
)

// JSON is the plugin for encoding/json.
type JSON struct{}

func (JSON) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "json",
		Summary: ":json to marshal anything, and a readable json.RawMessage",
	}
}

func (JSON) Imports() []plugin.Import {
	return []plugin.Import{{Name: "json", Path: "encoding/json"}}
}

func (JSON) Commands() []plugin.Command {
	return []plugin.Command{{
		Name: ":json",
		Lang: syntax.JSON,
		Arg:  "<exp>",
		Usage: cmdspec.Spec{
			Kind: cmdspec.GoExpr,
			Examples: []cmdspec.Example{
				{Line: ":json user", Says: "what its json tags produce, indented"},
			},
			See: []string{":xml", ":yaml", ":toml"},
		},
		Text:    true,
		Summary: "the value as indented JSON, the way it goes over a wire",
		Detail: "Answers what a struct's tags actually produce, which the value printer\n" +
			"cannot show: it reports the Go field names, and json reports the tags.",
		Rewrite: func(arg string) (string, error) {
			if strings.TrimSpace(arg) == "" {
				return "", fmt.Errorf("usage: :json <expression>   e.g. :json map[string]int{\"a\": 1}")
			}
			// The error is returned as the value rather than dropped: a type
			// containing a channel or a func is exactly what someone runs this
			// on to find out.
			return "func() string { __b, __err := json.MarshalIndent(" + arg + ", \"\", \"  \"); " +
				"if __err != nil { return \"cannot marshal: \" + __err.Error() }; return string(__b) }()", nil
		},
	}}
}

func (JSON) Renders() []plugin.Render {
	return []plugin.Render{{
		// json.RawMessage is a []byte, so the value printer shows it as a list
		// of small integers — the least useful possible rendering of something
		// whose whole content is text.
		Type: "json.RawMessage",
		Rich: func(v pretty.Value, st pretty.Styles) (string, bool) {
			s, ok := bytesToString(v)
			if !ok {
				return "", false
			}
			out := st.Type.Render("(json.RawMessage)") + " " + st.Str.Render(s)
			if v.More > 0 {
				out += "  " + st.Annot.Render(fmt.Sprintf("… %d more bytes", v.More))
			}
			return out, true
		},
		Inline: func(v pretty.Value, st pretty.Styles) (string, bool) {
			s, ok := bytesToString(v)
			if !ok {
				return "", false
			}
			return st.Str.Render(s), true
		},
	}}
}

// bytesToString reassembles the text of a []byte the encoder sent as a list.
// Each element arrives as the byte renderer's form — "123 (0x7b) '{'" — so the
// leading decimal is what to read.
func bytesToString(v pretty.Value) (string, bool) {
	if v.Kind != "list" {
		return "", false
	}
	var b strings.Builder
	for _, it := range v.Items {
		field, _, _ := strings.Cut(it.Repr, " ")
		n, err := strconv.ParseUint(field, 10, 8)
		if err != nil {
			return "", false
		}
		b.WriteByte(byte(n))
	}
	return b.String(), true
}
