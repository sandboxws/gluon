package repl

import (
	"strings"
	"testing"
)

// These drive the commands rather than the renderer: what the user is told is
// as much of the feature as what the program ends up containing.

func TestPinRefusesAndNamesTheEntryInTheWay(t *testing.T) {
	c := testCore(t)
	c.Submit("x := 1")
	c.Submit("x + 1")

	res := c.Submit(":pin 1")
	if !res.Err {
		t.Fatalf("pinning a binding a later entry reads was allowed: %q", res.Out)
	}
	if !strings.Contains(res.Out, "entry 2") {
		t.Errorf("refusal does not say which entry is in the way: %q", res.Out)
	}
}

func TestPinMarksTheTranscript(t *testing.T) {
	c := testCore(t)
	c.Submit("x := 1")
	c.Submit("y := 2")

	if res := c.Submit(":pin 2"); res.Err {
		t.Fatalf(":pin 2: %s", res.Out)
	}
	hist := c.Submit(":hist").Out
	if !strings.Contains(hist, "pinned") {
		t.Errorf(":hist does not mark the pinned entry:\n%s", hist)
	}
	// The entry is out of the program, so :src must not show it.
	if src := c.Submit(":src").Out; strings.Contains(src, "y := 2") {
		t.Errorf(":src still contains the pinned entry:\n%s", src)
	}
}

func TestUnpinPutsItBack(t *testing.T) {
	c := testCore(t)
	c.Submit("y := 2")
	if res := c.Submit(":pin 1"); res.Err {
		t.Fatalf(":pin 1: %s", res.Out)
	}
	if res := c.Submit(":unpin 1"); res.Err {
		t.Fatalf(":unpin 1: %s", res.Out)
	}
	if src := c.Submit(":src").Out; !strings.Contains(src, "y := 2") {
		t.Errorf("unpinned entry did not come back:\n%s", src)
	}
}

func TestBarePinListsWhatIsPinned(t *testing.T) {
	c := testCore(t)
	if out := c.Submit(":pin").Out; !strings.Contains(out, "nothing is pinned") {
		t.Errorf("bare :pin on a clean session: %q", out)
	}
	c.Submit("y := 2")
	c.Submit(":pin 1")
	if out := c.Submit(":pin").Out; !strings.Contains(out, "y := 2") {
		t.Errorf("bare :pin does not list the pinned entry: %q", out)
	}
}

// The name is still visible in :hist, so an `undefined` with no explanation
// reads like a bug in gluon rather than a consequence of the pin.
func TestUsingAPinnedBindingExplainsItself(t *testing.T) {
	c := testCore(t)
	c.Submit("x := 1")
	if res := c.Submit(":pin 1"); res.Err {
		t.Fatalf(":pin 1: %s", res.Out)
	}
	res := c.Submit("x + 1")
	if !res.Err {
		t.Fatal("referencing a pinned binding compiled")
	}
	if !strings.Contains(res.Out, "pinned") || !strings.Contains(res.Out, ":unpin 1") {
		t.Errorf("error does not explain the pin or how to undo it:\n%s", res.Out)
	}
}

func TestPinRejectsADeclaration(t *testing.T) {
	c := testCore(t)
	c.Submit("type Point struct { X int }")
	res := c.Submit(":pin 1")
	if !res.Err {
		t.Fatalf("pinning a declaration was allowed: %q", res.Out)
	}
	if !strings.Contains(res.Out, "runs nothing") {
		t.Errorf("refusal does not say why a declaration cannot be pinned: %q", res.Out)
	}
}

func TestPinOutOfRangeSaysSo(t *testing.T) {
	c := testCore(t)
	c.Submit("x := 1")
	res := c.Submit(":pin 7")
	if !res.Err || !strings.Contains(res.Out, "no entry 7") {
		t.Errorf("out-of-range :pin: %q", res.Out)
	}
}
