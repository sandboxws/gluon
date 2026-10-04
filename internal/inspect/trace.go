package inspect

import (
	"strings"

	"github.com/sandboxws/gluon/internal/pretty"
)

// TraceFrame is one captured call site, already mapped into the session's own
// terms by the caller — repl knows the entries, this knows the layout.
type TraceFrame struct {
	// Fn is the function's name as the runtime spells it.
	Fn string
	// Where is "entry 3" for a frame in the session's own code, and file:line
	// for one anywhere else.
	Where string
	// Src is the session line the frame sits on, when there is one.
	Src string
}

// RenderTrace lays the chain out innermost first, the way a Go traceback reads.
//
// The traced expression heads it because it is the innermost frame the user
// cares about and the one frame that is always there — the wrapper it is
// evaluated inside is gluon's, and has already been dropped.
func RenderTrace(expr string, frames []TraceFrame, st pretty.Styles, rich bool) string {
	rows := make([][2]string, 0, len(frames)+1)
	rows = append(rows, [2]string{expr, "the expression you traced"})
	for _, f := range frames {
		where := f.Where
		if f.Src != "" {
			where += "   " + f.Src
		}
		rows = append(rows, [2]string{f.Fn, where})
	}

	width := 0
	for _, r := range rows {
		if n := len(r[0]); n > width {
			width = n
		}
	}

	var b strings.Builder
	for _, r := range rows {
		pad := strings.Repeat(" ", width-len(r[0])+2)
		if rich {
			b.WriteString("  " + st.Type.Render(r[0]) + pad + st.Note.Render(r[1]) + "\n")
			continue
		}
		b.WriteString("  " + r[0] + pad + r[1] + "\n")
	}
	if len(frames) == 0 {
		b.WriteString("\n" + noCallers + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// noCallers is the answer when the chain is one line long, which at the prompt
// it usually is. It is worth saying rather than showing an empty list: the
// reason is gluon's architecture, and the way past it is a real debugger.
const noCallers = "  nothing of yours called it — gluon compiles each line into a program whose\n" +
	"  main runs it directly, so an expression evaluated at the prompt has no\n" +
	"  caller above it. For what a call does underneath, :save -debug writes the\n" +
	"  module and names the command that steps through it."
