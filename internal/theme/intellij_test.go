package theme

import (
	"encoding/xml"
	"strings"
	"testing"
)

// scheme is the shape of a real IntelliJ colour scheme, cut down to what the
// importer reads: a <colors> block of editor colours, an <attributes> block of
// syntax ones, an alias, and the stray characters real files carry.
const scheme = `<?xml version="1.0" encoding="UTF-8"?>
<scheme name="Test Scheme" version="142" parent_scheme="Darcula">
    <!--a comment, which every published scheme has-->
    <colors>
        <option name="CARET_COLOR" value="7DAEA3" />
        <option name="CARET_ROW_COLOR" value="282828" />"
        <option name="LINE_NUMBERS_COLOR" value="5A524C" />
        <option name="LINE_NUMBER_ON_CARET_ROW_COLOR" value="A69A8A" />
        <option name="TEARLINE_COLOR" value="32302F" />
        <option name="INDENT_GUIDE" value="32302F50" />
    </colors>
    <attributes>
        <option name="TEXT">
            <value>
                <option name="FOREGROUND" value="D4BE98" />
                <option name="BACKGROUND" value="1D2021" />
            </value>
        </option>
        <option name="DEFAULT_KEYWORD">
            <value>
                <option name="FOREGROUND" value="C28FD6" />
            </value>
        </option>
        <option name="DEFAULT_STRING">
            <value>
                <option name="FOREGROUND" value="A9B665" />
            </value>
        </option>
        <option name="DEFAULT_CLASS_NAME">
            <value>
                <option name="FOREGROUND" value="D8A657" />
            </value>
        </option>
        <option name="DEFAULT_LINE_COMMENT">
            <value>
                <option name="FOREGROUND" value="6E655A" />
            </value>
        </option>
        <option name="DEFAULT_NUMBER">
            <value>
                <option name="FOREGROUND" value="E78A4E" />
            </value>
        </option>
        <option name="DEFAULT_FUNCTION_CALL">
            <value>
                <option name="FOREGROUND" value="7DAEA3" />
            </value>
        </option>
        <option name="DEFAULT_PARENTHS">
            <value>
                <option name="FOREGROUND" value="7C7269" />
            </value>
        </option>
        <option name="DEFAULT_OPERATION_SIGN">
            <value>
                <option name="FOREGROUND" value="8DBCB0" />
            </value>
        </option>
        <option name="DEFAULT_BRACES">
            <value>
                <option name="FOREGROUND" value="89B482" />
            </value>
        </option>
        <option name="CONSOLE_ERROR_OUTPUT">
            <value>
                <option name="FOREGROUND" value="EA6962" />
            </value>
        </option>
        <option name="CONSOLE_YELLOW_OUTPUT">
            <value>
                <option name="FOREGROUND" value="D8A657" />
            </value>
        </option>
        <option name="GO_KEYWORD" baseAttributes="DEFAULT_KEYWORD" />
    </attributes>
</scheme>`

// TestIntelliJSchemeImports is the mapping, end to end. The values are checked
// rather than the count: a converter whose output nobody looks at is a
// converter that can be wrong in silence.
func TestIntelliJSchemeImports(t *testing.T) {
	f, rep, err := Import("test", []byte(scheme))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	want := map[string]string{
		"keyword": "#C28FD6", "string": "#A9B665", "type": "#D8A657",
		"comment": "#6E655A", "number": "#E78A4E", "builtin": "#7DAEA3",
		"punctuation": "#7C7269", "ident": "#D4BE98",
		"error": "#EA6962", "note": "#D8A657",
		// The three an editor scheme can answer and a TextMate theme cannot.
		"prompt": "#7DAEA3", "dim": "#A69A8A", "border": "#5A524C",
		// gluon's own relationship, not the scheme's.
		"annotation": "#6E655A",
	}
	for role, v := range want {
		if got := f.Palette[role]; got != v {
			t.Errorf("%s = %q, want %q", role, got, v)
		}
	}
	if rep.Format != "IntelliJ, Darcula" {
		t.Errorf("format = %q", rep.Format)
	}
	if rep.Source != "Test Scheme" {
		t.Errorf("source = %q", rep.Source)
	}
	if f.About != "imported from Test Scheme" {
		t.Errorf("about = %q", f.About)
	}
}

// The operator colour is an accent in every scheme this was built against, and
// punctuation is on every line of a REPL. Same judgement as keyword.operator on
// the TextMate side, and the same test guarding it.
func TestIntelliJPunctuationIsNotTheOperatorAccent(t *testing.T) {
	f, _, err := Import("test", []byte(scheme))
	if err != nil {
		t.Fatal(err)
	}
	for _, accent := range []string{"#8DBCB0", "#89B482"} {
		if f.Palette["punctuation"] == accent {
			t.Errorf("punctuation took %s, an accent the scheme gives operators or braces", accent)
		}
	}
}

// search and mode are the roles no editor format can answer, so they keep
// gluon's own values and the report says which roles those were.
func TestIntelliJKeepsGluonsOwnWhereNothingMeansIt(t *testing.T) {
	base, _ := Builtin(Default)
	f, rep, err := Import("test", []byte(scheme))
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"search", "mode"} {
		if f.Palette[role] != base.Palette[role] {
			t.Errorf("%s = %q, want gluon's own %q", role, f.Palette[role], base.Palette[role])
		}
	}
	kept := map[string]bool{}
	for _, row := range rep.Rows {
		if row.Kept {
			kept[row.Role] = true
		}
	}
	if !kept["search"] || !kept["mode"] {
		t.Error("the report does not say search and mode were kept, so the guess is invisible")
	}
	if kept["prompt"] || kept["dim"] || kept["border"] {
		t.Error("a role the scheme actually decided is reported as gluon's own")
	}
	if rep.Mapped != len(Roles())-2 {
		t.Errorf("mapped %d of %d roles, want all but search and mode", rep.Mapped, len(Roles()))
	}
}

// An alias is a real part of the format — a language plugin says "my keyword is
// the default keyword" — and following it is what keeps a scheme that styles
// only through aliases from converting to nothing.
func TestIntelliJFollowsBaseAttributes(t *testing.T) {
	var s ijScheme
	if err := decodeScheme(t, scheme, &s); err != nil {
		t.Fatal(err)
	}
	if got := s.foregrounds()["GO_KEYWORD"]; got != "C28FD6" {
		t.Errorf("GO_KEYWORD resolved to %q, want the keyword it aliases", got)
	}
}

// A cycle in a file gluon did not write must not become a loop in gluon.
func TestIntelliJAliasCycleTerminates(t *testing.T) {
	const cyclic = `<scheme name="c"><attributes>
		<option name="A" baseAttributes="B" />
		<option name="B" baseAttributes="A" />
	</attributes></scheme>`
	var s ijScheme
	if err := decodeScheme(t, cyclic, &s); err != nil {
		t.Fatal(err)
	}
	if got := s.foregrounds(); len(got) != 0 {
		t.Errorf("a cycle resolved to %v", got)
	}
}

// The two XML dialects are told apart by their root element, not by their
// extension and not by a substring: a .tmTheme is full of the word "scheme"
// inside scope names.
func TestFormatIsDecidedByTheRootElement(t *testing.T) {
	cases := []struct {
		name string
		data string
		want format
	}{
		{"IntelliJ", scheme, formatIntelliJ},
		{"plist", `<?xml version="1.0"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" ` +
			`"http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict/></plist>`, formatPlist},
		{"json", `{"name":"x","tokenColors":[]}`, formatJSON},
		{"neither", `<html><body>scheme</body></html>`, formatUnknown},
		{"empty", "", formatUnknown},
	}
	for _, tc := range cases {
		if got := detect([]byte(tc.data)); got != tc.want {
			t.Errorf("%s: detect = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A file that is XML and nothing else says so by name rather than by producing
// a theme with every colour quietly missing.
func TestIntelliJRefusesASchemeWithNoColours(t *testing.T) {
	_, _, err := Import("test", []byte(`<scheme name="empty" />`))
	if err == nil {
		t.Fatal("an empty scheme converted to a theme")
	}
	if !strings.Contains(err.Error(), "nothing to convert") {
		t.Errorf("error = %q", err)
	}
}

// decodeScheme is the reader's own parse, for the two tests that are about
// what the format means rather than about what a palette becomes.
func decodeScheme(t *testing.T, data string, s *ijScheme) error {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(data))
	dec.Strict = false
	return dec.Decode(s)
}
