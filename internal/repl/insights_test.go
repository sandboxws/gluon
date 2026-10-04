package repl

import (
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/eval"
	"github.com/sandboxws/gluon/internal/inspect"
	"github.com/sandboxws/gluon/internal/pretty"
)

func TestSplitMethodReadsBothReceiverSpellings(t *testing.T) {
	cases := []struct{ arg, recv, name string }{
		{"add", "", "add"},
		{"P.Inc", "P", "Inc"},
		{"(*P).Inc", "P", "Inc"},
		{"(P).Inc", "P", "Inc"},
		{"*P.Inc", "P", "Inc"},
		{"strings.Index", "strings", "Index"},
	}
	for _, tc := range cases {
		recv, name := splitMethod(tc.arg)
		if recv != tc.recv || name != tc.name {
			t.Errorf("splitMethod(%q) = (%q, %q), want (%q, %q)", tc.arg, recv, name, tc.recv, tc.name)
		}
	}
}

func TestIsDecisionAboutMatchesWholeNames(t *testing.T) {
	yes := []string{
		"can inline add with cost 4 as: func(int, int) int { return a + b }",
		"cannot inline add: function too complex: cost 280 exceeds budget 80",
	}
	for _, text := range yes {
		if !isDecisionAbout(text, "add") {
			t.Errorf("%q was not read as a decision about add", text)
		}
	}
	// A name that merely starts the same way is a different function, and
	// answering with its verdict would be worse than answering with none.
	no := []string{
		"can inline address with cost 4 as: func() {}",
		"inlining call to add",
		"cannot inline adder: unhandled op GO",
	}
	for _, text := range no {
		if isDecisionAbout(text, "add") {
			t.Errorf("%q was read as a decision about add", text)
		}
	}
}

// TestDeclaredFuncDerivesLinkerSymbols checks the half of :asm that decides
// which block to look for. It type-checks and does not build.
func TestDeclaredFuncDerivesLinkerSymbols(t *testing.T) {
	c := testCore(t)
	for _, decl := range []string{
		"func add(a, b int) int { return a + b }",
		"type P struct{ n int }",
		"func (p *P) Inc() { p.n++ }",
		"func (p P) Get() int { return p.n }",
	} {
		if res := c.Submit(decl); res.Err {
			t.Fatalf("%s: %s", decl, res.Out)
		}
	}

	cases := []struct{ arg, symbol string }{
		{"add", "main.add"},
		// The receiver's pointer-ness is the compiler's fact, not the user's
		// spelling, so both forms find the same block.
		{"P.Inc", "main.(*P).Inc"},
		{"(*P).Inc", "main.(*P).Inc"},
		{"P.Get", "main.P.Get"},
		{"(*P).Get", "main.P.Get"},
	}
	for _, tc := range cases {
		_, symbol, ok := c.declaredFunc(tc.arg)
		if !ok || symbol != tc.symbol {
			t.Errorf("declaredFunc(%q) = (%q, %v), want %q", tc.arg, symbol, ok, tc.symbol)
		}
	}
}

// TestAsmRefusesWhatItCannotList covers every way a name fails, because each
// one is a different answer.
func TestAsmRefusesWhatItCannotList(t *testing.T) {
	c := testCore(t)
	for _, decl := range []string{
		"func add(a, b int) int { return a + b }",
		"type P struct{ n int }",
		"func (p *P) Inc() { p.n++ }",
		"x := 42",
	} {
		if res := c.Submit(decl); res.Err {
			t.Fatalf("%s: %s", decl, res.Out)
		}
	}

	cases := []struct {
		arg  string
		want []string
	}{
		// A function from another package: the refusal states the reason,
		// because the reason is the same fact the non-goal records.
		{"strings.Index", []string{"the session's own package", "warm build cache"}},
		{"P", []string{"is a type", ":m P"}},
		{"x", []string{"is a variable"}},
		{"P.Missing", []string{"not a method of P", ":m P"}},
		{"nope", []string{"unknown: nope"}},
	}
	for _, tc := range cases {
		res := c.Submit(":asm " + tc.arg)
		if !res.Err {
			t.Errorf(":asm %s did not refuse: %q", tc.arg, res.Out)
			continue
		}
		for _, w := range tc.want {
			if !strings.Contains(res.Out, w) {
				t.Errorf(":asm %s: missing %q in %q", tc.arg, w, res.Out)
			}
		}
	}
}

func TestInsightUsageLines(t *testing.T) {
	c := testCore(t)
	// :vet is the one with an optional argument, so a bare :vet is a real
	// command rather than a usage line — the other three must say usage.
	for _, name := range []string{":inline", ":asm", ":race"} {
		res := c.Submit(name)
		if !res.Err || !strings.HasPrefix(res.Out, "usage:") {
			t.Errorf("%s with no argument: got %q (err=%v)", name, res.Out, res.Err)
		}
	}
}

// TestVetReportsCompileErrorsAsCompileErrors: an expression that does not
// type-check is an ordinary compile error against the typed line, not a vet
// failure. Vet prints type errors in the same file:line:col shape, so without
// the check first they would read as findings.
func TestVetReportsCompileErrorsAsCompileErrors(t *testing.T) {
	c := testCore(t)
	res := c.Submit(":vet nosuchname(1)")
	if !res.Err {
		t.Fatalf(":vet on a line that does not compile did not report an error: %q", res.Out)
	}
	if !strings.Contains(res.Out, "nosuchname") {
		t.Errorf("the error does not name what failed: %q", res.Out)
	}
	if strings.Contains(res.Out, "vet found nothing") {
		t.Errorf(":vet reported a clean session for a line that does not compile: %q", res.Out)
	}
}

func TestRenderInlineTwins(t *testing.T) {
	msgs := []eval.Diag{
		{Entry: 2, Text: "inlining call to add", Quote: "    add(1, 2)"},
		// gluon's own printer must never appear in a report about the user's
		// code (invariant 9).
		{Entry: 2, Text: "inlining call to __gluonPrint"},
	}
	plain := inspect.PlainInline(msgs, "nothing here")
	if strings.Contains(plain, "__gluon") {
		t.Errorf("gluon's own harness reached the report: %q", plain)
	}
	if !strings.Contains(plain, "inlining call to add") || !strings.Contains(plain, "add(1, 2)") {
		t.Errorf("the decision or its quote is missing: %q", plain)
	}
	if strings.ContainsRune(plain, '\x1b') {
		t.Errorf("the plain form carries escape codes: %q", plain)
	}

	// Silence is an answer and has to be said out loud.
	if got := inspect.PlainInline(nil, "nothing here"); !strings.Contains(got, "nothing here") {
		t.Errorf("an empty report did not say so: %q", got)
	}
	// Filtered down to nothing is the same answer as empty.
	only := []eval.Diag{{Text: "can inline __gluonPrint with cost 12"}}
	if got := inspect.PlainInline(only, "nothing here"); !strings.Contains(got, "nothing here") {
		t.Errorf("a report of only gluon's own names did not read as empty: %q", got)
	}

	rich := inspect.RenderInline(msgs, "nothing here", pretty.Styles{}, true)
	if !strings.Contains(rich, "inlining call to add") {
		t.Errorf("the rich twin lost the decision: %q", rich)
	}
}

func TestRenderAsmTwins(t *testing.T) {
	a := eval.AsmResult{
		Symbol:  "main.add",
		Emitted: true,
		Lines:   []string{"main.add STEXT size=16", "\t0x0000 (entry 0 line 0)\tADD\tR1, R0, R0"},
	}
	plain := inspect.PlainAsm(a)
	if !strings.Contains(plain, "main.add STEXT") || !strings.Contains(plain, "entry 0 line 0") {
		t.Errorf("the listing lost its header or its position: %q", plain)
	}
	if strings.ContainsRune(plain, '\x1b') {
		t.Errorf("the plain form carries escape codes: %q", plain)
	}
	if rich := inspect.RenderAsm(a, pretty.Styles{}, true); !strings.Contains(rich, "ADD\tR1") {
		t.Errorf("the rich twin lost an instruction: %q", rich)
	}

	// A symbol with no block says what happened and why it can happen.
	none := inspect.PlainAsm(eval.AsmResult{Symbol: "main.tiny"})
	if !strings.Contains(none, "no code") || !strings.Contains(none, "main.tiny") {
		t.Errorf("an empty listing did not explain itself: %q", none)
	}
	if !strings.Contains(none, "inlined at every call site") {
		t.Errorf("the likeliest reason is not named: %q", none)
	}
}

func TestRenderVetTwins(t *testing.T) {
	v := eval.VetResult{Findings: []eval.Diag{
		{Entry: 0, Text: `fmt.Printf format %s has arg 42 of wrong type int`, Quote: `    fmt.Printf("%s", 42)`},
	}}
	plain := inspect.PlainVet(v)
	if !strings.Contains(plain, "wrong type int") || !strings.Contains(plain, `fmt.Printf("%s", 42)`) {
		t.Errorf("the finding or its quote is missing: %q", plain)
	}
	if strings.ContainsRune(plain, '\x1b') {
		t.Errorf("the plain form carries escape codes: %q", plain)
	}
	if got := inspect.PlainVet(eval.VetResult{}); !strings.Contains(got, "vet found nothing") {
		t.Errorf("a clean session did not say so: %q", got)
	}
	if got := inspect.RenderVet(v, pretty.Styles{}, true); !strings.Contains(got, "wrong type int") {
		t.Errorf("the rich twin lost the finding: %q", got)
	}
}

func TestRenderRaceTwins(t *testing.T) {
	// Nothing found: the value, and the caveat, every time.
	clean := inspect.PlainRace(eval.RaceResult{}, "42")
	if !strings.Contains(clean, "42") {
		t.Errorf("the expression's value was not shown: %q", clean)
	}
	if !strings.Contains(clean, "not proof") {
		t.Errorf("the absence caveat is missing: %q", clean)
	}

	raced := eval.RaceResult{
		Raced:  true,
		Report: "WARNING: DATA RACE\n  Read at 0x0 by goroutine 7:\n      entry 3 line 1: go func() { n++ }()",
	}
	got := inspect.PlainRace(raced, "42")
	if !strings.Contains(got, "DATA RACE") || !strings.Contains(got, "entry 3 line 1") {
		t.Errorf("the report or its mapped frame is missing: %q", got)
	}
	// The run happened; a race is not a reason to hide what it produced.
	if !strings.Contains(got, "42") {
		t.Errorf("the value was dropped because a race was found: %q", got)
	}
	if strings.Contains(got, "not proof") {
		t.Errorf("the absence caveat was printed alongside a race: %q", got)
	}
	if strings.ContainsRune(got, '\x1b') {
		t.Errorf("the plain form carries escape codes: %q", got)
	}

	// The first instrumented build says what it paid for.
	first := inspect.PlainRace(eval.RaceResult{FirstBuild: true}, "42")
	if !strings.Contains(first, "instrumented standard library") {
		t.Errorf("the first build was not explained: %q", first)
	}
	if rich := inspect.RenderRace(raced, "42", pretty.Styles{}, true); !strings.Contains(rich, "DATA RACE") {
		t.Errorf("the rich twin lost the report: %q", rich)
	}
}
