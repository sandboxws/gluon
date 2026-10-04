package stdlib

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"

	"github.com/sandboxws/gluon/internal/plugin"
	"github.com/sandboxws/gluon/internal/pretty"
)

// HTTP is the plugin for net/http.
//
// A *http.Response printed structurally is twenty fields deep and the three
// that matter — status, headers, length — are scattered through it. Everything
// here is structure the child already sent, so none of it costs an evaluation.
type HTTP struct{}

func (HTTP) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "http",
		Summary: "responses and headers as what they are, not as twenty struct fields",
	}
}

func (HTTP) Imports() []plugin.Import {
	return []plugin.Import{
		{Name: "http", Path: "net/http"},
		{Name: "httptest", Path: "net/http/httptest"},
		{Name: "url", Path: "net/url"},
	}
}

func (HTTP) Renders() []plugin.Render {
	return []plugin.Render{
		{Type: "http.Header", Rich: renderHeader},
		{Type: "*http.Response", Rich: renderResponse},
		{Type: "http.Response", Rich: renderResponse},
	}
}

// renderHeader draws the canonical name/value table. The default map rendering
// is close, but a header's values are a slice, and "[application/json]" with
// its brackets is not what the header says.
func renderHeader(v pretty.Value, st pretty.Styles) (string, bool) {
	if v.Kind != "map" || len(v.Keys) == 0 {
		return "", false
	}
	type hdr struct{ name, value string }
	rows := make([]hdr, 0, len(v.Keys))
	for i, k := range v.Keys {
		rows = append(rows, hdr{unquote(k.Repr), headerValue(v.Items[i])})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(st.Border).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return st.Header
			}
			if col == 0 {
				return st.Type.Padding(0, 1)
			}
			return st.Str.Padding(0, 1)
		}).
		Headers("header", "value")
	for _, r := range rows {
		t.Row(r.name, r.value)
	}
	head := st.Type.Render("(http.Header)") + " " +
		st.Annot.Render(fmt.Sprintf("%d header(s)", len(rows)))
	return head + "\n" + t.Render(), true
}

// headerValue flattens the []string a header maps to. Repeated headers stay
// visible, comma-joined the way the wire format writes them.
func headerValue(v pretty.Value) string {
	if v.Kind != "list" {
		return unquote(v.Repr)
	}
	parts := make([]string, 0, len(v.Items))
	for _, it := range v.Items {
		parts = append(parts, unquote(it.Repr))
	}
	return strings.Join(parts, ", ")
}

// renderResponse pulls the three fields that answer "what came back" out of the
// twenty a response carries.
func renderResponse(v pretty.Value, st pretty.Styles) (string, bool) {
	body := v
	prefix := ""
	if v.Kind == "ptr" && len(v.Items) == 1 {
		body, prefix = v.Items[0], "*"
	}
	if body.Kind != "struct" {
		return "", false
	}

	status := fieldRepr(body, "Status")
	code := fieldRepr(body, "StatusCode")
	if status == "" && code == "" {
		return "", false
	}

	line := st.Type.Render("("+prefix+"http.Response)") + " " + statusStyle(code, st).Render(unquote(status))
	if proto := fieldRepr(body, "Proto"); proto != "" {
		line += "  " + st.Annot.Render(unquote(proto))
	}
	if n := fieldRepr(body, "ContentLength"); n != "" && n != "-1" {
		line += "  " + st.Annot.Render(n+" bytes")
	} else if n == "-1" {
		line += "  " + st.Annot.Render("length unknown")
	}

	var out strings.Builder
	out.WriteString(line)
	if h, ok := fieldValue(body, "Header"); ok && len(h.Keys) > 0 {
		if rendered, ok := renderHeader(h, st); ok {
			// The header table already names itself; drop that line, since the
			// response line above it is the heading here.
			if _, rest, found := strings.Cut(rendered, "\n"); found {
				out.WriteString("\n" + rest)
			}
		}
	}
	return out.String(), true
}

// statusStyle colours by class, which is the one thing about a status code that
// is worth reading before the number.
func statusStyle(code string, st pretty.Styles) lipgloss.Style {
	n, err := strconv.Atoi(code)
	switch {
	case err != nil:
		return st.Num
	case n >= 200 && n < 300:
		return st.Str
	case n >= 300 && n < 400:
		return st.Note
	case n >= 400:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	}
	return st.Num
}

func fieldValue(v pretty.Value, name string) (pretty.Value, bool) {
	for _, f := range v.Fields {
		if f.Name == name {
			return f.Val, true
		}
	}
	return pretty.Value{}, false
}

func fieldRepr(v pretty.Value, name string) string {
	f, ok := fieldValue(v, name)
	if !ok {
		return ""
	}
	return f.Repr
}

// unquote strips the quotes the encoder puts around a string's Repr.
func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		if out, err := strconv.Unquote(s); err == nil {
			return out
		}
		return s[1 : len(s)-1]
	}
	return s
}
