// Package find holds the primitives every gluon detector needs, and none of the
// policy any one of them applies.
//
// It exists because detectors that each grew their own copy of the same upward
// walk and the same prune list drifted apart — one pruned dist/, another did
// not — before anyone noticed. Invariant 12 is enforced by those two things
// being right, so they are worth having in exactly one place: the database
// detector, project-file lookup and :reload all walk with them.
//
// What is deliberately *not* here is a shared resolve(). Finding a single
// directory whose path ends in a name hint is one policy; a file detector's
// question is different in kind: enumerate every match across a bounded set and
// rank them. Merging the two would produce a function with a mode parameter,
// which is the shape of a function that should have stayed two.
package find

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Repo is the repository directory containing dir — the ceiling every search
// stops at. Invariant 12: a detector never looks above it.
//
// A .git found at the user's home directory is refused, and ok is false. That
// is a dotfiles checkout, not the repository a project lives in, and treating
// it as a ceiling is precisely how a detector once roamed the whole home
// directory and matched an unrelated application's project folder. When there
// is no repository the caller must not walk up at all rather than fall back to
// something wider.
func Repo(dir string) (string, bool) {
	home, _ := os.UserHomeDir()
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}
	for d := abs; ; {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			if home != "" && d == home {
				return "", false
			}
			return d, true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", false
		}
		d = parent
	}
}

// Skip reports the directories no gluon search descends into.
//
// The list is the union of what the detectors each pruned separately before
// they shared it. It is about searching a working tree for files somebody
// wrote — which is a different question from host.skipDir's, that being about
// what the go command treats as a package, and why that one stays where it is.
func Skip(name string) bool {
	switch name {
	case "node_modules", "vendor", "dist", "build", "testdata", "target", "coverage":
		return true
	}
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// Walk visits every file at most depth levels below root, pruning Skip
// directories and never descending into a directory that holds its own go.mod.
//
// The nested-module rule is what keeps a monorepo's other service out of the
// answer. apps/api and apps/web are two projects, and the database configured
// in one is the lookalike invariant 12 is about — near enough to be found,
// wrong enough to matter.
//
// Errors from fn stop the walk and are returned; an unreadable directory is
// skipped rather than fatal, because a permission error somewhere in a tree is
// not a reason to answer nothing.
func Walk(root string, depth int, fn func(path string, d fs.DirEntry) error) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return fn(p, d)
		}
		if p == root {
			return nil
		}
		if Skip(d.Name()) {
			return fs.SkipDir
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return fs.SkipDir
		}
		if strings.Count(rel, string(filepath.Separator))+1 > depth {
			return fs.SkipDir
		}
		// A nested module is a different project.
		if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
			return fs.SkipDir
		}
		return nil
	})
}

// Ancestors is dir and every directory above it up to and including root,
// nearest first.
//
// It exists because a project's database configuration routinely sits above its
// module — apps/api/go.mod with compose.yaml at the repository root — while a
// sibling's configuration must stay invisible. Walking ancestors and reading
// only their own entries gets the first without the second; descending into
// each ancestor would find apps/web/.env, which is another service's database.
func Ancestors(dir, root string) []string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return []string{dir}
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return []string{abs}
	}
	var out []string
	for d := abs; ; {
		out = append(out, d)
		if d == rootAbs {
			break
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return out
}
