package repl

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sandboxws/gluon/internal/find"
)

// reloadHost is :reload — Rails' reload!, for the one thing gluon actually
// keeps across an edit.
//
// The session's entries are what the user typed and are none of this command's
// business; :edit already owns changing those. What goes stale when a host's
// source changes is everything gluon derived from it, and invariant 18 names
// the three pieces. Nothing is built and nothing is run: the fix for state
// that would have been reused is to stop reusing it.
//
// refreshPlugins is deliberately not called. Plugin activation runs at :get
// and :use only — invariant 22 — and those are the two points where the build
// list changes. A source edit does not change what the session can link.
func (c *Core) reloadHost() Result {
	h := c.ev.Host()
	if h == nil {
		return Result{Out: "no host attached — nothing to reload. :use <dir> attaches one"}
	}
	ix, err := c.ev.Reload()
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	// The checker is new, so a completion built from the old one describes a
	// scope that no longer exists.
	c.gen++
	// And where the database is, which detect() otherwise keeps for as long as
	// the files it read are unchanged. A reload — typed, or reported by the
	// watcher through ReloadOnChange — is the session saying the host moved,
	// which is the one thing a stamp of the old file list cannot notice: a
	// different checkout at the same path reads as the same project.
	c.dropDetection()
	return Result{Out: fmt.Sprintf(
		"reloaded %s\n  dropped: the type checker's export data, the import cache, every cached result\n  %d importable package(s)",
		h.Dir, ix.Total())}
}

// ReloadOnChange is the reload a driver runs when the watcher reports one.
//
// The observation is printed above the reload's own report, because a session
// that changes its answers without being asked has to say why. That is the
// whole difference between a helpful watcher and an unexplained one.
func (c *Core) ReloadOnChange(what string) Result {
	res := c.reloadHost()
	if what == "" {
		return res
	}
	res.Out = what + "\n" + res.Out
	return res
}

// refresh is :refresh — run the session once with the pins lifted.
//
// A pinned entry is one the user said had already happened, so replay stops
// running it. That is exactly right until the source underneath it changes, at
// which point the session is carrying a result computed against code that no
// longer exists. :refresh is the way to bring it back in step without
// unpinning by hand and pinning again afterwards.
//
// It is a separate command from :reload rather than a flag on it because the
// two answer different questions: :reload drops caches and runs nothing,
// :refresh runs the program. Putting "does this execute?" behind a flag is the
// one distinction the static and eval tiers turn on.
func (c *Core) refresh() Result {
	was := make([]bool, len(c.sess.Entries))
	pinned := 0
	for i, e := range c.sess.Entries {
		was[i] = e.Pinned
		if e.Pinned {
			pinned++
		}
	}
	if pinned == 0 {
		return Result{Out: "nothing is pinned — :refresh re-runs what :pin took out of the replay"}
	}

	// Restored in a defer rather than on the way out, so a build failure, an
	// error return and a panic all leave the pins as they were found.
	// EvalTransient snapshots its own state the same way, for the same reason:
	// the requirement holds by construction instead of by remembering to
	// handle the error branch.
	defer func() {
		for i := 0; i < len(was) && i < len(c.sess.Entries); i++ {
			c.sess.Entries[i].Pinned = was[i]
		}
	}()
	for i := range c.sess.Entries {
		c.sess.Entries[i].Pinned = false
	}

	c.gen++
	res, err := c.ev.EvalFresh(c.sess)
	if err != nil {
		return Result{Out: "error: " + err.Error() + c.pinNote(err.Error()), Err: true}
	}
	// db.go's plural helper pluralises by adding an s, which "entry" does not.
	noun := "entries ran"
	if pinned == 1 {
		noun = "entry ran"
	}
	head := fmt.Sprintf("refreshed — %d pinned %s again", pinned, noun)
	out := c.format(res.Output)
	if res.ExitCode != 0 {
		return Result{Out: strings.TrimRight(head+"\n"+out, "\n") +
			fmt.Sprintf("\n[exit status %d]", res.ExitCode), Err: true}
	}
	if out == "" {
		return Result{Out: head}
	}
	return Result{Out: head + "\n" + out}
}

// watch is :watch [on|off] — poll the attached host and reload when its source
// moves.
//
// Polling rather than a filesystem-notification library: constraint K forbids a
// new dependency without an issue first, and `gluon watch` already stats on an
// interval for the same job.
func (c *Core) watch(arg string) Result {
	switch strings.TrimSpace(arg) {
	case "":
		return Result{Out: c.watchState()}

	case "off":
		if c.watcher == nil {
			return Result{Out: "not watching"}
		}
		dir := c.watcher.dir
		c.StopWatch()
		return Result{Out: "watching off — " + dir + " is no longer polled"}

	case "on":
		h := c.ev.Host()
		if h == nil {
			return Result{Out: "no host attached — nothing to watch. :use <dir> attaches one"}
		}
		if c.watcher != nil {
			return Result{Out: "already watching " + c.watcher.dir}
		}
		fp := scanHost(h.Dir)
		w := &watcher{dir: h.Dir, out: c.changes, stop: make(chan struct{})}
		c.watcher = w
		go w.run(fp)

		out := fmt.Sprintf("watching %s — %d Go file(s), every %s", h.Dir, len(fp.files), watchInterval)
		if fp.truncated {
			// Watching a subset in silence would make the failure look like
			// the feature simply not working.
			out += fmt.Sprintf("\n  stopped after %d files: the rest is not watched", maxWatched)
		}
		// Watch tells a driver that this command needs an event loop. Only a
		// driver that has one can deliver the change; the pipe answers it the
		// way it answers :edit.
		return Result{Out: out, Watch: true}
	}
	return Result{Out: "usage: :watch [on|off]", Err: true}
}

// watchState answers a bare :watch.
func (c *Core) watchState() string {
	if c.watcher == nil {
		return "not watching — :watch on polls the attached host and reloads when its source changes"
	}
	return fmt.Sprintf("watching %s every %s — :watch off stops", c.watcher.dir, watchInterval)
}

// StopWatch turns polling off. It is idempotent, and Close calls it, so the
// goroutine cannot outlive the session that started it.
func (c *Core) StopWatch() {
	if c.watcher == nil {
		return
	}
	close(c.watcher.stop)
	c.watcher = nil
}

// Watching reports whether the poll loop is running.
func (c *Core) Watching() bool { return c.watcher != nil }

// Changes is the stream the watcher writes and a driver with an event loop
// reads. A nil channel blocks forever, which is what a driver listening on a
// Core that will never watch should do.
func (c *Core) Changes() <-chan hostChange { return c.changes }

// hostChange is one observation: the attached host's Go source is not what it
// was when it was last looked at.
type hostChange struct {
	// What names the change in terms the user can recognise — a path relative
	// to the host module. The spec's rule is that a reload nobody asked for is
	// never unexplained, and "something changed" does not explain it.
	What string
}

// watchInterval is how often the loop stats the host.
//
// 300ms, the number `gluon watch` already chose for the same job
// (cmd/gluon/scratch.go): a stat loop over a tree costs far less than the
// build it triggers. A second, different interval for the same question would
// be an unexplained inconsistency.
const watchInterval = 300 * time.Millisecond

// maxWatched bounds a tick, the way maxFiles bounds the database detector's
// walk. A large host cannot make polling expensive, and when the cap is hit
// :watch says so rather than watching a subset in silence.
const maxWatched = 2000

// watchDepth is find.Walk's depth argument. It is deep enough that no real
// module tree reaches it; the bound that does the work is maxWatched.
const watchDepth = 32

// watcher polls one directory and reports that its Go source moved.
//
// It touches the filesystem and its own channel, and nothing else. Core is not
// safe for concurrent use — invariant 15, which already forbids asking for
// completion while an evaluation runs — so the goroutine emits an event and
// the driver serializes the reload with every other input, exactly as it
// serializes a keystroke. That is one rule rather than a second locking scheme.
type watcher struct {
	dir  string
	out  chan<- hostChange
	stop chan struct{}
}

// run polls until stop is closed. prev is the fingerprint taken when watching
// was turned on, so an edit made between the attach and the :watch is seen.
func (w *watcher) run(prev fingerprint) {
	t := time.NewTicker(watchInterval)
	defer t.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-t.C:
			cur := scanHost(w.dir)
			if cur.sum == prev.sum {
				continue
			}
			what := cur.since(prev)
			prev = cur
			// Blocking here is deliberate. The driver takes one event at a
			// time, and the next tick re-reads the tree, so an edit that lands
			// while this waits is still observed — no event is dropped and
			// none is queued behind a stale one.
			select {
			case w.out <- hostChange{What: what}:
			case <-w.stop:
				return
			}
		}
	}
}

// fingerprint is the state of a host's Go source at one moment.
type fingerprint struct {
	// files maps a path relative to the host onto its stamp. It is kept
	// beside sum so a change can be named and not merely noticed.
	files map[string]string
	// sum is the digest of the whole set. One string comparison per tick is
	// what keeps the loop cheaper than what it triggers.
	sum string
	// truncated reports that maxWatched was reached.
	truncated bool
}

// scanHost fingerprints the .go files under dir.
//
// Only .go files: nothing else changes what the session compiles against, and
// go.mod changing is :use's job rather than this one's.
//
// The walk starts at the host module and never climbs, which is invariant 12's
// rule; it prunes node_modules, .git and the rest of find.Skip's list, and it
// stops at a nested go.mod because that is a different project. It walks to
// the files rather than trusting directory mtimes — a directory's own mtime
// does not move when a file inside it is edited, the trap scratch.List
// documents.
func scanHost(dir string) fingerprint {
	fp := fingerprint{files: map[string]string{}}
	_ = find.Walk(dir, watchDepth, func(p string, d fs.DirEntry) error {
		if len(fp.files) >= maxWatched {
			fp.truncated = true
			return fs.SkipAll
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			// A file that vanished between the walk and the stat is a change
			// the next tick sees; it is not worth failing the scan over.
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			rel = p
		}
		fp.files[filepath.ToSlash(rel)] = stamp(fi.Size(), fi.ModTime())
		return nil
	})
	fp.sum = digest(fp.files)
	return fp
}

// stamp is what a file's identity is taken to be: its size and its
// modification time.
//
// Hashing contents would be exact and would cost a read of every file on every
// tick. (size, mtime) is what build systems use for the same trade, and the
// miss it admits — a same-size edit inside the clock's resolution — is caught
// by the next one.
func stamp(size int64, mod time.Time) string {
	return strconv.FormatInt(size, 10) + ":" + strconv.FormatInt(mod.UnixNano(), 10)
}

// digest reduces a file set to one comparable string.
func digest(files map[string]string) string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		fmt.Fprintf(h, "%s\x00%s\x00", p, files[p])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// since names what moved between prev and cur.
func (cur fingerprint) since(prev fingerprint) string {
	var changed []string
	for p, s := range cur.files {
		if prev.files[p] != s {
			changed = append(changed, p)
		}
	}
	for p := range prev.files {
		if _, still := cur.files[p]; !still {
			changed = append(changed, p+" (removed)")
		}
	}
	sort.Strings(changed)
	switch len(changed) {
	case 0:
		return "the host's source changed"
	case 1:
		return changed[0] + " changed"
	default:
		return fmt.Sprintf("%s changed, and %d other file(s)", changed[0], len(changed)-1)
	}
}
