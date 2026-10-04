package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// tomlKeys is every key a Config can hold, as :settings would name it. It
// recurses into plain structs — [plugins], [value] — and stops at a map or a
// slice, because those are one setting rather than a key per entry.
func tomlKeys(t reflect.Type, prefix string) []string {
	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("toml")
		if tag == "-" || tag == "" {
			continue
		}
		key := prefix + tag
		if f.Type.Kind() == reflect.Struct && f.Type.Name() != "" &&
			f.Type.PkgPath() == t.PkgPath() {
			out = append(out, tomlKeys(f.Type, key+".")...)
			continue
		}
		out = append(out, key)
	}
	return out
}

// TestEveryConfigKeyIsAnOption walks Config by reflection and requires a row
// for every key it can hold.
//
// A field added to the struct and not registered is a setting :settings claims
// gluon does not have — the same silent omission the command registry exists to
// make impossible, and the reason that registry's doc comment is as long as it
// is.
func TestEveryConfigKeyIsAnOption(t *testing.T) {
	covered := map[string]bool{}
	for _, o := range Options() {
		covered[o.Key] = true
		// A Family row covers its whole table: theme.<role> is sixteen keys.
		if o.Family && o.Table != "" {
			covered[o.Table] = true
		}
		// value.form.<kind> rows live under the same table as value.form.
		if o.Table != "" {
			covered[o.Table] = covered[o.Table] || o.Key == o.Table
		}
	}
	for _, key := range tomlKeys(reflect.TypeOf(Config{}), "") {
		if covered[key] {
			continue
		}
		if _, ok := Lookup(key); ok {
			continue
		}
		t.Errorf("config key %q has no Option — :settings would not know it exists", key)
	}
}

// TestEveryOptionNamesARealKey is the other direction, and it is differential
// rather than reasoned: write every settable option's Sample into a fresh
// config, load it, and require Get to read back what was written.
//
// A row whose Table or Name is wrong writes a line the next start rejects; a
// row whose Get reads the wrong field is a value :settings displays and never
// changed. Neither is visible by inspection. This is verifyAppend's stance —
// do not reason about whether the output is right, decode it and compare —
// applied to the registry itself.
func TestEveryOptionNamesARealKey(t *testing.T) {
	for _, o := range Options() {
		if o.Kind != Scalar || o.Family {
			continue
		}
		t.Run(o.Key, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			line, err := SetOption(path, o, o.Sample)
			if err != nil {
				t.Fatalf("SetOption(%s, %q): %v", o.Key, o.Sample, err)
			}
			if !strings.Contains(line, o.Name) {
				t.Errorf("the line written (%q) does not name %q", line, o.Name)
			}
			cfg, err := LoadFile(path)
			if err != nil {
				t.Fatalf("the file %s wrote does not load: %v", o.Key, err)
			}
			// Through Apply, for the reason SetOption's own check gives: the
			// loader expands a leading ~, so the bytes written and the value
			// read back differ for a path and agree for everything else.
			probe := &Config{}
			o.Apply(probe, o.Sample)
			if want, got := o.Get(probe), o.Get(cfg); got != want {
				t.Errorf("wrote %q, read back %q, want %q\n%s", o.Sample, got, want, mustRead(t, path))
			}

			line, found, err := UnsetOption(path, o)
			if err != nil || !found {
				t.Fatalf("UnsetOption(%s) = %q, %v, %v", o.Key, line, found, err)
			}
			cfg, err = LoadFile(path)
			if err != nil {
				t.Fatalf("the file %s unset does not load: %v", o.Key, err)
			}
			if got := o.Get(cfg); got != "" {
				t.Errorf("after unset, %s still reads %q", o.Key, got)
			}
		})
	}
}

// TestOptionsAreWellFormed is TestCommandsAreWellFormed's twin. A row that
// forgot a field is a row that prints an empty column.
func TestOptionsAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, o := range Options() {
		if o.Key == "" || o.Summary == "" || o.Default == "" || o.Allowed == "" {
			t.Errorf("%q is missing a key, summary, default or allowed", o.Key)
		}
		if seen[o.Key] {
			t.Errorf("two settings are called %q", o.Key)
		}
		seen[o.Key] = true
		if o.Effect == effectUnset {
			t.Errorf("%s does not say when a change takes effect", o.Key)
		}
		if o.Kind == kindUnset {
			t.Errorf("%s does not say whether it can be written", o.Key)
		}
		if o.Get == nil {
			t.Errorf("%s cannot report its current value", o.Key)
		}
		if o.Kind != Scalar {
			if o.Why == "" {
				t.Errorf("%s is listed and not settable, and does not say why", o.Key)
			}
			continue
		}
		if o.Family {
			// A family row is a placeholder for its members; Lookup is what
			// materialises one, and TestLookupResolvesAThemeRole proves the
			// member it produces can be read and applied.
			continue
		}
		if o.Apply == nil {
			t.Errorf("%s cannot be applied to a running session", o.Key)
		}
		if o.Sample == "" {
			t.Errorf("%s has no sample, so nothing proves it can be set", o.Key)
		}
	}
}

// TestEveryClosedSetDescribesItsValues. About is optional, but one that
// answered for some of its values and not others would leave a chooser showing
// a list half explained — and it is exactly the drift that happens when a value
// is added to the set and nothing asks the describer about it.
func TestEveryClosedSetDescribesItsValues(t *testing.T) {
	for _, o := range Options() {
		if o.Values == nil || o.About == nil {
			continue
		}
		for _, v := range o.Values() {
			if o.About(v) == "" {
				t.Errorf("%s describes every value but %q", o.Key, v)
			}
		}
	}
}

// TestOptionSummariesFit: the summary is a column in a table, not a paragraph.
// helpWidth makes the same argument about a command's one-liner.
func TestOptionSummariesFit(t *testing.T) {
	const max = 52
	for _, o := range Options() {
		if n := len([]rune(o.Summary)); n > max {
			t.Errorf("%s has a %d-rune summary; the column holds %d: %q", o.Key, n, max, o.Summary)
		}
	}
}

// TestEveryOptionIsAStringInTOML. afterValue parses one quoted string and
// nothing else, which is what lets a trailing comment survive an edit. The
// first boolean or integer setting added without extending it would silently
// lose somebody's comment, or write a line that does not parse.
func TestEveryOptionIsAStringInTOML(t *testing.T) {
	for _, o := range Options() {
		if o.Kind != Scalar || o.Family {
			continue
		}
		path := filepath.Join(t.TempDir(), "config.toml")
		if _, err := SetOption(path, o, o.Sample); err != nil {
			t.Fatal(err)
		}
		src := mustRead(t, path)
		if !strings.Contains(src, o.Name+" = \"") {
			t.Errorf("%s did not write a quoted string:\n%s", o.Key, src)
		}
	}
}

// TestLookupResolvesAThemeRole: sixteen roles are one row in the table and
// sixteen keys at the prompt.
func TestLookupResolvesAThemeRole(t *testing.T) {
	o, ok := Lookup("theme.keyword")
	if !ok {
		t.Fatal("theme.keyword is not a setting")
	}
	if o.Name != "keyword" || o.Table != "theme" || o.Family {
		t.Errorf("theme.keyword resolved to %+v", o)
	}
	cfg := &Config{Theme: map[string]string{"keyword": "#ff0000"}}
	if got := o.Get(cfg); got != "#ff0000" {
		t.Errorf("theme.keyword reads %q, want #ff0000", got)
	}
	o.Apply(cfg, "6")
	if cfg.Theme["keyword"] != "6" {
		t.Errorf("Apply did not set the role: %v", cfg.Theme)
	}
	if _, ok := Lookup("theme.nope"); ok {
		t.Error("theme.nope resolved to a setting")
	}
}

// TestValidateMatchesLoad: a value :settings accepts must be one the next start
// accepts too, or the command writes a config that stops gluon from booting.
func TestValidateMatchesLoad(t *testing.T) {
	bad := map[string]string{
		"timeout":         "1 fortnight",
		"value.form":      "treee",
		"value.form.list": "treee",
		"theme.name":      "no-such-theme-anywhere",
	}
	for key, v := range bad {
		o, ok := Lookup(key)
		if !ok {
			t.Fatalf("%s is not a setting", key)
		}
		if o.Validate == nil {
			t.Fatalf("%s has no validator", key)
		}
		if err := o.Validate(v); err == nil {
			t.Errorf("%s accepted %q", key, v)
		}
	}
}

// TestKeysExpandsTheFamily: a role has to be nameable, or it is a setting only
// a config file can reach.
func TestKeysExpandsTheFamily(t *testing.T) {
	keys := Keys()
	for _, want := range []string{"timeout", "value.form", "value.form.list", "theme.name", "theme.keyword"} {
		if !contains(keys, want) {
			t.Errorf("Keys() does not offer %q", want)
		}
	}
	if contains(keys, "theme.<role>") {
		t.Error("Keys() offered the family placeholder itself")
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
