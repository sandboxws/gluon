//go:build integration

// A restore really replays, so the guarantee that matters — a failed restore
// leaves the session it was leaving intact — can only be shown by making a
// replay actually fail. Run with:
//
//	go test -tags=integration ./internal/repl/
package repl

import (
	"os"
	"strings"
	"testing"
)

// TestAFailedRestoreLeavesTheSessionIntact is the worst outcome this capability
// could have: a restore that destroys the state the user was moving away from.
//
// The replay is made to fail the way it would in practice — the host the
// snapshot's entries call into stops declaring what they call — rather than by
// planting a snapshot that never compiled, because a snapshot is only ever
// taken from a session that ran.
func TestAFailedRestoreLeavesTheSessionIntact(t *testing.T) {
	dir, file := writeGreeter(t, "one")

	c, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Attach(dir); err != nil {
		t.Fatal(err)
	}

	if res := c.Submit("greet.Hello()"); res.Err {
		t.Fatalf("greet.Hello(): %s", res.Out)
	}
	if res := c.Submit(":bookmark calls-host"); res.Err {
		t.Fatalf(":bookmark calls-host: %s", res.Out)
	}

	// Build the session the restore must not damage: two entries, one of them
	// pinned, so order and pinned state are both on the line.
	if res := c.Submit(":reset"); res.Err {
		t.Fatalf(":reset: %s", res.Out)
	}
	for _, line := range []string{"x := 1", "y := 2"} {
		if res := c.Submit(line); res.Err {
			t.Fatalf("%s: %s", line, res.Out)
		}
	}
	if res := c.Submit(":pin 2"); res.Err {
		t.Fatalf(":pin 2: %s", res.Out)
	}
	before := c.sess.Clone().Entries

	// The function the snapshot calls is gone, so replaying the snapshot
	// cannot compile.
	if err := os.WriteFile(file,
		[]byte("package greet\n\nfunc Greeting() string { return \"one\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := c.Submit(":reload"); res.Err {
		t.Fatalf(":reload: %s", res.Out)
	}

	res := c.Submit(":restore calls-host")
	if !res.Err {
		t.Fatalf("the restore was expected to fail its replay, got: %q", res.Out)
	}
	if !strings.Contains(res.Out, "session unchanged") {
		t.Errorf("a failed restore does not say the session was kept: %q", res.Out)
	}

	after := c.sess.Entries
	if len(after) != len(before) {
		t.Fatalf("after a failed restore the session has %d entries, had %d", len(after), len(before))
	}
	for i := range before {
		if after[i].Src != before[i].Src {
			t.Errorf("entry %d is %q, was %q", i+1, after[i].Src, before[i].Src)
		}
		if after[i].Pinned != before[i].Pinned {
			t.Errorf("entry %d pinned=%v, was %v", i+1, after[i].Pinned, before[i].Pinned)
		}
	}
	// Intact means it still runs, not merely that the slice reads the same.
	if r := c.Submit("x + 1"); r.Err {
		t.Errorf("the kept session no longer evaluates: %s", r.Out)
	}
}

// TestARestoreThatReplaysPutsTheHostBackToWork is the other half: the failure
// path above is only worth having if the success path really re-runs the
// snapshot rather than assuming it still holds.
func TestARestoreReallyReplays(t *testing.T) {
	dir, file := writeGreeter(t, "one")

	c, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Attach(dir); err != nil {
		t.Fatal(err)
	}

	if res := c.Submit("greet.Hello()"); res.Err {
		t.Fatalf("greet.Hello(): %s", res.Out)
	}
	if res := c.Submit(":bookmark calls-host"); res.Err {
		t.Fatalf(":bookmark calls-host: %s", res.Out)
	}
	c.Submit(":reset")
	c.Submit("x := 1")

	rewriteGreeter(t, file, "two")
	if res := c.Submit(":reload"); res.Err {
		t.Fatalf(":reload: %s", res.Out)
	}

	if res := c.Submit(":restore calls-host"); res.Err {
		t.Fatalf(":restore calls-host: %s", res.Out)
	}
	if n := len(c.sess.Entries); n != 1 {
		t.Fatalf("restored session has %d entries, want 1", n)
	}
	// The entry ran against the source as it is now, which is the whole
	// difference between a replay and a recollection.
	if res := c.Submit("greet.Hello()"); !strings.Contains(res.Out, "two") {
		t.Errorf("after the restore the host answers %q, want the edited source's answer", res.Out)
	}
}
