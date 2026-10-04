package syntax

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testPalette is literal escape strings, never a palette derived from a
// ui.Theme. lipgloss resolves its colour profile from os.Stdout, which is never
// a terminal under `go test`, so a Theme-derived palette renders nothing at all
// — and every assertion below would pass whatever the code did.
//
// One distinct opener per role, so a golden test can name the role it expected.
func testPalette() Palette {
	var p Palette
	p.Close = "\x1b[0m"
	for r := RoleNone + 1; r < NumRoles; r++ {
		// Real SGR sequences: a CSI ends at the first byte in 0x40-0x7e, so a
		// palette using letters as parameters would produce escapes that strip
		// stops halfway through — and the test would be checking its own bug.
		p.Open[r] = "\x1b[" + itoa(30+int(r)) + "m"
	}
	return p
}

// strip removes CSI sequences. Deliberately hand-written rather than
// x/ansi.Strip: this is the oracle the round-trip property is checked against,
// and an oracle that shares a dependency with the thing it checks is worth
// less.
func strip(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j < len(s) {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// fixtures are the inputs that each encode one thing that was got wrong on the
// way here, or one thing a REPL sees constantly and a file never does.
var fixtures = []struct{ name, src string }{
	{"empty", ""},
	{"tabs", "\tif x {\n\t\ty()\n\t}\n"},
	{"inserted semicolons", "x := 1\ny := 2\n"},
	{"semicolon inside a block comment", "x /*\n*/ ()"},
	{"crlf line comment", "// hi\r\nx := 1\n"},
	{"crlf raw string", "s := `a\r\nb`\n"},
	{"invalid utf8 in a string", "x := \"\xff\"\n"},
	{"bare invalid utf8", "\xff"},
	{"multi-line raw string", "s := `a\nb`\n"},
	{"unterminated string", `x := "unterminated`},
	{"unterminated raw string", "x := `unterminated"},
	{"unterminated block comment", "/* unterminated"},
	{"unterminated char", `x := 'a`},
	{"incomplete block", "for i := range 3 {"},
	{"lone backtick", "`"},
	{"lone quote", `"`},
	{"empty literals", "a, b, c := \"\", '', ``\n"},
	{"hex float exponent", "x := 0x1p-2\n"},
	{"radix and separators", "x := 0b1010_1010\n"},
	{"trailing zero-x", "x := 0x"},
	{"unicode identifiers", "π := 1\nsayHello := \"世界\"\n"},
	{"only whitespace", "   \n\t\n"},
	{"nul byte", "x := 1\x00\n"},
	{"generics", "func F[T comparable](s []T) int { return len(s) }\n"},
	{"builtin vs name", "var len int\nn := len(s)\ns.len\n"},
	{"sql select", "SELECT id, name FROM users WHERE id = 1 -- a comment\n"},
	{"sql escaped quote", "SELECT 'it''s' FROM t"},
	{"sql unterminated", "SELECT 'oops"},
	{"json object", `{"a": 1, "b": [true, null, -2.5e3]}`},
	{"json unterminated", `{"a": "oops`},
	{"toml table", "[theme]\nname = \"go\"\n# c\nn = 1979-05-27T07:32:00Z\n"},
	{"toml triple", "s = \"\"\"a\nb\"\"\"\n"},
	{"toml unterminated triple", "s = \"\"\"a"},
}

func allLangs() []Lang { return []Lang{Go, SQL, JSON, TOML} }

// corpus is every .go file in the repository, plus the fixtures. There is no
// testdata directory anywhere in this repo and this is better than one: it
// grows with the code, and it is the same text :src prints.
func corpus(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, f := range fixtures {
		out["fixture:"+f.name] = f.src
	}
	root, err := os.Getwd()
	if err != nil {
		return out
	}
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			return out
		}
		root = parent
	}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err == nil {
			out[path] = string(b)
		}
		return nil
	})
	if len(out) < 50 {
		t.Fatalf("corpus is only %d files; the walk is not finding the repository", len(out))
	}
	return out
}

// TestHighlightPreservesEveryByte is the invariant the whole package exists to
// hold. Everything else here is a colour; this is the correctness.
func TestHighlightPreservesEveryByte(t *testing.T) {
	p := testPalette()
	for name, src := range corpus(t) {
		for _, lang := range allLangs() {
			got := strip(Highlight(lang, src, p))
			if got == src {
				continue
			}
			t.Errorf("%s as %s: %s", name, lang, firstDiff(src, got))
		}
	}
}

func firstDiff(want, got string) string {
	n := min(len(want), len(got))
	for i := 0; i < n; i++ {
		if want[i] != got[i] {
			return diffAt(want, got, i)
		}
	}
	if len(want) != len(got) {
		return diffAt(want, got, n)
	}
	return "identical"
}

func diffAt(want, got string, i int) string {
	lo := max(0, i-24)
	return "differs at byte " + itoa(i) +
		"\n  want ..." + quote(sliceAround(want, lo, i+24)) +
		"\n  got  ..." + quote(sliceAround(got, lo, i+24))
}

func sliceAround(s string, lo, hi int) string {
	return s[min(lo, len(s)):min(hi, len(s))]
}

func quote(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		case 0x1b:
			b.WriteString(`\e`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

// TestTokensAreOrderedAndInBounds is the narrower property. A failure here is a
// bug in a scanner loop; a failure in the round-trip test alone could also be a
// bug in Paint, so this one localises the fault.
func TestTokensAreOrderedAndInBounds(t *testing.T) {
	for name, src := range corpus(t) {
		for _, lang := range allLangs() {
			prev := 0
			for i, tok := range Tokens(lang, src) {
				switch {
				case tok.Start < 0 || tok.End > len(src):
					t.Errorf("%s as %s: token %d [%d,%d) out of bounds for %d bytes",
						name, lang, i, tok.Start, tok.End, len(src))
				case tok.End < tok.Start:
					t.Errorf("%s as %s: token %d [%d,%d) ends before it starts",
						name, lang, i, tok.Start, tok.End)
				case tok.Start < prev:
					t.Errorf("%s as %s: token %d starts at %d, behind the previous end %d",
						name, lang, i, tok.Start, prev)
				}
				if tok.End > prev {
					prev = tok.End
				}
			}
		}
	}
}

// TestScannersTerminate is cheap insurance on the one path a user cannot
// escape: these run on every keystroke in the input widget.
func TestScannersTerminate(t *testing.T) {
	inputs := []string{
		strings.Repeat("`", 4096),
		strings.Repeat("/*", 4096),
		strings.Repeat("'", 4096),
		strings.Repeat(`"`, 4096),
		strings.Repeat("\xff", 4096),
		strings.Repeat("0x", 4096),
		strings.Repeat("[", 4096),
		strings.Repeat("\"\"\"", 1024),
		"\ufeff{\"a\":1}",
		strings.Repeat("1979-05-27T07:32:00Z ", 512),
	}
	for _, src := range inputs {
		for _, lang := range allLangs() {
			done := make(chan int, 1)
			go func() { done <- len(Tokens(lang, src)) }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatalf("%s did not terminate on %.20q...", lang, src)
			}
		}
	}
}

// TestUnpaintedPaletteIsAPassthrough is the NO_COLOR contract at this level.
func TestUnpaintedPaletteIsAPassthrough(t *testing.T) {
	var zero Palette
	if zero.Painted() {
		t.Fatal("the zero Palette reports itself as painting")
	}
	for name, src := range corpus(t) {
		for _, lang := range append(allLangs(), None) {
			if got := Highlight(lang, src, zero); got != src {
				t.Errorf("%s as %s: an unpainted palette changed the text", name, lang)
			}
		}
	}
}

// TestNoneIsAPassthrough — a Result that never sets a language is untouched.
func TestNoneIsAPassthrough(t *testing.T) {
	p := testPalette()
	for name, src := range corpus(t) {
		if got := Highlight(None, src, p); got != src {
			t.Errorf("%s: Lang None changed the text", name)
		}
	}
}

// FuzzHighlightPreservesEveryByte is the same property, unbounded. It runs its
// seed corpus under `go test` and costs nothing there.
func FuzzHighlightPreservesEveryByte(f *testing.F) {
	for _, fx := range fixtures {
		f.Add(fx.src)
	}
	p := testPalette()
	f.Fuzz(func(t *testing.T, src string) {
		for _, lang := range allLangs() {
			if got := strip(Highlight(lang, src, p)); got != src {
				t.Fatalf("as %s: %s", lang, firstDiff(src, got))
			}
		}
	})
}
