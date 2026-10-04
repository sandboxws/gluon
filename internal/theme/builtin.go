package theme

import (
	"embed"
	"sort"
	"strings"
	"sync"
)

// Built-in themes are embedded TOML rather than Go literals, so that a shipped
// theme and a user's file go through exactly one loader. A built-in only the
// literal path could express would be a theme nobody could write by hand, and
// the test that parses these is then a real test of the user-facing path.
//
//go:embed themes/*.toml
var builtinFS embed.FS

var builtins = sync.OnceValue(func() map[string]File {
	out := map[string]File{}
	entries, err := builtinFS.ReadDir("themes")
	if err != nil {
		return out
	}
	for _, e := range entries {
		data, err := builtinFS.ReadFile("themes/" + e.Name())
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".toml")
		f, err := Decode(name, "", string(data))
		if err != nil {
			// Unreachable in a build that passed its tests:
			// TestBuiltinsParseThroughTheLoader is what holds this.
			continue
		}
		out[name] = f
	}
	return out
})

// Builtin is a theme that ships with gluon.
func Builtin(name string) (File, bool) {
	f, ok := builtins()[name]
	return f, ok
}

// BuiltinNames is every shipped theme, sorted.
func BuiltinNames() []string {
	m := builtins()
	out := make([]string, 0, len(m))
	for n := range m {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
