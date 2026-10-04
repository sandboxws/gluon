package theme

import (
	"strings"
	"testing"
)

const monokaiTM = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>name</key><string>Monokai</string>
  <key>semanticClass</key><string>theme.dark.monokai</string>
  <key>settings</key>
  <array>
    <dict><key>settings</key><dict>
      <key>background</key><string>#272822</string>
      <key>foreground</key><string>#F8F8F2</string>
      <key>caret</key><string>#F8F8F0</string>
    </dict></dict>
    <dict>
      <key>name</key><string>Comment</string>
      <key>scope</key><string>comment</string>
      <key>settings</key><dict><key>foreground</key><string>#75715E</string></dict>
    </dict>
    <dict>
      <key>scope</key><string>string, string.quoted</string>
      <key>settings</key><dict><key>foreground</key><string>#E6DB74</string></dict>
    </dict>
    <dict>
      <key>scope</key><string>keyword</string>
      <key>settings</key><dict><key>foreground</key><string>#AAAAAA</string></dict>
    </dict>
    <dict>
      <key>scope</key><string>keyword.control</string>
      <key>settings</key><dict><key>foreground</key><string>#F92672</string></dict>
    </dict>
    <dict>
      <key>scope</key><string>constant.numeric</string>
      <key>settings</key><dict><key>foreground</key><string>#AE81FF</string></dict>
    </dict>
    <dict>
      <key>scope</key><string>source.go entity.name.type</string>
      <key>settings</key><dict><key>foreground</key><string>#66D9EF</string></dict>
    </dict>
    <dict>
      <key>scope</key><string>invalid.illegal</string>
      <key>settings</key>
      <dict><key>foreground</key><string>#F8F8F0</string><key>fontStyle</key><string>italic</string></dict>
    </dict>
    <dict><key>scope</key><string>meta.ignored</string>
      <key>settings</key><dict><key>fontStyle</key><string>bold</string></dict></dict>
    <dict><key>scope</key><string>some.count</string>
      <key>settings</key><dict><key>foreground</key><string>#123456</string></dict>
      <key>weight</key><integer>3</integer></dict>
  </array>
</dict>
</plist>
`

const vscodeJSON = `{
  // A comment, which encoding/json alone would refuse.
  "name": "Night",
  "type": "dark",
  "colors": {
    "editor.foreground": "#D4D4D4",
    "editor.background": "#1E1E1E",
    "panel.border": "#2B2B2B",
    "editorLineNumber.foreground": "#858585",
  },
  "tokenColors": [
    { "scope": "comment", "settings": { "foreground": "#6A9955" } },
    { "scope": ["string", "string.quoted"], "settings": { "foreground": "#CE9178" } },
    { "scope": "keyword.control", "settings": { "foreground": "#C586C0" } },
    { "scope": "entity.name.type, support.type", "settings": { "foreground": "#4EC9B0" } },
    { "scope": "constant.numeric", "settings": { "foreground": "#B5CEA8" } },
    /* block comment */
    { "settings": { "foreground": "#D4D4D4" } },
  ],
}
`

func TestImportTMTheme(t *testing.T) {
	f, rep, err := Import("monokai", []byte(monokaiTM))
	if err != nil {
		t.Fatal(err)
	}
	for role, want := range map[string]string{
		"comment": "#75715E",
		"string":  "#E6DB74",
		// keyword.control is more specific than keyword, so it wins.
		"keyword": "#F92672",
		"number":  "#AE81FF",
		// A descendant selector is keyed on its last element.
		"type": "#66D9EF",
		// NOT the fixture's `invalid` foreground, #F8F8F0. That is the caret
		// colour, two hex digits off the editor's own #F8F8F2, and an error line
		// painted the colour of ordinary output has not been painted. gluon's own
		// red is kept instead — see the guard in toFile.
		"error": "#FF7373",
		// The editor's own foreground becomes what unscoped text is.
		"ident":       "#F8F8F2",
		"punctuation": "#F8F8F2",
		// Derived from gluon's own relationships.
		"annotation": "#75715E",
		"prompt":     "#66D9EF",
	} {
		if got := f.Palette[role]; got != want {
			t.Errorf("%s = %q, want %q", role, got, want)
		}
	}
	if rep.Source != "Monokai" || rep.Format != "tmTheme" {
		t.Errorf("report says %q/%q", rep.Source, rep.Format)
	}
	// Every role has a value, so the file written is always complete and every
	// guess is on screen where it can be edited.
	for _, role := range Roles() {
		if f.Palette[role] == "" {
			t.Errorf("%s was left unset", role)
		}
	}
	if rep.Mapped < 8 || rep.Mapped >= len(Roles()) {
		t.Errorf("mapped %d of %d roles, which is not a believable count", rep.Mapped, len(Roles()))
	}
	var kept int
	for _, r := range rep.Rows {
		if r.Kept {
			kept++
		}
	}
	if rep.Mapped+kept != len(Roles()) {
		t.Errorf("the report does not account for every role: %d mapped + %d kept != %d",
			rep.Mapped, kept, len(Roles()))
	}
}

func TestImportVSCode(t *testing.T) {
	f, rep, err := Import("night", []byte(vscodeJSON))
	if err != nil {
		t.Fatal(err)
	}
	for role, want := range map[string]string{
		"comment": "#6A9955",
		"string":  "#CE9178",
		"keyword": "#C586C0",
		"type":    "#4EC9B0",
		"number":  "#B5CEA8",
		"ident":   "#D4D4D4",
		// From the `colors` object, which a .tmTheme cannot express.
		"border": "#2B2B2B",
		"dim":    "#858585",
	} {
		if got := f.Palette[role]; got != want {
			t.Errorf("%s = %q, want %q", role, got, want)
		}
	}
	if rep.Format != "VS Code" {
		t.Errorf("format = %q", rep.Format)
	}
}

// TestImportedThemeLoadsBack closes the loop: what the importer writes is what
// the loader reads, through the same code a hand-written file goes through.
func TestImportedThemeLoadsBack(t *testing.T) {
	for name, src := range map[string]string{"monokai": monokaiTM, "night": vscodeJSON} {
		f, _, err := Import(name, []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		back, err := Decode(name, "", f.Encode())
		if err != nil {
			t.Fatalf("%s: the importer wrote a file the loader refuses: %v\n%s", name, err, f.Encode())
		}
		for _, role := range Roles() {
			if back.Palette[role] != f.Palette[role] {
				t.Errorf("%s: %s survived encoding as %q, want %q",
					name, role, back.Palette[role], f.Palette[role])
			}
		}
	}
}

func TestDetectFormat(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want format
	}{
		{"plist", monokaiTM, formatPlist},
		{"json", vscodeJSON, formatJSON},
		{"json with a BOM", "\xef\xbb\xbf{}", formatJSON},
		{"leading whitespace", "\n\n  <plist/>", formatPlist},
		{"neither", "keyword = red\n", formatUnknown},
		{"empty", "", formatUnknown},
	} {
		if got := detect([]byte(tc.in)); got != tc.want {
			t.Errorf("%s: detect = %v, want %v", tc.name, got, tc.want)
		}
	}
	if _, _, err := Import("x", []byte("keyword = red")); err == nil {
		t.Error("an unrecognised file was imported anyway")
	}
}

func TestImportRefusesAnInclude(t *testing.T) {
	_, _, err := Import("x", []byte(`{"include": "./dark_plus.json", "tokenColors": []}`))
	if err == nil || !strings.Contains(err.Error(), "dark_plus.json") {
		t.Errorf("an include was followed or not named: %v", err)
	}
}

func TestStripJSONCLeavesStringsAlone(t *testing.T) {
	in := `{"a": "// not a comment", "b": "/* nor this */", "c": "quote\" then // still fine"}`
	if got := string(stripJSONC([]byte(in))); got != in {
		t.Errorf("a comment marker inside a string was stripped:\n got  %s\n want %s", got, in)
	}
}

func TestNormaliseHex(t *testing.T) {
	for in, want := range map[string]string{
		"#abc":      "#aabbcc",
		"#AABBCC":   "#AABBCC",
		"#AABBCCDD": "#AABBCC", // a terminal has no alpha
		"red":       "",
		"#12345":    "",
		"#gggggg":   "",
		"":          "",
	} {
		if got := normaliseHex(in); got != want {
			t.Errorf("normaliseHex(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPlistRejectsMalformed(t *testing.T) {
	for _, bad := range []string{
		`<plist><dict><key>a</key></dict></plist>`,
		`<plist><dict><string>no key</string></dict></plist>`,
		`<plist><dict>`,
	} {
		if _, err := parsePlist([]byte(bad)); err == nil {
			t.Errorf("malformed plist accepted: %s", bad)
		}
	}
	if _, err := readPlist([]byte(`<plist><dict><key>name</key><string>x</string></dict></plist>`)); err == nil {
		t.Error("a plist with no settings array was accepted")
	}
}

// TestPlistSkipsScalarsAndDoctype: the DOCTYPE line names an apple.com URL and
// encoding/xml surfaces it as a Directive without fetching anything, and the
// <integer> in the fixture is skipped rather than tripping the reader.
func TestPlistSkipsScalarsAndDoctype(t *testing.T) {
	tm, err := readPlist([]byte(monokaiTM))
	if err != nil {
		t.Fatal(err)
	}
	if tm.Name != "Monokai" {
		t.Errorf("name = %q", tm.Name)
	}
	if tm.Global["foreground"] != "#F8F8F2" {
		t.Errorf("the unscoped rule did not become the global settings: %v", tm.Global)
	}
}

// The four themes below are named because each is a real file gluon could not
// read, or read wrongly, before the fix under test. A converter's bugs are only
// ever found by converting something, so the something is written down.

// TestJSONCThemeOpeningWithACommentIsStillJSON: VS Code's own Monokai starts
// with six lines of `//` naming the greys it was built from. detect looked at
// the first non-space byte, found a slash, and refused the file — while
// stripJSONC, twenty lines away, was written for exactly this.
func TestJSONCThemeOpeningWithACommentIsStillJSON(t *testing.T) {
	body := []byte("// a note\n/* and another */\n{\"type\":\"dark\"," +
		"\"colors\":{\"editor.foreground\":\"#F8F8F2\"}," +
		"\"tokenColors\":[{\"scope\":\"keyword\",\"settings\":{\"foreground\":\"#F92672\"}}]}")
	if got := detect(body); got != formatJSON {
		t.Fatalf("detect = %v, want formatJSON", got)
	}
	f, _, err := Import("monokai", body)
	if err != nil {
		t.Fatal(err)
	}
	if f.Palette["keyword"] != "#F92672" {
		t.Errorf("keyword = %q", f.Palette["keyword"])
	}
}

// TestTrailingCommaBehindAComment: Tokyo Night Light writes
// `"foreground": "#0f4b6e" //"#33635c"` and then a closing brace. Deciding
// whether a comma is trailing cannot be done while the comments are still
// there, which is why stripJSONC is two passes.
func TestTrailingCommaBehindAComment(t *testing.T) {
	body := []byte(`{"type":"light","colors":{"editor.foreground":"#343b59" // was #33635c
	},"tokenColors":[{"scope":"keyword","settings":{"foreground":"#5a4a78"}},]}`)
	f, _, err := Import("tokyo-night-day", body)
	if err != nil {
		t.Fatalf("still not valid JSON after stripping: %v", err)
	}
	if f.Palette["ident"] != "#343b59" {
		t.Errorf("ident = %q", f.Palette["ident"])
	}
}

// TestOneBadColourDoesNotCostTheTheme: GitHub Dark Default writes
// symbolIcon.constantForeground as an array. map[string]string refused the
// whole theme over a UI colour gluon does not even read.
func TestOneBadColourDoesNotCostTheTheme(t *testing.T) {
	body := []byte(`{"type":"dark","colors":{"editor.foreground":"#e6edf3",
		"symbolIcon.constantForeground":["#79c0ff","#a5d6ff"]},
		"tokenColors":[{"scope":"keyword","settings":{"foreground":"#ff7b72"}}]}`)
	f, _, err := Import("github-dark", body)
	if err != nil {
		t.Fatalf("one array-valued colour refused the theme: %v", err)
	}
	if f.Palette["ident"] != "#e6edf3" || f.Palette["keyword"] != "#ff7b72" {
		t.Errorf("ident = %q, keyword = %q", f.Palette["ident"], f.Palette["keyword"])
	}
}

// TestIdentIsTheForegroundNotTheVariableAccent: gluon's `ident` is every
// identifier a line of Go has, not the locals a `variable` rule means. Themes
// accent variables freely — Dracula pink, One red, Catppuccin maroon — and
// taking that painted a whole screen in it.
func TestIdentIsTheForegroundNotTheVariableAccent(t *testing.T) {
	body := []byte(`{"type":"dark","colors":{"editor.foreground":"#f8f8f2",
		"editor.background":"#282a36"},
		"tokenColors":[{"scope":"variable","settings":{"foreground":"#ff79c6"}}]}`)
	f, _, err := Import("dracula", body)
	if err != nil {
		t.Fatal(err)
	}
	if f.Palette["ident"] != "#f8f8f2" {
		t.Errorf("ident = %q, want the theme's own foreground", f.Palette["ident"])
	}
	// Still the answer when the theme states no foreground of its own.
	body = []byte(`{"type":"dark","colors":{},
		"tokenColors":[{"scope":"variable","settings":{"foreground":"#ff79c6"}}]}`)
	f, _, err = Import("dracula", body)
	if err != nil {
		t.Fatal(err)
	}
	if f.Palette["ident"] != "#ff79c6" {
		t.Errorf("ident = %q, want the variable accent when there is nothing better",
			f.Palette["ident"])
	}
}

// TestPunctuationIgnoresALanguagesOwnRule: One Light styles
// `source.elixir readwrite.module punctuation` red. Keyed on its last element
// that reads as a rule about punctuation, and gluon has one punctuation colour
// for every language — so every comma in a Go session came out red.
func TestPunctuationIgnoresALanguagesOwnRule(t *testing.T) {
	body := []byte(`{"type":"light","colors":{"editor.foreground":"#383A42"},
		"tokenColors":[{"scope":"source.elixir readwrite.module punctuation",
		"settings":{"foreground":"#E45649"}}]}`)
	f, _, err := Import("one-light", body)
	if err != nil {
		t.Fatal(err)
	}
	if f.Palette["punctuation"] != "#383A42" {
		t.Errorf("punctuation = %q, want the theme's own foreground", f.Palette["punctuation"])
	}
	// A rule that is about punctuation everywhere still wins.
	body = []byte(`{"type":"dark","colors":{"editor.foreground":"#c0caf5"},
		"tokenColors":[{"scope":"punctuation","settings":{"foreground":"#89ddff"}}]}`)
	f, _, err = Import("tokyo-night", body)
	if err != nil {
		t.Fatal(err)
	}
	if f.Palette["punctuation"] != "#89ddff" {
		t.Errorf("punctuation = %q, want the theme's context-free rule", f.Palette["punctuation"])
	}
}

// TestAGeneralQuestionAnsweredBySpecificRules: gruvbox scopes
// `string.quoted.single` and never `string`. The scope table only walks up, so
// without a pass that walks down, gruvbox's strings came out the colour of
// ordinary text.
func TestAGeneralQuestionAnsweredBySpecificRules(t *testing.T) {
	body := []byte(`{"type":"dark","colors":{"editor.foreground":"#ebdbb2"},
		"tokenColors":[{"scope":"string.quoted.single","settings":{"foreground":"#b8bb26"}}]}`)
	f, _, err := Import("gruvbox-dark", body)
	if err != nil {
		t.Fatal(err)
	}
	if f.Palette["string"] != "#b8bb26" {
		t.Errorf("string = %q, want the theme's only string rule", f.Palette["string"])
	}
}

// TestARoleThatResolvedToTheGroundIsNotResolved: gluon paints no background, so
// a colour equal to the source's own is a colour on top of itself. Tokyo
// Night's panel.border is #101014 on a #1a1b26 editor.
func TestARoleThatResolvedToTheGroundIsNotResolved(t *testing.T) {
	body := []byte(`{"type":"dark","colors":{"editor.background":"#1a1b26",
		"editor.foreground":"#a9b1d6","panel.border":"#101014"},
		"tokenColors":[{"scope":"keyword","settings":{"foreground":"#bb9af7"}}]}`)
	f, _, err := Import("tokyo-night", body)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := Builtin(Default)
	if f.Palette["border"] != base.Palette["border"] {
		t.Errorf("border = %q, want gluon's own %q", f.Palette["border"], base.Palette["border"])
	}
}

// TestTheBackgroundOutranksTheThemesOwnLabel: Tokyo Night Light ships
// `"type": "dark"` over an #e6e7ed background. A label can be wrong because
// nothing reads it; the background is the ground the colours were chosen
// against, which is what `appearance` means.
func TestTheBackgroundOutranksTheThemesOwnLabel(t *testing.T) {
	body := []byte(`{"type":"dark","colors":{"editor.background":"#e6e7ed",
		"editor.foreground":"#343b59"},
		"tokenColors":[{"scope":"keyword","settings":{"foreground":"#5a4a78"}}]}`)
	f, rep, err := Import("tokyo-night-day", body)
	if err != nil {
		t.Fatal(err)
	}
	if f.Appearance != Light {
		t.Errorf("appearance = %q, want %q — the background says so", f.Appearance, Light)
	}
	if rep.Appearance != Light {
		t.Errorf("the report says %q", rep.Appearance)
	}
	// And the leftovers come from the light default, not the dark one.
	light, _ := Builtin(DefaultLight)
	if f.Palette["search"] != light.Palette["search"] {
		t.Errorf("search = %q, want %q from %s",
			f.Palette["search"], light.Palette["search"], DefaultLight)
	}
}

// TestAHighlightRuleHasNotSaidWhatColourTheThingIs: `invalid` is written the
// same way in theme after theme — ink on a red field — and gluon takes only the
// ink. Dracula's is #F8F8F0 on #ff79c6, gruvbox's #282828 on #9d0006: in both
// the red that made it an error is the half gluon drops.
func TestAHighlightRuleHasNotSaidWhatColourTheThingIs(t *testing.T) {
	body := []byte(`{"type":"dark","colors":{"editor.background":"#282828",
		"editor.foreground":"#ebdbb2"},
		"tokenColors":[{"scope":"invalid","settings":{"foreground":"#fbf1c7","background":"#9d0006"}}]}`)
	f, _, err := Import("gruvbox-dark", body)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := Builtin(Default)
	if f.Palette["error"] != base.Palette["error"] {
		t.Errorf("error = %q, want gluon's own %q", f.Palette["error"], base.Palette["error"])
	}
	// A background that only restates the editor's own is not a highlight, and
	// the rule still counts.
	body = []byte(`{"type":"dark","colors":{"editor.background":"#282828",
		"editor.foreground":"#ebdbb2"},
		"tokenColors":[{"scope":"invalid","settings":{"foreground":"#fb4934","background":"#282828"}}]}`)
	f, _, err = Import("gruvbox-dark", body)
	if err != nil {
		t.Fatal(err)
	}
	if f.Palette["error"] != "#fb4934" {
		t.Errorf("error = %q, want the rule's own red", f.Palette["error"])
	}
}
