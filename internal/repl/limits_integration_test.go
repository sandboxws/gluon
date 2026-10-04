//go:build integration

// These tests actually invoke the Go toolchain. Run with:
//
//	go test -tags=integration ./internal/repl/
package repl

import (
	"strings"
	"testing"
)

// The line whose truncation the setting is about: 300 elements against a
// default of 200.
// ellipsis is how the omission is drawn in the form a list takes by default.
// The count itself is on the wire; what is asserted here is the user-visible
// half, because that is what the setting is for.
const ellipsis = "\u2026"

const threeHundredItems = `func() []int { xs := make([]int, 300); for i := range xs { xs[i] = i }; return xs }()`

// TestSettingsItemLimitChangesWhatALineShows is the capability end to end:
// :settings writes a setting, applyLive hands it to the evaluator, the
// evaluator hands it to the child in its environment, and the child describes
// more of the collection. Returning the setting to its default puts the
// truncation back.
func TestSettingsItemLimitChangesWhatALineShows(t *testing.T) {
	tempConfig(t, "")
	c, err := NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	t.Cleanup(func() { c.Close() })

	// The default truncates, and says so.
	res := c.Submit(threeHundredItems)
	if res.Err {
		t.Fatalf("evaluating at the default: %s", res.Out)
	}
	if !strings.Contains(res.Out, ellipsis) {
		t.Errorf("at the default the omission is not reported:\n%s", res.Out)
	}
	c.Submit(":undo")

	if r := c.settingsCmd("value.items 300"); r.Err {
		t.Fatalf(":settings value.items 300: %s", r.Out)
	}

	raised := c.Submit(threeHundredItems)
	if raised.Err {
		t.Fatalf("evaluating at items=300: %s", raised.Out)
	}
	if strings.Contains(raised.Out, ellipsis) {
		t.Errorf("at items=300 the collection is still truncated:\n%s", raised.Out)
	}
	// The cache is keyed on program text, so the same line under two limits
	// has to be two entries. Getting the truncated answer back here is the
	// failure the key exists to prevent.
	if raised.Out == res.Out {
		t.Error("the raised limit was served the truncated answer")
	}
	c.Submit(":undo")

	if r := c.settingsCmd("value.items -"); r.Err {
		t.Fatalf(":settings value.items -: %s", r.Out)
	}

	back := c.Submit(threeHundredItems)
	if back.Err {
		t.Fatalf("evaluating back at the default: %s", back.Out)
	}
	if !strings.Contains(back.Out, ellipsis) {
		t.Errorf("returning the setting to its default did not put the truncation back:\n%s", back.Out)
	}
}

// TestInspectOneShotLimitFillsTheView. The command whose whole purpose is
// looking at a large collection can look at all of it, and the session's own
// bound is unchanged afterwards.
func TestInspectOneShotLimitFillsTheView(t *testing.T) {
	tempConfig(t, "")
	c, err := NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	t.Cleanup(func() { c.Close() })

	if res := c.Submit("xs := " + threeHundredItems); res.Err {
		t.Fatalf("binding the slice: %s", res.Out)
	}

	// At the session's bound the view holds a prefix and says so.
	bounded := c.inspect("xs")
	if bounded.Err {
		t.Fatalf(":inspect xs: %s", bounded.Out)
	}
	if bounded.Modal == nil {
		t.Fatal(":inspect opened no modal for a 300-element slice")
	}
	if n := len(bounded.Modal.Rows); n != 200 {
		t.Errorf("the view holds %d rows at the default, want 200", n)
	}
	if bounded.Modal.Note == "" {
		t.Error("the view holds a prefix and does not say so")
	}

	// With -n it holds all of them, and reports no shortfall.
	whole := c.inspect("-n 300 xs")
	if whole.Err {
		t.Fatalf(":inspect -n 300 xs: %s", whole.Out)
	}
	if whole.Modal == nil {
		t.Fatal(":inspect -n 300 opened no modal")
	}
	if n := len(whole.Modal.Rows); n != 300 {
		t.Errorf("the view holds %d rows at -n 300, want 300", n)
	}
	if whole.Modal.Note != "" {
		t.Errorf("the view holds every element and still reports a shortfall: %q", whole.Modal.Note)
	}
	if !strings.Contains(whole.Modal.Title, "300 rows") {
		t.Errorf("the title is %q, want it to report 300 rows", whole.Modal.Title)
	}

	// One-shot: the next ordinary line is bounded as before. The transient's
	// cache key carried the raised limit, so the ordinary entry was never
	// poisoned by it.
	after := c.Submit("xs")
	if after.Err {
		t.Fatalf("evaluating xs after :inspect -n: %s", after.Out)
	}
	if !strings.Contains(after.Out, ellipsis) {
		t.Errorf(":inspect -n left the raised limit in force:\n%s", after.Out)
	}
	if items, _ := c.ev.Limits(); items != 200 {
		t.Errorf("the session's item limit is %d after :inspect -n, want 200", items)
	}
}
