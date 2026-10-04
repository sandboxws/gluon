//go:build integration

package repl

import (
	"strings"
	"testing"
)

// TestCompletesStructLiteralFields is the v5 roadmap item: a struct declared
// two lines ago completes its own fields inside a literal.
func TestCompletesStructLiteralFields(t *testing.T) {
	c, err := NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	defer c.Close()

	if res := c.Submit(`type Point struct { X, Y int; Label string }`); res.Err {
		t.Fatalf("setup failed: %s", res.Out)
	}
	if res := c.Submit(`func (p Point) Dist() int { return p.X*p.X + p.Y*p.Y }`); res.Err {
		t.Fatalf("setup failed: %s", res.Out)
	}

	got := c.Complete("p := Point{")
	if len(got) == 0 {
		t.Fatal("no completions inside a struct literal")
	}
	joined := strings.Join(got, " ")
	for _, want := range []string{"X: ", "Y: ", "Label: "} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %v", want, got)
		}
	}
	// A method is not a field. Offering Dist here would offer a mistake.
	if strings.Contains(joined, "Dist") {
		t.Errorf("offered a method inside a composite literal: %v", got)
	}

	// A field already set is not offered again.
	got = c.Complete("p := Point{X: 1, ")
	for _, g := range got {
		if strings.HasSuffix(g, "X: ") {
			t.Errorf("offered X twice: %v", got)
		}
	}

	// Ordinary completion still works outside a literal.
	if got := c.Complete("Poi"); len(got) == 0 {
		t.Error("literal handling broke ordinary identifier completion")
	}
}
