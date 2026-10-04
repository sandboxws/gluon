package render

import (
	"strings"
	"testing"
)

// The session's own imports are the program as written: a generated spec for
// the same path, or for another path under a name one of them binds, would be
// the redeclaration the compiler rejects.
func TestMainFromDefersToDeclaredImports(t *testing.T) {
	s := sessionOf(t,
		"import (\n\t\"math\"\n\tr \"example.com/lab/ds\"\n\t\"example.com/lab/2d-geom\"\n\t_ \"embed\"\n)",
		"math.Pi",
	)
	got, err := Main(s, []ImportSpec{
		{Path: "example.com/lab/math"},                  // a host package under a declared name
		{Path: "example.com/lab/ds"},                    // a declared path, under its own name
		{Name: "geom", Path: "example.com/lab/2d-geom"}, // a declared path whose name can't be guessed
		{Name: "r", Path: "example.com/other/r"},        // a declared alias
		{Name: "_", Path: "net/http/pprof"},             // blank: binds nothing, keeps its place
		{Path: "strings"},                               // unrelated
	})
	if err != nil {
		t.Fatal(err)
	}
	head, _, _ := strings.Cut(got, "func main()")
	for _, gone := range []string{`"example.com/lab/math"`, `"example.com/other/r"`} {
		if strings.Contains(head, gone) {
			t.Errorf("generated %s despite the declaration:\n%s", gone, head)
		}
	}
	for p, want := range map[string]int{
		`"example.com/lab/ds"`:      1,
		`"example.com/lab/2d-geom"`: 1,
		`"math"`:                    1,
		`_ "net/http/pprof"`:        1,
		`"strings"`:                 1,
	} {
		if n := strings.Count(head, p); n != want {
			t.Errorf("%s appears %d times, want %d:\n%s", p, n, want, head)
		}
	}
}

// A pinned entry contributes no code, so its import is not in the program and
// must not keep the generated block from importing the package.
func TestAPinnedImportDoesNotCount(t *testing.T) {
	s := sessionOf(t, `import "strings"`, `strings.ToUpper("x")`)
	s.Entries[0].Pinned = true
	if got := DeclaredImports(s); len(got) != 0 {
		t.Errorf("DeclaredImports = %v, want none", got)
	}
}

func TestImportedName(t *testing.T) {
	for _, c := range []struct {
		im   ImportSpec
		want string
	}{
		{ImportSpec{Path: "math"}, "math"},
		{ImportSpec{Path: "github.com/foo/bar/v2"}, "bar"},
		{ImportSpec{Name: "m", Path: "math"}, "m"},
		{ImportSpec{Name: "_", Path: "embed"}, ""},
		{ImportSpec{Name: ".", Path: "strings"}, ""},
		{ImportSpec{Path: "example.com/lab/2d-geom"}, ""},
	} {
		if got := ImportedName(c.im); got != c.want {
			t.Errorf("ImportedName(%+v) = %q, want %q", c.im, got, c.want)
		}
	}
}
