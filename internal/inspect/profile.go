package inspect

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/sandboxws/gluon/internal/pretty"
)

// A ProfileRow is one contributor to a profile, already mapped into the
// session's own terms by the caller — repl knows the entries, this knows the
// layout. It is the same split RenderTrace sits on the far side of.
type ProfileRow struct {
	// Flat and Pct are what pprof reported, kept as the strings it formatted.
	// It has already chosen the unit that reads best for the magnitude —
	// "30ms", "1.43MB" — and re-deriving one from a parsed number would only
	// be a second opinion about the same bytes.
	Flat string
	Pct  string
	// Fn is the function as the runtime spells it: "main.fib".
	Fn string
	// Where is "entry 3" for a position in the session's own code, and
	// file:line for one anywhere else. Empty when there was no position.
	Where string
	// Src is the session line the position sits on, when there is one.
	Src string
}

// A Profile is one report, ready to lay out.
type Profile struct {
	// Label names the profile and the measure it reports — "cpu profile", or
	// "mem profile, allocated bytes". A memory profile has four measures that
	// answer different questions, so an unlabelled number is exactly how a
	// reader concludes the wrong thing.
	Label string
	// Head is what was collected: "3 samples", "21MB allocated". It is reported
	// unconditionally rather than only when it is low, because a threshold
	// would need a number and the number would be wrong for some workload.
	// Stating it always lets the reader judge.
	Head string
	// Caveat is the honesty line, and it goes above the table rather than
	// under it: a warning printed after a ranking is read after the ranking
	// has already been believed.
	Caveat string
	// Rows are the contributors, heaviest first.
	Rows []ProfileRow
	// Nothing is what to say when Rows is empty. An empty table is not an
	// answer; "no allocation recorded" is.
	Nothing string
}

// RenderProfile formats a profile report with colour.
//
// pprof gathers and this shapes, the same way the child describes values and
// gluon formats them. The rows arrive already filtered and already mapped, so
// nothing here has to know what a harness frame is.
func RenderProfile(p Profile, st pretty.Styles) string { return profileText(p, st, true) }

// PlainProfile is RenderProfile's twin for a pipe: the same report with no
// escape codes in it. Result.Out always carries this form when the rich one
// went to a modal, so nothing is lost by not being a terminal (invariant 19).
func PlainProfile(p Profile) string { return profileText(p, pretty.Styles{}, false) }

func profileText(p Profile, st pretty.Styles, rich bool) string {
	var b strings.Builder

	head := p.Label
	if rich {
		head = st.Type.Render(p.Label)
	}
	if p.Head != "" {
		if rich {
			head += "  " + st.Annot.Render(p.Head)
		} else {
			head += "  " + p.Head
		}
	}
	b.WriteString(head + "\n")

	if p.Caveat != "" {
		if rich {
			b.WriteString("  " + st.Note.Render(p.Caveat) + "\n")
		} else {
			b.WriteString("  " + p.Caveat + "\n")
		}
	}

	if len(p.Rows) == 0 {
		nothing := p.Nothing
		if nothing == "" {
			nothing = "nothing was recorded"
		}
		if rich {
			nothing = st.Note.Render(nothing)
		}
		return strings.TrimRight(b.String(), "\n") + "\n\n  " + nothing
	}

	// Widths are measured on the unstyled text and the styles applied after,
	// because an escape code has a length and no width.
	cells := make([][4]string, 0, len(p.Rows))
	for _, r := range p.Rows {
		where := r.Where
		if r.Src != "" {
			where = strings.TrimSpace(where + "   " + r.Src)
		}
		cells = append(cells, [4]string{r.Flat, r.Pct, r.Fn, where})
	}
	var width [4]int
	for _, c := range cells {
		for i, cell := range c {
			if n := len(cell); n > width[i] {
				width[i] = n
			}
		}
	}

	b.WriteString("\n")
	for _, c := range cells {
		// The two numbers are right-aligned so magnitudes line up, which is
		// the comparison a ranking exists to make. The last column on a line
		// is never padded: trailing spaces inside a styled span survive a trim
		// that only sees them at the end, which is how the rich and plain
		// forms drift apart by whitespace nobody can see.
		line := "  " + style(st.Num, pad(c[0], width[0], true), rich) +
			"  " + style(st.Num, pad(c[1], width[1], true), rich)
		if c[3] == "" {
			line += "  " + style(st.Type, c[2], rich)
		} else {
			line += "  " + style(st.Type, pad(c[2], width[2], false), rich) +
				"  " + style(st.Note, c[3], rich)
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func style(s lipgloss.Style, text string, rich bool) string {
	if !rich {
		return text
	}
	return s.Render(text)
}

func pad(s string, w int, right bool) string {
	fill := strings.Repeat(" ", w-len(s))
	if right {
		return fill + s
	}
	return s + fill
}
