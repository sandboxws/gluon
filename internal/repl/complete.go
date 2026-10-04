package repl

import (
	"context"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/sandboxws/gluon/internal/check"
	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/complete"
	"github.com/sandboxws/gluon/internal/config"
	"github.com/sandboxws/gluon/internal/inspect"
	"github.com/sandboxws/gluon/internal/release"
	"github.com/sandboxws/gluon/internal/render"
	"github.com/sandboxws/gluon/internal/scratch"
)

// completions holds what completion needs between keystrokes.
//
// Everything here is a cache, and every entry has a cheap way to be recomputed,
// because a stale completion is a wrong one: offering a field of a variable the
// session no longer binds produces a line that does not compile.
type completions struct {
	// std maps an unambiguous stdlib package name to its import path. It is
	// filled in the background, because `go list std` costs ~0.5s and nothing
	// should wait on it.
	mu       sync.RWMutex
	std      map[string]string
	stdOther []string

	// stdStop and stdDone end that `go list std` and wait for it, so a closed
	// Core leaves no child running. A go process that outlives its session
	// keeps writing — its telemetry, on Linux under $XDG_CONFIG_HOME — after
	// the session's directories are gone.
	stdStop context.CancelFunc
	stdDone chan struct{}

	// names is the session's scope, valid for generation namesGen.
	names    []string
	namesGen int

	// gen is the generation the three lookup caches below were filled at. It is
	// deliberately not namesGen: Complete fills names eagerly, before it asks
	// for any of these, so one shared field would let that write mark these
	// current a moment before they are read — and a stale field list is a
	// completion that does not compile.
	gen int
	// fields caches a member list per expression, cleared whenever the session
	// changes underneath it.
	fields map[string][]string
	// literal is the struct fields of a named type, for composite literals.
	// Separate from fields because it must never include methods: a method in
	// a composite literal does not compile.
	literal map[string][]string
	// args caches the type check behind one callee, keyed on its text, so
	// moving from one argument of a call to the next picks a parameter out of
	// what is already here rather than checking again. A nil entry is a lookup
	// that failed and is not worth repeating on the next keystroke.
	args map[string]*check.Result
	// argsAt caches what was read out of that check for one argument position.
	// A keystroke inside a call asks twice — once for the candidates and once
	// for the hint — and the second ask has to be free, or every key held down
	// inside a call pays for the scope walk twice.
	argsAt map[argKey]argsAnswer

	// values caches the value sets completion reads that cost more than a
	// map lookup — plugin, module, database and theme names, the
	// scratchpads — dropped at :get and :use, and the pads at :scratch.
	values map[cmdspec.Source][]string

	// members caches a package's exported names by import path. Those never
	// change within a session.
	members map[string][]string
}

// argKey is one argument position of one callee. It is a struct rather than a
// joined string so that looking one up on a keystroke allocates nothing.
type argKey struct {
	callee string
	index  int
}

// argsAnswer is what the checker had to say about that position: the signature
// to show, and the names the parameter will take.
type argsAnswer struct {
	sig   string
	cands []string
}

// fresh drops the per-generation lookup caches when the session has moved
// underneath them. The three go together because one submitted line can change
// a type's fields, an expression's members and a callee's signature at once.
func (c *Core) fresh() {
	if c.comp.fields != nil && c.comp.gen == c.gen {
		return
	}
	c.comp.fields = map[string][]string{}
	c.comp.literal = map[string][]string{}
	c.comp.args = map[string]*check.Result{}
	c.comp.argsAt = map[argKey]argsAnswer{}
	c.comp.gen = c.gen
}

// startStdIndex lists the standard library once, off the hot path.
//
// The ambiguous names are kept separately rather than resolved. `rand` is both
// math/rand and crypto/rand, and picking one would offer Read where the user
// meant Intn. They are still offered as identifiers — typing `ran` should still
// complete to `rand` — but with no member list behind them, which leaves the
// choice where it belongs.
func (c *Core) startStdIndex() {
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	c.comp.stdStop, c.comp.stdDone = stop, done
	go func() {
		defer close(done)
		cmd := exec.CommandContext(ctx, "go", "list", "std")
		cmd.Env = append(os.Environ(), "GOFLAGS=", "GOWORK=off", "GOPROXY=off", "GOTOOLCHAIN=local")
		out, err := cmd.Output()
		if err != nil {
			return
		}
		byName := map[string][]string{}
		for _, p := range strings.Fields(string(out)) {
			if strings.Contains(p, "internal/") || strings.HasPrefix(p, "vendor/") {
				continue
			}
			n := path.Base(p)
			byName[n] = append(byName[n], p)
		}
		std := map[string]string{}
		var other []string
		for n, paths := range byName {
			if len(paths) == 1 {
				std[n] = paths[0]
			} else {
				other = append(other, n)
			}
		}
		c.comp.mu.Lock()
		c.comp.std, c.comp.stdOther = std, other
		c.comp.mu.Unlock()
	}()
}

// stopStdIndex ends the stdlib listing if it is still running, and waits until
// it has.
func (c *Core) stopStdIndex() {
	if c.comp.stdStop == nil {
		return
	}
	c.comp.stdStop()
	<-c.comp.stdDone
	c.comp.stdStop = nil
}

// Complete returns whole-line completions for a partly typed line.
//
// It must not be called while Submit is running: both touch the session and the
// evaluator, and Fields type-checks with the expression appended. The UI knows
// when it is busy and does not ask.
func (c *Core) Complete(line string) []string {
	if strings.TrimSpace(line) == "" {
		return nil
	}
	return complete.Suggest(line, complete.Context{
		Metas:     c.MetaNames(),
		Names:     c.completionNames(),
		Packages:  c.completionPackages(),
		Members:   c.completionMembers,
		Fields:    c.completionFields,
		Literal:   c.completionLiteral,
		Arguments: c.completionArguments,
		MetaArgs:  c.completionMetaArgs,
	})
}

// Hint is the callee's signature while the cursor is inside its argument list,
// for the input line to draw beside what is being typed.
//
// It goes through the same cache Complete does, so the second of the two calls
// a keystroke makes is a map lookup. Like Complete it must not be called while
// Submit is running: it type-checks the session.
//
// A meta command's hint is the rest of its usage, from the element under the
// cursor on (cmdspec.Spec.Rest). Where the argument is words it is the only
// hint there is — a URL is never a call. Where it is Go, a call's signature
// wins inside the call, because that is the more specific answer.
func (c *Core) Hint(line string) string {
	if strings.TrimSpace(line) == "" {
		return ""
	}
	cmd, typed, isMeta := c.metaLine(line)
	if isMeta && !cmd.Usage.Kind.IsGo() {
		return cmd.Usage.Rest(cmd.Arg, cmd.Usage.At(cmd.Arg, typed))
	}
	if h := complete.Hint(line, c.completionArguments); h != "" {
		return h
	}
	if isMeta {
		return cmd.Usage.Rest(cmd.Arg, cmd.Usage.At(cmd.Arg, typed))
	}
	return ""
}

// metaLine splits a line that is a known command followed by a space, and
// reports false for anything else — a line still spelling the command's name
// has a name to complete, not a usage to hint.
func (c *Core) metaLine(line string) (Command, string, bool) {
	if !strings.HasPrefix(line, ":") {
		return Command{}, "", false
	}
	i := strings.IndexAny(line, " \t")
	if i < 0 {
		return Command{}, "", false
	}
	cmd, ok := c.lookup(line[:i])
	if !ok {
		return Command{}, "", false
	}
	return cmd, strings.TrimLeft(line[i:], " \t"), true
}

// completionNames is everything nameable without a qualifier, recomputed only
// when the session has changed.
func (c *Core) completionNames() []string {
	if c.comp.names != nil && c.comp.namesGen == c.gen {
		return c.comp.names
	}
	var names []string
	if sc, err := c.scope(); err == nil {
		for _, g := range [][]inspect.Binding{sc.Vars, sc.Funcs, sc.Types, sc.Consts} {
			for _, b := range g {
				names = append(names, b.Name)
			}
		}
	} else {
		// The checker could not run; the names the session binds are still
		// known syntactically, and half a completion list beats none.
		for _, e := range c.sess.Entries {
			names = append(names, e.Binds...)
		}
	}
	// The value carriers are as nameable as anything else here.
	for _, v := range c.values() {
		names = append(names, render.OrdName(v.Ord))
	}
	if len(c.values()) > 0 {
		names = append(names, render.ItName)
	}
	c.comp.names, c.comp.namesGen = names, c.gen
	return names
}

// completionPackages maps a qualifier to the import path it stands for, in
// precedence order: what the session already imports, then what config
// preloads, then the attached host, then the unambiguous standard library.
//
// The order is the point. Once a session imports math/rand, `rand.` means that
// one, and completion has no business offering crypto/rand's names.
func (c *Core) completionPackages() map[string]string {
	out := map[string]string{}
	c.comp.mu.RLock()
	for n, p := range c.comp.std {
		out[n] = p
	}
	c.comp.mu.RUnlock()

	// A walk error leaves the host out of the map rather than emptying it:
	// completion is a keystroke, and the stdlib and preloaded names are still
	// right. :use is where an unreadable tree is reported.
	if ix, err := c.ev.Index(); err == nil && ix != nil {
		for _, p := range ix.All() {
			out[p.Name] = p.Path
		}
	}
	for n, spec := range c.ev.Preload() {
		out[n] = spec.Path
	}
	for _, im := range c.ev.Imports() {
		n := im.Name
		if n == "" {
			n = path.Base(im.Path)
		}
		out[n] = im.Path
	}
	return out
}

// completionMembers is a package's exported names, cached for the session.
func (c *Core) completionMembers(importPath string) []string {
	if c.comp.members == nil {
		c.comp.members = map[string][]string{}
	}
	if m, ok := c.comp.members[importPath]; ok {
		return m
	}
	var names []string
	if pkg, err := c.ev.Lookup(importPath); err == nil {
		names = inspect.PackageMembers(pkg)
	}
	// A failed lookup is cached too: it means the package is not in the build
	// list, and asking again on the next keystroke would cost a `go list`.
	c.comp.members[importPath] = names
	return names
}

// completionLiteral is the struct fields of a named type, for a composite
// literal. It resolves the type the same way :layout does, and is cached until
// the session changes for the same reason the field lookup is: a held-down key
// must not re-run the checker.
func (c *Core) completionLiteral(typeExpr string) []string {
	c.fresh()
	if f, ok := c.comp.literal[typeExpr]; ok {
		return f
	}
	var names []string
	if t, err := c.resolve(typeExpr); err == nil && t.IsType {
		names = inspect.FieldNames(t)
	}
	c.comp.literal[typeExpr] = names
	return names
}

// completionFields is what may follow a dot after expr.
//
// This type-checks on a keystroke — about a millisecond once the checker is
// warm, and only when a dot has actually been typed. The result is cached until
// the session changes, so holding a key down does not re-run it.
func (c *Core) completionFields(expr string) []string {
	c.fresh()
	if f, ok := c.comp.fields[expr]; ok {
		return f
	}
	var names []string
	if t, err := c.resolve(expr); err == nil {
		names = inspect.MemberNames(t)
	}
	c.comp.fields[expr] = names
	return names
}

// completionArguments is the callee's signature and the names its parameter
// will take, from one type check per callee per generation.
//
// Two caches, because a keystroke inside a call asks two questions of one
// answer. The check is keyed on the callee alone, so moving from one argument
// to the next reads a parameter out of what is already here; what was read is
// keyed on the position too, so the hint that follows the candidate list is a
// map lookup rather than the same walk again. A check that failed is cached as
// a nil result: the checker has already said it cannot answer, and asking again
// on every keystroke would only say it again.
func (c *Core) completionArguments(callee string, index int) (string, []string) {
	c.fresh()
	key := argKey{callee: callee, index: index}
	if a, ok := c.comp.argsAt[key]; ok {
		return a.sig, a.cands
	}
	res, ok := c.comp.args[callee]
	if !ok {
		// The error is dropped on purpose. Constraint F: a checker that cannot
		// answer offers nothing, and says nothing about it at a keystroke.
		res, _ = c.ev.Analyze(c.sess, callee)
		c.comp.args[callee] = res
	}
	sig, cands := inspect.Arguments(res, callee, index)
	c.comp.argsAt[key] = argsAnswer{sig: sig, cands: cands}
	return sig, cands
}

// completionMetaArgs completes a meta command's argument from what the command
// declares it takes: the flags allowed where the cursor is, and the values the
// operand or the flag value there may be. A Go operand answers !ok, so the Go
// paths complete it exactly as they always did.
//
// It runs on the keystroke path, whose whole budget is 12-22µs, so every value
// set it reads is either in memory or cached where it changes (invariant 22's
// rule, applied to the reading of files): the plugin, module, database and
// theme names at :get and :use, the scratchpads at :scratch.
func (c *Core) completionMetaArgs(line string) (int, []string, bool) {
	i := strings.IndexAny(line, " \t")
	if i < 0 {
		return 0, nil, false
	}
	cmd, ok := c.lookup(line[:i])
	if !ok {
		return 0, nil, false
	}
	j := i
	for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
		j++
	}
	sp := cmd.Usage
	at := sp.At(cmd.Arg, line[j:])
	var (
		cands []string
		fold  bool
	)
	switch at.Where {
	case cmdspec.OnFlag:
		cands = sp.FlagsFor(at)
	case cmdspec.InFlagValue:
		cands, fold = c.valuesOf(at.Flag.Values, at), at.Flag.Values.Fold
	case cmdspec.InOperand:
		switch {
		case sp.Kind.IsGo():
			return 0, nil, false
		case sp.Kind == cmdspec.SQL, at.Param == nil:
			return 0, nil, true
		}
		cands, fold = c.valuesOf(at.Param.Values, at), at.Param.Values.Fold
	default:
		// Inside a quote, or past what the command reads: nothing here is a
		// name gluon knows.
		return 0, nil, true
	}
	// An empty word gets nothing, as an empty identifier does. The usage hint
	// beside the line says what goes here, and a ghost value drawn over it
	// would be two answers to one question.
	if at.Word == "" {
		return 0, nil, true
	}
	// A parser that accepts any case is offered the case being typed, so the
	// candidate is still a case-sensitive extension of the line (invariant 16).
	if fold && at.Word == strings.ToLower(at.Word) {
		lower := make([]string, len(cands))
		for k, v := range cands {
			lower[k] = strings.ToLower(v)
		}
		cands = lower
	}
	return j + at.Start, cands, true
}

// valuesOf is what a declared value set holds, in this session, now.
func (c *Core) valuesOf(v cmdspec.Values, at cmdspec.At) []string {
	if len(v.Fixed) > 0 {
		return v.Fixed
	}
	switch v.Source {
	case cmdspec.Commands:
		return c.cached(v.Source, c.commandWords)
	case cmdspec.Plugins:
		return c.cached(v.Source, c.pluginNames)
	case cmdspec.Guides:
		if c.plugins == nil {
			return nil
		}
		return c.cached(v.Source, c.plugins.Guides)
	case cmdspec.GetAliases:
		return c.cached(v.Source, c.aliasNames)
	case cmdspec.Themes:
		return c.cached(v.Source, func() []string { return settingValues("theme.name") })
	case cmdspec.Settings:
		return settingKeys(at.Word)
	case cmdspec.SettingValues:
		if len(at.Operands) == 0 {
			return nil
		}
		return settingValues(at.Operands[0])
	case cmdspec.Pads:
		return c.cached(v.Source, scratch.PadNames)
	case cmdspec.Bookmarks:
		return c.marks.names()
	case cmdspec.Entries:
		return c.entryNumbers(false)
	case cmdspec.Pinned:
		return c.entryNumbers(true)
	case cmdspec.Databases:
		return c.cached(v.Source, c.databaseNames)
	case cmdspec.Modules:
		return c.cached(v.Source, c.moduleNames)
	case cmdspec.Releases:
		return releaseVersions()
	}
	return nil
}

// cached keeps a value set until invalidateCommands drops it — which :get and
// :use do, the two points where what the session can see changes — or until
// :scratch drops the scratchpads.
func (c *Core) cached(src cmdspec.Source, fill func() []string) []string {
	if vs, ok := c.comp.values[src]; ok {
		return vs
	}
	if c.comp.values == nil {
		c.comp.values = map[cmdspec.Source][]string{}
	}
	vs := fill()
	c.comp.values[src] = vs
	return vs
}

// commandWords is every command name, with its colon and without, and the
// commands an inactive plugin provides: :help reads those too.
func (c *Core) commandWords() []string {
	var out []string
	for _, n := range append(append([]string(nil), c.MetaNames()...), c.dormantNames()...) {
		out = append(out, n, strings.TrimPrefix(n, ":"))
	}
	return out
}

func (c *Core) pluginNames() []string {
	if c.plugins == nil {
		return nil
	}
	var out []string
	for _, p := range c.plugins.All() {
		out = append(out, p.Meta().Name)
	}
	return out
}

func (c *Core) aliasNames() []string {
	if c.plugins == nil {
		return nil
	}
	var out []string
	for name := range c.plugins.Aliases() {
		out = append(out, name)
	}
	return out
}

func (c *Core) databaseNames() []string {
	if c.ev == nil {
		return nil
	}
	var out []string
	for _, d := range c.databases() {
		out = append(out, d.Name)
	}
	return out
}

// moduleNames is every module the session requires, without its version: what
// :get -rm takes.
func (c *Core) moduleNames() []string {
	if c.ev == nil {
		return nil
	}
	reqs, _ := c.ev.Requires()
	out := make([]string, 0, len(reqs))
	for _, r := range reqs {
		path, _, _ := strings.Cut(r, " ")
		out = append(out, path)
	}
	return out
}

// entryNumbers is the session's entries by the number :hist prints, or only
// the pinned ones.
func (c *Core) entryNumbers(pinned bool) []string {
	if c.sess == nil {
		return nil
	}
	var out []string
	for i, e := range c.sess.Entries {
		if !pinned || e.Pinned {
			out = append(out, strconv.Itoa(i+1))
		}
	}
	return out
}

// settingKeys is every key :settings reads, a family expanded to its members.
// With a `key=` already typed, it is that key's values in the same spelling.
func settingKeys(word string) []string {
	if key, _, ok := strings.Cut(word, "="); ok {
		var out []string
		for _, v := range settingValues(key) {
			out = append(out, key+"="+v)
		}
		return out
	}
	var out []string
	for _, o := range config.Options() {
		if !o.Family {
			out = append(out, o.Key)
			continue
		}
		prefix := strings.TrimSuffix(o.Key, "<role>")
		for _, m := range o.Values() {
			out = append(out, prefix+m)
		}
	}
	return out
}

// settingValues is a setting's closed set, when it has one.
func settingValues(key string) []string {
	o, ok := config.Lookup(key)
	if !ok || o.Values == nil {
		return nil
	}
	return o.Values()
}

// releaseVersions is every Go release the installed toolchain describes, read
// off the api files' names. It is not releases(), which parses every file: a
// keystroke needs the list of names and nothing in them.
var releaseVersions = sync.OnceValue(func() []string {
	root := release.GOROOT()
	if root == "" {
		return nil
	}
	ents, err := os.ReadDir(filepath.Join(root, "api"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		name := e.Name()
		if v, ok := strings.CutPrefix(strings.TrimSuffix(name, ".txt"), "go"); ok &&
			strings.HasSuffix(name, ".txt") && strings.Contains(v, ".") {
			out = append(out, v)
		}
	}
	return out
})
