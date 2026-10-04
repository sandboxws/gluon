package render

import (
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/session"
)

// pinnedSession builds a session and pins the entries at the given indices,
// without going through PinBlockers — these tests are about what rendering
// does with a pin, not about which pins are allowed.
func pinnedSession(t *testing.T, pin []int, srcs ...string) *session.Session {
	t.Helper()
	s := sessionOf(t, srcs...)
	for _, i := range pin {
		s.Entries[i].Pinned = true
	}
	return s
}

func TestPinnedEntryContributesNoCode(t *testing.T) {
	s := pinnedSession(t, []int{0}, `println("side effect")`, `1 + 1`)
	out, err := Main(s, nil)
	if err != nil {
		t.Fatalf("Main: %v", err)
	}
	if strings.Contains(out, "side effect") {
		t.Errorf("pinned entry still in the program:\n%s", out)
	}
	if !strings.Contains(out, "1 + 1") {
		t.Errorf("unpinned entry missing:\n%s", out)
	}
}

func TestPinnedDeclarationStillHoists(t *testing.T) {
	// A declaration cannot be pinned through the command, but rendering must
	// not silently drop one if it ever is: the rest of the session needs it to
	// compile. PinBlockers is what enforces the rule; this pins the guarantee
	// that the two agree about which kinds are in play.
	s := sessionOf(t, `type Point struct { X int }`, `Point{1}`)
	if got := PinBlockers(s, 0); len(got) == 0 {
		t.Fatal("PinBlockers allowed a declaration to be pinned")
	}
}

func TestPinnedEntryDoesNotBindItsNames(t *testing.T) {
	// The pinned entry's `_ = x` suppression must go with it, or the program
	// references a variable that no longer exists.
	s := pinnedSession(t, []int{0}, `x := 1`, `2 + 2`)
	out, err := Main(s, nil)
	if err != nil {
		t.Fatalf("Main: %v", err)
	}
	if strings.Contains(out, "_ = x") {
		t.Errorf("pinned entry left its unused-suppression behind:\n%s", out)
	}
}

func TestPinBlockedByLaterUseOfBinding(t *testing.T) {
	s := sessionOf(t, `x := 1`, `x + 1`)
	why := PinBlockers(s, 0)
	if len(why) == 0 {
		t.Fatal("pinning a binding a later entry reads was allowed")
	}
	if !strings.Contains(why[0], "x is used by entry 2") {
		t.Errorf("blocker does not name the entry: %q", why[0])
	}
}

func TestPinBlockedByLaterUseOfOrdinal(t *testing.T) {
	s := sessionOf(t, `2 + 2`, `_1 * 10`)
	why := PinBlockers(s, 0)
	if len(why) == 0 {
		t.Fatal("pinning a value a later entry addresses was allowed")
	}
	if !strings.Contains(why[0], "_1") {
		t.Errorf("blocker does not name the ordinal: %q", why[0])
	}
}

func TestPinBlockedByIt(t *testing.T) {
	s := sessionOf(t, `2 + 2`, `it + 1`)
	if len(PinBlockers(s, 0)) == 0 {
		t.Fatal("pinning the value `it` names was allowed")
	}
}

func TestPinAllowedWhenNothingDependsOnIt(t *testing.T) {
	s := sessionOf(t, `x := 1`, `y := 2`, `y + 1`)
	if why := PinBlockers(s, 0); len(why) != 0 {
		t.Errorf("pinning an unreferenced binding was refused: %v", why)
	}
}

func TestPinAllowedAfterRedeclaration(t *testing.T) {
	// Once a later entry declares the same name, the pinned entry's binding is
	// dead and reading the name afterwards is reading the newer one.
	s := sessionOf(t, `x := 1`, `x := 2`, `x + 1`)
	if why := PinBlockers(s, 0); len(why) != 0 {
		t.Errorf("pinning a shadowed binding was refused: %v", why)
	}
}

func TestPinSelectorBaseCountsAsUse(t *testing.T) {
	// In p.X the p is a reference; only the X is a field name.
	s := sessionOf(t, `p := struct{ X int }{1}`, `p.X`)
	if len(PinBlockers(s, 0)) == 0 {
		t.Fatal("the base of a selector was not counted as a use")
	}
}

func TestPinIgnoresFieldNames(t *testing.T) {
	// A field called `x` in a composite literal is not a reference to the
	// variable `x`, so the pin is allowed.
	s := sessionOf(t, `x := 1`, `struct{ x int }{x: 2}`)
	why := PinBlockers(s, 0)
	if len(why) != 0 {
		t.Errorf("a struct-literal key was mistaken for a use: %v", why)
	}
}

func TestPinOutOfRange(t *testing.T) {
	s := sessionOf(t, `1 + 1`)
	if len(PinBlockers(s, 5)) == 0 {
		t.Fatal("an out-of-range index was allowed")
	}
}

func TestUnpinnedSessionRendersUnchanged(t *testing.T) {
	// The program text is what the result cache is keyed on, so a session with
	// no pins must render exactly as it did before pinning existed.
	srcs := []string{`x := 1`, `x + 1`}
	want := build(t, srcs...)
	s := pinnedSession(t, nil, srcs...)
	got, err := Main(s, nil)
	if err != nil {
		t.Fatalf("Main: %v", err)
	}
	if got != want {
		t.Errorf("pin support changed the bytes of an unpinned session:\n%s", got)
	}
}
