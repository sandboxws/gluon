package inspect

import (
	"strings"

	"github.com/sandboxws/gluon/internal/eval"
	"github.com/sandboxws/gluon/internal/pretty"
)

// reserved reports that a name is one gluon injected rather than one the user
// typed. The compiler and the detector both name gluon's harness the way they
// name anything else, and a report that says "inlining call to __gluonPrint"
// is invariant 9's failure in a new costume. Spelt as the prefix, the way
// :profile and :trace already spell it.
func reserved(text string) bool { return strings.Contains(text, "__gluon") }

// RenderInline formats what the compiler decided about inlining.
//
// Silence is a real answer and has to be said out loud, the same way
// RenderEscape says it: a line with no calls on it produces no decisions at
// all, which looks like a broken command otherwise.
func RenderInline(msgs []eval.Diag, nothing string, st pretty.Styles, rich bool) string {
	return inlineText(msgs, nothing, st, rich)
}

// PlainInline is RenderInline's twin for a pipe: the same report with no
// escape codes in it (invariant 19).
func PlainInline(msgs []eval.Diag, nothing string) string {
	return inlineText(msgs, nothing, pretty.Styles{}, false)
}

func inlineText(msgs []eval.Diag, nothing string, st pretty.Styles, rich bool) string {
	var b strings.Builder
	shown := 0
	for _, m := range msgs {
		if reserved(m.Text) {
			continue
		}
		shown++
		line := "  " + m.Text
		if rich {
			line = "  " + st.Note.Render(m.Text)
		}
		b.WriteString(line + "\n")
		if m.Quote != "" {
			b.WriteString(m.Quote + "\n")
		}
	}
	if shown == 0 {
		return "  " + nothing
	}
	return strings.TrimRight(b.String(), "\n")
}

// RenderAsm formats one function's listing. The listing is the answer, so
// nothing here interprets it — the header is styled and the instructions are
// passed through as the compiler wrote them.
func RenderAsm(a eval.AsmResult, st pretty.Styles, rich bool) string {
	return asmText(a, st, rich)
}

// PlainAsm is RenderAsm's twin for a pipe (invariant 19).
func PlainAsm(a eval.AsmResult) string { return asmText(a, pretty.Styles{}, false) }

func asmText(a eval.AsmResult, st pretty.Styles, rich bool) string {
	if !a.Emitted {
		// A function inlined at every call site and dropped by the linker has
		// no block. That is an answer, and naming the likeliest reason for it
		// is the difference between an answer and an empty pane.
		msg := "the compiler emitted no code for " + a.Symbol +
			" — a function inlined at every call site is dropped, and has no listing of its own"
		if rich {
			return "  " + st.Annot.Render(msg)
		}
		return "  " + msg
	}
	var b strings.Builder
	for i, line := range a.Lines {
		if i == 0 && rich {
			b.WriteString(st.Type.Render(line) + "\n")
			continue
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// RenderVet formats vet's findings against the session.
func RenderVet(v eval.VetResult, st pretty.Styles, rich bool) string {
	return vetText(v, st, rich)
}

// PlainVet is RenderVet's twin for a pipe (invariant 19).
func PlainVet(v eval.VetResult) string { return vetText(v, pretty.Styles{}, false) }

func vetText(v eval.VetResult, st pretty.Styles, rich bool) string {
	var b strings.Builder
	shown := 0
	for _, f := range v.Findings {
		if reserved(f.Text) {
			continue
		}
		shown++
		head := "  " + f.Text
		if rich {
			head = "  " + st.Note.Render(f.Text)
		}
		b.WriteString(head + "\n")
		if f.Quote != "" {
			b.WriteString(f.Quote + "\n")
		}
	}
	if shown == 0 {
		// The analyser names are not reported: `go vet` in its plain form does
		// not say which analyser found what, and naming the set that ran would
		// be gluon's list rather than the toolchain's.
		const nothing = "vet found nothing in this session"
		if rich {
			return "  " + st.Annot.Render(nothing)
		}
		return "  " + nothing
	}
	return strings.TrimRight(b.String(), "\n")
}

// raceCaveat is fixed, not conditional on anything.
//
// The detector observes one interleaving of one run. A threshold — "probably
// clean after N runs" — would need a number, and the number would be wrong for
// some workload. So the caveat is printed every time nothing is found, for the
// same reason :profile prints its sample count every time.
const raceCaveat = "no race detected in this run — the detector sees one interleaving, so this is not proof there is none"

// RenderRace formats what the detector saw. value is the expression's own
// result, already formatted by the caller the way an ordinary evaluation
// formats it: a race is something that happened during the run, not instead of
// it, so the value is shown either way.
func RenderRace(r eval.RaceResult, value string, st pretty.Styles, rich bool) string {
	return raceText(r, value, st, rich)
}

// PlainRace is RenderRace's twin for a pipe (invariant 19).
func PlainRace(r eval.RaceResult, value string) string {
	return raceText(r, value, pretty.Styles{}, false)
}

func raceText(r eval.RaceResult, value string, st pretty.Styles, rich bool) string {
	var b strings.Builder
	if r.FirstBuild {
		// Said after the fact rather than before it, because a command returns
		// one result: the first :race of a machine's life pays for compiling
		// the instrumented standard library, and a user who waited deserves to
		// know what they waited for and that the next one will not.
		note := "the instrumented standard library was compiled for this run; later :race lines reuse it"
		if rich {
			b.WriteString(st.Annot.Render("  "+note) + "\n")
		} else {
			b.WriteString("  " + note + "\n")
		}
	}
	if v := strings.TrimRight(value, "\n"); v != "" {
		b.WriteString(v + "\n")
	}

	if !r.Raced {
		if rich {
			b.WriteString(st.Annot.Render("  " + raceCaveat))
		} else {
			b.WriteString("  " + raceCaveat)
		}
		return strings.TrimRight(b.String(), "\n")
	}

	for _, line := range strings.Split(strings.TrimRight(r.Report, "\n"), "\n") {
		if reserved(line) {
			continue
		}
		if rich && strings.Contains(line, "DATA RACE") {
			b.WriteString("  " + st.Note.Render(line) + "\n")
			continue
		}
		b.WriteString("  " + line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
