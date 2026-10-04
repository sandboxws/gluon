//go:build integration

package repl

import (
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/release"
)

// internal/release already proves each snippet compiles at the directive it
// claims. What it cannot prove is that the REPL will take it: a snippet is fed
// through Submit one construct at a time, so a line the session classifies
// differently, or a declaration that will not hoist, fails here and nowhere
// else. This is the half that runs :since -run for real.
func TestEveryNoteRunsInARealSession(t *testing.T) {
	rels, err := releases()
	if err != nil {
		t.Skip("no toolchain release data:", err)
	}
	for _, r := range rels {
		for _, n := range r.Lang {
			t.Run(r.Version+"/"+n.Name, func(t *testing.T) {
				c := testCore(t)
				if why := c.cannotRun(n); why != "" {
					t.Skipf("no go toolchain new enough: %s", why)
				}
				res := c.since(r.Version + " -run " + n.Name)
				if res.Err {
					t.Fatalf("%s did not run:\n%s", n.Name, res.Out)
				}
				if !strings.Contains(res.Out, n.Title) {
					t.Errorf("the answer does not name the note:\n%s", res.Out)
				}
				if !strings.Contains(res.Out, n.Snippet) {
					t.Errorf("the answer does not show what it ran:\n%s", res.Out)
				}
			})
		}
	}
}

// Running a note puts it in the session, which is what makes it a REPL feature
// rather than a viewer — and is also the thing the footer warns about, so it
// had better be true.
func TestRunningANoteLandsInTheSession(t *testing.T) {
	c := testCore(t)
	rels, err := releases()
	if err != nil {
		t.Skip("no toolchain release data:", err)
	}
	r, n, ok := firstRunnableNote(c, rels)
	if !ok {
		t.Skip("no runnable note on this toolchain")
	}
	before := len(c.sess.Entries)
	if res := c.since(r.Version + " -run " + n.Name); res.Err {
		t.Fatalf("%s did not run:\n%s", n.Name, res.Out)
	}
	if after := len(c.sess.Entries); after <= before {
		t.Errorf("session went from %d to %d entries — the note did not land", before, after)
	}
}

func firstRunnableNote(c *Core, rels []release.Release) (release.Release, release.Note, bool) {
	for _, r := range rels {
		for _, n := range r.Lang {
			if c.cannotRun(n) == "" {
				return r, n, true
			}
		}
	}
	return release.Release{}, release.Note{}, false
}
