//go:build integration

package repl

import "testing"

// The keystroke budget is what argument completion had to fit inside: 12–22µs
// warm for an ordinary key (ROADMAP, "Measured facts"), with the
// composite-literal scan costing 400–800ns on top of it (ROADMAP, v7). A
// keystroke inside a call has to stay in the same country.
//
// The first keystroke inside a new call is not what is measured here — that one
// type-checks, and costs about what the first `x.` after a submit costs. These
// measure the second and every one after it, which is what holding a key down
// actually does.
//
// On an Apple M1 Pro, go1.25, against a warm three-line session:
//
//	BenchmarkCompleteIdentifier-10    27.3 µs/op
//	BenchmarkCompleteArgument-10      15.9 µs/op
//	BenchmarkHintWarm-10               0.16 µs/op
//
// A keystroke inside a call costs less than one outside it, not more, and the
// reason will hold: the identifier path builds and sorts every name in scope
// together with every package that could be qualified, where this one filters a
// scope walk that has already been narrowed by assignability.
//
// The pair the requirement is actually about is Complete and Hint, because a
// keystroke inside a call makes both calls. Hint's 160ns is the second ask
// hitting the cache the first one filled: the call scan, and two map lookups.
// That is inside the composite-literal path's 400–800ns, which is the budget
// this had to fit (ROADMAP, v7).

// benchCore is a session with three names of three types already bound.
func benchCore(b *testing.B) *Core {
	b.Helper()
	c, err := NewCore()
	if err != nil {
		b.Skip("no evaluator:", err)
	}
	b.Cleanup(func() { c.Close() })
	for _, line := range []string{
		"var w io.Writer = os.Stdout",
		"count := 7",
		`label := "hi"`,
	} {
		if res := c.Submit(line); res.Err {
			b.Fatalf("setup %q: %s", line, res.Out)
		}
	}
	return c
}

// BenchmarkCompleteArgument is a keystroke inside an open call, warm: the
// callee was resolved by the keystroke before this one.
func BenchmarkCompleteArgument(b *testing.B) {
	c := benchCore(b)
	const line = "strings.Repeat(label, co"
	if got := c.Complete(line); len(got) == 0 {
		b.Fatal("the benchmark line stopped completing")
	}
	b.ResetTimer()
	for b.Loop() {
		c.Complete(line)
	}
}

// BenchmarkCompleteIdentifier is the same keystroke outside a call, which is
// the number the one above has to be compared against.
func BenchmarkCompleteIdentifier(b *testing.B) {
	c := benchCore(b)
	const line = "co"
	if got := c.Complete(line); len(got) == 0 {
		b.Fatal("the benchmark line stopped completing")
	}
	b.ResetTimer()
	for b.Loop() {
		c.Complete(line)
	}
}

// BenchmarkHintWarm is the second call a keystroke makes. It must be the cache
// hit the shared lookup was built for, not a second type check.
func BenchmarkHintWarm(b *testing.B) {
	c := benchCore(b)
	const line = "strings.Repeat(label, co"
	if got := c.Hint(line); got == "" {
		b.Fatal("the benchmark line stopped hinting")
	}
	b.ResetTimer()
	for b.Loop() {
		c.Hint(line)
	}
}
