//go:build integration

package repl

import (
	"regexp"
	"strings"
	"testing"
)

// TestTimeReportsOnlyTheLine. :time's phases are collected for the whole
// package, and a signature hint type-checks the session on every keystroke
// between lines. What the hints measured belongs to no line: the account a
// line reports is the same whether or not a call was typed a character at a
// time before it.
func TestTimeReportsOnlyTheLine(t *testing.T) {
	c, err := NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	defer c.Close()
	if res := c.Submit(":time"); res.Err {
		t.Fatalf(":time: %s", res.Out)
	}
	names := func(out string) string {
		m := regexp.MustCompile(`\[(.*) · total`).FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("no phases in %q", out)
		}
		var ns []string
		for _, p := range strings.Split(m[1], " · ") {
			ns = append(ns, strings.Fields(p)[0])
		}
		return strings.Join(ns, " ")
	}
	// Twice first, so the import set is settled and the line after the hints
	// renders the program the one before them did.
	c.Submit(`strings.Repeat("ab", 1)`)
	quiet := names(c.Submit(`strings.Repeat("ab", 2)`).Out)

	typed := `strings.Repeat("ab", 3)`
	for i := 1; i < len(typed); i++ {
		c.Complete(typed[:i])
		c.Hint(typed[:i])
	}
	if got := names(c.Submit(typed).Out); got != quiet {
		t.Errorf("after hints the line reports %q; without them, %q", got, quiet)
	}
}
