package find

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// mktree builds a directory tree from a path -> contents map. A path ending in
// "/" is a directory.
func mktree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, content := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if strings.HasSuffix(p, "/") {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestRepoStopsAtTheGitDirectory is invariant 12's ceiling.
//
// Without it every search runs from wherever the walk happened to stop, which
// in the recorded failure was the user's home directory.
func TestRepoStopsAtTheGitDirectory(t *testing.T) {
	root := t.TempDir()
	mktree(t, root, map[string]string{
		"repo/.git/HEAD":       "ref: refs/heads/main\n",
		"repo/apps/api/go.mod": "module x\n",
	})
	got, ok := Repo(filepath.Join(root, "repo", "apps", "api"))
	if !ok {
		t.Fatal("no repository found")
	}
	want := filepath.Join(root, "repo")
	if got != want {
		t.Errorf("Repo = %q, want %q", got, want)
	}
}

// TestRepoRefusesTheHomeDirectory is the home-directory case, made a rule.
//
// A .git in $HOME is a dotfiles checkout. Accepting it as a ceiling would let a
// bounded search roam the entire home directory, which is exactly how a
// detector once reported another application's project folder as a match.
func TestRepoRefusesTheHomeDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mktree(t, home, map[string]string{
		".git/HEAD":  "ref: refs/heads/main\n",
		"code/x.txt": "",
	})
	if got, ok := Repo(filepath.Join(home, "code")); ok {
		t.Errorf("Repo accepted the home directory as a repository: %q", got)
	}
}

// TestRepoReportsWhenThereIsNone.
//
// "no repository" and "the repository is /" must be different answers: the
// caller uses the first to mean "do not walk up at all".
func TestRepoReportsWhenThereIsNone(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dir, "nowhere"))
	if got, ok := Repo(dir); ok {
		t.Errorf("Repo found %q where there is no .git", got)
	}
}

// TestSkipCoversWhatEachDetectorPrunedSeparately.
//
// One detector pruned node_modules and vendor, another also pruned dist, and
// the database detector needs testdata and build as well. Three lists that were
// each nearly right is the drift this package exists to end.
func TestSkipCoversWhatEachDetectorPrunedSeparately(t *testing.T) {
	for _, name := range []string{
		"node_modules", "vendor", "dist", "build", "testdata", "target", "coverage",
		".git", ".next", "_scratch",
	} {
		if !Skip(name) {
			t.Errorf("Skip(%q) = false, want it pruned", name)
		}
	}
	for _, name := range []string{"src", "internal", "config", "apps", "api", "db"} {
		if Skip(name) {
			t.Errorf("Skip(%q) = true, want it searched", name)
		}
	}
}

// TestWalkDoesNotEnterANestedModule is the monorepo lookalike rule.
//
// apps/api and apps/web are two projects. A database configured in the one you
// are not standing in is near enough to be found and wrong enough to matter,
// which is the shape of every failure invariant 12 records.
func TestWalkDoesNotEnterANestedModule(t *testing.T) {
	root := t.TempDir()
	mktree(t, root, map[string]string{
		"go.mod":                "module api\n",
		".env":                  "DATABASE_URL=postgres://localhost/api\n",
		"internal/store.go":     "package store\n",
		"web/go.mod":            "module web\n",
		"web/.env":              "DATABASE_URL=postgres://localhost/web\n",
		"node_modules/pkg/.env": "DATABASE_URL=postgres://localhost/nope\n",
	})

	var seen []string
	err := Walk(root, 3, func(p string, d fs.DirEntry) error {
		rel, _ := filepath.Rel(root, p)
		seen = append(seen, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(seen)
	for _, unwanted := range []string{"web/.env", "web/go.mod", "node_modules/pkg/.env"} {
		for _, got := range seen {
			if got == unwanted {
				t.Errorf("Walk entered %s; saw %v", unwanted, seen)
			}
		}
	}
	var foundOwn bool
	for _, got := range seen {
		if got == ".env" {
			foundOwn = true
		}
	}
	if !foundOwn {
		t.Errorf("Walk missed the module's own .env; saw %v", seen)
	}
}

// TestWalkRespectsTheDepthBound keeps the search finite. An unbounded walk of a
// monorepo is tens of seconds of stat calls to answer a question with one
// obvious answer.
func TestWalkRespectsTheDepthBound(t *testing.T) {
	root := t.TempDir()
	mktree(t, root, map[string]string{
		"a/b/shallow.txt":    "",
		"a/b/c/d/e/deep.txt": "",
	})
	var seen []string
	if err := Walk(root, 2, func(p string, d fs.DirEntry) error {
		rel, _ := filepath.Rel(root, p)
		seen = append(seen, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, got := range seen {
		if strings.Contains(got, "deep.txt") {
			t.Errorf("Walk went past the depth bound: %v", seen)
		}
	}
}

// TestAncestorsReadsUpwardButNotSideways.
//
// A compose file at the repository root belongs to the module below it; a .env
// in a sibling service does not. Ancestors is what expresses that difference —
// it returns the chain, and the caller reads only each link's own entries.
func TestAncestorsReadsUpwardButNotSideways(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	api := filepath.Join(repo, "apps", "api")
	mktree(t, root, map[string]string{"repo/apps/api/go.mod": "module api\n", "repo/apps/web/.env": ""})

	got := Ancestors(api, repo)
	want := []string{api, filepath.Join(repo, "apps"), repo}
	if len(got) != len(want) {
		t.Fatalf("Ancestors = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Ancestors[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestAncestorsStopsAtRootEvenWhenRootIsTheStart.
func TestAncestorsStopsAtRootEvenWhenRootIsTheStart(t *testing.T) {
	dir := t.TempDir()
	got := Ancestors(dir, dir)
	if len(got) != 1 || got[0] != dir {
		t.Errorf("Ancestors(dir, dir) = %v, want [%s]", got, dir)
	}
}
