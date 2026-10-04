package docgen

import (
	"fmt"
	"html"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/repl"
	"github.com/sandboxws/gluon/internal/syntax"
)

// A guide's transcripts are real. Each one is a script in site/sessions/, run
// through a real session by `just docs-sessions` (record_test.go, behind the
// sessions build tag) and saved beside it as <name>.out — what gluon printed,
// byte for byte, drawn the way a terminal draws it. A page includes one with
// {% session "name" %}. Rendering never runs anything, so `just docs` stays
// fast and deterministic; recording is the slow, real half, run when a script
// or gluon changes.
//
// A <name>.gl script is typed at the gluon prompt, one line per submission:
//
//	# a comment, never shown
//	@ a setup line: run, and not shown
//	x := []int{1, 2, 3}        a line to show, with what it printed
//	<<<                        the lines up to >>> are one submission,
//	type P struct{ X, Y int }  shown as typed
//	>>>
//	<<< paste                  the lines up to >>> are pasted, newline
//	a := 1                     and all: each is echoed as typing would have
//	a + 1                      echoed it, and they are evaluated as one
//	>>>                        batch
//
// A <name>.sh script is typed at a shell, one command per line, with the gluon
// this checkout builds first on PATH; @ and <<< mean the same there. A line of
// a .gl script that starts "$ " is a shell command too — the reader stepping
// out of gluon to run one, against the same home and the same project.
//
// Directives start with %. These apply to the whole script: %host <fixture>
// runs it in a copy of testdata/<fixture>, %attach attaches the session to
// that copy the way gluon -host does, %serve starts the fixture's servers,
// %env K=V sets a variable ($HOST in V is the fixture's copy), and
// %history <line> puts a line in the history
// file before the session starts. These apply where they stand: %clip N shows
// only the first N lines of the next step's output, and says how many more
// there are; %sh <command> runs a shell command there and shows nothing; and
// %editor, followed by a <<< block, is what the step before it writes in the
// editor it opened — the recorder stands in for the person at the keyboard.

// Script is a parsed script.
type Script struct {
	Directives []string
	Steps      []Step
}

// Step is one submission.
type Step struct {
	Src    string
	Hidden bool
	// Paste says Src is several lines pasted at once, which the prompt
	// evaluates as one batch.
	Paste bool
	// Clip is how many lines of the output a page shows; zero is all of them.
	Clip int
	// Shell says Src is a shell command rather than a line for the prompt.
	Shell bool
	// Editor is what is written in the editor the step opens; HasEditor says
	// the script gave any.
	Editor    string
	HasEditor bool
}

// ParseScript reads a script. shell says it is a .sh script, whose lines are
// shell commands.
func ParseScript(text string, shell bool) (Script, error) {
	var s Script
	clip := 0
	editorNext := false
	add := func(st Step) {
		st.Clip, clip = clip, 0
		st.Shell = st.Shell || shell
		s.Steps = append(s.Steps, st)
	}
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], " \t")
		t := strings.TrimSpace(l)
		if editorNext && t != "" && !strings.HasPrefix(l, "#") && t != "<<<" {
			return s, fmt.Errorf("%%editor is followed by %q rather than a <<< block", l)
		}
		switch {
		case t == "" || strings.HasPrefix(l, "#"):
		case strings.HasPrefix(l, "%clip "):
			n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(l, "%clip ")))
			if err != nil || n < 1 {
				return s, fmt.Errorf("%q: %%clip takes a number of lines", l)
			}
			clip = n
		case strings.HasPrefix(l, "%sh "):
			s.Steps = append(s.Steps, Step{Src: strings.TrimSpace(l[len("%sh "):]), Hidden: true, Shell: true})
		case t == "%editor":
			if len(s.Steps) == 0 {
				return s, fmt.Errorf("%%editor has no step before it to belong to")
			}
			editorNext = true
		case strings.HasPrefix(l, "%"):
			s.Directives = append(s.Directives, strings.TrimSpace(l[1:]))
		case t == "<<<" || t == "<<< paste":
			var block []string
			for i++; i < len(lines) && strings.TrimSpace(lines[i]) != ">>>"; i++ {
				block = append(block, strings.TrimRight(lines[i], " \t"))
			}
			if i >= len(lines) {
				return s, fmt.Errorf("a <<< block is never closed")
			}
			if editorNext {
				last := &s.Steps[len(s.Steps)-1]
				last.Editor, last.HasEditor = strings.Join(block, "\n"), true
				editorNext = false
				continue
			}
			add(Step{Src: strings.Join(block, "\n"), Paste: t != "<<<"})
		case strings.HasPrefix(l, "@"):
			add(Step{Src: strings.TrimSpace(l[1:]), Hidden: true})
		case !shell && strings.HasPrefix(l, "$ "):
			add(Step{Src: strings.TrimSpace(l[2:]), Shell: true})
		default:
			add(Step{Src: l})
		}
	}
	if editorNext {
		return s, fmt.Errorf("%%editor has no <<< block after it")
	}
	return s, nil
}

// Entry is one recorded submission: what was typed and what came back.
type Entry struct {
	In  string
	Out string
	Err bool
	// Lang is the language the result said its output is in — Go source for
	// :src and :gen — which the terminal paints and a page paints the same.
	Lang syntax.Lang
	// View says the terminal opens a view for this result rather than
	// printing it. Out is what the view shows, and what a pipe or gluon -e
	// prints; the page says a view opens, so the transcript never claims the
	// prompt printed it.
	View bool
	// Clip is how many lines of Out a page shows. The transcript keeps all
	// of them.
	Clip int
	// Shell says In was typed at a shell.
	Shell bool
	// Editor is what was written in the editor the line opened; Out is what
	// gluon printed when it closed.
	Editor string
}

// The transcript format is the terminal's own: a prompt, the line, and what it
// printed, one entry after another. What a page needs and a terminal does not
// show — that a line was an error, that its output is Go, that it opened a
// view or an editor — is said on a line of its own that starts with a !, and
// an output line that would read as one of these marks is written after !| so
// that it cannot.
const (
	promptMark   = "gluon> "
	shellMark    = "$ "
	continueMark = "  ...> "
	errMark      = "!error "
	langMark     = "!lang "
	viewMark     = "!view"
	clipMark     = "!clip "
	editorMark   = "!editor "
	rawMark      = "!| "
)

// FormatTranscript writes entries as a .out file.
func FormatTranscript(entries []Entry) string {
	var b strings.Builder
	for _, e := range entries {
		for i, l := range strings.Split(e.In, "\n") {
			switch {
			case i > 0:
				b.WriteString(continueMark + l + "\n")
			case e.Shell:
				b.WriteString(shellMark + l + "\n")
			default:
				b.WriteString(promptMark + l + "\n")
			}
		}
		if e.View {
			b.WriteString(viewMark + "\n")
		}
		if e.Editor != "" {
			for _, l := range strings.Split(e.Editor, "\n") {
				b.WriteString(editorMark + l + "\n")
			}
		}
		if e.Out == "" {
			continue
		}
		if e.Clip > 0 {
			b.WriteString(clipMark + strconv.Itoa(e.Clip) + "\n")
		}
		if e.Lang != syntax.None && !e.Err {
			b.WriteString(langMark + string(e.Lang) + "\n")
		}
		for _, l := range strings.Split(strings.TrimRight(e.Out, "\n"), "\n") {
			switch {
			case e.Err:
				b.WriteString(errMark)
			case strings.HasPrefix(l, promptMark), strings.HasPrefix(l, shellMark),
				strings.HasPrefix(l, continueMark), strings.HasPrefix(l, "!"):
				b.WriteString(rawMark)
			}
			b.WriteString(l + "\n")
		}
	}
	return b.String()
}

// ParseTranscript reads a .out file.
func ParseTranscript(text string) []Entry {
	type draft struct {
		Entry
		in, editor, out []string
	}
	var ds []*draft
	for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		var d *draft
		if len(ds) > 0 {
			d = ds[len(ds)-1]
		}
		// The marks that describe an entry come before its output; once it
		// has output, every line is output.
		before := d != nil && len(d.out) == 0
		switch {
		case strings.HasPrefix(l, rawMark) && d != nil:
			d.out = append(d.out, strings.TrimPrefix(l, rawMark))
		case strings.HasPrefix(l, promptMark):
			ds = append(ds, &draft{in: []string{strings.TrimPrefix(l, promptMark)}})
		case strings.HasPrefix(l, shellMark):
			ds = append(ds, &draft{Entry: Entry{Shell: true}, in: []string{strings.TrimPrefix(l, shellMark)}})
		case before && len(d.editor) == 0 && strings.HasPrefix(l, continueMark):
			d.in = append(d.in, strings.TrimPrefix(l, continueMark))
		case before && l == viewMark:
			d.View = true
		case before && strings.HasPrefix(l, editorMark):
			d.editor = append(d.editor, strings.TrimPrefix(l, editorMark))
		case before && strings.HasPrefix(l, clipMark):
			d.Clip, _ = strconv.Atoi(strings.TrimPrefix(l, clipMark))
		case before && strings.HasPrefix(l, langMark):
			d.Lang = syntax.Lang(strings.TrimPrefix(l, langMark))
		case d != nil:
			if strings.HasPrefix(l, errMark) {
				d.Err, l = true, strings.TrimPrefix(l, errMark)
			}
			d.out = append(d.out, l)
		}
	}
	out := make([]Entry, len(ds))
	for i, d := range ds {
		e := d.Entry
		e.In = strings.Join(d.in, "\n")
		e.Editor = strings.Join(d.editor, "\n")
		e.Out = strings.Join(d.out, "\n")
		out[i] = e
	}
	return out
}

// Tracker notes which commands the guide pages work through, for the coverage
// check. It is shared across one Build.
type Tracker struct {
	// Worked maps a command to the guide pages that show it being typed.
	Worked map[string][]string
	// kinds is each command's argument kind, and its aliases' — how a line is
	// painted, and how an alias counts for the command.
	kinds map[string]cmdspec.Kind
	canon map[string]string
}

// NewTracker reads the kinds and aliases from the registry.
func NewTracker() *Tracker {
	t := &Tracker{Worked: map[string][]string{}, kinds: map[string]cmdspec.Kind{}, canon: map[string]string{}}
	for _, c := range repl.Reference() {
		for _, n := range append([]string{c.Name}, c.Aliases...) {
			if _, seen := t.kinds[n]; !seen {
				t.kinds[n], t.canon[n] = c.Usage.Kind, c.Name
			}
		}
	}
	return t
}

// Unworked is every command the guides never show being typed, sorted.
func (t *Tracker) Unworked(cmds []string) []string {
	var out []string
	for _, c := range cmds {
		if len(t.Worked[c]) == 0 {
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}

var metaWord = regexp.MustCompile(`^(:[a-z]+)(\s|$)`)

// Badges say what a terminal did that a transcript cannot show.
const (
	viewBadge   = `<span class="vw" title="At the prompt this opens a view you can scroll and filter. What it shows is below, and it is what a pipe or gluon -e prints.">opens a view</span>`
	editorBadge = `<span class="vw" title="At the prompt this opens your editor. The marked lines are what was written in it; what follows them is what gluon printed when it closed.">opens the editor</span>`
)

// session renders a recorded transcript into a page, and notes the commands it
// works through when the page is a guide.
func (t *Tracker) session(root, name string, p Page, caption ...string) (template.HTML, error) {
	b, err := os.ReadFile(filepath.Join(root, "site", "sessions", name+".out"))
	if err != nil {
		return "", fmt.Errorf("session %q has not been recorded — run `just docs-sessions %s`: %w", name, name, err)
	}
	entries := ParseTranscript(string(b))
	var h strings.Builder
	h.WriteString(`<div class="repl">`)
	for _, c := range caption {
		label := "gluon"
		if len(entries) > 0 && entries[0].Shell {
			label = "Shell"
		}
		h.WriteString(`<div class="cmd-bar"><span class="ck">` + label + `</span><span class="cv">` + html.EscapeString(c) + `</span></div>`)
	}
	h.WriteString(`<div class="term-body ex">`)
	for _, e := range entries {
		in := strings.Split(e.In, "\n")
		for i, l := range in {
			var mark, text string
			switch {
			case e.Shell && i == 0:
				mark, text = `<span class="pr">$</span> `, html.EscapeString(l)
			case e.Shell:
				mark, text = `<span class="an">&gt;</span> `, html.EscapeString(l)
			case i == 0:
				mark, text = `<span class="pr">gluon&gt;</span> `, t.paintInput(l)
			default:
				mark, text = `<span class="an">  ...&gt;</span> `, t.paintInput(l)
			}
			tail := ""
			if i == len(in)-1 {
				switch {
				case e.View:
					tail = viewBadge
				case e.Editor != "":
					tail = editorBadge
				}
			}
			h.WriteString(`<div class="tl">` + mark + text + tail + `</div>`)
		}
		if m := metaWord.FindStringSubmatch(e.In); m != nil && !e.Shell && p.Section == "Guides" {
			if c, ok := t.canon[m[1]]; ok {
				t.Worked[c] = appendOnce(t.Worked[c], p.Path)
			}
		}
		if e.Editor != "" {
			for _, l := range paintLines(e.Editor, syntax.Go) {
				h.WriteString(`<div class="tl ed">` + l + `</div>`)
			}
		}
		lines, classes, more := outLines(e)
		for i, l := range lines {
			h.WriteString(`<div class="tl out` + classes[i] + `">` + l + `</div>`)
		}
		if more > 0 {
			h.WriteString(`<div class="tl out an">… ` + strconv.Itoa(more) + ` more lines</div>`)
		}
	}
	h.WriteString(`</div></div>`)
	return template.HTML(h.String()), nil
}

// outLines is an entry's output painted a line at a time, with the class each
// line's div takes and, when the entry is clipped, how many lines were left
// out.
func outLines(e Entry) (lines, classes []string, more int) {
	if e.Out == "" {
		return nil, nil, 0
	}
	if e.Lang != syntax.None && !e.Err {
		lines = paintLines(e.Out, e.Lang)
		classes = make([]string, len(lines))
	} else {
		for _, l := range strings.Split(e.Out, "\n") {
			lines = append(lines, paintOutput(l, e.Err, e.Shell))
			cls := ""
			if !e.Err && ruled(l) {
				cls = " bx"
			}
			classes = append(classes, cls)
		}
	}
	if e.Clip > 0 && len(lines) > e.Clip+1 {
		lines, classes, more = lines[:e.Clip], classes[:e.Clip], len(lines)-e.Clip
	}
	return lines, classes, more
}

// hero is a recorded transcript as the landing page's terminal plays it:
// every line a .tl the replay reveals in turn — typed lines typed, printed
// lines printed — each with the pause before it. The landing page is not a
// guide, so nothing here counts toward the guides' coverage.
func (t *Tracker) hero(root, name string) (template.HTML, error) {
	b, err := os.ReadFile(filepath.Join(root, "site", "sessions", name+".out"))
	if err != nil {
		return "", fmt.Errorf("session %q has not been recorded — run `just docs-sessions %s`: %w", name, name, err)
	}
	var h strings.Builder
	first, printed := true, false
	for _, e := range ParseTranscript(string(b)) {
		for i, l := range strings.Split(e.In, "\n") {
			// A pause before each typed line: longer after an answer, the
			// time it takes to read one, than between two lines typed in turn.
			wait := 220
			switch {
			case first:
				wait = 300
			case printed && i == 0:
				wait = 500
			}
			first = false
			mark, text := `<span class="pr">gluon&gt;</span> `, t.paintInput(l)
			switch {
			case e.Shell && i == 0:
				mark, text = `<span class="pr">$</span> `, html.EscapeString(l)
			case e.Shell:
				mark, text = `<span class="an">&gt;</span> `, html.EscapeString(l)
			case i > 0:
				mark = `<span class="an">  ...&gt;</span> `
			}
			fmt.Fprintf(&h, `<div class="tl in" data-wait="%d">%s<span class="txt">%s</span></div>`+"\n", wait, mark, text)
		}
		lines, classes, more := outLines(e)
		for i, l := range lines {
			wait := 45
			if i == 0 {
				wait = 290
			}
			fmt.Fprintf(&h, `<div class="tl out%s" data-wait="%d"><span class="txt">%s</span></div>`+"\n", classes[i], wait, l)
		}
		if more > 0 {
			fmt.Fprintf(&h, `<div class="tl out" data-wait="45"><span class="txt"><span class="an">… %d more lines</span></span></div>`+"\n", more)
		}
		printed = len(lines) > 0
	}
	return template.HTML(h.String()), nil
}

// paintInput paints one typed line the way the prompt does: a meta command by
// its argument's kind, anything else as Go.
func (t *Tracker) paintInput(l string) string {
	if m := metaWord.FindStringSubmatch(l); m != nil {
		kind := t.kinds[m[1]]
		name, arg, _ := strings.Cut(l, " ")
		out := `<span class="kw">` + html.EscapeString(name) + `</span>`
		if arg == "" {
			return out
		}
		switch {
		case kind.IsGo():
			return out + " " + paint(arg, syntax.Go)
		case kind == cmdspec.SQL:
			return out + " " + paint(arg, syntax.SQL)
		}
		return out + " " + html.EscapeString(arg)
	}
	return paint(l, syntax.Go)
}

// typePrefix is the `(type)` a printed value starts with.
var typePrefix = regexp.MustCompile(`^\(([^()]|\([^()]*\))*\)`)

// paintOutput paints one printed line lightly: a value's `(type)` in the type
// colour, a table's rules in the punctuation colour, an error in the error
// colour, and the rest as it was printed. A shell's output is only ruled: what
// a program prints to a shell is not a gluon value.
func paintOutput(l string, isErr, shell bool) string {
	if isErr {
		return `<span class="er">` + html.EscapeString(l) + `</span>`
	}
	if loc := typePrefix.FindStringIndex(l); loc != nil && !shell {
		return `<span class="ty">` + html.EscapeString(l[:loc[1]]) + `</span>` + rules(l[loc[1]:])
	}
	return rules(l)
}

// isRule is a box-drawing character: what a table's borders are drawn in.
func isRule(r rune) bool { return r >= 0x2500 && r <= 0x257F }

// rules escapes a line and puts each run of box-drawing characters in the
// punctuation colour, so a table's frame recedes behind what it holds.
func rules(l string) string {
	var b strings.Builder
	in := false
	for _, r := range l {
		if isRule(r) != in {
			if in {
				b.WriteString(`</span>`)
			} else {
				b.WriteString(`<span class="p">`)
			}
			in = !in
		}
		b.WriteString(html.EscapeString(string(r)))
	}
	if in {
		b.WriteString(`</span>`)
	}
	return b.String()
}

// ruled reports whether a line is part of a table's frame: its first
// character past the indent draws a border. Such lines are set solid, so a
// table's verticals join the way the terminal draws them.
func ruled(l string) bool {
	for _, r := range l {
		if r != ' ' {
			return isRule(r)
		}
	}
	return false
}

// appendOnce appends s unless xs has it.
func appendOnce(xs []string, s string) []string {
	for _, x := range xs {
		if x == s {
			return xs
		}
	}
	return append(xs, s)
}

// Rewrite replaces each pair's first string with its second throughout out —
// the recorder's machine paths with the ones a reader's machine would print —
// and keeps every box-drawn table aligned while it does. A cell in a table
// keeps its width, so its column stays where it was, and the table is then
// narrowed back to what its cells need; a table nothing was replaced in is
// left byte for byte. Longer strings are replaced first, so a directory inside
// another is replaced as itself.
func Rewrite(out string, pairs [][2]string) string {
	ps := append([][2]string{}, pairs...)
	sort.SliceStable(ps, func(i, j int) bool { return len(ps[i][0]) > len(ps[j][0]) })
	replace := func(s string) string {
		for _, p := range ps {
			if p[0] != "" {
				s = strings.ReplaceAll(s, p[0], p[1])
			}
		}
		return s
	}
	lines := strings.Split(out, "\n")
	changed := make([]bool, len(lines))
	for i, l := range lines {
		if !ruled(l) {
			lines[i] = replace(l)
			continue
		}
		cells := strings.Split(l, "│")
		for j, c := range cells {
			r := replace(c)
			if r == c {
				continue
			}
			changed[i] = true
			if n := utf8.RuneCountInString(c) - utf8.RuneCountInString(r); n > 0 && j > 0 && j < len(cells)-1 {
				r += strings.Repeat(" ", n)
			}
			cells[j] = r
		}
		lines[i] = strings.Join(cells, "│")
	}
	for start := 0; start < len(lines); {
		if !ruled(lines[start]) {
			start++
			continue
		}
		// A table ends at its bottom border, so two drawn one under the
		// other are narrowed as the two tables they are.
		end, touched := start, false
		for end < len(lines) && ruled(lines[end]) {
			touched = touched || changed[end]
			end++
			if strings.HasPrefix(strings.TrimLeft(lines[end-1], " "), "╰") {
				break
			}
		}
		if touched {
			narrow(lines[start:end])
		}
		start = end
	}
	return strings.Join(lines, "\n")
}

// narrow takes the slack out of a box-drawn table's columns. Every cell is
// drawn with one space of padding on its right, so a column whose every cell
// ends in more than one space is wider than it needs to be.
func narrow(rows []string) {
	// A row splits into the text before its first rule, then each rule and
	// the text after it: content cells, or runs of ─ on a border row.
	split := func(l string) [][]rune {
		segs := [][]rune{nil}
		for _, r := range l {
			if strings.ContainsRune("│┬┼┴╭╮├┤╰╯", r) {
				segs = append(segs, []rune{r}, nil)
				continue
			}
			segs[len(segs)-1] = append(segs[len(segs)-1], r)
		}
		return segs
	}
	grid := make([][][]rune, len(rows))
	for i, l := range rows {
		grid[i] = split(l)
		if len(grid[i]) != len(grid[0]) {
			return // not one table after all: leave it as it was
		}
	}
	for col := 2; col < len(grid[0])-1; col += 2 {
		slack := -1
		for _, segs := range grid {
			seg := segs[col]
			if len(seg) > 0 && seg[0] == '─' {
				continue
			}
			n := 0
			for k := len(seg) - 1; k >= 0 && seg[k] == ' '; k-- {
				n++
			}
			if slack < 0 || n < slack {
				slack = n
			}
		}
		if slack <= 1 {
			continue
		}
		for _, segs := range grid {
			segs[col] = segs[col][:len(segs[col])-(slack-1)]
		}
	}
	for i, segs := range grid {
		var b strings.Builder
		for _, seg := range segs {
			b.WriteString(string(seg))
		}
		rows[i] = b.String()
	}
}

// PlainTranscript is a recorded transcript as plain text, for a file that is
// not a web page: the README. It is what the terminal showed, prompts and
// output, with nothing the page adds — no badge, no editor mark — and a clipped
// answer ends where the page's does, saying how many lines are left out.
func PlainTranscript(text string) string {
	var b strings.Builder
	for _, e := range ParseTranscript(text) {
		for i, l := range strings.Split(e.In, "\n") {
			switch {
			case i > 0 && e.Shell:
				b.WriteString("> " + l + "\n")
			case i > 0:
				b.WriteString(continueMark + l + "\n")
			case e.Shell:
				b.WriteString(shellMark + l + "\n")
			default:
				b.WriteString(promptMark + l + "\n")
			}
		}
		if e.Out == "" {
			continue
		}
		lines := strings.Split(e.Out, "\n")
		more := 0
		if e.Clip > 0 && len(lines) > e.Clip+1 {
			lines, more = lines[:e.Clip], len(lines)-e.Clip
		}
		for _, l := range lines {
			b.WriteString(l + "\n")
		}
		if more > 0 {
			fmt.Fprintf(&b, "… %d more lines\n", more)
		}
	}
	return b.String()
}
