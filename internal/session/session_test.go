package session

import (
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		in   string
		want Kind
	}{
		// Expressions: the bare-value case a REPL exists for.
		{"1 + 1", KindExpr},
		{"x", KindExpr},
		{`fmt.Println("hi")`, KindExpr}, // a call is an expression; arity is a typing question
		{"[]int{1, 2}", KindExpr},
		{"func() {}", KindExpr}, // a func literal is a value
		{"m[k]", KindExpr},

		// Declarations that must hoist above main.
		{"func f() int { return 1 }", KindDecl},
		{"type T struct{ X int }", KindDecl}, // must hoist or methods are impossible
		{"func (t T) Sum() int { return t.X }", KindDecl},
		{"const N = 10", KindDecl},

		// Statements, including var: hoisting var would stop it referencing
		// locals bound by earlier entries.
		{"x := 1", KindStmt},
		{"var y = 2", KindStmt},
		{"x = 3", KindStmt},
		{"for i := 0; i < 3; i++ { _ = i }", KindStmt},
		{"if true { _ = 1 }", KindStmt},
	}
	for _, tc := range tests {
		got, err := Classify(tc.in)
		if err != nil {
			t.Errorf("Classify(%q): unexpected error: %v", tc.in, err)
			continue
		}
		if got.Kind != tc.want {
			t.Errorf("Classify(%q) = %v, want %v", tc.in, got.Kind, tc.want)
		}
	}
}

func TestClassifyBinds(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"x := 1", []string{"x"}},
		{"a, b := 1, 2", []string{"a", "b"}},
		{"_, err := f()", []string{"err"}}, // blank is not a binding
		{"var n int", []string{"n"}},
		{"x = 1", nil},                          // assignment binds nothing
		{"for i := 0; i < 3; i++ {}", nil},      // scoped to the loop, never unused across entries
		{"if v, ok := m[k]; ok { _ = v }", nil}, // scoped to the if
	}
	for _, tc := range tests {
		got, err := Classify(tc.in)
		if err != nil {
			t.Fatalf("Classify(%q): %v", tc.in, err)
		}
		if len(got.Binds) != len(tc.want) {
			t.Errorf("Classify(%q).Binds = %v, want %v", tc.in, got.Binds, tc.want)
			continue
		}
		for i := range tc.want {
			if got.Binds[i] != tc.want[i] {
				t.Errorf("Classify(%q).Binds = %v, want %v", tc.in, got.Binds, tc.want)
				break
			}
		}
	}
}

func TestIsIncomplete(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"1 + 1", false},
		{"func f() {", true},
		{"func f() {\n\treturn\n}", false},
		{"[]int{1,", true},
		{`fmt.Println("}")`, false}, // a brace inside a string must not count
		{"x := `raw {`", false},     // a closed raw string is complete
		{"x := `raw {", true},       // ...an unterminated one is not
		{"s := \"abc", true},        // unterminated interpreted string
		{"x := 1 /* open", true},    // unterminated comment
		{"// } comment", false},
	}
	for _, tc := range tests {
		if got := IsIncomplete(tc.in); got != tc.want {
			t.Errorf("IsIncomplete(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestCloneDoesNotShareBinds is the aliasing this helper exists for: an entry
// carries a []string, so a shallow copy of the entry slice would hand the
// caller a snapshot the live session can still write into.
func TestCloneDoesNotShareBinds(t *testing.T) {
	s := &Session{Entries: []Entry{
		{Kind: KindStmt, Src: "x := 1", Binds: []string{"x"}},
		{Kind: KindExpr, Src: "x + 1", Pinned: true},
	}}

	snap := s.Clone()

	s.Entries[0].Binds[0] = "mutated"
	s.Entries[0].Src = "y := 2"
	s.Entries[1].Pinned = false
	s.Append(Entry{Kind: KindExpr, Src: "extra"})

	if len(snap.Entries) != 2 {
		t.Fatalf("clone has %d entries, want the 2 it was taken from", len(snap.Entries))
	}
	if got := snap.Entries[0].Binds[0]; got != "x" {
		t.Errorf("clone's Binds followed the live entry: got %q, want %q", got, "x")
	}
	if got := snap.Entries[0].Src; got != "x := 1" {
		t.Errorf("clone's Src followed the live entry: got %q", got)
	}
	if !snap.Entries[1].Pinned {
		t.Error("clone lost the pinned state it was taken with")
	}
}

// TestCloneOfEmptyIsUsable: a session with no entries clones to one that can be
// appended to, not to a nil the caller has to check for.
func TestCloneOfEmptyIsUsable(t *testing.T) {
	snap := (&Session{}).Clone()
	if snap == nil {
		t.Fatal("Clone of an empty session is nil")
	}
	if len(snap.Entries) != 0 {
		t.Errorf("clone of an empty session has %d entries", len(snap.Entries))
	}
	snap.Append(Entry{Src: "x := 1"})
	if len(snap.Entries) != 1 {
		t.Error("clone of an empty session cannot be appended to")
	}
}

func TestSplitConstructsKeepsAMultiLineFuncWhole(t *testing.T) {
	got, rest := SplitConstructs("x := 1\nfunc f() int {\n\treturn 2\n}\nf()\n")
	want := []string{"x := 1", "func f() int {\n\treturn 2\n}", "f()"}
	if rest != "" {
		t.Errorf("rest = %q, want empty", rest)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d constructs %q, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("construct %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSplitConstructsIsNotFooledByBracesInsideALiteral(t *testing.T) {
	// An unclosed brace inside a string or a comment must not open a construct
	// and swallow the lines after it. A brace-counting splitter reads one
	// never-finished construct here; the scanner reads three finished ones.
	//
	// The bare comment is a construct of its own, which is what the piped
	// driver has always done with one — Classify is what answers for it, not
	// the splitter.
	got, rest := SplitConstructs("s := \"{\"\n// {\nlen(s)\n")
	if rest != "" {
		t.Errorf("rest = %q, want empty — nothing here is unfinished", rest)
	}
	want := []string{"s := \"{\"", "// {", "len(s)"}
	if len(got) != len(want) {
		t.Fatalf("got %d constructs %q, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("construct %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSplitConstructsHandsBackTheTrailingPartial(t *testing.T) {
	got, rest := SplitConstructs("x := 1\nfor i := range 3 {\n")
	if len(got) != 1 || got[0] != "x := 1" {
		t.Fatalf("got %q, want only the assignment", got)
	}
	if rest != "for i := range 3 {\n" {
		t.Errorf("rest = %q, want the unfinished loop", rest)
	}
}

func TestSplitConstructsDropsBlankRuns(t *testing.T) {
	got, rest := SplitConstructs("\n\nx := 1\n\n\ny := 2\n\n")
	if rest != "" {
		t.Errorf("rest = %q, want empty", rest)
	}
	if len(got) != 2 || got[0] != "x := 1" || got[1] != "y := 2" {
		t.Fatalf("got %q, want the two assignments", got)
	}
}

// TestSplitConstructsMatchesTheLoopItReplaced is a differential test against the
// accumulate-until-complete loop that submitAll, runPipe and paste each spelled
// for themselves before SplitConstructs existed. It is kept because those three
// have to agree: text that reloads into different entries than the same text
// pasted would be two ideas of what a construct is, and this is the only place
// that would notice.
func TestSplitConstructsMatchesTheLoopItReplaced(t *testing.T) {
	// The loop as it was, verbatim, as the oracle.
	old := func(text string) []string {
		var buf, out []string
		for _, line := range strings.Split(text, "\n") {
			buf = append(buf, line)
			joined := strings.Join(buf, "\n")
			if strings.TrimSpace(joined) == "" {
				buf = nil
				continue
			}
			if IsIncomplete(joined) {
				continue
			}
			buf = nil
			out = append(out, joined)
		}
		return out
	}

	for _, text := range []string{
		"",
		"\n",
		"x := 1",
		"x := 1\n",
		"x := 1\ny := 2\n",
		"\n\nx := 1\n\n\ny := 2\n\n",
		"func f() int {\n\treturn 2\n}\nf()\n",
		"type T struct {\n\tA int\n}\nT{A: 1}\n",
		"s := \"{\"\n// {\nlen(s)\n",
		"s := `a\nb`\nlen(s)\n",
		"for i := range 3 {\n",
		"m := map[string][]int{\n\t\"a\": {1, 2},\n}\nm\n",
		"\t\n   \nx := 1",
	} {
		want, got := old(text), func() []string { c, _ := SplitConstructs(text); return c }()
		if len(want) != len(got) {
			t.Errorf("%q: got %d constructs %q, want %d %q", text, len(got), got, len(want), want)
			continue
		}
		for i := range want {
			if want[i] != got[i] {
				t.Errorf("%q: construct %d = %q, want %q", text, i, got[i], want[i])
			}
		}
	}
}

func TestSplitConstructsAtNumbersTheLines(t *testing.T) {
	// Line 1 blank, 2 the assignment, 3-5 the func, 6 blank, 7 the call.
	got, rest := SplitConstructsAt("\nx := 1\nfunc f() int {\n\treturn 2\n}\n\nf()\n")
	if rest != "" {
		t.Errorf("rest = %q, want empty", rest)
	}
	want := []Construct{
		{Src: "x := 1", Line: 2},
		{Src: "func f() int {\n\treturn 2\n}", Line: 3},
		{Src: "f()", Line: 7},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d constructs %+v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("construct %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
