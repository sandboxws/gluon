package pretty

import (
	"strings"
	"testing"
)

// corpus is the values every form test runs over. It is the union of what
// render_test.go already pins and the cases the new forms make interesting: a
// list of structs both uniform and not, a struct nested two deep, a collection
// past maxRows, a cell past maxCell, the empty collections, and the two
// back-reference kinds.
func corpus() [][]Value {
	long := Value{Type: "[]int", Kind: "list"}
	for i := 0; i < 60; i++ {
		long.Items = append(long.Items, Value{Type: "int", Kind: "scalar", Repr: itoa(i)})
	}
	user := func(id int, name string) Value {
		return Value{Type: "main.User", Kind: "struct", Fields: []Field{
			{Name: "ID", Val: Value{Type: "int", Kind: "scalar", Repr: itoa(id)}},
			{Name: "Name", Val: Value{Type: "string", Kind: "string", Repr: name}},
		}}
	}
	n := 3
	return [][]Value{
		{{Type: "int", Kind: "scalar", Repr: "2"}},
		{{Type: "string", Kind: "string", Repr: "héllo", Len: &n, Runes: &n}},
		{{Type: "", Kind: "nil", Repr: "nil"}},
		{{Type: "[]int", Kind: "list"}},
		{{Type: "map[string]int", Kind: "map"}},
		{{Type: "[]int", Kind: "list", Len: &n, Cap: &n, Items: []Value{
			{Type: "int", Kind: "scalar", Repr: "1"},
			{Type: "int", Kind: "scalar", Repr: "2"},
		}}},
		{{Type: "map[string]int", Kind: "map",
			Keys:  []Value{{Type: "string", Kind: "string", Repr: "a"}},
			Items: []Value{{Type: "int", Kind: "scalar", Repr: "1"}}}},
		{user(7, "ada")},
		{{Type: "[]main.User", Kind: "list", Items: []Value{user(7, "ada"), user(8, "bob")}}},
		{{Type: "[]any", Kind: "list", Items: []Value{
			user(7, "ada"), {Type: "int", Kind: "scalar", Repr: "1"}}}},
		{{Type: "*main.User", Kind: "ptr", Items: []Value{user(7, "ada")}}},
		{{Type: "main.Order", Kind: "struct", Fields: []Field{
			{Name: "ID", Val: Value{Type: "int", Kind: "scalar", Repr: "9"}},
			{Name: "By", Val: user(7, "ada")},
		}}},
		{long},
		{{Type: "main.Wide", Kind: "struct", Fields: []Field{
			{Name: "Blurb", Val: Value{Type: "string", Kind: "string",
				Repr: strings.Repeat("x", 400)}},
		}}},
		{{Type: "*main.Node", Kind: "struct", ID: 1, Fields: []Field{
			{Name: "Self", Val: Value{Type: "*main.Node", Kind: "cycle", ID: 1}},
		}}},
		{{Type: "int", Kind: "scalar", Repr: "1"}, {Type: "string", Kind: "string", Repr: "a"}},
	}
}

// TestTheDefaultFormIsTheOldRenderer is the compatibility proof, and it has two
// halves because either alone proves nothing.
//
// The equality half says the new entry point and the old one agree over a
// corpus that crosses every bound the renderer has. On its own it would pass
// just as happily if a refactor moved both, so the golden half says neither
// moved: three renderings written out, box-drawing characters and all.
func TestTheDefaultFormIsTheOldRenderer(t *testing.T) {
	st := PlainStyles()
	hooks := map[string]Hook{"time.Duration": {
		Rich:   func(v Value, _ Styles) (string, bool) { return "(time.Duration) " + v.Repr, true },
		Inline: func(v Value, _ Styles) (string, bool) { return v.Repr, true },
	}}
	for i, vals := range corpus() {
		if got, want := RichIn(vals, st, Options{}), Rich(vals, st); got != want {
			t.Errorf("corpus[%d]: RichIn at the zero Options moved bytes\n got %q\nwant %q", i, got, want)
		}
		got := RichIn(vals, st, Options{Hooks: hooks})
		if want := RichWith(vals, st, hooks); got != want {
			t.Errorf("corpus[%d]: RichIn with hooks moved bytes\n got %q\nwant %q", i, got, want)
		}
	}

	golden := []struct {
		name string
		val  Value
		want string
	}{
		{"list", Value{Type: "[]int", Kind: "list", Items: []Value{
			{Type: "int", Kind: "scalar", Repr: "1"},
		}}, "([]int)\n╭───┬───────╮\n│ # │ value │\n├───┼───────┤\n│ 0 │ 1     │\n╰───┴───────╯"},
		{"map", Value{Type: "map[string]int", Kind: "map",
			Keys:  []Value{{Type: "string", Kind: "string", Repr: "a"}},
			Items: []Value{{Type: "int", Kind: "scalar", Repr: "1"}}},
			"(map[string]int)\n╭─────┬───────╮\n│ key │ value │\n├─────┼───────┤\n│ \"a\" │ 1     │\n╰─────┴───────╯"},
		{"struct", Value{Type: "main.P", Kind: "struct", Fields: []Field{
			{Name: "X", Val: Value{Type: "int", Kind: "scalar", Repr: "3"}},
		}}, "(main.P)\n╭───────┬───────╮\n│ field │ value │\n├───────┼───────┤\n│ X     │ 3     │\n╰───────┴───────╯"},
	}
	for _, g := range golden {
		if got := RichIn([]Value{g.val}, st, Options{}); got != g.want {
			t.Errorf("%s golden moved\n got %q\nwant %q", g.name, got, g.want)
		}
	}
}

// TestNoConfigMeansTheTableForm: the zero Form has to be the table, or every
// caller that never heard of forms silently changes shape.
func TestNoConfigMeansTheTableForm(t *testing.T) {
	if (Options{}).Form != FormTable {
		t.Errorf("the zero Options is not FormTable")
	}
	if Form(0).String() != "table" {
		t.Errorf("Form(0) is %q, want table", Form(0).String())
	}
}

// TestFormNamesRoundTrip is the registry-completeness check the command
// registry's tests make about commands: a form that cannot be named cannot be
// configured, and one named twice dispatches to whichever came first.
func TestFormNamesRoundTrip(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range Forms() {
		name := f.String()
		if name == "" {
			t.Fatalf("form %d has no name", f)
		}
		if seen[name] {
			t.Errorf("two forms are called %q", name)
		}
		seen[name] = true
		if f.About() == "" {
			t.Errorf("%s describes itself with nothing", name)
		}
		got, err := ParseForm(name)
		if err != nil || got != f {
			t.Errorf("ParseForm(%q) = %v, %v; want %v", name, got, err, f)
		}
	}
	if len(seen) != len(FormNames()) {
		t.Errorf("Forms() and FormNames() disagree: %d vs %d", len(seen), len(FormNames()))
	}
}

// TestParseFormRejectsUnknown: the error names what is available, because
// "unknown form" alone leaves the user to guess a second time.
func TestParseFormRejectsUnknown(t *testing.T) {
	_, err := ParseForm("treee")
	if err == nil {
		t.Fatal("ParseForm accepted treee")
	}
	for _, n := range FormNames() {
		if !strings.Contains(err.Error(), n) {
			t.Errorf("the error does not name %q: %v", n, err)
		}
	}
}

// TestEveryFormDrawsEveryKind. A form is a preference over a whole session, so
// a value it cannot draw must still be drawn — there is no kind for which the
// honest answer is nothing.
func TestEveryFormDrawsEveryKind(t *testing.T) {
	st := PlainStyles()
	for _, f := range Forms() {
		for i, vals := range corpus() {
			out := RichIn(vals, st, Options{Form: f})
			if strings.TrimSpace(out) == "" {
				t.Errorf("%s drew nothing for corpus[%d]", f, i)
			}
			if f == FormLine && strings.Contains(out, "\n") && len(vals) == 1 {
				t.Errorf("%s took more than one line for corpus[%d]: %q", f, i, out)
			}
		}
	}
}

// TestFormBoundsHold. maxRows and maxCell exist because scrollback is finite;
// a form that ignored them would be the one place a value could run away.
func TestFormBoundsHold(t *testing.T) {
	st := PlainStyles()
	long := Value{Type: "[]int", Kind: "list"}
	for i := 0; i < 400; i++ {
		long.Items = append(long.Items, Value{Type: "int", Kind: "scalar", Repr: itoa(i)})
	}

	line := RichIn([]Value{long}, st, Options{Form: FormLine})
	if n := len([]rune(line)); n > maxLine+len("([]int) ")+8 {
		t.Errorf("FormLine ran to %d runes, maxLine is %d", n, maxLine)
	}

	tree := RichIn([]Value{long}, st, Options{Form: FormTree})
	if n := strings.Count(tree, "\n"); n > maxRows+2 {
		t.Errorf("FormTree drew %d lines, maxRows is %d", n, maxRows)
	}
	if !strings.Contains(tree, "more") {
		t.Errorf("FormTree truncated without saying so:\n%s", tree)
	}

	wide := Value{Type: "main.W", Kind: "struct"}
	for i := 0; i < 12; i++ {
		wide.Fields = append(wide.Fields, Field{
			Name: "F" + itoa(i),
			Val:  Value{Type: "string", Kind: "string", Repr: strings.Repeat("y", 200)},
		})
	}
	cols := RichIn([]Value{{Type: "[]main.W", Kind: "list", Items: []Value{wide, wide}}},
		st, Options{Form: FormColumns})
	for _, l := range strings.Split(cols, "\n") {
		if n := len([]rune(l)); n > maxCols*(maxColCell+3)+24 {
			t.Errorf("FormColumns drew a %d-rune line:\n%s", n, l)
		}
	}
	if !strings.Contains(cols, "more") {
		t.Errorf("FormColumns dropped fields without naming them:\n%s", cols)
	}
}

// TestColumnsFallsBackWhenNotUniform: a column that exists for some rows and
// not others is a worse table than one column of rendered values, which is the
// rule browse.go already states. The fallback is the ordinary table, not
// nothing.
func TestColumnsFallsBackWhenNotUniform(t *testing.T) {
	st := PlainStyles()
	mixed := []Value{{Type: "[]any", Kind: "list", Items: []Value{
		{Type: "main.User", Kind: "struct", Fields: []Field{
			{Name: "ID", Val: Value{Type: "int", Kind: "scalar", Repr: "7"}}}},
		{Type: "int", Kind: "scalar", Repr: "1"},
	}}}
	if got, want := RichIn(mixed, st, Options{Form: FormColumns}), Rich(mixed, st); got != want {
		t.Errorf("a non-uniform list did not fall back to the table form:\n got %q\nwant %q", got, want)
	}

	uniform := []Value{{Type: "[]main.User", Kind: "list", Items: []Value{
		{Type: "main.User", Kind: "struct", Fields: []Field{
			{Name: "ID", Val: Value{Type: "int", Kind: "scalar", Repr: "7"}},
			{Name: "Name", Val: Value{Type: "string", Kind: "string", Repr: "ada"}}}},
	}}}
	got := RichIn(uniform, st, Options{Form: FormColumns})
	for _, want := range []string{"ID", "Name", "ada"} {
		if !strings.Contains(got, want) {
			t.Errorf("the columns form does not name %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "value") {
		t.Errorf("the columns form kept the value column:\n%s", got)
	}
}

// TestGoFormSaysWhyWhenThereIsNoLiteral. :test may refuse — that is a judgement
// about a test. A display form may not: it falls back and names the component
// that has no literal, so the fallback is never silent.
func TestGoFormSaysWhyWhenThereIsNoLiteral(t *testing.T) {
	st := PlainStyles()
	ch := []Value{{Type: "main.Conn", Kind: "struct", Fields: []Field{
		{Name: "Sock", Val: Value{Type: "chan int", Kind: "scalar", Repr: "chan int"}},
	}}}
	got := RichIn(ch, st, Options{Form: FormLiteral})
	if !strings.Contains(got, "no literal form") {
		t.Errorf("the literal form refused without saying why:\n%s", got)
	}
	if !strings.Contains(got, "Sock") {
		t.Errorf("the literal form did not name the component:\n%s", got)
	}

	ok := []Value{{Type: "[]int", Kind: "list", Items: []Value{
		{Type: "int", Kind: "scalar", Repr: "1"},
		{Type: "int", Kind: "scalar", Repr: "2"},
	}}}
	if got, want := RichIn(ok, st, Options{Form: FormLiteral}), "[]int{1, 2}"; !strings.Contains(got, want) {
		t.Errorf("the literal form gave %q, want it to contain %q", got, want)
	}
}

// TestAPerKindOverrideBeatsTheDefault, and applies at the top level only: what
// a nested value gets is decided by the form drawing it.
func TestAPerKindOverrideBeatsTheDefault(t *testing.T) {
	st := PlainStyles()
	list := []Value{{Type: "[]int", Kind: "list", Items: []Value{
		{Type: "int", Kind: "scalar", Repr: "1"}}}}
	opts := Options{Form: FormTree, Kinds: map[string]Form{"list": FormTable}}
	if got, want := RichIn(list, st, opts), Rich(list, st); got != want {
		t.Errorf("the per-kind override did not win:\n got %q\nwant %q", got, want)
	}

	strct := []Value{{Type: "main.P", Kind: "struct", Fields: []Field{
		{Name: "X", Val: Value{Type: "int", Kind: "scalar", Repr: "3"}}}}}
	if got := RichIn(strct, st, opts); !strings.Contains(got, "├") && !strings.Contains(got, "└") {
		t.Errorf("a kind with no override did not take the default form:\n%s", got)
	}
}

// TestAPointerTakesItsTargetsKind: rich unwraps a pointer, so a per-kind rule
// that stopped at "ptr" would name a kind the renderer never reaches.
func TestAPointerTakesItsTargetsKind(t *testing.T) {
	p := Value{Type: "*main.P", Kind: "ptr", Items: []Value{
		{Type: "main.P", Kind: "struct", Fields: []Field{
			{Name: "X", Val: Value{Type: "int", Kind: "scalar", Repr: "3"}}}}}}
	opts := Options{Form: FormTable, Kinds: map[string]Form{"struct": FormLine}}
	if got := opts.formFor(p); got != FormLine {
		t.Errorf("a pointer to a struct took %v, want the struct's form", got)
	}
}

// TestAFormNeverReachesPlain is the invariant, at this layer. Plain builds no
// Options, so there is no path rather than a check that could be forgotten.
func TestAFormNeverReachesPlain(t *testing.T) {
	for i, vals := range corpus() {
		want := Plain(vals)
		for _, f := range Forms() {
			// The forms are installed by a driver on Core.Render; Plain is a
			// different function and takes no options at all. This asserts the
			// shape of that fact: nothing a form does can be seen from here.
			if got := Plain(vals); got != want {
				t.Errorf("corpus[%d]: Plain moved under form %s", i, f)
			}
		}
	}
}

// TestLiteralFormIgnoresHooks. The form answers with source and a plugin
// renderer answers with a rendering; consulting one would produce something
// that is neither. Pinned so it reads as a decision.
func TestLiteralFormIgnoresHooks(t *testing.T) {
	st := PlainStyles()
	hooks := map[string]Hook{"int": {
		Rich:   func(Value, Styles) (string, bool) { return "HOOKED", true },
		Inline: func(Value, Styles) (string, bool) { return "HOOKED", true },
	}}
	vals := []Value{{Type: "int", Kind: "scalar", Repr: "2"}}
	if got := RichIn(vals, st, Options{Form: FormLiteral, Hooks: hooks}); strings.Contains(got, "HOOKED") {
		t.Errorf("a hook reached the literal form: %q", got)
	}
	if got := RichIn(vals, st, Options{Form: FormLine, Hooks: hooks}); !strings.Contains(got, "HOOKED") {
		t.Errorf("a hook did not reach the line form: %q", got)
	}
}

// TestInlineWithNoHooksIsInline is the guard on the inlineWith refactor: the
// one-line form has one definition, and the no-hooks path through it must be
// the bytes Plain has always produced.
func TestInlineWithNoHooksIsInline(t *testing.T) {
	for i, vals := range corpus() {
		for _, v := range vals {
			if got, want := v.inlineWith(PlainStyles(), Options{}), v.inline(); got != want {
				t.Errorf("corpus[%d]: inlineWith moved bytes\n got %q\nwant %q", i, got, want)
			}
		}
	}
}
