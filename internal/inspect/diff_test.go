package inspect

import (
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/pretty"
)

func str(s string) pretty.Value   { return pretty.Value{Type: "string", Kind: "string", Repr: s} }
func num(s string) pretty.Value   { return pretty.Value{Type: "int", Kind: "scalar", Repr: s} }
func boolv(s string) pretty.Value { return pretty.Value{Type: "bool", Kind: "scalar", Repr: s} }

func structv(typ string, fields ...pretty.Field) pretty.Value {
	return pretty.Value{Type: typ, Kind: "struct", Fields: fields}
}

func fld(name string, v pretty.Value) pretty.Field { return pretty.Field{Name: name, Val: v} }

func listv(typ string, items ...pretty.Value) pretty.Value {
	n := len(items)
	return pretty.Value{Type: typ, Kind: "list", Len: &n, Items: items}
}

func mapv(typ string, kv ...pretty.Value) pretty.Value {
	v := pretty.Value{Type: typ, Kind: "map"}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Keys = append(v.Keys, kv[i])
		v.Items = append(v.Items, kv[i+1])
	}
	n := len(v.Keys)
	v.Len = &n
	return v
}

var names = []string{"want", "got"}

// find returns the diff at a path, so a test asserts on the row it means
// rather than on the order the walk happened to produce.
func find(r DiffResult, path string) (Diff, bool) {
	for _, d := range r.Diffs {
		if d.Path == path {
			return d, true
		}
	}
	return Diff{}, false
}

// TestDiffTwoStructsDifferingInOneField: one row, naming the field, and the
// nineteen fields that match are not in the report at all.
func TestDiffTwoStructsDifferingInOneField(t *testing.T) {
	a := structv("main.User", fld("Name", str("ana")), fld("Age", num("30")), fld("OK", boolv("true")))
	b := structv("main.User", fld("Name", str("bob")), fld("Age", num("30")), fld("OK", boolv("true")))

	r := DiffValues(a, b)
	if len(r.Diffs) != 1 {
		t.Fatalf("got %d differences, want 1: %+v", len(r.Diffs), r.Diffs)
	}
	d := r.Diffs[0]
	if d.Path != ".Name" {
		t.Errorf("path = %q, want .Name", d.Path)
	}
	if d.A != `"ana"` || d.B != `"bob"` {
		t.Errorf("sides = %q and %q, want the two names", d.A, d.B)
	}
	out := PlainDiff(names, r)
	for _, absent := range []string{".Age", ".OK"} {
		if strings.Contains(out, absent) {
			t.Errorf("the report lists the matching field %s:\n%s", absent, out)
		}
	}
}

// TestDiffIdenticalValuesSaysSo: empty output would read as a command that did
// nothing.
func TestDiffIdenticalValuesSaysSo(t *testing.T) {
	v := structv("main.User", fld("Name", str("ana")), fld("Tags", listv("[]string", str("a"), str("b"))))
	r := DiffValues(v, v)
	if !r.Identical() {
		t.Fatalf("a value differs from itself: %+v", r.Diffs)
	}
	for _, out := range []string{PlainDiff(names, r), stripANSI(RenderDiff(names, r, pretty.PlainStyles()))} {
		if strings.TrimSpace(out) == "" {
			t.Fatal("identical values produced empty output")
		}
		if !strings.Contains(out, "identical") {
			t.Errorf("the report does not say they are identical:\n%s", out)
		}
	}
}

// TestDiffTwoSequencesDifferingAtOneIndex.
func TestDiffTwoSequencesDifferingAtOneIndex(t *testing.T) {
	a := listv("[]int", num("1"), num("2"), num("3"))
	b := listv("[]int", num("1"), num("9"), num("3"))

	r := DiffValues(a, b)
	if len(r.Diffs) != 1 {
		t.Fatalf("got %d differences, want 1: %+v", len(r.Diffs), r.Diffs)
	}
	d := r.Diffs[0]
	if d.Path != "[1]" {
		t.Errorf("path = %q, want [1]", d.Path)
	}
	if d.A != "2" || d.B != "9" {
		t.Errorf("sides = %q and %q, want 2 and 9", d.A, d.B)
	}
}

// TestDiffDifferentTypesStopsThere: naming both types is the answer; walking
// into them would call every field different and say nothing.
func TestDiffDifferentTypesStopsThere(t *testing.T) {
	a := structv("main.User", fld("Name", str("ana")))
	b := structv("main.Admin", fld("Name", str("bob")))

	r := DiffValues(a, b)
	if len(r.Diffs) != 1 {
		t.Fatalf("got %d differences, want 1: %+v", len(r.Diffs), r.Diffs)
	}
	if r.Diffs[0].Kind != DiffType {
		t.Errorf("kind = %v, want DiffType", r.Diffs[0].Kind)
	}
	out := PlainDiff(names, r)
	for _, want := range []string{"main.User", "main.Admin", "types differ"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, ".Name") {
		t.Errorf("the report compared the contents anyway:\n%s", out)
	}
}

// TestDiffNestedTypeDifferenceIsARow: only the root case is a sentence.
func TestDiffNestedTypeDifferenceIsARow(t *testing.T) {
	a := structv("main.Box", fld("V", pretty.Value{Type: "int", Kind: "scalar", Repr: "1"}))
	b := structv("main.Box", fld("V", pretty.Value{Type: "string", Kind: "string", Repr: "1"}))

	r := DiffValues(a, b)
	d, ok := find(r, ".V")
	if !ok {
		t.Fatalf("no difference at .V: %+v", r.Diffs)
	}
	if d.Kind != DiffType || d.A != "int" || d.B != "string" {
		t.Errorf("got %+v, want a type difference naming int and string", d)
	}
	if out := PlainDiff(names, r); !strings.Contains(out, "type") {
		t.Errorf("the row is not marked as a type difference:\n%s", out)
	}
}

// TestDiffKeyOnOneSideOnly.
func TestDiffKeyOnOneSideOnly(t *testing.T) {
	a := mapv("map[string]int", str("a"), num("1"), str("b"), num("2"))
	b := mapv("map[string]int", str("a"), num("1"))

	r := DiffValues(a, b)
	d, ok := find(r, `["b"]`)
	if !ok {
		t.Fatalf(`no difference at ["b"]: %+v`, r.Diffs)
	}
	if d.Kind != DiffOnlyA {
		t.Errorf("kind = %v, want DiffOnlyA", d.Kind)
	}
	out := PlainDiff(names, r)
	if !strings.Contains(out, "only in want") {
		t.Errorf("the report does not say which side has it:\n%s", out)
	}
	// The key is the whole answer: a "1 key against 2" row beside it would
	// restate what naming the key already said. A sequence is the case where
	// the length is its own fact, because an off-by-one shifts every position
	// after it.
	if len(r.Diffs) != 1 {
		t.Errorf("got %d differences, want just the key: %+v", len(r.Diffs), r.Diffs)
	}
}

// TestDiffKeyOnTheOtherSideOnly walks the merge the other way, and past a key
// both sides have.
func TestDiffKeyOnTheOtherSideOnly(t *testing.T) {
	a := mapv("map[string]int", str("a"), num("1"))
	b := mapv("map[string]int", str("a"), num("1"), str("z"), num("9"))

	r := DiffValues(a, b)
	d, ok := find(r, `["z"]`)
	if !ok {
		t.Fatalf(`no difference at ["z"]: %+v`, r.Diffs)
	}
	if d.Kind != DiffOnlyB || d.B != "9" {
		t.Errorf("got %+v, want the value present only in got", d)
	}
	if out := PlainDiff(names, r); !strings.Contains(out, "only in got") {
		t.Errorf("the report does not say which side has it:\n%s", out)
	}
}

// TestDiffSequencesOfDifferentLength: the lengths as a structural fact, and
// the positions that actually differ as well.
func TestDiffSequencesOfDifferentLength(t *testing.T) {
	a := listv("[]int", num("1"), num("2"))
	b := listv("[]int", num("1"), num("7"), num("3"))

	r := DiffValues(a, b)
	root, ok := find(r, "")
	if !ok {
		t.Fatalf("the length difference was not reported: %+v", r.Diffs)
	}
	if root.Kind != DiffLen || root.A != "2" || root.B != "3" {
		t.Errorf("got %+v, want the two lengths", root)
	}
	if d, ok := find(r, "[1]"); !ok || d.Kind != DiffValue {
		t.Errorf("the differing position was not reported: %+v", r.Diffs)
	}
	if d, ok := find(r, "[2]"); !ok || d.Kind != DiffOnlyB {
		t.Errorf("the extra element was not reported: %+v", r.Diffs)
	}
	out := PlainDiff(names, r)
	for _, want := range []string{"length", "only in got"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report lacks %q:\n%s", want, out)
		}
	}
}

// TestDiffTruncationIsReportedFromAnyDepth is the honesty requirement. The cap
// is set on a nested node, not the root, because checking only the top would
// pass a test built the other way and still lie in practice.
func TestDiffTruncationIsReportedFromAnyDepth(t *testing.T) {
	capped := listv("[]int", num("1"), num("2"))
	capped.More = 198
	n := 200
	capped.Len = &n

	a := structv("main.Big", fld("Name", str("ana")), fld("Items", capped))
	b := structv("main.Big", fld("Name", str("bob")), fld("Items", capped))

	r := DiffValues(a, b)
	if !r.Truncated {
		t.Fatal("a cap two levels down was not noticed")
	}
	for _, out := range []string{PlainDiff(names, r), stripANSI(RenderDiff(names, r, pretty.PlainStyles()))} {
		if !strings.Contains(out, "only the part that was described") {
			t.Errorf("the report does not state the limitation:\n%s", out)
		}
	}

	// And when nothing else differs, "identical" still carries the caveat —
	// which is the whole point of tracking it.
	same := DiffValues(a, a)
	if !same.Truncated {
		t.Fatal("the cap went unnoticed when the values matched")
	}
	out := PlainDiff(names, same)
	if !strings.Contains(out, "identical") || !strings.Contains(out, "only the part that was described") {
		t.Errorf("an identical-but-capped comparison overclaims:\n%s", out)
	}
}

// TestDiffNilAgainstEmpty: the same type, different shapes. A nil slice and an
// empty one are a real distinction and the report keeps it.
func TestDiffNilAgainstEmpty(t *testing.T) {
	a := pretty.Value{Type: "[]int", Kind: "nil", Repr: "nil"}
	b := listv("[]int")

	r := DiffValues(a, b)
	if len(r.Diffs) != 1 || r.Diffs[0].Kind != DiffValue {
		t.Fatalf("got %+v, want one value difference", r.Diffs)
	}
	if r.Diffs[0].A != "nil" {
		t.Errorf("left side = %q, want nil", r.Diffs[0].A)
	}
}

// TestDiffFollowsPointersWithoutNamingThem: Go's selector derefs on its own,
// so the path a reader can type is the path through the pointee.
func TestDiffFollowsPointersWithoutNamingThem(t *testing.T) {
	ptr := func(name string) pretty.Value {
		return pretty.Value{Type: "*main.User", Kind: "ptr", Items: []pretty.Value{
			structv("main.User", fld("Name", str(name))),
		}}
	}
	r := DiffValues(ptr("ana"), ptr("bob"))
	if _, ok := find(r, ".Name"); !ok {
		t.Errorf("path through a pointer is not a Go selector: %+v", r.Diffs)
	}
}

// TestRenderDiffFormsAgree: the rich table and the plain form report the same
// paths, because Result.Out carries the plain one through a pipe and a script
// reading it must see what the terminal shows.
func TestRenderDiffFormsAgree(t *testing.T) {
	a := structv("main.User", fld("Name", str("ana")), fld("Tags", listv("[]string", str("x"))))
	b := structv("main.User", fld("Name", str("bob")), fld("Tags", listv("[]string", str("x"), str("y"))))

	r := DiffValues(a, b)
	plain := PlainDiff(names, r)
	rich := stripANSI(RenderDiff(names, r, pretty.PlainStyles()))
	for _, want := range []string{".Name", `"ana"`, `"bob"`, ".Tags", "length", ".Tags[1]", "only in got"} {
		if !strings.Contains(plain, want) {
			t.Errorf("plain form lacks %q:\n%s", want, plain)
		}
		if !strings.Contains(rich, want) {
			t.Errorf("rich form lacks %q:\n%s", want, rich)
		}
	}
	// Both name the operands rather than a and b.
	for _, out := range []string{plain, rich} {
		if !strings.Contains(out, "want") || !strings.Contains(out, "got") {
			t.Errorf("the columns are not named after the operands:\n%s", out)
		}
	}
}

// TestDiffResolvesBackReferences is the case that made :diff report a value as
// differing from itself. Both operands travel in one payload, so the encoder
// writes the second occurrence of an object as a reference to the first —
// which is what `:diff xs, xs` and `y := x; :diff x, y` both produce.
func TestDiffResolvesBackReferences(t *testing.T) {
	target := listv("[]int", num("1"), num("2"))
	target.ID = 1
	ref := pretty.Value{Type: "[]int", Kind: "shared", ID: 1}

	if r := DiffValues(target, ref); !r.Identical() {
		t.Errorf("a value differs from a reference to itself: %+v", r.Diffs)
	}
	if r := DiffValues(ref, target); !r.Identical() {
		t.Errorf("resolution does not work from the left: %+v", r.Diffs)
	}

	// A reference nested inside a struct resolves too.
	a := structv("main.Box", fld("Items", target))
	b := structv("main.Box", fld("Items", ref))
	if r := DiffValues(a, b); !r.Identical() {
		t.Errorf("a nested reference was not resolved: %+v", r.Diffs)
	}

	// A reference to something neither tree holds stays a reference rather
	// than being silently called equal.
	dangling := pretty.Value{Type: "[]int", Kind: "shared", ID: 99}
	if r := DiffValues(target, dangling); r.Identical() {
		t.Error("a reference to nothing was treated as a match")
	}
}

// TestDiffComparesCyclesByIdentity: a cycle cannot be followed, so what is
// left is whether the two point back at the same object — which is answerable
// because one encoding numbered both.
func TestDiffComparesCyclesByIdentity(t *testing.T) {
	cyc := func(id int) pretty.Value {
		return structv("main.Node", fld("Next", pretty.Value{Type: "*main.Node", Kind: "cycle", ID: id}))
	}
	if r := DiffValues(cyc(1), cyc(1)); !r.Identical() {
		t.Errorf("two cycles onto the same object differ: %+v", r.Diffs)
	}
	r := DiffValues(cyc(1), cyc(2))
	if d, ok := find(r, ".Next"); !ok || d.A != "<cycle #1>" || d.B != "<cycle #2>" {
		t.Errorf("cycles onto different objects were not reported: %+v", r.Diffs)
	}
}
