package host

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Pkg is one importable package in the host module.
type Pkg struct {
	Name string // the package clause, e.g. "pricing"
	Path string // the import path, e.g. "example.com/shop/internal/pricing"
}

// Index maps a package name to the host packages that could satisfy it.
//
// It exists because goimports cannot be trusted to answer this question. Its
// candidate set includes the whole module cache, so with a typical cache a bare
// `trace.New(...)` against a host that has its own internal/trace resolves to
// k8s.io/utils/trace — a package that is not even in the build list. Injecting the host's own import
// before goimports runs settles the name first; goimports then only fills in
// the stdlib, which is what it is good at.
type Index struct {
	byName map[string][]Pkg
	total  int
}

// Total is how many importable packages the host offers.
func (ix *Index) Total() int { return ix.total }

// Names lists the package names available, sorted.
func (ix *Index) Names() []string {
	out := make([]string, 0, len(ix.byName))
	for n := range ix.byName {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// All lists every package, sorted by import path.
func (ix *Index) All() []Pkg {
	var out []Pkg
	for _, cands := range ix.byName {
		out = append(out, cands...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Resolve returns the host package a bare qualifier names. A zero Pkg means
// the host does not offer that name, which is not an error: goimports may
// still know it from the stdlib.
//
// An ambiguous name is an error naming every candidate, never a guess
// (invariant 13): picking one of several plausible answers silently is how a
// tool teaches something false.
func (ix *Index) Resolve(name string) (Pkg, error) {
	cands := ix.byName[name]
	switch len(cands) {
	case 0:
		return Pkg{}, nil
	case 1:
		return cands[0], nil
	}
	paths := make([]string, len(cands))
	for i, c := range cands {
		paths[i] = "  " + c.Path
	}
	sort.Strings(paths)
	return Pkg{}, fmt.Errorf("%q is ambiguous in this host — write the import you mean:\n%s",
		name, strings.Join(paths, "\n"))
}

// Index enumerates the packages this session may import from the host.
//
// It walks the tree rather than running `go list ./...`, which was measured at
// ~1.8s in a 1,724-package module because it loads every package — 1,713 of
// them `main`, which can never be imported by anything. Reading one package
// clause per directory answers the same question in a fraction of the time.
//
// The internal/ rule is applied here, and it is what makes the result
// unambiguous: that module held ten packages named `trace`, but only the one
// under its root internal/ had a parent that prefixes the session's import
// path, so the other nine were not candidates at all. The rule that made this
// feature hard is the same rule that disambiguates it.
func (h *Host) Index() (*Index, error) {
	h.walks.Add(1)
	ix := &Index{byName: map[string][]Pkg{}}
	root := filepath.Clean(h.Dir)

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable directory is not worth failing the attach over;
			// the packages under it simply do not appear.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if p != root && skipDir(d.Name()) {
			return fs.SkipDir
		}
		// A directory with its own go.mod is a different module. Its packages
		// are not reachable through this module path, so the subtree is not
		// ours to index.
		if p != root {
			if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
				return fs.SkipDir
			}
		}

		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		ip := h.Path
		if rel != "." {
			ip = path.Join(h.Path, filepath.ToSlash(rel))
		}
		if !h.CanImport(ip) {
			// Still descend: a non-importable internal/ dir can hold nested
			// packages, and their own reachability is judged separately.
			return nil
		}
		name, ok := pkgName(p)
		if !ok || name == "main" {
			return nil
		}
		ix.byName[name] = append(ix.byName[name], Pkg{Name: name, Path: ip})
		ix.total++
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ix, nil
}

// skipDir mirrors the directories the go command itself never treats as
// packages, so the index cannot offer something a build would refuse.
func skipDir(name string) bool {
	return name == "vendor" || name == "testdata" || name == "node_modules" ||
		strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// pkgName reads the package clause out of the first non-test Go file in dir.
// PackageClauseOnly stops the parser at the clause, so this stays cheap even
// over a tree with thousands of files.
func pkgName(dir string) (string, bool) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, n), nil, parser.PackageClauseOnly)
		if err != nil || f.Name == nil {
			continue
		}
		return f.Name.Name, true
	}
	return "", false
}

// CanImport reports whether the session module may import p, applying the
// internal/ rule exactly as cmd/go does.
//
// findInternal below returns the *final* internal element deliberately: it
// produces the most restrictive requirement on the importer, which is what
// cmd/go/internal/load/pkg.go documents and does.
func (h *Host) CanImport(p string) bool {
	i, ok := findInternal(p)
	if !ok {
		return true
	}
	return hasPathPrefix(h.SessionPath(), p[:i])
}

func findInternal(p string) (int, bool) {
	switch {
	case strings.HasSuffix(p, "/internal"):
		return len(p) - len("internal"), true
	case strings.Contains(p, "/internal/"):
		return strings.LastIndex(p, "/internal/") + 1, true
	case p == "internal", strings.HasPrefix(p, "internal/"):
		return 0, true
	}
	return 0, false
}

// hasPathPrefix is str.HasPathPrefix from cmd/go: prefix may carry a trailing
// slash, and a match must fall on an element boundary.
func hasPathPrefix(s, prefix string) bool {
	if len(s) == len(prefix) {
		return s == prefix
	}
	if prefix == "" {
		return true
	}
	if len(s) > len(prefix) {
		if prefix[len(prefix)-1] == '/' || s[len(prefix)] == '/' {
			return s[:len(prefix)] == prefix
		}
	}
	return false
}
