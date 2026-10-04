package pretty

import (
	"strings"
	"testing"
)

func scalar(t, r string) Value { return Value{Type: t, Kind: "scalar", Repr: r} }

// TestLiteralWritesTheShapesItCan walks the value kinds the encoder produces
// for something that does have a literal form. The expectation is the source
// text, not a parse: the generated test is read by a person before it is kept,
// so the spelling is part of what is being tested.
func TestLiteralWritesTheShapesItCan(t *testing.T) {
	tests := []struct {
		name string
		in   Value
		want string
	}{
		{"int", scalar("int", "42"), "42"},
		{"negative", scalar("int", "-7"), "-7"},
		{"float", scalar("float64", "3.25"), "3.25"},
		{"bool", scalar("bool", "true"), "true"},
		// A type an untyped constant would not default to is converted, or
		// `want` and `got` would not even be the same type.
		{"int64", scalar("int64", "42"), "int64(42)"},
		{"named", scalar("main.Celsius", "36.6"), "Celsius(36.6)"},
		{"duration", scalar("time.Duration", "5000000"), "time.Duration(5000000)"},
		// The two numbers the encoder glosses for display.
		{"byte", scalar("uint8", `195 (0xc3)`), "uint8(195)"},
		{"rune", scalar("int32", `99 'c'`), "int32(99)"},
		{"string", Value{Type: "string", Kind: "string", Repr: `a "b"`}, `"a \"b\""`},
		{"nil slice", Value{Type: "[]int", Kind: "nil", Repr: "nil"}, "([]int)(nil)"},
		{"nil pointer", Value{Type: "*main.Point", Kind: "nil", Repr: "nil"}, "(*Point)(nil)"},
		{"erased nil", Value{Type: "", Kind: "nil", Repr: "nil"}, "nil"},
		{
			"slice",
			Value{Type: "[]int", Kind: "list", Items: []Value{scalar("int", "1"), scalar("int", "2")}},
			"[]int{1, 2}",
		},
		{"empty slice", Value{Type: "[]int", Kind: "list", Items: []Value{}}, "[]int{}"},
		{
			"array",
			Value{Type: "[2]string", Kind: "list", Items: []Value{
				{Type: "string", Kind: "string", Repr: "a"},
				{Type: "string", Kind: "string", Repr: "b"},
			}},
			`[2]string{"a", "b"}`,
		},
		{
			"map",
			Value{Type: "map[string]int", Kind: "map",
				Keys:  []Value{{Type: "string", Kind: "string", Repr: "a"}},
				Items: []Value{scalar("int", "1")}},
			`map[string]int{"a": 1}`,
		},
		{
			"struct",
			Value{Type: "main.Point", Kind: "struct", Fields: []Field{
				{Name: "X", Val: scalar("int", "1")},
				{Name: "Y", Val: scalar("int", "2")},
			}},
			"Point{X: 1, Y: 2}",
		},
		{
			"nested struct",
			Value{Type: "main.Box", Kind: "struct", Fields: []Field{
				{Name: "At", Val: Value{Type: "main.Point", Kind: "struct", Fields: []Field{
					{Name: "X", Val: scalar("int", "1")},
				}}},
				{Name: "Tags", Val: Value{Type: "[]string", Kind: "list", Items: []Value{
					{Type: "string", Kind: "string", Repr: "t"},
				}}},
			}},
			`Box{At: Point{X: 1}, Tags: []string{"t"}}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Literal(tc.in)
			if err != nil {
				t.Fatalf("Literal: %v", err)
			}
			if got != tc.want {
				t.Errorf("Literal = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestLiteralRefusesWhatItCannotWrite is the other half, and the one the whole
// design turns on: a refusal that names the component beats source that does
// not compile, and beats a placeholder that makes a failing test look
// generated.
func TestLiteralRefusesWhatItCannotWrite(t *testing.T) {
	ptr := Value{Type: "*main.Point", Kind: "ptr", Items: []Value{
		{Type: "main.Point", Kind: "struct", Fields: []Field{{Name: "X", Val: scalar("int", "1")}}},
	}}
	tests := []struct {
		name     string
		in       Value
		wantPath string
		wantWhy  string
	}{
		{"pointer", ptr, "", "pointer"},
		{"channel", scalar("chan int", "chan int"), "", "channel"},
		{"receive-only channel", scalar("<-chan int", "<-chan int"), "", "channel"},
		{"func", scalar("func(int) int", "func(int) int"), "", "func"},
		{"address", scalar("uintptr", "824633720832"), "", "address"},
		{"cycle", Value{Type: "*main.Node", Kind: "cycle", ID: 1}, "", "refers back"},
		{"shared", Value{Type: "main.Point", Kind: "shared", ID: 2}, "", "same object"},
		{"unprintable", scalar("?", "<unprintable: boom>"), "", "not a Go literal"},
		{"not a number", scalar("float64", "+Inf"), "", "not a Go literal"},
		{
			"truncated list",
			Value{Type: "[]int", Kind: "list", Items: []Value{scalar("int", "1")}, More: 300},
			"", "more items than were sent",
		},
		{
			"past the depth cap",
			Value{Type: "main.Deep", Kind: "struct", Repr: "{…}"},
			"", "nested deeper",
		},
		// The nested cases: the refusal is only actionable if it says which
		// part of the value caused it.
		{
			"pointer in a struct",
			Value{Type: "main.Box", Kind: "struct", Fields: []Field{
				{Name: "X", Val: scalar("int", "1")},
				{Name: "At", Val: ptr},
			}},
			".At", "pointer",
		},
		{
			"channel in a slice",
			Value{Type: "[]chan int", Kind: "list", Items: []Value{
				scalar("chan int", "chan int"),
			}},
			"[0]", "channel",
		},
		{
			"func under a map key",
			Value{Type: "map[string]func()", Kind: "map",
				Keys:  []Value{{Type: "string", Kind: "string", Repr: "run"}},
				Items: []Value{scalar("func()", "func()")}},
			`["run"]`, "func",
		},
		{
			"unexported field of another package's type",
			Value{Type: "time.Time", Kind: "struct", Fields: []Field{
				{Name: "wall", Val: scalar("uint64", "0")},
			}},
			".wall", "unexported",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src, err := Literal(tc.in)
			if err == nil {
				t.Fatalf("Literal returned %q, want a refusal", src)
			}
			if src != "" {
				t.Errorf("Literal returned source %q alongside a refusal", src)
			}
			var nl *NoLiteral
			if !asNoLiteral(err, &nl) {
				t.Fatalf("error is %T, want *NoLiteral", err)
			}
			if nl.Path != tc.wantPath {
				t.Errorf("path = %q, want %q", nl.Path, tc.wantPath)
			}
			if !strings.Contains(nl.Why, tc.wantWhy) {
				t.Errorf("reason = %q, want it to mention %q", nl.Why, tc.wantWhy)
			}
		})
	}
}

func asNoLiteral(err error, out **NoLiteral) bool {
	nl, ok := err.(*NoLiteral)
	if ok {
		*out = nl
	}
	return ok
}

// TestUnexportedFieldOfTheSessionsOwnType is the other side of the unexported
// rule: the generated file is package main beside the session's program, so a
// type the session declared can name its own unexported fields there.
func TestUnexportedFieldOfTheSessionsOwnType(t *testing.T) {
	got, err := Literal(Value{Type: "main.counter", Kind: "struct", Fields: []Field{
		{Name: "n", Val: scalar("int", "3")},
	}})
	if err != nil {
		t.Fatalf("Literal: %v", err)
	}
	if got != "counter{n: 3}" {
		t.Errorf("Literal = %q, want %q", got, "counter{n: 3}")
	}
}
