package repl

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sandboxws/gluon/internal/session"
)

// These drive the commands rather than the poll loop: what the user is told
// when a session goes back in step with its host is as much of the feature as
// the invalidation itself.

// hostDir writes a module whose source can then be edited under the session's
// feet. Nothing here is built — attaching walks the tree and rewrites go.mod,
// which is all these tests need.
func hostDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":                    "module example.com/proj\n\ngo 1.24\n",
		"internal/greet/greet.go":   "package greet\n\nfunc Hello() string { return \"one\" }\n",
		"node_modules/pkg/index.go": "package pkg\n",
		".git/hooks/hook.go":        "package hook\n",
	}
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func attached(t *testing.T) (*Core, string) {
	t.Helper()
	c := testCore(t)
	dir := hostDir(t)
	if _, err := c.Attach(dir); err != nil {
		t.Fatalf("attach: %v", err)
	}
	return c, dir
}

func TestReloadWithoutAHostSaysNothingWasDropped(t *testing.T) {
	c := testCore(t)
	res := c.Submit(":reload")
	if res.Err {
		t.Fatalf(":reload on a standalone session errored: %q", res.Out)
	}
	if !strings.Contains(res.Out, "no host attached") {
		t.Errorf(":reload with no host: %q, want it to say so rather than succeed at nothing", res.Out)
	}
}

// The session is what the user typed; :reload is about what gluon derived. A
// reload that renumbered or unpinned anything would be :edit's job done badly.
func TestReloadLeavesTheSessionAlone(t *testing.T) {
	c, _ := attached(t)
	c.Submit("x := 1")
	c.Submit("y := 2")
	if res := c.Submit(":pin 2"); res.Err {
		t.Fatalf(":pin 2: %s", res.Out)
	}
	before := c.Submit(":hist").Out

	if res := c.Submit(":reload"); res.Err {
		t.Fatalf(":reload: %s", res.Out)
	}
	if after := c.Submit(":hist").Out; after != before {
		t.Errorf("the session changed across a reload:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if !c.sess.Entries[1].Pinned {
		t.Error("the pinned entry came unpinned")
	}
}

// A reload that did not drop the result cache would look exactly like one that
// worked, right up to the moment it served an answer from before the edit.
func TestReloadReportsWhatItDropped(t *testing.T) {
	c, dir := attached(t)
	res := c.Submit(":reload")
	if res.Err {
		t.Fatalf(":reload: %s", res.Out)
	}
	for _, want := range []string{dir, "cached result", "importable package"} {
		if !strings.Contains(res.Out, want) {
			t.Errorf(":reload output does not mention %q:\n%s", want, res.Out)
		}
	}
}

// A reload nobody asked for has to say what prompted it.
func TestWatchTriggeredReloadNamesTheChange(t *testing.T) {
	c, _ := attached(t)
	res := c.ReloadOnChange("internal/greet/greet.go changed")
	if res.Err {
		t.Fatalf("ReloadOnChange: %s", res.Out)
	}
	if !strings.Contains(res.Out, "internal/greet/greet.go changed") {
		t.Errorf("a reload the user did not ask for is unexplained:\n%s", res.Out)
	}
}

func TestRefreshWithNothingPinnedDoesNotEvaluate(t *testing.T) {
	c := testCore(t)
	c.Submit("x := 1")

	// Appended by hand so the session cannot build. If :refresh evaluated
	// anyway, the answer would be a compile error rather than this message.
	e, err := session.Classify("undefinedOnPurpose()")
	if err != nil {
		t.Fatal(err)
	}
	c.sess.Append(e)

	res := c.Submit(":refresh")
	if res.Err {
		t.Fatalf(":refresh with nothing pinned evaluated: %q", res.Out)
	}
	if !strings.Contains(res.Out, "nothing is pinned") {
		t.Errorf(":refresh with nothing pinned: %q", res.Out)
	}
}

// The requirement is that pins survive a failure, and the defer is what makes
// it hold by construction rather than by remembering the error branch.
func TestRefreshRestoresPinsWhenTheEvaluationFails(t *testing.T) {
	c := testCore(t)
	c.Submit("x := 1")

	// Pinned by hand: :pin would refuse to create this state, and the failure
	// path is what is under test, not how a session reaches it.
	e, err := session.Classify("undefinedOnPurpose()")
	if err != nil {
		t.Fatal(err)
	}
	e.Pinned = true
	c.sess.Append(e)

	res := c.Submit(":refresh")
	if !res.Err {
		t.Fatalf(":refresh ran a session that cannot build: %q", res.Out)
	}
	if !c.sess.Entries[1].Pinned {
		t.Error("a failed :refresh left the entry unpinned — the pin state leaked")
	}
}

// :refresh is a replay by definition: the program text is identical to last
// time. Served from the cache it would answer with the run before it, which is
// the one thing the command exists not to do.
func TestRefreshIsNotServedFromTheResultCache(t *testing.T) {
	c := testCore(t)
	c.Submit("x := 1")
	if res := c.Submit(":pin 1"); res.Err {
		t.Fatalf(":pin 1: %s", res.Out)
	}
	c.Submit("time.Now().UnixNano()")

	first := c.Submit(":refresh")
	if first.Err {
		t.Fatalf("first :refresh: %s", first.Out)
	}
	second := c.Submit(":refresh")
	if second.Err {
		t.Fatalf("second :refresh: %s", second.Out)
	}
	if first.Out == second.Out {
		t.Errorf("two refreshes of the same program text gave the same answer, "+
			"so the second was served from the cache:\n%s", first.Out)
	}
	if !c.sess.Entries[0].Pinned {
		t.Error("a successful :refresh left the entry unpinned")
	}
}

func TestBareWatchReportsTheState(t *testing.T) {
	c, dir := attached(t)
	if out := c.Submit(":watch").Out; !strings.Contains(out, "not watching") {
		t.Errorf("bare :watch before starting: %q", out)
	}
	if res := c.Submit(":watch on"); res.Err {
		t.Fatalf(":watch on: %s", res.Out)
	}
	out := c.Submit(":watch").Out
	if !strings.Contains(out, "watching") || !strings.Contains(out, dir) {
		t.Errorf("bare :watch while on: %q, want the state and the directory", out)
	}
}

func TestWatchOffStopsPolling(t *testing.T) {
	c, _ := attached(t)
	if res := c.Submit(":watch on"); res.Err {
		t.Fatalf(":watch on: %s", res.Out)
	}
	res := c.Submit(":watch off")
	if res.Err || !strings.Contains(res.Out, "off") {
		t.Errorf(":watch off: %q (err=%v)", res.Out, res.Err)
	}
	if c.Watching() {
		t.Error("the poll loop is still running after :watch off")
	}
	if out := c.Submit(":watch off").Out; !strings.Contains(out, "not watching") {
		t.Errorf(":watch off twice: %q", out)
	}
}

// A loop that can never fire is not started, and the message says why rather
// than leaving the user watching nothing.
func TestWatchWithoutAHostRefuses(t *testing.T) {
	c := testCore(t)
	res := c.Submit(":watch on")
	if res.Watch {
		t.Error(":watch on with no host asked the driver for an event loop")
	}
	if c.Watching() {
		t.Error("a poll loop was started with nothing to poll")
	}
	if !strings.Contains(res.Out, "no host attached") {
		t.Errorf(":watch on with no host: %q", res.Out)
	}
}

// The pipe driver has no event loop, so it answers for itself — the way it
// already answers :edit.
func TestWatchNeedsATerminal(t *testing.T) {
	c, _ := attached(t)
	res := c.Submit(":watch on")
	if !res.Watch {
		t.Fatal(":watch on did not tell the driver it needs an event loop")
	}
	msg := noTerminal(c, res)
	if !strings.Contains(msg, ":watch needs a terminal") {
		t.Errorf("noTerminal for :watch: %q", msg)
	}
	if c.Watching() {
		t.Error("the pipe driver left a poll loop running for events it cannot deliver")
	}
	if got := noTerminal(c, Result{Out: "ordinary"}); got != "" {
		t.Errorf("noTerminal refused an ordinary result: %q", got)
	}
}

func TestFingerprintNoticesANestedEdit(t *testing.T) {
	dir := hostDir(t)
	first := scanHost(dir)
	if len(first.files) == 0 {
		t.Fatal("nothing was fingerprinted")
	}
	if first.truncated {
		t.Error("a three-file tree hit the file cap")
	}
	if scanHost(dir).sum != first.sum {
		t.Error("the fingerprint changed with nothing touched")
	}

	// A directory's own mtime does not move when a file inside it is edited,
	// which is why this walks to the files.
	p := filepath.Join(dir, "internal", "greet", "greet.go")
	if err := os.WriteFile(p, []byte("package greet\n\nfunc Hello() string { return \"two\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := scanHost(dir)
	if second.sum == first.sum {
		t.Error("editing a nested file did not change the fingerprint")
	}
	if got := second.since(first); !strings.Contains(got, "internal/greet/greet.go") {
		t.Errorf("the change is described as %q, which does not name the file", got)
	}
}

// The prune list is what keeps a tick cheap in a repository that also holds a
// JavaScript front end.
func TestFingerprintSkipsWhatNoSearchDescendsInto(t *testing.T) {
	fp := scanHost(hostDir(t))
	for p := range fp.files {
		if strings.HasPrefix(p, "node_modules/") || strings.HasPrefix(p, ".git/") {
			t.Errorf("%s is being watched", p)
		}
	}
}

func TestFingerprintNamesARemovedFile(t *testing.T) {
	dir := hostDir(t)
	first := scanHost(dir)
	if err := os.Remove(filepath.Join(dir, "internal", "greet", "greet.go")); err != nil {
		t.Fatal(err)
	}
	if got := scanHost(dir).since(first); !strings.Contains(got, "removed") {
		t.Errorf("a deleted file is described as %q", got)
	}
}

// The watcher must not reach Core. Core is not safe for concurrent use, and a
// second thing touching it is exactly what invariant 15 forbids — the reload
// reaches it through the driver's event loop or not at all. This reads the
// source rather than the behaviour, because the failure it guards against is a
// data race that a passing test would not catch.
func TestTheWatcherNeverTouchesCore(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "reload.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Anything the goroutine can reach: watcher's own methods, and the two
	// free functions run calls.
	onTheGoroutine := map[string]bool{"scanHost": true, "digest": true, "stamp": true, "since": true}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		onWatcher := fn.Recv != nil && strings.Contains(types.ExprString(fn.Recv.List[0].Type), "watcher")
		if !onWatcher && !onTheGoroutine[fn.Name.Name] {
			continue
		}
		// The whole declaration, not just the body: a Core arriving as a
		// parameter is the same door left open.
		ast.Inspect(fn, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if ok && id.Name == "Core" {
				t.Errorf("%s names Core; the poll loop must reach it only through the driver", fn.Name.Name)
			}
			return true
		})
	}

	// And the type itself holds no way to get there.
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != "watcher" {
				continue
			}
			if strings.Contains(types.ExprString(ts.Type), "Core") {
				t.Error("watcher holds a Core, so a later edit can reach it without going through the driver")
			}
		}
	}
}

// The loop must let go when watching is turned off; a goroutine still stat-ing
// a tree after :watch off is the leak this asserts against.
func TestPollLoopExitsWhenWatchingIsTurnedOff(t *testing.T) {
	w := &watcher{dir: hostDir(t), out: make(chan hostChange, 1), stop: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		w.run(scanHost(w.dir))
		close(done)
	}()
	close(w.stop)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the poll loop did not exit when watching was turned off")
	}
}

func TestPollLoopEmitsAnEventForAChange(t *testing.T) {
	dir := hostDir(t)
	ch := make(chan hostChange, 1)
	w := &watcher{dir: dir, out: ch, stop: make(chan struct{})}
	go w.run(scanHost(dir))
	t.Cleanup(func() { close(w.stop) })

	p := filepath.Join(dir, "internal", "greet", "greet.go")
	if err := os.WriteFile(p, []byte("package greet\n\nfunc Hello() string { return \"two\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-ch:
		if !strings.Contains(ev.What, "greet.go") {
			t.Errorf("the event does not name the file: %q", ev.What)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no event after an edit to a watched file")
	}
}

// Invariant 15: one thing touches Core at a time. A reload that landed in the
// middle of an evaluation would be the second thing.
func TestWatchReloadWaitsForTheEvaluationInFlight(t *testing.T) {
	m := newTestModel(t)
	m.busy = true

	next, _ := m.Update(changeMsg{What: "greet.go changed"})
	mm := next.(model)
	if mm.pendingReload != "greet.go changed" {
		t.Fatalf("pendingReload = %q, want the change held until the evaluation lands", mm.pendingReload)
	}
	if !mm.busy {
		t.Error("the reload cleared busy, so it ran beside the evaluation")
	}

	next, cmd := mm.Update(resultMsg(Result{Out: "(int) 1"}))
	nm := next.(model)
	if nm.pendingReload != "" {
		t.Errorf("pendingReload = %q, want it consumed once the evaluation finished", nm.pendingReload)
	}
	if !nm.busy {
		t.Error("the deferred reload did not start after the result landed")
	}
	if cmd == nil {
		t.Error("want commands for the printed result and the deferred reload")
	}
}

func TestWatchReloadRunsImmediatelyWhenIdle(t *testing.T) {
	m := newTestModel(t)
	next, cmd := m.Update(changeMsg{What: "greet.go changed"})
	mm := next.(model)
	if mm.pendingReload != "" {
		t.Errorf("pendingReload = %q, want the reload started rather than deferred", mm.pendingReload)
	}
	if !mm.busy {
		t.Error("busy = false, want the reload running")
	}
	if cmd == nil {
		t.Fatal("want the reload command and the re-armed listener")
	}
}

// A listener that is not put back makes the session notice one change and then
// go deaf, so the re-arm is returned whatever else the branch decides.
func TestChangeListenerIsReArmedWhileBusy(t *testing.T) {
	m := newTestModel(t)
	m.core, m.busy = testCore(t), true

	_, cmd := m.Update(changeMsg{What: "greet.go changed"})
	if cmd == nil {
		t.Fatal("a change arriving mid-evaluation left nothing listening for the next one")
	}
}

// firstMsg runs cmd's whole tree and returns the first message of type T.
//
// Every branch runs on its own goroutine, because one of the commands in these
// batches is the re-armed change listener, which blocks by design until the
// next change arrives — walking the batch in order would wait on it forever.
func firstMsg[T tea.Msg](cmd tea.Cmd, d time.Duration) (T, bool) {
	out := make(chan T, 4)
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() {
			msg := c()
			if batch, ok := msg.(tea.BatchMsg); ok {
				for _, sub := range batch {
					run(sub)
				}
				return
			}
			if want, ok := any(msg).(T); ok {
				out <- want
			}
		}()
	}
	run(cmd)
	select {
	case got := <-out:
		return got, true
	case <-time.After(d):
		var zero T
		return zero, false
	}
}

// The whole chain the driver actually runs: :watch on starts the loop, Init
// hands back the listener, an edit becomes a message, and the message becomes
// a reload that says what it saw. Each link is tested above; this is the one
// that fails if they are wired to each other wrongly.
func TestWatchReachesTheDriverEndToEnd(t *testing.T) {
	c, dir := attached(t)
	m := newModel(nil, "test")
	m.core, m.hist = c, &history{}

	if res := c.Submit(":watch on"); res.Err {
		t.Fatalf(":watch on: %s", res.Out)
	}
	t.Cleanup(c.StopWatch)

	listen := m.Init()
	if listen == nil {
		t.Fatal("Init returned no listener, so a change can never reach the driver")
	}

	p := filepath.Join(dir, "internal", "greet", "greet.go")
	if err := os.WriteFile(p, []byte("package greet\n\nfunc Hello() string { return \"two\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ev, ok := firstMsg[changeMsg](listen, 10*time.Second)
	if !ok {
		t.Fatal("the edit never reached the driver")
	}

	next, cmd := m.Update(ev)
	if !next.(model).busy {
		t.Error("the change did not start a reload")
	}
	res, ok := firstMsg[resultMsg](cmd, 10*time.Second)
	if !ok {
		t.Fatal("the reload produced no result")
	}
	if !strings.Contains(res.Out, "greet.go") || !strings.Contains(res.Out, "reloaded") {
		t.Errorf("the reload reported %q, want it to name the change and the reload", res.Out)
	}
}

func TestWaitForChangeDeliversTheNextEvent(t *testing.T) {
	ch := make(chan hostChange, 1)
	cmd := waitForChange(ch)
	ch <- hostChange{What: "greet.go changed"}
	msg, ok := cmd().(changeMsg)
	if !ok {
		t.Fatalf("waitForChange produced %T, want a changeMsg", cmd())
	}
	if msg.What != "greet.go changed" {
		t.Errorf("What = %q, want the observation carried through", msg.What)
	}
}

// TestStartupAttachDoesNotWalkTheIndex. host.Index walks the module's whole
// tree, and internal/host records that walk as "the ~0.3s that moved off the
// attach and onto first use". use's own comment says why :use pays it anyway —
// the count and the names are half of what somebody typing it asked for — and
// then names the case that must not: "a startup that attaches without being
// typed is what stops paying".
//
// Attach is that case. It is -host on the command line of a session that has
// not started, in front of a startup screen that would otherwise spend a third
// of a second with nothing to say. Behind indexOnce the walk still happens on
// the first line that needs the index, so this costs nothing in total.
//
// Walks is the counter that exists for exactly this assertion, rather than a
// timing one.
func TestStartupAttachDoesNotWalkTheIndex(t *testing.T) {
	c := testCore(t)
	dir := hostDir(t)
	if _, err := c.Attach(dir); err != nil {
		t.Fatalf("attach: %v", err)
	}
	h := c.ev.Host()
	if h == nil {
		t.Fatal("attach left the session standalone")
	}
	if got := h.Walks(); got != 0 {
		t.Errorf("startup attach walked the host tree %d time(s); the walk belongs at first use", got)
	}

	// :use is the typed one, and it still pays what it always paid.
	d := testCore(t)
	if res := d.Submit(":use " + dir); res.Err {
		t.Fatalf(":use: %s", res.Out)
	}
	if got := d.ev.Host().Walks(); got != 1 {
		t.Errorf(":use walked %d time(s), want 1 — the count is half of what it is for", got)
	}
}
