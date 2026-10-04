package complete

import "testing"

// TestCallAt pins what the scan finds and, as importantly, what it refuses.
// A refusal is the safe answer here: it costs an offer that would have been
// useful, where a wrong accept costs a type check on a callee that is not one.
func TestCallAt(t *testing.T) {
	tests := []struct {
		name   string
		line   string
		ok     bool
		callee string
		index  int
		prefix string
	}{
		{name: "a bare call", line: "f(", ok: true, callee: "f"},
		{
			name: "a qualified call", line: "fmt.Fprintln(",
			ok: true, callee: "fmt.Fprintln",
		},
		{
			name: "a method call", line: "b.WriteString(",
			ok: true, callee: "b.WriteString", index: 0,
		},
		{
			name: "a chained selector", line: "a.b.C(",
			ok: true, callee: "a.b.C",
		},
		{
			name: "the second argument", line: "strings.Repeat(s, ",
			ok: true, callee: "strings.Repeat", index: 1,
		},
		{
			name: "a partly typed argument", line: "strings.Repeat(s, co",
			ok: true, callee: "strings.Repeat", index: 1, prefix: "co",
		},
		{
			// The prefix keeps its dot so Suggest can hand a selector back to
			// the path that completes package members.
			name: "a selector as the argument", line: "fmt.Fprintln(os.St",
			ok: true, callee: "fmt.Fprintln", prefix: "os.St",
		},
		{
			// The inner call is the open one; the outer's comma is not counted
			// because the scan starts after the inner paren.
			name: "nested calls", line: "fmt.Println(strings.Repeat(s, ",
			ok: true, callee: "strings.Repeat", index: 1,
		},
		{
			// A closed inner call leaves the outer one open, and its comma is
			// the outer's own.
			name: "a closed call as an argument", line: "fmt.Println(f(1, 2), ",
			ok: true, callee: "fmt.Println", index: 1,
		},
		{
			name: "a comma inside a closed literal", line: "f(Point{X: 1, Y: 2}, ",
			ok: true, callee: "f", index: 1,
		},
		{
			name: "a comma inside a string", line: `f("a, b", `,
			ok: true, callee: "f", index: 1,
		},
		{
			// A parenthesis inside a closed rune literal is content, not a
			// call, and the argument after it is still the second.
			name: "a paren inside a rune argument", line: `f('(', `,
			ok: true, callee: "f", index: 1,
		},
		{
			name: "a call after a keyword", line: "go f(",
			ok: true, callee: "f",
		},
		{
			name: "a call in an if", line: "if strings.Contains(s, ",
			ok: true, callee: "strings.Contains", index: 1,
		},

		{name: "no call at all", line: "x := 1", ok: false},
		{name: "a closed call", line: "f(1)", ok: false},
		{name: "a paren inside a string", line: `s := "f(`, ok: false},
		{name: "a paren inside a rune", line: `c := '('`, ok: false},
		{name: "a paren inside a raw string", line: "s := `f(", ok: false},
		{name: "an unterminated string inside a call", line: `f("ab`, ok: false},
		{name: "a paren inside a comment", line: "x := 1 // f(", ok: false},
		{name: "a keyword before the paren", line: "if (", ok: false},
		{name: "a for before the paren", line: "for (", ok: false},
		{name: "a func literal's parameters", line: "f := func(", ok: false},
		{name: "a callee ending in a paren", line: "get()(", ok: false},
		{name: "a callee ending in a bracket", line: "fs[i](", ok: false},
		{name: "a grouping paren", line: "x := (", ok: false},
		{name: "a paren after an operator", line: "x := 1 + (", ok: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := callAt(tc.line)
			if ok != tc.ok {
				t.Fatalf("callAt(%q) ok = %v, want %v (got %+v)", tc.line, ok, tc.ok, got)
			}
			if !ok {
				return
			}
			if got.Callee != tc.callee || got.Index != tc.index || got.Prefix != tc.prefix {
				t.Errorf("callAt(%q) = %+v, want callee %q index %d prefix %q",
					tc.line, got, tc.callee, tc.index, tc.prefix)
			}
		})
	}
}

// stubArgs is a fixed answer for one callee, so the Suggest tests exercise the
// wiring rather than the checker.
func stubArgs(callee string, sig string, cands ...string) func(string, int) (string, []string) {
	return func(c string, index int) (string, []string) {
		if c != callee {
			return "", nil
		}
		return sig, cands
	}
}

// TestSuggestCompletesArguments is the wiring: inside an open call the answer
// comes from Arguments, whole-line, prefix-filtered, and nothing else answers
// in its place.
func TestSuggestCompletesArguments(t *testing.T) {
	ctx := Context{
		Names:     []string{"n", "s", "w", "writer"},
		Packages:  map[string]string{"os": "os"},
		Members:   func(string) []string { return []string{"Stdout", "Stderr"} },
		Arguments: stubArgs("fmt.Fprintln", "fmt.Fprintln([w io.Writer], a ...any)", "w", "writer"),
	}
	tests := []struct {
		name string
		line string
		want []string
	}{
		{
			name: "the open call decides",
			line: "fmt.Fprintln(",
			want: []string{"fmt.Fprintln(w", "fmt.Fprintln(writer"},
		},
		{
			name: "a partly typed argument filters",
			line: "fmt.Fprintln(wr",
			want: []string{"fmt.Fprintln(writer"},
		},
		{
			// Nothing in scope fits, so nothing is offered — and in particular
			// the identifier list is not offered in its place, which it would
			// be if this branch fell through.
			name: "nothing fits",
			line: "unknown(",
			want: nil,
		},
		{
			name: "a prefix nothing extends",
			line: "fmt.Fprintln(zz",
			want: nil,
		},
		{
			// The selector path still owns a dotted argument: Arguments deals
			// in names the session bound, and os.Stdout is not one of them.
			name: "a package member inside a call",
			line: "fmt.Fprintln(os.St",
			want: []string{"fmt.Fprintln(os.Stderr", "fmt.Fprintln(os.Stdout"},
		},
		{
			// Outside a call the identifier path is untouched.
			name: "no call at all",
			line: "wr",
			want: []string{"writer"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Suggest(tc.line, ctx)
			if len(got) != len(tc.want) {
				t.Fatalf("Suggest(%q) = %v, want %v", tc.line, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("Suggest(%q) = %v, want %v", tc.line, got, tc.want)
				}
			}
		})
	}
}

// TestSuggestWithoutArgumentsIsUnchanged: a Context with no Arguments — a
// disabled checker, or any caller that never set it — completes exactly as it
// did before this capability, identifiers and all.
func TestSuggestWithoutArgumentsIsUnchanged(t *testing.T) {
	ctx := Context{Names: []string{"writer", "n"}}
	got := Suggest("fmt.Fprintln(wr", ctx)
	if len(got) != 1 || got[0] != "fmt.Fprintln(writer" {
		t.Errorf("Suggest = %v, want the identifier completion", got)
	}
}

// TestSuggestPrefersALiteralToACall: a composite literal being typed inside a
// call is a literal. Its fields are the only thing that can go there, so the
// literal check stays first.
func TestSuggestPrefersALiteralToACall(t *testing.T) {
	ctx := Context{
		Literal:   func(string) []string { return []string{"X", "Y"} },
		Arguments: stubArgs("f", "f([p Point])", "p"),
	}
	got := Suggest("f(Point{", ctx)
	if len(got) != 2 || got[0] != "f(Point{X: " {
		t.Errorf("Suggest = %v, want the literal's fields", got)
	}
}

// TestHintSharesTheScanWithSuggest: the hint answers for the same call Suggest
// completes, and it is never a candidate.
func TestHintSharesTheScanWithSuggest(t *testing.T) {
	arguments := stubArgs("fmt.Fprintln", "fmt.Fprintln([w io.Writer], a ...any)", "w")
	if got := Hint("fmt.Fprintln(", arguments); got != "fmt.Fprintln([w io.Writer], a ...any)" {
		t.Errorf("Hint = %q", got)
	}
	// Still inside the call while an argument is being typed, including a
	// dotted one that Suggest hands to the selector path.
	if got := Hint("fmt.Fprintln(os.St", arguments); got == "" {
		t.Error("the hint left while an argument was being typed")
	}
	for _, line := range []string{"fmt.Fprintln(w)", "x := 1", ""} {
		if got := Hint(line, arguments); got != "" {
			t.Errorf("Hint(%q) = %q, want none", line, got)
		}
	}
	if got := Hint("fmt.Fprintln(", nil); got != "" {
		t.Errorf("Hint with no lookup = %q, want none", got)
	}
}
