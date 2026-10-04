// Package eval owns the temp module and turns a session into output. The temp
// module is created once per session, not once per line, so the build cache
// stays warm — that is the difference between ~0.2s and ~1s per keystroke.
package eval

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/types"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/modfile"

	"github.com/sandboxws/gluon/internal/check"
	"github.com/sandboxws/gluon/internal/gluonrt"
	"github.com/sandboxws/gluon/internal/host"
	"github.com/sandboxws/gluon/internal/render"
	"github.com/sandboxws/gluon/internal/session"
)

// DefaultTimeout bounds a single evaluation. The whole session replays on
// every line, so one accidental `for {}` would otherwise wedge the REPL.
const DefaultTimeout = 30 * time.Second

type Evaluator struct {
	dir     string
	timeout time.Duration
	// imports caches what goimports last resolved. Re-running goimports costs
	// ~135ms; reusing the set and formatting in-process costs ~1ms, and the
	// build itself catches the rare line that changes the import set.
	imports []render.ImportSpec
	// resolved records that the current import set is a real answer rather
	// than an absent one. It starts true: a session whose newest line names no
	// qualifier gluon cannot already resolve needs no imports, and running
	// goimports to be told so costs ~135ms. importsSatisfied is what decides,
	// and it errs towards running the full pass.
	resolved bool
	// noValueFuncs remembers callees the compiler has rejected as having no
	// result, seeded with the common ones so the first use is already free.
	noValueFuncs map[string]bool
	// cache maps rendered program -> result, so re-running an identical
	// program (notably after :undo) skips the build and exec entirely.
	cache *resultCache
	// checker type-checks in-process. It is the fast path: a build is ~200ms,
	// a warm check ~30µs, so most lines never reach the toolchain.
	checker *check.Checker
	// healthy records that the last program run exited cleanly. A constant
	// expression may only be answered without running when it does: appending
	// 1+1 to a session whose earlier entry panics must still show the panic.
	healthy bool
	// attached is the host module the session imports from, or nil for a
	// standalone session. It is opt-in because attaching re-inherits the
	// host's package-load cost on every line.
	attached *host.Host
	// index is the attached host's importable packages, by name. It settles a
	// bare qualifier before goimports can offer something from the module
	// cache instead.
	//
	// It is built on first use rather than on attach. The walk is ~0.3s on a
	// large repository, and a `gluon -host` startup or an MCP auto-attach that
	// never names a host package used to pay it before the banner. indexOnce
	// is reset wherever the host or its source changes.
	index     *host.Index
	indexErr  error
	indexOnce sync.Once
	// preload is config's import list, by the name a line would use. It is
	// consulted the same way the host index is, so an entry nothing names is
	// never written.
	preload map[string]render.ImportSpec
	// got is the importable packages of the modules the session required
	// with :get, by package name, with the standard library's names in std.
	// It is what a bare qualifier resolves to before goimports can reach into
	// the whole module cache: after :get gorm.io/driver/postgres, `postgres`
	// is that package, and not whichever other one named postgres this
	// machine happens to hold. Built on first need and dropped by invalidate,
	// which every change to the requirement set calls.
	got map[string][]string
	std map[string]bool

	// The three below are set for the duration of one evalWith and restored
	// after it. They are fields rather than parameters because the write path
	// forks into a cached branch and a goimports branch, and both have to see
	// the same extra imports — threading them would mean four signatures where
	// one of them silently not being updated is the bug.

	// extraImports are merged into whatever the session already needs. It
	// exists for the blank driver import, which no qualifier names and which
	// seedImports therefore cannot produce.
	extraImports []render.ImportSpec
	// extraEnv is added to the child process only: not to the build, not to
	// the checker, not to `go doc`. It is how a resolved DSN reaches a running
	// program without being written into its source.
	extraEnv []string
	// redact is masked out of anything the child printed, before it becomes a
	// Result. The source cannot leak a secret it never held, so the child's own
	// output is the only surface left.
	redact []string

	// maxItems and maxDepth bound how much of a value the child describes.
	// They are evaluator fields rather than evalOpts because they are not a
	// property of one command: every path that builds a program wants them,
	// and a caller that forgot one would produce a truncation the user had
	// already asked not to have. They reach the child as environment, so the
	// program text is the same at every setting.
	maxItems, maxDepth int
}

type Result struct {
	// Output is just the new suffix produced by this line.
	Output string
	// Source is the fully rendered program, for :src and error context.
	Source   string
	ExitCode int
}

// TimeoutError is a run that hit the evaluation deadline, as opposed to one
// that failed on its own terms.
//
// Its message is the caller's own, byte for byte — the deadline means
// different things to different callers, which is why runBinary takes the
// sentence rather than composing one — so nothing a user reads changes. The
// type is what lets a caller tell a deadline from any other failure without
// matching on that text, which is the fragile thing this replaces.
type TimeoutError struct {
	Msg string
	// After is the deadline that passed.
	After time.Duration
}

func (e *TimeoutError) Error() string { return e.Msg }

// BuildError is a compile or link failure, already rewritten to refer to what
// the user typed rather than to a path under /var/folders.
type BuildError struct {
	Msg    string
	Source string
}

func (e *BuildError) Error() string { return e.Msg }

// ErrNoChecker is the type checker being switched off, as opposed to running
// and finding a problem. It is a sentinel because callers act on the
// difference: invariant 5 says the checker is never an authority, so a command
// that cannot consult it falls through to the compiler rather than refusing.
var ErrNoChecker = errors.New("type checking is disabled by GLUON_NO_TYPECHECK")

func New() (*Evaluator, error) {
	dir, err := os.MkdirTemp("", "gluon-session-")
	if err != nil {
		return nil, err
	}
	e := &Evaluator{
		dir:          dir,
		timeout:      DefaultTimeout,
		noValueFuncs: seedNoValue(),
		cache:        newResultCache(32),
		healthy:      true,
		resolved:     true,
		maxItems:     gluonrt.DefaultMaxItems,
		maxDepth:     gluonrt.DefaultMaxDepth,
	}

	if err := e.writeMod(); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	for name, src := range render.RuntimeFiles() {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			os.RemoveAll(dir)
			return nil, err
		}
	}
	e.newChecker()
	return e, nil
}

// writeMod emits the temp module's go.mod, and the go.sum that goes with it,
// for the currently attached host — or the standalone pair when there is none.
//
// The go.sum is the host's own, copied verbatim. The session requires the host
// through a filesystem replace, which needs no sum itself, but the host's
// requirements are resolved in the session module and do; see host.Sum.
func (e *Evaluator) writeMod() error {
	mod := ""
	var sum []byte
	if e.attached != nil {
		m, err := e.attached.Mod()
		if err != nil {
			return err
		}
		mod = m
		if sum, err = e.attached.Sum(); err != nil {
			return err
		}
	} else {
		goVer, err := e.goMinor()
		if err != nil {
			return err
		}
		// The directive must not exceed the toolchain that will build this, or
		// GOTOOLCHAIN=local turns it into a hard error instead of a download.
		mod = fmt.Sprintf("module gluon.local/session\n\ngo %s\n", goVer)
	}
	if err := os.WriteFile(filepath.Join(e.dir, "go.mod"), []byte(mod), 0o644); err != nil {
		return err
	}
	return e.writeSum(sum)
}

// writeSum keeps the go.sum in step with the go.mod just written. An empty sum
// removes the file rather than leaving the previous host's: writeMod rebuilds
// go.mod from scratch on every :use, so sums for modules the new go.mod does
// not require would be the one piece of the old module left behind.
func (e *Evaluator) writeSum(sum []byte) error {
	p := filepath.Join(e.dir, "go.sum")
	if len(sum) == 0 {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return os.WriteFile(p, sum, 0o644)
}

// newChecker rebuilds the in-process type checker. It is a fresh one every
// time because the checker caches export data for the life of a session, and
// attaching or detaching a host changes which packages resolve at all.
//
// GLUON_NO_TYPECHECK exists because the checker is the one component that can
// reject a program the compiler would have accepted. If go/types and the
// installed toolchain ever disagree, this restores v1's behaviour of letting
// the compiler be the only authority.
func (e *Evaluator) newChecker() {
	if os.Getenv("GLUON_NO_TYPECHECK") != "" {
		e.checker = nil
		return
	}
	e.checker = check.New(check.Options{Dir: e.dir, Env: e.env(), Runtime: render.RuntimeFiles()})
}

// Host reports the module the session is attached to, or nil.
func (e *Evaluator) Host() *host.Host { return e.attached }

// Index is the attached host's importable packages, or nil for a standalone
// session. The tree is walked the first time it is asked for and not before.
//
// The error is the walk's. It is returned rather than swallowed because :use
// reports the package count and so asks immediately: an attach that could not
// read the tree has to say so at the moment it is typed, and a first use that
// arrives later has to be told the same thing rather than silently offering
// nothing.
func (e *Evaluator) Index() (*host.Index, error) {
	if e.attached == nil {
		return nil, nil
	}
	e.indexOnce.Do(func() { e.index, e.indexErr = e.attached.Index() })
	return e.index, e.indexErr
}

// dropIndex forgets the index so the next ask rebuilds it. It is called
// wherever the host, or the source underneath it, changes.
func (e *Evaluator) dropIndex() {
	e.index, e.indexErr, e.indexOnce = nil, nil, sync.Once{}
}

// Imports is the import set the last render settled on. Completion reads it so
// a qualifier the session already uses resolves to the package it already
// means, rather than to whichever one happens to share the name.
func (e *Evaluator) Imports() []render.ImportSpec {
	return append([]render.ImportSpec(nil), e.imports...)
}

// CacheLen is how many results the cache is holding.
//
// It exists for the commands that must not add one. A cached result is served
// on program text alone, so a question that ran the user's own expression and
// left an answer behind would have that answer returned as current the next
// time the same text came round — the wrongness evalOpts.live describes. That
// promise is only assertable by counting.
func (e *Evaluator) CacheLen() int { return e.cache.order.Len() }

// Preload is the configured name-to-import map.
func (e *Evaluator) Preload() map[string]render.ImportSpec {
	out := make(map[string]render.ImportSpec, len(e.preload))
	for k, v := range e.preload {
		out[k] = v
	}
	return out
}

// UseHost points the temp module underneath h, so the session can import the
// host's packages including the ones under internal/. A nil h detaches.
//
// Everything derived from the old module is dropped: the checker's export data
// names packages that may no longer resolve, the import cache holds paths
// goimports may no longer offer, and the result cache is keyed on program text
// alone — the same text means something different once go.mod changes, which
// is exactly the case a stale hit would get wrong.
func (e *Evaluator) UseHost(h *host.Host) error {
	prev := e.attached
	e.attached = h
	if err := e.writeMod(); err != nil {
		e.attached = prev
		// Leave the module as it was rather than half-switched.
		_ = e.writeMod()
		return err
	}
	// The index describes the previous host, and the new one's is not built
	// until something asks. writeMod has already validated that the directory
	// is a module, which is what the attach was ever able to promise; the walk
	// only decides which packages are importable.
	e.dropIndex()
	e.invalidate()
	return nil
}

// invalidate drops everything the session derived from the module it is
// pointed at: the checker's export data, the import cache, and the results of
// programs already run.
//
// Invariant 18 names the three, and says why the last one matters most — the
// result cache is keyed on program text alone, so the same text means
// something different once the module changes, and a stale hit is a wrong
// answer that looks exactly like a right one.
//
// It is one function rather than a copy per caller. :use, :get and :reload all
// change what the same program text means, and a caller that dropped the
// checker but forgot the results would appear to work while serving answers
// from before the change — the failure that is invisible until it is expensive.
func (e *Evaluator) invalidate() {
	e.imports, e.resolved = nil, true
	e.got, e.std = nil, nil
	e.cache = newResultCache(32)
	e.newChecker()
}

// Reload re-derives what the attached host's source feeds, for the case :use
// does not cover: the host's .go files changed and its go.mod did not.
//
// The index is rebuilt too, because a package added to the host since the
// attach is one the session cannot otherwise name. Nothing is built and
// nothing is run: what this fixes is state that would have been reused, so
// dropping it is the whole of the work.
func (e *Evaluator) Reload() (*host.Index, error) {
	if e.attached == nil {
		return nil, ErrNoHost
	}
	e.dropIndex()
	// :reload reports the package count, so it asks immediately — the walk it
	// was always paying for, at the moment it was always paying for it.
	ix, err := e.Index()
	if err != nil {
		return nil, err
	}
	e.invalidate()
	return ix, nil
}

// ErrNoHost is what Reload answers with when the session is standalone. There
// is nothing derived from a host to drop, and reporting that is better than
// succeeding at nothing.
var ErrNoHost = errors.New("no host module is attached")

// seedImports returns the imports the session's qualifiers name that gluon can
// resolve without asking goimports: the attached host's packages, and the ones
// preloaded from config.
//
// Order matters here. goimports resolves a bare `trace` against everything it
// can see, which includes the whole module cache; with a typical cache that
// answers k8s.io/utils/trace, a package the temp module does not even require. An
// import the program already has is one goimports leaves alone, so settling
// these names first is what makes host attachment work at all — and what makes
// a preloaded import skip the ~135ms pass rather than merely survive it.
//
// A name the session never uses yields nothing, which is what makes preloading
// free: an unused entry is not an unused import, it is not an import.
func (e *Evaluator) seedImports(s *session.Session) ([]render.ImportSpec, error) {
	// Asked of the attachment rather than of the index, so a session with
	// nothing to seed does not build an index to discover it has nothing.
	// Requirements are asked of go.mod, which is a read rather than a go list.
	if e.attached == nil && len(e.preload) == 0 {
		if reqs, _ := e.Requires(); len(reqs) == 0 {
			return e.extraImports, nil
		}
	}
	// A name the session binds is a local, not a package — the same
	// subtraction UnboundQualifiers makes for the goimports fast path.
	bound := map[string]bool{}
	for _, en := range s.Entries {
		for _, b := range en.Binds {
			bound[b] = true
		}
	}
	// So is a name the session's own import declarations bind: `import "math"`
	// means the standard library's, whatever the host calls one of its
	// packages. Asking the index anyway seeded the host's under the same name.
	for _, im := range render.DeclaredImports(s) {
		if n := render.ImportedName(im); n != "" {
			bound[n] = true
		}
	}

	seen := map[string]bool{}
	var out []render.ImportSpec
	for _, en := range s.Entries {
		for _, q := range render.Qualifiers(en.Src) {
			if seen[q] || bound[q] || render.Synthetic(q) {
				continue
			}
			seen[q] = true
			spec, err := e.resolveSeed(q)
			if err != nil {
				return nil, &BuildError{Msg: err.Error()}
			}
			if spec.Path != "" {
				out = append(out, spec)
			}
		}
	}
	// seedImports is the one place both write branches consult, which is why
	// the extras are merged here rather than at either call site.
	return mergeImports(out, e.extraImports), nil
}

// resolveSeed maps one qualifier to an import gluon already knows.
//
// The host wins over config: attaching is an explicit statement about this
// project, while config is global, so inside a project its own `trace` is the
// one meant.
func (e *Evaluator) resolveSeed(q string) (render.ImportSpec, error) {
	ix, err := e.Index()
	if err != nil {
		return render.ImportSpec{}, err
	}
	if ix != nil {
		pkg, err := ix.Resolve(q)
		if err != nil {
			return render.ImportSpec{}, err
		}
		if pkg.Path != "" {
			spec := render.ImportSpec{Path: pkg.Path}
			// An explicit alias whenever the clause disagrees with the last
			// path element, so importsSatisfied and the reader both see the
			// name the code actually uses.
			if pkg.Name != path.Base(pkg.Path) {
				spec.Name = pkg.Name
			}
			return spec, nil
		}
	}
	// What the session required with :get comes next: it is an explicit
	// statement about this session, where config is about every session.
	if spec, ok := e.required(q); ok {
		return spec, nil
	}
	if spec, ok := e.preload[q]; ok {
		return spec, nil
	}
	return render.ImportSpec{}, nil
}

// required resolves a qualifier to a package of a module the session required
// with :get. It answers only when exactly one package of those modules has the
// name: two is a choice gluon does not make for anyone, and goimports is asked
// as before. A standard library name is never taken — `errors` stays the
// standard library's after :get github.com/pkg/errors, as it would in any Go
// file with goimports behind it.
func (e *Evaluator) required(q string) (render.ImportSpec, bool) {
	if e.got == nil {
		e.got, e.std = e.listRequired()
	}
	paths := e.got[q]
	if len(paths) != 1 || e.std[q] {
		return render.ImportSpec{}, false
	}
	spec := render.ImportSpec{Path: paths[0]}
	if q != path.Base(paths[0]) {
		spec.Name = q
	}
	return spec, true
}

// listRequired is the packages of the session's own requirements, by name,
// and the standard library's names. One go list answers both. A session with
// no requirements asks nothing, and a list that fails leaves the qualifier to
// goimports rather than failing the line.
func (e *Evaluator) listRequired() (got map[string][]string, std map[string]bool) {
	none := func() (map[string][]string, map[string]bool) { return map[string][]string{}, map[string]bool{} }
	reqs, err := e.Requires()
	if err != nil || len(reqs) == 0 {
		return none()
	}
	// An attached host's own requirements are in the session's go.mod too —
	// that is how its packages build here — but they are the project's, not
	// what the session asked for, and counting them made a name ambiguous
	// that the session's :get had settled: ent brings ariga.io/atlas, which
	// has a sql/postgres of its own.
	hosts := map[string]bool{}
	if e.attached != nil {
		if hr, herr := e.attached.Requires(); herr == nil {
			for _, r := range hr {
				mod, _, _ := strings.Cut(r, " ")
				hosts[mod] = true
			}
		}
	}
	args := []string{"list", "-e", "-f", "{{.Standard}} {{.Name}} {{.ImportPath}}", "--", "std"}
	for _, r := range reqs {
		if mod, _, _ := strings.Cut(r, " "); !hosts[mod] {
			args = append(args, mod+"/...")
		}
	}
	if len(args) == 6 {
		return none()
	}
	ctx, cancel := context.WithTimeout(context.Background(), e.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir, cmd.Env = e.dir, e.env()
	out, _ := cmd.Output() // -e: a pattern that matches nothing is not a failure
	return readPackages(string(out))
}

// readPackages reads listRequired's go list: "<standard> <name> <path>" a
// line. A main package and an internal one are not importable, so neither is
// a candidate.
func readPackages(out string) (got map[string][]string, std map[string]bool) {
	got, std = map[string][]string{}, map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 3 || f[1] == "main" || importInternal(f[2]) {
			continue
		}
		if f[0] == "true" {
			std[f[1]] = true
			continue
		}
		got[f[1]] = append(got[f[1]], f[2])
	}
	return got, std
}

// importInternal reports whether a path is under an internal directory, which
// nothing outside its module can import.
func importInternal(p string) bool {
	return strings.Contains(p, "/internal/") || strings.HasSuffix(p, "/internal") || strings.HasPrefix(p, "internal/")
}

// SetPreload records the imports config wants available by name. It is called
// again after every attach, because a host rule can add to the set.
func (e *Evaluator) SetPreload(paths []string) {
	e.preload = map[string]render.ImportSpec{}
	for _, p := range paths {
		name := render.PackageName(p)
		if name == "" {
			continue
		}
		spec := render.ImportSpec{Path: p}
		// go.yaml.in/yaml/v3 is imported as yaml, and github.com/foo/bar/v2 as
		// bar. Writing the alias explicitly means the name gluon predicted is
		// the name the program uses, whatever the package actually calls
		// itself.
		if name != path.Base(p) {
			spec.Name = name
		}
		e.preload[name] = spec
	}
}

// SetTimeout bounds a single evaluation. The whole session replays on every
// line, so this is what stops one accidental `for {}` wedging the REPL.
func (e *Evaluator) SetTimeout(d time.Duration) {
	if d > 0 {
		e.timeout = d
	}
}

// SetLimits bounds how many elements of one collection and how many nested
// levels the child describes. A non-positive value leaves that bound alone, so
// a caller with only one of the two to set passes 0 for the other.
//
// The bounds travel to the child in its environment and nowhere else. Writing
// them into the runtime source instead would make every non-default session
// render a different program: the result cache would miss on every :undo, and
// the toolchain would rebuild the runtime file each time a setting moved.
func (e *Evaluator) SetLimits(items, depth int) {
	if items > 0 {
		e.maxItems = items
	}
	if depth > 0 {
		e.maxDepth = depth
	}
}

// Limits reports the bounds in force, so a caller that wants one evaluation at
// a different bound can restore what it found.
func (e *Evaluator) Limits() (items, depth int) {
	return orDefault(e.maxItems, gluonrt.DefaultMaxItems), orDefault(e.maxDepth, gluonrt.DefaultMaxDepth)
}

// orDefault reads an unset bound as its default. The zero value has to mean
// "the default" and not "describe nothing": an Evaluator built as a literal
// rather than through New is a shape the tests use throughout, and it must
// describe values the way an ordinary session does.
func orDefault(n, def int) int {
	if n < 1 {
		return def
	}
	return n
}

// limitEnv is what the child needs in its environment to honour the current
// bounds — nothing at all when they are the defaults.
//
// Unset rather than set-to-the-default is what makes "identical at the
// defaults" a fact about the environment rather than a claim about parsing.
func (e *Evaluator) limitEnv() []string {
	items, depth := e.Limits()
	var env []string
	if items != gluonrt.DefaultMaxItems {
		env = append(env, fmt.Sprintf("%s=%d", gluonrt.EnvMaxItems, items))
	}
	if depth != gluonrt.DefaultMaxDepth {
		env = append(env, fmt.Sprintf("%s=%d", gluonrt.EnvMaxDepth, depth))
	}
	return env
}

// cacheKey is the program text, plus the bounds when they are not the
// defaults. The same text under two limits is two different questions, and the
// answer to one must never be served for the other.
//
// The suffix is add-only: a default session keys on the bare text it always
// did, so an entry recorded before this existed is still the entry it finds.
func (e *Evaluator) cacheKey(src string) string {
	env := e.limitEnv()
	if len(env) == 0 {
		return src
	}
	return src + "\x00" + strings.Join(env, ";")
}

func (e *Evaluator) Dir() string  { return e.dir }
func (e *Evaluator) Close() error { return os.RemoveAll(e.dir) }

// env is the hermetic environment every build runs in.
// GOPROXY=off keeps a REPL session from reaching the network; GOWORK=off stops
// an ambient workspace from dragging unrelated modules into the build.
func (e *Evaluator) env() []string {
	return append(os.Environ(),
		"GOFLAGS=",
		"GOWORK=off",
		"GOPROXY=off",
		"GOTOOLCHAIN=local",
	)
}

// Lang is the go directive in force for this session, "1.24".
//
// It is the host's when one is attached and the toolchain's otherwise, which is
// exactly what writeMod decides and for the same reason: a session attached to
// a module carries that module's directive, and the directive is what the
// compiler gates language features on. So a 1.27 toolchain still rejects
// range-over-func in a session attached to a `go 1.21` host, and anything that
// wants to say whether a feature is usable here has to ask this rather than
// asking the version.
//
// It returns "" when the toolchain cannot be reached, for which the caller has
// a better answer than a fabricated version.
func (e *Evaluator) Lang() string {
	if e.attached != nil {
		return e.attached.Go
	}
	v, err := e.goMinor()
	if err != nil {
		return ""
	}
	return v
}

// Toolchain is the installed toolchain's minor version, "1.27", ignoring any
// host directive. Lang is what the compiler gates syntax on; this is what
// decides whether the compiler can do the thing at all — a relaxation that no
// go line gates still needs a new enough toolchain, and the two questions have
// different answers in a session attached to an older module.
func (e *Evaluator) Toolchain() string {
	v, err := e.goMinor()
	if err != nil {
		return ""
	}
	return v
}

func (e *Evaluator) goMinor() (string, error) {
	out, err := exec.Command("go", "env", "GOVERSION").Output()
	if err != nil {
		return "", fmt.Errorf("go toolchain not found on PATH: %w", err)
	}
	v := strings.TrimSpace(string(out)) // e.g. "go1.27.0"
	m := regexp.MustCompile(`go(\d+)\.(\d+)`).FindStringSubmatch(v)
	if m == nil {
		return "", fmt.Errorf("could not parse go version %q", v)
	}
	return m[1] + "." + m[2], nil
}

// Render returns the program the session currently describes without building
// or running it. :src and :save need only the text, and a build costs ~400ms.
func (e *Evaluator) Render(s *session.Session) (string, error) {
	src, _, err := e.write(s, len(s.Entries)-1)
	return src, err
}

// Analyze renders and type-checks the session without building it, optionally
// with one extra expression appended, and returns the checked package for the
// inspector to ask questions of.
//
// Appending the expression rather than checking it in isolation is what makes
// imports work: goimports resolves the rendered program, so `:t strings.Builder`
// sees strings even when the session itself never imported it. The entry is
// popped again before returning, so inspecting never changes the session.
func (e *Evaluator) Analyze(s *session.Session, extra string) (*check.Result, error) {
	if e.checker == nil {
		return nil, ErrNoChecker
	}
	if extra != "" {
		entry, err := session.Classify(extra)
		if err != nil {
			return nil, err
		}
		s.Append(entry)
		defer s.Pop()
		// Same reason EvalTransient does it: :t strings.Builder on a session
		// that never imported strings would otherwise leave it in the cached
		// import block, and the next ordinary line would write it verbatim and
		// fail to build with "imported and not used".
		imports, resolved := e.imports, e.resolved
		defer func() { e.imports, e.resolved = imports, resolved }()
	}
	src, _, err := e.write(s, len(s.Entries)-1)
	if err != nil {
		return nil, err
	}
	res, err := e.checker.Check([]byte(src))
	if err != nil {
		return nil, err
	}
	return res, nil
}

// BlockDiag is one diagnostic against a block of constructs that was checked
// and never built.
//
// Structured rather than the compiler-shaped text diagText and explain produce,
// because this is the one caller that has a coordinate system of its own. The
// block came from somewhere — a buffer, a file — and only that caller knows
// which of its lines a construct started on. Handing it text it would have to
// parse back would make the buffer's line numbering depend on the format of a
// diagnostic. The build path is untouched: it still has nothing but stderr to
// read, and explain is still the single rewriter for it.
type BlockDiag struct {
	// Construct indexes the srcs given to CheckBlock, or -1 for a diagnostic
	// against the session the block was checked against rather than the block.
	Construct int
	// Line and Col are 1-based within that construct's own source, and are 0
	// when the position could not be mapped back to one — a link-time failure
	// has no file:line at all (invariant 2).
	Line, Col int
	Msg       string
	// Quote is the offending line with a caret beneath the column, empty when
	// there is no position to quote.
	Quote string
}

// CheckBlock type-checks srcs against the session without building, running, or
// leaving anything behind.
//
// It is Analyze's sibling for more than one construct. Analyze takes a single
// extra because it goes through session.Classify, which answers for one; a
// block needs all of them classified before any is appended, so a construct
// that will not parse leaves the session exactly as it found it — the rule
// appendBatch already follows.
//
// A nil error means the check ran. It does not mean the block is valid: read
// the diagnostics for that. Any error at all means the checker could not
// answer, and the caller MUST NOT report the block as either good or bad —
// invariant 5, the checker is never an authority over what the compiler would
// accept.
func (e *Evaluator) CheckBlock(s *session.Session, srcs []string) ([]BlockDiag, error) {
	// Before the checker is consulted: an empty block has nothing for it to be
	// an authority about, so "clean" here needs no authority to say.
	if len(srcs) == 0 {
		return nil, nil
	}
	if e.checker == nil {
		return nil, ErrNoChecker
	}

	entries := make([]session.Entry, 0, len(srcs))
	for _, src := range srcs {
		entry, err := session.Classify(src)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}

	first := len(s.Entries)
	for _, entry := range entries {
		s.Append(entry)
	}
	defer func() { s.Entries = s.Entries[:first] }()

	// Same reason Analyze and EvalTransient do it: the block names packages the
	// session does not have, and leaving them in the cached import block would
	// make the next ordinary line fail to build with "imported and not used"
	// (invariant 14).
	imports, resolved := e.imports, e.resolved
	defer func() { e.imports, e.resolved = imports, resolved }()

	// Twice at most. The first pass may be rendered from a cached import set
	// that does not cover the block yet, which reports as "undefined: strings"
	// — a message that means goimports has not run, not that the user is
	// wrong. importFixable is what tells those two apart, and guessing wrong
	// costs ~135ms to learn nothing.
	for range 2 {
		src, cached, err := e.write(s, len(s.Entries)-1)
		if err != nil {
			return nil, err
		}
		res, err := e.checker.Check([]byte(src))
		if err != nil {
			return nil, err
		}
		if len(res.Errs) == 0 {
			return nil, nil
		}
		if cached && importFixable(s, res.Errs) {
			if _, rerr := e.writeResolved(s, filepath.Join(e.dir, "main.go"), len(s.Entries)-1); rerr != nil {
				return nil, rerr
			}
			continue
		}
		// An import the checker could not read is its own limitation, not the
		// user's mistake: a host package that does not compile has no export
		// data, and every name it supplies then reports as undefined. Saying
		// the block is wrong on those grounds is exactly the authority
		// invariant 5 refuses.
		if importUnreadable(res.Errs) {
			return nil, ErrNoChecker
		}
		return blockDiags(s, res, first), nil
	}
	return nil, ErrNoChecker
}

// blockDiags maps the checker's positions back onto the constructs they belong
// to. The rendered program carries a //line directive per entry, so a position
// already names an entry rather than a line in a file the user has never
// opened — render.EntryOf is what reads it back.
func blockDiags(s *session.Session, res *check.Result, first int) []BlockDiag {
	out := make([]BlockDiag, 0, len(res.Errs))
	seen := map[string]bool{}
	for _, te := range res.Errs {
		pos := res.Fset.Position(te.Pos)
		d := BlockDiag{Construct: -1, Msg: te.Msg}
		if i, ok := render.EntryOf(filepath.Base(pos.Filename)); ok && i >= 0 && i < len(s.Entries) {
			line, col := entryPos(s.Entries[i].Kind, pos.Line, pos.Column)
			d.Line, d.Col = line, col
			d.Quote = quoteEntry(s, i, pos.Line, pos.Column)
			if i >= first {
				d.Construct = i - first
			}
		}
		// The same mistake reported against several entries is one mistake.
		key := fmt.Sprintf("%d:%d:%d:%s", d.Construct, d.Line, d.Col, d.Msg)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, d)
	}
	return out
}

// Lookup exposes the checker's package cache to the inspector.
func (e *Evaluator) Lookup(path string) (*types.Package, error) {
	if e.checker == nil {
		return nil, ErrNoChecker
	}
	return e.checker.Lookup(path)
}

// Loaded is the checker's cache without the loading. It answers only for
// packages some earlier check already read export data for, so an inspector
// may widen its search over them for free and never turns a static command
// into a build.
func (e *Evaluator) Loaded(path string) *types.Package {
	if e.checker == nil {
		return nil
	}
	return e.checker.Loaded(path)
}

// Warm starts a background load of a package's types, so the first completion
// that names it does not pay for the load on the keystroke path.
//
// The checker is captured here rather than read from the goroutine: invalidate
// replaces it at every :get and :use, and a warm-up still running then should
// finish into the checker it started on — where its work is simply discarded —
// rather than race the field. It returns immediately and reports nothing; a
// head start that did not arrive costs the caller what it costs today.
func (e *Evaluator) Warm(path string) {
	ck := e.checker
	if ck == nil || path == "" {
		return
	}
	go ck.Warm(path)
}

// EvalTransient evaluates one entry against the session without keeping it, so
// a command can ask the running program a question the session does not bind.
//
// The entry is popped, but Eval also mutates evaluator state, and a transient
// that pulls in an import the session does not have (:bench needs testing and
// runtime, :err needs errors) would leave it in the cached import block. The
// next ordinary line writes that block verbatim, fails to build with "imported
// and not used", and only recovers by paying a full goimports pass. So the
// import cache is snapshotted and restored, and so is healthy: whether the
// session exits cleanly is a property of the session, not of a question asked
// about it.
func (e *Evaluator) EvalTransient(s *session.Session, entry session.Entry) (Result, error) {
	imports, resolved, healthy := e.imports, e.resolved, e.healthy
	defer func() { e.imports, e.resolved, e.healthy = imports, resolved, healthy }()

	s.Append(entry)
	defer s.Pop()
	return e.Eval(s)
}

// Eval renders, builds, and runs the session. On a build failure the caller is
// expected to roll the offending entry back off the session.
func (e *Evaluator) Eval(s *session.Session) (Result, error) {
	return e.evalWith(s, evalOpts{})
}

// EvalFrom evaluates a session whose entries from firstNew onward were just
// appended together — a pasted batch. One build serves all of them and every
// one of them prints; Eval is the newest-entry-only special case. On failure
// the caller is expected to roll all of the batch's entries back.
func (e *Evaluator) EvalFrom(s *session.Session, firstNew int) (Result, error) {
	return e.evalWith(s, evalOpts{batchFrom: &firstNew})
}

// evalOpts are the ways one evaluation may differ from an ordinary line. Every
// field exists for a failure that has a name; the matching Evaluator fields say
// what each one is protecting against.
type evalOpts struct {
	// live neither reads nor writes the result cache.
	//
	// The cache is keyed on program text alone, so a question whose answer
	// lives in a database would otherwise be answered from whenever it was
	// first asked. A REPL reporting a stale row count as current is the quiet
	// wrongness this project rejected an interpreter to avoid — resultCache's
	// own doc comment is candid that a hit skips execution, which is the right
	// trade for :undo and a lie for a query.
	//
	// Not writing matters as much as not reading: an entry put here would be
	// served to a later ordinary Eval of the same text.
	live    bool
	imports []render.ImportSpec
	env     []string
	redact  []string
	// batchFrom, when set, unmutes from this entry index onward instead of
	// only the newest, so a pasted batch prints every one of its lines. nil is
	// the ordinary rule. A pointer rather than an int because 0 is a real
	// index — a batch pasted into an empty session — and a forgotten zero
	// value must mean "newest", never "replay everything".
	batchFrom *int
}

// putUnlessLive records a result unless this evaluation was live.
func (e *Evaluator) putUnlessLive(opts evalOpts, src string, res Result) {
	if opts.live {
		return
	}
	e.cache.put(e.cacheKey(src), res, nil)
}

func (e *Evaluator) evalWith(s *session.Session, opts evalOpts) (Result, error) {
	if len(opts.imports) > 0 || len(opts.env) > 0 || len(opts.redact) > 0 {
		imports, env, redact := e.extraImports, e.extraEnv, e.redact
		e.extraImports, e.extraEnv, e.redact = opts.imports, opts.env, opts.redact
		defer func() { e.extraImports, e.extraEnv, e.redact = imports, env, redact }()
	}

	e.predictNoValue(s)

	// from is the first entry whose output the user has not seen. Everything
	// below renders with it, so the retry paths re-emit the same program.
	from := len(s.Entries) - 1
	if opts.batchFrom != nil {
		from = *opts.batchFrom
	}

	src, cached, err := e.write(s, from)
	if err != nil {
		return Result{}, err
	}

	if !opts.live {
		if res, cerr, ok := e.cache.get(e.cacheKey(src)); ok {
			trace("cache-hit", time.Now())
			return res, cerr
		}
	}

	// Type-check first. Most lines are decided here, in microseconds rather
	// than the ~200ms a build costs.
	switch v := e.analyze(s, &src, &cached, from); v.kind {
	case verdictConst:
		// The answer was already known at compile time; nothing has to run.
		res := Result{Output: v.output, Source: src}
		e.putUnlessLive(opts, src, res)
		return res, nil

	case verdictValid:
		// A declaration binds a name and runs nothing, and the check has
		// already proved it compiles, so neither the build nor the exec buys
		// anything. Anything else still needs a binary to run.
		if !render.Executes(s) {
			res := Result{Source: src}
			e.putUnlessLive(opts, src, res)
			return res, nil
		}

	case verdictErrors:
		return Result{Source: src}, &BuildError{Msg: e.explain(s, v.diag, src), Source: src}

	case verdictUnavailable:
		// The checker could not run; the build below is the only authority.
	}

	buildOut, err := e.build()
	if err != nil && cached && importProblem(buildOut) {
		// The cached import set no longer matches the session; resolve fully.
		if src, err = e.writeResolved(s, filepath.Join(e.dir, "main.go"), from); err == nil {
			buildOut, err = e.build()
		}
	}
	if err != nil {
		// A call with no return values cannot be an argument to the printer.
		// Rather than type-check up front, let the compiler tell us once and
		// re-render that entry as a bare statement.
		if e.retryNoValue(s, buildOut) {
			if src, _, err = e.write(s, from); err != nil {
				return Result{}, err
			}
			if buildOut, err = e.build(); err != nil {
				return Result{Source: src}, &BuildError{Msg: e.explain(s, buildOut, src), Source: src}
			}
		} else {
			return Result{Source: src}, &BuildError{Msg: e.explain(s, buildOut, src), Source: src}
		}
	}

	// A declaration produces no output, so the exec (~135ms, most of it macOS
	// validating a freshly linked binary) buys nothing. The build above has
	// already proved it compiles.
	if !render.Executes(s) {
		res := Result{Source: src}
		e.putUnlessLive(opts, src, res)
		return res, nil
	}

	out, code, err := e.run()
	if err != nil {
		e.healthy = false
		// The partial output goes back with the error rather than being
		// dropped. On a deadline it is the only thing that says how far the
		// run got — which is what lets :check name the case that was still
		// running — and every caller that only wants the error is unaffected,
		// because they already ignore the Result when err is non-nil.
		return Result{Output: out, Source: src}, err
	}
	if code != 0 {
		// A panic traceback names the synthetic files, which mean nothing on
		// their own; annotate each frame with the line it stands for.
		out = mapTraceback(s, out, e.dir)
	}
	// A session whose program exits non-zero has a panic or an os.Exit in it,
	// and that outcome must survive the next line rather than being skipped
	// over by a constant answered without running.
	e.healthy = code == 0
	// No diffing: the generated program mutes everything before the newest
	// entry, so whatever arrives here is exactly this line's output.
	res := Result{Output: out, Source: src, ExitCode: code}
	e.putUnlessLive(opts, src, res)
	return res, nil
}

// write emits main.go, reusing the cached import set when there is one. It
// reports whether it took that fast path, so the caller knows a build failure
// might just mean the import set moved. from is the unmute boundary — see
// render.MainFrom; every caller outside a batch passes len(s.Entries)-1.
func (e *Evaluator) write(s *session.Session, from int) (src string, cached bool, err error) {
	return e.writeAs(s, render.PrintSink, from)
}

func (e *Evaluator) writeAs(s *session.Session, sink render.Sink, from int) (src string, cached bool, err error) {
	path := filepath.Join(e.dir, "main.go")

	if e.resolved && e.importsSatisfied(s) {
		// The seeds have to be merged in, not just relied on for the
		// satisfied check: on the first line of a session the cached set is
		// still empty, and that is exactly the line a preloaded import exists
		// to answer without goimports.
		seed, serr := e.seedImports(s)
		if serr != nil {
			return "", false, serr
		}
		// The extra imports go in too, for the same reason the seeds do and
		// one more. A live evaluation names packages the session does not
		// have, so without them the fast path renders a program that cannot
		// compile; and the goimports pass that would otherwise resolve them
		// rewrites the import block, which moves the //line directives off the
		// declarations they belong to and collapses every position in the
		// program onto the last entry. :profile reports positions, so that
		// showed up there first — but it was already costing :query and
		// :bench -profile a ~135ms pass and a wrong position in every
		// traceback.
		imps := mergeImports(mergeImports(e.imports, seed), e.extraImports)
		raw, rerr := render.MainFrom(s, imps, sink, from)
		if rerr == nil {
			t0 := time.Now()
			formatted, ferr := render.Format([]byte(raw))
			trace("gofmt", t0)
			if ferr == nil {
				if werr := os.WriteFile(path, formatted, 0o644); werr != nil {
					return "", false, werr
				}
				return string(formatted), true, nil
			}
		}
	}

	src, err = e.writeResolvedAs(s, path, sink, from)
	return src, false, err
}

// seedNoValue lists functions that return nothing, so a session does not have
// to pay a doomed build to discover what is already known. Builtins first,
// then the stdlib calls that come up constantly when practising.
func seedNoValue() map[string]bool {
	m := map[string]bool{}
	for _, name := range []string{
		"close", "delete", "panic", "clear", "print", "println",
		"slices.Sort", "slices.SortFunc", "slices.SortStableFunc", "slices.Reverse",
		"sort.Slice", "sort.SliceStable", "sort.Sort", "sort.Stable",
		"sort.Ints", "sort.Strings", "sort.Float64s",
		"runtime.GC", "runtime.Gosched",
	} {
		m[name] = true
	}
	return m
}

// predictNoValue marks a trailing call whose callee is already known to return
// nothing, skipping the build that would only teach us that again.
func (e *Evaluator) predictNoValue(s *session.Session) {
	n := len(s.Entries)
	if n == 0 {
		return
	}
	last := &s.Entries[n-1]
	if last.Kind != session.KindExpr || last.NoValue {
		return
	}
	if callee, ok := render.Callee(last.Src); ok && e.noValueFuncs[callee] {
		last.NoValue = true
	}
}

// importsSatisfied reports whether the newest entry only uses qualifiers that
// are already imported or are local bindings. When it does not, taking the
// cached-import fast path guarantees a failed build, so goimports runs first.
func (e *Evaluator) importsSatisfied(s *session.Session) bool {
	if len(s.Entries) == 0 {
		return true
	}
	quals := render.UnboundQualifiers(s.Entries[len(s.Entries)-1].Src)
	if len(quals) == 0 {
		return true
	}

	known := make(map[string]bool, len(e.imports)+len(e.extraImports)+len(e.preload))
	for _, im := range e.imports {
		known[importName(im)] = true
	}
	// An import the caller supplied for this evaluation alone is as known as
	// one already resolved: writeAs renders with both merged. Being wrong
	// here costs a failed build and the importProblem retry below it, which
	// resolves fully — the same recovery a stale cached set already has.
	for _, im := range e.extraImports {
		known[importName(im)] = true
	}
	// A name gluon can resolve itself is as good as one already imported —
	// better, since resolving it costs nothing.
	for name := range e.preload {
		known[name] = true
	}
	// A local variable used as x.Field looks the same syntactically.
	for _, en := range s.Entries {
		for _, b := range en.Binds {
			known[b] = true
		}
	}
	// And a name the session imports itself is already imported.
	for _, im := range render.DeclaredImports(s) {
		if n := render.ImportedName(im); n != "" {
			known[n] = true
		}
	}
	var unknown []string
	for _, q := range quals {
		// it and _N are gluon's own; goimports can never resolve them, so
		// treating one as a package would buy a ~135ms pass to learn nothing.
		if render.Synthetic(q) {
			continue
		}
		if !known[q] {
			unknown = append(unknown, q)
		}
	}
	if len(unknown) == 0 {
		return true
	}
	// The host's packages are the last thing consulted rather than the first,
	// because consulting them is what builds the index. A name already
	// resolved, preloaded or bound is answered without walking the host's
	// tree at all.
	ix, err := e.Index()
	if err != nil || ix == nil {
		return false
	}
	hostNames := make(map[string]bool, len(ix.Names()))
	for _, name := range ix.Names() {
		hostNames[name] = true
	}
	for _, q := range unknown {
		if !hostNames[q] {
			return false
		}
	}
	return true
}

// importName is the identifier an import is written under.
func importName(im render.ImportSpec) string {
	if im.Name != "" {
		return im.Name
	}
	return path.Base(im.Path)
}

// mergeImports unions two import sets, preferring the first on a collision.
//
// Deduping by path is not enough: two different paths under the same name are
// a redeclaration the compiler rejects, and the cached set is the one that has
// already been proved to build.
func mergeImports(base, extra []render.ImportSpec) []render.ImportSpec {
	if len(extra) == 0 {
		return base
	}
	paths := make(map[string]bool, len(base))
	names := make(map[string]bool, len(base))
	for _, im := range base {
		paths[im.Path] = true
		names[importName(im)] = true
	}
	out := append([]render.ImportSpec(nil), base...)
	for _, im := range extra {
		// A blank import binds no identifier, so two of them cannot collide.
		// Deduping them by "name" would silently drop the second driver.
		if paths[im.Path] || (im.Name != "_" && names[importName(im)]) {
			continue
		}
		paths[im.Path] = true
		names[importName(im)] = true
		out = append(out, im)
	}
	return out
}

// writeResolved runs the full goimports pass and remembers what it decided.
func (e *Evaluator) writeResolved(s *session.Session, path string, from int) (string, error) {
	return e.writeResolvedAs(s, path, render.PrintSink, from)
}

func (e *Evaluator) writeResolvedAs(s *session.Session, file string, sink render.Sink, from int) (string, error) {
	seed, err := e.seedImports(s)
	if err != nil {
		return "", err
	}
	raw, err := render.MainFrom(s, seed, sink, from)
	if err != nil {
		return "", err
	}
	t0 := time.Now()
	fixed, err := render.FixImports(file, []byte(raw))
	trace("goimports", t0)
	if err != nil {
		// goimports parses too, so a syntax error surfaces here first.
		return raw, &BuildError{Msg: trimPath(err.Error(), e.dir), Source: raw}
	}
	if imps, ierr := render.ExtractImports(fixed); ierr == nil {
		e.imports = imps
		e.resolved = true
	}
	if err := os.WriteFile(file, fixed, 0o644); err != nil {
		return "", err
	}
	return string(fixed), nil
}

func (e *Evaluator) build() (string, error) {
	defer trace("build", time.Now())
	// -o into the temp dir is mandatory. A bare `go build` once dropped a
	// tracked 3.1 MB Mach-O binary into a repository's history.
	// -gcflags=-e removes the compiler's ten-error cap, which a pasted block
	// can exceed. It applies only to the package named on the command line, so
	// the stdlib's cached objects are untouched. The type checker normally
	// reports errors first and has no such cap; this is for when it cannot run.
	return e.buildWith("-e", filepath.Join(e.dir, "prog"))
}

// buildWith is build with a chosen gcflags value and output path.
//
// gcflags must be one argv element: repeated -gcflags replaces rather than
// appends, so passing them separately would silently drop all but the last.
// It also carries no pattern, which confines it to the package named on the
// command line — `all=` would recompile the stdlib under different flags and
// destroy the warm cache the whole design depends on.
func (e *Evaluator) buildWith(gcflags, out string) (string, error) {
	return e.buildArgs(nil, gcflags, out)
}

// errBuildTimeout marks a build the deadline killed rather than one the
// compiler rejected, so a caller that knows why its build is slow can say so.
// Only :race does: its first instrumented build compiles the whole race
// runtime, and the ordinary timeout message names :undo, which is no help.
var errBuildTimeout = errors.New("the build timed out")

// buildArgs is buildWith with extra go build flags in front of -gcflags.
//
// -race is the only caller. It is a property of the whole build — a different
// runtime and a different set of compiled objects — not compiler flags for one
// package, so it cannot ride in gcflags.
func (e *Evaluator) buildArgs(extra []string, gcflags, out string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), e.timeout)
	defer cancel()
	args := append([]string{"build"}, extra...)
	args = append(args, "-gcflags="+gcflags, "-o", out, ".")
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = e.dir
	cmd.Env = e.env()
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return buf.String(), errBuildTimeout
	}
	return buf.String(), err
}

// vet runs the toolchain's analysers over the session directory.
//
// It is e.env() and not the child environment: vet compiles and type-checks,
// it does not run the program, so a resolved DSN has no business reaching it.
// Output is combined because vet writes diagnostics to stderr and its -json
// form to stdout, and the caller parses rather than switching on the stream.
func (e *Evaluator) vet() (string, error) {
	defer trace("vet", time.Now())
	ctx, cancel := context.WithTimeout(context.Background(), e.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "vet", ".")
	cmd.Dir = e.dir
	cmd.Env = e.env()
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return buf.String(), fmt.Errorf("go vet timed out after %s", e.timeout)
	}
	return buf.String(), err
}

func (e *Evaluator) run() (string, int, error) {
	return e.runBinary("prog", "timed out after "+e.timeout.String()+
		" — the whole session replays each line, so a blocking statement wedges it; use :undo")
}

// runBinary runs one of the session directory's binaries and reports what it
// printed and how it exited.
//
// onTimeout is the caller's own message because the deadline means different
// things to different callers: an ordinary line that wedged is :undo's problem,
// and a :race that ran long is the instrumented build's.
func (e *Evaluator) runBinary(name, onTimeout string) (string, int, error) {
	defer trace("run", time.Now())
	ctx, cancel := context.WithTimeout(context.Background(), e.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(e.dir, name))
	cmd.Dir = e.dir
	// The extra environment goes to the child and nowhere else. build, the
	// checker and `go doc` all call e.env() directly and never see it, which is
	// what keeps a resolved DSN out of a build error.
	cmd.Env = append(append(e.env(), e.extraEnv...), e.limitEnv()...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()

	out := ownLine(mask(buf.String(), e.redact))
	if ctx.Err() == context.DeadlineExceeded {
		return out, -1, &TimeoutError{Msg: onTimeout, After: e.timeout}
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out, ee.ExitCode(), nil
	}
	if err != nil {
		return out, -1, err
	}
	return out, 0, nil
}

// drainNote is how the runtime's note about waiting for goroutines begins —
// __gluonDrain's own words, which TestTheWaitNoteIsTheRuntimes holds to.
const drainNote = "[gluon waited "

// ownLine puts the runtime's wait note on a line of its own. The runtime
// prints it after whatever the program printed, and cannot know how that
// ended: a program whose output has no final newline would take the note into
// its last line, and a value, a table or a plugin's report would be read with
// gluon's aside glued to its end — :grpc's listing named a method's response
// type "Order[gluon waited 139µs …]". The note is gluon's, and it is always a
// line of its own.
func ownLine(out string) string {
	if !strings.Contains(out, drainNote) {
		return out
	}
	var b strings.Builder
	for {
		i := strings.Index(out, drainNote)
		if i < 0 {
			b.WriteString(out)
			return b.String()
		}
		b.WriteString(out[:i])
		if i > 0 && out[i-1] != '\n' {
			b.WriteByte('\n')
		}
		b.WriteString(drainNote)
		out = out[i+len(drainNote):]
	}
}

// noValueRe captures the expression the compiler rejected. The message names
// it directly ("slices.Sort(x) (no value) used as value"), which is more
// reliable than mapping a line number through goimports' rewriting.
var noValueRe = regexp.MustCompile(`(?m)[\w./-]+\.go:\d+:\d+:\s*(.+?) \(no value\) used as value`)

// retryNoValue reads the expressions the compiler rejected out of its output.
// It is the fallback for when the checker is unavailable; normally markVoid
// learns the same thing without paying for a build.
func (e *Evaluator) retryNoValue(s *session.Session, buildOut string) bool {
	matches := noValueRe.FindAllStringSubmatch(buildOut, -1)
	if len(matches) == 0 {
		return false
	}
	exprs := make([]string, 0, len(matches))
	for _, m := range matches {
		exprs = append(exprs, m[1])
	}
	return e.markNoValue(s, exprs)
}

// markNoValue marks the entries matching these expression texts as having no
// result, so they are re-rendered as bare statements.
//
// Matching is on whitespace-squashed text because the rendered program is
// gofmt'd while the entry still holds what the user typed. It must consider
// every entry rather than only the newest: `slices.Sort(x)` typed mid-session
// is replayed on every later line, so the entry needing re-rendering is often
// not the one just submitted.
func (e *Evaluator) markNoValue(s *session.Session, exprs []string) bool {
	marked := false
	for _, raw := range exprs {
		want := squash(raw)
		if want == "" {
			continue
		}
		for i := range s.Entries {
			en := &s.Entries[i]
			if en.Kind == session.KindExpr && !en.NoValue && squash(en.Src) == want {
				en.NoValue = true
				marked = true
				// Remember the callee so no later session line repeats this.
				if callee, ok := render.Callee(en.Src); ok {
					e.noValueFuncs[callee] = true
				}
				break
			}
		}
	}
	return marked
}

// squash strips whitespace so the compiler's rendering of an expression can be
// compared to the text the user typed.
func squash(s string) string {
	return strings.Join(strings.Fields(s), "")
}

// importProblem reports whether a build failure looks like the import set
// drifting rather than a genuine mistake in the user's code.
var importProblemRe = regexp.MustCompile(`undefined: |imported and not used|cannot find package|is not in std|no required module provides`)

func importProblem(buildOut string) bool { return importProblemRe.MatchString(buildOut) }

// trace reports per-phase timings when GLUON_TIMING is set. Latency is the
// one thing that decides whether a compiler-backed REPL is usable, so it is
// worth being able to see where a slow evaluation went.
func trace(phase string, start time.Time) { traceDur(phase, time.Since(start)) }

// traceDur reports a duration measured by the caller, for phases that are not
// one contiguous span — the type check runs in rounds with re-rendering in
// between, and charging it for that would misattribute goimports' cost.
//
// Phases are collected as well as printed, so :time can show them in the TUI.
// Writing to stderr there would interleave with Bubble Tea's own rendering.
func traceDur(phase string, d time.Duration) {
	collect(phase, d)
	if os.Getenv("GLUON_TIMING") == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "  [%s %v]\n", phase, d.Round(time.Millisecond))
}

// Phase is one measured span of an evaluation.
type Phase struct {
	Name string
	D    time.Duration
}

// Collecting phases is package-level rather than per-evaluator because trace
// is called from free functions all through the package. One evaluation runs
// at a time per REPL, and the mutex keeps concurrent tests honest.
var (
	phaseMu   sync.Mutex
	phaseOn   bool
	phaseList []Phase
)

func collect(name string, d time.Duration) {
	phaseMu.Lock()
	defer phaseMu.Unlock()
	if phaseOn {
		phaseList = append(phaseList, Phase{name, d})
	}
}

// Timing turns phase collection on or off. It is off by default: the
// collection itself is trivial, but a REPL that reports timings nobody asked
// for is noise.
func Timing(on bool) {
	phaseMu.Lock()
	defer phaseMu.Unlock()
	phaseOn, phaseList = on, nil
}

// TakePhases returns the phases measured since the last call and clears them.
func TakePhases() []Phase {
	phaseMu.Lock()
	defer phaseMu.Unlock()
	out := phaseList
	phaseList = nil
	return out
}

func trimPath(s, dir string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, dir+"/", ""), dir, "")
}

// Get adds a module requirement to the temp module.
//
// This is the one thing gluon does that reaches the network, and it is a
// command rather than a fallback for exactly that reason: an automatic `go get`
// on an unresolved qualifier would mutate go.mod, hit the network and take
// seconds, all without being asked. Typing :get is the asking.
//
// The hermetic environment is relaxed here and only here — GOPROXY comes back
// from `go env` — so a session that never types :get still cannot reach out.
func (e *Evaluator) Get(mod string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), networkTimeout)
	defer cancel()

	// A module fetch is not a build: the proxy has to be the user's real one,
	// and GONOSUMDB/GOSUMDB stay as configured so a private module resolves
	// the way it does everywhere else.
	env := append(e.env(), "GOPROXY="+goEnv("GOPROXY"), "GOFLAGS=-mod=mod")
	cmd := exec.CommandContext(ctx, "go", "get", "--", mod)
	cmd.Dir = e.dir
	cmd.Env = env
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	out := strings.TrimSpace(trimPath(buf.String(), e.dir))

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("go get %s timed out after %s", mod, networkTimeout)
		}
		if out == "" {
			return "", fmt.Errorf("go get %s: %w", mod, err)
		}
		return "", fmt.Errorf("%s", out)
	}

	// The build list has changed, so everything derived from it is stale: the
	// same text may now compile.
	e.invalidate()
	return out, nil
}

// ErrRestoreConflict is what Restore answers when the recorded go.sum
// contradicts the one already beside the session's go.mod: the same module at
// the same version, hashed two different ways.
//
// Refused rather than merged, and refused rather than resolved. Two hashes for
// one version is either a tampered cache or a record written against a
// different module of the same name, and both are things a person has to look
// at. Picking one would be gluon deciding which of two checksums to trust,
// which is the one job the toolchain's sum database exists to do.
var ErrRestoreConflict = errors.New("the recorded checksums conflict with the session's")

// Restore puts a scratchpad's module requirements back, using only what is
// already on this machine.
//
// It is a separate method from Get and not a flag on it, because Get is the one
// method that relaxes GOPROXY: it appends the user's real proxy and GOFLAGS=-mod=mod
// so a fetch can happen. Nothing here does that. The requirements are written
// into go.mod directly and the recorded sums are unioned with whatever writeSum
// put there for an attached host, so the next build runs under e.env() like
// every other build — GOPROXY=off — and a module the local cache no longer
// holds fails with the toolchain's own words rather than being fetched behind
// the user's back.
//
// That is what makes ":get is the only command that goes online" mechanical
// rather than a promise: opening a scratchpad has no code path that could
// reach the network, so it cannot grow one by accident.
//
// invalidate is called from inside, not by the caller: the build list has
// changed, so the checker's export data, the import cache and the result cache
// all describe a module that no longer exists — invariant 18, and a caller that
// forgot it would serve answers from before the restore.
func (e *Evaluator) Restore(reqs []string, sum []byte) error {
	if len(reqs) == 0 && len(sum) == 0 {
		return nil
	}

	path := filepath.Join(e.dir, "go.mod")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return err
	}
	for _, r := range reqs {
		mod, ver, ok := strings.Cut(strings.TrimSpace(r), " ")
		if !ok || mod == "" || ver == "" {
			return fmt.Errorf("recorded requirement %q is not a module and a version", r)
		}
		if err := f.AddRequire(mod, ver); err != nil {
			return err
		}
	}
	f.Cleanup()
	out, err := f.Format()
	if err != nil {
		return err
	}

	existing, err := os.ReadFile(filepath.Join(e.dir, "go.sum"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	merged, err := mergeSums(existing, sum)
	if err != nil {
		return err
	}

	if err := os.WriteFile(path, out, 0o644); err != nil {
		return err
	}
	if err := e.writeSum(merged); err != nil {
		return err
	}
	e.invalidate()
	return nil
}

// mergeSums unions two go.sum files, refusing a line that contradicts one it
// already has.
//
// A go.sum line is "module version hash", with the version carrying a /go.mod
// suffix for the second line of each pair. Module-and-version is therefore the
// key, and a second hash under an existing key is the conflict — not a
// duplicate, which is what an identical line is and which is simply dropped.
func mergeSums(existing, recorded []byte) ([]byte, error) {
	var out []string
	seen := map[string]string{}

	add := func(data []byte) error {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) != 3 {
				return fmt.Errorf("go.sum line is not module, version and hash: %q", line)
			}
			key := fields[0] + " " + fields[1]
			if prev, ok := seen[key]; ok {
				if prev == fields[2] {
					continue
				}
				return fmt.Errorf("%w: %s is recorded as %s and as %s",
					ErrRestoreConflict, key, prev, fields[2])
			}
			seen[key] = fields[2]
			out = append(out, line)
		}
		return nil
	}

	if err := add(existing); err != nil {
		return nil, err
	}
	if err := add(recorded); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	sort.Strings(out)
	return []byte(strings.Join(out, "\n") + "\n"), nil
}

// ErrNotRequired is what Remove answers with for a module nothing requires.
// It is distinct from ErrHostRequires because the two are fixed differently:
// one is a typo, the other is a module whose owner is the host's go.mod.
var ErrNotRequired = errors.New("not required by this session")

// ErrHostRequires is what Remove answers with for a module the attached host
// requires. gluon never writes into a product repo (invariant 25), and the
// session's go.mod is the host's own copy while attached, so dropping the
// requirement here would either be undone by the next :use or be a change the
// host never asked for.
var ErrHostRequires = errors.New("required by the attached host, not by this session")

// Remove drops a module requirement the session added, and reports every
// requirement that changed as a result.
//
// It is `go get <mod>@none` rather than a modfile.DropRequire and rewrite:
// @none is the toolchain's own removal, so go.sum stays consistent without
// writeSum having to work out which sums went with it.
//
// GOPROXY stays off — the hermetic default — which is what makes constraint G
// mechanical rather than a promise. :get is the only command that reaches the
// network; if resolving a removal somehow needed a fetch (a downgrade pulling
// a version that is not in the module cache) this fails and says so instead of
// quietly going online.
func (e *Evaluator) Remove(mod string) (changed []string, err error) {
	before, err := e.Requires()
	if err != nil {
		return nil, err
	}
	// The host's own requirement is hidden from Requires, so it would
	// otherwise read as "nobody required it" — the least useful of the two
	// answers available.
	if e.attached != nil && e.attached.Path == mod {
		return nil, fmt.Errorf("%s is the attached host module: %w", mod, ErrHostRequires)
	}
	if !requires(before, mod) {
		return nil, fmt.Errorf("%s is %w", mod, ErrNotRequired)
	}
	if e.attached != nil {
		if hostReqs, herr := e.attached.Requires(); herr == nil && requires(hostReqs, mod) {
			return nil, fmt.Errorf("%s is %w", mod, ErrHostRequires)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), networkTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "get", "--", mod+"@none")
	cmd.Dir = e.dir
	cmd.Env = append(e.env(), "GOFLAGS=-mod=mod")
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	runErr := cmd.Run()
	out := strings.TrimSpace(trimPath(buf.String(), e.dir))
	if runErr != nil {
		if out == "" {
			return nil, fmt.Errorf("go get %s@none: %w", mod, runErr)
		}
		return nil, fmt.Errorf("%s", out)
	}

	// The build list has changed, so everything derived from it is stale —
	// invariant 18's three, for the same reason Get drops them.
	e.invalidate()

	after, err := e.Requires()
	if err != nil {
		return nil, err
	}
	return changedRequires(before, after), nil
}

// requires reports whether a "path version" list names mod.
func requires(reqs []string, mod string) bool {
	for _, r := range reqs {
		if p, _, ok := strings.Cut(r, " "); ok && p == mod {
			return true
		}
		if r == mod {
			return true
		}
	}
	return false
}

// changedRequires describes every requirement that differs between two build
// lists, in the direction a removal reads.
//
// A removal can downgrade a module another requirement shares, and a
// downgrade that went unreported would be a silent change to what the session
// compiles against — so a version that moved is named as loudly as one that
// went.
func changedRequires(before, after []string) []string {
	now := make(map[string]string, len(after))
	for _, r := range after {
		p, v, _ := strings.Cut(r, " ")
		now[p] = v
	}
	had := make(map[string]bool, len(before))
	var out []string
	for _, r := range before {
		p, v, _ := strings.Cut(r, " ")
		had[p] = true
		switch nv, still := now[p]; {
		case !still:
			out = append(out, "removed "+r)
		case nv != v:
			out = append(out, p+" "+nv+" (was "+v+")")
		}
	}
	for _, r := range after {
		if p, _, _ := strings.Cut(r, " "); !had[p] {
			out = append(out, "now requires "+r)
		}
	}
	return out
}

// Importers reports which of the session's entries import a package of mod, by
// the number :hist shows.
//
// The question is asked of the resolved import set rather than of the entries'
// text, because the text names a qualifier and only the import block knows
// which module that qualifier came from. Matching is on a path boundary, so
// example.com/a does not claim example.com/ab.
func (e *Evaluator) Importers(s *session.Session, mod string) []int {
	if len(s.Entries) == 0 {
		return nil
	}
	if len(e.imports) == 0 {
		// invalidate drops the import set at every :get and :use, so a
		// removal that follows one would otherwise judge the session by an
		// empty import block and let through the removal that breaks it.
		// Rendering resolves the set again; it builds nothing and runs
		// nothing, which is the same thing :src costs.
		_, _ = e.Render(s)
	}
	// The seeds are asked for too, and they are the half that matters here: a
	// plugin's import is preloaded, so a session using it never runs goimports
	// and e.imports stays empty — which is exactly the module :get -rm is most
	// often pointed at. This is the same union writeAs renders with.
	imps := e.imports
	if seed, err := e.seedImports(s); err == nil {
		imps = mergeImports(imps, seed)
	}

	names := map[string]bool{}
	for _, im := range imps {
		if underModule(im.Path, mod) {
			names[importName(im)] = true
		}
	}
	if len(names) == 0 {
		return nil
	}
	var out []int
	for i, en := range s.Entries {
		for _, q := range render.Qualifiers(en.Src) {
			if names[q] {
				out = append(out, i+1)
				break
			}
		}
	}
	return out
}

// underModule reports whether an import path belongs to a module, matching at
// a path boundary so example.com/a is not a prefix of example.com/ab.
func underModule(importPath, mod string) bool {
	return importPath == mod || strings.HasPrefix(importPath, mod+"/")
}

// Requires lists the modules :get has added, newest last.
func (e *Evaluator) Requires() ([]string, error) {
	data, err := os.ReadFile(filepath.Join(e.dir, "go.mod"))
	if err != nil {
		return nil, err
	}
	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range f.Require {
		// The host requirement is gluon's own plumbing, not something the user
		// asked for.
		if e.attached != nil && r.Mod.Path == e.attached.Path {
			continue
		}
		out = append(out, r.Mod.Path+" "+r.Mod.Version)
	}
	return out, nil
}

// Sum is the go.sum the session's requirements are resolved against, for a
// caller recording what it takes to build this session again — a scratchpad.
//
// Beside Requires and for the same reason: the pair is what a restore needs,
// and reading the file from outside this package would put the knowledge of
// where writeSum puts it in two places.
func (e *Evaluator) Sum() ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(e.dir, "go.sum"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

// networkTimeout bounds :get. A module fetch is slower than anything else
// gluon does, and the REPL must not look wedged while it happens.
const networkTimeout = 90 * time.Second

// goEnv reads one `go env` value, for the settings :get must not override.
func goEnv(name string) string {
	out, err := exec.Command("go", "env", name).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Doc shells out to `go doc`, run inside the session's temp module so an
// import the session already resolved is documented in that context.
//
// go doc already knows how to find a symbol, resolve a method, and format the
// result; reimplementing any of that would be worse in every way.
func (e *Evaluator) Doc(sym string, src bool) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), e.timeout)
	defer cancel()
	args := []string{"doc"}
	if src {
		// -src prints the implementation, which is pry's show-source.
		args = append(args, "-src")
	}
	args = append(args, "--", sym)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = e.dir
	cmd.Env = e.env()
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := strings.TrimSpace(buf.String())
	if err != nil {
		if out == "" {
			return "", fmt.Errorf("go doc %s: %w", sym, err)
		}
		return "", fmt.Errorf("%s", trimPath(out, e.dir))
	}
	return out, nil
}

// DocPackage is `:doc -pkg` — Doc with the answer required to be a package's.
//
// The argument is not resolved with `go list` first, because `go doc` resolves
// a short name the way a person means it — `go doc json` documents
// encoding/json, and `go doc rand/v2` math/rand/v2 — while `go list json`
// refuses ("package json is not in std"). Resolving here would buy the
// disambiguation by taking the short form away.
func (e *Evaluator) DocPackage(pkg string) (string, error) {
	out, err := e.Doc(pkg, false)
	if err != nil {
		return "", fmt.Errorf("%s is unresolved as a package: %w", pkg, err)
	}
	if isPackageDoc(out, pkg) {
		return out, nil
	}
	// A command has no package header — `go doc cmd/go` prints its doc comment
	// — so a path go list resolves is a package even when the header the check
	// wanted is not there. It runs only on the answer that already failed, so
	// the common form still costs one process.
	if _, lerr := e.PackageDir(pkg); lerr == nil {
		return out, nil
	}
	return "", fmt.Errorf("%s is unresolved as a package — go doc resolves that name to a "+
		"symbol, which :doc %s without -pkg shows", pkg, pkg)
}

// isPackageDoc reports whether go doc's output documents pkg itself rather
// than a symbol inside some other package.
//
// The header line says nothing on its own: `go doc strings.Builder` opens with
// `package strings // import "strings"`, the same line `go doc strings` opens
// with. What separates them is whether the argument *is* the package that
// header names — json is a path suffix of encoding/json, and strings.Builder
// is not one of strings.
func isPackageDoc(out, pkg string) bool {
	line, _, _ := strings.Cut(out, "\n")
	if !strings.HasPrefix(line, "package ") {
		return false
	}
	_, after, ok := strings.Cut(line, `// import "`)
	if !ok {
		return false
	}
	path, _, ok := strings.Cut(after, `"`)
	if !ok {
		return false
	}
	return path == pkg || strings.HasSuffix(path, "/"+pkg)
}

// PackageDir is the directory a package's sources are in.
//
// Located with `go list` rather than assumed under GOMODCACHE, so a custom
// module cache is found and a replaced module resolves to the directory it was
// replaced with. The environment is the session's hermetic one, so a module in
// the build list that was never downloaded reports as absent instead of being
// fetched — :get is the only command that goes online.
func (e *Evaluator) PackageDir(pkg string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), e.timeout)
	defer cancel()
	cmd := e.listCmd(ctx, "{{.Dir}}", pkg)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Run(); err != nil {
		out := strings.TrimSpace(buf.String())
		if out == "" {
			return "", fmt.Errorf("go list %s: %w", pkg, err)
		}
		return "", fmt.Errorf("%s", trimPath(out, e.dir))
	}
	dir := strings.TrimSpace(buf.String())
	if dir == "" {
		return "", fmt.Errorf("go list reported no source directory for %s", pkg)
	}
	return dir, nil
}

// listCmd is a `go list -f` in the session's module, under the same hermetic
// environment every other toolchain call gets. It builds the command without
// running it so a test can read the environment it would have run under.
func (e *Evaluator) listCmd(ctx context.Context, format, pkg string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "go", "list", "-f", format, "--", pkg)
	cmd.Dir = e.dir
	cmd.Env = e.env()
	return cmd
}
