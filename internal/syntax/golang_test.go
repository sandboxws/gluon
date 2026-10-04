package syntax

import (
	"strings"
	"testing"
)

// roleTrace renders the tokens of src as "role(text)" pairs, so a table test
// reads as what a person would say out loud about the line.
func roleTrace(lang Lang, src string) string {
	var parts []string
	for _, t := range Tokens(lang, src) {
		parts = append(parts, t.Role.String()+"("+quote(src[t.Start:t.End])+")")
	}
	return strings.Join(parts, " ")
}

func TestGoRoles(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"keywords", "for x := range y",
			"keyword(for) ident(x) punctuation(:=) keyword(range) ident(y)"},
		{"predeclared types", "var s string",
			"keyword(var) ident(s) type(string)"},
		{"predeclared constants", "x := nil == true",
			"ident(x) punctuation(:=) type(nil) punctuation(==) type(true)"},
		{"builtin when called", "n := len(s)",
			"ident(n) punctuation(:=) builtin(len) punctuation(() ident(s) punctuation())"},
		{"builtin shadowed as a name", "var len int",
			"keyword(var) ident(len) type(int)"},
		{"builtin as a selector", "x := s.len",
			"ident(x) punctuation(:=) ident(s) punctuation(.) ident(len)"},
		{"builtin across a comment", "len /*n*/ (x)",
			"builtin(len) comment(/*n*/) punctuation(() ident(x) punctuation())"},
		{"builtin across a newline is not a call", "len\n(x)",
			"ident(len) punctuation(() ident(x) punctuation())"},
		{"strings and runes", "a, b := \"s\", 'r'",
			`ident(a) punctuation(,) ident(b) punctuation(:=) string("s") punctuation(,) string('r')`},
		{"numbers", "a := 1 + 1.5 + 0x1f + 1e9 + 1i + 1_000",
			"ident(a) punctuation(:=) number(1) punctuation(+) number(1.5) punctuation(+) " +
				"number(0x1f) punctuation(+) number(1e9) punctuation(+) number(1i) punctuation(+) number(1_000)"},
		{"line comment", "x := 1 // why",
			"ident(x) punctuation(:=) number(1) comment(// why)"},
		{"operators", "x &^= 1",
			"ident(x) punctuation(&^=) number(1)"},
		{"type parameter tilde", "type N interface{ ~int }",
			"keyword(type) ident(N) keyword(interface) punctuation({) punctuation(~) type(int) punctuation(})"},
		{"generic constraint", "func F[T comparable]()",
			"keyword(func) ident(F) punctuation([) ident(T) type(comparable) punctuation(]) punctuation(() punctuation())"},
	} {
		if got := roleTrace(Go, tc.src); got != tc.want {
			t.Errorf("%s\n  src  %s\n  got  %s\n  want %s", tc.name, tc.src, got, tc.want)
		}
	}
}

// TestInsertedSemicolonIsNotPainted: go/scanner reports a SEMICOLON at the end
// of a line that has none. Painting it would put a character on screen that
// nobody typed.
func TestInsertedSemicolonIsNotPainted(t *testing.T) {
	for _, tok := range Tokens(Go, "x := 1\ny := 2\n") {
		if tok.Role == RolePunct && "x := 1\ny := 2\n"[tok.Start] == '\n' {
			t.Errorf("an inserted semicolon was painted at %d", tok.Start)
		}
	}
	got := roleTrace(Go, "x := 1\ny := 2")
	want := "ident(x) punctuation(:=) number(1) ident(y) punctuation(:=) number(2)"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// TestSemicolonInsideBlockCommentIsSkipped pins the case go/scanner documents:
// when a newline falls inside a /*...*/ comment the inserted semicolon is
// positioned at that newline, which is behind the comment token already
// emitted. A cursor that trusted it would slice backwards.
func TestSemicolonInsideBlockCommentIsSkipped(t *testing.T) {
	src := "x /*\n*/ ()"
	p := testPalette()
	if got := strip(Highlight(Go, src, p)); got != src {
		t.Fatalf("%s", firstDiff(src, got))
	}
	prev := 0
	for _, tok := range Tokens(Go, src) {
		if tok.Start < prev {
			t.Fatalf("token at %d is behind the previous end %d", tok.Start, prev)
		}
		prev = tok.End
	}
}

// TestIllegalRuneIsMeasuredNotLiteral: on invalid UTF-8 the scanner consumes
// one byte but reports utf8.RuneError, whose literal is three. Trusting the
// literal would swallow the two bytes after it.
func TestIllegalRuneIsMeasuredNotLiteral(t *testing.T) {
	for _, src := range []string{"\xff", "x := \xff\xfe\n", "\xffabc"} {
		p := testPalette()
		if got := strip(Highlight(Go, src, p)); got != src {
			t.Errorf("%q: %s", src, firstDiff(src, got))
		}
	}
}

// TestCommentAndRawStringMeasureFromSource: scanComment and scanRawString both
// strip carriage returns out of the literal they return, so len(lit)
// understates the token by one byte per CR.
func TestCommentAndRawStringMeasureFromSource(t *testing.T) {
	for _, src := range []string{"// hi\r\nx := 1\n", "s := `a\r\nb`\n", "/* a\r\nb */\n"} {
		p := testPalette()
		if got := strip(Highlight(Go, src, p)); got != src {
			t.Errorf("%q: %s", src, firstDiff(src, got))
		}
	}
}

// TestUnterminatedLiteralPaintsToEnd is what a half-typed REPL line is, and the
// feedback that explains why the prompt went to a continuation.
func TestUnterminatedLiteralPaintsToEnd(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`x := "abc`, `ident(x) punctuation(:=) string("abc)`},
		{"x := `abc", "ident(x) punctuation(:=) string(`abc)"},
		{"/* abc", "comment(/* abc)"},
		{"// abc", "comment(// abc)"},
	} {
		if got := roleTrace(Go, tc.src); got != tc.want {
			t.Errorf("%q\n  got  %s\n  want %s", tc.src, got, tc.want)
		}
	}
}

// TestTabsSurvive is the test that would have caught lipgloss.Style.Render,
// which converts a tab to four spaces on every path it has. gofmt indents with
// tabs, so this is :src's whole indentation.
func TestTabsSurvive(t *testing.T) {
	src := "func f() {\n\tif x {\n\t\ty()\n\t}\n}\n"
	out := Highlight(Go, src, testPalette())
	if strings.Contains(strip(out), "    ") {
		t.Error("a tab became spaces")
	}
	if strip(out) != src {
		t.Error(firstDiff(src, strip(out)))
	}
}

// TestMultiLineTokenClosesPerLine: a run left open across a newline paints the
// left margin of every line below it once anything downstream cuts the text
// into lines, which the pager and the input widget both do.
func TestMultiLineTokenClosesPerLine(t *testing.T) {
	p := testPalette()
	out := Highlight(Go, "s := `a\nb\nc`\n", p)
	for i, line := range strings.Split(out, "\n") {
		if strings.Count(line, p.Close) != strings.Count(line, p.Open[RoleString])+
			strings.Count(line, p.Open[RoleIdent])+strings.Count(line, p.Open[RolePunct]) {
			t.Errorf("line %d does not close every run it opens: %q", i, line)
		}
	}
}

func TestSQLRoles(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"select", "SELECT id FROM users WHERE id = 1",
			"keyword(SELECT) ident(id) keyword(FROM) ident(users) keyword(WHERE) " +
				"ident(id) punctuation(=) number(1)"},
		{"lowercase keywords", "select 1",
			"keyword(select) number(1)"},
		{"aggregate is a builtin only when called", "SELECT count(*), count FROM t",
			"keyword(SELECT) builtin(count) punctuation(() punctuation(*) punctuation()) " +
				"punctuation(,) ident(count) keyword(FROM) ident(t)"},
		{"escaped quote does not close", "SELECT 'it''s'",
			"keyword(SELECT) string('it''s')"},
		{"line comment", "SELECT 1 -- why",
			"keyword(SELECT) number(1) comment(-- why)"},
		{"bind markers", "WHERE a = $1 AND b = :name",
			"keyword(WHERE) ident(a) punctuation(=) punctuation($1) keyword(AND) " +
				"ident(b) punctuation(=) punctuation(:name)"},
		{"types", "id BIGINT, at TIMESTAMPTZ",
			"ident(id) type(BIGINT) punctuation(,) ident(at) type(TIMESTAMPTZ)"},
	} {
		if got := roleTrace(SQL, tc.src); got != tc.want {
			t.Errorf("%s\n  src  %s\n  got  %s\n  want %s", tc.name, tc.src, got, tc.want)
		}
	}
}

func TestJSONRoles(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"a key is not a value", `{"a": "b"}`,
			`punctuation({) type("a") punctuation(:) string("b") punctuation(})`},
		{"key with space before the colon", `{"a" : 1}`,
			`punctuation({) type("a") punctuation(:) number(1) punctuation(})`},
		{"literals", `[true, false, null]`,
			"punctuation([) keyword(true) punctuation(,) keyword(false) punctuation(,) keyword(null) punctuation(])"},
		{"negative exponent", `{"n": -2.5e3}`,
			`punctuation({) type("n") punctuation(:) number(-2.5e3) punctuation(})`},
		{"escaped quote inside a string", `{"a": "x\"y"}`,
			`punctuation({) type("a") punctuation(:) string("x\"y") punctuation(})`},
	} {
		if got := roleTrace(JSON, tc.src); got != tc.want {
			t.Errorf("%s\n  src  %s\n  got  %s\n  want %s", tc.name, tc.src, got, tc.want)
		}
	}
}

func TestTOMLRoles(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"table header and key", "[theme]\nname = \"go\"",
			`punctuation([) type(theme) punctuation(]) type(name) punctuation(=) string("go")`},
		{"comment", "# why\nn = 1",
			"comment(# why) type(n) punctuation(=) number(1)"},
		{"booleans are values, not keys", "on = true",
			"type(on) punctuation(=) keyword(true)"},
		{"datetime is one token", "at = 1979-05-27T07:32:00Z",
			"type(at) punctuation(=) number(1979-05-27T07:32:00Z)"},
		{"triple-quoted beats single", "s = \"\"\"a\nb\"\"\"",
			"type(s) punctuation(=) string(\"\"\"a\\nb\"\"\")"},
		{"quoted key", "\"a.b\" = 1",
			`type("a.b") punctuation(=) number(1)`},
	} {
		if got := roleTrace(TOML, tc.src); got != tc.want {
			t.Errorf("%s\n  src  %s\n  got  %s\n  want %s", tc.name, tc.src, got, tc.want)
		}
	}
}

func BenchmarkHighlightLine(b *testing.B) {
	const line = `for i, v := range slices.Sorted(maps.Keys(m)) { fmt.Printf("%d=%v\n", i, v) }`
	p := testPalette()
	b.ReportAllocs()
	for b.Loop() {
		_ = Highlight(Go, line, p)
	}
}
