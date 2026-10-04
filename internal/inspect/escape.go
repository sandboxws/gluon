package inspect

import (
	"go/types"
	"strings"

	"github.com/sandboxws/gluon/internal/eval"
	"github.com/sandboxws/gluon/internal/pretty"
)

// RenderEscape formats what the compiler said about an expression.
//
// Silence is a real answer and has to be said out loud: an empty report looks
// like a broken command otherwise.
func RenderEscape(here, elsewhere []eval.EscMsg, st pretty.Styles, rich bool) string {
	var b strings.Builder
	write := func(msgs []eval.EscMsg) {
		for _, m := range msgs {
			line := "  " + m.Text
			if rich {
				line = "  " + st.Note.Render(m.Text)
			}
			b.WriteString(line + "\n")
			if m.Quote != "" {
				b.WriteString(m.Quote + "\n")
			}
		}
	}

	switch {
	case len(here) > 0:
		write(here)
	case len(elsewhere) > 0:
		// The allocation can happen inside a function the session declared, in
		// which case the compiler reports it against that declaration — an
		// older entry. Showing nothing here would be a lie by omission.
		b.WriteString("  nothing in this expression itself; from elsewhere in the session:\n")
		write(elsewhere)
	default:
		return "  no allocations — nothing here reaches the heap"
	}
	// Gated on rich like everything above it: styling this line unconditionally
	// put escape codes into output a pipe was reading.
	const context = "  (in this context — a value that outlives its frame escapes; one that does not, does not)"
	if rich {
		b.WriteString(st.Annot.Render(context))
	} else {
		b.WriteString(context)
	}
	return strings.TrimRight(b.String(), "\n")
}

// IsError reports whether a type satisfies the error interface.
func IsError(t types.Type) bool {
	errIface, ok := types.Universe.Lookup("error").Type().Underlying().(*types.Interface)
	if !ok {
		return false
	}
	return types.Implements(t, errIface) || types.Implements(types.NewPointer(t), errIface)
}

// RenderErrChain lays the unwrap chain out flat: one level per line, concrete
// type on the left and message on the right. Each level is what you could
// errors.Is or errors.As against.
func RenderErrChain(items []pretty.Value, st pretty.Styles, rich bool) string {
	type link struct{ depth, typ, msg string }
	var links []link
	width := 0
	for _, it := range items {
		parts := strings.SplitN(it.Repr, "\x1f", 3)
		if len(parts) != 3 {
			continue
		}
		// errors.Join's own message is its children joined by newlines, which
		// would break the one-line-per-level layout.
		l := link{parts[0], parts[1], strings.ReplaceAll(parts[2], "\n", " · ")}
		links = append(links, l)
		if n := len(l.typ) + 2*atoiSafe(l.depth); n > width {
			width = n
		}
	}
	if len(links) == 0 {
		return "no chain"
	}

	var b strings.Builder
	for _, l := range links {
		indent := strings.Repeat("  ", atoiSafe(l.depth))
		typ := indent + l.typ
		pad := strings.Repeat(" ", width-len(typ)+2)
		if rich {
			b.WriteString("  " + st.Type.Render(typ) + pad + st.Note.Render(l.msg) + "\n")
			continue
		}
		b.WriteString("  " + typ + pad + l.msg + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}
