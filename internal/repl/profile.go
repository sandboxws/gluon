package repl

// :profile and :memprof — where the time and the allocations went.
//
// :bench and :esc already answer "how much": ns/op, B/op, allocs/op, and
// whether a value reaches the heap. Neither answers *where* — which call,
// which allocation site, which function is responsible for the number :bench
// just printed. These two do, from a real run of the expression.
//
// What they deliberately are not is `:pprof <port>`, the live profiling server
// the source analysis asked for. gluon compiles each line into a program that
// runs and exits, so there is no process to serve from — the same
// architectural mismatch as binding.pry. The honest version of that request is
// this one: capture a profile from a real run, summarise it, and hand over the
// standard command that opens it.
//
// The summary is a starting point and says so. A REPL expression is usually
// short, a CPU profile of a short expression has almost no samples in it, and
// a ranking drawn from three samples is not a measurement. The sample count is
// reported every time for exactly that reason.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sandboxws/gluon/internal/inspect"
	"github.com/sandboxws/gluon/internal/pretty"
	"github.com/sandboxws/gluon/internal/render"
	"github.com/sandboxws/gluon/internal/session"
)

// profileKind is which of the two profiles is being captured. They are two
// commands rather than one with a -mem flag because the reports have different
// units, different default measures and different failure modes — the same
// reason :esc and :bench are two commands.
type profileKind int

const (
	cpuProfile profileKind = iota
	memProfile
)

func (k profileKind) command() string {
	if k == memProfile {
		return ":memprof"
	}
	return ":profile"
}

// label heads the report and the file note. Both spellings are the same width,
// which is what keeps the note's continuation line aligned under either.
func (k profileKind) label() string {
	if k == memProfile {
		return "mem profile"
	}
	return "cpu profile"
}

func (k profileKind) file() string {
	if k == memProfile {
		return "mem"
	}
	return "cpu"
}

// profileEnv is the variable the child reads the profile's path from.
//
// The path travels through the environment the way :query passes a DSN, so it
// never enters the generated source: :src stays readable, and the rewrite is
// the same bytes whatever directory the session happens to own. It is not a
// secret, but reusing the mechanism avoids a second way of getting a value
// into the child.
const profileEnv = "GLUON_PROFILE"

// profileSource is the rewrite the two commands evaluate.
//
// It is deliberately small, and every statement in it is one a line you could
// have typed would do — constraint C. runtime/pprof is standard library, so the
// child stays what invariant B requires.
//
// Every identifier it introduces carries gluon's reserved prefix, the
// convention __gluonPrint, __gluonMute and __gluonDrain already follow. It
// matters more here than elsewhere: these names end up in the profile, and
// invariant 9 records what happens when gluon's own wrapper is reported as the
// user's code.
//
// The value is kept alive across the capture the way benchSource keeps its
// sink alive, so the expression is not optimised out of the region measuring
// it. A void expression has nothing to keep, and unlike :bench it is allowed:
// profiling a call made for its effects is a normal thing to want, where
// benchmarking one has nothing to report.
//
// The empty string comes back on success and the error text on failure, so a
// profile that could not be written says why rather than being inferred from a
// missing file.
func profileSource(arg string, kind profileKind, void bool) string {
	var b strings.Builder
	b.WriteString("func() string {\n")

	if kind == memProfile {
		// MemProfileRate samples one allocation per 512KB by default, which
		// records nothing at all for an expression that allocates a few
		// kilobytes — every REPL expression. 1 records every allocation, which
		// is what `go test -memprofilerate=1` does for the same reason.
		//
		// The runtime asks that the rate be set once and as early as possible.
		// Here is as early as gluon can reach: everything above is the
		// session replaying, and that is the part this must not measure.
		b.WriteString("\truntime.MemProfileRate = 1\n")
		b.WriteString(profileAssign(arg, void))
		// The heap profile's in-use side is only accurate after a collection.
		// The allocated side, which is what is reported, is cumulative and
		// does not need one — but a GC here costs nothing next to a build and
		// makes the other three measures in the same file worth opening.
		b.WriteString("\truntime.GC()\n")
		b.WriteString(profileCreate())
		b.WriteString("\t__gluonErr = pprof.WriteHeapProfile(__gluonPF)\n")
		b.WriteString("\t__gluonPF.Close()\n")
		b.WriteString(profileKeepAlive(void))
		b.WriteString("\tif __gluonErr != nil {\n\t\treturn __gluonErr.Error()\n\t}\n")
		b.WriteString("\treturn \"\"\n}()")
		return b.String()
	}

	b.WriteString(profileCreate())
	b.WriteString("\tif __gluonErr = pprof.StartCPUProfile(__gluonPF); __gluonErr != nil {\n")
	b.WriteString("\t\treturn __gluonErr.Error()\n\t}\n")
	b.WriteString(profileAssign(arg, void))
	b.WriteString("\tpprof.StopCPUProfile()\n")
	b.WriteString("\t__gluonPF.Close()\n")
	b.WriteString(profileKeepAlive(void))
	b.WriteString("\treturn \"\"\n}()")
	return b.String()
}

func profileCreate() string {
	return "\t__gluonPF, __gluonErr := os.Create(os.Getenv(" + strconv.Quote(profileEnv) + "))\n" +
		"\tif __gluonErr != nil {\n\t\treturn __gluonErr.Error()\n\t}\n"
}

func profileAssign(arg string, void bool) string {
	if void {
		return "\t" + arg + "\n"
	}
	return "\t__gluonV := " + arg + "\n"
}

func profileKeepAlive(void bool) string {
	if void {
		return ""
	}
	return "\truntime.KeepAlive(__gluonV)\n"
}

// profileImports names every package the rewrite uses, which is more than
// :bench -profile has to name.
//
// pprof has to be named because it resolves to two standard library packages
// and net/http/pprof is the one that would compile and do nothing. The others
// are named for a second reason: an import set the evaluator can satisfy from
// what it already holds is one goimports does not have to be run for, and a
// goimports pass rewrites the import block in a way that moves the //line
// directives off the declarations they belong to. Those directives are what
// maps a reported position back onto the session's own source, so naming the
// whole set is what keeps the report saying "entry 2" instead of a path under
// /var/folders.
//
// The list follows the source rather than leading it: an import the rewrite
// does not name would be an unused one, which does not compile.
func profileImports(kind profileKind, void bool) []render.ImportSpec {
	imps := []render.ImportSpec{{Path: "os"}, {Path: "runtime/pprof"}}
	if kind == memProfile || !void {
		// MemProfileRate and GC on one side, KeepAlive on the other.
		imps = append(imps, render.ImportSpec{Path: "runtime"})
	}
	return imps
}

// profile is :profile — the expression under the CPU profiler.
func (c *Core) profile(arg string) Result { return c.captureProfile(arg, cpuProfile) }

// memprof is :memprof — the expression under the heap profiler.
func (c *Core) memprof(arg string) Result { return c.captureProfile(arg, memProfile) }

func (c *Core) captureProfile(arg string, kind profileKind) Result {
	usage := "usage: :profile <expression>   e.g. :profile solve(puzzle)"
	if kind == memProfile {
		usage = "usage: :memprof <expression>   e.g. :memprof parse(doc)"
	}
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return Result{Out: usage, Err: true}
	}
	target, err := c.resolve(arg)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	switch {
	case target.IsType:
		return Result{Out: "error: that is a type, not something to run", Err: true}
	case len(target.Tuple) > 1:
		return Result{Out: "error: " + kind.command() + " takes one value; " + arg +
			" returns " + strconv.Itoa(len(target.Tuple)), Err: true}
	}

	path := profilePath(c.ev.Dir(), kind)

	entry := session.Entry{Kind: session.KindExpr, Src: profileSource(arg, kind, target.Void)}
	// EvalLive, not EvalTransient, for the two reasons :bench -profile has.
	// The result cache is keyed on program text alone, so a cached hit would
	// report a path nothing had written; and the extra imports need naming
	// rather than guessing, because `pprof` resolves to two standard library
	// packages. Invariant 14 still holds — EvalLive snapshots the imports and
	// pops the entry exactly as EvalTransient does.
	res, err := c.ev.EvalLive(c.sess, entry, profileImports(kind, target.Void),
		[]string{profileEnv + "=" + path}, nil)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}

	userOut, vals := pretty.Parse(res.Output)
	out := strings.TrimRight(userOut, "\n")
	if len(vals) != 1 {
		if out == "" {
			out = "error: the profiled expression produced no answer"
		}
		return Result{Out: out, Err: true}
	}
	if msg := vals[0].Repr; msg != "" {
		return Result{Out: "error: the profile could not be written: " + msg, Err: true}
	}

	report := c.profileReport(kind, path)
	if out != "" {
		// What the expression itself printed comes first, as it does on any
		// other line.
		report = out + "\n\n" + report
	}
	report += "\n\n" + c.profileNote(kind.label(), path)
	return Result{Out: report, Modal: pageable(kind.command()+" "+arg, report)}
}

// profilePath names the file this invocation writes.
//
// The name is unique per invocation, so a second :profile does not silently
// overwrite the first — which the user may still have open in `go tool pprof`.
// dir is the evaluator's own temp directory and never a project: invariant 1
// exists because a bare `go build` once put a tracked 3.1 MB binary into
// another repository's history, uncovered by .gitignore, and a profile is
// exactly that kind of artefact.
func profilePath(dir string, kind profileKind) string {
	return filepath.Join(dir, fmt.Sprintf("%s-%d-%d.pprof",
		kind.file(), time.Now().UnixNano(), profileSeq.Add(1)))
}

// profileSeq separates two profiles taken inside the same clock tick. The
// timestamp alone does not: a test taking eight in a loop caught them
// colliding, and while two real invocations are a build apart, a name that is
// unique by luck is not unique.
var profileSeq atomic.Uint64

// pprofTimeout bounds the summary. `go tool pprof -top` on a file this small
// answers in well under a second; the bound is here so a toolchain that hangs
// costs a message rather than the prompt.
const pprofTimeout = 20 * time.Second

// profileRows is how many contributors the inline summary shows. It is a
// summary — the file holds everything, and the command that opens it is
// printed under every report.
const profileRows = 15

// profileReport summarises the profile, or says why it could not.
//
// The fallback is the posture invariant 5 sets for the type checker: degrade
// to what is certainly true — the file exists and here is what opens it —
// never to a wrong summary. `go tool pprof`'s output format is not a
// documented interface, so a toolchain that changes it costs the summary and
// nothing else.
func (c *Core) profileReport(kind profileKind, path string) string {
	top, err := c.pprofTop(kind, path)
	if err != nil {
		return c.profileUnavailable(kind, err.Error())
	}
	p, ok := c.profileFrom(kind, top)
	if !ok {
		return c.profileUnavailable(kind, "go tool pprof printed nothing this understands")
	}
	if c.Rich {
		return inspect.RenderProfile(p, c.Styles)
	}
	return inspect.PlainProfile(p)
}

func (c *Core) profileUnavailable(kind profileKind, why string) string {
	st := c.styles()
	head, note := kind.label(), "no summary — "+why
	if c.Rich {
		head, note = st.Type.Render(head), st.Note.Render(note)
	}
	return head + "\n  " + note
}

// pprofTop asks the toolchain's own pprof for the summary.
//
// Decoding the protobuf profile format here would mean either a new dependency
// (constraint K) or a hand-rolled decoder, and ROADMAP.md already records what
// hand-rolling a format costs in the rejected YAML scanner: a detector that is
// confidently wrong is worse than one that says nothing. `go tool pprof` ships
// with the toolchain gluon already requires, and internal/repl already shells
// to `go list std` for completion.
//
// -lines rather than function granularity because "which line" is the question
// a profile is opened to answer, and because the line is what carries the
// //line directive that maps a position back onto the session's own source.
func (c *Core) pprofTop(kind profileKind, path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pprofTimeout)
	defer cancel()
	args := []string{"tool", "pprof", "-top", "-lines",
		"-nodecount=" + strconv.Itoa(profileRows)}
	if kind == memProfile {
		// A heap profile carries four measures and pprof defaults to
		// inuse_space, which answers a different question. Allocated bytes is
		// the one that pairs with the B/op :bench already reports.
		args = append(args, "-sample_index=alloc_space")
	}
	cmd := exec.CommandContext(ctx, "go", append(args, path)...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Run(); err != nil {
		if out := strings.TrimSpace(buf.String()); out != "" {
			return "", errors.New(firstLine(out))
		}
		return "", err
	}
	return buf.String(), nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// profileFrom turns pprof's report into gluon's.
func (c *Core) profileFrom(kind profileKind, top string) (inspect.Profile, bool) {
	head, rows, ok := parsePprofTop(top)
	if !ok {
		return inspect.Profile{}, false
	}

	p := inspect.Profile{Label: kind.label()}
	if kind == memProfile {
		// The measure is named rather than left to be assumed. A memory
		// profile has four, they answer different questions, and printing one
		// unlabelled is how a reader concludes the wrong thing.
		p.Label += ", allocated bytes"
		p.Head = head.total + " allocated"
		p.Nothing = "no allocation recorded — nothing in this expression reached the heap"
	} else {
		// The sample count and nothing else. pprof also prints a duration,
		// and it was in this line until it was measured: StopCPUProfile
		// blocks until the profiler has flushed, which pins the reported
		// duration near 200ms whether the expression took a microsecond or a
		// tenth of a second. Printed beside a sample count it reads as the
		// expression's own runtime, which is the one thing it is not.
		n, known := cpuSamples(head.samples)
		p.Head = plural(n, "sample")
		if !known {
			p.Head = "sample count unknown"
		}
		if known && n < enoughSamples {
			p.Caveat = "too few samples to draw a conclusion from — read the ranking " +
				"below as a hint, not a measurement"
		}
		p.Nothing = "no samples were collected — Go samples at 100 Hz, so an " +
			"expression that finishes inside a hundredth of a second contributes none"
	}

	for _, r := range rows {
		// A row with no flat value of its own contributed nothing itself; it
		// is on the path to something that did. The graph in the file shows
		// that better than a column of zeros under a heading that says where
		// the time went.
		if r.flat == "0" {
			continue
		}
		where, src := c.profileWhere(r.loc)
		if harnessRow(kind, r.fn, where) {
			continue
		}
		p.Rows = append(p.Rows, inspect.ProfileRow{
			Flat: r.flat, Pct: r.pct, Fn: r.fn, Where: where, Src: src,
		})
	}
	if len(p.Rows) == 0 {
		// With nothing to rank, a caveat about how to read the ranking has
		// nothing to qualify. The memory total goes too: it is the whole
		// process's, and printing it above a line that says no allocation was
		// recorded is two answers to one question.
		p.Caveat = ""
		if kind == memProfile {
			p.Head = ""
		}
	}
	return p, true
}

// enoughSamples is where a CPU profile stops being a curiosity.
//
// The relative error on a contributor's share falls as 1/sqrt(n), so a hundred
// samples — one second of CPU at Go's 100 Hz — puts it around 10%. Below that
// the ranking can invert between two runs of the same expression, which is the
// failure the caveat names.
//
// The number is a judgement, and it is the second signal rather than the only
// one: the sample count is printed either way, so a reader who disagrees with
// the threshold has what they need to say so.
const enoughSamples = 100

// cpuSamplePeriod is one sample. Go's CPU profiler runs at 100 Hz —
// StartCPUProfile's rate, which gluon never changes — so pprof's "Total
// samples = 30ms" is three of them.
//
// The count is what gets printed, because it is what the reader has to judge:
// "30ms of samples" hides that the ranking rests on three observations, and
// "3 samples" does not.
const cpuSamplePeriod = 10 * time.Millisecond

func cpuSamples(total string) (int, bool) {
	if total == "" {
		return 0, false
	}
	d, err := time.ParseDuration(total)
	if err != nil {
		return 0, false
	}
	return int(d / cpuSamplePeriod), true
}

// harnessRow reports that a row names gluon's own harness rather than the
// user's code.
//
// The reserved prefix is the exact match the whole convention exists for.
// Every identifier gluon injects into a generated program carries it —
// __gluonPrint, __gluonMute, __gluonDrain, and the ones profileSource adds —
// so a frame carrying it is gluon's by construction rather than by
// resemblance. Invariant 9 is why that matters: :esc once reported gluon's own
// wrapper as the user's allocation, and it took a measurement to notice.
//
// Two shapes carry no prefix and are still gluon's. main.main is the program
// render writes around every session, and main.main.funcN is a closure inside
// it — which the rewrite's own wrapper is. A closure cannot be given a name:
// the compiler names it after the function enclosing it, so the prefix on the
// variable it is assigned to never reaches the profile. The position settles
// it instead. A row in one of those two that falls inside the session's own
// source is the user's code — a func literal they typed — and is kept.
//
// runtime/pprof is the profiler measuring itself.
//
// runtime is excluded from a memory profile and kept in a CPU one, which looks
// inconsistent and is not. The heap profiler records the stack above its own
// allocator, so a user allocation is attributed to the user's line and never
// lands flat in the runtime: a flat runtime row in a heap profile is the
// runtime allocating for itself — thread stacks, GC metadata — and would
// otherwise be the entire report for an expression that allocates nothing. In
// a CPU profile runtime.mallocgc is where the time actually went, and dropping
// it would delete the answer. :trace's harnessFrame drops runtime frames for a
// third reason again: in a traceback they are the same three lines under every
// Go program.
func harnessRow(kind profileKind, fn, where string) bool {
	switch {
	case strings.Contains(fn, "__gluon"):
		return true
	case strings.HasPrefix(fn, "runtime/pprof."):
		return true
	case kind == memProfile && strings.HasPrefix(fn, "runtime."):
		return true
	case fn == "main.main":
		// The program render writes around every session. What lands flat in
		// it is the replayed prefix and the rewrite's own statements, neither
		// of which is what was asked about.
		return true
	case strings.HasPrefix(fn, "main.main.func"):
		// A closure directly inside main, and which one depends on where it
		// was typed. In an ordinary entry it is a func literal the user wrote
		// and is theirs. In the entry gluon generated for this evaluation it
		// is the rewrite's own wrapper — except for a literal the user typed
		// on the :profile line itself, which is nested inside that wrapper and
		// so carries a second .func segment: main.main.func1.func1 against the
		// wrapper's main.main.func1. That suffix is the only thing separating
		// them, and it is the compiler's own naming rather than a guess.
		if where == profiledExpr {
			return strings.Count(fn, ".func") < 2
		}
		return !strings.HasPrefix(where, "entry ")
	}
	return false
}

// profiledExpr is what a position inside the entry gluon generated for this
// evaluation is called. That entry holds the harness with the user's own
// expression interpolated into it, so there is no session line to name — and
// naming the temp file it was written to would be worse than saying what it is.
const profiledExpr = "the expression you profiled"

// profileWhere maps a position pprof reported onto the session's own source.
//
// The compiler resolves the same //line directives here that the runtime does
// for a panic traceback, so a position inside the session already names a
// synthetic file rather than a path under /var/folders the user has never
// opened. The mapping is render.EntryOf's, the one mapTraceback and :trace
// both use, and DeclLineShift is applied for the reason it exists: gofmt hoists
// the directive off a declaration's own line, putting the code one line later.
func (c *Core) profileWhere(loc string) (where, src string) {
	if loc == "" {
		return "", ""
	}
	i := strings.LastIndexByte(loc, ':')
	if i < 0 {
		return shortLoc(loc), ""
	}
	file, num := loc[:i], loc[i+1:]
	line, err := strconv.Atoi(num)
	if err != nil {
		return shortLoc(loc), ""
	}
	n, ok := render.EntryOf(filepath.Base(file))
	if !ok {
		// Not the session's own code: the standard library, or the module the
		// session is attached to. Both are real files worth naming, and the
		// absolute path to them is noise beside a function name that already
		// says which package it is.
		return shortLoc(loc), ""
	}
	if n >= len(c.sess.Entries) {
		return profiledExpr, ""
	}
	en := c.sess.Entries[n]
	if en.Kind == session.KindDecl {
		line -= render.DeclLineShift
	}
	where = "entry " + strconv.Itoa(n+1)
	lines := strings.Split(en.Src, "\n")
	if line >= 1 && line <= len(lines) {
		src = strings.TrimSpace(lines[line-1])
	}
	return where, src
}

// shortLoc keeps the last two elements of a path outside the session, which is
// the part that identifies the file to a reader who knows the package.
func shortLoc(loc string) string {
	parts := strings.Split(loc, "/")
	if len(parts) <= 2 {
		return loc
	}
	return strings.Join(parts[len(parts)-2:], "/")
}

// pprofHead is what the report's preamble said.
type pprofHead struct {
	// samples is pprof's "Total samples = 30ms", a duration, and empty for a
	// heap profile which has no such line.
	samples string
	// total is the "of X total" the accounting line ends with.
	total string
}

// pprofTopLine is one parsed row.
type pprofTopLine struct{ flat, pct, fn, loc string }

// parsePprofTop reads what `go tool pprof -top` printed.
//
// Loosely, on purpose. Neither the preamble nor the column layout is a
// documented interface, and both have moved across toolchain versions.
// Everything here is recoverable: a row that does not parse is dropped, and a
// report whose column header never appears is refused outright so the caller
// falls back to naming the file. A summary that is confidently wrong is worse
// than no summary.
func parsePprofTop(out string) (pprofHead, []pprofTopLine, bool) {
	var head pprofHead
	if m := pprofSamplesRe.FindStringSubmatch(out); m != nil {
		head.samples = m[1]
	}
	if m := pprofTotalRe.FindStringSubmatch(out); m != nil {
		head.total = m[1]
	}

	var rows []pprofTopLine
	inRows := false
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if !inRows {
			// The column header, which is the only line in the preamble whose
			// first and last words are these.
			inRows = len(f) == 5 && f[0] == "flat" && f[4] == "cum%"
			continue
		}
		// flat, flat%, sum%, cum, cum%, function, and then a position and
		// possibly "(inline)". Fewer than six fields is not a row.
		if len(f) < 6 {
			continue
		}
		row := pprofTopLine{flat: f[0], pct: f[1], fn: f[5]}
		if len(f) > 6 {
			row.loc = f[6]
		}
		rows = append(rows, row)
	}
	return head, rows, inRows
}

var (
	pprofSamplesRe = regexp.MustCompile(`Total samples = (\S+)`)
	pprofTotalRe   = regexp.MustCompile(`of (\S+) total`)
)
