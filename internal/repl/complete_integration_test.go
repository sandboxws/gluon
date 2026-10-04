//go:build integration

package repl

import (
	"strings"
	"testing"
)

func hasLine(got []string, want string) bool {
	for _, g := range got {
		if g == want {
			return true
		}
	}
	return false
}

// Completion runs against the session's own type information, so it has to be
// exercised against a real one rather than a fixture: the whole point is that
// `p.` knows about a struct the user declared two lines ago.
func TestCompleteAgainstALiveSession(t *testing.T) {
	c, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	for _, line := range []string{
		"type Point struct { X, Y int }",
		"func (p Point) Dist() int { return p.X*p.X + p.Y*p.Y }",
		"p := Point{3, 4}",
		"count := 7",
	} {
		if res := c.Submit(line); res.Err {
			t.Fatalf("%q: %s", line, res.Out)
		}
	}

	t.Run("bound names", func(t *testing.T) {
		got := c.Complete("cou")
		if !hasLine(got, "count") {
			t.Errorf("a bound variable was not offered: %v", got)
		}
	})

	t.Run("declared types and funcs", func(t *testing.T) {
		if got := c.Complete("Poi"); !hasLine(got, "Point") {
			t.Errorf("a declared type was not offered: %v", got)
		}
	})

	t.Run("fields and methods of a value", func(t *testing.T) {
		got := c.Complete("p.")
		for _, want := range []string{"p.X", "p.Y", "p.Dist"} {
			if !hasLine(got, want) {
				t.Errorf("missing %q in %v", want, got)
			}
		}
	})

	t.Run("mid-line", func(t *testing.T) {
		got := c.Complete("fmt.Println(p.D")
		if !hasLine(got, "fmt.Println(p.Dist") {
			t.Errorf("Complete = %v", got)
		}
	})

	t.Run("stdlib package members", func(t *testing.T) {
		// The stdlib index is built in the background; wait for it the way the
		// UI would, by asking again.
		var got []string
		for range 100 {
			if got = c.Complete("strings.ToU"); len(got) > 0 {
				break
			}
		}
		if !hasLine(got, "strings.ToUpper") {
			t.Errorf("Complete(\"strings.ToU\") = %v", got)
		}
	})

	t.Run("meta commands", func(t *testing.T) {
		got := c.Complete(":sl")
		if !hasLine(got, ":slice") {
			t.Errorf("Complete(\":sl\") = %v", got)
		}
	})

	t.Run("every suggestion extends the line", func(t *testing.T) {
		// textinput appends whatever extends past what was typed, so a
		// suggestion that is not a true prefix extension corrupts the line.
		for _, line := range []string{"cou", "p.", "p.D", "strings.To", ":sl", "fmt.Println(p."} {
			for _, s := range c.Complete(line) {
				if !strings.HasPrefix(s, line) {
					t.Errorf("Complete(%q) offered %q, which does not extend it", line, s)
				}
			}
		}
	})
}

// A name the session no longer binds must stop being offered, or completion
// writes a line that does not compile.
func TestCompletionFollowsTheSession(t *testing.T) {
	c, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if res := c.Submit("gone := 1"); res.Err {
		t.Fatal(res.Out)
	}
	if got := c.Complete("go"); !hasLine(got, "gone") {
		t.Fatalf("the binding was not offered: %v", got)
	}
	if res := c.Submit(":undo"); res.Err {
		t.Fatal(res.Out)
	}
	if got := c.Complete("go"); hasLine(got, "gone") {
		t.Errorf("an undone binding is still offered: %v", got)
	}
}

// Completion must never mutate the session it inspects — Fields type-checks
// with the expression appended, which is exactly the operation that could.
func TestCompletionDoesNotDisturbTheSession(t *testing.T) {
	c, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	for _, line := range []string{"s := \"hi\"", "n := 2"} {
		if res := c.Submit(line); res.Err {
			t.Fatalf("%q: %s", line, res.Out)
		}
	}
	before := len(c.sess.Entries)
	src, err := c.source()
	if err != nil {
		t.Fatal(err)
	}

	for _, line := range []string{"s.", "n.", "strings.To", "unknown.", "s.Inde"} {
		c.Complete(line)
	}

	if got := len(c.sess.Entries); got != before {
		t.Errorf("the session grew from %d to %d entries", before, got)
	}
	after, err := c.source()
	if err != nil {
		t.Fatal(err)
	}
	if after != src {
		t.Errorf("the rendered program changed:\n--- before ---\n%s\n--- after ---\n%s", src, after)
	}
	// And the session still evaluates.
	if res := c.Submit("s + \"!\""); res.Err {
		t.Fatalf("the session broke: %s", res.Out)
	}
}
