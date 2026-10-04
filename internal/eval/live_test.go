package eval

import (
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/gluonrt"
	"github.com/sandboxws/gluon/internal/render"
	"github.com/sandboxws/gluon/internal/session"
)

// TestLiveEvalDoesNotRecordItsResult is half of what makes :query honest.
//
// The result cache is keyed on program text alone. `SELECT count(*) FROM users`
// renders to the same bytes every time, so an entry written once would be
// served forever — and a row count from ten minutes ago, presented as current,
// is the quiet wrongness this project rejected an interpreter to avoid.
//
// Not writing matters as much as not reading, which is what this asserts: an
// entry put during a live evaluation would be handed to a later *ordinary* Eval
// of the same text, so the staleness would outlive the query that caused it.
func TestLiveEvalDoesNotRecordItsResult(t *testing.T) {
	e := &Evaluator{cache: newResultCache(8)}
	const src = "package main // SELECT count(*) FROM users"

	e.putUnlessLive(evalOpts{live: true}, src, Result{Output: "42"})
	if _, _, ok := e.cache.get(src); ok {
		t.Fatal("a live result was written to the cache")
	}

	// The ordinary path must be untouched.
	e.putUnlessLive(evalOpts{}, src, Result{Output: "42"})
	if _, _, ok := e.cache.get(src); !ok {
		t.Fatal("an ordinary result was not cached")
	}
}

// TestLiveEvalDoesNotEvictOrdinaryEntries is why the fix is a bypass rather
// than a nonce in the source.
//
// Making each query's text unique would also work, and would fill a capped LRU
// with keys that can never be hit again — evicting the :undo history the cache
// exists for, which is the same harm invariant 8 names for addresses. Live
// evaluations must leave the cache exactly as they found it.
func TestLiveEvalDoesNotEvictOrdinaryEntries(t *testing.T) {
	e := &Evaluator{cache: newResultCache(4)}
	e.cache.put("the :undo target", Result{Output: "kept"}, nil)

	for i := 0; i < 40; i++ {
		e.putUnlessLive(evalOpts{live: true}, "query "+strings.Repeat("x", i), Result{Output: "rows"})
	}

	res, _, ok := e.cache.get("the :undo target")
	if !ok {
		t.Fatal("40 live evaluations evicted an ordinary entry")
	}
	if res.Output != "kept" {
		t.Errorf("Output = %q, want %q", res.Output, "kept")
	}
}

// TestMaskHidesEverySecret covers the one surface a secret can still reach.
//
// The generated source names an environment variable rather than holding a DSN,
// so a build error cannot leak one. What can is the child's own output: several
// drivers echo the connection string they failed to parse.
func TestMaskHidesEverySecret(t *testing.T) {
	const pw = "hunter2SuperSecret"
	const dsn = "postgres://app:" + pw + "@localhost:5432/acme"
	out := "pq: could not parse " + dsn + " (password " + pw + ")"

	got := mask(out, []string{dsn, pw})
	if strings.Contains(got, pw) {
		t.Errorf("the password survived masking: %s", got)
	}
	if strings.Contains(got, dsn) {
		t.Errorf("the connection string survived masking: %s", got)
	}
	if !strings.Contains(got, "could not parse") {
		t.Errorf("masking destroyed the message: %s", got)
	}
}

// TestMaskSkipsShortSecrets keeps the cure from being worse than the disease.
//
// A two-character password would turn every occurrence of those two characters
// in a result set into ***, shredding the answer while pretending to protect
// it. A password that short is not the failure this defends against.
func TestMaskSkipsShortSecrets(t *testing.T) {
	out := "id  name\n1   abcdef\n2   ab"
	if got := mask(out, []string{"ab"}); got != out {
		t.Errorf("a short secret was masked, mangling the output:\n%s", got)
	}
	// ...and one at the threshold still is.
	if got := mask(out, []string{"abcdef"}); strings.Contains(got, "abcdef") {
		t.Errorf("a secret at the length threshold was not masked:\n%s", got)
	}
}

// TestMaskIsFreeWhenNothingIsSecret keeps ordinary lines byte-for-byte
// identical. Every evaluation goes through run(), so a mask that rewrote output
// with no secrets registered would change what the whole REPL prints.
func TestMaskIsFreeWhenNothingIsSecret(t *testing.T) {
	const out = "(string) \"***\"  len=3"
	if got := mask(out, nil); got != out {
		t.Errorf("mask changed output with no secrets: %q", got)
	}
}

// TestMergeImportsKeepsEveryBlankImport.
//
// importName reports "_" for a blank import, so deduping by name would drop the
// second one. Go allows any number of them, and a dropped driver import fails
// at run time with `sql: unknown driver` — an error that blames the driver
// rather than the import that went missing.
func TestMergeImportsKeepsEveryBlankImport(t *testing.T) {
	base := []render.ImportSpec{{Path: "database/sql"}, {Name: "_", Path: "github.com/lib/pq"}}
	extra := []render.ImportSpec{{Name: "_", Path: "modernc.org/sqlite"}}

	got := mergeImports(base, extra)
	if len(got) != 3 {
		t.Fatalf("mergeImports = %v, want all three kept", got)
	}
	var found bool
	for _, im := range got {
		if im.Path == "modernc.org/sqlite" {
			found = true
		}
	}
	if !found {
		t.Errorf("the second blank import was dropped: %v", got)
	}
}

// TestMergeImportsStillRefusesADuplicatePath — a blank import of something
// already imported is a redeclaration the compiler rejects.
func TestMergeImportsStillRefusesADuplicatePath(t *testing.T) {
	base := []render.ImportSpec{{Name: "_", Path: "github.com/lib/pq"}}
	got := mergeImports(base, []render.ImportSpec{{Name: "_", Path: "github.com/lib/pq"}})
	if len(got) != 1 {
		t.Errorf("mergeImports = %v, want the duplicate dropped", got)
	}
}

// TestExtraImportsReachSeedImportsOnBothBranches.
//
// writeAs forks: a cached-import fast path and a full goimports pass. Both call
// seedImports, which is why the extras are merged there. If only one branch saw
// them, the driver import would appear or vanish depending on whether the
// session had already resolved its imports — an intermittent `sql: unknown
// driver` that would look like a database problem.
func TestExtraImportsReachSeedImportsOnBothBranches(t *testing.T) {
	driver := render.ImportSpec{Name: "_", Path: "github.com/lib/pq"}

	// The early-return branch: no host index, no preloads.
	bare := &Evaluator{extraImports: []render.ImportSpec{driver}}
	got, err := bare.seedImports(sessionWithNoQualifiers())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != driver.Path {
		t.Errorf("early return dropped the extra import: %v", got)
	}

	// The full branch: a preload makes seedImports do its real work.
	full := &Evaluator{
		extraImports: []render.ImportSpec{driver},
		preload:      map[string]render.ImportSpec{"strings": {Path: "strings"}},
	}
	got, err = full.seedImports(sessionWithNoQualifiers())
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, im := range got {
		if im.Path == driver.Path {
			found = true
		}
	}
	if !found {
		t.Errorf("the full branch dropped the extra import: %v", got)
	}
}

func sessionWithNoQualifiers() *session.Session {
	s := &session.Session{}
	s.Append(session.Entry{Kind: session.KindExpr, Src: "1 + 1"})
	return s
}

func TestTheWaitNoteStartsItsOwnLine(t *testing.T) {
	note := "[gluon waited 139µs for 1 goroutine(s) — a real main would have exited here]\n"
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"no note\n", "no note\n"},
		{"tail\n" + note, "tail\n" + note},
		{"tail" + note, "tail\n" + note},
		{note, note},
		{"a" + note + "b" + note, "a\n" + note + "b\n" + note},
	} {
		if got := ownLine(tc.in); got != tc.want {
			t.Errorf("ownLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestTheWaitNoteIsTheRuntimes: ownLine finds the note by how it begins, so
// the runtime has to still begin it that way.
func TestTheWaitNoteIsTheRuntimes(t *testing.T) {
	if !strings.Contains(gluonrt.Source, `"`+drainNote) {
		t.Errorf("the runtime no longer prints a note beginning %q", drainNote)
	}
}
