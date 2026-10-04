package cmdspec

import "testing"

var benchSpec = Spec{
	Kind: GoExpr,
	Params: []Param{
		{Name: "exp"},
		{Name: "other", Optional: true, Sep: ",", Help: "a second expression, measured beside the first"},
	},
	Flags: []Flag{{Name: "-count", Value: "n", Help: "run it n times"}, {Name: "-cpu", Value: "list"}, {Name: "-profile"}},
}

var implSpec = Spec{
	Kind: GoName,
	Params: []Param{
		{Name: "t", Help: "the type"},
		{Name: "i", Sep: ",", Help: "the interface, from anywhere the session can see"},
	},
}

// TestRestIsWhatIsLeft is the hint at each point of typing a line.
func TestRestIsWhatIsLeft(t *testing.T) {
	cases := []struct {
		name  string
		spec  Spec
		arg   string
		typed string
		want  string
	}{
		{"nothing typed", httpSpec, "<m> <url>", "", "<method> <url> [-H <header>]... [-d <body>] [-t <duration>]"},
		{"the method typed", httpSpec, "<m> <url>", "GET ", "<url> [-H <header>]... [-d <body>] [-t <duration>]"},
		{"typing the method", httpSpec, "<m> <url>", "GE", "<method> <url> [-H <header>]... [-d <body>] [-t <duration>]"},
		{"a flag's value", httpSpec, "<m> <url>", "GET u -t ", "<duration>"},
		{"a given flag drops out", httpSpec, "<m> <url>", "GET u -d x ", "[-H <header>]... [-t <duration>]"},
		{"on a flag", httpSpec, "<m> <url>", "GET u -d x -", "[-H <header>]... [-t <duration>]"},

		{"flags lead", benchSpec, "[flags] a[,b]", "", "[-count <n>] [-cpu <list>] [-profile] <exp>[, <other>]"},
		{"a flag given", benchSpec, "[flags] a[,b]", "-count 3 ", "[-cpu <list>] [-profile] <exp>[, <other>]"},
		{"the value of -count", benchSpec, "[flags] a[,b]", "-count ", "<n>  run it n times"},
		{"inside the operand", benchSpec, "[flags] a[,b]", "strings.Rep", ""},
		{"the second operand", benchSpec, "[flags] a[,b]", "f(1, 2), ", "[other]  a second expression, measured beside the first"},

		{"after the comma", implSpec, "<t>, <i>", "bytes.Buffer, ", "<i>  the interface, from anywhere the session can see"},
		{"before it", implSpec, "<t>, <i>", "bytes.Buffer", ""},

		{"a mode chosen", typeSpec, "[-v|-d] <exp>", "-v ", "<exp>"},
		{"past the grammar", scratchSpec, "[flag|name]", "a b", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.spec.Rest(c.arg, c.spec.At(c.arg, c.typed)); got != c.want {
				t.Errorf("Rest after %q\n got %q\nwant %q", c.typed, got, c.want)
			}
		})
	}
}

// TestCommaIndexSkipsNestedCommas: a comma inside a call, a literal or a
// string is not the separator.
func TestCommaIndexSkipsNestedCommas(t *testing.T) {
	cases := []struct {
		text string
		n    int
		open bool
	}{
		{"a", 0, false},
		{"a, ", 1, true},
		{"a, b", 1, false},
		{"f(1, 2), ", 1, true},
		{`strings.Split("a,b", ","), `, 1, true},
		{"[]int{1, 2}", 0, false},
	}
	for _, c := range cases {
		if n, open := commaIndex(c.text); n != c.n || open != c.open {
			t.Errorf("commaIndex(%q) = %d, %v; want %d, %v", c.text, n, open, c.n, c.open)
		}
	}
}
