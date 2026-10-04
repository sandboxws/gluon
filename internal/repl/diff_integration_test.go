//go:build integration

package repl

import (
	"strings"
	"testing"
)

// The unit tests compare trees a test built. These compare two values a real
// child process described, which is the only way to find out whether the two
// halves of the contract — what the encoder writes and what the walk reads —
// actually meet.

// TestDiffLeavesTheSessionUnchanged. Invariant 14: :diff evaluates a
// synthesized expression through EvalTransient, and neither the entry nor the
// imports it needed may survive it.
func TestDiffLeavesTheSessionUnchanged(t *testing.T) {
	c := testCore(t)
	for _, line := range []string{
		`type User struct { Name string; Age int }`,
		`want := User{Name: "ana", Age: 30}`,
		`got := User{Name: "bob", Age: 30}`,
	} {
		if res := c.Submit(line); res.Err {
			t.Fatalf("setup %q: %s", line, res.Out)
		}
	}
	entries, imports := len(c.sess.Entries), importPaths(c)

	res := c.Submit(":diff want, got")
	if res.Err {
		t.Fatalf(":diff want, got: %s", res.Out)
	}
	if got := len(c.sess.Entries); got != entries {
		t.Errorf("session has %d entries, want %d", got, entries)
	}
	if got := importPaths(c); got != imports {
		t.Errorf("imports are now %q, were %q", got, imports)
	}
	if res := c.Submit("want"); res.Err {
		t.Errorf("the session no longer evaluates: %s", res.Out)
	}
}

// TestDiffReportsOnlyWhatDiffers, against values the encoder actually produced.
func TestDiffReportsOnlyWhatDiffers(t *testing.T) {
	c := testCore(t)
	for _, line := range []string{
		`type User struct { Name string; Age int; Tags []string }`,
		`want := User{Name: "ana", Age: 30, Tags: []string{"x"}}`,
		`got := User{Name: "bob", Age: 30, Tags: []string{"x", "y"}}`,
	} {
		if res := c.Submit(line); res.Err {
			t.Fatalf("setup %q: %s", line, res.Out)
		}
	}

	res := c.Submit(":diff want, got")
	if res.Err {
		t.Fatalf("%s", res.Out)
	}
	for _, want := range []string{".Name", `"ana"`, `"bob"`, ".Tags", "length", ".Tags[1]", `"y"`} {
		if !strings.Contains(res.Out, want) {
			t.Errorf("the report lacks %q:\n%s", want, res.Out)
		}
	}
	if strings.Contains(res.Out, ".Age") {
		t.Errorf("the matching field was listed:\n%s", res.Out)
	}
}

// TestDiffOnIdenticalValuesSaysSo, end to end.
func TestDiffOnIdenticalValuesSaysSo(t *testing.T) {
	c := testCore(t)
	if res := c.Submit(`xs := []int{1, 2, 3}`); res.Err {
		t.Fatalf("setup: %s", res.Out)
	}
	res := c.Submit(":diff xs, xs")
	if res.Err {
		t.Fatalf("%s", res.Out)
	}
	if !strings.Contains(res.Out, "identical") {
		t.Errorf("the report does not say they are identical:\n%s", res.Out)
	}
}

// TestDiffOfDifferentTypesNamesBoth, end to end. The two values are boxed into
// any to travel together, and the encoder describes what each box holds — so
// the types the report names are the concrete ones, not interface{} twice.
func TestDiffOfDifferentTypesNamesBoth(t *testing.T) {
	c := testCore(t)
	for _, line := range []string{`n := 42`, `s := "42"`} {
		if res := c.Submit(line); res.Err {
			t.Fatalf("setup %q: %s", line, res.Out)
		}
	}
	res := c.Submit(":diff n, s")
	if res.Err {
		t.Fatalf("%s", res.Out)
	}
	for _, want := range []string{"types differ", "int", "string"} {
		if !strings.Contains(res.Out, want) {
			t.Errorf("the report lacks %q:\n%s", want, res.Out)
		}
	}
}

// TestDiffOfCappedCollectionsSaysSoHonestly: past the encoder's item cap the
// comparison is of two prefixes, and the report says which.
func TestDiffOfCappedCollectionsSaysSoHonestly(t *testing.T) {
	c := testCore(t)
	for _, line := range []string{
		`big := make([]int, 500)`,
		`other := make([]int, 500)`,
	} {
		if res := c.Submit(line); res.Err {
			t.Fatalf("setup %q: %s", line, res.Out)
		}
	}
	res := c.Submit(":diff big, other")
	if res.Err {
		t.Fatalf("%s", res.Out)
	}
	if !strings.Contains(res.Out, "only the part that was described") {
		t.Errorf("a comparison of two capped values overclaims:\n%s", res.Out)
	}
}
