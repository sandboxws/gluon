package eval

import (
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sandboxws/gluon/internal/check"
	"github.com/sandboxws/gluon/internal/render"
	"github.com/sandboxws/gluon/internal/session"
)

type verdictKind int

const (
	// verdictUnavailable means the checker could not answer, for any reason.
	// The build is then the only authority, exactly as in v1.
	verdictUnavailable verdictKind = iota
	// verdictValid means the program type-checks.
	verdictValid
	// verdictErrors means it does not, and diag says why.
	verdictErrors
	// verdictConst means the answer is a compile-time constant already in hand.
	verdictConst
)

type verdict struct {
	kind   verdictKind
	diag   string // compiler-shaped diagnostics, for verdictErrors
	output string // the encoded value, for verdictConst
}

// analyze type-checks the rendered program and decides what still has to
// happen. It may re-render — learning that a call has no result, or that the
// cached import set has gone stale, changes the program — so src and cached
// are updated in place for the caller to build from.
//
// Every failure path returns verdictUnavailable rather than a verdict on the
// user's code. The checker is an optimisation; it must never be able to reject
// a program the compiler would have accepted.
//
// from is the unmute boundary the caller rendered with; the re-render paths
// must use the same one or the retry would emit a different program.
func (e *Evaluator) analyze(s *session.Session, src *string, cached *bool, from int) verdict {
	if e.checker == nil {
		return verdict{kind: verdictUnavailable}
	}
	var spent time.Duration
	defer func() { traceDur("typecheck", spent) }()

	// Each pass can teach something that changes the rendering, so the check
	// repeats. The bound is small and fixed: a disagreement between checker
	// and renderer must not be able to spin.
	for range 3 {
		t0 := time.Now()
		res, err := e.checker.Check([]byte(*src))
		spent += time.Since(t0)
		if err != nil {
			return verdict{kind: verdictUnavailable}
		}

		if len(res.Errs) > 0 {
			// A call with no results cannot be an argument to the printer.
			// v1 discovered this by failing a build; the checker knows it for
			// free, and remembers the callee so it is never asked twice.
			if e.markVoid(s, res, *src) {
				if !e.rerender(s, src, cached, from) {
					return verdict{kind: verdictUnavailable}
				}
				continue
			}
			// The cached import set may simply not cover this line yet.
			if *cached && importFixable(s, res.Errs) {
				resolved, rerr := e.writeResolved(s, filepath.Join(e.dir, "main.go"), from)
				if rerr != nil {
					return verdict{kind: verdictUnavailable}
				}
				*src, *cached = resolved, false
				continue
			}
			// An import the checker could not read is the checker's own
			// limitation, not the user's mistake: a host package that does
			// not compile has no export data, and go/types reports that as
			// "could not import X". Rejecting the program on those grounds
			// would make the checker an authority over a failure it cannot
			// describe — invariant 5 — and the message it produces names a
			// package rather than the line that is actually wrong. The build
			// says which, so fall through to it.
			if importUnreadable(res.Errs) {
				return verdict{kind: verdictUnavailable}
			}
			return verdict{kind: verdictErrors, diag: diagText(res.Errs)}
		}

		e.markArity(s, res)
		if out, ok := e.constant(s, res, from); ok {
			return verdict{kind: verdictConst, output: out}
		}
		return verdict{kind: verdictValid}
	}
	return verdict{kind: verdictUnavailable}
}

// markVoid marks every entry the checker found to be a call with no results.
func (e *Evaluator) markVoid(s *session.Session, res *check.Result, src string) bool {
	voids := res.VoidCalls(render.PrintFunc)
	if len(voids) == 0 {
		return false
	}
	texts := make([]string, 0, len(voids))
	for _, ex := range voids {
		texts = append(texts, exprText(res.Fset, src, ex))
	}
	return e.markNoValue(s, texts)
}

// rerender re-emits main.go after the session has been amended.
func (e *Evaluator) rerender(s *session.Session, src *string, cached *bool, from int) bool {
	next, c, err := e.write(s, from)
	if err != nil {
		return false
	}
	*src, *cached = next, c
	return true
}

// constant answers a compile-time constant without building or running.
//
// It applies only when the newest entry is the expression being printed. For
// any other trailing entry the program's output comes from somewhere else —
// rendering mutes everything before the newest entry, so an older print call
// contributes nothing and must not be mistaken for this line's answer.
func (e *Evaluator) constant(s *session.Session, res *check.Result, from int) (string, bool) {
	// An earlier entry that panics or exits non-zero must still do so. Only a
	// session whose last run was clean can have a line skipped.
	if !e.healthy {
		return "", false
	}
	n := len(s.Entries)
	if n == 0 {
		return "", false
	}
	// A batch prints every entry from the boundary on, not just the newest, so
	// a constant answer for the newest alone would swallow the others' output.
	// Batches always run.
	if from != n-1 {
		return "", false
	}
	if last := s.Entries[n-1]; last.Kind != session.KindExpr || last.NoValue {
		return "", false
	}
	tail, ok := res.Printed(render.PrintFunc)
	if !ok || !tail.Const {
		return "", false
	}
	return constResult(tail.TV.Type, tail.TV.Value)
}

// exprText recovers an expression's source text by offset. Slicing the input
// is exact, where re-printing the AST would introduce gofmt's own spacing.
func exprText(fset *token.FileSet, src string, e ast.Expr) string {
	lo := fset.Position(e.Pos()).Offset
	hi := fset.Position(e.End()).Offset
	if lo < 0 || hi > len(src) || lo >= hi {
		return ""
	}
	return src[lo:hi]
}

// diagText renders diagnostics the way the compiler prints them, so a single
// rewriter serves both the checked and the built path.
func diagText(errs []types.Error) string {
	var b strings.Builder
	for _, err := range errs {
		b.WriteString(err.Error())
		b.WriteByte('\n')
	}
	return b.String()
}

// importUnreadable reports whether any diagnostic is go/types failing to read
// an import, rather than a fault in the program.
//
// It is the whole set rather than any one of them: once an import is
// unreadable, every name that package supplies is also reported as undefined,
// so the other diagnostics are consequences of the same gap and none of them
// is evidence about the user's code.
func importUnreadable(errs []types.Error) bool {
	for _, err := range errs {
		if strings.Contains(err.Msg, "could not import ") {
			return true
		}
	}
	return false
}

// undefinedRe captures the name go/types could not resolve.
var undefinedRe = regexp.MustCompile(`undefined: ([A-Za-z_][A-Za-z0-9_]*)`)

// importFixable reports whether re-running goimports could plausibly fix these
// diagnostics. Only an unresolved package qualifier can be: goimports adds an
// import for pkg.Sym, and nothing it does will ever resolve a bare misspelled
// identifier.
//
// The distinction is worth drawing because the two look identical in the
// message — go/types says "undefined: strings" for a missing import and
// "undefined: undefinedThing" for a typo — and guessing wrong costs ~135ms to
// learn nothing, on exactly the mistake a person makes most while learning.
func importFixable(s *session.Session, errs []types.Error) bool {
	quals := map[string]bool{}
	for _, en := range s.Entries {
		for _, q := range render.Qualifiers(en.Src) {
			if render.Synthetic(q) {
				continue
			}
			quals[q] = true
		}
	}
	for _, err := range errs {
		text := err.Error()
		m := undefinedRe.FindStringSubmatch(text)
		if m == nil {
			// Anything else — an orphaned import, a package that moved — is
			// still worth a full resolve.
			if importProblem(text) {
				return true
			}
			continue
		}
		if quals[m[1]] {
			return true
		}
	}
	return false
}

// markArity records how many values the newest printed entry yields, so a
// later line addressing it as _N can be refused with its own name rather than
// with the compiler's "assignment mismatch" against a line the user did not
// just type.
//
// Like markVoid this writes back onto the entry, but from a clean check rather
// than a failed one. When the checker is unavailable it simply never runs, and
// the compiler reports the mismatch instead — a worse message, never a wrong
// answer.
func (e *Evaluator) markArity(s *session.Session, res *check.Result) {
	n := len(s.Entries)
	if n == 0 {
		return
	}
	last := &s.Entries[n-1]
	if last.Kind != session.KindExpr || last.NoValue {
		return
	}
	if tail, ok := res.Printed(render.PrintFunc); ok && tail.Values > 0 {
		last.Values = tail.Values
	}
}
