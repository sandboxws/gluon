//go:build integration

// A scratchpad only means anything across processes, and every guarantee it
// makes — the entries come back, a pin is still a pin, one build not N, nothing
// is fetched — is about what the toolchain does when the file is read again.
// None of that can be shown without really building. Run with:
//
//	go test -tags=integration ./internal/repl/
package repl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/eval"
	"github.com/sandboxws/gluon/internal/scratch"
)

// padCore is one gluon process's worth of Core, in a scratch tree the test
// owns. Two of them in one test are the two processes the feature is about.
func padCore(t *testing.T) *Core {
	t.Helper()
	c, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestAPadSurvivesTheProcessThatWroteIt(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	a := padCore(t)
	if res, _ := a.OpenPad("work"); res.Err {
		t.Fatal(res.Out)
	}
	if res := a.Submit("func double(n int) int { return n * 2 }"); res.Err {
		t.Fatalf("the declaration: %s", res.Out)
	}
	if res := a.Submit("x := double(21)"); res.Err {
		t.Fatalf("the statement: %s", res.Out)
	}
	a.Close()

	// A second process, which shares nothing with the first but the tree.
	b := padCore(t)
	res, _ := b.OpenPad("work")
	if res.Err {
		t.Fatalf("reopening: %s", res.Out)
	}
	if !strings.Contains(res.Out, "2 entries") {
		t.Errorf("the open does not say what it restored: %q", res.Out)
	}
	if hist := b.Submit(":hist").Out; !strings.Contains(hist, "double") {
		t.Errorf(":hist does not show yesterday's entries:\n%s", hist)
	}
	// The declaration is not merely recorded, it is in force.
	if out := b.Submit("double(x)").Out; !strings.Contains(out, "84") {
		t.Errorf("calling into the restored declaration answered %q, want 84", out)
	}
}

// TestAPinnedEntryIsStillPinnedTomorrow. The assertion is a file the entry
// writes: a pin that came back as a flag but not as behaviour would pass a test
// that only read the flag.
func TestAPinnedEntryIsStillPinnedTomorrow(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	marker := filepath.Join(t.TempDir(), "effect")

	a := padCore(t)
	if res, _ := a.OpenPad("effects"); res.Err {
		t.Fatal(res.Out)
	}
	if res := a.Submit("os.WriteFile(`" + marker + "`, []byte(`1`), 0o644)"); res.Err {
		t.Fatalf("the effectful entry: %s", res.Out)
	}
	if res := a.Submit(":pin 1"); res.Err {
		t.Fatalf(":pin 1: %s", res.Out)
	}
	a.Close()

	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	b := padCore(t)
	if res, _ := b.OpenPad("effects"); res.Err {
		t.Fatalf("reopening: %s", res.Out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("a pinned entry ran again on reopen")
	}
	if pins := b.Submit(":pin").Out; !strings.Contains(pins, "1") {
		t.Errorf("the entry came back unpinned:\n%s", pins)
	}
}

// TestReopeningAPadCostsOneBuildNotOnePerEntry. Counted through the evaluator's
// own phase timing rather than by watching the clock: N builds and one build
// differ by seconds on a slow machine and by nothing on a fast one, but the
// number of builds is exact.
func TestReopeningAPadCostsOneBuildNotOnePerEntry(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	a := padCore(t)
	if res, _ := a.OpenPad("many"); res.Err {
		t.Fatal(res.Out)
	}
	for _, src := range []string{"a := 1", "b := 2", "c := 3", "d := 4", "e := 5", "a + b + c + d + e"} {
		if res := a.Submit(src); res.Err {
			t.Fatalf("%s: %s", src, res.Out)
		}
	}
	a.Close()

	b := padCore(t)
	eval.Timing(true)
	defer eval.Timing(false)
	eval.TakePhases()

	if res, _ := b.OpenPad("many"); res.Err {
		t.Fatalf("reopening: %s", res.Out)
	}

	builds := 0
	for _, p := range eval.TakePhases() {
		if p.Name == "build" {
			builds++
		}
	}
	if builds > 1 {
		t.Errorf("reopening six entries cost %d builds, want one", builds)
	}
}

// TestAPadThatNoLongerCompilesOpensEmptyAndSaysWhy, and leaves the file alone —
// the two halves of the same guarantee.
func TestAPadThatNoLongerCompilesOpensEmptyAndSaysWhy(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir, file := writeGreeter(t, "one")

	a := padCore(t)
	if _, err := a.Attach(dir); err != nil {
		t.Fatal(err)
	}
	if res, _ := a.OpenPad("hosted"); res.Err {
		t.Fatal(res.Out)
	}
	if res := a.Submit("greet.Hello()"); res.Err {
		t.Fatalf("greet.Hello(): %s", res.Out)
	}
	a.Close()

	before, err := os.ReadFile(filepath.Join(scratch.PadDir("hosted"), scratch.PadName))
	if err != nil {
		t.Fatal(err)
	}

	// The host stops declaring what the pad calls, which is how a pad stops
	// compiling in practice.
	if err := os.WriteFile(file, []byte("package greet\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	b := padCore(t)
	res, _ := b.OpenPad("hosted")
	if !res.Err {
		t.Fatal("a pad that no longer compiles opened without complaint")
	}
	if !strings.Contains(res.Out, scratch.PadName) {
		t.Errorf("the answer does not name the file: %q", res.Out)
	}
	if !strings.Contains(res.Out, "gluon scratch show") {
		t.Errorf("the answer does not name how to read it without running it: %q", res.Out)
	}
	if len(b.sess.Entries) != 0 {
		t.Errorf("the session is not empty: %d entries", len(b.sess.Entries))
	}

	// And the file is untouched, including after a line is typed into the
	// session that opened empty.
	b.Submit("x := 1")
	after, err := os.ReadFile(filepath.Join(scratch.PadDir("hosted"), scratch.PadName))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("the pad was overwritten:\n%s\nwas\n%s", after, before)
	}
}

// TestAPadWithElevenValuelessCallsStillReopens is why NoValue is recorded.
//
// It is learned from a failed build and re-learned by markNoValue, but evalWith
// retries exactly once and `go build` stops at ten errors, while predictNoValue
// only inspects the last entry. So eleven of them would fail to replay, and
// fail again on the retry — unless the file already knows.
func TestAPadWithElevenValuelessCallsStillReopens(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	a := padCore(t)
	if res, _ := a.OpenPad("valueless"); res.Err {
		t.Fatal(res.Out)
	}
	for i := range 11 {
		src := "func noop" + string(rune('a'+i)) + "() {}"
		if res := a.Submit(src); res.Err {
			t.Fatalf("%s: %s", src, res.Out)
		}
		call := "noop" + string(rune('a'+i)) + "()"
		if res := a.Submit(call); res.Err {
			t.Fatalf("%s: %s", call, res.Out)
		}
	}
	a.Close()

	novalue := 0
	data, err := os.ReadFile(filepath.Join(scratch.PadDir("valueless"), scratch.PadName))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "//gluon:novalue" {
			novalue++
		}
	}
	if novalue != 11 {
		t.Errorf("%d entries were recorded as value-less, want 11:\n%s", novalue, data)
	}

	b := padCore(t)
	if res, _ := b.OpenPad("valueless"); res.Err {
		t.Fatalf("reopening eleven value-less calls: %s", res.Out)
	}
	if len(b.sess.Entries) != 22 {
		t.Errorf("%d entries came back, want 22", len(b.sess.Entries))
	}
}

// TestSavingInsideAPadRefreshesItsProgramInPlace, so gopls and a debugger open
// the work in progress rather than a dated copy of it.
func TestSavingInsideAPadRefreshesItsProgramInPlace(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	c := padCore(t)
	if res, _ := c.OpenPad("inplace"); res.Err {
		t.Fatal(res.Out)
	}
	if res := c.Submit("x := 1"); res.Err {
		t.Fatalf("x := 1: %s", res.Out)
	}

	res := c.Submit(":save")
	if res.Err {
		t.Fatalf(":save: %s", res.Out)
	}
	main := filepath.Join(scratch.PadDir("inplace"), "main.go")
	if !strings.Contains(res.Out, main) {
		t.Errorf(":save does not name the pad's own program: %q", res.Out)
	}
	first, err := os.ReadFile(main)
	if err != nil {
		t.Fatalf("no program in the pad: %v", err)
	}

	// A second line, and a second save: the same file, refreshed.
	if res := c.Submit("y := x + 1"); res.Err {
		t.Fatalf("y := x + 1: %s", res.Out)
	}
	if res := c.Submit(":save"); res.Err {
		t.Fatalf("the second :save: %s", res.Out)
	}
	second, err := os.ReadFile(main)
	if err != nil {
		t.Fatal(err)
	}
	if string(second) == string(first) {
		t.Error("the second save did not refresh the program")
	}
	if !strings.Contains(string(second), "y := x + 1") {
		t.Errorf("the refreshed program is not the session:\n%s", second)
	}

	// One directory, not two: a bare :save in a pad writes no dated scratch.
	ents, err := os.ReadDir(scratch.Root())
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		t.Errorf("%d directories under the root, want just the pad: %v", len(ents), ents)
	}

	// :save with a topic is what it always was.
	if res := c.Submit(":save heap sort"); res.Err {
		t.Fatalf(":save heap sort: %s", res.Out)
	}
	ents, err = os.ReadDir(scratch.Root())
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 2 {
		t.Errorf("a topic did not write a new dated scratch: %v", ents)
	}
}

// TestAPadReattachesItsHost and, when the directory has gone, says so before
// the errors it explains.
func TestAPadReattachesItsHost(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir, _ := writeGreeter(t, "hello")

	a := padCore(t)
	if _, err := a.Attach(dir); err != nil {
		t.Fatal(err)
	}
	if res, _ := a.OpenPad("attached"); res.Err {
		t.Fatal(res.Out)
	}
	if res := a.Submit("greet.Hello()"); res.Err {
		t.Fatalf("greet.Hello(): %s", res.Out)
	}
	a.Close()

	b := padCore(t)
	res, _ := b.OpenPad("attached")
	if res.Err {
		t.Fatalf("reopening: %s", res.Out)
	}
	if !strings.Contains(res.Out, "example.com/proj") {
		t.Errorf("the open does not name the host it put back: %q", res.Out)
	}
	if b.ev.Host() == nil {
		t.Fatal("the session is not attached")
	}
	if out := b.Submit("greet.Hello()").Out; !strings.Contains(out, "hello") {
		t.Errorf("the restored host is not usable: %q", out)
	}
}

func TestAPadWhoseHostIsGoneOpensStandaloneAndSaysSo(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir, _ := writeGreeter(t, "hello")

	a := padCore(t)
	if _, err := a.Attach(dir); err != nil {
		t.Fatal(err)
	}
	if res, _ := a.OpenPad("orphan"); res.Err {
		t.Fatal(res.Out)
	}
	// Nothing that needs the host, so the pad still replays once it is gone —
	// the case where "standalone" is the whole of the damage.
	if res := a.Submit("x := 1"); res.Err {
		t.Fatalf("x := 1: %s", res.Out)
	}
	a.Close()

	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	b := padCore(t)
	res, _ := b.OpenPad("orphan")
	if res.Err {
		t.Fatalf("a pad whose host is gone did not open: %s", res.Out)
	}
	if !strings.Contains(res.Out, "gone") || !strings.Contains(res.Out, "standalone") {
		t.Errorf("the answer does not say the host is gone: %q", res.Out)
	}
	if b.ev.Host() != nil {
		t.Error("the session claims a host that is not there")
	}
	if len(b.sess.Entries) != 1 {
		t.Errorf("the entries did not come back: %d", len(b.sess.Entries))
	}
}

// TestAPadRestoresItsModuleFromTheCacheWithoutFetching. GOPROXY=off in the
// environment as well, so a restore that reached for the network would fail
// loudly rather than quietly succeed on a machine that happens to be online.
func TestAPadRestoresItsModuleFromTheCacheWithoutFetching(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	a := padCore(t)
	if res, _ := a.OpenPad("moduled"); res.Err {
		t.Fatal(res.Out)
	}
	if res := a.Submit(":get github.com/google/go-cmp"); res.Err {
		t.Skip("no network, or the module is not fetchable here:", res.Out)
	}
	if res := a.Submit(`cmp.Diff([]int{1}, []int{1})`); res.Err {
		t.Fatalf("the module is not usable: %s", res.Out)
	}
	a.Close()

	t.Setenv("GOPROXY", "off")
	b := padCore(t)
	res, _ := b.OpenPad("moduled")
	if res.Err {
		t.Fatalf("reopening with the proxy off: %s", res.Out)
	}
	if !strings.Contains(res.Out, "nothing fetched") {
		t.Errorf("the open does not say what it did instead of fetching: %q", res.Out)
	}
	if out := b.Submit(`cmp.Diff([]int{1}, []int{2})`).Out; strings.Contains(out, "error") {
		t.Errorf("the restored module is not usable: %q", out)
	}
}

// TestAPadWhoseModuleIsGoneNamesTheGetLine: nothing is fetched, and the answer
// is the line that would bring it back rather than a build error to decode.
func TestAPadWhoseModuleIsGoneNamesTheGetLine(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	a := padCore(t)
	if res, _ := a.OpenPad("missing"); res.Err {
		t.Fatal(res.Out)
	}
	if res := a.Submit(":get github.com/google/go-cmp"); res.Err {
		t.Skip("no network, or the module is not fetchable here:", res.Out)
	}
	if res := a.Submit(`cmp.Diff([]int{1}, []int{1})`); res.Err {
		t.Fatalf("the module is not usable: %s", res.Out)
	}
	a.Close()

	// The machine the pad is opened on no longer holds the module, and cannot
	// reach for it either.
	t.Setenv("GOMODCACHE", t.TempDir())
	t.Setenv("GOPROXY", "off")

	c := padCore(t)
	res, _ := c.OpenPad("missing")
	if !res.Err {
		t.Fatal("a pad whose module is not on the machine opened as though it were")
	}
	if !strings.Contains(res.Out, "nothing was fetched") {
		t.Errorf("the answer does not say nothing was fetched: %q", res.Out)
	}
	if !strings.Contains(res.Out, ":get github.com/google/go-cmp") {
		t.Errorf("the answer does not give the :get line that would restore it: %q", res.Out)
	}
	// And the pad is intact: a failed open never writes.
	if len(c.sess.Entries) != 0 {
		t.Errorf("the session is not empty: %d entries", len(c.sess.Entries))
	}
}

// :scratch -edit hands over the pad's own session file, so the reload has to
// read it back through the pad parser. Reading it as a bare entry stream is
// what Reload used to do, and the pad header is the half session.Unmarshal is
// right to refuse — so editing a pad answered `unknown directive
// "//gluon:pad 1"` and changed nothing, for every pad that had ever been
// written.
func TestEditingAPadReloadsItThroughThePadReader(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	c := padCore(t)
	if res, _ := c.OpenPad("edited"); res.Err {
		t.Fatal(res.Out)
	}
	if res := c.Submit("func double(n int) int { return n * 2 }"); res.Err {
		t.Fatalf("the declaration: %s", res.Out)
	}
	if res := c.Submit("x := double(21)"); res.Err {
		t.Fatalf("the statement: %s", res.Out)
	}
	// Pinned, because a pin is the decision the pad format exists to carry and
	// the one a reload through the wrong reader would silently drop.
	if res := c.Submit(":pin 2"); res.Err {
		t.Fatalf("pinning: %s", res.Out)
	}

	path := c.Submit(":scratch -edit").Edit
	if path == "" {
		t.Fatal(":scratch -edit named no file to open")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Exactly what an editor does: append a line and save.
	if err := os.WriteFile(path, append(data, []byte("fmt.Println(double(3))\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	res := c.Reload(path)
	if res.Err {
		t.Fatalf("reloading the pad: %s", res.Out)
	}
	if !strings.Contains(res.Out, "6") {
		t.Errorf("the reload did not report what the pad printed:\n%s", res.Out)
	}
	if n := len(c.sess.Entries); n != 3 {
		t.Fatalf("the pad has %d entries after the edit, want 3", n)
	}
	if !c.sess.Entries[1].Pinned {
		t.Error("the pin did not survive the round trip through the file")
	}
	// A pinned entry contributes no code, so the value it once printed must not
	// come back with it.
	if strings.Contains(res.Out, "42") {
		t.Errorf("the pinned entry ran again:\n%s", res.Out)
	}

	// And gluon is still writing the pad down: it handed the file to an editor
	// itself, so the changed size and mtime are its own doing and not a second
	// gluon's.
	if res := c.Submit("y := 1"); res.Err {
		t.Fatalf("the line after the reload: %s", res.Out)
	}
	if strings.Contains(c.Submit("z := 2").Out, "stopped writing") {
		t.Error("the reload made gluon think another process owned the pad")
	}
}
