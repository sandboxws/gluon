package pretty

import (
	"encoding/json"
	"strings"
	"testing"
)

func parseOne(t *testing.T, js string) []Value {
	t.Helper()
	_, vals := Parse("\x00GLUON\x00" + js + "\n")
	if len(vals) == 0 {
		t.Fatalf("nothing parsed from %s", js)
	}
	return vals
}

// The plain renderer is a compatibility surface: pipes and scripts read it.
func TestPlainShapes(t *testing.T) {
	tests := []struct {
		name string
		js   string
		want string
	}{
		{"int", `[{"t":"int","k":"scalar","r":"2"}]`, "(int) 2"},
		{"slice", `[{"t":"[]int","k":"list","l":3,"c":3,"i":[{"t":"int","k":"scalar","r":"1"},{"t":"int","k":"scalar","r":"2"},{"t":"int","k":"scalar","r":"3"}]}]`,
			"([]int) [1 2 3]  len=3 cap=3"},
		{"string", `[{"t":"string","k":"string","r":"héllo","l":6,"u":5}]`,
			`(string) "héllo"  len=6 bytes, 5 runes`},
		{"byte", `[{"t":"uint8","k":"scalar","r":"195 (0xc3)"}]`, "(uint8) 195 (0xc3)"},
		{"untyped nil", `[{"k":"nil","t":"","r":"nil"}]`, "nil"},
		{"multi-value", `[{"t":"int","k":"scalar","r":"42"},{"k":"nil","t":"","r":"nil"}]`,
			"(int) 42, nil"},
		{"typed nil error", `[{"t":"*main.E","k":"nil","r":"nil","n":"non-nil interface holding a nil pointer — err != nil is TRUE"}]`,
			"(*main.E) nil  non-nil interface holding a nil pointer — err != nil is TRUE"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Plain(parseOne(t, tc.js)); got != tc.want {
				t.Errorf("\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

// A bordered box around the number 2 is worse than the number 2.
func TestScalarsDoNotGetTables(t *testing.T) {
	got := Rich(parseOne(t, `[{"t":"int","k":"scalar","r":"2"}]`), PlainStyles())
	if strings.Contains(got, "─") || strings.Contains(got, "\n") {
		t.Errorf("scalar should stay one line, got:\n%s", got)
	}
}

func TestListGetsTable(t *testing.T) {
	js := `[{"t":"[]int","k":"list","l":3,"c":3,"i":[{"t":"int","k":"scalar","r":"3"},{"t":"int","k":"scalar","r":"1"},{"t":"int","k":"scalar","r":"2"}]}]`
	got := Rich(parseOne(t, js), PlainStyles())
	t.Logf("\n%s", got)
	for _, want := range []string{"[]int", "len=3 cap=3", "#", "value", "3", "1", "2"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestMapAndStructTables(t *testing.T) {
	mapJS := `[{"t":"map[string]int","k":"map","l":2,"ky":[{"t":"string","k":"string","r":"a"},{"t":"string","k":"string","r":"b"}],"i":[{"t":"int","k":"scalar","r":"1"},{"t":"int","k":"scalar","r":"2"}]}]`
	got := Rich(parseOne(t, mapJS), PlainStyles())
	t.Logf("\n%s", got)
	if !strings.Contains(got, "key") || !strings.Contains(got, "value") {
		t.Errorf("map table missing headers:\n%s", got)
	}

	structJS := `[{"t":"main.P","k":"struct","f":[{"n":"X","v":{"t":"int","k":"scalar","r":"3"}},{"n":"Y","v":{"t":"int","k":"scalar","r":"4"}}]}]`
	got = Rich(parseOne(t, structJS), PlainStyles())
	t.Logf("\n%s", got)
	for _, want := range []string{"field", "X", "Y", "3", "4"} {
		if !strings.Contains(got, want) {
			t.Errorf("struct table missing %q:\n%s", want, got)
		}
	}
}

// The program's own stdout must survive alongside the described value.
func TestParseSeparatesUserOutput(t *testing.T) {
	raw := "hi there\n\x00GLUON\x00[{\"t\":\"int\",\"k\":\"scalar\",\"r\":\"3\"}]\n"
	userOut, vals := Parse(raw)
	if userOut != "hi there\n" {
		t.Errorf("userOut = %q", userOut)
	}
	if len(vals) != 1 || vals[0].Repr != "3" {
		t.Errorf("vals = %+v", vals)
	}
}

// A malformed payload must not swallow the program's output.
func TestParseFallsBackOnGarbage(t *testing.T) {
	raw := "output\n\x00GLUON\x00{not json}\n"
	userOut, vals := Parse(raw)
	if vals != nil {
		t.Errorf("expected no values, got %+v", vals)
	}
	if !strings.Contains(userOut, "output") {
		t.Errorf("user output lost: %q", userOut)
	}
}

// A back-reference is a scalar, and the label only appears on an object
// something actually points at.
func TestCycleAndSharedRendering(t *testing.T) {
	cyclic := Value{
		Type: "*main.Node", Kind: "ptr", ID: 1,
		Items: []Value{{
			Type: "main.Node", Kind: "struct",
			Fields: []Field{
				{Name: "Val", Val: Value{Type: "int", Kind: "scalar", Repr: "1"}},
				{Name: "Next", Val: Value{Type: "*main.Node", Kind: "cycle", ID: 1}},
			},
		}},
	}
	got := Plain([]Value{cyclic})
	for _, want := range []string{"#1&", "<cycle #1>", "cyclic"} {
		if !strings.Contains(got, want) {
			t.Errorf("Plain() = %q, want it to contain %q", got, want)
		}
	}

	shared := Value{
		Type: "[]*main.P", Kind: "list", ID: 1,
		Items: []Value{
			{Type: "*main.P", Kind: "ptr", ID: 2, Items: []Value{{Type: "main.P", Kind: "struct",
				Fields: []Field{{Name: "X", Val: Value{Type: "int", Kind: "scalar", Repr: "1"}}}}}},
			{Type: "*main.P", Kind: "shared", ID: 2},
		},
	}
	got = Plain([]Value{shared})
	for _, want := range []string{"#2&", "<shared #2>", "more than once"} {
		if !strings.Contains(got, want) {
			t.Errorf("Plain() = %q, want it to contain %q", got, want)
		}
	}
}

// The child labels every object it tracks, because a shared target is written
// before the reference to it is seen. Nothing pointed at keeps a label.
func TestUnreferencedIDsAreNotShown(t *testing.T) {
	v := Value{
		Type: "[]*main.P", Kind: "list", ID: 1,
		Items: []Value{{Type: "*main.P", Kind: "ptr", ID: 2, Items: []Value{
			{Type: "main.P", Kind: "struct", Fields: []Field{
				{Name: "X", Val: Value{Type: "int", Kind: "scalar", Repr: "1"}}}}}}},
	}
	if got := Plain([]Value{v}); strings.Contains(got, "#") {
		t.Errorf("Plain() = %q, want no labels when nothing refers back", got)
	}
}

func TestCellIsBounded(t *testing.T) {
	long := strings.Repeat("x", 400)
	v := Value{Kind: "struct", Fields: []Field{
		{Name: "S", Val: Value{Kind: "string", Repr: long}}}}
	for _, line := range strings.Split(v.rich(PlainStyles(), Options{}), "\n") {
		if len([]rune(line)) > maxCell+40 {
			t.Errorf("table line is %d runes, want it bounded:\n%s", len([]rune(line)), line)
		}
	}
}

// TestHooksFallThroughByteForByte is invariant 19.
//
// A plugin that declines, or that has no formatter for a type, must leave the
// rendering exactly as it was. Anything less means installing a plugin could
// change output that has nothing to do with it.
func TestHooksFallThroughByteForByte(t *testing.T) {
	corpus := []string{
		`[{"t":"int","k":"scalar","r":"42"}]`,
		`[{"t":"string","k":"string","r":"hi","l":2,"u":2}]`,
		`[{"t":"[]int","k":"list","l":3,"c":3,"i":[
			{"k":"scalar","r":"1"},{"k":"scalar","r":"2"},{"k":"scalar","r":"3"}]}]`,
		`[{"t":"map[string]int","k":"map","l":1,
			"ky":[{"k":"string","r":"a"}],"i":[{"k":"scalar","r":"1"}]}]`,
		`[{"t":"main.P","k":"struct","f":[
			{"n":"X","v":{"k":"scalar","r":"1"}},{"n":"Y","v":{"k":"string","r":"s"}}]}]`,
		`[{"t":"time.Duration","k":"scalar","r":"3s"}]`,
		`[{"t":"*main.Node","k":"ptr","d":1,"i":[{"k":"struct","f":[
			{"n":"Next","v":{"k":"cycle","d":1}}]}]}]`,
	}

	// Every hook here either declines or is registered for a type nothing in
	// the corpus uses.
	decline := func(Value, Styles) (string, bool) { return "SHOULD NOT APPEAR", false }
	hooks := map[string]Hook{
		"time.Duration":  {Rich: decline, Inline: decline},
		"int":            {Rich: decline, Inline: decline},
		"*http.Response": {Rich: func(Value, Styles) (string, bool) { return "SHOULD NOT APPEAR", true }},
		// Registered for a type nothing in the corpus uses.
		"uuid.UUID": {Inline: func(Value, Styles) (string, bool) { return "SHOULD NOT APPEAR", true }},
	}

	st := PlainStyles()
	for _, blob := range corpus {
		var vals []Value
		if err := json.Unmarshal([]byte(blob), &vals); err != nil {
			t.Fatal(err)
		}
		// Every form, not just the table one. A plugin must not be able to move
		// any form's bytes by declining, and a form added later gets this for
		// free — which is the point of running the loop over Forms() rather
		// than over a list somebody has to remember to extend.
		for _, f := range Forms() {
			want := RichIn(vals, st, Options{Form: f})
			got := RichIn(vals, st, Options{Form: f, Hooks: hooks})
			if got != want {
				t.Errorf("hooks changed the %s rendering of %s\n got %q\nwant %q", f, blob, got, want)
			}
		}
	}
}

// TestHookAppliesWhenItClaims is the other half: a hook that accepts really
// does replace the rendering.
func TestHookAppliesWhenItClaims(t *testing.T) {
	vals := []Value{{Type: "time.Duration", Kind: "scalar", Repr: "3s"}}
	hooks := map[string]Hook{
		"time.Duration": {Rich: func(Value, Styles) (string, bool) { return "THREE SECONDS", true }},
	}
	if got := RichWith(vals, PlainStyles(), hooks); got != "THREE SECONDS" {
		t.Errorf("hook did not apply: %q", got)
	}
}

// TestHooksNeverReachPlain guards the compatibility surface directly: there is
// no hook parameter on Plain, and this is the test that says that is deliberate.
func TestHooksNeverReachPlain(t *testing.T) {
	vals := []Value{{Type: "time.Duration", Kind: "scalar", Repr: "3s"}}
	if got, want := Plain(vals), "(time.Duration) 3s"; got != want {
		t.Errorf("Plain = %q, want %q — pipes and gluon -e read this", got, want)
	}
}

// TestInlineHookReachesATableCell is the point of the two-form Hook: a value
// nested in a struct or a slice should read the same as one on its own line.
func TestInlineHookReachesATableCell(t *testing.T) {
	hooks := map[string]Hook{
		"time.Duration": {Inline: func(Value, Styles) (string, bool) { return "TWO HOURS", true }},
	}
	v := Value{Type: "main.Job", Kind: "struct", Fields: []Field{
		{Name: "Name", Val: Value{Kind: "string", Repr: "index"}},
		{Name: "Took", Val: Value{Type: "time.Duration", Kind: "scalar", Repr: "2h0m0s"}},
	}}
	out := RichWith([]Value{v}, PlainStyles(), hooks)
	if !strings.Contains(out, "TWO HOURS") {
		t.Errorf("the inline hook did not reach the struct cell:\n%s", out)
	}

	// And in a list, and as a map value.
	list := Value{Type: "[]time.Duration", Kind: "list", Items: []Value{
		{Type: "time.Duration", Kind: "scalar", Repr: "2h0m0s"}}}
	if out := RichWith([]Value{list}, PlainStyles(), hooks); !strings.Contains(out, "TWO HOURS") {
		t.Errorf("the inline hook did not reach a list cell:\n%s", out)
	}
	m := Value{Type: "map[string]time.Duration", Kind: "map",
		Keys:  []Value{{Kind: "string", Repr: "k"}},
		Items: []Value{{Type: "time.Duration", Kind: "scalar", Repr: "2h0m0s"}}}
	if out := RichWith([]Value{m}, PlainStyles(), hooks); !strings.Contains(out, "TWO HOURS") {
		t.Errorf("the inline hook did not reach a map cell:\n%s", out)
	}
}

// TestRichOnlyHookIsNotConsultedForCells. Rich supplies its own type prefix and
// may take several lines; using it in a cell would put "(time.Duration)" inside
// a column that already says so.
func TestRichOnlyHookIsNotConsultedForCells(t *testing.T) {
	hooks := map[string]Hook{
		"time.Duration": {Rich: func(Value, Styles) (string, bool) { return "RICH FORM", true }},
	}
	v := Value{Type: "main.Job", Kind: "struct", Fields: []Field{
		{Name: "Took", Val: Value{Type: "time.Duration", Kind: "scalar", Repr: "2h0m0s"}},
	}}
	if out := RichWith([]Value{v}, PlainStyles(), hooks); strings.Contains(out, "RICH FORM") {
		t.Errorf("the Rich form was used inside a cell:\n%s", out)
	}
}

// TestAMisbehavingHookCannotBreakTheTable. A cell is one line by construction,
// and an over-long or multi-line one corrupts every row below it. The hook is
// not trusted to know that.
func TestAMisbehavingHookCannotBreakTheTable(t *testing.T) {
	hooks := map[string]Hook{
		"time.Duration": {Inline: func(Value, Styles) (string, bool) {
			return "line one\nline two\r\nline three " + strings.Repeat("x", 400), true
		}},
	}
	v := Value{Type: "main.Job", Kind: "struct", Fields: []Field{
		{Name: "Took", Val: Value{Type: "time.Duration", Kind: "scalar", Repr: "2h0m0s"}},
	}}
	out := RichWith([]Value{v}, PlainStyles(), hooks)
	for _, line := range strings.Split(out, "\n") {
		if len([]rune(line)) > maxCell+40 {
			t.Errorf("a hook widened a row to %d runes:\n%s", len([]rune(line)), line)
		}
	}
	// The table is head, header, one row, foot — a multi-line cell would add more.
	if n := strings.Count(out, "\n"); n > 6 {
		t.Errorf("a multi-line cell added %d lines to the table:\n%s", n, out)
	}
}
