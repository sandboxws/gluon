//go:build integration

package repl

import (
	"strings"
	"testing"
)

// Argument completion is the intersection of two things only a live session
// knows: the callee's signature, and what the session has bound with what type.
// A fixture could fake either one; only a real session has both.
func TestCompletesArgumentsAgainstALiveSession(t *testing.T) {
	c := testCore(t)
	for _, line := range []string{
		"var w io.Writer = os.Stdout",
		"count := 7",
		`label := "hi"`,
	} {
		if res := c.Submit(line); res.Err {
			t.Fatalf("setup %q: %s", line, res.Out)
		}
	}

	t.Run("an interface parameter takes the one name that fits", func(t *testing.T) {
		got := c.Complete("fmt.Fprintln(")
		if !hasLine(got, "fmt.Fprintln(w") {
			t.Errorf("Complete = %v, want the writer", got)
		}
		for _, no := range []string{"fmt.Fprintln(count", "fmt.Fprintln(label"} {
			if hasLine(got, no) {
				t.Errorf("offered %q, which is not an io.Writer: %v", no, got)
			}
		}
	})

	t.Run("the second parameter has its own type", func(t *testing.T) {
		got := c.Complete("strings.Repeat(label, ")
		if !hasLine(got, "strings.Repeat(label, count") {
			t.Errorf("Complete = %v, want the int", got)
		}
		for _, no := range []string{"strings.Repeat(label, label", "strings.Repeat(label, w"} {
			if hasLine(got, no) {
				t.Errorf("offered %q, which is not an int: %v", no, got)
			}
		}
	})

	t.Run("a partly typed argument filters case-sensitively", func(t *testing.T) {
		if got := c.Complete("strings.Repeat(label, co"); !hasLine(got, "strings.Repeat(label, count") {
			t.Errorf("Complete = %v", got)
		}
		// Invariant 16: accepting a case-insensitive match would leave a line
		// the preview never showed.
		if got := c.Complete("strings.Repeat(label, CO"); len(got) != 0 {
			t.Errorf("Complete = %v, want nothing for a case mismatch", got)
		}
	})

	t.Run("nothing fits, so nothing is offered", func(t *testing.T) {
		// count is an int and label is a string; neither is a func, and the
		// identifier list must not be offered in their place.
		if got := c.Complete("sort.Slice(label, "); len(got) != 0 {
			t.Errorf("Complete = %v, want nothing", got)
		}
	})

	// The Editing guide shows the strings.Repeat line. These are asserted
	// verbatim because a signature that reads differently from the documented
	// one is a doc bug nothing else would catch.
	t.Run("the signatures read as documented", func(t *testing.T) {
		for _, tc := range []struct{ line, want string }{
			{
				line: "fmt.Fprintln(",
				want: "fmt.Fprintln([w io.Writer], a ...any) (n int, err error)",
			},
			{
				line: `strings.Repeat("ab", `,
				want: "strings.Repeat(s string, [count int]) string",
			},
			{
				line: "slices.Sort(",
				want: "slices.Sort[S ~[]E, E cmp.Ordered]([x S])",
			},
		} {
			if got := c.Hint(tc.line); got != tc.want {
				t.Errorf("Hint(%q) =\n  %q\nwant\n  %q", tc.line, got, tc.want)
			}
		}
	})

	t.Run("the hint marks the parameter being typed", func(t *testing.T) {
		first := c.Hint("strings.Replace(")
		if !strings.Contains(first, "[s string]") {
			t.Errorf("Hint = %q, want the first parameter marked", first)
		}
		second := c.Hint("strings.Replace(label, ")
		if !strings.Contains(second, "[old string]") {
			t.Errorf("Hint = %q, want the second parameter marked", second)
		}
		if strings.Contains(second, "[s string]") {
			t.Errorf("Hint = %q, still marks the first parameter", second)
		}
	})

	t.Run("the hint leaves with the call", func(t *testing.T) {
		for _, line := range []string{`strings.Replace("a", "b", "c", 1)`, "count", ""} {
			if got := c.Hint(line); got != "" {
				t.Errorf("Hint(%q) = %q, want none", line, got)
			}
		}
	})

	t.Run("a generic callee is shown and not completed", func(t *testing.T) {
		if res := c.Submit("xs := []int{3, 1, 2}"); res.Err {
			t.Fatalf("setup: %s", res.Out)
		}
		hint := c.Hint("slices.Sort(")
		if !strings.Contains(hint, "cmp.Ordered") {
			t.Errorf("Hint = %q, want the generic signature", hint)
		}
		if got := c.Complete("slices.Sort("); len(got) != 0 {
			t.Errorf("Complete = %v, want nothing for a generic callee", got)
		}
	})

	t.Run("a conversion is not a call", func(t *testing.T) {
		if got := c.Hint("int("); got != "" {
			t.Errorf("Hint = %q, want none for a conversion", got)
		}
		if got := c.Complete("int("); len(got) != 0 {
			t.Errorf("Complete = %v, want nothing for a conversion", got)
		}
	})

	t.Run("selector completion inside a call still works", func(t *testing.T) {
		// Arguments deals in names the session bound, and os.Stdout is not one
		// of them. The path that completes package members keeps that case.
		var got []string
		for range 100 {
			if got = c.Complete("fmt.Fprintln(os.Std"); len(got) > 0 {
				break
			}
		}
		if !hasLine(got, "fmt.Fprintln(os.Stdout") {
			t.Errorf("Complete = %v", got)
		}
	})
}

// TestArgumentsWithoutTheChecker is constraint F end to end: the checker is
// where both halves come from, so its absence is silence — and everything that
// did not need it is untouched.
func TestArgumentsWithoutTheChecker(t *testing.T) {
	t.Setenv("GLUON_NO_TYPECHECK", "1")
	c := testCore(t)
	for _, line := range []string{"var w io.Writer = os.Stdout", "count := 7"} {
		if res := c.Submit(line); res.Err {
			t.Fatalf("setup %q: %s", line, res.Out)
		}
	}

	if got := c.Hint("fmt.Fprintln("); got != "" {
		t.Errorf("Hint = %q with no checker", got)
	}
	if got := c.Complete("fmt.Fprintln("); len(got) != 0 {
		t.Errorf("Complete = %v with no checker", got)
	}
	if got := c.Complete("fmt.Fprintln(co"); len(got) != 0 {
		t.Errorf("Complete = %v with no checker", got)
	}

	// Identifier completion never needed the checker: the names a session
	// binds are known syntactically, and that is unchanged.
	if got := c.Complete("cou"); !hasLine(got, "count") {
		t.Errorf("Complete(\"cou\") = %v, want the bound name", got)
	}
}

// TestTheCallHintWinsInsideAMetaArgument: a meta command whose argument is Go
// still gets a call's signature inside the call, because that is the more
// specific answer. Outside it, the command's usage is the hint.
func TestTheCallHintWinsInsideAMetaArgument(t *testing.T) {
	c := testCore(t)
	if got, want := c.Hint(`:t strings.Repeat("ab", `), "strings.Repeat(s string, [count int]) string"; got != want {
		t.Errorf("inside the call:\n got %q\nwant %q", got, want)
	}
	if got, want := c.Hint(":bench "), "[-count <n>] [-cpu <list>] [-profile] <exp>[, <other>]"; got != want {
		t.Errorf("before the operand:\n got %q\nwant %q", got, want)
	}
}
