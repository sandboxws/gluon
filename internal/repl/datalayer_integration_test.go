//go:build integration

package repl

import (
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/pretty"
)

// The two data-layer commands that answer without a database. Both go through
// EvalTransient like every other plugin command, so both must leave the session
// exactly as they found it — invariant 14.

// TestExpandShowsBothHalvesAndLeavesTheSessionUnchanged.
//
// sqlx is the library under test rather than a mock because the whole claim is
// about what sqlx.Named and sqlx.In really do to a query: In flattens a slice
// into as many placeholders as it has elements, Named turns :name into a
// bindvar and reorders the arguments to match, and a rewrite that got either
// wrong would still produce plausible-looking output.
func TestExpandShowsBothHalvesAndLeavesTheSessionUnchanged(t *testing.T) {
	online(t)
	c, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	for _, cmd := range c.Commands() {
		if cmd.Name == ":expand" {
			t.Fatal(":expand exists in a session with no sqlx")
		}
	}
	if res := c.Submit(":get sqlx"); res.Err {
		t.Fatalf(":get sqlx failed: %s", res.Out)
	}

	if res := c.Submit(`x := []int{1, 2, 3}`); res.Err {
		t.Fatalf("setup failed: %s", res.Out)
	}
	entriesBefore := len(c.sess.Entries)
	importsBefore := len(c.ev.Imports())

	// A list expansion: one placeholder becomes three, and the arguments are
	// flattened to match.
	res := c.Submit(`:expand "SELECT * FROM users WHERE id IN (?)", []int{1, 2, 3}`)
	if res.Err {
		t.Fatalf(":expand failed: %s", res.Out)
	}
	if !strings.Contains(res.Out, "IN (?, ?, ?)") {
		t.Errorf("the list placeholder was not expanded:\n%s", res.Out)
	}
	if !strings.Contains(res.Out, "args  [1 2 3]") {
		t.Errorf("the flattened arguments were not reported separately:\n%s", res.Out)
	}

	// Named parameters: the query keeps its placeholders and the arguments
	// arrive in the order the rewrite produced.
	named := c.Submit(`:expand "SELECT * FROM u WHERE name = :name AND age > :age", ` +
		`map[string]any{"name": "Ada", "age": 30}`)
	if named.Err {
		t.Fatalf(":expand with named parameters failed: %s", named.Out)
	}
	if !strings.Contains(named.Out, "name = ? AND age > ?") {
		t.Errorf("the named parameters were not rewritten:\n%s", named.Out)
	}
	if !strings.Contains(named.Out, "args  [Ada 30]") {
		t.Errorf("the arguments are not in the rewrite's order:\n%s", named.Out)
	}
	// Never substituted into the query text — a query with its parameters
	// already put in is not the query that runs.
	if strings.Contains(named.Out, `name = 'Ada'`) || strings.Contains(named.Out, `name = "Ada"`) {
		t.Errorf("an argument was interpolated into the query:\n%s", named.Out)
	}

	// A positional query with one argument must come back untouched: running
	// sqlx.Named on it would return an empty argument list without an error.
	positional := c.Submit(`:expand "SELECT * FROM u WHERE id = ?", 7`)
	if positional.Err {
		t.Fatalf(":expand with a positional argument failed: %s", positional.Out)
	}
	if !strings.Contains(positional.Out, "args  [7]") {
		t.Errorf("a positional argument was dropped:\n%s", positional.Out)
	}

	if got := len(c.sess.Entries); got != entriesBefore {
		t.Errorf("the session gained %d entries", got-entriesBefore)
	}
	if got := len(c.ev.Imports()); got != importsBefore {
		t.Errorf("the session gained %d imports", got-importsBefore)
	}
	if res := c.Submit(`x`); res.Err || !strings.Contains(res.Out, "[1 2 3]") {
		t.Errorf("session broken after :expand: %q", res.Out)
	}
}

// TestSchemaListsEntsDescriptorsAndLeavesTheSessionUnchanged.
//
// The subpackage is fetched rather than the module root because that is what
// puts export data where the type checker can see it, and the descriptors are
// the point: this asserts the generated source compiles against ent's real
// schema.Table, not against a struct shaped like it. It is the most expensive
// test in this tier — ent brings a dozen modules with it — and it is the only
// thing that would catch a field ent renamed.
func TestSchemaListsEntsDescriptorsAndLeavesTheSessionUnchanged(t *testing.T) {
	online(t)
	c, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	for _, cmd := range c.Commands() {
		if cmd.Name == ":schema" {
			t.Fatal(":schema exists in a session with no ent")
		}
	}
	if res := c.Submit(":get entgo.io/ent/dialect/sql/schema"); res.Err {
		t.Fatalf(":get ent failed: %s", res.Out)
	}
	// And there is still no :sql, which is the finding this plugin records.
	for _, cmd := range c.Commands() {
		if cmd.Name == ":sql" {
			t.Error(":sql appeared for ent, whose builders can only produce SQL by running it")
		}
	}

	for _, line := range []string{
		`gid := &schema.Column{Name: "group_id", Type: field.TypeInt, Nullable: true}`,
		`groups := &schema.Table{Name: "groups", Columns: []*schema.Column{{Name: "id", Type: field.TypeInt}}}`,
		`users := &schema.Table{Name: "users", Columns: []*schema.Column{` +
			`{Name: "id", Type: field.TypeInt}, ` +
			`{Name: "role", Type: field.TypeEnum, Enums: []string{"admin", "user"}}, gid}}`,
		`users.PrimaryKey = []*schema.Column{users.Columns[0]}`,
		`users.ForeignKeys = []*schema.ForeignKey{{Symbol: "users_groups", ` +
			`Columns: []*schema.Column{gid}, RefTable: groups, RefColumns: groups.Columns}}`,
	} {
		if res := c.Submit(line); res.Err {
			t.Fatalf("setup line %q failed: %s", line, res.Out)
		}
	}

	entriesBefore := len(c.sess.Entries)
	importsBefore := len(c.ev.Imports())

	res := c.Submit(`:schema []*schema.Table{users, groups}`)
	if res.Err {
		t.Fatalf(":schema failed: %s", res.Out)
	}
	for _, want := range []string{
		"users", "groups",
		"pk",                               // from Table.PrimaryKey, which a column does not know about
		"null",                             // and its opposite, always spelled out
		"not null",                         //
		"enum(admin|user)",                 // what tells an enum from the string ent calls it
		"→ group_id references groups(id)", // an edge, as the foreign key it became
	} {
		if !strings.Contains(res.Out, want) {
			t.Errorf(":schema output is missing %q:\n%s", want, res.Out)
		}
	}

	if got := len(c.sess.Entries); got != entriesBefore {
		t.Errorf("the session gained %d entries", got-entriesBefore)
	}
	if got := len(c.ev.Imports()); got != importsBefore {
		t.Errorf("the session gained %d imports", got-importsBefore)
	}
}

// TestPoolStatRendererFiresOnARealStat.
//
// The unit tests for this renderer are built on a blob of encoder JSON, and a
// blob that agreed with the renderer while disagreeing with pgx would pass all
// of them and render nothing in a session. Two things can only be checked
// against the real value: that reflect.Type.String() really is "*pgxpool.Stat",
// and that the counters really are reachable — every field on both Stat and the
// puddle.Stat under it is unexported, so this rests on the child encoder
// reading them through reflect.NewAt.
//
// The pool is created against an address nothing listens on, deliberately.
// pgxpool.New builds the pool without dialing, so a Stat exists with no server
// anywhere — which is what makes this runnable in the ordinary test tier.
func TestPoolStatRendererFiresOnARealStat(t *testing.T) {
	online(t)
	c, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if _, ok := c.Hooks()["*pgxpool.Stat"]; ok {
		t.Fatal("the pgx renderer was installed before its module was in the build list")
	}
	if res := c.Submit(":get github.com/jackc/pgx/v5/pgxpool"); res.Err {
		t.Fatalf(":get pgxpool failed: %s", res.Out)
	}
	if _, ok := c.Hooks()["*pgxpool.Stat"]; !ok {
		t.Fatal("the pgx renderer was not installed after :get")
	}

	// Core.Render is Plain here because there is no terminal, so install the
	// rich one the way the TUI does. That is also the point: a renderer reaches
	// Rich and nothing else.
	styles := pretty.PlainStyles()
	hooks := c.Hooks()
	c.Render = func(v []pretty.Value) string { return pretty.RichWith(v, styles, hooks) }

	if res := c.Submit(`p, _ := pgxpool.New(context.Background(), ` +
		`"postgres://u@127.0.0.1:1/none")`); res.Err {
		t.Fatalf("building a pool failed: %s", res.Out)
	}
	res := c.Submit(`p.Stat()`)
	if res.Err {
		t.Fatalf("p.Stat() failed: %s", res.Out)
	}
	out := stripStyles(res.Out)

	// The labels, which only appear if the walk reached both depths.
	for _, want := range []string{"max", "acquired", "idle", "acquires", "connections opened"} {
		if !strings.Contains(out, want) {
			t.Errorf("the renderer did not label %q — it declined, or the fields moved:\n%s", want, out)
		}
	}
	// MaxConns defaults to 10 or the core count, whichever is larger, so the
	// assertion is that a max was read at all rather than which number it is.
	if strings.Contains(out, "&{s:") {
		t.Errorf("the value fell through to the flattened struct form:\n%s", out)
	}

	// Plain is untouched by any of it — invariant 21.
	c.Render = pretty.Plain
	plain := c.Submit(`p.Stat()`)
	if strings.Contains(plain.Out, "connections opened") {
		t.Errorf("the pgx renderer reached Plain, which pipes and scripts read:\n%s", plain.Out)
	}
}
