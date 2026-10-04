package repl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/config"
	"github.com/sandboxws/gluon/internal/scratch"
	"github.com/sandboxws/gluon/internal/session"
	"github.com/sandboxws/gluon/internal/ui"
)

// padRoot points the scratch tree at a temp directory for one test, so nothing
// here can reach the tree the person running the tests actually uses.
func padRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	return filepath.Join(dir, "gluon", "scratch")
}

// padFileOf is the bytes on disk, which is what most of these assert about:
// the point of the feature is the file, not the report.
func padFileOf(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(scratch.PadDir(name), scratch.PadName))
	if err != nil {
		t.Fatalf("reading the pad file: %v", err)
	}
	return data
}

func TestTheDefaultPadIsWhereGluonLands(t *testing.T) {
	padRoot(t)
	c := testCore(t)

	// The driver's open, with nothing named — the case every first `gluon` is.
	res, _ := c.OpenPad(padName(Start{}, c.Config()))
	if res.Err {
		t.Fatalf("opening the default pad: %s", res.Out)
	}
	if !strings.Contains(res.Out, "default") {
		t.Errorf("the line does not name the scratchpad: %q", res.Out)
	}
	if !strings.Contains(res.Out, "created") {
		t.Errorf("the first open does not say it created one: %q", res.Out)
	}
	if c.pad == nil || c.pad.name != "default" {
		t.Fatalf("the session is not in default: %+v", c.pad)
	}
	// Created at the open, so it is listable before anything has been typed.
	if !scratch.IsPad(scratch.PadDir("default")) {
		t.Error("the default pad was not created on disk")
	}
}

// TestNewCoreInstallsNoPad is the property every padless surface rests on. A
// pad is installed by a driver, so a Core built without one — every test in this
// package, `gluon -e`, the MCP server, the piped loop — has none by
// construction rather than by a check.
func TestNewCoreInstallsNoPad(t *testing.T) {
	padRoot(t)
	c := testCore(t)
	if c.pad != nil {
		t.Fatalf("NewCore installed a scratchpad: %+v", c.pad)
	}
	c.Submit("x := 1")
	if _, err := os.Stat(scratch.Root()); !os.IsNotExist(err) {
		t.Errorf("a padless session wrote into the scratch tree: %v", err)
	}
}

// TestGluonEAndTheMCPServerInstallNoPad names the two surfaces the claim in
// cmd/gluon/mcp.go depends on, and holds the line at the one place it can be
// held: neither of them opens a pad, because neither of them calls OpenPad.
func TestGluonEAndTheMCPServerInstallNoPad(t *testing.T) {
	padRoot(t)
	c := testCore(t)
	if _, err := c.EvalBatch([]string{"x := 1", "x + 1"}); err != nil {
		t.Fatalf("EvalBatch: %v", err)
	}
	if c.pad != nil {
		t.Errorf("the one-shot path installed a scratchpad: %+v", c.pad)
	}
	if _, err := os.Stat(scratch.Root()); !os.IsNotExist(err) {
		t.Errorf("the one-shot path wrote into the scratch tree: %v", err)
	}
	// The piped loop takes only the host directory: the pad fields are not
	// ignored by a check it could stop making, they are never passed in.
	if res := c.Submit(":scratch"); res.Err {
		t.Fatalf(":scratch in a padless session: %s", res.Out)
	}
}

func TestNothingIsWrittenUntilTheEntriesChange(t *testing.T) {
	padRoot(t)
	c := testCore(t)
	if res, _ := c.OpenPad("default"); res.Err {
		t.Fatal(res.Out)
	}
	c.Submit("x := 1")
	before := padFileOf(t, "default")
	writes := c.pad.writes

	// :help changes nothing about the session, and Core.gen is bumped for it
	// all the same — which is exactly why the dedupe is on the bytes.
	for _, line := range []string{":help", ":hist", ":ls"} {
		if res := c.Submit(line); res.Err {
			t.Fatalf("%s: %s", line, res.Out)
		}
	}
	if c.pad.writes != writes {
		t.Errorf("a command that changed nothing wrote the pad (%d writes, was %d)", c.pad.writes, writes)
	}
	if got := padFileOf(t, "default"); string(got) != string(before) {
		t.Errorf("the file changed:\n%s\nwas\n%s", got, before)
	}

	// A pin changes bytes, so it does write.
	if res := c.Submit(":pin 1"); res.Err {
		t.Fatalf(":pin 1: %s", res.Out)
	}
	if !strings.Contains(string(padFileOf(t, "default")), session.DirectivePrefix+"pin") {
		t.Error("pinning an entry did not reach the file")
	}
}

// TestAPastedBatchWritesThePadOnce: SubmitBatch calls Submit for the single
// case and submitEach calls it N times, so without the depth counter a ten-line
// paste would write ten times.
func TestAPastedBatchWritesThePadOnce(t *testing.T) {
	padRoot(t)
	c := testCore(t)
	if res, _ := c.OpenPad("default"); res.Err {
		t.Fatal(res.Out)
	}
	writes := c.pad.writes

	// The last construct will not classify, which is what sends the batch down
	// submitEach — the path where the count actually matters.
	c.SubmitBatch([]string{"a := 1", "b := 2", "}"})

	if got := c.pad.writes - writes; got != 1 {
		t.Errorf("a pasted batch wrote the pad %d times, want 1", got)
	}
	if c.padDepth != 0 {
		t.Errorf("padDepth = %d after the batch, want 0", c.padDepth)
	}
	src := string(padFileOf(t, "default"))
	for _, want := range []string{"a := 1", "b := 2"} {
		if !strings.Contains(src, want) {
			t.Errorf("%q is not in the pad:\n%s", want, src)
		}
	}
}

// TestAPadThatWillNotLoadIsNeverOverwritten is the single worst bug the feature
// can have: open → replay fails → session empty → one line typed → forty lines
// replaced by one.
func TestAPadThatWillNotLoadIsNeverOverwritten(t *testing.T) {
	padRoot(t)
	// A pad this gluon refuses to read at all. The refusal is the point; how it
	// is provoked is not.
	dir := scratch.PadDir("broken")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	before := []byte("//gluon:pad 99\nx := 1\ny := 2\n")
	if err := os.WriteFile(filepath.Join(dir, scratch.PadName), before, 0o600); err != nil {
		t.Fatal(err)
	}

	c := testCore(t)
	res, _ := c.OpenPad("broken")
	if !res.Err {
		t.Fatal("a pad that cannot be read opened without complaint")
	}
	if !strings.Contains(res.Out, "not being written to") {
		t.Errorf("the answer does not say it will not be written: %q", res.Out)
	}

	after := c.Submit("z := 3")
	if !strings.Contains(after.Out, "not being written to") {
		t.Errorf("the reason was not repeated when the next line landed: %q", after.Out)
	}
	if got := padFileOf(t, "broken"); string(got) != string(before) {
		t.Fatalf("the pad was overwritten:\n%s\nwas\n%s", got, before)
	}
	// Said once, not on every line for the rest of the session.
	third := c.Submit("z + 1")
	if strings.Contains(third.Out, "not being written to") {
		t.Errorf("the reason is repeated on every line: %q", third.Out)
	}
}

func TestASecondGluonStopsWritingAndSaysSo(t *testing.T) {
	padRoot(t)
	c := testCore(t)
	if res, _ := c.OpenPad("shared"); res.Err {
		t.Fatal(res.Out)
	}
	c.Submit("x := 1")

	// Another gluon, writing the same pad. Only the bytes on disk matter here;
	// which process put them there does not.
	other := []byte("//gluon:pad 1\nsomething := \"else\"\n")
	if err := os.WriteFile(filepath.Join(scratch.PadDir("shared"), scratch.PadName), other, 0o600); err != nil {
		t.Fatal(err)
	}

	res := c.Submit("y := 2")
	if !strings.Contains(res.Out, "another gluon") {
		t.Errorf("the answer does not say another gluon wrote it: %q", res.Out)
	}
	if !strings.Contains(res.Out, ":scratch") {
		t.Errorf("the answer does not name how to keep this work: %q", res.Out)
	}
	if got := padFileOf(t, "shared"); string(got) != string(other) {
		t.Errorf("the other process's pad was overwritten:\n%s", got)
	}
	// The session is untouched: the loser keeps its work, it just stops
	// writing it down.
	if len(c.sess.Entries) != 2 {
		t.Errorf("the session lost entries: %d", len(c.sess.Entries))
	}
}

func TestResettingAPadLeavesThePreviousContentsBehind(t *testing.T) {
	padRoot(t)
	c := testCore(t)
	if res, _ := c.OpenPad("work"); res.Err {
		t.Fatal(res.Out)
	}
	c.Submit("x := 1")
	c.Submit("y := 2")
	before := padFileOf(t, "work")

	res := c.Submit(":reset")
	prev := filepath.Join(scratch.PadDir("work"), scratch.PadPrevName)
	if !strings.Contains(res.Out, prev) {
		t.Errorf("the answer does not name where the previous contents are: %q", res.Out)
	}
	kept, err := os.ReadFile(prev)
	if err != nil {
		t.Fatalf("the previous contents were not kept: %v", err)
	}
	if string(kept) != string(before) {
		t.Errorf("what was kept is not what was there:\n%s\nwas\n%s", kept, before)
	}
	if strings.Contains(string(padFileOf(t, "work")), "x := 1") {
		t.Error("the pad still holds the cleared session")
	}
}

func TestAPadlessSessionRefusesToSwitchAndSaysWhereToGetOne(t *testing.T) {
	padRoot(t)
	c := testCore(t)
	for _, line := range []string{":scratch -off", ":scratch -mv other", ":scratch -edit"} {
		res := c.Submit(line)
		if !res.Err {
			t.Errorf("%s answered a padless session without complaint: %q", line, res.Out)
			continue
		}
		if !strings.Contains(res.Out, ":scratch <name>") || !strings.Contains(res.Out, "-scratch") {
			t.Errorf("%s does not name how to get a scratchpad: %q", line, res.Out)
		}
	}
	// The listing is the one thing that still answers: what there is does not
	// depend on being in one.
	if res := c.Submit(":scratch"); res.Err {
		t.Errorf("the listing was refused in a padless session: %q", res.Out)
	}
}

func TestTheListAndTheViewAreBuiltFromOneRowBuilder(t *testing.T) {
	padRoot(t)
	c := testCore(t)
	if res, _ := c.OpenPad("one"); res.Err {
		t.Fatal(res.Out)
	}
	c.Submit("x := 1")
	if res := c.Submit(":scratch two"); res.Err {
		t.Fatal(res.Out)
	}

	res := c.Submit(":scratch")
	if res.Err {
		t.Fatalf(":scratch: %s", res.Out)
	}
	if res.Modal == nil {
		t.Fatal(":scratch offered no view")
	}
	headers, rows, names := c.padRowsAll()
	if len(res.Modal.Rows) != len(rows) || len(res.Modal.Headers) != len(headers) {
		t.Errorf("the view was not built from the row builder: %v vs %v", res.Modal.Rows, rows)
	}
	if len(res.Modal.Entries) != len(names) {
		t.Errorf("%d entries for %d rows", len(res.Modal.Entries), len(names))
	}
	// Everything the view shows is in Out as well, so a driver that cannot go
	// full-screen loses nothing — invariant 19.
	for _, r := range rows {
		if !strings.Contains(res.Out, r[0]) {
			t.Errorf("row %q is in the view and not in Out:\n%s", r[0], res.Out)
		}
	}
	// The active pad is marked, and it is the one the session is in.
	if !strings.Contains(res.Out, "* two") {
		t.Errorf("the active scratchpad is not marked:\n%s", res.Out)
	}
	// The view can change what it is showing, so it has to rebuild from the
	// directory rather than describe what it opened with.
	if res.Modal.Refresh != ":scratch" {
		t.Errorf("Refresh = %q, want :scratch", res.Modal.Refresh)
	}
}

func TestSwitchingPadsFromTheViewSubmitsACommand(t *testing.T) {
	padRoot(t)
	c := testCore(t)
	if res, _ := c.OpenPad("one"); res.Err {
		t.Fatal(res.Out)
	}
	res := c.Submit(":scratch")
	if res.Modal == nil || len(res.Modal.Entries) == 0 {
		t.Fatal("no entries to choose from")
	}
	e := res.Modal.Entries[0]
	if len(e.Choices) != 1 {
		t.Fatalf("a row offers %d choices, want one", len(e.Choices))
	}
	// A line the driver submits as though it had been typed, never a callback
	// into Core from the wrong goroutine.
	if want := ":scratch " + e.Title; e.Choices[0].Run != want {
		t.Errorf("choosing a row runs %q, want %q", e.Choices[0].Run, want)
	}
}

func TestRemovingAPadShowsItBeforeAskingAndRefusesTheOneYouAreIn(t *testing.T) {
	padRoot(t)
	c := testCore(t)
	if res, _ := c.OpenPad("old"); res.Err {
		t.Fatal(res.Out)
	}
	c.Submit("x := 1")
	if res := c.Submit(":scratch current"); res.Err {
		t.Fatal(res.Out)
	}

	// The one you are in is refused, and the answer names the move to make.
	res := c.Submit(":scratch -rm current")
	if !res.Err {
		t.Error("removing the scratchpad the session is in was allowed")
	}
	if !strings.Contains(res.Out, ":scratch <other>") {
		t.Errorf("the refusal does not name the switch to make first: %q", res.Out)
	}

	// Another one is shown before anything happens.
	c.Rich = true
	res = c.Submit(":scratch -rm old")
	if res.Err {
		t.Fatalf(":scratch -rm old: %s", res.Out)
	}
	if !strings.Contains(res.Out, "x := 1") {
		t.Errorf("what would be removed was not shown:\n%s", res.Out)
	}
	if res.Modal == nil || res.Modal.Confirm == nil {
		t.Fatal("nothing was asked")
	}
	if res.Modal.Confirm.Run != ":scratch -rm -force old" {
		t.Errorf("the confirmation runs %q", res.Modal.Confirm.Run)
	}
	if !scratch.IsPad(scratch.PadDir("old")) {
		t.Fatal("the scratchpad was removed before the question was answered")
	}

	// Through a pipe it prints what would go and names the line, removing
	// nothing.
	c.Rich = false
	res = c.Submit(":scratch -rm old")
	if !strings.Contains(res.Out, ":scratch -rm -force old") {
		t.Errorf("the pipe form does not name the line that would remove it:\n%s", res.Out)
	}
	if !scratch.IsPad(scratch.PadDir("old")) {
		t.Fatal("the pipe form removed it")
	}

	// And the forced form does remove it.
	if res := c.Submit(":scratch -rm -force old"); res.Err {
		t.Fatalf(":scratch -rm -force old: %s", res.Out)
	}
	if scratch.IsPad(scratch.PadDir("old")) {
		t.Error("the forced form did not remove it")
	}
}

// TestDetachingLeavesTheFileAlone: what -off removes is the writer, not the
// work.
func TestDetachingLeavesTheFileAlone(t *testing.T) {
	padRoot(t)
	c := testCore(t)
	if res, _ := c.OpenPad("notes"); res.Err {
		t.Fatal(res.Out)
	}
	c.Submit("x := 1")
	before := padFileOf(t, "notes")

	if res := c.Submit(":scratch -off"); res.Err {
		t.Fatalf(":scratch -off: %s", res.Out)
	}
	c.Submit("y := 2")
	if got := padFileOf(t, "notes"); string(got) != string(before) {
		t.Errorf("a detached session kept writing:\n%s", got)
	}
	if len(c.sess.Entries) != 2 {
		t.Errorf("detaching changed the session: %d entries", len(c.sess.Entries))
	}
}

// TestRenamingTakesTheSessionWithIt.
func TestRenamingTakesTheSessionWithIt(t *testing.T) {
	padRoot(t)
	c := testCore(t)
	if res, _ := c.OpenPad("scratch-one"); res.Err {
		t.Fatal(res.Out)
	}
	c.Submit("x := 1")

	res := c.Submit(":scratch -mv parser bug")
	if res.Err {
		t.Fatalf(":scratch -mv: %s", res.Out)
	}
	if c.pad.name != "parser-bug" {
		t.Errorf("the session is in %q, want parser-bug", c.pad.name)
	}
	if !strings.Contains(res.Out, "parser-bug") {
		t.Errorf("the answer does not name what it opened: %q", res.Out)
	}
	if scratch.IsPad(scratch.PadDir("scratch-one")) {
		t.Error("the old directory is still a scratchpad")
	}
	// Still writing, under the new name.
	c.Submit("y := 2")
	if !strings.Contains(string(padFileOf(t, "parser-bug")), "y := 2") {
		t.Error("the renamed pad is not being written to")
	}
}

// TestASlugIsNamedWhenItDiffersFromWhatWasTyped, so two spellings are not
// mistaken for two scratchpads.
func TestASlugIsNamedWhenItDiffersFromWhatWasTyped(t *testing.T) {
	padRoot(t)
	c := testCore(t)
	res := c.Submit(":scratch Parser Bug")
	if res.Err {
		t.Fatalf(":scratch Parser Bug: %s", res.Out)
	}
	if !strings.Contains(res.Out, "parser-bug") {
		t.Errorf("the answer does not name the scratchpad it opened: %q", res.Out)
	}
	if c.pad == nil || c.pad.name != "parser-bug" {
		t.Errorf("opened %+v", c.pad)
	}
}

// TestADatedNameIsRefused: a date in that tree means a throwaway, and a
// scratchpad taking one would collide with what `gluon new` writes.
func TestADatedNameIsRefused(t *testing.T) {
	padRoot(t)
	c := testCore(t)
	res := c.Submit(":scratch 2026-09-05-notes")
	if !res.Err {
		t.Fatalf("a dated name was accepted: %q", res.Out)
	}
	if c.pad != nil {
		t.Errorf("a refused name still installed a pad: %+v", c.pad)
	}
}

// TestPadNameResolution pins what decides which pad a driver opens.
func TestPadNameResolution(t *testing.T) {
	cfg := func(pad string) *config.Config { c := &config.Config{}; c.Scratch.Pad = pad; return c }
	cases := []struct {
		name  string
		start Start
		cfg   *config.Config
		want  string
	}{
		{"nothing said at all", Start{}, nil, "default"},
		{"nothing configured", Start{}, cfg(""), "default"},
		{"configured", Start{}, cfg("notes"), "notes"},
		{"the flag beats the file", Start{Pad: "other"}, cfg("notes"), "other"},
		{"-no-scratch beats both", Start{NoPad: true, Pad: "other"}, cfg("notes"), ""},
		{"off in the file", Start{}, cfg("off"), ""},
		{"- in the file", Start{}, cfg("-"), ""},
	}
	for _, tc := range cases {
		if got := padName(tc.start, tc.cfg); got != tc.want {
			t.Errorf("%s: padName = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestASecondGluonThatWroteBeforeTheFirstLineIsStillCaught: the stat baseline
// is recorded at the open, not at the first write, so the window between
// landing on a pad and typing into it is covered too.
func TestASecondGluonThatWroteBeforeTheFirstLineIsStillCaught(t *testing.T) {
	padRoot(t)
	// A pad that already exists, so the open reads it rather than creating it.
	if err := scratch.WritePad(&scratch.Pad{
		Name: "raced", Dir: scratch.PadDir("raced"), Sess: &session.Session{},
	}); err != nil {
		t.Fatal(err)
	}

	c := testCore(t)
	if res, _ := c.OpenPad("raced"); res.Err {
		t.Fatal(res.Out)
	}
	other := []byte("//gluon:pad 1\nsomething := \"else\"\n")
	if err := os.WriteFile(filepath.Join(scratch.PadDir("raced"), scratch.PadName), other, 0o600); err != nil {
		t.Fatal(err)
	}

	res := c.Submit("x := 1")
	if !strings.Contains(res.Out, "another gluon") {
		t.Errorf("the first line after the open overwrote another process's pad: %q", res.Out)
	}
	if got := padFileOf(t, "raced"); string(got) != string(other) {
		t.Errorf("the other process's pad was overwritten:\n%s", got)
	}
}

// TestThePadSentenceIsUnchanged. Result.Out is a frozen surface — invariant 30
// — and PadOpen was added beside the code that builds it. These are the exact
// sentences the open has always written, so that adding a second reader of the
// same facts cannot move a byte the first one emits.
//
// The sentence and the row block are built from the same switch, which is what
// makes this a pin on both: if the clause changes, this fails before anybody
// notices the screen disagreeing with a pipe.
func TestThePadSentenceIsUnchanged(t *testing.T) {
	padRoot(t)

	// A pad that does not exist yet: created.
	c := testCore(t)
	res, po := c.OpenPad("fresh")
	if want := "scratchpad fresh — created, the session is empty"; res.Out != want {
		t.Errorf("a created pad says %q, want %q", res.Out, want)
	}
	if po.Name != "fresh" || po.State != "created, the session is empty" {
		t.Errorf("PadOpen disagrees with the sentence: %+v", po)
	}

	// The same pad, reopened with nothing in it: empty.
	d := testCore(t)
	res, po = d.OpenPad("fresh")
	if want := "scratchpad fresh — empty"; res.Out != want {
		t.Errorf("an empty pad says %q, want %q", res.Out, want)
	}
	if po.State != "empty" {
		t.Errorf("PadOpen disagrees with the sentence: %+v", po)
	}

	// One entry, and one of them pinned. Written straight to disk so this
	// stays a unit test: replaying costs a build, and the sentence is what is
	// under test rather than the replay.
	sess := &session.Session{Entries: []session.Entry{
		{Kind: session.KindDecl, Src: "type T struct{}", Pinned: true},
	}}
	if err := scratch.WritePad(&scratch.Pad{
		Name: "one", Dir: scratch.PadDir("one"), Sess: sess,
	}); err != nil {
		t.Skipf("writing a pad directly is not available here: %v", err)
	}
	e := testCore(t)
	res, po = e.OpenPad("one")
	if want := "scratchpad one — 1 entry restored, 1 pinned"; res.Out != want {
		t.Errorf("a restored pad says %q, want %q", res.Out, want)
	}
	if po.State != "1 entry restored, 1 pinned" {
		t.Errorf("PadOpen disagrees with the sentence: %+v", po)
	}
}

// TestTheHostLeavesTheSentenceAndGetsARow. The host is the fact that suffered
// most from arriving after a semicolon, so the screen gives it a row — while
// Result.Out, which a pipe reads and cannot lay out, still names it in the
// sentence it always did.
func TestTheHostLeavesTheSentenceAndGetsARow(t *testing.T) {
	var po PadOpen
	po.Name, po.State = "work", "2 entries restored"
	po.Host = "github.com/acme/inventory-api"
	po.Notes = []string{"1 module restored from the local cache, nothing fetched"}

	// The row block: the host stands alone, and what is left qualifies the open.
	if got := padValue(ui.Plain(), po); !strings.Contains(got, "2 entries restored") ||
		strings.Contains(got, "inventory-api") {
		t.Errorf("the pad row should carry the state and not the host: %q", got)
	}
	if got := hostValue(ui.Plain(), po, Boot{}); got != "github.com/acme/inventory-api" {
		t.Errorf("the host row is %q", got)
	}
}

// TestThePadsViewNamesItsDirectoryFromHome. The footer of :scratch's view
// names where the pads live, and the settings view's footer names its file
// from ~: under the home directory, so does this one. The absolute path was
// the longest thing in the view, and it said whose machine it was.
func TestThePadsViewNamesItsDirectoryFromHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	// No evaluator: the footer is words and a path, and a Core with a moved
	// HOME would warm a Go cache of its own for nothing.
	c := &Core{}
	if got, want := c.padFooter(), "~/.local/share/gluon/scratch"; !strings.HasSuffix(got, want) {
		t.Errorf("the footer is %q, want it to end %q", got, want)
	}
}
