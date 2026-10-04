//go:build integration

package repl

import (
	"strings"
	"testing"
)

// TestTraceLeavesTheSessionAlone is invariant 14 on this path. :trace's rewrite
// pulls in runtime and fmt that the session does not have, and it runs through
// EvalTransient precisely so neither the entry list nor the resolved import set
// carries that afterwards — a session that grew an import because you asked
// where something was called from would be a session you could no longer
// reproduce from its own transcript.
func TestTraceLeavesTheSessionAlone(t *testing.T) {
	c := testCore(t)

	for _, line := range []string{"func double(n int) int { return n * 2 }", "x := 21"} {
		if res := c.Submit(line); res.Err {
			t.Fatalf("%s: %s", line, res.Out)
		}
	}
	entries := len(c.sess.Entries)
	imports := len(c.ev.Imports())

	res := c.Submit(":trace double(x)")
	if res.Err {
		t.Fatalf(":trace failed: %s", res.Out)
	}
	if !strings.Contains(res.Out, "42") {
		t.Errorf(":trace did not report the value:\n%s", res.Out)
	}

	if got := len(c.sess.Entries); got != entries {
		t.Errorf("entries = %d, want %d — :trace added one", got, entries)
	}
	if got := len(c.ev.Imports()); got != imports {
		t.Errorf("imports = %d, want %d — :trace's rewrite leaked runtime or fmt", got, imports)
	}
}

// TestTraceShowsNoHarness: the wrapper :trace synthesizes, the main render
// writes around every session, and the temp directory the program was built in
// are all gluon's, and none of them is something the user typed or can open.
func TestTraceShowsNoHarness(t *testing.T) {
	c := testCore(t)
	if res := c.Submit("func double(n int) int { return n * 2 }"); res.Err {
		t.Fatalf("%s", res.Out)
	}

	res := c.Submit(":trace double(21)")
	if res.Err {
		t.Fatalf(":trace failed: %s", res.Out)
	}
	for _, leak := range []string{"main.main", "gluon-in-", "/private/var", "__gluon"} {
		if strings.Contains(res.Out, leak) {
			t.Errorf("%q reached the output:\n%s", leak, res.Out)
		}
	}
	// The expression itself is the one frame that is always there.
	if !strings.Contains(res.Out, "double(21)") {
		t.Errorf("the traced expression is missing from the chain:\n%s", res.Out)
	}
}

// TestTraceOfAVoidCall: a call with nothing to return is the natural thing to
// ask this about, so it reports the chain rather than refusing. The rewrite has
// a second shape for it — there is no value to name, so there is no pair to
// return.
func TestTraceOfAVoidCall(t *testing.T) {
	c := testCore(t)
	if res := c.Submit(`func shout(s string) { fmt.Println(s + "!") }`); res.Err {
		t.Fatalf("%s", res.Out)
	}

	res := c.Submit(`:trace shout("hi")`)
	if res.Err {
		t.Fatalf(":trace on a void call failed: %s", res.Out)
	}
	if !strings.Contains(res.Out, `shout("hi")`) {
		t.Errorf("the traced expression is missing from the chain:\n%s", res.Out)
	}
	// The call really ran, so its own output is there.
	if !strings.Contains(res.Out, "hi!") {
		t.Errorf("the void call did not run:\n%s", res.Out)
	}
}
