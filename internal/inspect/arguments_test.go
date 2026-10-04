package inspect

import (
	"slices"
	"strings"
	"testing"
)

// args type-checks a session with the callee appended as its trailing
// expression, which is the shape Evaluator.Analyze hands the REPL.
func args(t *testing.T, callee string, index int, lines ...string) (string, []string) {
	t.Helper()
	return Arguments(analyze(t, append(lines, callee)...), callee, index)
}

// TestArgumentsFiltersByAssignability is the whole point of the capability: the
// candidates are not the names in scope, they are the names the parameter will
// take. types.AssignableTo is the compiler's own rule, so a name offered here
// is a line that builds.
func TestArgumentsFiltersByAssignability(t *testing.T) {
	session := []string{
		"var w io.Writer = os.Stdout",
		"n := 3",
		"s := \"hi\"",
	}
	tests := []struct {
		name   string
		callee string
		index  int
		want   []string
		absent []string
	}{
		{
			// The exact type, and nothing else in scope is an io.Writer.
			name: "an interface parameter", callee: "fmt.Fprintln", index: 0,
			want: []string{"w"}, absent: []string{"n", "s"},
		},
		{
			// ...any takes everything, which is the correct answer and not an
			// interesting one — what matters is that the variadic tail is
			// reached at all.
			name: "a variadic tail", callee: "fmt.Fprintln", index: 1,
			want: []string{"n", "s", "w"},
		},
		{
			name: "a variadic tail further along", callee: "fmt.Fprintln", index: 7,
			want: []string{"n", "s", "w"},
		},
		{
			name: "an exact type", callee: "strings.Repeat", index: 0,
			want: []string{"s"}, absent: []string{"n", "w"},
		},
		{
			name: "the second parameter has its own type", callee: "strings.Repeat", index: 1,
			want: []string{"n"}, absent: []string{"s", "w"},
		},
		{
			// Non-variadic, and the call already has every argument it takes.
			name: "past the last parameter", callee: "strings.Repeat", index: 2,
			absent: []string{"n", "s", "w"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sig, got := args(t, tc.callee, tc.index, session...)
			if sig == "" {
				t.Fatalf("no signature for %s", tc.callee)
			}
			for _, w := range tc.want {
				if !slices.Contains(got, w) {
					t.Errorf("%s at %d: missing %q in %v", tc.callee, tc.index, w, got)
				}
			}
			for _, a := range tc.absent {
				if slices.Contains(got, a) {
					t.Errorf("%s at %d: offered %q, which does not fit: %v", tc.callee, tc.index, a, got)
				}
			}
		})
	}
}

// TestArgumentsTakesUntypedConstants is the rule that makes the answer the
// compiler's rather than a name match: an untyped constant is assignable to
// every numeric type it fits in, so it belongs in both lists.
func TestArgumentsTakesUntypedConstants(t *testing.T) {
	session := []string{
		"const k = 3",
		"func takesInt(i int) {}",
		"func takesFloat(f float64) {}",
	}
	for _, callee := range []string{"takesInt", "takesFloat"} {
		if _, got := args(t, callee, 0, session...); !slices.Contains(got, "k") {
			t.Errorf("%s: an untyped constant was not offered: %v", callee, got)
		}
	}
	// A typed int is not a float64, and offering it would not compile.
	session = append(session, "n := 3")
	if _, got := args(t, "takesFloat", 0, session...); slices.Contains(got, "n") {
		t.Errorf("a typed int was offered for a float64 parameter: %v", got)
	}
}

// TestArgumentsSignatureReads pins the hint's shape: the callee's own text, the
// session's package as the qualifier so no import paths leak in, and the
// parameter being typed in brackets.
func TestArgumentsSignatureReads(t *testing.T) {
	tests := []struct {
		name   string
		callee string
		index  int
		want   string
	}{
		{
			name: "the first parameter", callee: "strings.Replace", index: 0,
			want: "strings.Replace([s string], old string, new string, n int) string",
		},
		{
			name: "the last parameter", callee: "strings.Replace", index: 3,
			want: "strings.Replace(s string, old string, new string, [n int]) string",
		},
		{
			name: "past the last parameter", callee: "strings.Replace", index: 4,
			want: "strings.Replace(s string, old string, new string, n int) string",
		},
		{
			// The variadic parameter is written as the call site writes it, and
			// stays marked however many arguments follow it.
			name: "a variadic parameter", callee: "fmt.Fprintln", index: 3,
			want: "fmt.Fprintln(w io.Writer, [a ...any]) (n int, err error)",
		},
		{
			name: "a generic callee", callee: "slices.Sort", index: 0,
			want: "slices.Sort[S ~[]E, E cmp.Ordered]([x S])",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := args(t, tc.callee, tc.index, "x := 1")
			if got != tc.want {
				t.Errorf("signature =\n  %q\nwant\n  %q", got, tc.want)
			}
		})
	}
}

// TestArgumentsOffersNothingForAGeneric: assignability against a type parameter
// is inference, and gluon shows the signature rather than half an answer.
func TestArgumentsOffersNothingForAGeneric(t *testing.T) {
	sig, got := args(t, "slices.Sort", 0, "xs := []int{3, 1, 2}")
	if !strings.Contains(sig, "cmp.Ordered") {
		t.Errorf("the generic signature was not shown: %q", sig)
	}
	if got != nil {
		t.Errorf("a generic callee offered candidates: %v", got)
	}
}

// TestArgumentsIsSilent is constraint F at the keystroke: every failure returns
// nothing at all, and none of them is an error the user has to read.
func TestArgumentsIsSilent(t *testing.T) {
	tests := []struct {
		name   string
		callee string
		lines  []string
	}{
		{name: "a type — a conversion, not a call", callee: "int"},
		{name: "a declared type", callee: "Point", lines: []string{"type Point struct{ X int }"}},
		{name: "a value that is not callable", callee: "n", lines: []string{"n := 3"}},
		{name: "a name nothing declared", callee: "nosuchthing"},
		{name: "a builtin, which has no signature to read", callee: "len"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sig, got := args(t, tc.callee, 0, tc.lines...)
			if sig != "" || got != nil {
				t.Errorf("Arguments = %q, %v; want silence", sig, got)
			}
		})
	}
}

// TestArgumentsWithoutACheckerIsSilent: a disabled checker reaches here as a
// nil result, and it must not be a panic on the keystroke path.
func TestArgumentsWithoutACheckerIsSilent(t *testing.T) {
	if sig, got := Arguments(nil, "fmt.Println", 0); sig != "" || got != nil {
		t.Errorf("Arguments(nil) = %q, %v; want silence", sig, got)
	}
}

// TestArgumentsOffersTheSessionsOwnNames: a func the session declared is as
// callable as a stdlib one, and its parameters filter the same way.
func TestArgumentsOffersTheSessionsOwnNames(t *testing.T) {
	session := []string{
		"type Point struct{ X, Y int }",
		"p := Point{X: 1}",
		"n := 2",
		"func move(p Point, by int) Point { return Point{p.X + by, p.Y} }",
	}
	sig, got := args(t, "move", 0, session...)
	if want := "move([p Point], by int) Point"; sig != want {
		t.Errorf("signature = %q, want %q", sig, want)
	}
	if !slices.Contains(got, "p") || slices.Contains(got, "n") {
		t.Errorf("candidates = %v, want p and not n", got)
	}
	if _, got := args(t, "move", 1, session...); !slices.Contains(got, "n") || slices.Contains(got, "p") {
		t.Errorf("candidates at 1 = %v, want n and not p", got)
	}
	// The func itself is in scope, and it is not a Point.
	if _, got := args(t, "move", 0, session...); slices.Contains(got, "move") {
		t.Errorf("the callee was offered as its own argument: %v", got)
	}
}
