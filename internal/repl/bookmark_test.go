package repl

import (
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/session"
)

// These drive the commands rather than the store: what the user is told about a
// snapshot is as much of the feature as what the snapshot holds — a bookmark
// mistaken for saved work is work lost.

func TestBookmarkRecordsAndCounts(t *testing.T) {
	c := testCore(t)
	c.Submit("x := 1")
	c.Submit("x + 1")

	res := c.Submit(":bookmark before")
	if res.Err {
		t.Fatalf(":bookmark before: %s", res.Out)
	}
	if !strings.Contains(res.Out, "before") || !strings.Contains(res.Out, "2 entries") {
		t.Errorf("snapshot line does not name it and count it: %q", res.Out)
	}
	// A snapshot that looked durable and was not would lose work.
	if !strings.Contains(res.Out, "this session") {
		t.Errorf("snapshot line does not say it is for this session: %q", res.Out)
	}
}

func TestBareBookmarkLists(t *testing.T) {
	c := testCore(t)
	c.Submit("x := 1")
	c.Submit(":bookmark one")
	c.Submit("y := 2")
	c.Submit(":bookmark two")

	out := c.Submit(":bookmark").Out
	for _, want := range []string{"one", "1 entry", "two", "2 entries"} {
		if !strings.Contains(out, want) {
			t.Errorf("listing is missing %q:\n%s", want, out)
		}
	}
	// Oldest first, so the list reads in the order the snapshots were taken
	// rather than in whatever order the map iterated.
	if strings.Index(out, "one") > strings.Index(out, "two") {
		t.Errorf("listing is not in the order they were taken:\n%s", out)
	}
}

func TestBareBookmarkOnACleanSession(t *testing.T) {
	c := testCore(t)
	out := c.Submit(":bookmark").Out
	if !strings.Contains(out, "no snapshot taken") {
		t.Errorf("bare :bookmark with none taken: %q", out)
	}
}

// TestBookmarkSaysItReplaced: a name silently overwritten is a snapshot the
// user still believes they have.
func TestBookmarkSaysItReplaced(t *testing.T) {
	c := testCore(t)
	c.Submit("x := 1")
	c.Submit(":bookmark here")
	c.Submit("y := 2")

	res := c.Submit(":bookmark here")
	if res.Err {
		t.Fatalf("re-bookmarking: %s", res.Out)
	}
	if !strings.Contains(res.Out, "replaced") {
		t.Errorf("replacing a name does not say so: %q", res.Out)
	}
	if out := c.Submit(":bookmark").Out; !strings.Contains(out, "2 entries") {
		t.Errorf("the snapshot was not replaced:\n%s", out)
	}
	if n := strings.Count(c.Submit(":bookmark").Out, "here"); n != 1 {
		t.Errorf("replacing left %d entries named here, want 1", n)
	}
}

// TestBookmarkLeavesTheSessionAlone: taking a snapshot is a copy, and a copy
// that moved what it copied would be the one thing a snapshot must not do.
func TestBookmarkLeavesTheSessionAlone(t *testing.T) {
	c := testCore(t)
	c.Submit("x := 1")
	c.Submit("y := 2")
	if res := c.Submit(":pin 1"); res.Err {
		t.Fatalf(":pin 1: %s", res.Out)
	}
	before := c.sess.Clone().Entries

	if res := c.Submit(":bookmark mark"); res.Err {
		t.Fatalf(":bookmark mark: %s", res.Out)
	}

	after := c.sess.Entries
	if len(after) != len(before) {
		t.Fatalf("session has %d entries after a snapshot, had %d", len(after), len(before))
	}
	for i := range before {
		if after[i].Src != before[i].Src || after[i].Pinned != before[i].Pinned {
			t.Errorf("entry %d changed: %+v, was %+v", i+1, after[i], before[i])
		}
	}
}

func TestBranchNamesItselfAndRestoresBack(t *testing.T) {
	c := testCore(t)
	c.Submit("x := 1")

	res := c.Submit(":branch")
	if res.Err {
		t.Fatalf(":branch: %s", res.Out)
	}
	// The generated name has to be in the output or the snapshot is
	// unreachable, which is the whole of what :branch is for.
	name := generatedName(t, res.Out)

	c.Submit("y := 2")
	if len(c.sess.Entries) != 2 {
		t.Fatalf(":branch changed the session: %d entries", len(c.sess.Entries))
	}
	if r := c.Submit(":restore " + name); r.Err {
		t.Fatalf(":restore %s: %s", name, r.Out)
	}
	if len(c.sess.Entries) != 1 {
		t.Errorf("after restoring the branch: %d entries, want 1", len(c.sess.Entries))
	}
}

func TestBranchWithANameKeepsTheSession(t *testing.T) {
	c := testCore(t)
	c.Submit("x := 1")

	res := c.Submit(":branch here")
	if res.Err {
		t.Fatalf(":branch here: %s", res.Out)
	}
	if !strings.Contains(res.Out, "unchanged") {
		t.Errorf(":branch does not say the session continues: %q", res.Out)
	}
	if len(c.sess.Entries) != 1 {
		t.Errorf(":branch changed the session: %d entries, want 1", len(c.sess.Entries))
	}
	if out := c.Submit(":bookmark").Out; !strings.Contains(out, "here") {
		t.Errorf(":branch did not record a snapshot:\n%s", out)
	}
}

// generatedName is the name out of a bare :branch's line: it is the first word,
// because the line is built to start with the name it wants typed back.
func generatedName(t *testing.T, out string) string {
	t.Helper()
	name := strings.TrimSuffix(strings.Fields(out)[0], ":")
	if name == "" {
		t.Fatalf("no generated name in %q", out)
	}
	return name
}

func TestRestoreBringsBackPinnedState(t *testing.T) {
	c := testCore(t)
	c.Submit("x := 1")
	c.Submit("y := 2")
	if res := c.Submit(":pin 2"); res.Err {
		t.Fatalf(":pin 2: %s", res.Out)
	}
	if res := c.Submit(":bookmark pinned"); res.Err {
		t.Fatalf(":bookmark pinned: %s", res.Out)
	}

	if res := c.Submit(":unpin 2"); res.Err {
		t.Fatalf(":unpin 2: %s", res.Out)
	}
	c.Submit("z := 3")

	if res := c.Submit(":restore pinned"); res.Err {
		t.Fatalf(":restore pinned: %s", res.Out)
	}
	if n := len(c.sess.Entries); n != 2 {
		t.Fatalf("restored session has %d entries, want 2", n)
	}
	if c.sess.Entries[0].Src != "x := 1" || c.sess.Entries[1].Src != "y := 2" {
		t.Errorf("restored the wrong entries: %+v", c.sess.Entries)
	}
	if !c.sess.Entries[1].Pinned {
		t.Error("the restored entry lost the pin the snapshot was taken with")
	}
	// The pin is what keeps the entry out of the program, so :src is the other
	// end of the same claim.
	if src := c.Submit(":src").Out; strings.Contains(src, "y := 2") {
		t.Errorf("the restored pin is not in force:\n%s", src)
	}
}

func TestRestoreOfAnUnknownNameListsTheOnesThereAre(t *testing.T) {
	c := testCore(t)
	c.Submit("x := 1")
	c.Submit(":bookmark real")

	res := c.Submit(":restore nope")
	if !res.Err {
		t.Fatalf("restoring an unknown name succeeded: %q", res.Out)
	}
	if !strings.Contains(res.Out, "nope") {
		t.Errorf("the refusal does not name what was asked for: %q", res.Out)
	}
	if !strings.Contains(res.Out, "real") {
		t.Errorf("the refusal does not list the names that exist: %q", res.Out)
	}
}

func TestBareRestoreReportsUsage(t *testing.T) {
	c := testCore(t)
	res := c.Submit(":restore")
	if !res.Err || !strings.HasPrefix(res.Out, "usage:") {
		t.Errorf("bare :restore: %q (err=%v)", res.Out, res.Err)
	}
}

// TestSnapshotsDoNotShareWithTheSession: the store holds a copy going in and
// hands out a copy coming back, so restoring the same name twice gives the same
// session both times.
func TestSnapshotsAreCopiesBothWays(t *testing.T) {
	var b bookmarks
	live := &session.Session{Entries: []session.Entry{
		{Kind: session.KindStmt, Src: "x := 1", Binds: []string{"x"}},
	}}
	b.put("m", live)

	live.Entries[0].Binds[0] = "mutated"
	live.Append(session.Entry{Src: "y := 2"})

	got, ok := b.get("m")
	if !ok {
		t.Fatal("the snapshot is not there")
	}
	if len(got.Entries) != 1 || got.Entries[0].Binds[0] != "x" {
		t.Errorf("the store followed the live session: %+v", got.Entries)
	}

	got.Entries[0].Src = "written into the copy"
	again, _ := b.get("m")
	if again.Entries[0].Src != "x := 1" {
		t.Errorf("a restored copy wrote back into the store: %q", again.Entries[0].Src)
	}
}

// TestAutoNamesDoNotTakeATypedOne: a generated name that landed on a snapshot
// the user named by hand would replace work they took deliberately.
func TestAutoNamesDoNotTakeATypedOne(t *testing.T) {
	c := testCore(t)
	c.Submit(":bookmark b1")
	c.Submit(":bookmark b2")

	name := generatedName(t, c.Submit(":branch").Out)
	if name == "b1" || name == "b2" {
		t.Errorf(":branch generated %q, which is already taken", name)
	}
	out := c.Submit(":bookmark").Out
	for _, want := range []string{"b1", "b2", name} {
		if !strings.Contains(out, want) {
			t.Errorf("listing lost %q:\n%s", want, out)
		}
	}
}

// TestBookmarkDetailSaysItIsNotDurable pins the sentence the risk rests on:
// the commands' own documentation is where a user learns a snapshot is not a
// save.
//
// It must also point somewhere that works. :save writes a program, not a
// session, and cannot be reopened as one — so the thing a reader is sent to
// when they wanted their work back is :scratch.
func TestBookmarkDetailSaysItIsNotDurable(t *testing.T) {
	c := &Core{}
	for _, name := range []string{":bookmark", ":branch", ":restore"} {
		res := c.help(name)
		if res.Err {
			t.Fatalf(":help %s: %s", name, res.Out)
		}
		if !strings.Contains(res.Out, "last for this session") {
			t.Errorf(":help %s does not say snapshots are for this session:\n%s", name, res.Out)
		}
		if !strings.Contains(res.Out, ":scratch") {
			t.Errorf(":help %s does not name :scratch as what makes a session durable:\n%s", name, res.Out)
		}
	}
}
