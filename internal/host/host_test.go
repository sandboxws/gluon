package host

import (
	"go/version"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeHost builds a small module on disk with the shape that matters: a
// package under internal/, a nested package under a subdirectory's own
// internal/ (which the session must *not* be able to import), a main package,
// and a nested module that is not part of this one.
func writeHost(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":                  "module example.com/proj\n\ngo 1.24\n",
		"internal/trace/trace.go": "package trace\n\nfunc New() int { return 1 }\n",
		"pub/pub.go":              "package pub\n\nconst N = 7\n",
		"sub/internal/trace/t.go": "package trace\n\nfunc New() int { return 2 }\n",
		"cmd/tool/main.go":        "package main\n\nfunc main() {}\n",
		"testdata/skipme/x.go":    "package skipme\n",
		"vendor/other/o.go":       "package other\n",
		"_ignored/i.go":           "package ignored\n",
		"nested/go.mod":           "module example.com/other\n\ngo 1.24\n",
		"nested/deep/d.go":        "package deep\n",
		"named/dirname/clause.go": "package different\n",
	}
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestFromGoMod(t *testing.T) {
	dir := writeHost(t)
	h, err := FromGoMod(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if h.Path != "example.com/proj" {
		t.Errorf("Path = %q, want example.com/proj", h.Path)
	}
	// The directive is copied verbatim. Reading it from the toolchain instead
	// would silently change what the session accepts.
	if h.Go != "1.24" {
		t.Errorf("Go = %q, want 1.24", h.Go)
	}
	if got := h.SessionPath(); got != "example.com/proj/gluonsession" {
		t.Errorf("SessionPath = %q", got)
	}
}

func TestModHasRequireAndReplace(t *testing.T) {
	// A directory with a space in it, because the replace has to survive as
	// one token.
	dir := filepath.Join(t.TempDir(), "some dir", "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/proj\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &Host{Dir: dir, Path: "example.com/proj", Go: "1.24"}
	mod, err := h.Mod()
	if err != nil {
		t.Fatal(err)
	}
	// A bare replace does not put the module in the build list, so the require
	// is load-bearing rather than decorative.
	for _, want := range []string{
		"module example.com/proj/gluonsession",
		"go 1.24",
		"require example.com/proj v0.0.0",
		"example.com/proj => ",
	} {
		if !strings.Contains(mod, want) {
			t.Errorf("go.mod missing %q:\n%s", want, mod)
		}
	}
	if !strings.Contains(mod, `"`+dir+`"`) {
		t.Errorf("replace path not quoted:\n%s", mod)
	}
}

func TestModCarriesTheHostsRequirementsAndReplaces(t *testing.T) {
	dir := t.TempDir()
	hostMod := `module example.com/proj

go 1.24

require (
	example.com/dep v1.2.3
	example.com/tool v0.4.0 // indirect
)

replace example.com/dep => example.com/fork v1.2.4

replace example.com/local => ./vendored/local
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(hostMod), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &Host{Dir: dir, Path: "example.com/proj", Go: "1.24"}
	mod, err := h.Mod()
	if err != nil {
		t.Fatal(err)
	}

	// Pruning means the session module has to name these itself; inheriting
	// them through the replaced host is exactly what does not happen.
	for _, want := range []string{
		"example.com/dep v1.2.3 // indirect",
		"example.com/tool v0.4.0 // indirect",
		"example.com/dep => example.com/fork v1.2.4",
	} {
		if !strings.Contains(mod, want) {
			t.Errorf("go.mod missing %q:\n%s", want, mod)
		}
	}
	// A relative directory replace is relative to the host, and the session
	// module is somewhere else entirely.
	if want := "example.com/local => " + filepath.Join(dir, "vendored", "local"); !strings.Contains(mod, want) {
		t.Errorf("relative replace not rebased, want %q:\n%s", want, mod)
	}
	// The host is required at v0.0.0 and replaced by its directory; the copy
	// must not have turned that into an indirect requirement.
	if strings.Contains(mod, "example.com/proj v0.0.0 // indirect") {
		t.Errorf("the host itself was marked indirect:\n%s", mod)
	}
}

func TestCanImportAppliesTheInternalRule(t *testing.T) {
	h := &Host{Path: "example.com/proj", Go: "1.24"}
	cases := []struct {
		path string
		want bool
	}{
		{"example.com/proj/pub", true},
		// The session sits at example.com/proj/gluonsession, so the parent of
		// this internal element prefixes it.
		{"example.com/proj/internal/trace", true},
		{"example.com/proj/internal", true},
		// The parent here is .../proj/sub, which does not prefix the session.
		{"example.com/proj/sub/internal/trace", false},
		// The *final* internal element decides, being the most restrictive.
		{"example.com/proj/internal/a/internal/b", false},
		{"other.com/x/internal/y", false},
		{"other.com/x/y", true},
		// A directory merely named "internalish" is not an internal element.
		{"example.com/proj/sub/internalish", true},
	}
	for _, c := range cases {
		if got := h.CanImport(c.path); got != c.want {
			t.Errorf("CanImport(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestIndexOffersOnlyReachablePackages(t *testing.T) {
	dir := writeHost(t)
	h, err := FromGoMod(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	ix, err := h.Index()
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]string{}
	for _, p := range ix.All() {
		got[p.Path] = p.Name
	}
	for _, want := range []string{
		"example.com/proj/internal/trace",
		"example.com/proj/pub",
		"example.com/proj/named/dirname",
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("index missing %q; has %v", want, got)
		}
	}
	for _, unwanted := range []string{
		// main packages cannot be imported by anything.
		"example.com/proj/cmd/tool",
		// The internal rule puts this out of reach of the session module.
		"example.com/proj/sub/internal/trace",
		// Directories the go command never treats as packages.
		"example.com/proj/testdata/skipme",
		"example.com/proj/vendor/other",
		"example.com/proj/_ignored",
		// A nested module's packages are not reachable through this path.
		"example.com/proj/nested/deep",
	} {
		if _, ok := got[unwanted]; ok {
			t.Errorf("index should not offer %q", unwanted)
		}
	}

	// The package clause, not the directory name, is what code writes.
	if got["example.com/proj/named/dirname"] != "different" {
		t.Errorf("clause name = %q, want different", got["example.com/proj/named/dirname"])
	}
}

func TestResolveNeverGuesses(t *testing.T) {
	ix := &Index{byName: map[string][]Pkg{
		"trace": {
			{Name: "trace", Path: "example.com/proj/internal/trace"},
			{Name: "trace", Path: "example.com/proj/other/trace"},
		},
		"pub": {{Name: "pub", Path: "example.com/proj/pub"}},
	}}

	if p, err := ix.Resolve("pub"); err != nil || p.Path != "example.com/proj/pub" {
		t.Errorf("Resolve(pub) = %v, %v", p, err)
	}
	// A name the host does not offer is not an error: goimports may still know
	// it from the stdlib.
	if p, err := ix.Resolve("strings"); err != nil || p.Path != "" {
		t.Errorf("Resolve(strings) = %v, %v; want zero, nil", p, err)
	}
	_, err := ix.Resolve("trace")
	if err == nil {
		t.Fatal("ambiguous name resolved silently")
	}
	// Both candidates must appear, or the message cannot be acted on.
	for _, want := range []string{"internal/trace", "other/trace"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q omits %q", err, want)
		}
	}
}

func TestDetectRejectsNonModule(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain on PATH")
	}
	// A directory outside any module: t.TempDir is under /var/folders, which
	// has no go.mod above it — the shape of a folder of loose Go files, which
	// -host cannot attach to.
	dir := t.TempDir()
	if _, err := Detect(dir); err == nil {
		t.Fatal("Detect succeeded outside a module")
	}
}

func TestDetectWalksUp(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain on PATH")
	}
	dir := writeHost(t)
	h, err := Detect(filepath.Join(dir, "pub"))
	if err != nil {
		t.Fatal(err)
	}
	if h.Path != "example.com/proj" {
		t.Errorf("Path = %q, want example.com/proj", h.Path)
	}
	// Detect must report the module root, not the directory it was given.
	// Both sides are resolved because `go env GOMOD` reports a real path, and
	// on darwin /var is a symlink to /private/var.
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := filepath.EvalSymlinks(h.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("Dir = %q, want %q", got, want)
	}
}

func TestBuildableRejectsAFutureDirective(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(p, []byte("module example.com/future\n\ngo 1.99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := FromGoMod(p)
	if err == nil {
		t.Fatal("accepted a go directive above the installed toolchain")
	}
	// GOTOOLCHAIN=local turns this into a hard error with a message naming a
	// temp path; saying it here is the difference between a fixable report and
	// a mystery.
	if !strings.Contains(err.Error(), "1.99") {
		t.Errorf("error does not name the directive: %v", err)
	}
}

// A go line naming the installed release exactly, patch and all, builds. The
// check once compared against version.Lang of the toolchain, which drops the
// patch: "go1.27" sorts below "go1.27.0", so such a module was refused.
func TestBuildableAcceptsTheInstalledRelease(t *testing.T) {
	out, err := exec.Command("go", "env", "GOVERSION").Output()
	if err != nil {
		t.Skip("no go toolchain on PATH")
	}
	installed := strings.TrimSpace(string(out))
	if !version.IsValid(installed) {
		t.Skipf("toolchain %q is not a release", installed)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "go.mod")
	mod := "module example.com/exact\n\ngo " + strings.TrimPrefix(installed, "go") + "\n"
	if err := os.WriteFile(p, []byte(mod), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := FromGoMod(p); err != nil {
		t.Fatalf("refused a go directive naming the installed release: %v", err)
	}
}

func TestSumIsTheHostsOwn(t *testing.T) {
	dir := writeHost(t)
	h := &Host{Dir: dir, Path: "example.com/proj", Go: "1.24"}

	// writeHost's module has no third-party requirements, so it has no go.sum.
	// That is the shape every host had before a real project attached, and it
	// must read as "nothing to copy" rather than as an error.
	sum, err := h.Sum()
	if err != nil {
		t.Fatalf("Sum with no go.sum: %v", err)
	}
	if sum != nil {
		t.Errorf("Sum = %q, want nil when the host has no go.sum", sum)
	}

	const body = "example.com/dep v1.2.3 h1:abc=\nexample.com/dep v1.2.3/go.mod h1:def=\n"
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	sum, err = h.Sum()
	if err != nil {
		t.Fatal(err)
	}
	// Verbatim: a rewritten sum is a sum that no longer verifies.
	if string(sum) != body {
		t.Errorf("Sum = %q, want %q", sum, body)
	}
}
