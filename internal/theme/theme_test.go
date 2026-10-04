package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuiltinsParseThroughTheLoader is why the built-ins are embedded TOML
// rather than Go literals: this exercises the same code a user's file goes
// through, so a loader bug fails here instead of only for users.
func TestBuiltinsParseThroughTheLoader(t *testing.T) {
	names := BuiltinNames()
	if len(names) < 2 {
		t.Fatalf("expected at least the go and terminal themes, got %v", names)
	}
	for _, n := range names {
		f, ok := Builtin(n)
		if !ok {
			t.Errorf("%s is named but does not load", n)
			continue
		}
		if f.About == "" {
			t.Errorf("%s has no `about` line, so `gluon theme` has nothing to print", n)
		}
	}
}

// TestBuiltinsAreComplete: a shipped theme sets every role, so nothing depends
// on the fallback being right.
func TestBuiltinsAreComplete(t *testing.T) {
	for _, n := range BuiltinNames() {
		f, _ := Builtin(n)
		for _, role := range Roles() {
			if _, ok := f.Palette[role]; !ok {
				t.Errorf("theme %s does not set %q", n, role)
			}
		}
		if len(f.Palette) != len(Roles()) {
			t.Errorf("theme %s sets %d roles, want %d", n, len(f.Palette), len(Roles()))
		}
	}
}

// TestTerminalThemeIsTodaysPalette is the backward-compatibility pin. Someone
// who writes `name = "terminal"` is asking for what gluon looked like before,
// and these ten numbers are what it looked like.
func TestTerminalThemeIsTodaysPalette(t *testing.T) {
	was := map[string]string{
		"type": "6", "annotation": "8", "note": "3", "string": "2",
		"number": "7", "border": "8", "prompt": "6", "error": "1",
		"dim": "8", "search": "3",
	}
	f, ok := Builtin("terminal")
	if !ok {
		t.Fatal("no terminal theme")
	}
	for role, want := range was {
		if got := f.Palette[role]; got != want {
			t.Errorf("terminal theme changed %q from %q to %q", role, want, got)
		}
	}
}

func TestResolveOrder(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "custom.toml", "[theme]\nstring = \"#111111\"\n")

	for _, tc := range []struct {
		name, theme, role, want string
		overrides               map[string]string
	}{
		{name: "default", theme: "", role: "string", want: "#4ADE95"},
		{name: "named built-in", theme: "terminal", role: "string", want: "2"},
		{name: "named file", theme: "custom", role: "string", want: "#111111"},
		{name: "a partial theme inherits the default",
			theme: "custom", role: "keyword", want: "#C6ACD9"},
		{name: "an override beats the theme", theme: "terminal", role: "string",
			want: "#ABCDEF", overrides: map[string]string{"string": "#ABCDEF"}},
		{name: "an override beats the default", theme: "", role: "keyword",
			want: "1", overrides: map[string]string{"keyword": "1"}},
	} {
		p, err := Resolve(tc.theme, tc.overrides, dir)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got := p[tc.role]; got != tc.want {
			t.Errorf("%s: %s = %q, want %q", tc.name, tc.role, got, tc.want)
		}
	}
}

// TestResolveAlwaysReturnsACompletePalette — including when the named theme
// does not exist, which is reported rather than fatal.
func TestResolveAlwaysReturnsACompletePalette(t *testing.T) {
	for _, name := range []string{"", "go", "terminal", "nosuchtheme"} {
		p, err := Resolve(name, nil, t.TempDir())
		for _, role := range Roles() {
			if p[role] == "" {
				t.Errorf("theme %q: %s is unset (err=%v)", name, role, err)
			}
		}
	}
	if _, err := Resolve("nosuchtheme", nil, t.TempDir()); err == nil {
		t.Error("an unknown theme name resolved without saying so")
	}
}

// TestUserFileShadowsBuiltin: a shipped theme can be fixed without renaming it.
func TestUserFileShadowsBuiltin(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.toml", "[theme]\nstring = \"#000001\"\n")
	p, err := Resolve("go", nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	// Resolve starts from the built-in go and then applies the named theme,
	// which here is the user's own file of the same name.
	if p["string"] != "#000001" {
		t.Errorf("a user's go.toml did not shadow the built-in: string = %q", p["string"])
	}
}

func TestUnknownRoleInAThemeFileIsAnError(t *testing.T) {
	if _, err := Decode("x", "", "[theme]\nstirng = \"2\"\n"); err == nil {
		t.Fatal("a misspelled role was accepted")
	} else if !strings.Contains(err.Error(), "stirng") {
		t.Errorf("the error does not name the key: %v", err)
	}
}

func TestUnknownKeyInAThemeFileIsAnError(t *testing.T) {
	if _, err := Decode("x", "", "colour = true\n"); err == nil {
		t.Fatal("an unknown setting was accepted")
	}
}

// TestNoRoleIsCalledName guards the reserved key: config.toml lifts `name` out
// of [theme] before validating roles, which only works while no role is called
// that.
func TestNoRoleIsCalledName(t *testing.T) {
	for _, r := range Roles() {
		if r == "name" {
			t.Fatal("a role called `name` collides with the reserved key in [theme]")
		}
	}
}

func TestValidName(t *testing.T) {
	for _, bad := range []string{"", "../x", "a/b", `a\b`, ".hidden", "/etc/passwd"} {
		if err := ValidName(bad); err == nil {
			t.Errorf("%q was accepted as a theme name", bad)
		}
	}
	for _, ok := range []string{"go", "terminal", "my-theme", "monokai_dark"} {
		if err := ValidName(ok); err != nil {
			t.Errorf("%q was rejected: %v", ok, err)
		}
	}
}

// TestNamedRefusesAPathEvenWhenTheFileExists is ValidName doing its actual job.
func TestNamedRefusesAPathEvenWhenTheFileExists(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "outside.toml")
	if err := os.WriteFile(outside, []byte("[theme]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Named("../"+filepath.Base(dir)+"/outside", filepath.Join(dir, "themes")); err == nil {
		t.Error("a theme name containing a path was followed")
	}
}

func TestNamesListsBuiltinsAndFiles(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "mine.toml", "[theme]\n")
	write(t, dir, "notes.txt", "ignored")
	got := strings.Join(Names(dir), " ")
	for _, want := range []string{"go", "terminal", "mine"} {
		if !strings.Contains(got, want) {
			t.Errorf("Names is missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "notes") {
		t.Errorf("Names picked up a non-theme file: %s", got)
	}
}

// TestNoneSurvivesResolution: None is a value, not an omission. An omitted role
// inherits; None says positively that this role is not painted.
func TestNoneSurvivesResolution(t *testing.T) {
	p, err := Resolve("terminal", nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if p["ident"] != None {
		t.Errorf("terminal ident = %q, want %q", p["ident"], None)
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestBuiltinsAreDistinct: no two shipped themes are the same fifteen values.
//
// This is not pedantry, it is what the Gruvppuccin import ran into. That family
// has a Mocha and a Macchiato that differ only in their editor background — and
// gluon takes no background, because the terminal owns it — so they converted to
// identical palettes and only one was shipped. Two names for one theme is one
// name too many, the same rule File.Name already states about naming a theme
// twice.
func TestBuiltinsAreDistinct(t *testing.T) {
	seen := map[string]string{}
	for _, n := range BuiltinNames() {
		f, _ := Builtin(n)
		var b strings.Builder
		for _, role := range Roles() {
			b.WriteString(role + "=" + f.Palette[role] + ";")
		}
		key := b.String()
		if prev, dup := seen[key]; dup {
			t.Errorf("%s and %s are the same palette under two names", prev, n)
		}
		seen[key] = n
	}
}

// TestImportedBuiltinsSayWhereTheyCameFrom: a shipped theme that is somebody
// else's work names it, in the file and in the field `gluon theme import`
// writes. The built-ins gluon drew itself set no source, and that is the
// difference the field exists to record.
func TestImportedBuiltinsSayWhereTheyCameFrom(t *testing.T) {
	own := map[string]bool{"go": true, "go-light": true, "terminal": true}
	for _, n := range BuiltinNames() {
		f, _ := Builtin(n)
		switch {
		case own[n] && f.Source != "":
			t.Errorf("%s is gluon's own but claims a source %q", n, f.Source)
		case !own[n] && f.Source == "":
			t.Errorf("%s was converted from somebody else's theme and does not say so", n)
		}
	}
}

// TestBuiltinsSayWhichGroundTheyAreFor: a shipped theme declares its
// appearance.
//
// It is optional in the format, because a theme somebody writes for themselves
// on their own terminal has nobody to tell. A shipped one is being offered to a
// stranger through a list, and the list is where "this is for a white terminal"
// has to be said — gluon paints no background and cannot find out on its own.
func TestBuiltinsSayWhichGroundTheyAreFor(t *testing.T) {
	for _, n := range BuiltinNames() {
		f, _ := Builtin(n)
		if f.Appearance == "" {
			t.Errorf("%s does not say which ground it was drawn for", n)
		}
	}
}

// TestBuiltinRolesReadOnTheirOwnGround is the test the light themes needed.
//
// A palette is foreground colours, and the ground it is read against is the
// terminal's. So the one mistake a theme file can make that nothing else here
// would catch is a colour that vanishes into the background it declared: amber
// `search` on white, a `border` a shade off black. Both happened — the first
// to every light theme imported before `fill` learned to pick its base by
// appearance, the second to Tokyo Night, whose `panel.border` is #101014.
//
// The bounds are loose on purpose, because this is not a contrast standard and
// gluon does not know the real background. It is a floor under "can this be
// seen at all", and it is grouped by what a role is *for*: `border` is drawn to
// be looked past and is allowed to be faint, a comment recedes, and everything
// that carries meaning has to read.
func TestBuiltinRolesReadOnTheirOwnGround(t *testing.T) {
	for _, n := range BuiltinNames() {
		f, _ := Builtin(n)
		for _, role := range Roles() {
			v := f.Palette[role]
			if !Readable(role, f.Appearance, v) {
				l, _ := Luminance(v)
				t.Errorf("%s: %s = %s cannot be read on a %s terminal (luminance %.3f)",
					n, role, v, f.Appearance, l)
			}
		}
	}
}
