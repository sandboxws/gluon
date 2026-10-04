//go:build integration

// These tests actually invoke the Go toolchain. Run with:
//
//	go test -tags=integration ./internal/eval/
package eval

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/pretty"
	"github.com/sandboxws/gluon/internal/session"
)

// omitted reports the count the child said it left out of the top-level value.
// It is the observation the whole capability is about: the wire's m field
// already carried it, so nothing new had to be sent to see a limit move.
func omitted(t *testing.T, raw string) int {
	t.Helper()
	_, vals := pretty.Parse(raw)
	if len(vals) == 0 {
		t.Fatalf("no value in %q", raw)
	}
	return vals[0].More
}

// evalLine appends src to s and evaluates it, returning the child's output.
func evalLine(t *testing.T, ev *Evaluator, s *session.Session, src string) string {
	t.Helper()
	e, err := session.Classify(src)
	if err != nil {
		t.Fatalf("Classify(%q): %v", src, err)
	}
	s.Append(e)
	res, err := ev.Eval(s)
	if err != nil {
		s.Pop()
		t.Fatalf("Eval(%q): %v", src, err)
	}
	return res.Output
}

func newEvaluator(t *testing.T) *Evaluator {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	ev, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ev.Close() })
	return ev
}

const threeHundred = `func() []int { xs := make([]int, 300); for i := range xs { xs[i] = i }; return xs }()`

// TestItemLimitReachesTheChild is the whole path end to end: the setting sets
// an evaluator field, the field becomes an environment variable, and the child
// parses it and describes more of the collection.
//
// The third evaluation is the one the cache key exists for. At the default
// again, the answer must be the default's answer — not the 300-item one that
// the same program text produced a moment earlier.
func TestItemLimitReachesTheChild(t *testing.T) {
	ev := newEvaluator(t)
	s := &session.Session{}

	if got := omitted(t, evalLine(t, ev, s, threeHundred)); got != 100 {
		t.Errorf("at the default: omitted %d, want 100", got)
	}
	s.Pop()

	ev.SetLimits(300, 0)
	if got := omitted(t, evalLine(t, ev, s, threeHundred)); got != 0 {
		t.Errorf("at items=300: omitted %d, want 0", got)
	}
	s.Pop()

	ev.SetLimits(200, 0)
	if got := omitted(t, evalLine(t, ev, s, threeHundred)); got != 100 {
		t.Errorf("back at the default: omitted %d, want 100 — the cache served the raised limit's entry", got)
	}
}

// A raise between two identical lines must be seen. The cache is keyed on
// program text, and without the limit in the key the second evaluation would
// be answered from the first.
func TestRaisingTheLimitIsNotAnsweredFromTheCache(t *testing.T) {
	ev := newEvaluator(t)
	s := &session.Session{}

	first := evalLine(t, ev, s, threeHundred)
	s.Pop()
	ev.SetLimits(300, 0)
	second := evalLine(t, ev, s, threeHundred)

	if first == second {
		t.Error("the raised limit was served the truncated answer")
	}
	if got := omitted(t, second); got != 0 {
		t.Errorf("omitted %d after the raise, want 0", got)
	}
}

func TestDepthLimitReachesTheChild(t *testing.T) {
	ev := newEvaluator(t)
	s := &session.Session{}

	const decl = `type deep struct { N int; X []deep }`
	const nest = `func() deep { v := deep{N: 0}; for i := 1; i < 8; i++ { v = deep{N: i, X: []deep{v}} }; return v }()`

	evalLine(t, ev, s, decl)
	shallow := evalLine(t, ev, s, nest)
	s.Pop()

	ev.SetLimits(0, 20)
	deeper := evalLine(t, ev, s, nest)

	// Deeper means more of the value described structurally rather than as one
	// flat line, so more nodes carry a kind of their own.
	sn := strings.Count(shallow, `"k":"struct"`)
	dn := strings.Count(deeper, `"k":"struct"`)
	if dn <= sn {
		t.Errorf("depth=20 described %d struct levels, default described %d; want more", dn, sn)
	}
}

// TestProgramTextIsIdenticalAcrossLimits is the claim the design rests on: the
// numbers travel in the environment, so the program is the same program. If
// they were templated into the runtime source instead, every setting change
// would rebuild it and every :undo across one would miss the cache.
func TestProgramTextIsIdenticalAcrossLimits(t *testing.T) {
	ev := newEvaluator(t)
	s := &session.Session{}

	e, err := session.Classify(threeHundred)
	if err != nil {
		t.Fatal(err)
	}
	s.Append(e)

	base, err := ev.Eval(s)
	if err != nil {
		t.Fatal(err)
	}

	// The defaults, set explicitly rather than left alone.
	ev.SetLimits(200, 6)
	same, err := ev.Eval(s)
	if err != nil {
		t.Fatal(err)
	}
	if same.Source != base.Source {
		t.Error("SetLimits with the defaults changed the program text")
	}

	ev.SetLimits(300, 20)
	raised, err := ev.Eval(s)
	if err != nil {
		t.Fatal(err)
	}
	if raised.Source != base.Source {
		t.Error("a raised limit changed the program text; it must travel in the environment, not the source")
	}
}
