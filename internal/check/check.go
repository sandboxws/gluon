// Package check type-checks the generated program inside gluon's own process,
// using export data produced by the session's temp module.
//
// This is the fast path that v1 did not have. A build costs ~200ms; a warm
// type check costs ~30µs, so anything the checker can answer on its own — a
// type error, a call with no results, a constant expression, a declaration
// that only needed validating — never reaches the toolchain at all.
//
// Two importers were rejected before this one:
//
//   - go/importer.ForCompiler(...,"gc",nil) cannot work. Since Go 1.20 there
//     is no pre-installed export data; $GOROOT/pkg holds only include and
//     tool, with zero .a files, so it cannot resolve even "fmt".
//   - golang.org/x/tools/go/packages is correct but shells out to `go list` on
//     every call, which is the ~200ms this package exists to avoid.
//
// gcexportdata.NewImporter is also unsuitable: it runs `go list` once per
// import path and ignores the caller's environment, so gluon's hermetic env
// would not apply. Instead the export set is loaded with a single
// `go list -deps -export` whenever the import set grows, and the resulting
// *types.Package values are cached for the life of the session.
package check

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/tools/go/gcexportdata"
)

// Filename is the name the generated program is checked under. It matches what
// `go build` reports so a single diagnostic rewriter serves both paths.
const Filename = "main.go"

type Options struct {
	// Dir is the temp module. `go list` runs here so imports resolve in that
	// module's context rather than the user's cwd.
	Dir string
	// Env is the hermetic environment; it must be the same one the build uses,
	// or the checker could resolve a package the compiler will not.
	Env []string
	// Runtime is the injected runtime's sources, filename → contents, checked
	// alongside main.go — they are siblings in the same package, so they have
	// to be part of every check or __gluonPrint would be undefined. Exactly
	// what render.RuntimeFiles emits: the fd-gate entry is already the one
	// variant matching this machine's GOOS, so no build-tag evaluation happens
	// here.
	Runtime map[string]string
}

// Checker holds the per-session caches.
type Checker struct {
	opts Options

	// mu guards every cache below. The checker used to be single-threaded by
	// construction — one Submit at a time, and completion never asked while
	// one runs (invariant 15) — but the warm-up after :get loads export data
	// on a goroutine, so a Check arriving in the meantime has to wait rather
	// than race the maps it is filling. Nothing else about the ownership
	// changed: this is one background loader, not concurrent checking.
	mu sync.Mutex

	// fset spans the whole session. Positions recorded when export data was
	// read must stay meaningful, so it is never replaced — only appended to.
	fset *token.FileSet
	// pkgs is the shared import cache. Reusing it is what turns a 1.8ms check
	// into a 30µs one: the second check re-reads no export data at all.
	pkgs map[string]*types.Package
	// exports maps an import path to its export data file, refreshed only when
	// an import appears that is not already known.
	exports map[string]string
	// rt is the parsed runtime. Its sources never change, so they are parsed
	// once, in filename order so fset positions stay deterministic.
	rt []*ast.File

	// disabled records that the checker could not be used, with the reason.
	// Every caller must be able to fall back to building; a checker that
	// cannot resolve export data must never be mistaken for a clean program.
	disabled error
}

func New(opts Options) *Checker {
	return &Checker{
		opts:    opts,
		fset:    token.NewFileSet(),
		pkgs:    map[string]*types.Package{},
		exports: map[string]string{},
	}
}

// Disabled reports why the checker is unavailable, or nil if it is working.
// A disabled checker is not an error condition: it means callers pay v1's
// prices, which is the correct fallback when export data cannot be read.
func (c *Checker) Disabled() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.disabled
}

// Result is one successful run of the type checker. Errs holds the
// diagnostics; a non-empty Errs is a program that will not compile.
type Result struct {
	Fset *token.FileSet
	Pkg  *types.Package
	Info *types.Info
	File *ast.File
	Errs []types.Error
}

// Check parses and type-checks src as the session's main.go. A nil error means
// the checker ran; it does not mean the program is valid — read Errs for that.
func (c *Checker) Check(src []byte) (*Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.disabled != nil {
		return nil, c.disabled
	}

	file, err := parser.ParseFile(c.fset, Filename, src, parser.ParseComments)
	if err != nil {
		// A syntax error is a real answer, and a cheaper one than the build.
		return nil, err
	}

	rt, err := c.runtime()
	if err != nil {
		c.disabled = err
		return nil, err
	}

	// The injected runtime imports fmt, reflect, syscall and friends. Its
	// files are siblings in the same package, so their imports need export
	// data just as much as the user's do.
	paths := importPaths(file)
	for _, f := range rt {
		paths = append(paths, importPaths(f)...)
	}
	if err := c.ensure(paths); err != nil {
		c.disabled = err
		return nil, err
	}

	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
		Scopes:     map[ast.Node]*types.Scope{},
	}

	var errs []types.Error
	conf := types.Config{
		Importer: &exportImporter{fset: c.fset, files: c.exports, cache: c.pkgs},
		// Without an Error hook Check stops at the first problem, which would
		// throw away every diagnostic after it.
		Error: func(err error) {
			var te types.Error
			if errors.As(err, &te) {
				errs = append(errs, te)
			}
		},
		// goimports already owns the import block; complaining about an import
		// the user never typed would be noise.
		DisableUnusedImportCheck: true,
		// GoVersion is deliberately unset. gluon's go/types comes from the
		// toolchain that built gluon: naming a higher version is an error, and
		// naming a lower one would reject syntax the real build accepts.
		//
		// Sizes is set explicitly because the nil default is documented as
		// amd64, and :layout reports field offsets and padding for the machine
		// the program will actually be built for.
		Sizes: types.SizesFor("gc", runtime.GOARCH),
	}

	pkg, err := conf.Check("main", c.fset, append([]*ast.File{file}, rt...), info)
	if err != nil && len(errs) == 0 {
		// A failure with no diagnostics is the checker itself giving up —
		// a bad import, say — not a verdict on the user's code.
		return nil, err
	}
	return &Result{Fset: c.fset, Pkg: pkg, Info: info, File: file, Errs: errs}, nil
}

// runtime parses the injected runtime once and keeps the ASTs. go/types does
// not mutate the trees it reads, so the same files can back every check.
func (c *Checker) runtime() ([]*ast.File, error) {
	if c.rt != nil {
		return c.rt, nil
	}
	names := make([]string, 0, len(c.opts.Runtime))
	for name := range c.opts.Runtime {
		names = append(names, name)
	}
	sort.Strings(names)
	files := make([]*ast.File, 0, len(names))
	for _, name := range names {
		f, err := parser.ParseFile(c.fset, name, c.opts.Runtime[name], 0)
		if err != nil {
			return nil, fmt.Errorf("parsing injected runtime %s: %w", name, err)
		}
		files = append(files, f)
	}
	c.rt = files
	return files, nil
}

// ensure loads export data for any import path not already known. Loading is
// all-or-nothing per call and covers transitive dependencies, so a session
// that settles on a stable import set stops paying for this entirely.
func (c *Checker) ensure(paths []string) error {
	var missing []string
	for _, p := range paths {
		if p == "unsafe" {
			continue
		}
		if _, ok := c.exports[p]; !ok {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	// Listing the import paths rather than "." keeps the user's own code out
	// of it: `-export` on the main package would try to compile the very
	// program the checker exists to avoid compiling, and would fail whenever
	// the code has an error — exactly when the checker is needed most.
	args := append([]string{"list", "-deps", "-export", "-e", "-json=ImportPath,Export,Error", "--"}, missing...)
	cmd := exec.Command("go", args...)
	cmd.Dir = c.opts.Dir
	cmd.Env = c.opts.Env
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return fmt.Errorf("go list: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return fmt.Errorf("go list: %w", err)
	}

	dec := json.NewDecoder(strings.NewReader(string(out)))
	found := 0
	for {
		var p struct {
			ImportPath string
			Export     string
		}
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return fmt.Errorf("go list output: %w", err)
		}
		if p.Export != "" {
			c.exports[p.ImportPath] = p.Export
			found++
		}
	}
	if found == 0 {
		return fmt.Errorf("no export data for %s", strings.Join(missing, ", "))
	}
	return nil
}

// importPaths reads the import block back off the parsed file.
func importPaths(f *ast.File) []string {
	out := make([]string, 0, len(f.Imports))
	for _, im := range f.Imports {
		if p, err := strconv.Unquote(im.Path.Value); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// exportImporter resolves imports from the pre-loaded export set. Anything not
// in the set is a hard error rather than a fallback to `go list`: a silent
// per-import shell-out is the cost this package exists to avoid.
type exportImporter struct {
	fset  *token.FileSet
	files map[string]string
	cache map[string]*types.Package
}

func (i *exportImporter) Import(path string) (*types.Package, error) {
	return i.ImportFrom(path, "", 0)
}

func (i *exportImporter) ImportFrom(path, _ string, _ types.ImportMode) (*types.Package, error) {
	if path == "unsafe" {
		return types.Unsafe, nil
	}
	if p, ok := i.cache[path]; ok && p.Complete() {
		return p, nil
	}
	name, ok := i.files[path]
	if !ok {
		return nil, fmt.Errorf("no export data for %q", path)
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r, err := gcexportdata.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("reading export data for %q: %w", path, err)
	}
	return gcexportdata.Read(r, i.fset, i.cache, path)
}

// Loaded returns a package only if its types are already in the cache, and nil
// otherwise. It never loads.
//
// The distinction from Lookup is the whole point: loading a package runs
// `go list -deps -export`, which compiles what it lists. A command that
// promises to answer without building — :iface searching for implementors —
// may consult what a previous check already paid for, and must not pay for
// more.
func (c *Checker) Loaded(path string) *types.Package {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.disabled != nil {
		return nil
	}
	if p, ok := c.pkgs[path]; ok && p.Complete() {
		return p
	}
	return nil
}

// Lookup returns a package by import path, loading its export data if it is
// not already in the cache.
//
// The inspector needs this to ask whether a type satisfies fmt.Stringer or
// io.Reader: those interfaces have to be real *types.Interface values from the
// same checker, and the session itself may never import the package they live
// in.
func (c *Checker) Lookup(path string) (*types.Package, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.disabled != nil {
		return nil, c.disabled
	}
	if p, ok := c.pkgs[path]; ok && p.Complete() {
		return p, nil
	}
	if err := c.ensure([]string{path}); err != nil {
		return nil, err
	}
	imp := &exportImporter{fset: c.fset, files: c.exports, cache: c.pkgs}
	return imp.Import(path)
}

// Warm loads a package's types ahead of the keystroke that asks for them.
//
// It is Lookup with the answer thrown away and every failure swallowed. A
// module's root need not be a package at all — k8s.io/api has no code in it —
// and that is not a condition to report: the caller asked for a head start,
// not for a verdict. In particular it never sets disabled, because a path that
// is not a package says nothing about whether the checker works.
//
// Loading is what makes it worth doing. ensure alone would fill the export
// map, and Loaded reads the type cache, so completion would still pay to read
// the export data on the first keystroke that named the package.
func (c *Checker) Warm(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.disabled != nil {
		return
	}
	if p, ok := c.pkgs[path]; ok && p.Complete() {
		return
	}
	if err := c.ensure([]string{path}); err != nil {
		return
	}
	imp := &exportImporter{fset: c.fset, files: c.exports, cache: c.pkgs}
	_, _ = imp.Import(path)
}
