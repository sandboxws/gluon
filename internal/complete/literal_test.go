package complete

import (
	"strings"
	"testing"
)

// literalAt is a scan rather than a parse, because the line is by definition
// incomplete. These are the cases where a scan can be fooled.

func TestLiteralAtFindsTheType(t *testing.T) {
	cases := map[string]string{
		"Point{":                    "Point",
		"p := Point{":               "Point",
		"p := Point{X: 1, ":         "Point",
		"pkg.Config{":               "pkg.Config",
		"x := []Point{Point{":       "Point",
		"Outer{Inner: Inner{":       "Inner",
		"f(Point{":                  "Point",
		"Point{X: 1, Y: 2}, Other{": "Other",
	}
	for line, want := range cases {
		lit, ok := literalAt(line)
		if !ok {
			t.Errorf("literalAt(%q) found no literal", line)
			continue
		}
		if lit.Type != want {
			t.Errorf("literalAt(%q).Type = %q, want %q", line, lit.Type, want)
		}
	}
}

// TestLiteralAtDeclines covers everything that has a brace but is not a struct
// literal with named fields.
func TestLiteralAtDeclines(t *testing.T) {
	for _, line := range []string{
		"",
		"x := 1",
		"Point{}",          // closed
		"if x {",           // a block
		"for {",            // a block
		"func() {",         // a func literal
		"type T struct {",  // a declaration
		"map[string]int{",  // elements are not named fields
		"[]int{",           // same
		"[]Point{",         // same: the brace follows a bracket
		"x := 5{",          // a digit cannot start a type
		`s := "Point{"`,    // a brace inside a string
		"s := `raw {`",     // and inside a raw string
		"c := '{'",         // and inside a rune literal
		"x := 1 // Point{", // and inside a comment
	} {
		if lit, ok := literalAt(line); ok {
			t.Errorf("literalAt(%q) claimed a literal of type %q", line, lit.Type)
		}
	}
}

// TestLiteralAtSkipsQuotedBraces is the specific failure a scan invites: a
// brace inside a string is not a brace.
func TestLiteralAtSkipsQuotedBraces(t *testing.T) {
	lit, ok := literalAt(`Point{Name: "a}b", `)
	if !ok {
		t.Fatal("a brace inside a string closed the literal")
	}
	if lit.Type != "Point" {
		t.Errorf("Type = %q", lit.Type)
	}
	if len(lit.Set) != 1 || lit.Set[0] != "Name" {
		t.Errorf("Set = %v, want [Name]", lit.Set)
	}

	// An escaped quote must not end the string early.
	if _, ok := literalAt(`Point{Name: "a\"}", `); !ok {
		t.Error("an escaped quote ended the string early")
	}
}

func TestKeysAlreadySet(t *testing.T) {
	cases := map[string][]string{
		"Point{":                        nil,
		"Point{X: 1, ":                  {"X"},
		"Point{X: 1, Y: 2, ":            {"X", "Y"},
		"Cfg{Name: \"a\", Port: 8080, ": {"Name", "Port"},
		// A nested literal's keys are its own business.
		"Outer{Inner: Inner{A: 1}, ": {"Inner"},
		// A key whose value is a call.
		"Cfg{Timeout: time.Second, ": {"Timeout"},
		// A slice value with its own braces.
		"Cfg{Tags: []string{\"a\"}, ": {"Tags"},
		// A map value with colons inside it.
		"Cfg{M: map[string]int{\"a\": 1}, ": {"M"},
	}
	for line, want := range cases {
		lit, ok := literalAt(line)
		if !ok {
			t.Errorf("literalAt(%q) found no literal", line)
			continue
		}
		if strings.Join(lit.Set, ",") != strings.Join(want, ",") {
			t.Errorf("literalAt(%q).Set = %v, want %v", line, lit.Set, want)
		}
	}
}

func TestFieldPrefix(t *testing.T) {
	cases := map[string]string{
		"Point{":        "",
		"Point{X":       "X",
		"Point{X: 1, ":  "",
		"Point{X: 1, Y": "Y",
		// A value being typed is not a key.
		"Point{X: fo":        "\x00",
		"Point{X: 1, Y: bar": "\x00",
	}
	for line, want := range cases {
		lit, ok := literalAt(line)
		if !ok {
			t.Errorf("literalAt(%q) found no literal", line)
			continue
		}
		if lit.Prefix != want {
			t.Errorf("literalAt(%q).Prefix = %q, want %q", line, lit.Prefix, want)
		}
	}
}

// TestSuggestCompletesFields is the whole point, through the public entry.
func TestSuggestCompletesFields(t *testing.T) {
	ctx := Context{
		Names: []string{"foo"},
		Literal: func(typ string) []string {
			if typ == "Point" {
				return []string{"X", "Y", "Label"}
			}
			return nil
		},
	}

	got := Suggest("p := Point{", ctx)
	want := []string{"p := Point{X: ", "p := Point{Y: ", "p := Point{Label: "}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("Suggest = %v\nwant %v", got, want)
	}

	// A field already set is not offered again.
	got = Suggest("p := Point{X: 1, ", ctx)
	for _, g := range got {
		if strings.HasSuffix(g, "X: ") {
			t.Errorf("offered X twice: %v", got)
		}
	}

	// And the partly typed name filters, case-sensitively.
	if got := Suggest("p := Point{La", ctx); len(got) != 1 || !strings.HasSuffix(got[0], "Label: ") {
		t.Errorf("prefix filter = %v, want just Label", got)
	}
	if got := Suggest("p := Point{la", ctx); len(got) != 0 {
		t.Errorf("lowercase prefix matched %v; Go is case-sensitive", got)
	}
}

// TestSuggestDoesNotCompleteFieldsForAValue. `Point{X: fo` is typing a value,
// and offering a field name there would produce a line that does not compile.
func TestSuggestDoesNotCompleteFieldsForAValue(t *testing.T) {
	ctx := Context{
		Literal: func(string) []string { return []string{"X", "Y"} },
	}
	if got := Suggest("p := Point{X: fo", ctx); len(got) != 0 {
		t.Errorf("offered field names where a value goes: %v", got)
	}
}

// TestSuggestFallsBackWhenTheTypeIsUnknown, rather than offering nothing at all
// inside every brace.
func TestSuggestFallsBackWhenTheTypeIsUnknown(t *testing.T) {
	ctx := Context{
		Names:   []string{"foo", "bar"},
		Literal: func(string) []string { return nil },
	}
	if got := Suggest("p := Unknown{fo", ctx); len(got) != 0 {
		t.Errorf("an unknown type offered %v", got)
	}
}
