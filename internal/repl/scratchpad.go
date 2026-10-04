package repl

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/host"
	"github.com/sandboxws/gluon/internal/scratch"
)

// scratchpad is the pad this session is written to.
//
// Everything about it is per-Core state, and it is deliberately not a pointer
// into the scratch package's own types: what is held here is what persist needs
// to decide whether to write, and that is a different question from what a pad
// file contains.
type scratchpad struct {
	name string
	dir  string

	// readOnly is why this session has stopped writing, empty while it is
	// writing.
	//
	// This is the mechanism that makes the worst bug the feature can have
	// unreachable: open → replay fails → session empty → user types one line →
	// persist → forty lines replaced by one. Every failure to open sets it,
	// persist returns while it is set, and the reason is repeated once when the
	// next line lands. A flag rather than care, because care is what fails at
	// four in the afternoon.
	readOnly string
	// told records that readOnly has been reported, so it is said once rather
	// than on every line for the rest of the session.
	told bool

	// last is the bytes persist wrote, and lastSum the sums beside them. The
	// dedupe key is the rendered file and not Core.gen: gen is bumped at the
	// top of Submit for every submission, :help included, because it exists to
	// invalidate completion caches and deliberately over-counts. "Write when
	// gen moved" would write on every line and dedupe nothing.
	last    []byte
	lastSum []byte
	// size and mod are what the file looked like after this session wrote it,
	// for the stat that detects a second gluon in the same pad.
	size int64
	mod  time.Time
	// writes is how many times this session has written the file. It is what
	// tells a first write from a later one — the stat guard has nothing to
	// compare against until there has been one — and it is what the
	// once-per-submission property is asserted on.
	writes int
	// entries is how many the last write held, so a write that shrinks the pad
	// can keep what it replaced.
	entries int
}

// OpenPad installs a scratchpad, replaying it. It is called by a driver and
// never by NewCore.
//
// That is invariant 33's pattern reapplied — Core holds the form's value while
// only a driver ever sets Core.Render — and it is what makes the padless
// surfaces padless by construction rather than by a check someone can forget:
// `gluon -e`, `gluon mcp` and the piped loop never touch a scratchpad because
// they never install one, and every existing test keeps a padless Core with no
// edit at all.
func (c *Core) OpenPad(name string) (Result, PadOpen) { return c.loadPad(name) }

// PadOpen is what opening a scratchpad did, as facts rather than as the
// sentence Result.Out carries.
//
// Two surfaces want the same thing in different shapes. `:scratch <name>` is
// typed mid-session and owes one line, and Result.Out is that line — read by a
// pipe, by gluon -e, by the -json envelopes and by every MCP tool, none of
// which can lay anything out. The startup screen has the whole screen and no
// reader parsing it, and the host is the fact that suffers most from arriving
// after a semicolon in a sentence about modules.
//
// So the sentence is built exactly as it always was and this is filled in
// beside it. Deriving one from the other was the alternative and it is the
// riskier trade: Result.Out is a frozen surface, invariant 30, and the way to
// keep frozen bytes frozen is not to rewrite the code that produces them.
type PadOpen struct {
	// Name is the pad as ValidPadName slugged it, which is what a row labels.
	Name string
	// State is the clause after the name, and nothing else: "empty", "1 entry
	// restored", "created, the session is empty". A row does not repeat the
	// name, because the label already said it — which was the whole complaint
	// about saying "scratchpad default" twice on consecutive lines.
	State string
	// Host is the module path the open left attached, empty when none, and
	// HostWhy is the clause beside it when something about the attachment is
	// worth saying — that it came from the command line rather than from what
	// the pad recorded, which is the one case where the path alone is a
	// half-truth.
	Host, HostWhy string
	// Notes is everything Out joins with "; ", minus the host, which the
	// screen gives a row of its own. What is left qualifies the open rather
	// than standing alone, so it stays in the pad row's own value.
	Notes []string
	// Failed reports that the pad is not being written to. Out carries the
	// whole reason — several lines, a path, sometimes a command to run — and a
	// caller prints it verbatim rather than reshaping it into rows, because
	// reshaping would lose the line somebody has to type.
	Failed bool
}

// loadPad opens a pad and replays it — the driver's open at startup, and
// :scratch <name> afterwards, which are the same operation from a different
// starting point.
func (c *Core) loadPad(name string) (Result, PadOpen) {
	slug, err := scratch.ValidPadName(name)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}, PadOpen{Failed: true}
	}
	po := PadOpen{Name: slug}

	p, err := scratch.ReadPad(slug)
	if err != nil {
		return c.padUnopened(slug, scratch.PadDir(slug), err.Error()), po.failed()
	}

	fresh := !scratch.IsPad(p.Dir)
	var notes []string
	// hostNote is which of the notes the host row takes over, or -1. It is an
	// index rather than a prefix test because two of the notes begin "attached
	// to " and only one of them is an attachment that happened — the other
	// carries the qualifier that makes it true, and matching on the prefix
	// would swallow exactly the half worth keeping.
	hostNote := -1

	// The host first, so a message about a host that has gone precedes the
	// build errors it explains.
	if p.Host != "" {
		switch {
		case c.ev.Host() != nil:
			// A host named on the command line wins: it is the more specific
			// answer, and it was typed today.
			notes = append(notes, "attached to "+c.ev.Host().Path+" from the command line, not the recorded "+p.Host)
			po.Host, po.HostWhy, hostNote = c.ev.Host().Path,
				"from the command line, not the recorded "+p.Host, len(notes)-1
		default:
			h, herr := host.Detect(p.Host)
			if herr != nil {
				notes = append(notes, "the recorded host "+p.Host+" is gone — opening standalone")
			} else if uerr := c.ev.UseHost(h); uerr != nil {
				notes = append(notes, "the recorded host "+p.Host+" would not attach ("+uerr.Error()+") — opening standalone")
			} else {
				notes = append(notes, "attached to "+h.Path)
				po.Host, hostNote = h.Path, len(notes)-1
			}
		}
	}

	if len(p.Requires) > 0 {
		if rerr := c.ev.Restore(p.Requires, p.Sum); rerr != nil {
			return c.padUnopened(p.Name, p.Dir, fmt.Sprintf("its modules could not be restored — %v", rerr)), po.failed()
		}
		// "Nothing was fetched" is only credible if the open says what it did
		// instead, so the count and the fact are on the same line.
		notes = append(notes, fmt.Sprintf("%s restored from the local cache, nothing fetched",
			plural(len(p.Requires), "module")))
	}

	if now := scratch.GoMinor(); p.Go != "" && p.Go != now {
		// A likely cause of a replay that used to work, named before the errors
		// rather than after them.
		notes = append(notes, "written with go "+p.Go+", running "+now)
	}

	c.gen++
	c.dropDetection()

	pinned := 0
	for _, e := range p.Sess.Entries {
		if e.Pinned {
			pinned++
		}
	}

	// An empty pad costs no evaluation, so landing on one is indistinguishable
	// in speed from the startup gluon has always had.
	if len(p.Sess.Entries) > 0 {
		res := c.swapSession(func() Result {
			// Installed as recorded rather than re-classified, for restore's
			// reason: pinning is a decision the session holds and Classify has
			// never seen. One ev.Eval for the whole session, so reopening costs
			// one build and not one per entry.
			c.sess.Entries = p.Sess.Entries
			if _, eerr := c.ev.Eval(c.sess); eerr != nil {
				return Result{Out: eerr.Error() + c.pinNote(eerr.Error()), Err: true}
			}
			return Result{}
		})
		if res.Err {
			why := "it no longer replays — " + res.Out
			// A module the cache no longer holds fails the build with the
			// toolchain's own words, which are about go.sum and not about
			// anything the user did. Recognising that and naming the line that
			// brings it back is the whole of what "nothing is fetched" costs.
			if lines := padGetLines(p.Requires, res.Out); lines != "" {
				why += "\n  " + lines
			}
			return c.padUnopened(p.Name, p.Dir, why), po.failed()
		}
	}

	// Opening a pad restores module requirements and attaches a host, so what
	// the session can see has changed — which restore never has to worry about,
	// because a snapshot cannot change the build list. Invariant 22 names
	// opening a pad as the third point activation may run, for this line.
	c.refreshPlugins()

	c.pad = &scratchpad{name: slug, dir: p.Dir, entries: len(p.Sess.Entries)}
	if fresh {
		// Created here rather than at the first line typed, because "the
		// scratchpad you are in" has to be listable before you have typed
		// anything into it.
		if werr := c.writePad(); werr != nil {
			// Reported rather than swallowed: a session that believes it is
			// being saved and is not is the failure this whole feature exists
			// to avoid.
			c.pad.readOnly = "scratchpad " + slug + " could not be created: " + werr.Error()
			c.pad.told = true
			return Result{Out: c.pad.readOnly, Err: true}, po.failed()
		}
	} else {
		c.rememberFile()
	}

	// po.State is the same clause the sentence is about to carry, kept as its
	// own string so a row can print it without the name in front of it.
	switch {
	case fresh:
		po.State = "created, the session is empty"
	case len(p.Sess.Entries) == 0:
		po.State = "empty"
	default:
		po.State = fmt.Sprintf("%s restored", entryWord(len(p.Sess.Entries)))
		if pinned > 0 {
			po.State += fmt.Sprintf(", %d pinned", pinned)
		}
	}
	for i, n := range notes {
		// The host has a row of its own. What is left qualifies the open — a
		// host that has gone, modules restored from the cache, a go directive
		// that moved — and stays in the pad row's value.
		if i != hostNote {
			po.Notes = append(po.Notes, n)
		}
	}

	head := "scratchpad " + slug + " — " + po.State
	if len(notes) > 0 {
		head += "; " + strings.Join(notes, "; ")
	}
	return Result{Out: head}, po
}

// failed marks an open that did not happen. The name survives it, because a
// caller still has to say which pad it was.
func (p PadOpen) failed() PadOpen { p.Failed = true; return p }

// padUnopened is every failure to open: unreadable, a newer format, modules
// that would not restore, a session that no longer replays.
//
// swapSession has already put the session back, so what is left to decide is
// what this session writes to now. A session that was in a pad stays in it: the
// user asked to move and could not, and the pad they are still in is still
// theirs. A session that was in none is held read-only against the pad that
// failed — not so it can be written, but so the reason can be repeated once
// when the next line lands, which is what stops a pad that opened empty from
// being replaced by whatever is typed next.
func (c *Core) padUnopened(name, dir, why string) Result {
	reason := "scratchpad " + name + " is not being written to: " + why
	out := reason + "\n  " + filepath.Join(dir, scratch.PadName) +
		"\n  `gluon scratch show " + name + "` reads it without running it"
	if c.pad != nil && c.pad.readOnly == "" {
		return Result{Out: out + "\n  this session is still writing to " + c.pad.name, Err: true}
	}
	// told stays false deliberately. The reason is in this answer, and it is
	// said once more when the next line lands — because the line somebody types
	// after a failed open is the one where they are about to assume it is being
	// saved.
	c.pad = &scratchpad{name: name, dir: dir, readOnly: reason}
	return Result{Out: out, Err: true}
}

// moduleTrouble is the toolchain's own vocabulary for "this module is not on
// this machine". It is matched rather than parsed: the words change between
// releases, and the answer this drives — naming the :get line — is right
// whenever any of them appears and harmless when none does.
var moduleTrouble = []string{
	"missing go.sum entry", "module lookup disabled", "cannot find module",
	"no required module provides", "is not in std",
	// The everyday face of a module that is gone: goimports cannot resolve the
	// qualifier, so the checker rejects the line before the build ever loads
	// the module graph. eval's own importProblemRe makes the same judgement
	// about the same words, and for the same reason.
	"undefined: ",
}

// padGetLines names the recorded modules a failed replay is likely about, each
// with the :get line that would restore it.
func padGetLines(reqs []string, msg string) string {
	if len(reqs) == 0 {
		return ""
	}
	line := func(r string) string {
		mod, ver, ok := strings.Cut(r, " ")
		if !ok {
			return ":get " + r
		}
		return ":get " + mod + "@" + ver
	}
	var named []string
	for _, r := range reqs {
		mod, _, _ := strings.Cut(r, " ")
		if strings.Contains(msg, mod) {
			named = append(named, line(r))
		}
	}
	if len(named) == 0 {
		// The module may not be named in the message — a build that failed
		// while loading the graph reports go.sum, not the import. Falling back
		// to all of them is right where being silent would leave the user with
		// a checksum error and no next step.
		matched := false
		for _, phrase := range moduleTrouble {
			if strings.Contains(msg, phrase) {
				matched = true
				break
			}
		}
		if !matched {
			return ""
		}
		for _, r := range reqs {
			named = append(named, line(r))
		}
	}
	return "nothing was fetched — this brings back what it recorded:\n  " + strings.Join(named, "\n  ")
}

// flushPad writes the pad once per submission, at the outermost level.
//
// res is nil for a caller with nothing to report through — EvalBatch, whose
// `gluon -e` driver installs no pad in the first place.
func (c *Core) flushPad(res *Result) {
	c.padDepth--
	if c.padDepth > 0 {
		return
	}
	note := c.persist()
	if note == "" || res == nil {
		return
	}
	if res.Out == "" {
		res.Out = note
		return
	}
	res.Out += "\n" + note
}

// persist writes the session to the pad, and answers with whatever the user has
// to be told about that.
//
// It renders first and returns without writing when the bytes equal what it
// last wrote. That is exact where a counter is not: :pin changes bytes, :use
// changes the header, :help changes nothing. Rendering is string concatenation
// over the entries — microseconds — and it happens after evaluation, never on
// the keystroke path.
func (c *Core) persist() string {
	p := c.pad
	if p == nil {
		return ""
	}
	if p.readOnly != "" {
		if p.told {
			return ""
		}
		p.told = true
		return p.readOnly
	}

	next, sum := c.padFile()
	if bytes.Equal(next.Marshal(), p.last) && bytes.Equal(sum, p.lastSum) {
		return ""
	}

	if note := c.padStandDown(); note != "" {
		return note
	}

	kept := ""
	if len(c.sess.Entries) < p.entries {
		// A change that shrinks the pad is :reset, :drop or an :edit that took
		// lines out, and across restarts those became destructive the moment
		// the session grew a disk. One file answers all of them.
		prev := filepath.Join(p.dir, scratch.PadPrevName)
		if err := os.Rename(filepath.Join(p.dir, scratch.PadName), prev); err == nil {
			kept = "the previous contents are in " + prev
		}
	}

	if err := c.writePad(); err != nil {
		// Reported rather than swallowed, unlike history.add: durability is the
		// whole point of the feature, so a session that believes it is being
		// saved and is not is the failure that matters.
		p.readOnly = "scratchpad " + p.name + " is not being written to: " + err.Error()
		p.told = true
		return p.readOnly
	}
	return kept
}

// padFile is what would be written now: the entries, and the surroundings it
// takes to put them back.
func (c *Core) padFile() (*scratch.Pad, []byte) {
	p := &scratch.Pad{
		Name: c.pad.name,
		Dir:  c.pad.dir,
		Go:   scratch.GoMinor(),
		Sess: c.sess,
	}
	if h := c.ev.Host(); h != nil {
		p.Host = h.Dir
	}
	if reqs, err := c.ev.Requires(); err == nil {
		p.Requires = reqs
	}
	// The sums are recorded only when there is a requirement of the session's
	// own to verify. Everything else in that file belongs to the attached host,
	// and writeMod puts it back on its own — a copy here would be a second one
	// to keep in step.
	if len(p.Requires) > 0 {
		if sum, err := c.ev.Sum(); err == nil {
			p.Sum = sum
		}
	}
	// A resolved DSN never enters this file because it never enters the
	// session: it reaches the child through its environment (invariants 24 and
	// 25), so the entries the user typed are the only thing here.
	return p, p.Sum
}

// writePad writes the file and records what it looks like afterwards.
func (c *Core) writePad() error {
	p := c.pad
	next, sum := c.padFile()
	if err := scratch.WritePad(next); err != nil {
		return err
	}
	p.last, p.lastSum = next.Marshal(), sum
	p.entries = len(c.sess.Entries)
	p.writes++
	c.rememberFile()
	return nil
}

// rememberFile records the size and mtime of the pad file, which is what the
// next write compares against.
func (c *Core) rememberFile() {
	p := c.pad
	if fi, err := os.Stat(filepath.Join(p.dir, scratch.PadName)); err == nil {
		p.size, p.mod = fi.Size(), fi.ModTime()
		if p.last == nil {
			// Opened rather than written: the file on disk is the baseline, so
			// a session that changes nothing rewrites nothing.
			if data, err := os.ReadFile(filepath.Join(p.dir, scratch.PadName)); err == nil {
				p.last = data
			}
			if data, err := os.ReadFile(filepath.Join(p.dir, scratch.PadSumName)); err == nil {
				p.lastSum = data
			}
		}
	}
}

// padStandDown is the between-processes rule: stat before each write, and when
// the file is not the one this session left, stop writing and say so.
//
// A pid lock file was the alternative and is worse: a crashed gluon leaves a
// stale lock that blocks the pad, liveness checks need a file per GOOS, and the
// failure it prevents is one this detects anyway — with nothing lost, because
// the loser keeps its session and is told how to keep it under another name.
func (c *Core) padStandDown() string {
	p := c.pad
	if p.mod.IsZero() {
		// Nothing has been stat-ed yet, so there is nothing to compare against.
		// This is a pad whose creation failed, not one that has been read: an
		// open records the file it found, which is what makes a second gluon
		// that wrote between the open and the first line detectable.
		return ""
	}
	fi, err := os.Stat(filepath.Join(p.dir, scratch.PadName))
	switch {
	case errors.Is(err, os.ErrNotExist):
		p.readOnly = "scratchpad " + p.name + " has been removed by something else — this session is no longer being written down"
	case err != nil:
		p.readOnly = "scratchpad " + p.name + " cannot be read: " + err.Error()
	case fi.Size() != p.size || !fi.ModTime().Equal(p.mod):
		p.readOnly = "another gluon has written scratchpad " + p.name +
			" — this session has stopped writing rather than overwrite it"
	default:
		return ""
	}
	p.told = true
	return p.readOnly + "\n  :scratch <name> keeps this session under another name"
}

// scratchCmd is :scratch — the pad this session is saved in, and the way
// between them.
func (c *Core) scratchCmd(arg string) Result {
	// Whatever this does, the list of pads completion holds may be out of
	// date after it: a pad opened, renamed or removed.
	delete(c.comp.values, cmdspec.Pads)
	flag, rest, ok := parseScratchArgs(arg)
	if !ok {
		if rest == "" {
			return c.scratchList()
		}
		res, _ := c.loadPad(rest)
		return res
	}

	switch flag {
	case "-off":
		return c.scratchOff()
	case "-rm":
		return c.scratchRemove(rest)
	case "-mv":
		return c.scratchRename(rest)
	default:
		return c.scratchEdit()
	}
}

// parseScratchArgs reads :scratch's one flag, when it leads. Anything else is
// a scratchpad's name, which is why `:scratch -h` used to open one called h.
func parseScratchArgs(arg string) (flag, rest string, ok bool) {
	return leadingFlag(strings.TrimSpace(arg), "-off", "-rm", "-mv", "-edit")
}

// noPad is what every flag but the listing answers in a session that is not in
// a pad. Naming the way to get one matters more than the refusal: `gluon -e`
// and a piped script are padless on purpose, and somebody meeting this in the
// REPL has typed -no-scratch or :scratch -off.
func (c *Core) noPad() Result {
	return Result{
		Out: "this session is not in a scratchpad — :scratch <name> opens one, " +
			"and `gluon -scratch <name>` starts in one",
		Err: true,
	}
}

func (c *Core) scratchOff() Result {
	if c.pad == nil {
		return c.noPad()
	}
	name := c.pad.name
	// The session is untouched and the file is left exactly as it is: what
	// detaching removes is the writer, not the work.
	c.pad = nil
	return Result{Out: "detached from scratchpad " + name +
		" — the session continues and nothing more is written down"}
}

func (c *Core) scratchEdit() Result {
	if c.pad == nil {
		return c.noPad()
	}
	p := filepath.Join(c.pad.dir, scratch.PadName)
	if _, err := os.Stat(p); err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	// The file itself rather than a copy: Reload routes a pad file through the
	// pad reader, so the directives — and the pins they carry — survive being
	// edited, which is the one thing :edit's own temp file cannot promise.
	return Result{Edit: p}
}

func (c *Core) scratchRename(arg string) Result {
	if c.pad == nil {
		return c.noPad()
	}
	to := strings.TrimSpace(arg)
	if to == "" {
		return Result{Out: "usage: :scratch -mv <name>   renames the scratchpad you are in", Err: true}
	}
	from := c.pad.name
	slug, err := scratch.RenamePad(from, to)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	c.pad.name, c.pad.dir = slug, scratch.PadDir(slug)
	// The file moved with the directory, so what was stat-ed is still what is
	// there; recording it again is what keeps the next write from reading the
	// move as a second gluon.
	c.rememberFile()
	if slug != to {
		return Result{Out: fmt.Sprintf("scratchpad %s → %s (%s is the slug of %q)", from, slug, slug, to)}
	}
	return Result{Out: fmt.Sprintf("scratchpad %s → %s", from, slug)}
}

// scratchRemove is :scratch -rm, which shows what would go before it goes.
//
// The confirmation is :replay's shape and for :replay's reason: what is about
// to be destroyed is printed verbatim rather than counted, because a count is
// not something a reader can check. Removal is typed rather than offered inside
// the listing because a row cannot ask a question — ModalConfirm is documented
// as the decision the whole view exists to ask, one per view and not one per
// row, and a removal chosen from a row would put "removing needs confirming"
// into a footer with no key to press.
func (c *Core) scratchRemove(arg string) Result {
	rest, forced := cutFlag(strings.TrimSpace(arg), "-force")
	name := strings.TrimSpace(rest)
	if name == "" {
		return Result{Out: "usage: :scratch -rm <name>   shows it, then asks", Err: true}
	}
	slug, err := scratch.ValidPadName(name)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	if c.pad != nil && c.pad.name == slug {
		return Result{Out: "scratchpad " + slug + " is the one this session is in — " +
			":scratch <other> moves first, and then it can go", Err: true}
	}
	p, err := scratch.ReadPad(slug)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	if !scratch.IsPad(p.Dir) {
		return Result{Out: "no scratchpad named " + slug, Err: true}
	}

	if forced {
		if err := scratch.RemovePad(slug); err != nil {
			return Result{Out: "error: " + err.Error(), Err: true}
		}
		return Result{Out: fmt.Sprintf("removed scratchpad %s (%s)", slug, entryWord(len(p.Sess.Entries)))}
	}

	listing := padListing(slug, p)
	run := ":scratch -rm -force " + slug
	if !c.Rich {
		// The useful degradation is :replay's: showing what would go carries no
		// risk and is most of the value, so a pipe gets the lines and the line
		// that removes them rather than nothing at all.
		return Result{Out: listing + "\nconfirming needs a terminal — " + run + " removes it without asking"}
	}
	return Result{
		Out: listing + "\nenter removes it; nothing has been removed yet",
		Modal: &ModalSpec{
			Title:   "scratchpad " + slug,
			Summary: fmt.Sprintf("%s in %s — nothing removed", entryWord(len(p.Sess.Entries)), slug),
			Headers: []string{"#", "line"},
			Rows:    padRows(p),
			Note:    "this is the whole scratchpad — removing it cannot be undone",
			Confirm: &ModalConfirm{Label: "remove this scratchpad", Run: run},
		},
	}
}

// padListing is the pad's own lines, numbered, in the linear form Out carries.
func padListing(name string, p *scratch.Pad) string {
	var b strings.Builder
	fmt.Fprintf(&b, "scratchpad %s holds %s:\n", name, entryWord(len(p.Sess.Entries)))
	for i, e := range p.Sess.Entries {
		mark := " "
		if e.Pinned {
			mark = "*"
		}
		fmt.Fprintf(&b, "%3d %s %s\n", i+1, mark, flattenForHistory(e.Src))
	}
	return strings.TrimRight(b.String(), "\n")
}

func padRows(p *scratch.Pad) [][]string {
	rows := make([][]string, len(p.Sess.Entries))
	for i, e := range p.Sess.Entries {
		rows[i] = []string{fmt.Sprintf("%d", i+1), flattenForHistory(e.Src)}
	}
	return rows
}

// scratchList is the bare :scratch.
//
// Out and Modal are built from one row builder, so invariant 19 holds by
// construction rather than by care — settingsList's argument, and the same
// shape: the two cannot say different things because there is one place that
// decides what they say.
func (c *Core) scratchList() Result {
	headers, rows, names := c.padRowsAll()
	var b strings.Builder
	b.WriteString("scratchpads\n")
	b.WriteString(settingsTable(headers, rows, c.styles()).Render())
	b.WriteString("\n" + c.padFooter())

	entries := make([]ModalEntry, len(names))
	for i, n := range names {
		entries[i] = ModalEntry{
			Title: n,
			Text:  "Open " + n + ". The session becomes its entries, replayed as one program.",
			// A row hands back the command that opens it rather than opening
			// it: Core belongs to the evaluation goroutine, so a view holding a
			// callback into it would be reading the session from the wrong one.
			Choices: []ModalChoice{{Label: "open " + n, Run: ":scratch " + n}},
		}
	}
	return Result{
		Out: b.String(),
		Modal: &ModalSpec{
			Title:   "scratchpads",
			Summary: fmt.Sprintf("%s  browsed", plural(len(rows), "scratchpad")),
			Headers: headers,
			Rows:    rows,
			Entries: entries,
			// The view can open a pad, so what it shows afterwards has to come
			// from the directory again rather than from what it opened with.
			Refresh: ":scratch",
			Note:    c.padFooter(),
		},
	}
}

// padRowsAll is the one description of the rows both forms are built from.
func (c *Core) padRowsAll() (headers []string, rows [][]string, names []string) {
	headers = []string{"scratchpad", "entries", "host", "last worked on"}
	for _, info := range scratch.Pads() {
		name := info.Name
		if c.pad != nil && c.pad.name == name {
			// The one the session is in, marked in the column that is read
			// first — a marker in a trailing column is one nobody scans for.
			name = "* " + name
		}
		entries := entryWord(info.Entries)
		if info.Pinned > 0 {
			entries += fmt.Sprintf(" (%d pinned)", info.Pinned)
		}
		host := info.Host
		if host == "" {
			host = "—"
		}
		rows = append(rows, []string{name, entries, host, padWhen(info.Mod)})
		names = append(names, info.Name)
	}
	return headers, rows, names
}

// padWhen is how long ago a pad was last written, which is what "have I touched
// this since Tuesday" actually asks. An absolute timestamp answers a different
// question and is longer.
func padWhen(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d/time.Minute), "minute") + " ago"
	case d < 24*time.Hour:
		return plural(int(d/time.Hour), "hour") + " ago"
	case d < 30*24*time.Hour:
		return plural(int(d/(24*time.Hour)), "day") + " ago"
	default:
		return t.Format("2006-01-02")
	}
}

func (c *Core) padFooter() string {
	// Shortened as the settings view's footer shortens its file: the
	// directory is worth naming, and an absolute path is not worth a line.
	root := shortenPath(scratch.Root(), c)
	if c.pad == nil {
		return "not in a scratchpad — :scratch <name> opens one · " + root
	}
	if c.pad.readOnly != "" {
		return c.pad.readOnly + " · " + root
	}
	return "in " + c.pad.name + " — every line lands in it · " + root
}
