package db

import (
	"go/parser"
	"strings"
	"testing"
)

// TestExpandSplitsTheQueryFromItsArguments. A query is a Go expression and may
// carry a comma inside a string, a composite literal or a call, so splitting on
// commas would cut `[]int{1, 2}` in half.
func TestExpandSplitsTheQueryFromItsArguments(t *testing.T) {
	for _, tc := range []struct {
		in    string
		query string
		args  []string
	}{
		{`"SELECT 1"`, `"SELECT 1"`, nil},
		{`"SELECT * FROM t WHERE id IN (?)", []int{1, 2, 3}`,
			`"SELECT * FROM t WHERE id IN (?)"`, []string{`[]int{1, 2, 3}`}},
		{`q, a, b`, `q`, []string{`a`, `b`}},
		// A comma inside the query itself belongs to the query.
		{`"SELECT a, b FROM t WHERE x = ?", 1`, `"SELECT a, b FROM t WHERE x = ?"`, []string{`1`}},
		{`fmt.Sprintf("SELECT %s", c), 1`, `fmt.Sprintf("SELECT %s", c)`, []string{`1`}},
	} {
		query, args, err := splitExpandArgs(tc.in)
		if err != nil {
			t.Errorf("splitExpandArgs(%q): %v", tc.in, err)
			continue
		}
		if query != tc.query {
			t.Errorf("splitExpandArgs(%q) query = %q, want %q", tc.in, query, tc.query)
		}
		if strings.Join(args, "|") != strings.Join(tc.args, "|") {
			t.Errorf("splitExpandArgs(%q) args = %v, want %v", tc.in, args, tc.args)
		}
	}
}

// TestExpandRefusesASpreadArgument rather than guessing. `xs...` means the
// slice is the whole argument list; a slice passed to sqlx.In means one
// argument that expands into several placeholders. The two produce different
// queries, so this is the kind of quiet wrongness worth a refusal.
func TestExpandRefusesASpreadArgument(t *testing.T) {
	if _, err := expandRewrite(`"SELECT 1", xs...`); err == nil {
		t.Fatal("a spread argument was accepted")
	} else if !strings.Contains(err.Error(), "spread") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

func TestExpandRewriteParses(t *testing.T) {
	src, err := expandRewrite(`"SELECT * FROM users WHERE id IN (?)", []int{1, 2, 3}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.ParseExpr(src); err != nil {
		t.Fatalf("produced source that does not parse: %v\n%s", err, src)
	}
	for _, want := range []string{"sqlx.Named", "sqlx.In"} {
		if !strings.Contains(src, want) {
			t.Errorf("the rewrite does not call %s:\n%s", want, src)
		}
	}
}

// TestExpandNeverSubstitutesArgumentsIntoTheQuery is the whole reason the
// command exists in this shape. A query with its parameters already put in is
// not the query that runs, so the two halves are reported separately — the
// query as sqlx rewrote it, then the argument list on its own line.
//
// The generated program returns __q unchanged and concatenates the arguments
// after it; nothing formats the two together, which is what an interpolation
// would have to do.
func TestExpandNeverSubstitutesArgumentsIntoTheQuery(t *testing.T) {
	src, err := expandRewrite(`"SELECT * FROM users WHERE id = ? AND name = ?", 7, "Ada"`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(src, `return __q + "\nargs  " + fmt.Sprint(__a)`) {
		t.Errorf("the query and the arguments are not reported separately:\n%s", src)
	}
	// The one place the two could be woven together.
	for _, bad := range []string{"Sprintf(__q", "Replace(__q", "ReplaceAll(__q"} {
		if strings.Contains(src, bad) {
			t.Errorf("the arguments are formatted into the query text (%s):\n%s", bad, src)
		}
	}
	// The literal arguments must reach the argument slice, never the query.
	q, _, err := splitExpandArgs(`"SELECT * FROM users WHERE id = ?", 7`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(q, "7") {
		t.Errorf("an argument landed in the query text: %q", q)
	}
}

// TestExpandOpensNoConnection. sqlx.Named and sqlx.In are pure functions over
// the query text, so the generated program has no reason to reach a database —
// and if it ever did, it would need a driver gluon deliberately does not link.
func TestExpandOpensNoConnection(t *testing.T) {
	src, err := expandRewrite(`"SELECT * FROM users WHERE id IN (?)", []int{1, 2}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"sql.Open", "sqlx.Open", "sqlx.Connect", "database/sql", "_ \""} {
		if strings.Contains(src, bad) {
			t.Errorf("the generated source contains %q, so it reaches a database:\n%s", bad, src)
		}
	}
}

// TestExpandRunsNamedOnlyForANamedQuery. sqlx.Named on a positional query
// returns an empty argument list and no error, which would silently drop what
// was passed — and "::" is a Postgres cast rather than a parameter.
func TestExpandGuardsNamedWithSqlxsOwnRule(t *testing.T) {
	src, err := expandRewrite(`q, a`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(src, "__named := false") {
		t.Fatalf("Named is not guarded:\n%s", src)
	}
	// The doubled-colon skip is the half that a strings.Contains would miss.
	if !strings.Contains(src, `if __q[__i+1] == ':' { __i++; continue }`) {
		t.Errorf("a Postgres cast would be read as a named parameter:\n%s", src)
	}
	if !strings.Contains(src, "if __named && len(__a) == 1") {
		t.Errorf("Named runs unguarded:\n%s", src)
	}
}
