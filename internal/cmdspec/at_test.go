package cmdspec

import (
	"reflect"
	"testing"
)

var httpSpec = Spec{
	Kind: Words, FlagsAt: After,
	Params: []Param{
		{Name: "method", Values: Values{Fixed: []string{"GET", "POST"}, Fold: true}},
		{Name: "url"},
	},
	Flags: []Flag{
		{Name: "-H", Value: "header", Repeat: true},
		{Name: "-d", Value: "body"},
		{Name: "-t", Value: "duration"},
	},
}

var typeSpec = Spec{
	Kind: GoExpr,
	Flags: []Flag{
		{Name: "-v", Mode: true},
		{Name: "-d", Mode: true, Runs: true},
	},
}

var scratchSpec = Spec{
	Kind:   Words,
	Params: []Param{{Name: "name", Optional: true}},
	Flags: []Flag{
		{Name: "-rm", Mode: true},
		{Name: "-off", Mode: true, Alone: true},
		{Name: "-force", Hidden: true},
	},
}

// TestAtClassifiesTheWord walks the positions completion and the hint ask
// about, including the edges the parsers were built around.
func TestAtClassifiesTheWord(t *testing.T) {
	cases := []struct {
		name  string
		spec  Spec
		arg   string
		typed string
		where Where
		word  string
		param string // the operand's name, for InOperand
		flag  string // the flag, for InFlagValue
	}{
		{"first operand", httpSpec, "<m> <url>", "G", InOperand, "G", "method", ""},
		{"second operand", httpSpec, "<m> <url>", "GET ", InOperand, "", "url", ""},
		{"flags follow the operands", httpSpec, "<m> <url>", "GET https://x -", OnFlag, "-", "", ""},
		{"no flag before them", httpSpec, "<m> <url>", "-", InOperand, "-", "method", ""},
		{"a flag's value", httpSpec, "<m> <url>", "GET https://x -t ", InFlagValue, "", "", "-t"},
		{"a value is consumed", httpSpec, "<m> <url>", "GET https://x -t 5s ", Nowhere, "", "", ""},
		{"inside a quote", httpSpec, "<m> <url>", "GET https://x -H 'Accept: ", InQuote, "'Accept: ", "", ""},
		{"a closed quote is a word", httpSpec, "<m> <url>", "GET https://x -H 'A: b' -", OnFlag, "-", "", ""},

		{"a leading flag on Go", typeSpec, "[-v|-d] <exp>", "-", OnFlag, "-", "", ""},
		{"-dx is Go", typeSpec, "[-v|-d] <exp>", "-dx", InOperand, "-dx", "exp", ""},
		{"Go after a flag", typeSpec, "[-v|-d] <exp>", "-v str", InOperand, "str", "exp", ""},
		{"Go is verbatim", typeSpec, "[-v|-d] <exp>", "a -v", InOperand, "-v", "exp", ""},

		{"a flag leads", scratchSpec, "[flag|name]", "-", OnFlag, "-", "", ""},
		{"the name after -rm", scratchSpec, "[flag|name]", "-rm o", InOperand, "o", "name", ""},
		{"past the grammar", scratchSpec, "[flag|name]", "one two", Nowhere, "two", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			at := c.spec.At(c.arg, c.typed)
			if at.Where != c.where || at.Word != c.word {
				t.Fatalf("At(%q) = where %d word %q, want where %d word %q", c.typed, at.Where, at.Word, c.where, c.word)
			}
			if c.param != "" && (at.Param == nil || at.Param.Name != c.param) {
				t.Errorf("At(%q) operand = %+v, want %s", c.typed, at.Param, c.param)
			}
			if c.flag != "" && at.Flag.Name != c.flag {
				t.Errorf("At(%q) flag = %q, want %s", c.typed, at.Flag.Name, c.flag)
			}
			if at.Start+len(at.Word) != len(c.typed) && at.Word != "" {
				t.Errorf("At(%q) word %q does not end the line (start %d)", c.typed, at.Word, at.Start)
			}
		})
	}
}

// TestFlagsForOffersWhatIsLeft: a flag given is not offered again unless it
// repeats, a second mode is not offered once one is chosen, a flag that is the
// whole argument only on an empty line, and never a hidden one.
func TestFlagsForOffersWhatIsLeft(t *testing.T) {
	got := httpSpec.FlagsFor(httpSpec.At("<m> <url>", "GET https://x -H 'A: b' -t 5s -"))
	if want := []string{"-H ", "-d "}; !reflect.DeepEqual(got, want) {
		t.Errorf("after -H and -t: %v, want %v", got, want)
	}
	if got := typeSpec.FlagsFor(typeSpec.At("[-v|-d] <exp>", "-v -")); len(got) != 0 {
		t.Errorf("a second mode was offered: %v", got)
	}
	if got := scratchSpec.FlagsFor(scratchSpec.At("[flag|name]", "-")); !reflect.DeepEqual(got, []string{"-rm", "-off"}) {
		t.Errorf("an empty :scratch line: %v, want -rm and -off, and never -force", got)
	}
}
