package inspect

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"

	"github.com/sandboxws/gluon/internal/pretty"
)

// RenderMethods draws a method set. Pointer-receiver methods are listed with
// the value-receiver ones but marked, because the rule that puts them in *T's
// method set and not in T's is one of the things a Go newcomer gets wrong.
func RenderMethods(t *Target, methods []Method, satisfied, ptrSatisfied []string, st pretty.Styles) string {
	name := t.name(t.Type)

	var b strings.Builder
	b.WriteString(st.Type.Render(name))
	switch len(methods) {
	case 0:
		b.WriteString(st.Annot.Render("  no methods"))
	case 1:
		b.WriteString(st.Annot.Render("  1 method"))
	default:
		b.WriteString(st.Annot.Render(fmt.Sprintf("  %d methods", len(methods))))
	}

	if len(methods) > 0 {
		rows := make([][]string, 0, len(methods))
		anyPtr := false
		for _, m := range methods {
			recv := ""
			if m.PointerOnly {
				recv = "*"
				anyPtr = true
			}
			rows = append(rows, []string{recv, m.Name, m.Sig})
		}
		tbl := table.New().
			Border(lipgloss.RoundedBorder()).
			BorderStyle(st.Border).
			StyleFunc(func(_, col int) lipgloss.Style {
				switch col {
				case 0:
					return st.Note.Padding(0, 0, 0, 1)
				case 1:
					return st.Type.Padding(0, 1)
				default:
					return st.Annot.Padding(0, 1)
				}
			}).
			Rows(rows...)
		b.WriteString("\n" + tbl.String())
		if anyPtr {
			b.WriteString("\n" + st.Annot.Render(
				fmt.Sprintf("  * = pointer receiver: in *%s's method set, not %s's", name, name)))
		}
	}

	if len(satisfied) > 0 {
		b.WriteString("\n" + st.Annot.Render("  satisfies  ") + st.Str.Render(strings.Join(satisfied, ", ")))
	}
	if len(ptrSatisfied) > 0 {
		b.WriteString("\n" + st.Annot.Render(fmt.Sprintf("  *%s satisfies  ", name)) +
			st.Str.Render(strings.Join(ptrSatisfied, ", ")))
	}
	return b.String()
}

// PlainMethods is the pipe-friendly form, with no colour or borders.
func PlainMethods(t *Target, methods []Method, satisfied, ptrSatisfied []string) string {
	var b strings.Builder
	b.WriteString(t.name(t.Type))
	for _, m := range methods {
		recv := ""
		if m.PointerOnly {
			recv = "*"
		}
		b.WriteString("\n  " + recv + m.Name + m.Sig)
	}
	if len(satisfied) > 0 {
		b.WriteString("\n  satisfies " + strings.Join(satisfied, ", "))
	}
	if len(ptrSatisfied) > 0 {
		b.WriteString("\n  *" + t.name(t.Type) + " satisfies " + strings.Join(ptrSatisfied, ", "))
	}
	return b.String()
}
