// Package host resolves the Go module a gluon session attaches to, so a line
// typed at the prompt can import the surrounding project's packages.
//
// The hard part is `internal/`. Per `go help importpath`, a package under
// internal/ is importable only by code sharing the import path above it, so a
// module named gluon.local/session can never import
// example.com/shop/internal/pricing no matter how it is replaced.
//
// The rule is enforced in cmd/go/internal/load/pkg.go (disallowInternal), and
// in the module branch it is a pure string prefix test on the *importer's
// import path*:
//
//	parentOfInternal := p.ImportPath[:i]
//	if str.HasPathPrefix(importerPath, parentOfInternal) {
//		return nil
//	}
//
// No file-path or module-identity check is involved. So the temp module is
// given a path *underneath* the host's — example.com/shop/gluonsession — and
// the prefix test passes. Verified end to end: importing
// example.com/shop/internal/pricing from such a module builds and runs.
//
// Nested module paths are ordinary (golang.org/x/tools and .../gopls), and the
// replace itself needs no go.sum entry — a replace to a directory is never
// verified — so the host's domain is never resolved. Its requirements still
// are, which is what Sum carries across.
package host

import (
	"errors"
	"fmt"
	"go/version"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

// nested is the last element of the session module's path. Its only job is to
// sit one element below the host so the prefix test in disallowInternal
// matches; the directory on disk is still gluon's own temp dir.
const nested = "gluonsession"

// Host is a module gluon can attach a session to.
type Host struct {
	// Dir is the directory holding the host's go.mod.
	Dir string
	// Path is the host's module path, e.g. "example.com/shop".
	Path string
	// Go is the go directive copied verbatim from the host's go.mod. It is
	// never taken from `go env GOVERSION`: the host's code was written against
	// its own directive, and a session that claims a different one can accept
	// syntax the host rejects or reject syntax it uses.
	Go string

	// walks counts the directory walks Index has run. The walk is the ~0.3s
	// that moved off the attach and onto first use, and the count is the only
	// way to say so that is not a timing assertion — Walks is what the test
	// reads.
	walks atomic.Int64
}

// Walks is how many times Index has walked this host's tree. It exists for the
// test that pins the walk to first use rather than to attach.
func (h *Host) Walks() int { return int(h.walks.Load()) }

// SessionPath is the module path the temp module must declare.
func (h *Host) SessionPath() string { return path.Join(h.Path, nested) }

// String is what :use and gluon doctor report.
func (h *Host) String() string { return fmt.Sprintf("%s (go %s) at %s", h.Path, h.Go, h.Dir) }

// Mod is the go.mod text for a session attached to this host.
//
// The require is not optional. A bare replace does not put the module in the
// build list, so the import fails to resolve with a message that blames the
// import rather than the missing requirement.
//
// It is built through modfile rather than fmt so a directory containing a
// space or a quote is written as a quoted token instead of silently producing
// a go.mod that does not parse.
func (h *Host) Mod() (string, error) { return h.ModFor(nested) }

// ModFor is Mod for a module at an arbitrary path below the host. A scratch
// directory wants the same arrangement a session does, under its own name.
//
// The host's own requirements are copied in as indirect. Under module graph
// pruning — every go.mod with a 1.17 or later directive — the main module has
// to name every module providing a package it transitively imports; the
// replaced host's requirements are pruned out of the graph rather than
// inherited. Without them the build stops at "updates to go.mod needed; to
// update it: go mod tidy", which is advice the session cannot take: the go.mod
// is regenerated from here on every attach.
//
// The host's replaces come too, because Go applies replace directives only
// from the main module. A host that pins a fork or a sibling directory would
// otherwise build in the session against the module it replaced — the same
// import path resolving to different code, silently. A directory replace
// written relative to the host is made absolute, since the session module is
// not in the host's directory.
func (h *Host) ModFor(sub string) (string, error) {
	hm, err := h.modFile()
	if err != nil {
		return "", err
	}

	f := &modfile.File{}
	if err := f.AddModuleStmt(path.Join(h.Path, sub)); err != nil {
		return "", err
	}
	if err := f.AddGoStmt(h.Go); err != nil {
		return "", err
	}

	reqs := []*modfile.Require{{Mod: module.Version{Path: h.Path, Version: "v0.0.0"}}}
	for _, r := range hm.Require {
		if r.Mod.Path == h.Path {
			continue
		}
		reqs = append(reqs, &modfile.Require{Mod: r.Mod, Indirect: true})
	}
	f.SetRequire(reqs)

	if err := f.AddReplace(h.Path, "", h.Dir, ""); err != nil {
		return "", err
	}
	for _, r := range hm.Replace {
		if r.Old.Path == h.Path {
			continue
		}
		to := r.New.Path
		if r.New.Version == "" && modfile.IsDirectoryPath(to) && !filepath.IsAbs(to) {
			to = filepath.Join(h.Dir, filepath.FromSlash(to))
		}
		if err := f.AddReplace(r.Old.Path, r.Old.Version, to, r.New.Version); err != nil {
			return "", err
		}
	}

	f.Cleanup()
	out, err := f.Format()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// modFile parses the host's own go.mod. It is read per call rather than cached
// on the Host: ModFor runs when a session starts and when :use switches, not
// per line, and a host edited mid-session should be re-read rather than
// remembered.
// Requires is the host's own build list, as "path version" strings.
//
// It is the same shape Evaluator.Requires reports, so whatever asks "is this
// module available" gets one answer whether a session is attached or not —
// which matters because `gluon init` runs without an evaluator and still has to
// say which database drivers a project provides.
func (h *Host) Requires() ([]string, error) {
	f, err := h.modFile()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(f.Require))
	for _, r := range f.Require {
		out = append(out, r.Mod.Path+" "+r.Mod.Version)
	}
	return out, nil
}

func (h *Host) modFile() (*modfile.File, error) {
	p := filepath.Join(h.Dir, "go.mod")
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	return modfile.Parse(p, data, nil)
}

// Sum is the host's go.sum, to be written verbatim beside the session's
// go.mod.
//
// The replace exempts the host module itself from verification, but not the
// modules the host requires. Every third-party package a host package imports
// is resolved through the *session* module's go.sum, so without this one
// `go list -export` answers "missing go.sum entry" for each of them and the
// checker sees a package with no export data — not an import error naming the
// module that is actually missing. A host with no third-party requirements has
// nothing to miss, which is why this stayed invisible until a real project
// attached.
//
// No go.sum is not an error: a stdlib-only module has none, and a vendored one
// does not consult it.
func (h *Host) Sum() ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(h.Dir, "go.sum"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return b, err
}

// Detect finds the module containing dir. An empty dir means the working
// directory.
//
// `go env GOMOD` does the upward walk, honours GO111MODULE, and answers in a
// few milliseconds, which makes it the cheapest detector available. It reports
// the literal os.DevNull outside a module, and a directory of loose Go files
// with no go.mod is therefore not something -host can attach to.
func Detect(dir string) (*Host, error) {
	if dir == "" {
		dir = "."
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	} else if !fi.IsDir() {
		abs = filepath.Dir(abs)
	}

	cmd := exec.Command("go", "env", "GOMOD")
	cmd.Dir = abs
	// GOWORK=off for the same reason the evaluator sets it: the default is
	// auto, which searches containing directories, and a go.work elsewhere
	// under the user's tree would answer for a module that is not this one.
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go env GOMOD in %s: %w", abs, err)
	}

	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == os.DevNull {
		return nil, fmt.Errorf("%s is not inside a Go module", abs)
	}
	return FromGoMod(gomod)
}

// FromGoMod reads a go.mod and returns the host it describes.
func FromGoMod(gomod string) (*Host, error) {
	data, err := os.ReadFile(gomod)
	if err != nil {
		return nil, err
	}
	// modfile.Parse over ParseLax: a go.mod gluon cannot fully understand is
	// one whose go directive it might also misread, and that directive decides
	// whether the build works at all.
	f, err := modfile.Parse(gomod, data, nil)
	if err != nil {
		return nil, err
	}
	if f.Module == nil || f.Module.Mod.Path == "" {
		return nil, fmt.Errorf("%s declares no module path", gomod)
	}

	h := &Host{Dir: filepath.Dir(gomod), Path: f.Module.Mod.Path}
	if f.Go != nil {
		h.Go = f.Go.Version
	}
	if h.Go == "" {
		// A go.mod predating the directive. Anything the local toolchain can
		// build is fair, and 1.16 is the last release where it was optional.
		h.Go = "1.16"
	}
	if err := h.buildable(); err != nil {
		return nil, err
	}
	return h, nil
}

// buildable rejects a host whose go directive is above the installed
// toolchain. GOTOOLCHAIN=local — which gluon sets, and which is set in this
// user's go env — turns that into a hard error rather than a download, and the
// toolchain's own message names a temp path the user never wrote. Saying it
// here names the host instead.
func (h *Host) buildable() error {
	out, err := exec.Command("go", "env", "GOVERSION").Output()
	if err != nil {
		return fmt.Errorf("go toolchain not found on PATH: %w", err)
	}
	installed := strings.TrimSpace(string(out))
	// Against the release itself, not version.Lang of it: Lang("go1.27.0") is
	// "go1.27", which sorts below a "go 1.27.0" directive, so a module whose go
	// line named exactly the installed release was refused.
	if version.Compare("go"+h.Go, installed) > 0 {
		return fmt.Errorf("%s needs go %s but %s is installed — gluon runs with GOTOOLCHAIN=local, so it will not download one",
			h.Path, h.Go, installed)
	}
	return nil
}
