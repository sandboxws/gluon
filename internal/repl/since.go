package repl

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/sandboxws/gluon/internal/release"
	"github.com/sandboxws/gluon/internal/syntax"
)

// :since is what each Go release added, read from the toolchain that shipped
// it — $GOROOT/api/go1.N.txt and $GOROOT/doc/godebug.md — plus a curated layer
// for the language changes no api file describes. See internal/release.
//
// Nothing here goes online. Constraint G: :get is the only command that does,
// and a proposal number is printed rather than fetched, the way :doc -url is.

// releases is the parsed toolchain, read once per process.
//
// Measured at 46.9ms for a whole 1.27 distribution, which is a tenth of one
// evaluation — so it is cached rather than made lazy, and the first :since of a
// session pays it. A package-level cache rather than a Core field because the
// answer is a property of the machine, and two sessions in one process would
// otherwise parse it twice.
var releases = sync.OnceValues(func() ([]release.Release, error) {
	root := release.GOROOT()
	if root == "" {
		return nil, errors.New("no go toolchain on PATH — :since reads $GOROOT/api")
	}
	return release.Load(root)
})

const sinceUsage = "usage: :since [version] [-lang] [-pkg <path>] [-run <name>]"

type sinceMode int

const (
	sinceBrowse sinceMode = iota
	sinceLang
	sincePkg
	sinceRun
)

type sinceArgs struct {
	version string
	mode    sinceMode
	pkg     string
	run     string
}

// parseSinceArgs reads the version and at most one mode flag.
//
// Flags are accepted before and after the version, because `:since 1.23 -lang`
// and `:since -lang 1.23` are the same question and refusing one of them would
// be a rule the reader has to remember. A second mode flag is an error rather
// than a silent pick, which is parseDocArgs' rule and is there for the same
// reason: two modes named is a line whose author expected something.
func parseSinceArgs(arg string) (sinceArgs, error) {
	var a sinceArgs
	set := func(m sinceMode) error {
		if a.mode != sinceBrowse {
			return errors.New("only one of -lang, -pkg or -run at a time")
		}
		a.mode = m
		return nil
	}
	fields := strings.Fields(arg)
	// A flag taking a value consumes the next field, so the index moves in the
	// loop body. A closure doing it also works — Go 1.22 copies the
	// per-iteration variable back before the post statement, which is the
	// change this command has a note about — but relying on that to read a
	// flag is a question the next reader should not have to answer.
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		takesValue := f == "-pkg" || f == "-run"
		var val string
		if takesValue {
			if i+1 >= len(fields) {
				return a, fmt.Errorf("%s takes a value", f)
			}
			i++
			val = fields[i]
		}
		var err error
		switch {
		case f == "-lang":
			err = set(sinceLang)
		case f == "-pkg":
			if err = set(sincePkg); err == nil {
				a.pkg = val
			}
		case f == "-run":
			if err = set(sinceRun); err == nil {
				a.run = val
			}
		case strings.HasPrefix(f, "-"):
			err = fmt.Errorf("unknown flag %s", f)
		default:
			if a.version != "" {
				err = fmt.Errorf("two versions named, %s and %s", a.version, f)
			}
			a.version = strings.TrimPrefix(f, "go")
		}
		if err != nil {
			return a, err
		}
	}
	if a.mode == sinceRun && a.version == "" {
		return a, errors.New("-run needs the release the note belongs to, as in :since 1.23 -run range-over-func")
	}
	return a, nil
}

// since is the command.
func (c *Core) since(arg string) Result {
	a, err := parseSinceArgs(arg)
	if err != nil {
		return Result{Out: sinceUsage + "\n" + err.Error(), Err: true}
	}
	rels, err := releases()
	if err != nil {
		return Result{Out: err.Error(), Err: true}
	}

	var rel release.Release
	if a.version != "" {
		found, ok := release.Find(rels, a.version)
		if !ok {
			return Result{
				Out: fmt.Sprintf("no release %s — this toolchain describes %s to %s",
					a.version, rels[len(rels)-1].Version, rels[0].Version),
				Err: true,
			}
		}
		rel = found
	}

	switch a.mode {
	case sinceRun:
		return c.sinceRun(rel, a.run)
	case sincePkg:
		return c.sincePkg(rels, a.pkg, a.version)
	case sinceLang:
		return c.sinceLang(rels, rel, a.version != "")
	}
	if a.version == "" {
		return c.sinceList(rels)
	}
	return c.sinceRelease(rel)
}

// sinceList is every release the toolchain describes.
func (c *Core) sinceList(rels []release.Release) Result {
	lang := c.ev.Lang()
	headers := []string{"version", "added", "packages", "language"}
	rows := make([][]string, 0, len(rels))
	entries := make([]ModalEntry, 0, len(rels))
	for _, r := range rels {
		rows = append(rows, []string{
			markCurrent(r.Version, lang),
			addedCell(r),
			fmt.Sprint(len(r.Packages())),
			langCell(r),
		})
		entries = append(entries, ModalEntry{
			Title:   "go " + r.Version,
			Text:    releaseSummary(r),
			Choices: []ModalChoice{{Label: r.Version, About: "open this release", Run: ":since " + r.Version}},
		})
	}
	note := sinceListNote(lang)
	var b strings.Builder
	fmt.Fprintf(&b, "%s, newest first\n", plural(len(rels), "Go release"))
	b.WriteString(settingsTable(headers, rows, c.styles()).Render())
	b.WriteString("\n" + note)
	return Result{
		Out: b.String(),
		Modal: &ModalSpec{
			Title:   "Go releases",
			Summary: fmt.Sprintf("%s  browsed", plural(len(rels), "Go release")),
			Headers: headers,
			Rows:    rows,
			Entries: entries,
			Note:    note,
			Refresh: ":since",
		},
	}
}

// sinceRelease is one release: its language changes, its behaviour changes, and
// its stdlib additions a package at a time.
func (c *Core) sinceRelease(r release.Release) Result {
	headers := []string{"change", "what it is", "notes"}
	var rows [][]string
	var entries []ModalEntry

	for _, n := range r.Lang {
		rows = append(rows, []string{"language", n.Title, needsCell(n)})
		entries = append(entries, ModalEntry{
			Title:   n.Title,
			Text:    noteBody(n),
			Choices: c.runChoice(r, n),
			Why:     c.cannotRun(n),
		})
	}
	// Only when nobody has written the release up. A curated release's notes
	// are the better account of the same changes, and showing both would be one
	// release described twice in two vocabularies.
	if !r.Curated {
		for _, g := range r.NamedGates() {
			rows = append(rows, []string{"gated", g.Feature, "nobody has written this up"})
			entries = append(entries, ModalEntry{Title: g.Feature, Text: gateBody(r, g)})
		}
	}
	for _, b := range r.Behaviour {
		rows = append(rows, []string{"godebug", settingOr(b), godebugSummary(b.Text)})
		entries = append(entries, ModalEntry{Title: settingOr(b), Text: goDevLinks(b.Text)})
	}
	for _, pkg := range r.Packages() {
		syms := symbolsIn(r, pkg)
		rows = append(rows, []string{pkg, plural(len(syms), "addition"), kindSummary(syms)})
		entries = append(entries, ModalEntry{Title: pkg, Text: declList(syms)})
	}

	note := releaseNote(r)
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", releaseSummary(r))
	if len(rows) > 0 {
		b.WriteString(settingsTable(headers, rows, c.styles()).Render())
		b.WriteString("\n")
	}
	b.WriteString(linearRelease(r))
	b.WriteString(note)
	return Result{
		Out: b.String(),
		Modal: &ModalSpec{
			Title:   "go " + r.Version,
			Summary: fmt.Sprintf("go %s  %s  browsed", r.Version, plural(len(rows), "change")),
			Headers: headers,
			Rows:    rows,
			Entries: entries,
			Note:    note,
			Refresh: ":since " + r.Version,
		},
	}
}

// runChoice is the "run it" a curated note offers, or nothing when the session
// could not build it.
//
// The choice hands back a command rather than the snippet, and not only because
// ModalChoice.Run is submitted a line at a time while a snippet is many. It is
// invariant 19's rule: one path decides what running a note means — check the
// directive, feed the session, report what happened — and a widget that ran the
// code itself would be a second one.
func (c *Core) runChoice(r release.Release, n release.Note) []ModalChoice {
	if c.cannotRun(n) != "" {
		return nil
	}
	return []ModalChoice{{
		Label: "run",
		About: "evaluate it into this session",
		Run:   fmt.Sprintf(":since %s -run %s", r.Version, n.Name),
	}}
}

// sinceRun evaluates a note's snippet into the session.
func (c *Core) sinceRun(r release.Release, name string) Result {
	n, ok := r.Note(name)
	if !ok {
		return Result{Out: sinceNoNote(r, name), Err: true}
	}
	if why := c.cannotRun(n); why != "" {
		return Result{Out: why, Err: true}
	}
	res := c.submitAll(n.Snippet)
	head := fmt.Sprintf("go %s · %s\n\n%s\n", n.Needs, n.Title, n.Snippet)
	return Result{Out: head + "\n" + res.Out, Err: res.Err}
}

// cannotRun is why this session would not build the snippet, or "".
//
// Saying it here rather than letting the build say it is the argument
// host.buildable() makes: GOTOOLCHAIN=local turns a directive the toolchain
// cannot meet into a hard error naming a temp path the reader never wrote, and
// the two ways to be short of a directive have different fixes.
func (c *Core) cannotRun(n release.Note) string {
	// A change no directive gates is a question about the toolchain, and
	// answering it with the session's go line would send the reader to edit a
	// go.mod that was never in the way.
	if n.ToolchainOnly() {
		tc := c.ev.Toolchain()
		if tc == "" || release.Compare(tc, n.Needs) >= 0 {
			return ""
		}
		return fmt.Sprintf("%s needs the go %s toolchain and this is go %s — "+
			"no go directive gates it, so the go.mod line will not help",
			n.Title, n.Needs, tc)
	}
	lang := c.ev.Lang()
	if lang == "" || release.Compare(lang, n.Needs) >= 0 {
		return ""
	}
	if h := c.ev.Host(); h != nil {
		return fmt.Sprintf("%s needs go %s, and the module at %s says go %s — "+
			"raise its go directive, or :use -off to leave it",
			n.Title, n.Needs, h.Path, lang)
	}
	return fmt.Sprintf("%s needs go %s, and this session builds at go %s — "+
		"gluon runs with GOTOOLCHAIN=local, so it will not fetch a newer one",
		n.Title, n.Needs, lang)
}

// sinceLang is the curated language changes, as prose.
func (c *Core) sinceLang(rels []release.Release, one release.Release, scoped bool) Result {
	var b strings.Builder
	title := "the language changes"
	if scoped {
		title = "go " + one.Version + " · the language changes"
		rels = []release.Release{one}
	}
	var found int
	for _, r := range rels {
		for _, n := range r.Lang {
			if found > 0 {
				b.WriteString("\n" + strings.Repeat("─", 8) + "\n\n")
			}
			found++
			fmt.Fprintf(&b, "go %s · %s\n\n%s\n", r.Version, n.Title, noteBody(n))
		}
	}
	if found == 0 {
		return Result{Out: sinceUncurated(one, scoped)}
	}
	return sourceResult(title, strings.TrimSpace(b.String()), syntax.None)
}

// sincePkg is every addition to one package, across every release.
func (c *Core) sincePkg(rels []release.Release, pkg, version string) Result {
	var b strings.Builder
	var found int
	for _, r := range rels {
		if version != "" && r.Version != version {
			continue
		}
		syms := symbolsIn(r, pkg)
		if len(syms) == 0 {
			continue
		}
		found += len(syms)
		fmt.Fprintf(&b, "go %s\n%s\n\n", r.Version, declList(syms))
	}
	if found == 0 {
		return Result{
			Out: fmt.Sprintf("%s gained nothing in the releases this toolchain describes — "+
				"an import path, not a name, so `maps` rather than `maps.Keys`", pkg),
		}
	}
	title := fmt.Sprintf("%s · %s", pkg, plural(found, "addition"))
	return sourceResult(title, strings.TrimSpace(b.String()), syntax.None)
}

// --- cells and prose -------------------------------------------------------

func markCurrent(version, lang string) string {
	if lang != "" && version == lang {
		return version + " ←"
	}
	return version
}

// addedCell marks go1.txt rather than explaining it in the cell: the file is
// the whole original API and not a delta, but spelling that out inline widens
// the column for all twenty-eight rows to annotate one. The footer carries the
// sentence, the way the ← marker does.
func addedCell(r release.Release) string {
	if r.Initial {
		return fmt.Sprintf("%d *", len(r.API))
	}
	return fmt.Sprint(len(r.API))
}

// langCell distinguishes a release nobody has written notes for from one that
// changed no syntax. A dash claiming the second on the strength of the first
// would be the lie this column exists to avoid.
//
// An unwritten release still has a floor: the checker's own gates. "2 gated"
// says the type checker admits two things only at this version and that nobody
// has written them up — which is a great deal more than a dash, and is why a
// release landing between two of gluon's is not simply blank.
func langCell(r release.Release) string {
	switch {
	case r.Curated && len(r.Lang) > 0:
		return plural(len(r.Lang), "change")
	case r.Curated:
		return "none"
	case len(r.Gates) > 0:
		return fmt.Sprintf("%d gated", len(r.Gates))
	default:
		return "—"
	}
}

// sinceListNote explains the three marks the table uses. The dash is the one
// that matters: it says nobody looked, and a reader who took it for "no
// language changes" would have been told something nothing checked.
func sinceListNote(lang string) string {
	var parts []string
	if lang != "" {
		parts = append(parts, "← is the go directive this session builds at")
	}
	parts = append(parts,
		"* is the original Go 1 API rather than additions to it",
		"— under language means nobody has written notes for that release, not that it changed none")
	return strings.Join(parts, ". ") + "."
}

func releaseNote(r release.Release) string {
	if len(r.Lang) == 0 {
		return "Additions are read from $GOROOT/api/go" + r.Version + ".txt; behaviour changes from doc/godebug.md."
	}
	return "Running a language note evaluates it into this session, which the scratchpad keeps — :branch first if that matters."
}

func releaseSummary(r release.Release) string {
	if r.Initial {
		return fmt.Sprintf("go %s · %s in the original Go 1 API, across %s",
			r.Version, plural(len(r.API), "declaration"), plural(len(r.Packages()), "package"))
	}
	parts := []string{fmt.Sprintf("go %s · %s", r.Version, plural(len(r.API), "addition"))}
	parts = append(parts, plural(len(r.Packages()), "package"))
	if len(r.Lang) > 0 {
		parts = append(parts, plural(len(r.Lang), "language change"))
	}
	if n := len(r.Behaviour); n > 0 {
		// plural() pluralises by appending an s, which "entry" does not do.
		parts = append(parts, fmt.Sprintf("%d godebug %s", n, plurals(n, "entry", "entries")))
	}
	return strings.Join(parts, ", ")
}

func noteBody(n release.Note) string {
	var b strings.Builder
	b.WriteString(n.Text)
	fmt.Fprintf(&b, "\n\nNeeds go %s — %s.", n.Needs, belowClause(n))
	if n.Issue != 0 {
		fmt.Fprintf(&b, "\nProposal: https://go.dev/issue/%d", n.Issue)
	}
	fmt.Fprintf(&b, "\n\n%s", n.Snippet)
	return b.String()
}

// belowClause says what a lower go directive does, rather than echoing the
// word the file stores. "below that, error" is a field name showing through.
func belowClause(n release.Note) string {
	switch {
	case n.ToolchainOnly():
		return "no go directive gates it; only the toolchain decides"
	case n.Gated():
		return "under a lower go directive the compiler rejects it"
	default:
		return "under a lower go directive it still builds and means something else"
	}
}

func sinceUncurated(r release.Release, scoped bool) string {
	if !scoped {
		return "no language notes are written yet"
	}
	return fmt.Sprintf("no language notes are written for go %s — the api file and "+
		"doc/godebug.md still describe it, so `:since %s` is not empty",
		r.Version, r.Version)
}

func sinceNoNote(r release.Release, name string) string {
	if len(r.Lang) == 0 {
		return fmt.Sprintf("go %s has no language notes to run", r.Version)
	}
	var names []string
	for _, n := range r.Lang {
		names = append(names, n.Name)
	}
	return fmt.Sprintf("go %s has no note called %q — it has %s",
		r.Version, name, strings.Join(names, ", "))
}

func symbolsIn(r release.Release, pkg string) []release.Symbol {
	var out []release.Symbol
	for _, s := range r.API {
		if s.Pkg == pkg {
			out = append(out, s)
		}
	}
	return out
}

// kindSummary is "3 func, 1 type", in a fixed order so two releases read the
// same way.
func kindSummary(syms []release.Symbol) string {
	order := []string{"func", "method", "type", "const", "var"}
	count := map[string]int{}
	for _, s := range syms {
		count[s.Kind()]++
	}
	var parts []string
	for _, k := range order {
		if count[k] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", count[k], k))
			delete(count, k)
		}
	}
	return strings.Join(parts, ", ")
}

func declList(syms []release.Symbol) string {
	var b strings.Builder
	for _, s := range syms {
		fmt.Fprintf(&b, "  %s", s.Decl)
		if len(s.Platforms) > 0 {
			fmt.Fprintf(&b, "  [%s]", strings.Join(s.Platforms, " "))
		}
		if u := s.URL(); u != "" {
			fmt.Fprintf(&b, "  %s", u)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// linearRelease is what the table cannot hold, for the driver that has no
// screen. Invariant 19: Out carries the same information in linear form, so a
// pipe, gluon -e and the MCP tool lose nothing by not being able to open a row.
func linearRelease(r release.Release) string {
	var b strings.Builder
	for _, n := range r.Lang {
		fmt.Fprintf(&b, "\nlanguage · %s\n%s\n", n.Title, noteBody(n))
	}
	for _, be := range r.Behaviour {
		fmt.Fprintf(&b, "\ngodebug · %s\n%s\n", settingOr(be), goDevLinks(be.Text))
	}
	for _, pkg := range r.Packages() {
		fmt.Fprintf(&b, "\n%s\n%s\n", pkg, declList(symbolsIn(r, pkg)))
	}
	b.WriteString("\n")
	return b.String()
}

// plurals is plural() for a word that does not take a bare s.
func plurals(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// linkRe is a markdown inline link, which doc/godebug.md is full of.
var linkRe = regexp.MustCompile(`\[([^\]]*)\]\(([^)]*)\)`)

// goDevLinks makes godebug.md's relative links usable without rendering the
// markdown. Its targets are written for go.dev — "/pkg/os#Lstat" — so printed
// as they stand they name a path on no machine. Rewriting the target and
// leaving every other byte alone is a smaller thing than rendering, and it is
// the same posture :doc -url takes: an address is printed, never fetched.
func goDevLinks(s string) string {
	return linkRe.ReplaceAllStringFunc(s, func(m string) string {
		g := linkRe.FindStringSubmatch(m)
		if !strings.HasPrefix(g[2], "/") {
			return m
		}
		return "[" + g[1] + "](https://go.dev" + g[2] + ")"
	})
}

// godebugSummary is the one line a table cell holds: the paragraph's first
// line, with the link syntax taken off and the backticks with it. The cell is a
// summary and the row still opens onto the paragraph as written, so nothing is
// lost by making the column readable.
func godebugSummary(text string) string {
	line := strings.Join(strings.Fields(text), " ")
	line = linkRe.ReplaceAllString(line, "$1")
	line = strings.ReplaceAll(line, "`", "")
	// The paragraph is hard-wrapped in the source, so the first *line* is
	// whatever column godebug.md broke at — "Go 1.23 changed the behavior of"
	// is a whole cell that way. The wrapping is joined out first, and the cut
	// is marked so a truncated cell does not read as a finished sentence.
	if cut := truncate(line, 63); cut != line {
		return cut + "…"
	}
	return line
}

// needsCell says what the version in a note means, which is not the same thing
// for all three kinds. "needs go 1.27" on a change no directive gates would send
// a reader to edit a go.mod line that was never the problem.
func needsCell(n release.Note) string {
	switch {
	case n.ToolchainOnly():
		return "go " + n.Needs + " toolchain; no directive"
	case n.Gated():
		return "needs go " + n.Needs
	default:
		return "go " + n.Needs + ", or it means something else"
	}
}

func gateBody(r release.Release, g release.Gate) string {
	return fmt.Sprintf("The type checker admits %q only at go %s.\n\n"+
		"This is read from $GOROOT/src/go/types, which is exact about the version "+
		"and only sometimes names the feature — some gates carry no name at all, "+
		"so this list is a floor and not the release's changes. Nobody has "+
		"written go %s up, which is what `:since %s -lang` will tell you.",
		g.Feature, g.Version, r.Version, r.Version)
}

func settingOr(b release.Behaviour) string {
	if b.Setting == "" {
		return "—"
	}
	return b.Setting
}
