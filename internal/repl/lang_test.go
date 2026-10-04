package repl

import (
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/syntax"
)

// TestResultOutIsNeverPainted is invariant 21 at this layer.
//
// Result.Out is what a pipe, `gluon -e`, the -json envelopes and every MCP tool
// read. Core runs on a background goroutine and must not know what a terminal
// can do, so it tags the language and paints nothing. The escape sequences are
// added by the TUI on the way to tea.Println, and nowhere else.
func TestResultOutIsNeverPainted(t *testing.T) {
	withPalette(t)
	c := testCore(t)
	for _, line := range []string{
		":src", ":hist", ":ls", ":help", ":plugins", ":t 1", ":conf", ":env",
		":settings", ":settings value.form", ":scratch",
	} {
		res := c.Submit(line)
		if strings.ContainsRune(res.Out, 0x1b) {
			t.Errorf("%s put escape sequences in Result.Out: %q", line, res.Out)
		}
		if res.Modal != nil && strings.ContainsRune(res.Modal.Text, 0x1b) {
			t.Errorf("%s put escape sequences in Modal.Text", line)
		}
	}
}

// TestSourceCommandsAreTagged: a command that answers in source has to say so,
// or its answer is printed grey and the tag is dead weight.
func TestSourceCommandsAreTagged(t *testing.T) {
	c := testCore(t)
	res := c.Submit(":src")
	if res.Lang != syntax.Go {
		t.Errorf(":src answered with Lang %q, want %q", res.Lang, syntax.Go)
	}
}

// TestPageableCarriesTheLanguage: Out and Modal.Text are the same bytes, so
// they must be the same language too — a pager that painted a different
// language from the line above it would be gluon disagreeing with itself.
func TestPageableCarriesTheLanguage(t *testing.T) {
	long := strings.Repeat("x := 1\n", 40)
	if got := pageableIn("t", long, syntax.Go); got == nil || got.Lang != syntax.Go {
		t.Errorf("pageableIn dropped the language")
	}
	if got := pageable("t", long); got == nil || got.Lang != syntax.None {
		t.Errorf("pageable invented a language")
	}
	if got := sourceResult("t", long, syntax.Go); got.Lang != got.Modal.Lang {
		t.Errorf("sourceResult let Out and Modal disagree: %q vs %q", got.Lang, got.Modal.Lang)
	}
}
