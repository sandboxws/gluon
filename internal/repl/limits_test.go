package repl

import (
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/config"
	"github.com/sandboxws/gluon/internal/gluonrt"
)

// limitsCore is a Core with a config of its own and a real evaluator, because
// what these tests assert is that :settings reaches the evaluator. A recording
// stand-in would only prove applyLive called something.
func limitsCore(t *testing.T) *Core {
	t.Helper()
	tempConfig(t, "")
	c, err := NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// TestSettingsCarriesALimitToTheEvaluator. The setting is Live, which is a
// promise that the next line honours it — not that the file records it.
func TestSettingsCarriesALimitToTheEvaluator(t *testing.T) {
	c := limitsCore(t)

	if res := c.settingsCmd("value.items 500"); res.Err {
		t.Fatalf(":settings value.items 500: %s", res.Out)
	}
	if items, _ := c.ev.Limits(); items != 500 {
		t.Errorf("evaluator item limit = %d, want 500", items)
	}

	if res := c.settingsCmd("value.depth 12"); res.Err {
		t.Fatalf(":settings value.depth 12: %s", res.Out)
	}
	items, depth := c.ev.Limits()
	if depth != 12 {
		t.Errorf("evaluator depth limit = %d, want 12", depth)
	}
	// Setting one must not reset the other: applyLive passes the pair, so a
	// second change reading only its own key would silently undo the first.
	if items != 500 {
		t.Errorf("setting the depth reset the item limit to %d", items)
	}
}

// Returning a setting to its default has to put the default back in force,
// not merely delete the line. `-` is the spelling :settings uses for that.
func TestSettingsReturnsALimitToItsDefault(t *testing.T) {
	c := limitsCore(t)

	if res := c.settingsCmd("value.items 500"); res.Err {
		t.Fatalf(":settings value.items 500: %s", res.Out)
	}
	if res := c.settingsCmd("value.items -"); res.Err {
		t.Fatalf(":settings value.items -: %s", res.Out)
	}
	if items, _ := c.ev.Limits(); items != gluonrt.DefaultMaxItems {
		t.Errorf("item limit = %d after returning to the default, want %d", items, gluonrt.DefaultMaxItems)
	}
}

// A config file's limits are in force from the first line, without anyone
// having to set them again.
func TestConfiguredLimitsAreInForceAtStartup(t *testing.T) {
	tempConfig(t, "[value]\nitems = \"400\"\ndepth = \"9\"\n")
	c, err := NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	t.Cleanup(func() { c.Close() })

	items, depth := c.ev.Limits()
	if items != 400 || depth != 9 {
		t.Errorf("limits = (%d, %d), want (400, 9)", items, depth)
	}
}

// An invalid limit is refused before anything is written, and the message says
// what is accepted. The evaluator keeps the bound it had.
func TestSettingsRefusesAnInvalidLimit(t *testing.T) {
	c := limitsCore(t)

	for _, v := range []string{"0", "-5", "abc", "1e6", "200000"} {
		res := c.settingsCmd("value.items " + v)
		if !res.Err {
			t.Errorf(":settings value.items %s was accepted", v)
			continue
		}
		if !strings.Contains(res.Out, "whole number") && !strings.Contains(res.Out, "positive") &&
			!strings.Contains(res.Out, "above") {
			t.Errorf(":settings value.items %s said %q, which does not name what is accepted", v, res.Out)
		}
	}
	if items, _ := c.ev.Limits(); items != gluonrt.DefaultMaxItems {
		t.Errorf("a refused value moved the limit to %d", items)
	}
}

// The detail text has to say that the whole-value budget still applies, or a
// user who raises the limit and still sees a truncation has no explanation.
func TestLimitDetailNamesTheBoundThatStillApplies(t *testing.T) {
	for _, key := range []string{"value.items", "value.depth"} {
		o, ok := config.Lookup(key)
		if !ok {
			t.Fatalf("%s is not a setting", key)
		}
		if !strings.Contains(o.Detail, "whole value") {
			t.Errorf("%s detail does not name the whole-value bound:\n%s", key, o.Detail)
		}
	}
}

// The -n parse is deliberately narrow: everything after the flag is a Go
// expression, and a parser that looked past the first token would find a minus
// inside one and read it as a flag.
func TestCutInspectItems(t *testing.T) {
	for _, tc := range []struct {
		arg     string
		items   int
		rest    string
		wantErr bool
		whyItIs string
	}{
		{arg: "users", items: 0, rest: "users", whyItIs: "no flag"},
		{arg: "-n 300 users", items: 300, rest: "users"},
		{arg: "-n 1 xs[0:2]", items: 1, rest: "xs[0:2]"},
		{arg: "-n   500    rows", items: 500, rest: "rows"},
		{arg: "-n 300 f(a, b)", items: 300, rest: "f(a, b)"},
		{arg: "-nums", items: 0, rest: "-nums", whyItIs: "negating a variable, not a flag"},
		{arg: "-n300", items: 0, rest: "-n300", whyItIs: "no space, so not the flag"},
		{arg: "-x 3 users", items: 0, rest: "-x 3 users", whyItIs: "some other unary minus"},
		{arg: "-n 0 users", wantErr: true},
		{arg: "-n -5 users", wantErr: true},
		{arg: "-n abc users", wantErr: true},
		{arg: "-n 200000 users", wantErr: true, whyItIs: "past the bound gluon accepts"},
		{arg: "-n", wantErr: true, whyItIs: "the flag with nothing after it"},
		{arg: "-n users", wantErr: true, whyItIs: "the value is not a number"},
	} {
		t.Run(tc.arg, func(t *testing.T) {
			items, rest, err := cutInspectItems(tc.arg)
			if tc.wantErr {
				if err == nil {
					t.Errorf("cutInspectItems(%q) = (%d, %q, nil), want an error — %s", tc.arg, items, rest, tc.whyItIs)
				}
				return
			}
			if err != nil {
				t.Fatalf("cutInspectItems(%q): %v", tc.arg, err)
			}
			if items != tc.items || rest != tc.rest {
				t.Errorf("cutInspectItems(%q) = (%d, %q), want (%d, %q) — %s",
					tc.arg, items, rest, tc.items, tc.rest, tc.whyItIs)
			}
		})
	}
}

// A bad -n is refused before anything is resolved or evaluated, and the message
// begins with usage: the way every other refused invocation does.
func TestInspectRefusesABadItemLimit(t *testing.T) {
	c := limitsCore(t)

	for _, arg := range []string{"-n 0 xs", "-n -3 xs", "-n abc xs", "-n 200000 xs", "-n"} {
		res := c.inspect(arg)
		if !res.Err {
			t.Errorf(":inspect %s was accepted", arg)
			continue
		}
		if !strings.HasPrefix(res.Out, "usage:") {
			t.Errorf(":inspect %s said %q, want a usage line", arg, res.Out)
		}
	}
	// Nothing was evaluated, so the session is untouched and the bound is
	// still the session's own.
	if items, _ := c.ev.Limits(); items != gluonrt.DefaultMaxItems {
		t.Errorf("a refused -n moved the session's limit to %d", items)
	}
	if n := len(c.sess.Entries); n != 0 {
		t.Errorf("a refused -n left %d entries in the session", n)
	}
}

// The one-shot limit is one-shot: whatever :inspect -n did, the next ordinary
// line is bounded by value.items again.
func TestInspectRestoresTheSessionLimit(t *testing.T) {
	c := limitsCore(t)

	if r := c.settingsCmd("value.items 250"); r.Err {
		t.Fatalf(":settings value.items 250: %s", r.Out)
	}
	// It does not matter whether this resolves — the restore is in a defer, so
	// the failing paths are exactly the ones that would leak it.
	c.inspect("-n 900 nothingIsBoundToThisName")

	if items, _ := c.ev.Limits(); items != 250 {
		t.Errorf("item limit = %d after :inspect -n 900, want the session's 250", items)
	}
}

// :inspect with no argument still reports usage, and the usage line names -n
// so the flag is discoverable from the refusal as well as from :help.
func TestInspectUsageNamesTheFlag(t *testing.T) {
	c := limitsCore(t)
	res := c.inspect("")
	if !res.Err || !strings.HasPrefix(res.Out, "usage:") {
		t.Fatalf(":inspect with no argument: %q (err=%v)", res.Out, res.Err)
	}
	if !strings.Contains(res.Out, "-n") {
		t.Errorf("the usage line does not name -n: %q", res.Out)
	}
}
