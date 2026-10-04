package repl

import (
	"strconv"
	"strings"

	"github.com/sandboxws/gluon/internal/inspect"
	"github.com/sandboxws/gluon/internal/pretty"
	"github.com/sandboxws/gluon/internal/session"
)

// diff is :diff — what differs between two values, and where.
//
// Two values printed one after another are diffed by eye, which is the wrong
// tool for two structs of a dozen fields or two slices that differ at index 40.
// gluon already holds both as structured trees, so the comparison happens in
// gluon's own process: no new child code, no new imports, and nothing about it
// visible in :src.
//
// Both operands are evaluated as one expression, the way :slice does it. Two
// evaluations would build twice, and worse, would run the session's replay
// twice — so a value derived from anything non-deterministic would differ for
// reasons that have nothing to do with the comparison.
func (c *Core) diff(arg string) Result {
	const usage = "usage: :diff <a>, <b>   e.g. :diff want, got"
	names := splitTop(arg)
	switch {
	case len(names) == 0:
		return Result{Out: usage, Err: true}
	case len(names) == 1:
		return Result{Out: "error: :diff compares two expressions; " + names[0] +
			" is one — " + usage, Err: true}
	case len(names) > 2:
		return Result{Out: "error: :diff compares two expressions; got " +
			strconv.Itoa(len(names)), Err: true}
	}

	for _, name := range names {
		target, err := c.resolve(name)
		if err != nil {
			return Result{Out: "error: " + err.Error(), Err: true}
		}
		switch {
		case target.IsType:
			return Result{Out: "error: " + name + " is a type, not a value — " +
				":impl and :layout compare types", Err: true}
		case target.Void:
			return Result{Out: "error: " + name + " has no value to compare", Err: true}
		case len(target.Tuple) > 1:
			return Result{Out: "error: :diff compares one value each; " + name +
				" returns " + strconv.Itoa(len(target.Tuple)), Err: true}
		}
	}

	// The pair comes back as a two-result func literal, so the encoder
	// describes both from one run. any is what every printed value is boxed
	// into on the ordinary path too, so nothing is lost by naming it here.
	entry := session.Entry{
		Kind:   session.KindExpr,
		Src:    "func() (any, any) { return " + names[0] + ", " + names[1] + " }()",
		Values: 2,
	}
	res, err := c.ev.EvalTransient(c.sess, entry)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	userOut, vals := pretty.Parse(res.Output)
	if len(vals) != 2 {
		out := strings.TrimRight(userOut, "\n")
		if out == "" {
			out = "error: the two values did not come back to compare"
		}
		return Result{Out: out, Err: true}
	}

	d := inspect.DiffValues(vals[0], vals[1])
	out := inspect.PlainDiff(names, d)
	if c.Rich {
		out = inspect.RenderDiff(names, d, c.Styles)
	}
	// Result.Out always carries the linear form, so a pipe loses nothing when
	// the modal opens — invariant 19.
	return Result{Out: out, Modal: pageable("diff "+names[0]+", "+names[1], out)}
}
