package complete

import "testing"

// benchLine is one representative line, chosen so that both scans do their
// whole job on it: it ends inside an open call and inside an open composite
// literal at once, so neither takes an early exit the other does not. It also
// carries a parenthesis inside a string and a nested closed call, which are the
// two things the scan has to walk rather than count.
const benchLine = `fmt.Fprintln(w, strings.Repeat("a(b", n), Point{X: 1, Y: `

// BenchmarkCallAt and BenchmarkLiteralAt are a pair, and only the pair means
// anything: the requirement is that finding an open call costs no more than
// finding an open composite literal, whose 400–800ns is the budget the
// keystroke path was measured against (ROADMAP, v7: "Completing a struct
// literal is a scan, not a parse").
//
// On an Apple M1 Pro, go1.25:
//
//	BenchmarkCallAt-10       232 ns/op    0 B/op   0 allocs/op
//	BenchmarkLiteralAt-10    327 ns/op   56 B/op   4 allocs/op
//
// callAt is the cheaper of the two, and cheaper for a reason that will hold:
// literalAt builds the list of keys already set, where this scan only counts
// commas and slices the line.
func BenchmarkCallAt(b *testing.B) {
	for b.Loop() {
		if _, ok := callAt(benchLine); !ok {
			b.Fatal("the benchmark line stopped being an open call")
		}
	}
}

func BenchmarkLiteralAt(b *testing.B) {
	for b.Loop() {
		literalAt(benchLine)
	}
}
