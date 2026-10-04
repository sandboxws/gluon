package complete

import (
	"strings"
	"testing"
)

func ctx() Context {
	return Context{
		Metas: []string{":type", ":methods", ":ls", ":time"},
		Names: []string{"count", "counter", "Point", "dist", "_1", "it"},
		Packages: map[string]string{
			"strings": "strings",
			"slices":  "slices",
			"trace":   "example.com/proj/internal/trace",
		},
		Members: func(p string) []string {
			switch p {
			case "strings":
				return []string{"Builder", "ToLower", "ToTitle", "ToUpper", "TrimSpace"}
			case "example.com/proj/internal/trace":
				return []string{"New", "Step", "Tracer"}
			}
			return nil
		},
		Fields: func(expr string) []string {
			if expr == "p" {
				return []string{"Dist", "X", "Y"}
			}
			return nil
		},
	}
}

func has(got []string, want string) bool {
	for _, g := range got {
		if g == want {
			return true
		}
	}
	return false
}

func TestCompletesMetaCommands(t *testing.T) {
	got := Suggest(":t", ctx())
	if !has(got, ":type") || !has(got, ":time") {
		t.Errorf("Suggest(\":t\") = %v", got)
	}
	// Once there is an argument the line is ordinary Go again.
	if got := Suggest(":t co", ctx()); !has(got, ":t count") {
		t.Errorf("Suggest(\":t co\") = %v", got)
	}
}

func TestCompletesPackageMembers(t *testing.T) {
	got := Suggest(`x := strings.To`, ctx())
	for _, want := range []string{"x := strings.ToLower", "x := strings.ToTitle", "x := strings.ToUpper"} {
		if !has(got, want) {
			t.Errorf("missing %q in %v", want, got)
		}
	}
	if has(got, "x := strings.TrimSpace") {
		t.Errorf("offered a non-matching member: %v", got)
	}
	// The whole line comes back, because that is what textinput matches on.
	for _, g := range got {
		if !strings.HasPrefix(g, "x := strings.To") {
			t.Errorf("%q is not an extension of the line", g)
		}
	}
}

func TestCompletionIsCaseSensitive(t *testing.T) {
	// textinput keeps the typed prefix verbatim and appends the rest, so a
	// case-insensitive match would produce strings.toupper.
	if got := Suggest(`strings.tou`, ctx()); len(got) != 0 {
		t.Errorf("case-insensitive match offered: %v", got)
	}
}

func TestCompletesFieldsAndMethods(t *testing.T) {
	got := Suggest(`p.`, ctx())
	for _, want := range []string{"p.Dist", "p.X", "p.Y"} {
		if !has(got, want) {
			t.Errorf("missing %q in %v", want, got)
		}
	}
	if got := Suggest(`p.D`, ctx()); !has(got, "p.Dist") || has(got, "p.X") {
		t.Errorf("Suggest(\"p.D\") = %v", got)
	}
	// An expression nothing knows about yields nothing rather than guessing.
	if got := Suggest(`unknown.`, ctx()); len(got) != 0 {
		t.Errorf("Suggest(\"unknown.\") = %v", got)
	}
}

func TestPackageWinsOverFieldsForTheSameName(t *testing.T) {
	c := ctx()
	c.Fields = func(string) []string { return []string{"ShouldNotAppear"} }
	got := Suggest(`trace.`, c)
	if !has(got, "trace.New") {
		t.Errorf("package members missing: %v", got)
	}
	if has(got, "trace.ShouldNotAppear") {
		t.Errorf("a package qualifier was treated as a value: %v", got)
	}
}

func TestCompletesNamesPackagesAndKeywords(t *testing.T) {
	got := Suggest(`co`, ctx())
	for _, want := range []string{"count", "counter", "const", "continue", "complex", "copy"} {
		if !has(got, want) {
			t.Errorf("missing %q in %v", want, got)
		}
	}
	// A name identical to what is typed is not a completion.
	if got := Suggest(`count`, ctx()); has(got, "count") {
		t.Errorf("offered the typed word back: %v", got)
	}
	if got := Suggest(`str`, ctx()); !has(got, "strings") || !has(got, "string") {
		t.Errorf("Suggest(\"str\") = %v", got)
	}
}

func TestCompletesMidLine(t *testing.T) {
	// Only the trailing token is completed; everything before it is carried
	// through untouched.
	got := Suggest(`for i := range sli`, ctx())
	if !has(got, "for i := range slices") {
		t.Errorf("Suggest = %v", got)
	}
	got = Suggest(`fmt.Println(strings.ToU`, ctx())
	if !has(got, "fmt.Println(strings.ToUpper") {
		t.Errorf("Suggest = %v", got)
	}
}

func TestNoSuggestionsWhereThereIsNoToken(t *testing.T) {
	for _, line := range []string{"", "   ", "x := ", "1 + "} {
		if got := Suggest(line, ctx()); len(got) != 0 {
			t.Errorf("Suggest(%q) = %v", line, got)
		}
	}
}

func TestCapped(t *testing.T) {
	many := make([]string, Max*2)
	for i := range many {
		many[i] = "aaaa" + strings.Repeat("b", i%7) + string(rune('a'+i%26)) + itoa(i)
	}
	c := ctx()
	c.Names = many
	if got := Suggest("aaaa", c); len(got) > Max {
		t.Errorf("returned %d suggestions, cap is %d", len(got), Max)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
