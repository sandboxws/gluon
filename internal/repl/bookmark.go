package repl

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/sandboxws/gluon/internal/session"
)

// A snapshot is what the session looked like when it was named.
//
// Snapshots live for the process and no longer, and that is now a decision
// rather than a limitation: :scratch makes the session durable for *every*
// command — :pin, :drop, :undo, :get and :use all land in the same file — which
// is precisely the inconsistency a durable bookmark would have introduced.
//
// The three costs this comment used to name against persistence are the reasons
// they stay per-process. A location: the scratchpad owns one, and a second
// durable store of the same shape in the same tree is what there is no room
// for. An eviction policy: a pad exists only when somebody typed its name and
// :scratch -rm removes it, while :branch auto-names — durable auto-named
// snapshots are exactly the thing that would need an eviction policy nothing
// here has. And a second format: the pad's sidecar is not a program and
// competes with nothing.
//
// A snapshot is a point inside one editing session; a scratchpad is how you
// keep one past today. Each command's Detail says so, because a bookmark
// mistaken for saved work is work lost.
type snapshot struct {
	// sess is a deep copy, so the live session can go on being edited.
	sess *session.Session
	// seq orders the listing by when each was taken. Names are a map, and a
	// listing that came out in map order would be a different listing every
	// time it was asked for.
	seq int
}

// bookmarks is the per-process store, keyed by name.
type bookmarks struct {
	by map[string]snapshot
	// next is both the sequence counter and what auto-naming counts with, so
	// two branches taken in one session never collide.
	next int
}

// put records sess under name, reporting whether it replaced one. The session
// is cloned here rather than by the caller: a store that held a live pointer
// would be a store whose contents changed under it.
func (b *bookmarks) put(name string, sess *session.Session) (replaced bool) {
	if b.by == nil {
		b.by = map[string]snapshot{}
	}
	old, replaced := b.by[name]
	seq := b.next
	if replaced {
		// Replacing keeps the original position in the listing: the name has
		// been there since it was first taken, and a listing that reordered
		// itself on every re-bookmark would be hard to read down.
		seq = old.seq
	}
	b.next++
	b.by[name] = snapshot{sess: sess.Clone(), seq: seq}
	return replaced
}

// get is the snapshot under name, deep-copied again on the way out. Restoring
// the same name twice has to give the same session both times, which it would
// not if the caller were handed the stored slice to evaluate against.
func (b *bookmarks) get(name string) (*session.Session, bool) {
	s, ok := b.by[name]
	if !ok {
		return nil, false
	}
	return s.sess.Clone(), true
}

// names is every snapshot taken, oldest first.
func (b *bookmarks) names() []string {
	out := make([]string, 0, len(b.by))
	for name := range b.by {
		out = append(out, name)
	}
	sort.Slice(out, func(i, j int) bool { return b.by[out[i]].seq < b.by[out[j]].seq })
	return out
}

// autoName is the name :branch generates when it was not given one. It counts
// with the same counter the store orders by, so a generated name is never one
// a later branch reuses, and it is short because the whole point is that it can
// be typed back into :restore.
func (b *bookmarks) autoName() string {
	for {
		name := "b" + strconv.Itoa(b.next+1)
		if _, taken := b.by[name]; !taken {
			return name
		}
		// A user who typed :bookmark b2 by hand owns that name. Skip past it
		// rather than replacing a snapshot they took deliberately.
		b.next++
	}
}

// entryWord is "1 entry" or "N entries", because a count that reads "1 entries"
// in a message about the user's own session is the kind of thing that makes the
// rest of the output look unconsidered.
func entryWord(n int) string {
	if n == 1 {
		return "1 entry"
	}
	return fmt.Sprintf("%d entries", n)
}

// bookmark is :bookmark — record the session under a name, or list what has
// been recorded.
func (c *Core) bookmark(arg string) Result {
	name := strings.TrimSpace(arg)
	if name == "" {
		return Result{Out: c.listBookmarks()}
	}
	if strings.Fields(name)[0] != name {
		return Result{Out: "usage: :bookmark [name]   a name is one word", Err: true}
	}

	replaced := c.marks.put(name, c.sess)
	what := fmt.Sprintf("%s: %s, for this session", name, entryWord(len(c.sess.Entries)))
	if replaced {
		return Result{Out: what + " — replaced the snapshot that was under that name"}
	}
	return Result{Out: what}
}

// listBookmarks is the bare :bookmark, and the tail of a failed :restore.
func (c *Core) listBookmarks() string {
	names := c.marks.names()
	if len(names) == 0 {
		return "no snapshot taken this session — :bookmark <name> takes one"
	}
	var b strings.Builder
	b.WriteString("snapshots, for this session only:\n")
	for _, n := range names {
		fmt.Fprintf(&b, "  %s  %s\n", n, entryWord(len(c.marks.by[n].sess.Entries)))
	}
	b.WriteString("  :restore <name> goes back to one; :scratch keeps a session past this process")
	return b.String()
}

// branch is :branch — the same snapshot :bookmark takes, named for you.
//
// It is a separate command rather than a flag because the thing it removes is
// having to decide on a name before you know whether you will want the state
// back. A bare :bookmark lists; a bare :branch has to snapshot, and one bare
// invocation that did either depending on context is the overload that makes a
// command unpredictable.
func (c *Core) branch(arg string) Result {
	name := strings.TrimSpace(arg)
	if name == "" {
		name = c.marks.autoName()
	} else if strings.Fields(name)[0] != name {
		return Result{Out: "usage: :branch [name]   a name is one word", Err: true}
	}

	replaced := c.marks.put(name, c.sess)
	what := fmt.Sprintf("%s: %s, for this session", name, entryWord(len(c.sess.Entries)))
	if replaced {
		what += " — replaced the snapshot that was under that name"
	}
	// The name is in the line whether it was typed or generated, and so is the
	// command that comes back to it: a generated name nobody was told is a
	// snapshot nobody can reach.
	return Result{Out: what + "\nthe session is unchanged; :restore " + name + " comes back here"}
}

// restore is :restore — put a snapshot back and replay it.
//
// The replay goes through swapSession, which is the path :edit's reload already
// uses: it is the one place that knows how to put the previous entries back
// when the new ones will not run, and getting that wrong destroys the session
// the user was trying to leave.
func (c *Core) restore(arg string) Result {
	name := strings.TrimSpace(arg)
	if name == "" {
		return Result{Out: "usage: :restore <name>   the name :bookmark shows", Err: true}
	}
	snap, ok := c.marks.get(name)
	if !ok {
		return Result{Out: "no snapshot named " + name + "\n" + c.listBookmarks(), Err: true}
	}

	// The session is being replaced wholesale, so anything derived from
	// reading the host is as stale as it is after :edit's reload.
	c.gen++
	c.dropDetection()

	res := c.swapSession(func() Result {
		// The entries are installed as recorded — order and pinned state
		// included — rather than re-classified from their source, because
		// pinning is a decision the session holds and Classify has never seen.
		c.sess.Entries = snap.Entries
		if _, err := c.ev.Eval(c.sess); err != nil {
			return Result{Out: "error: " + err.Error() + c.pinNote(err.Error()), Err: true}
		}
		return Result{}
	})
	if res.Err {
		return res
	}
	return Result{Out: fmt.Sprintf("restored %s (%s)", name, entryWord(len(c.sess.Entries)))}
}

// replay is :replay — find lines in the persisted history and bring them into
// the session, after showing what they are.
//
// The confirmation is the point. A history line is a line somebody typed, some
// of them wrote a file or made a request, and the session replays every entry
// on every subsequent evaluation — so three lines brought in unread can be
// three writes, repeated for the rest of the session. What is about to happen
// is shown verbatim rather than summarised, because a count is not something a
// reader can check.
func (c *Core) replay(arg string) Result {
	pattern, run := cutFlag(strings.TrimSpace(arg), "-run")
	if pattern == "" {
		return Result{Out: "usage: :replay [-run] <pattern>   -run brings them in unasked", Err: true}
	}

	lines, err := searchHistory(pattern)
	if err != nil {
		// Distinct from matching nothing, and deliberately so: "gluon could
		// not look" and "there is no such line" are different answers, and
		// only the second is about what you typed.
		return Result{Out: "error: history is unavailable — " + err.Error(), Err: true}
	}
	if len(lines) == 0 {
		return Result{Out: fmt.Sprintf("no history line matches %q — nothing was run", pattern)}
	}

	if run {
		return c.replayLines(lines)
	}

	listing := replayListing(pattern, lines)
	if !c.Rich {
		// The useful degradation is not :edit's refusal. Listing what would be
		// brought in carries no risk and is most of the value, so a pipe gets
		// the list and the line that would bring them in — rather than nothing
		// at all because it cannot hold a confirmation.
		return Result{Out: listing + "\nconfirming needs a terminal — :replay -run " +
			pattern + " brings them in without asking"}
	}
	// Out carries the same list linearly whatever the driver does with Modal:
	// invariant 19, so a driver that cannot go full-screen loses nothing.
	return Result{
		Out: listing + "\nenter brings them in; nothing has run yet",
		Modal: &ModalSpec{
			Title:   fmt.Sprintf("history matching %q", pattern),
			Summary: fmt.Sprintf("%s matched %q — nothing brought in", plural(len(lines), "line"), pattern),
			Headers: []string{"#", "line"},
			Rows:    replayRows(lines),
			Note:    "nothing has run yet — these were typed in earlier sessions, and some of them may have had effects",
			Confirm: &ModalConfirm{
				Label: "bring these in",
				Run:   ":replay -run " + pattern,
			},
		},
	}
}

// replayRows numbers the matches for the table. The numbers are the reading
// order, not :hist's — nothing here is in the session yet.
func replayRows(lines []string) [][]string {
	rows := make([][]string, len(lines))
	for i, line := range lines {
		rows[i] = []string{strconv.Itoa(i + 1), line}
	}
	return rows
}

// replayListing is the same matches in the linear form Out carries.
func replayListing(pattern string, lines []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s matching %q, oldest first:\n", plural(len(lines), "history line"), pattern)
	for i, line := range lines {
		fmt.Fprintf(&b, "%3d  %s\n", i+1, line)
	}
	return strings.TrimRight(b.String(), "\n")
}

// replayLines brings the matches into the session, in the order the history
// holds them.
//
// submitAll is what :load and :edit's reload already use, so a construct that
// arrived as one history line goes in as one entry and the order is the file's.
// It stops at the first line that will not run, exactly as :load does: the
// lines before it are real entries of a real session, and rolling them back
// would be undoing work the user watched succeed.
func (c *Core) replayLines(lines []string) Result {
	before := len(c.sess.Entries)
	res := c.submitAll(strings.Join(lines, "\n"))
	brought := len(c.sess.Entries) - before

	head := fmt.Sprintf("brought in %s from history", plural(brought, "line"))
	if res.Err {
		head = fmt.Sprintf("brought in %s of %s before one would not run",
			plural(brought, "line"), plural(len(lines), "match"))
	}
	if res.Out == "" {
		return Result{Out: head, Err: res.Err}
	}
	return Result{Out: head + "\n" + res.Out, Err: res.Err}
}
