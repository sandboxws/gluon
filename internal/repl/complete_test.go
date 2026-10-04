package repl

import (
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/session"
)

// bind appends an entry to the session without evaluating it. It is what a
// submit does to the session minus the build, which is what these tests are
// about: the caches are keyed on the generation, not on the child process.
func bind(t *testing.T, c *Core, lines ...string) {
	t.Helper()
	for _, line := range lines {
		e, err := session.Classify(line)
		if err != nil {
			t.Fatalf("classify %q: %v", line, err)
		}
		c.sess.Append(e)
	}
}

// TestArgumentCacheResolvesOncePerCallee is the keystroke budget as a fact
// about the cache: moving from one argument of a call to the next must not ask
// the checker again, because the signature and the scope it already holds are
// enough to answer.
func TestArgumentCacheResolvesOncePerCallee(t *testing.T) {
	c := testCore(t)
	bind(t, c, `s := "hi"`, "n := 3")

	sig, cands := c.completionArguments("strings.Repeat", 0)
	if !strings.Contains(sig, "[s string]") {
		t.Fatalf("signature = %q, want the first parameter marked", sig)
	}
	if len(cands) != 1 || cands[0] != "s" {
		t.Fatalf("candidates at 0 = %v, want [s]", cands)
	}
	first := c.comp.args["strings.Repeat"]
	if first == nil {
		t.Fatal("the callee was not cached")
	}

	// The next keystroke is the next argument. The index picks a different
	// parameter out of the same check.
	sig, cands = c.completionArguments("strings.Repeat", 1)
	if !strings.Contains(sig, "[count int]") {
		t.Errorf("signature = %q, want the second parameter marked", sig)
	}
	if len(cands) != 1 || cands[0] != "n" {
		t.Errorf("candidates at 1 = %v, want [n]", cands)
	}
	if c.comp.args["strings.Repeat"] != first {
		t.Error("the second keystroke inside one call type-checked again")
	}

	// A keystroke inside a call asks twice: once for the candidates and once
	// for the hint beside them. The second ask reads the answer the first one
	// wrote rather than walking the scope again.
	if _, ok := c.comp.argsAt[argKey{callee: "strings.Repeat", index: 1}]; !ok {
		t.Error("the answer for one argument position was not kept")
	}

	// A submit moves the generation, and a callee re-declared with a different
	// signature must not be completed against the old one.
	c.gen++
	if _, _ = c.completionArguments("strings.Repeat", 0); c.comp.args["strings.Repeat"] == first {
		t.Error("a submit did not clear the cached callee")
	}
}

// TestArgumentCacheKeepsAFailedLookup: the checker has already said it cannot
// answer, and asking again on every keystroke would only say it again.
func TestArgumentCacheKeepsAFailedLookup(t *testing.T) {
	c := testCore(t)
	if sig, cands := c.completionArguments("nosuchthing", 0); sig != "" || cands != nil {
		t.Fatalf("Arguments = %q, %v; want silence", sig, cands)
	}
	if _, ok := c.comp.args["nosuchthing"]; !ok {
		t.Error("a failed lookup was not cached, so the next keystroke repeats it")
	}
}

// TestCompletionCachesShareOneGeneration: a submitted line can change a type's
// fields, an expression's members and a callee's signature at once, so the
// three go stale together.
func TestCompletionCachesShareOneGeneration(t *testing.T) {
	c := &Core{}
	c.fresh()
	c.comp.fields["x"] = []string{"stale"}
	c.comp.literal["Point"] = []string{"stale"}
	c.comp.args["f"] = nil
	c.comp.argsAt[argKey{callee: "f"}] = argsAnswer{sig: "stale"}

	c.fresh()
	if len(c.comp.fields) != 1 || len(c.comp.literal) != 1 ||
		len(c.comp.args) != 1 || len(c.comp.argsAt) != 1 {
		t.Error("the caches were dropped without the session changing")
	}

	c.gen++
	c.fresh()
	if len(c.comp.fields) != 0 || len(c.comp.literal) != 0 ||
		len(c.comp.args) != 0 || len(c.comp.argsAt) != 0 {
		t.Errorf("a submit left %d fields, %d literals, %d callees and %d positions behind",
			len(c.comp.fields), len(c.comp.literal), len(c.comp.args), len(c.comp.argsAt))
	}
}

// TestNamesDoNotMarkTheLookupCachesCurrent is why names has a generation of its
// own. Complete fills it eagerly, before it asks for a field or a callee: one
// shared field would let that write mark the lookup caches current a moment
// before they are read, and they would then never be dropped at all.
func TestNamesDoNotMarkTheLookupCachesCurrent(t *testing.T) {
	c := testCore(t)
	c.fresh()
	c.comp.fields["x"] = []string{"stale"}

	c.gen++
	c.completionNames()
	c.fresh()
	if len(c.comp.fields) != 0 {
		t.Error("reading the names left a stale field list behind")
	}
}

// TestHintFollowsTheSameGatesAsCompletion is invariant 15 at the keystroke:
// nothing asks Core while an evaluation is running, and nothing asks with the
// cursor anywhere but the end of the line, because textinput would append an
// accepted suggestion there regardless.
func TestHintFollowsTheSameGatesAsCompletion(t *testing.T) {
	c := testCore(t)
	bind(t, c, `s := "hi"`)

	base := newModel(c, "test")
	base.hist = &history{}
	base.winW, base.winH = 120, 40

	typed := func(m model, line string, pos int) model {
		m.in.SetValue(line)
		m.in.SetCursor(pos)
		return m.suggest()
	}

	m := typed(base, "strings.Repeat(", 15)
	if m.hint == "" {
		t.Fatal("no hint inside an open call")
	}
	if !strings.Contains(m.hint, "[s string]") {
		t.Errorf("hint = %q, want the first parameter marked", m.hint)
	}

	// The hint is not a candidate. Whatever the suggestion list holds, the
	// signature is not in it — so tab cannot type it.
	for _, s := range m.in.AvailableSuggestions() {
		if strings.Contains(s, "[s string]") {
			t.Errorf("the hint reached the suggestion list: %q", s)
		}
	}

	// The cursor moved off the end: this is no longer a place anything is
	// offered, and the hint goes with the offer.
	if got := typed(m, "strings.Repeat(", 3); got.hint != "" {
		t.Errorf("hint = %q with the cursor mid-line", got.hint)
	}

	// Busy. Core is not safe to touch while an evaluation runs, so nothing is
	// asked and the hint stays where the last answer left it — empty, because
	// submitting cleared it.
	busy := base
	busy.busy = true
	if got := typed(busy, "strings.Repeat(", 15); got.hint != "" {
		t.Errorf("hint = %q while busy", got.hint)
	}

	// Closing the call closes the hint.
	if got := typed(base, `strings.Repeat("a", 2)`, 22); got.hint != "" {
		t.Errorf("hint = %q after the call closed", got.hint)
	}
}

// TestSubmitClearsTheHint: the hint describes the line just submitted, and the
// session it was read from is about to change.
func TestSubmitClearsTheHint(t *testing.T) {
	c := testCore(t)
	m := newModel(c, "test")
	m.hist = &history{}
	m.winW, m.winH = 120, 40

	m.in.SetValue("strings.Repeat(")
	m.in.SetCursor(15)
	m = m.suggest()
	if m.hint == "" {
		t.Fatal("no hint to clear, so this asserts nothing")
	}

	next, _ := m.submit()
	if next.hint != "" {
		t.Errorf("hint = %q after submitting", next.hint)
	}
}
