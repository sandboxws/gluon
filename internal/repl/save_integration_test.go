//go:build integration

package repl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hostFixture is a small module for a session to attach to. It carries its own
// go.mod, which is all the detector needs.
func hostFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module gluon.test/hosted\n\ngo 1.25.0\n",
		"internal/counter/counter.go": "package counter\n\n" +
			"type Counter struct{ n int }\n\n" +
			"func New() *Counter     { return &Counter{} }\n" +
			"func (c *Counter) Add() { c.n++ }\n",
		".vscode/settings.json": "{}\n",
	}
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestSaveDebugLeavesTheHostTreeUntouched is invariant 1 on this path, and the
// risk the design names outright: a debugger configuration is exactly the kind
// of file that gets written into whatever project is nearby, and invariant 25
// is there because a tracked binary once reached another repo's history.
//
// The scratch's *module path* nests under the host so it can import the host's
// packages. Its *directory* does not — it comes from scratch.Root() either way,
// and that is what makes this structural rather than a rule to remember. The
// fixture carries a .vscode of its own so an overwrite would be caught too.
func TestSaveDebugLeavesTheHostTreeUntouched(t *testing.T) {
	root := hostFixture(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	c := attachedCore(t, root)
	if res := c.Submit("x := 1"); res.Err {
		t.Fatalf("%s", res.Out)
	}

	before := treeSnapshot(t, root)
	// Two empty snapshots would compare equal and prove nothing.
	if _, ok := before[".vscode/settings.json"]; !ok {
		t.Fatalf("the snapshot did not see the fixture's own files: %v", before)
	}
	res := c.save("-debug hosted")
	if res.Err {
		t.Fatalf(":save -debug failed: %s", res.Out)
	}
	after := treeSnapshot(t, root)

	for name, size := range after {
		if before[name] != size {
			t.Errorf("%s changed in the host tree", name)
		}
	}
	for name := range before {
		if _, ok := after[name]; !ok {
			t.Errorf("%s disappeared from the host tree", name)
		}
	}

	// And the configuration really was written — somewhere else.
	var dir string
	for _, line := range strings.Split(res.Out, "\n") {
		if rest, ok := strings.CutPrefix(line, "saved → "); ok {
			dir = filepath.Dir(rest)
		}
	}
	if dir == "" || strings.HasPrefix(dir, root) {
		t.Fatalf("the scratch was written inside the host: %q", dir)
	}
}
