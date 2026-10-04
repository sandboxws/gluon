// Package release is what each Go release added, read from the toolchain that
// shipped it.
//
// The distribution carries this as data. $GOROOT/api/go1.N.txt is every stdlib
// API addition for release N, one declaration per line, and since go1.19.txt
// every line ends in the GitHub issue number of the proposal that accepted it —
// so a citation is a fact on disk rather than a lookup. $GOROOT/doc/godebug.md
// carries the behaviour changes and the GODEBUG setting that reverts each one.
//
// Reading those rather than shipping a copy of go.dev is the same argument
// internal/repl makes about `go doc`: the installed toolchain is the authority,
// and a second copy is a thing that can be wrong. It also means this package
// makes no network call and needs no cache — a release note is already local.
//
// The one thing the api files do not describe is a change to the *language*.
// Generics, range-over-func, per-iteration loop variables and generic type
// aliases are not API additions and appear in no file here. Those are the
// curated half, in notes/, and they are the half that carries a runnable
// snippet — see notes.go for the rule that keeps them honest.
package release

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// A Release is one Go version, as the installed toolchain describes it.
type Release struct {
	// Version is the minor version alone: "1.24". Go 1.0 is "1.0", though its
	// file is named go1.txt and holds the whole original API rather than a
	// delta — Initial says so.
	Version string
	// Initial marks the release whose file is the Go 1 API surface itself. Its
	// API entries were not "added" by it in the sense every other file means.
	Initial bool
	// Lang is the curated language changes, and is empty for a release nobody
	// has written one for. Empty means unwritten, never "there were none" —
	// Curated is the difference.
	Lang []Note
	// Curated reports whether anyone has looked at this release's language
	// changes. It is the distinction :doc -examples draws between finding none
	// and not having looked, and a screen that cannot draw it would be claiming
	// Go 1.25 changed no syntax on the strength of nobody having checked.
	Curated bool
	// API is the stdlib additions, one entry per declaration with the
	// GOOS/GOARCH duplicates collapsed into Platforms.
	API []Symbol
	// Behaviour is the GODEBUG history for this release, one entry per
	// paragraph of doc/godebug.md.
	Behaviour []Behaviour
	// Gates is the syntax the type checker admits only at this version. It is
	// not what the release changed — see gates.go for why it cannot be — but it
	// is a floor under the language column: a release nobody has written up
	// still names what the checker gates, instead of showing nothing.
	Gates []Gate
}

// Packages is the sorted set of import paths this release touched.
func (r Release) Packages() []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range r.API {
		if !seen[s.Pkg] {
			seen[s.Pkg] = true
			out = append(out, s.Pkg)
		}
	}
	sortStrings(out)
	return out
}

// NamedGates is the gates for this release that carry a feature name.
func (r Release) NamedGates() []Gate {
	var out []Gate
	for _, g := range r.Gates {
		if g.Named() {
			out = append(out, g)
		}
	}
	return out
}

// Note returns the curated language change with this name.
func (r Release) Note(name string) (Note, bool) {
	for _, n := range r.Lang {
		if n.Name == name {
			return n, true
		}
	}
	return Note{}, false
}

// A Symbol is one declaration a release added to the standard library.
type Symbol struct {
	// Pkg is the import path.
	Pkg string
	// Decl is the declaration as the api file spells it: "func Keys(...)",
	// "type Config struct, Field T", "method (*T) M() error".
	Decl string
	// Issue is the proposal that accepted it, or 0. Mandatory from Go 1.19
	// onward per $GOROOT/api/README; absent before, which is why this is a
	// number to test rather than a string to print blindly.
	Issue int
	// Platforms is the GOOS-GOARCH pairs this declaration is limited to, empty
	// when it is on every platform. go1.20.txt is 9,165 lines of which 8,864
	// are the same syscall declarations repeated per platform; collapsing them
	// is the difference between a readable release and nine thousand rows.
	Platforms []string
}

// Kind is the leading word of the declaration — "func", "type", "method",
// "const", "var" — for a caller that wants to group by it.
func (s Symbol) Kind() string {
	if i := strings.IndexByte(s.Decl, ' '); i > 0 {
		return s.Decl[:i]
	}
	return s.Decl
}

// URL is the proposal this declaration came from, or "" when the file predates
// the requirement. It is printed and never fetched, the way :doc -url is.
func (s Symbol) URL() string {
	if s.Issue == 0 {
		return ""
	}
	return "https://go.dev/issue/" + strconv.Itoa(s.Issue)
}

// A Behaviour is one paragraph of the GODEBUG history: something a release
// changed about how an existing program runs, and the setting that reverts it.
type Behaviour struct {
	// Setting is the GODEBUG key, when the paragraph names one.
	Setting string
	// Text is the paragraph, as doc/godebug.md wrote it.
	Text string
}

// Load reads every release the toolchain at goroot describes.
//
// goroot is a parameter rather than a lookup so the tests can run against a
// fixture tree, which is the same reason internal/theme takes its directory
// from the caller: a package that resolves its own paths is one whose real path
// is never exercised.
//
// Everything is read eagerly, including go1.txt's 30,871 lines and go1.1.txt's
// 50,454. Measured at 46.9ms for all 157,054 lines of a 1.27 distribution
// (darwin/arm64, M1 Pro), plus ~25ms for the checker's gates — a fifth of what
// one evaluation costs, so the caller caches this once and no release is parsed
// lazily. A lazy split would buy nothing anyway: the listing needs a count from
// every file.
func Load(goroot string) ([]Release, error) {
	vers, err := versions(goroot)
	if err != nil {
		return nil, err
	}
	debug := parseGodebug(readFile(filepath.Join(goroot, "doc", "godebug.md")))
	gates := Gates(goroot)
	out := make([]Release, 0, len(vers))
	for _, v := range vers {
		syms, err := parseAPI(readFile(apiPath(goroot, v)))
		if err != nil {
			return nil, fmt.Errorf("api/%s: %w", apiName(v), err)
		}
		notes, curated := notesFor(v)
		out = append(out, Release{
			Version:   v,
			Initial:   v == "1.0",
			Lang:      notes,
			Curated:   curated,
			API:       syms,
			Behaviour: debug[v],
			Gates:     GatesAt(gates, v),
		})
	}
	sortReleases(out)
	return out, nil
}

// Find is one release by version, accepting "1.24" or "go1.24".
func Find(rels []Release, v string) (Release, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "go")
	for _, r := range rels {
		if r.Version == v {
			return r, true
		}
	}
	return Release{}, false
}

// GOROOT is the installed toolchain's root, read once.
//
// `go env GOROOT` rather than runtime.GOROOT(): gluon runs the go on PATH, and
// a binary built by one toolchain and run beside another must describe the one
// it will actually build with. That is invariant 4's reasoning applied to the
// root instead of the version.
var GOROOT = sync.OnceValue(func() string {
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
})

// versions lists the releases goroot has an api file for.
func versions(goroot string) ([]string, error) {
	dir := filepath.Join(goroot, "api")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("no api directory in %s: %w", goroot, err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "go1") || !strings.HasSuffix(name, ".txt") {
			continue
		}
		switch name {
		case "go1.txt":
			out = append(out, "1.0")
			continue
		case "except.txt":
			continue
		}
		v := strings.TrimSuffix(strings.TrimPrefix(name, "go"), ".txt")
		if _, _, ok := split(v); ok {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no api files in %s", dir)
	}
	return out, nil
}

func apiName(v string) string {
	if v == "1.0" {
		return "go1.txt"
	}
	return "go" + v + ".txt"
}

func apiPath(goroot, v string) string { return filepath.Join(goroot, "api", apiName(v)) }

// readFile is the missing-file-is-empty read: a toolchain without doc/godebug.md
// still has an api directory, and half the data is better than an error that
// names neither half.
func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// split parses "1.24" into its two numbers.
func split(v string) (major, minor int, ok bool) {
	a, b, found := strings.Cut(v, ".")
	if !found {
		return 0, 0, false
	}
	major, err := strconv.Atoi(a)
	if err != nil {
		return 0, 0, false
	}
	minor, err = strconv.Atoi(b)
	if err != nil {
		return 0, 0, false
	}
	return major, minor, true
}

// Compare orders two "1.24"-style versions numerically. "1.9" is below "1.10",
// which is the whole reason this is not a string comparison.
func Compare(a, b string) int {
	am, an, aok := split(a)
	bm, bn, bok := split(b)
	switch {
	case !aok && !bok:
		return strings.Compare(a, b)
	case !aok:
		return -1
	case !bok:
		return 1
	case am != bm:
		return sign(am - bm)
	default:
		return sign(an - bn)
	}
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}
