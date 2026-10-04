package db

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/pretty"
)

// poolStatBlob is what the child encoder sends for a *pgxpool.Stat: three
// counters on the Stat itself and nine more behind a pointer to puddle.Stat,
// every one of them an unexported field. The encoder reads those through
// reflect.NewAt, which is what makes a renderer possible here at all.
//
// The field names and nesting are a real one's, from pgx v5.10.0 with puddle
// v2.2.2 — not invented, because a blob that agreed with the renderer and
// disagreed with pgx would pass every test here and render nothing in a
// session. TestPoolStatRendersARealStat is what keeps the two in step.
const poolStatBlob = `{"t":"*pgxpool.Stat","k":"ptr","d":1,"i":[{"k":"struct","f":[
	{"n":"s","v":{"t":"*puddle.Stat","k":"ptr","d":2,"i":[{"k":"struct","f":[
		{"n":"constructingResources","v":{"t":"int32","k":"scalar","r":"1"}},
		{"n":"acquiredResources","v":{"t":"int32","k":"scalar","r":"3"}},
		{"n":"idleResources","v":{"t":"int32","k":"scalar","r":"5"}},
		{"n":"maxResources","v":{"t":"int32","k":"scalar","r":"20"}},
		{"n":"acquireCount","v":{"t":"int64","k":"scalar","r":"412"}},
		{"n":"acquireDuration","v":{"t":"time.Duration","k":"scalar","r":"1.5ms"}},
		{"n":"emptyAcquireCount","v":{"t":"int64","k":"scalar","r":"7"}},
		{"n":"emptyAcquireWaitTime","v":{"t":"time.Duration","k":"scalar","r":"12ms"}},
		{"n":"canceledAcquireCount","v":{"t":"int64","k":"scalar","r":"2"}}]}]}},
	{"n":"newConnsCount","v":{"t":"int64","k":"scalar","r":"9"}},
	{"n":"lifetimeDestroyCount","v":{"t":"int64","k":"scalar","r":"4"}},
	{"n":"idleDestroyCount","v":{"t":"int64","k":"scalar","r":"6"}}]}]}`

// bothForms is the pair a renderer has to supply for a value to read the same
// wherever it appears — owning its line, and inside a table cell.
type bothForms struct {
	rich   func(pretty.Value, pretty.Styles) (string, bool)
	inline func(pretty.Value, pretty.Styles) (string, bool)
}

func poolRenderFor(t *testing.T, typ string) bothForms {
	t.Helper()
	for _, r := range (Pgx{}).Renders() {
		if r.Type == typ {
			return bothForms{r.Rich, r.Inline}
		}
	}
	t.Fatalf("no renderer for %s", typ)
	return bothForms{}
}

// TestPoolStatCountersAreLabelled. The counters sit at two depths behind
// unexported names; the whole value of the renderer is that each one arrives
// beside a label saying what it counts.
func TestPoolStatCountersAreLabelled(t *testing.T) {
	out, ok := poolRenderFor(t, "*pgxpool.Stat").rich(parse(t, poolStatBlob), pretty.Styles{})
	if !ok {
		t.Fatal("declined a pool stat")
	}
	for _, want := range []string{
		"max", "acquired", "idle", "constructing",
		"acquires", "acquires that waited", "acquires canceled",
		"time spent acquiring", "time spent waiting", "connections opened",
		"closed — max lifetime", "closed — max idle",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("no label %q in the rendering:\n%s", want, out)
		}
	}
	// Every counter, from both depths, and the durations among them.
	for _, want := range []string{"20", "412", "1.5ms", "9", "6"} {
		if !strings.Contains(out, want) {
			t.Errorf("counter %q is missing:\n%s", want, out)
		}
	}
	// The pointer field's own name says nothing and must not become a row.
	if strings.Contains(out, "\ns \n") || strings.Contains(out, "| s ") {
		t.Errorf("the pointer field became a counter row:\n%s", out)
	}
	if !strings.Contains(out, "pgxpool.Stat") {
		t.Errorf("the rendering does not name the type:\n%s", out)
	}
}

// TestPoolStatKeepsAFieldItDoesNotKnow. The field names are pgx's own and
// unexported, so a version may rename or add one. A counter gluon has not heard
// of is still a named number, and dropping it would be the one failure a
// renderer must not have: showing less than the struct rendering it replaced.
func TestPoolStatKeepsAFieldItDoesNotKnow(t *testing.T) {
	v := parse(t, `{"t":"pgxpool.Stat","k":"struct","f":[
		{"n":"acquiredResources","v":{"k":"scalar","r":"3"}},
		{"n":"maxResources","v":{"k":"scalar","r":"20"}},
		{"n":"someFutureCount","v":{"k":"scalar","r":"11"}}]}`)
	out, ok := poolRenderFor(t, "pgxpool.Stat").rich(v, pretty.Styles{})
	if !ok {
		t.Fatal("declined a stat carrying an unknown field")
	}
	if !strings.Contains(out, "someFutureCount") || !strings.Contains(out, "11") {
		t.Errorf("an unrecognised counter was dropped:\n%s", out)
	}
}

// TestPoolStatOrdersTheKnownCountersFirst, in poolLabels' reading order, so the
// table reads the same whatever order a pgx version declares its fields in.
func TestPoolStatOrdersTheKnownCountersFirst(t *testing.T) {
	v := parse(t, `{"t":"pgxpool.Stat","k":"struct","f":[
		{"n":"zzzUnknown","v":{"k":"scalar","r":"0"}},
		{"n":"idleResources","v":{"k":"scalar","r":"5"}},
		{"n":"maxResources","v":{"k":"scalar","r":"20"}}]}`)
	got := sortPoolCounters(poolFields(v, 0))
	want := []string{"maxResources", "idleResources", "zzzUnknown"}
	for i, w := range want {
		if got[i].name != w {
			t.Fatalf("counter order = %v, want %v", got, want)
		}
	}
}

// TestPoolStatInlineIsOneLine — a cell has room for one fact, and the one
// somebody scanning a table of pools wants is how many connections are in use.
func TestPoolStatInlineIsOneLine(t *testing.T) {
	out, ok := poolRenderFor(t, "*pgxpool.Stat").inline(parse(t, poolStatBlob), pretty.Styles{})
	if !ok {
		t.Fatal("declined a pool stat inline")
	}
	if strings.Contains(out, "\n") {
		t.Errorf("the inline form is more than one line: %q", out)
	}
	if !strings.Contains(out, "3/20") || !strings.Contains(out, "5") {
		t.Errorf("the inline form does not say what the pool is doing: %q", out)
	}
	// A total from the history counters has nothing to compare against in a
	// cell, and would push out the one number that does.
	if strings.Contains(out, "412") {
		t.Errorf("a history counter reached the cell: %q", out)
	}
}

// TestPoolStatDeclinesCleanly is the half that keeps a plugin from breaking
// output it does not understand. Every one of these is a value the renderer
// could be handed by a type that merely shares the name.
func TestPoolStatDeclinesCleanly(t *testing.T) {
	for name, blob := range map[string]string{
		"a scalar":        `{"t":"pgxpool.Stat","k":"scalar","r":"whatever"}`,
		"a nil pointer":   `{"t":"*pgxpool.Stat","k":"nil","r":"nil"}`,
		"an empty struct": `{"t":"pgxpool.Stat","k":"struct","f":[]}`,
		"a list":          `{"t":"pgxpool.Stat","k":"list","l":0,"i":[]}`,
		"only nested nothing": `{"t":"pgxpool.Stat","k":"struct","f":[
			{"n":"s","v":{"k":"ptr","i":[{"k":"struct","f":[]}]}}]}`,
	} {
		v := parse(t, blob)
		if out, ok := poolRenderFor(t, "pgxpool.Stat").rich(v, pretty.Styles{}); ok {
			t.Errorf("%s was accepted by Rich: %q", name, out)
		}
		if out, ok := poolRenderFor(t, "pgxpool.Stat").inline(v, pretty.Styles{}); ok {
			t.Errorf("%s was accepted by Inline: %q", name, out)
		}
	}
	// A Stat with counters but neither of the two that make a ratio has no
	// one-line answer worth giving, so only Inline declines.
	partial := parse(t, `{"t":"pgxpool.Stat","k":"struct","f":[
		{"n":"acquireCount","v":{"k":"scalar","r":"412"}}]}`)
	if _, ok := poolRenderFor(t, "pgxpool.Stat").rich(partial, pretty.Styles{}); !ok {
		t.Error("Rich declined a stat it could still tabulate")
	}
	if out, ok := poolRenderFor(t, "pgxpool.Stat").inline(partial, pretty.Styles{}); ok {
		t.Errorf("Inline invented a ratio it did not have: %q", out)
	}
}

// TestPoolStatWalkIsBounded. The renderer is handed whatever the child called a
// pgxpool.Stat, and a deep or self-similar value must cost a bounded walk.
func TestPoolStatWalkIsBounded(t *testing.T) {
	deep := parse(t, `{"k":"struct","f":[{"n":"a","v":{"k":"struct","f":[
		{"n":"b","v":{"k":"struct","f":[{"n":"c","v":{"k":"struct","f":[
			{"n":"tooDeep","v":{"k":"scalar","r":"1"}}]}}]}}]}}]}`)
	for _, c := range poolFields(deep, 0) {
		if c.name == "tooDeep" {
			t.Error("the walk went past maxPoolDepth")
		}
	}
}

// TestPoolStatFallsThroughByteForByte is invariant 21 for this renderer
// specifically.
//
// internal/pretty pins the property against a hand-written declining hook; this
// pins it against the real one, which is the half that could regress. The
// assertion cannot live in internal/pretty — it would have to import the
// plugin, and the plugin imports pretty.
func TestPoolStatFallsThroughByteForByte(t *testing.T) {
	hooks := map[string]pretty.Hook{}
	for _, r := range (Pgx{}).Renders() {
		hooks[r.Type] = pretty.Hook{Rich: r.Rich, Inline: r.Inline}
	}

	corpus := []string{
		`[{"t":"int","k":"scalar","r":"42"}]`,
		`[{"t":"string","k":"string","r":"hi","l":2,"u":2}]`,
		`[{"t":"main.P","k":"struct","f":[{"n":"X","v":{"k":"scalar","r":"1"}}]}]`,
		// The renderer's own type, in every shape it must decline.
		`[{"t":"pgxpool.Stat","k":"scalar","r":"whatever"}]`,
		`[{"t":"*pgxpool.Stat","k":"nil","r":"nil"}]`,
		`[{"t":"pgxpool.Stat","k":"struct","f":[]}]`,
		// A struct holding one, so the Inline path is exercised in a cell.
		`[{"t":"main.App","k":"struct","f":[
			{"n":"Pool","v":{"t":"pgxpool.Stat","k":"struct","f":[
				{"n":"acquireCount","v":{"k":"scalar","r":"1"}}]}}]}]`,
	}

	st := pretty.PlainStyles()
	for _, blob := range corpus {
		var vals []pretty.Value
		if err := json.Unmarshal([]byte(blob), &vals); err != nil {
			t.Fatal(err)
		}
		want := pretty.Rich(vals, st)
		got := pretty.RichWith(vals, st, hooks)
		if got != want {
			t.Errorf("the pgx hooks changed the rendering of %s\n got %q\nwant %q", blob, got, want)
		}
	}
}

// TestPoolStatAppliesWhenItClaims — the other half. A fallthrough test alone
// would pass on a renderer that had stopped working entirely.
func TestPoolStatAppliesWhenItClaims(t *testing.T) {
	hooks := map[string]pretty.Hook{}
	for _, r := range (Pgx{}).Renders() {
		hooks[r.Type] = pretty.Hook{Rich: r.Rich, Inline: r.Inline}
	}
	vals := []pretty.Value{parse(t, poolStatBlob)}
	st := pretty.PlainStyles()
	if pretty.RichWith(vals, st, hooks) == pretty.Rich(vals, st) {
		t.Error("the renderer changed nothing for the value it exists to render")
	}
}
