package repl

import (
	"fmt"
	"os"

	"github.com/sandboxws/gluon/internal/plugin"
	"github.com/sandboxws/gluon/internal/pretty"
	"github.com/sandboxws/gluon/internal/render"
	"github.com/sandboxws/gluon/internal/session"
)

// The half of a live plugin command that has to be gluon's.
//
// :http does all of this inline because it is a builtin and can. A plugin
// cannot: Rewrite sees its argument and nothing else. So the plugin declares
// which variables its program reads and gluon does the reading, which is what
// keeps one definition of "the child gets the secret and nothing else does"
// rather than one per plugin — invariants 24 and 25.

// runLive answers a command that dials something.
//
// EvalLive rather than EvalTransient for two independent reasons, either
// sufficient. The result cache is keyed on program text alone, so the same
// call twice would answer with whatever it first saw — the failure :query
// documents. And it is what carries a child-only environment and applies mask
// to what comes back.
func (c *Core) runLive(from string, lc *plugin.LiveCall, arg string) Result {
	call, err := lc.Plan(arg)
	if err != nil {
		return Result{Out: err.Error(), Err: true}
	}
	env, secrets, err := resolveRefs(call.Refs)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	entry, err := session.Classify(call.Source)
	if err != nil {
		return Result{Out: "error: " + from + " produced source gluon cannot parse: " +
			err.Error(), Err: true}
	}

	res, err := c.ev.EvalLive(c.sess, entry, importSpecs(call.Imports), env, secrets)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	if call.Report == nil {
		return Result{Out: c.format(res.Output)}
	}

	out, vals := pretty.Parse(res.Output)
	ans := call.Report(out, vals)
	if ans.Text != "" || ans.Failed {
		return Result{Out: ans.Text, Err: ans.Failed}
	}
	// The value is the answer, so it goes through the ordinary value path and
	// every renderer that applies to a value anywhere applies here — including
	// pretty.Plain through a pipe, which no plugin may reach (invariant 21).
	if len(vals) == 0 {
		return Result{Out: "error: the call produced no answer", Err: true}
	}
	return Result{Out: c.Render(vals)}
}

// resolveRefs looks up every variable a plan named, and refuses when one has
// no value.
//
// It returns what the child's environment gets and what must not appear in the
// child's output. Both come from this one walk: a second lookup is how a value
// ends up masked in one place and not the other. resolveHTTPRefs states the
// same rule for :http, over headers rather than a name list.
func resolveRefs(refs []string) (env, secrets []string, err error) {
	seen := map[string]bool{}
	for _, name := range refs {
		if seen[name] {
			continue
		}
		seen[name] = true
		val, ok := os.LookupEnv(name)
		switch {
		case !ok:
			return nil, nil, fmt.Errorf(
				"$%s is not set — gluon reads it from its own environment, and will not "+
					"call with it empty", name)
		case val == "":
			return nil, nil, fmt.Errorf(
				"$%s is set to nothing — an empty credential reads at the far end as a "+
					"wrong one rather than a missing one", name)
		}
		env = append(env, name+"="+val)
		secrets = append(secrets, val)
	}
	return env, secrets, nil
}

// importSpecs is a plugin's import list in the evaluator's own shape.
func importSpecs(imports []plugin.Import) []render.ImportSpec {
	out := make([]render.ImportSpec, 0, len(imports))
	for _, im := range imports {
		out = append(out, render.ImportSpec{Name: im.Name, Path: im.Path})
	}
	return out
}
