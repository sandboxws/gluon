package repl

// :trace — where an expression is being evaluated from.
//
// Rails has binding.pry, which pauses a live process and hands you a shell.
// gluon cannot: each line compiles into a program that runs and exits, so
// there is nothing to pause. What it can do is capture the call stack while the
// expression is evaluated and report it in the session's own terms — which
// entry each frame belongs to, and the line the user actually typed there —
// rather than the temp path the program was generated into.
//
// The limit is worth naming, because the output shows it: the stack is captured
// where the expression is evaluated, so it holds the expression's callers, not
// its callees. A function the expression is about to call has not been entered
// yet, and by the time it returns its frame is gone. For what happens
// underneath a call, the answer is to leave the REPL: :save -debug writes the
// module and names the dlv command that steps through it.

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sandboxws/gluon/internal/inspect"
	"github.com/sandboxws/gluon/internal/pretty"
	"github.com/sandboxws/gluon/internal/render"
	"github.com/sandboxws/gluon/internal/session"
)

// trace is :trace — the expression's value, and the call stack it was evaluated
// on. It runs through EvalTransient like :bench and :err, which is what keeps
// the session's entries and imports untouched (invariant 14): the rewrite pulls
// in runtime and fmt, and both are snapshotted and restored.
func (c *Core) trace(arg string) Result {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return Result{Out: "usage: :trace <expression>   e.g. :trace f(1)", Err: true}
	}
	target, err := c.resolve(arg)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	switch {
	case target.IsType:
		return Result{Out: "error: that is a type, not something to evaluate", Err: true}
	case len(target.Tuple) > 1:
		return Result{Out: "error: :trace takes one value; " + arg + " returns " +
			strconv.Itoa(len(target.Tuple)), Err: true}
	}

	entry := session.Entry{Kind: session.KindExpr, Src: traceSource(arg, target.Void)}
	res, err := c.ev.EvalTransient(c.sess, entry)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}

	userOut, vals := pretty.Parse(res.Output)
	// A void call returns the frames alone; anything with a value returns the
	// pair, and the printer renders a tuple as two values.
	want := 2
	if target.Void {
		want = 1
	}
	if len(vals) != want {
		out := strings.TrimRight(userOut, "\n")
		if out == "" {
			out = "error: no stack came back"
		}
		return Result{Out: out, Err: true}
	}

	frames := c.traceFrames(vals[want-1].Items)
	var b strings.Builder
	// What the expression itself printed comes first, as it does on any other
	// line. A void call is often traced for exactly that output.
	if out := strings.TrimRight(userOut, "\n"); out != "" {
		b.WriteString(out + "\n")
	}
	if !target.Void {
		b.WriteString(c.Render(vals[:1]) + "\n")
	}
	b.WriteString("\n" + inspect.RenderTrace(arg, frames, c.Styles, c.Rich))
	return Result{Out: b.String()}
}

// traceSource is the rewrite. It is deliberately small: the capture is three
// statements around the expression, and every one of them is something a line
// you could have typed would do.
//
// runtime.Callers rather than runtime.Stack because the frames are wanted as
// data — a formatted traceback would have to be parsed back, and the file names
// in it are the ones this exists to translate. The fields travel \x1f-separated
// the way :err's chain does, which keeps the child describing and gluon
// formatting.
func traceSource(arg string, void bool) string {
	head := "\t__gluonPC := make([]uintptr, 64)\n" +
		"\t__gluonN := runtime.Callers(1, __gluonPC)\n"
	tail := "\tvar __gluonSt []string\n" +
		"\t__gluonIt := runtime.CallersFrames(__gluonPC[:__gluonN])\n" +
		"\tfor {\n" +
		"\t\t__gluonF, __gluonMore := __gluonIt.Next()\n" +
		"\t\t__gluonSt = append(__gluonSt, fmt.Sprintf(\"%s\\x1f%s\\x1f%d\", __gluonF.Function, __gluonF.File, __gluonF.Line))\n" +
		"\t\tif !__gluonMore {\n" +
		"\t\t\tbreak\n" +
		"\t\t}\n" +
		"\t}\n"
	if void {
		return "func() []string {\n" + head + "\t" + arg + "\n" + tail +
			"\treturn __gluonSt\n}()"
	}
	return "func() (any, []string) {\n" + head + "\t__gluonV := " + arg + "\n" + tail +
		"\treturn __gluonV, __gluonSt\n}()"
}

// traceFrames turns the captured frames into the session's own terms, dropping
// the ones that belong to gluon.
func (c *Core) traceFrames(items []pretty.Value) []inspect.TraceFrame {
	var out []inspect.TraceFrame
	for _, it := range items {
		parts := strings.SplitN(it.Repr, "\x1f", 3)
		if len(parts) != 3 {
			continue
		}
		fn, file, num := parts[0], parts[1], parts[2]
		if harnessFrame(fn) {
			continue
		}
		line, _ := strconv.Atoi(num)
		if f, ok := c.sessionFrame(fn, file, line); ok {
			out = append(out, f)
			continue
		}
		out = append(out, inspect.TraceFrame{Fn: fn, Where: file + ":" + num})
	}
	return out
}

// sessionFrame maps a frame in the session's own code onto the entry it came
// from. The runtime resolves the same //line directives the compiler does, so
// a frame there already names a synthetic file rather than the temp path — the
// mapping is render.EntryOf's, the one mapTraceback uses for panics.
//
// DeclLineShift is applied for the same reason it is there: gofmt hoists the
// directive off a declaration's own line, putting the code one line later.
// render.ColumnShift is its counterpart for the statement case and has nothing
// to apply to here — runtime.Frame carries a line and no column.
func (c *Core) sessionFrame(fn, file string, line int) (inspect.TraceFrame, bool) {
	i, ok := render.EntryOf(filepath.Base(file))
	if !ok || i >= len(c.sess.Entries) {
		return inspect.TraceFrame{}, false
	}
	en := c.sess.Entries[i]
	if en.Kind == session.KindDecl {
		line -= render.DeclLineShift
	}
	f := inspect.TraceFrame{Fn: fn, Where: "entry " + strconv.Itoa(i+1)}
	src := strings.Split(en.Src, "\n")
	if line >= 1 && line <= len(src) {
		f.Src = strings.TrimSpace(src[line-1])
	}
	return f, true
}

// harnessFrame reports that a frame belongs to gluon rather than to the user.
//
// Three shapes, and each is gluon's own: the wrapper :trace synthesises, which
// is a literal inside the generated main; that main itself, which is the
// program render writes around every session; and the injected value printer,
// whose identifiers are all __gluon-prefixed for exactly this kind of reason.
// The runtime frames under main go too — they are the same three lines under
// every Go program, and they name paths in GOROOT that say nothing about the
// session. A function the user declared is main.<name> and is kept.
func harnessFrame(fn string) bool {
	switch {
	case fn == "main.main", strings.HasPrefix(fn, "main.main.func"):
		return true
	case strings.HasPrefix(fn, "main.__gluon"):
		return true
	case strings.HasPrefix(fn, "runtime."):
		return true
	}
	return false
}
