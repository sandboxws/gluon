package eval

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sandboxws/gluon/internal/render"
	"github.com/sandboxws/gluon/internal/session"
)

// InlineResult is what :inline found.
//
// Here is what the analysed line itself produced; Elsewhere is every other
// decision the build reported, which is where a declared function's own
// verdict lives — it is attributed to the entry that declared it, not to the
// line asking about it.
type InlineResult struct {
	Here      []Diag
	Elsewhere []Diag
	Source    string
}

// inlineKeep is the complete inlining vocabulary, the complement of
// escapeKeep: -m prints both questions interleaved and each command reports
// only its own.
var inlineKeep = []string{
	"can inline",
	"cannot inline",
	"inlining call to",
}

func isInlineMsg(text string) bool {
	for _, k := range inlineKeep {
		if strings.Contains(text, k) {
			return true
		}
	}
	return false
}

// InlineAnalysis renders the session with the expression appended, builds it
// with -m=2, and never runs it.
//
// -m=2 rather than -m: level 1 says a function can be inlined and that a call
// was, but only level 2 says why one cannot — "function too complex: cost 107
// exceeds budget 80" — and the reason is the half worth leaving the REPL for.
func (e *Evaluator) InlineAnalysis(s *session.Session, entry session.Entry) (InlineResult, error) {
	var res InlineResult
	err := e.buildOnly(s, entry, "-e -m=2", func(out, src string, berr error) error {
		kept, rest := splitKeep(out, isInlineMsg)
		if berr != nil && strings.TrimSpace(rest) != "" {
			return &BuildError{Msg: e.explain(s, rest, src), Source: src}
		}
		here, elsewhere := mapMessages(s, kept)
		res = InlineResult{Source: src, Here: here, Elsewhere: elsewhere}
		return nil
	})
	if err != nil {
		return InlineResult{}, err
	}
	return res, nil
}

// AsmResult is one function's assembly listing, already read out of the
// whole-package -S output.
type AsmResult struct {
	// Symbol is the linker symbol the block was found under — main.add,
	// main.(*P).Inc.
	Symbol string
	// Lines is the block: its STEXT header and the instructions under it,
	// with every source position rewritten to name an entry.
	Lines []string
	// Emitted reports that the compiler produced a block at all. A function
	// inlined at every call site and dropped has none, which is an answer
	// rather than an empty pane.
	Emitted bool
	Source  string
}

// Assembly builds the session with -S and returns the listing for one symbol.
//
// entry is appended when it carries source, and is empty for :asm: the
// function being asked about is already one of the session's declarations, so
// there is nothing to add to the program.
func (e *Evaluator) Assembly(s *session.Session, entry session.Entry, symbol string) (AsmResult, error) {
	var res AsmResult
	err := e.buildOnly(s, entry, "-e -S", func(out, src string, berr error) error {
		// The listing goes to stderr alongside any diagnostics, so a failed
		// build has to be separated from it before explain sees the buffer —
		// every instruction line carries a file:line the regex would match.
		block, rest := sliceSymbol(out, symbol)
		if berr != nil {
			return &BuildError{Msg: e.explain(s, rest, src), Source: src}
		}
		res = AsmResult{Symbol: symbol, Source: src, Emitted: len(block) > 0}
		for _, line := range block {
			res.Lines = append(res.Lines, mapAsmPositions(s, line))
		}
		return nil
	})
	if err != nil {
		return AsmResult{}, err
	}
	return res, nil
}

// sliceSymbol cuts one function's block out of a -S listing and returns
// everything else as rest.
//
// A block is a `main.<sym> STEXT ...` header followed by tab-indented lines,
// and it ends at the next line that is not indented — the next header, or the
// symbol data that follows the last one. Both halves of that shape have been
// stable since Go 1.10, which is why the slicer keys on them rather than on
// the header's own field layout, which has not been.
func sliceSymbol(out, symbol string) (block []string, rest string) {
	var others []string
	header := symbol + " STEXT"
	in := false
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "\t") {
			if in {
				block = append(block, line)
				continue
			}
			others = append(others, line)
			continue
		}
		if in {
			// A line at column zero ends the block, whatever it is.
			in = false
		}
		if strings.HasPrefix(line, header) {
			in = true
			block = append(block, line)
			continue
		}
		others = append(others, line)
	}
	return block, strings.Join(others, "\n")
}

// asmPosRe matches the parenthesised source position the compiler puts on
// every instruction line. With a //line directive in play — which is every
// line gluon renders — it writes the directive's position first and the
// physical one after it in brackets:
//
//	(/tmp/gluon-session-1/gluon-in-0.go:2[/tmp/gluon-session-1/main.go:4])
//
// The whole group is matched rather than just the first half, because the
// bracketed half names main.go under a temp directory the user has never
// opened. Only the first position is read; the rest goes.
var asmPosRe = regexp.MustCompile(`\([^()]*\.go:\d+[^()]*\)`)

// asmFileRe reads the leading <path>.go:<line> back out of such a group. It is
// separate from asmPosRe because the group's tail may nest brackets for a
// chain of inlined frames, and only the outermost position is the one the user
// typed.
var asmFileRe = regexp.MustCompile(`([^()\[\]:\s]+\.go):(\d+)`)

// mapAsmPositions rewrites every position in one listing line to name the
// entry it came from, so no path under /var/folders reaches the pane.
func mapAsmPositions(s *session.Session, line string) string {
	return asmPosRe.ReplaceAllStringFunc(line, func(match string) string {
		m := asmFileRe.FindStringSubmatch(match)
		if m == nil {
			return match
		}
		base := filepath.Base(m[1])
		idx, ok := render.EntryOf(base)
		if !ok {
			// gluonrt.go and the package header are gluon's own code. The
			// basename says which, without a directory the user never opened
			// — the same fallback mapTraceback takes.
			return "(" + base + ":" + m[2] + ")"
		}
		return fmt.Sprintf("(entry %d line %d)", idx, entryLine(s, idx, atoi(m[2])))
	})
}

// entryLine undoes the shift gofmt introduces between an entry's own source
// and the rendered program, which is the same correction quoteEntry makes
// before it indexes.
//
// The result is clamped to the first line: a position that maps above one is
// the declaration's own header, and "line 0" is a position no editor has.
func entryLine(s *session.Session, idx, line int) int {
	if idx >= 0 && idx < len(s.Entries) && s.Entries[idx].Kind == session.KindDecl {
		line -= render.DeclLineShift
	}
	if line < 1 {
		return 1
	}
	return line
}

// VetResult is what :vet found.
type VetResult struct {
	// Findings are the diagnostics against the user's own code, in the order
	// vet reported them.
	Findings []Diag
	Source   string
}

// Vet runs the toolchain's vet analysers over the session, with the
// expression appended when there is one, and never runs the program.
//
// Rendering goes through the ordinary sink rather than EscSink: the whole
// point of `:vet fmt.Printf("%s", 42)` is a call whose results EscSink's
// `_ = expr` cannot bind, and vet reads the call itself either way.
//
// Vet's exit 1 means "found something", not "failed", so the exit code is
// ignored in favour of parsing what it printed. A run that fails for any other
// reason prints nothing that parses and reports nothing, which is the same
// answer as a clean session — so the error is returned rather than swallowed.
func (e *Evaluator) Vet(s *session.Session, entry session.Entry) (VetResult, error) {
	var res VetResult
	err := e.withEntry(s, entry, render.PrintSink, func(src string) error {
		out, verr := e.vet()
		findings, rest := splitKeep(out, func(string) bool { return true })
		if len(findings) == 0 && verr != nil {
			// Nothing parsed as a diagnostic, so vet did not run at all.
			return &BuildError{Msg: trimPath(strings.TrimSpace(rest), e.dir), Source: src}
		}
		here, elsewhere := mapMessages(s, findings)
		// Vet reports in file order, which puts the appended expression last.
		// Reading order is the session's, so elsewhere comes first.
		res = VetResult{Source: src, Findings: append(elsewhere, here...)}
		return nil
	})
	if err != nil {
		return VetResult{}, err
	}
	return res, nil
}

// RaceResult is what :race saw.
type RaceResult struct {
	// Output is what the program printed, with the detector's own report
	// taken out of it, so the caller reads a value the way it reads any
	// evaluation's.
	Output string
	// Report is the detector's report, every frame named by the entry it came
	// from. Empty when no race was found.
	Report string
	// Raced reports that the detector found something. It is the exit code,
	// not the presence of Report: a report with no exit and an exit with no
	// report are both things to say out loud rather than guess about.
	Raced bool
	// FirstBuild reports that the instrumented binary did not exist before
	// this call, so the race-instrumented standard library may have been
	// compiled for it.
	FirstBuild bool
	Source     string
	ExitCode   int
}

// raceExitCode is what a binary built with -race exits with when the detector
// reported something. It is the detector's own choice of code, and it is not a
// crash: the program ran to completion and its value is still worth printing.
const raceExitCode = 66

// raceBinary is the instrumented binary's name, deliberately not prog.
//
// A -race build is a different program — different instrumentation, different
// runtime, different output — and the ordinary path's result cache is keyed on
// source text, which cannot tell the two apart. Writing over prog would also
// cost the next ordinary line a fresh Gatekeeper validation. So the two
// binaries sit side by side, and nothing on this path reads or writes
// e.cache: the cache key cannot distinguish the two questions, so it must not
// be asked either of them.
const raceBinary = "prog-race"

// RaceRun builds the session with the race detector enabled, runs the
// expression once, and reports what the detector saw.
//
// It is the one command here that executes, because a data race is a runtime
// fact: there is no build that can be asked about it. It never falls back to a
// run without the detector — a value printed under a command named :race, by a
// program the detector never watched, is the confidently-wrong answer this
// project rejects.
func (e *Evaluator) RaceRun(s *session.Session, entry session.Entry) (RaceResult, error) {
	first := !e.raceBuilt()

	var res RaceResult
	err := e.withEntry(s, entry, render.PrintSink, func(src string) error {
		res = RaceResult{Source: src, FirstBuild: first}

		out, berr := e.buildArgs([]string{"-race"}, "-e", filepath.Join(e.dir, raceBinary))
		if berr != nil {
			if errors.Is(berr, errBuildTimeout) {
				return &BuildError{Msg: raceBuildTimeout(e.timeout), Source: src}
			}
			// A platform that has no race detector says so in the toolchain's
			// own words, which name the supported targets. Nothing is
			// paraphrased and nothing is retried without the flag.
			return &BuildError{Msg: e.explain(s, out, src), Source: src}
		}

		runOut, code, rerr := e.runBinary(raceBinary, raceRunTimeout(e.timeout))
		if rerr != nil {
			return rerr
		}
		res.ExitCode = code
		res.Raced = code == raceExitCode

		report, rest := splitRace(runOut)
		res.Report = mapRaceFrames(s, report)
		if code != 0 && !res.Raced {
			// Any other non-zero exit is an ordinary crash, and its traceback
			// is annotated the way every other one is.
			rest = mapTraceback(s, rest, e.dir)
		}
		res.Output = rest
		return nil
	})
	if err != nil {
		return RaceResult{FirstBuild: first}, err
	}
	return res, nil
}

// raceBuilt reports whether this session has already built its instrumented
// binary, which is the only thing gluon can cheaply know about how warm the
// toolchain's -race cache is.
func (e *Evaluator) raceBuilt() bool {
	_, err := os.Stat(filepath.Join(e.dir, raceBinary))
	return err == nil
}

// raceBuildTimeout says what ran long, because the ordinary build's message
// does not apply: nothing the user typed is wedged, the toolchain is compiling
// the instrumented standard library.
func raceBuildTimeout(d time.Duration) string {
	return "the instrumented build timed out after " + d.String() +
		" — the first -race build compiles the race-instrumented standard library, " +
		"which takes longer than an ordinary line; :settings can raise the timeout"
}

// raceRunTimeout is the same distinction for the run: a -race binary is
// several times slower than the one an ordinary line produces, so a timeout
// here is not the wedged statement :undo answers.
func raceRunTimeout(d time.Duration) string {
	return "the instrumented run timed out after " + d.String() +
		" — a -race binary runs several times slower than an ordinary one, so an " +
		"expression that fits in the timeout normally may not under the detector"
}

// raceBanner delimits the detector's report in the child's output. The
// detector writes it whole between two of these, so the split is on the marker
// rather than on any of the report's own wording.
const raceBanner = "=================="

// splitRace separates the detector's report from what the program itself
// printed, so a value can still be parsed out of the rest.
func splitRace(out string) (report, rest string) {
	var reported, others []string
	in := false
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, raceBanner) {
			in = !in
			reported = append(reported, line)
			continue
		}
		if in {
			reported = append(reported, line)
			continue
		}
		// The detector's own summary sits outside the banners.
		if strings.HasPrefix(line, "Found ") && strings.Contains(line, "data race") {
			reported = append(reported, line)
			continue
		}
		others = append(others, line)
	}
	return strings.Join(reported, "\n"), strings.Join(others, "\n")
}

// raceFrameRe matches one frame's position in a detector report: the whole
// path ending in a synthetic file, so the temp directory in front of it goes
// with it. It is traceRe's shape, kept separate only because the replacement
// below reads the file name back out.
var raceFrameRe = regexp.MustCompile(`\S*` + lineFileHint + `\d+\.go:(\d+)`)

// mapRaceFrames names every frame in the detector's report by the entry it
// came from, and quotes that entry's line.
//
// mapTraceback quotes but does not name: for a panic the source line is the
// whole answer, while a race report is read by comparing two stacks, and
// "entry 3" is what tells you which of your lines they are. The machinery is
// the same — the runtime honours the //line directives, so the frames already
// carry synthetic files — only the replacement differs.
func mapRaceFrames(s *session.Session, report string) string {
	if report == "" {
		return ""
	}
	return raceFrameRe.ReplaceAllStringFunc(report, func(match string) string {
		m := raceFrameRe.FindStringSubmatch(match)
		idx, ok := render.EntryOf(pathBase(match))
		if !ok || idx >= len(s.Entries) {
			return match
		}
		line := entryLine(s, idx, atoi(m[1]))
		src := strings.Split(s.Entries[idx].Src, "\n")
		if line < 1 || line > len(src) {
			return fmt.Sprintf("entry %d", idx)
		}
		return fmt.Sprintf("entry %d line %d: %s", idx, line, strings.TrimSpace(src[line-1]))
	})
}
