package eval

import (
	"strings"

	"github.com/sandboxws/gluon/internal/redact"
	"github.com/sandboxws/gluon/internal/render"
	"github.com/sandboxws/gluon/internal/session"
)

// EvalLive runs one transient entry against a live resource.
//
// It is EvalTransient with the result cache switched off in both directions,
// plus the extra imports and the child-only environment such a resource needs.
// The cache is keyed on program text alone, so without this the same query
// would return the same rows forever — see evalOpts.live.
//
// Invariant 14 still holds: imports, resolved and healthy are snapshotted
// exactly as EvalTransient does, and the entry is popped. That matters more
// here than it does for :bench. The driver arrives as a blank import that no
// line names, so leaving it in e.imports would make the next ordinary line
// write an import it does not use, fail to build, and recover only by paying a
// full goimports pass.
//
// redact is what must not appear in the child's output. The source never holds
// the secret — it reads an environment variable by name — so the child's own
// stdout and stderr are the only surface left, and a driver that echoes its
// connection string on a parse failure is a real thing.
func (e *Evaluator) EvalLive(s *session.Session, entry session.Entry, imports []render.ImportSpec, env, redact []string) (Result, error) {
	saved, resolved, healthy := e.imports, e.resolved, e.healthy
	defer func() { e.imports, e.resolved, e.healthy = saved, resolved, healthy }()

	s.Append(entry)
	defer s.Pop()
	return e.evalWith(s, evalOpts{live: true, imports: imports, env: env, redact: redact})
}

// EvalFresh evaluates the whole session with the result cache switched off in
// both directions.
//
// It is what :refresh needs and EvalLive's reasoning applies unchanged: the
// program text is byte-identical to the last run — that is what a replay is —
// so a cached lookup returns the answer from before the source changed, and a
// cached store poisons every later run of the same text. The alternative, a
// nonce written into the source to make the text unique, is on ROADMAP.md's
// permanently rejected list with the measurements that put it there.
//
// Nothing is snapshotted here, unlike EvalLive: this is the session's own
// program with its own imports, not a transient question asked about it.
func (e *Evaluator) EvalFresh(s *session.Session) (Result, error) {
	return e.evalWith(s, evalOpts{live: true})
}

// minSecret is the shortest string mask will hide.
//
// Masking a two-character password would shred unrelated output — every "ab" in
// a result set would become ***, which destroys the answer while pretending to
// protect it. A password that short is not the failure this defends against.
//
// The floor and the marker come from internal/redact, which bounds :env and
// :conf by the same number for the same reason. Two spellings of *** would be
// two things a reader has to learn.
const minSecret = redact.MinSecret

// mask replaces every secret in s with ***.
//
// It is applied to what the child printed, which is the one place a secret can
// still surface: a driver's own error sometimes echoes the connection string it
// could not parse, and that string is not gluon's to leak.
func mask(s string, secrets []string) string {
	if len(secrets) == 0 {
		return s
	}
	for _, sec := range secrets {
		if len(sec) < minSecret {
			continue
		}
		s = strings.ReplaceAll(s, sec, redact.Mask)
	}
	return s
}
