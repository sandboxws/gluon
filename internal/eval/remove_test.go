package eval

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/host"
	"github.com/sandboxws/gluon/internal/render"
	"github.com/sandboxws/gluon/internal/session"
)

// writeRequires builds a temp module whose go.mod requires reqs, for the
// refusals Remove reaches before it ever calls the toolchain.
func writeRequires(t *testing.T, reqs ...string) string {
	t.Helper()
	dir := t.TempDir()
	mod := "module gluon.local/session\n\ngo 1.25\n"
	for _, r := range reqs {
		mod += "\nrequire " + r + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A module nobody requires and a module the host requires are different
// mistakes with different fixes, so they are different errors.
func TestRemoveDistinguishesNeverRequiredFromTheHosts(t *testing.T) {
	e := &Evaluator{dir: writeRequires(t, "example.com/dep v1.2.3")}

	_, err := e.Remove("example.com/other")
	if !errors.Is(err, ErrNotRequired) {
		t.Errorf("removing a module nothing requires: %v, want ErrNotRequired", err)
	}
	// The message has to name the module: the error is read, not matched.
	if err == nil || !strings.Contains(err.Error(), "example.com/other") {
		t.Errorf("error does not name the module: %v", err)
	}

	hostDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(hostDir, "go.mod"),
		[]byte("module example.com/proj\n\ngo 1.25\n\nrequire example.com/dep v1.2.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.attached = &host.Host{Dir: hostDir, Path: "example.com/proj", Go: "1.25"}

	if _, err := e.Remove("example.com/dep"); !errors.Is(err, ErrHostRequires) {
		t.Errorf("removing a host requirement: %v, want ErrHostRequires", err)
	}
	// Requires hides the host's own requirement, so without this case the
	// host module itself would read as "nobody required it".
	if _, err := e.Remove("example.com/proj"); !errors.Is(err, ErrHostRequires) {
		t.Errorf("removing the host module itself: %v, want ErrHostRequires", err)
	}
}

// A refusal must not have touched go.mod: the whole point of checking first is
// that the session can be repaired and the command retried.
func TestRemoveRefusesBeforeItWrites(t *testing.T) {
	dir := writeRequires(t, "example.com/dep v1.2.3")
	before, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	e := &Evaluator{dir: dir}
	if _, err := e.Remove("example.com/nope"); err == nil {
		t.Fatal("removing a module nothing requires succeeded")
	}
	after, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("go.mod changed on a refusal:\n%s\nwant:\n%s", after, before)
	}
}

// The prefix case is the one a naive HasPrefix gets wrong, and it is not
// hypothetical: example.com/a and example.com/ab are both valid module paths.
func TestUnderModuleMatchesOnAPathBoundary(t *testing.T) {
	cases := []struct {
		path, mod string
		want      bool
	}{
		{"example.com/a", "example.com/a", true},
		{"example.com/a/sub", "example.com/a", true},
		{"example.com/ab", "example.com/a", false},
		{"example.com/ab/sub", "example.com/a", false},
		{"example.com/b", "example.com/a", false},
		{"example.com", "example.com/a", false},
	}
	for _, c := range cases {
		if got := underModule(c.path, c.mod); got != c.want {
			t.Errorf("underModule(%q, %q) = %v, want %v", c.path, c.mod, got, c.want)
		}
	}
}

// Importers is what stands between :get -rm and a session that no longer
// builds, so it has to name the entries rather than merely count them.
func TestImportersNamesTheEntriesThatUseTheModule(t *testing.T) {
	e := &Evaluator{
		dir: writeRequires(t),
		imports: []render.ImportSpec{
			{Path: "fmt"},
			{Path: "example.com/a/sub"},
			{Path: "example.com/ab"},
		},
	}
	s := &session.Session{Entries: []session.Entry{
		{Kind: session.KindExpr, Src: `fmt.Sprint("x")`},
		{Kind: session.KindExpr, Src: `sub.New()`},
		{Kind: session.KindExpr, Src: `1 + 1`},
		{Kind: session.KindExpr, Src: `sub.Other() + len(ab.Name)`},
	}}

	if got, want := e.Importers(s, "example.com/a"), []int{2, 4}; !reflect.DeepEqual(got, want) {
		t.Errorf("Importers(example.com/a) = %v, want %v", got, want)
	}
	// ab is its own module: removing example.com/a must not blame it.
	if got, want := e.Importers(s, "example.com/ab"), []int{4}; !reflect.DeepEqual(got, want) {
		t.Errorf("Importers(example.com/ab) = %v, want %v", got, want)
	}
	if got := e.Importers(s, "example.com/unused"); got != nil {
		t.Errorf("Importers of a module nothing imports = %v, want none", got)
	}
}

// An import written under an alias is used under that alias, so the qualifier
// the entries name is the alias and not the last path element.
func TestImportersFollowsAnAlias(t *testing.T) {
	e := &Evaluator{
		dir:     writeRequires(t),
		imports: []render.ImportSpec{{Name: "yaml", Path: "gopkg.in/yaml.v3"}},
	}
	s := &session.Session{Entries: []session.Entry{
		{Kind: session.KindExpr, Src: `yaml.Marshal(x)`},
	}}
	if got, want := e.Importers(s, "gopkg.in/yaml.v3"), []int{1}; !reflect.DeepEqual(got, want) {
		t.Errorf("Importers = %v, want %v", got, want)
	}
}

// A downgrade is a change to what the session compiles against, and one that
// went unreported would be invisible until something stopped compiling.
func TestChangedRequiresReportsMoreThanTheModuleNamed(t *testing.T) {
	before := []string{"example.com/a v1.2.0", "example.com/b v1.5.0", "example.com/c v0.1.0"}
	after := []string{"example.com/b v1.4.0", "example.com/d v0.2.0"}
	want := []string{
		"removed example.com/a v1.2.0",
		"example.com/b v1.4.0 (was v1.5.0)",
		"removed example.com/c v0.1.0",
		"now requires example.com/d v0.2.0",
	}
	if got := changedRequires(before, after); !reflect.DeepEqual(got, want) {
		t.Errorf("changedRequires =\n%v\nwant\n%v", got, want)
	}
}
