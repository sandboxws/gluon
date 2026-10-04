package repl

import (
	"reflect"
	"strings"
	"testing"
)

// TestDiffSplitsOnTheTopLevelComma. :diff takes two expressions, and an
// expression routinely contains commas of its own — a generic instantiation, a
// call, a composite literal. Only the comma between the operands separates
// them.
func TestDiffSplitsOnTheTopLevelComma(t *testing.T) {
	for _, tc := range []struct {
		arg  string
		want []string
	}{
		{"want, got", []string{"want", "got"}},
		{"a,b", []string{"a", "b"}},
		{"f(1, 2), g(3, 4)", []string{"f(1, 2)", "g(3, 4)"}},
		{"m[Pair[string, int]{}], n", []string{"m[Pair[string, int]{}]", "n"}},
		{`[]int{1, 2}, []int{1, 3}`, []string{"[]int{1, 2}", "[]int{1, 3}"}},
		{`strings.Split("a,b", ","), parts`, []string{`strings.Split("a,b", ",")`, "parts"}},
		{"User{Name: \"a\"}, u", []string{`User{Name: "a"}`, "u"}},
	} {
		if got := splitTop(tc.arg); !reflect.DeepEqual(got, tc.want) {
			t.Errorf(":diff %s split into %q, want %q", tc.arg, got, tc.want)
		}
	}
}

// TestDiffNeedsTwoExpressions covers both refusals the spec names: no argument
// is a usage line, and one argument says what is missing.
func TestDiffNeedsTwoExpressions(t *testing.T) {
	c := &Core{} // no evaluator: reaching one would panic rather than refuse

	res := c.diff("")
	if !res.Err || !strings.HasPrefix(res.Out, "usage:") {
		t.Errorf(":diff with no argument: got %q (err=%v), want a usage line", res.Out, res.Err)
	}
	if res := c.Submit(":diff"); !res.Err || !strings.HasPrefix(res.Out, "usage:") {
		t.Errorf(":diff through dispatch: got %q (err=%v), want a usage line", res.Out, res.Err)
	}

	res = c.diff("x")
	if !res.Err {
		t.Fatalf(":diff x: got a result, want an error:\n%s", res.Out)
	}
	if !strings.Contains(res.Out, "two expressions") {
		t.Errorf(":diff x: %q does not say two are required", res.Out)
	}

	res = c.diff("a, b, c")
	if !res.Err || !strings.Contains(res.Out, "two expressions") {
		t.Errorf(":diff a, b, c: got %q (err=%v), want a refusal naming the count", res.Out, res.Err)
	}
}

// TestDiffTakesTwoOperandsInOneArgument. The two values are one argument line,
// not two parameters, and go_diff's tool description is that line quoted — so
// an agent reads "a, b" and sends "a, b". Changing the spelling here changes
// what the tool tells a caller to send.
//
// :diff evaluates, so it is eval tier; TestMCPTierIsIntentional is where that
// decision lives.
func TestDiffTakesTwoOperandsInOneArgument(t *testing.T) {
	cmd, ok := (&Core{}).lookup(":diff")
	if !ok {
		t.Fatal(":diff is not registered")
	}
	if cmd.Arg != "a, b" {
		t.Errorf(":diff Arg = %q, want \"a, b\"", cmd.Arg)
	}
	if cmd.Static {
		t.Error(":diff evaluates; it must not be in the static tier")
	}
}
