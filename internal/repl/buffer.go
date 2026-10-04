package repl

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sandboxws/gluon/internal/eval"
	"github.com/sandboxws/gluon/internal/scratch"
	"github.com/sandboxws/gluon/internal/session"
	"github.com/sandboxws/gluon/internal/syntax"
)

// :buf — a multi-line buffer you edit and run as one evaluation.
//
// It is not :edit. :edit opens the session and replaces it; the buffer is
// somewhere to write code that has not run yet, and running it appends. The
// two answer different questions and neither is the other's flag.
//
// The buffer is a string on Core and a file on disk only while an editor has
// it. That order matters: the file is scaffolding, and the text is the thing.

// bufName is the file inside the scaffolded module that the editor opens.
const bufName = "main.go"

func (c *Core) buffer(arg string) Result {
	switch strings.TrimSpace(arg) {
	case "":
		return c.bufOpen()
	case "-show":
		return c.bufShow()
	case "-clear":
		return c.bufClear()
	case "-check":
		return c.bufCheck()
	case "-run":
		return c.bufRun()
	}
	return Result{Out: "usage: :buf [-check|-run|-show|-clear]", Err: true}
}

// bufOpen scaffolds the file and asks the driver to hand over the terminal.
//
// Core opens nothing itself: it runs on the evaluation goroutine and must not
// touch a terminal it cannot see. Edit names the file and EditThen names the
// line to submit when the editor exits, which is a line the user could have
// typed.
func (c *Core) bufOpen() Result {
	path, err := c.bufWrite()
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	// No Out, exactly as :edit sets none: both drivers return on Edit before
	// they would print one, and a driver with no terminal answers with the
	// refusal instead. A line here would be one nothing reads.
	//
	// EditLine is what closes the loop: a buffer that would not compile reopens
	// on the construct that would not, so edit-check-edit costs one keystroke
	// rather than a hunt. It is cleared by the answer that had nothing to say —
	// and then the cursor lands in the code region instead, because the obvious
	// place to type in a Go file is the bottom of main, which is exactly the
	// one place here that is not the buffer.
	line := c.bufErrLine
	if line == 0 {
		line = c.bufCodeLine
	}
	return Result{Edit: path, EditThen: ":buf -run", EditLine: line}
}

func (c *Core) bufShow() Result {
	if _, err := c.bufTake(); err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	if strings.TrimSpace(c.buf) == "" {
		return Result{Out: "the buffer is empty — :buf opens it"}
	}
	return Result{Out: c.buf, Lang: syntax.Go}
}

func (c *Core) bufClear() Result {
	if strings.TrimSpace(c.buf) == "" && c.bufDir == "" {
		return Result{Out: "the buffer is already empty"}
	}
	lines := len(strings.Split(strings.TrimRight(c.buf, "\n"), "\n"))
	if strings.TrimSpace(c.buf) == "" {
		lines = 0
	}
	c.buf = ""
	if c.bufDir != "" {
		// The module stays; the text in it does not. Keeping the directory is
		// what keeps an editor's undo history and marks pointing at the same
		// path across a session.
		os.Remove(filepath.Join(c.bufDir, bufName))
	}
	return Result{Out: "buffer cleared — " + plural(lines, "line") + " dropped"}
}

// bufCheck answers whether the buffer compiles against the session, without
// building it and without appending anything.
//
// It may never answer for the checker when the checker cannot answer for
// itself. Invariant 5: this is the one component that could reject a program
// the compiler would accept, so every failure path says so rather than
// guessing, and none of them decides whether :buf -run may proceed.
func (c *Core) bufCheck() Result {
	note, err := c.bufTake()
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	constructs, rest := session.SplitConstructsAt(c.buf)
	if len(constructs) == 0 && strings.TrimSpace(rest) == "" {
		return Result{Out: "the buffer is empty — nothing to check"}
	}

	var problems []string
	if note != "" {
		problems = append(problems, note)
	}
	if strings.TrimSpace(rest) != "" {
		problems = append(problems, bufUnfinished(c.buf, rest))
	}

	srcs := make([]string, 0, len(constructs))
	for _, cs := range constructs {
		srcs = append(srcs, cs.Src)
	}
	diags, err := c.ev.CheckBlock(c.sess, srcs)
	switch {
	case errors.Is(err, eval.ErrNoChecker):
		problems = append(problems, "the checker could not answer — :buf -run builds it instead")
	case err != nil:
		// A parse failure is a real answer and a cheaper one than the build,
		// so it reads as a problem rather than as the checker being absent.
		problems = append(problems, "error: "+err.Error())
	}
	c.bufErrLine = bufFirstLine(constructs, diags)
	for _, d := range diags {
		problems = append(problems, bufDiagLine(constructs, d))
	}

	if len(problems) == 0 {
		return Result{Out: plural(len(constructs), "construct") + ", no errors"}
	}
	// A note about context the user edited is not an error about their code.
	// It reports beside the diagnostics and, on its own, is not a failure.
	return Result{Out: strings.Join(problems, "\n"), Err: len(problems) > 1 || note == ""}
}

// bufRun evaluates the whole buffer as one evaluation.
//
// Through SubmitBatch, which is what a pasted block already goes through: one
// build for however many constructs, every one of them printing, and a batch
// that will not compile as a whole landed one construct at a time so the
// failure reports against the construct that owns it.
//
// It does not consult bufCheck. The compiler decides, exactly as it does for a
// line typed at the prompt — which is why there is no flag to override it.
func (c *Core) bufRun() Result {
	note, err := c.bufTake()
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	constructs, rest := session.SplitConstructsAt(c.buf)
	if len(constructs) == 0 {
		if strings.TrimSpace(rest) != "" {
			return Result{Out: joinLines(note, bufUnfinished(c.buf, rest)), Err: true}
		}
		// The note travels with this answer rather than being dropped by it.
		// "nothing ran" plus "what you wrote was outside the regions" is the
		// one pair of facts that explains the other, and reporting the first
		// without the second is how somebody concludes the buffer is broken.
		return Result{Out: joinLines(note, "the buffer is empty — nothing to run"), Err: note != ""}
	}

	srcs := make([]string, 0, len(constructs))
	for _, cs := range constructs {
		srcs = append(srcs, cs.Src)
	}
	res := c.SubmitBatch(srcs)
	c.bufErrLine = 0
	if res.Err {
		// The compiler has already said what is wrong and its answer is the one
		// that reports. The checker is asked only *where*, so the next :buf can
		// open there — a position, not a verdict. It may decline, in which case
		// the buffer opens at the top, which is where it opened before.
		if diags, cerr := c.ev.CheckBlock(c.sess, srcs); cerr == nil {
			c.bufErrLine = bufFirstLine(constructs, diags)
		}
	}
	if note != "" {
		res.Out = joinLines(note, res.Out)
	}

	// The unfinished tail is reported after the constructs that did run, not
	// instead of them: the good half of a buffer is still worth having, and
	// swallowing the tail would be the buffer quietly losing what was typed.
	if strings.TrimSpace(rest) != "" {
		res.Out = joinLines(res.Out, bufUnfinished(c.buf, rest))
		res.Err = true
	}
	return res
}

// joinLines puts two answers on their own lines, skipping whichever is empty.
func joinLines(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "\n" + b
}

// bufTake reads the buffer back from the file an editor was given, when there
// is one.
//
// A file that cannot be read leaves the buffer as it was: text the user typed
// is not something to drop because a stat failed. A file whose markers are
// gone is refused for the opposite reason — reading it anyway would replace
// what they wrote with the rendered session around it.
//
// note is what to say about edits outside the regions, empty when there were
// none. It reports rather than restores: putting the preamble back would be
// gluon editing a file somebody has open, and the preamble is regenerated from
// the session on the next open anyway.
func (c *Core) bufTake() (note string, err error) {
	if c.bufDir == "" {
		return "", nil
	}
	data, rerr := os.ReadFile(filepath.Join(c.bufDir, bufName))
	if rerr != nil {
		return "", nil
	}
	file := string(data)

	if !c.bufScaffold {
		c.buf = file
		return "", nil
	}

	decls, code, err := extractBufRegions(file)
	if err != nil {
		return "", err
	}
	c.buf = bufJoinRegions(decls, code)

	// Against the exact bytes handed over, not against a freshly rendered
	// preamble: regenerating would put a declaration written among the
	// statements into the other region, which changes both regions' line counts
	// and so the //line numbers around them — and the file would then differ
	// from itself for a reason the user did nothing about.
	if bufOutsideRegions(c.bufWritten) != bufOutsideRegions(file) {
		note = "note: an edit outside the two regions was not taken — the buffer is what lies between the markers"
	}
	return note, nil
}

// bufJoinRegions is the two regions as one buffer: declarations first, because
// that is the order render puts them in and the order the next open will write
// them back in.
func bufJoinRegions(decls, code string) string {
	switch {
	case strings.TrimSpace(decls) == "":
		return ensureNL(code)
	case strings.TrimSpace(code) == "":
		return ensureNL(decls)
	}
	return ensureNL(decls) + "\n" + ensureNL(code)
}

// bufOutsideRegions is every line of a scaffolded file that is not inside one
// of its two regions — the context, which is what has to be identical for the
// context to be unedited.
//
// By line range rather than by cutting the regions' text back out of the file:
// a region's content is followed by the newline that ended it, so removing the
// text leaves that newline behind, and an empty region and a two-line one then
// differ by a blank line that nobody typed. That reported an edit to the
// preamble every time somebody wrote in an empty region.
func bufOutsideRegions(file string) string {
	lines := strings.Split(file, "\n")
	m, err := bufMarkers(lines)
	if err != nil {
		return file
	}
	var out []string
	for i, l := range lines {
		if (i > m.declsAt && i < m.declsEnd) || (i > m.codeAt && i < m.codeEnd) {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// bufWrite puts the buffer where an editor can open it, creating the module on
// first use.
//
// What the editor gets is a file that compiles on its own, in a module of its
// own, because anything less makes the user's own language server useless on
// it: a fragment of statements is not Go, and a Go file with no go.mod resolves
// no imports. That is the whole argument for handing the terminal to an editor
// somebody has already configured — their gopls is the compile check, live,
// while they type.
//
// scratch.Write is the same scaffolder :save uses, so the module a buffer opens
// in and the module a saved session opens in are the same kind of thing.
// Runtime carries the injected printer, which the rendered session calls; Host
// nests the go.mod under an attached project so its internal packages resolve
// exactly as the session's own code sees them.
func (c *Core) bufWrite() (string, error) {
	if c.bufDir == "" {
		dir, err := os.MkdirTemp("", "gluon-buf-")
		if err != nil {
			return "", err
		}
		c.bufDir = dir
	}

	body, scaffolded, err := c.bufBody()
	if err != nil {
		return "", err
	}
	c.bufScaffold = scaffolded
	c.bufWritten = body
	if !scaffolded {
		path := filepath.Join(c.bufDir, bufName)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return "", err
		}
		return path, nil
	}
	return scratch.Write(c.bufDir, scratch.Options{
		Topic:   "buffer",
		Body:    body,
		Runtime: true,
		Host:    c.host(),
	})
}

// bufBody assembles the file, and reports whether it is the scaffolded shape.
//
// A Core with no evaluator has no session to render and no module to render it
// into — a driver the tests build and nothing production runs. It gets the
// buffer's own text and nothing around it, which is honest: there is no session
// for a language server to resolve against either.
func (c *Core) bufBody() (body string, scaffolded bool, err error) {
	if c.ev == nil {
		return c.buf, false, nil
	}
	src, err := c.ev.Render(c.sess)
	if err != nil {
		return "", false, err
	}
	decls, code := c.bufRegions()
	body, codeLine, err := insertBufRegions(src, decls, code)
	if err != nil {
		return "", false, err
	}
	c.bufCodeLine = codeLine
	return body, true, nil
}

// bufRegions sorts the buffer's constructs into the two places the file has for
// them: declarations above main, everything else inside it.
//
// Go will not take a func declaration inside func main, and a file that asked
// it to would have a language server painting every one of them red — which is
// the one thing the scaffold exists to prevent. The split decides nothing about
// what the code means: on the way back both regions are handed to
// session.Classify, which answers from each construct's own source, exactly as
// it does for a line typed at the prompt.
//
// It does mean a declaration written among the statements moves above them on
// the next open. That is where gluon puts it anyway — :src has always shown it
// there — and the move happens once, because a buffer that has been through
// here is already in that order.
//
// A construct that will not classify goes with the statements. Someone is
// mid-edit; guessing that broken source is a declaration would move it, and
// moving code somebody is in the middle of writing is worse than leaving it.
func (c *Core) bufRegions() (decls, code string) {
	var ds, cs []string
	constructs, rest := session.SplitConstructsAt(c.buf)
	for _, con := range constructs {
		if e, err := session.Classify(con.Src); err == nil && e.Kind == session.KindDecl {
			ds = append(ds, con.Src)
			continue
		}
		cs = append(cs, con.Src)
	}
	if strings.TrimSpace(rest) != "" {
		cs = append(cs, strings.TrimRight(rest, "\n"))
	}
	return strings.Join(ds, "\n\n"), strings.Join(cs, "\n")
}

// bufDiagLine puts a diagnostic on the line of the buffer the user wrote.
//
// The construct index is what makes this possible: CheckBlock appended one
// entry per construct, so an entry's own line plus the line that construct
// started on is the line in the buffer, with no arithmetic anywhere else.
func bufDiagLine(constructs []session.Construct, d eval.BlockDiag) string {
	where := "buffer"
	if d.Construct >= 0 && d.Construct < len(constructs) && d.Line > 0 {
		line := constructs[d.Construct].Line + d.Line - 1
		where += ":" + strconv.Itoa(line)
		if d.Col > 0 {
			where += ":" + strconv.Itoa(d.Col)
		}
	} else if d.Construct < 0 {
		// Against the session rather than the block. It has no buffer line to
		// sit on, and claiming one would point at code that is not wrong.
		where = "session"
	}
	out := where + ": " + d.Msg
	if d.Quote != "" {
		out += "\n" + d.Quote
	}
	return out
}

// bufMark is one diagnostic placed on a document line, for a view with a gutter
// to draw in. The text is bufDiagLine's, so what the gutter marks and what
// :buf -check prints are the same sentence about the same line.
type bufMark struct {
	// Line is 1-based in the document, or 0 for a diagnostic that belongs to
	// no line of it — one against the session rather than the block.
	Line int
	Msg  string
}

// CheckDocument type-checks a whole document, without running it and without
// changing the session that is loaded.
//
// replaces is the one thing that differs between the surfaces, and it decides
// what the document is checked *against*. :buf adds to the session, so it is
// checked against it — which is what :buf -check has always done. :edit and a
// scratchpad replace the session, so checking those against the entries
// currently loaded would report every name in them as declared twice.
//
// Invariant 5 is kept by deferring to CheckBlock entirely: an error means the
// checker could not answer, and the caller must report neither verdict.
func (c *Core) CheckDocument(text string, replaces bool) ([]bufMark, error) {
	if c.ev == nil {
		return nil, eval.ErrNoChecker
	}
	against := c.sess
	if !replaces {
		against = &session.Session{}
	}
	constructs, rest := session.SplitConstructsAt(text)
	srcs := make([]string, 0, len(constructs))
	for _, cs := range constructs {
		srcs = append(srcs, cs.Src)
	}
	diags, err := c.ev.CheckBlock(against, srcs)
	if err != nil {
		return nil, err
	}
	marks := make([]bufMark, 0, len(diags)+1)
	for _, d := range diags {
		m := bufMark{Msg: bufDiagLine(constructs, d)}
		if d.Construct >= 0 && d.Construct < len(constructs) && d.Line > 0 {
			m.Line = constructs[d.Construct].Line + d.Line - 1
		}
		marks = append(marks, m)
	}
	// A document ending inside a construct is not a type error and the checker
	// never sees it — the split hands it back instead. It is still the first
	// thing wrong with the document, so it is reported here rather than left
	// for the compiler to find on the way out.
	if strings.TrimSpace(rest) != "" {
		marks = append(marks, bufMark{
			Line: len(strings.Split(text, "\n")) - len(strings.Split(rest, "\n")) + 1,
			Msg:  bufUnfinished(text, rest),
		})
	}
	return marks, nil
}

// bufUnfinished reports a construct the buffer ends in the middle of.
func bufUnfinished(buf, rest string) string {
	line := len(strings.Split(buf, "\n")) - len(strings.Split(rest, "\n")) + 1
	first := strings.SplitN(strings.TrimLeft(rest, "\n"), "\n", 2)[0]
	return fmt.Sprintf("buffer:%d: unfinished — the buffer ends inside this construct\n    %s",
		line, strings.TrimRight(first, " \t"))
}

// The buffer's two regions, and the markers that bound them.
//
// Two because Go will not take a func declaration inside func main. Bounded at
// both ends rather than "from the marker to whatever comes next", because
// read-back has to be able to refuse: a file whose markers have been mangled
// must report that rather than guess, and a guess here would either swallow the
// rendered session into the buffer or silently drop what somebody wrote.
const (
	bufDeclsMarker = "//gluon:decls"
	bufCodeMarker  = "//gluon:code"
	bufEndMarker   = "//gluon:end"
)

const (
	bufDeclsNote = bufDeclsMarker + "  —  functions, types and constants you write go here"
	bufCodeNote  = bufCodeMarker + "  —  statements and expressions go here"
	bufEndNote   = bufEndMarker + "  —  everything outside these two regions is context"
)

// errBufMarkers is what a mangled file reports. The session is never touched on
// this path and the file is left exactly where it is.
var errBufMarkers = errors.New("the buffer's " + bufDeclsMarker + " / " + bufCodeMarker +
	" markers are missing or out of order — the file was left alone, and nothing was run")

// insertBufRegions cuts the two editable regions into the rendered program.
//
// The insertion points come from the parser rather than from searching the text
// for "func main": the rendered program is full of line directives and printer
// wrappers, and a string search would eventually find one of them. Offsets are
// raw byte offsets — token.File.Offset ignores line directives, where
// fset.Position deliberately honours them — so the arithmetic is over the bytes
// actually on disk.
//
// Each region opens with a //line directive pointing back at this file. Without
// it the user's own code inherits the numbering of whichever gluon-in-N.go
// directive came last, and their editor reports their mistakes at a line of a
// file that does not exist — which would cost exactly the thing the scaffold is
// for. gofmt leaves a //line at column 1 where it is, so format-on-save in the
// user's editor does not undo this.
//
// The result is deliberately not re-formatted. The rendered half arrives from
// render.Format already, and running gofmt over the whole thing again would
// move the directives relative to the code they govern — which is what
// render.ColumnShift and render.DeclLineShift exist to compensate for, and not
// a correction worth having to make twice.
func insertBufRegions(src, decls, code string) (body string, codeLine int, err error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, bufName, src, parser.SkipObjectResolution)
	if err != nil {
		return "", 0, err
	}
	var main *ast.FuncDecl
	for _, d := range file.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "main" && fn.Body != nil {
			main = fn
		}
	}
	if main == nil {
		// Invariant 2 says one is always emitted, even empty, so this is a
		// renderer that changed rather than a session that has no code.
		return "", 0, errors.New("the rendered session has no func main to open a buffer in")
	}
	tf := fset.File(file.Package)
	declAt, codeAt := tf.Offset(main.Pos()), tf.Offset(main.Body.Rbrace)

	region := func(note, body string) string {
		if strings.TrimSpace(body) == "" {
			// An empty region is otherwise two adjacent comment lines with no
			// line between them to put a cursor on, and the obvious place to
			// type — the bottom of main — is outside it. One blank line is
			// where the cursor lands and where typing works.
			body = "\n"
		}
		// The directive is a placeholder until the line it sits on is known;
		// rewriting a number never changes a line count, so one pass to build
		// and one to number is enough.
		return "//line " + bufName + ":0\n" + note + "\n" + ensureNL(body) + bufEndNote + "\n"
	}

	var b strings.Builder
	b.WriteString(src[:declAt])
	b.WriteString(region(bufDeclsNote, decls))
	b.WriteString("\n")
	b.WriteString(src[declAt:codeAt])
	b.WriteString(region(bufCodeNote, code))
	b.WriteString(src[codeAt:])
	return numberBufRegions(b.String())
}

// numberBufRegions points each region's line directive at the line after
// itself, now that the assembled file says which line that is, and reports the
// first line of the code region so an editor can open with the cursor already
// where typing works.
func numberBufRegions(src string) (string, int, error) {
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		if line == "//line "+bufName+":0" {
			// A directive on line i+1 governs the line after it, which is i+2.
			lines[i] = "//line " + bufName + ":" + strconv.Itoa(i+2)
		}
	}
	m, err := bufMarkers(lines)
	if err != nil {
		return "", 0, err
	}
	return strings.Join(lines, "\n"), m.codeAt + 2, nil
}

// extractBufRegions reads the two regions back out of a file an editor has had.
//
// Everything outside them is context. It is dropped, and the caller says so
// when it has changed, because a preamble edit that vanished without a word is
// the one thing this shape must not do quietly.
func extractBufRegions(file string) (decls, code string, err error) {
	lines := strings.Split(file, "\n")
	m, err := bufMarkers(lines)
	if err != nil {
		return "", "", err
	}
	join := func(from, to int) string {
		return strings.TrimRight(strings.Join(lines[from+1:to], "\n"), " \t\n")
	}
	return join(m.declsAt, m.declsEnd), join(m.codeAt, m.codeEnd), nil
}

// bufMarks is where a scaffolded file's four markers sit.
type bufMarks struct{ declsAt, declsEnd, codeAt, codeEnd int }

// bufMarkers finds them, in order.
//
// Scanning forward from each one is what makes "out of order" a refusal for
// free: a code marker above the declarations one is never reached. Markers are
// matched with their indentation trimmed, because the ones inside main are
// ordinary comments and an editor that formats on save will have indented them
// — only the line directives have to stay at column 1, and gofmt already
// leaves those where they are.
func bufMarkers(lines []string) (bufMarks, error) {
	at := func(from int, marker string) int {
		for i := from; i < len(lines); i++ {
			if strings.HasPrefix(strings.TrimSpace(lines[i]), marker) {
				return i
			}
		}
		return -1
	}
	var m bufMarks
	if m.declsAt = at(0, bufDeclsMarker); m.declsAt < 0 {
		return m, errBufMarkers
	}
	if m.declsEnd = at(m.declsAt+1, bufEndMarker); m.declsEnd < 0 {
		return m, errBufMarkers
	}
	if m.codeAt = at(m.declsEnd+1, bufCodeMarker); m.codeAt < 0 {
		return m, errBufMarkers
	}
	if m.codeEnd = at(m.codeAt+1, bufEndMarker); m.codeEnd < 0 {
		return m, errBufMarkers
	}
	return m, nil
}

// bufFirstLine is the buffer line of the first diagnostic that has one, or 0.
//
// Only the first: reopening an editor can land on one line, and the first thing
// wrong is the one worth landing on. A diagnostic against the session rather
// than the block has no buffer line and is skipped.
func bufFirstLine(constructs []session.Construct, diags []eval.BlockDiag) int {
	for _, d := range diags {
		if d.Construct >= 0 && d.Construct < len(constructs) && d.Line > 0 {
			return constructs[d.Construct].Line + d.Line - 1
		}
	}
	return 0
}
