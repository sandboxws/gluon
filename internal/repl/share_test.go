package repl

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sandboxws/gluon/internal/syntax"

	"github.com/sandboxws/gluon/internal/config"
)

// The tests below are the two halves of what makes :share safe: that what is
// shown is what is sent, and that nothing gets sent without somebody saying so
// this time. Everything else follows from those.

// shareServer stands in for the Playground and records what reached it. The
// upload is the one part of this command that cannot be exercised against the
// real service, so every path that must not make a request is asserted on the
// count rather than on the message.
func shareServer(t *testing.T, h http.HandlerFunc) (calls *atomic.Int64, body *atomic.Pointer[string]) {
	t.Helper()
	calls, body = &atomic.Int64{}, &atomic.Pointer[string]{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		b, _ := io.ReadAll(r.Body)
		s := string(b)
		body.Store(&s)
		h(w, r)
	}))
	t.Cleanup(srv.Close)

	endpoint, play := shareEndpoint, sharePlay
	shareEndpoint = srv.URL
	t.Cleanup(func() { shareEndpoint, sharePlay = endpoint, play })
	return calls, body
}

// okServer answers the way the service does: the snippet's identifier, alone.
func okServer(t *testing.T, id string) (*atomic.Int64, *atomic.Pointer[string]) {
	t.Helper()
	return shareServer(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, id)
	})
}

// richCore is a Core that reports a terminal, which is what the confirmation
// path needs. Rich is set in exactly one place in production — newModel — so a
// test that wants the modal has to say so itself.
func richCore(t *testing.T) *Core {
	t.Helper()
	c := testCore(t)
	c.Rich = true
	return c
}

// confirmLine is what the view submits: the spec's own Confirm.Run, never a
// line a test composed. Reading it from the spec is the point — a test that
// built the line itself would still pass if the token stopped being checked.
func confirmLine(t *testing.T, res Result) string {
	t.Helper()
	if res.Modal == nil || res.Modal.Confirm == nil {
		t.Fatalf(":share did not ask: %#v", res.Modal)
	}
	return res.Modal.Confirm.Run
}

// TestShareShowsExactlyWhatItUploads is task 1.1 and the design's first goal:
// what is confirmed is byte-for-byte what is sent. Two renderings of the
// session — one to display, one to post — is the shape this exists to refuse.
func TestShareShowsExactlyWhatItUploads(t *testing.T) {
	c := richCore(t)
	c.Submit("x := 41")
	c.Submit("x + 1")

	res := c.Submit(":share")
	if res.Err {
		t.Fatalf(":share: %s", res.Out)
	}
	shown := res.Modal.Text

	calls, body := okServer(t, "abc123")
	if got := c.Submit(confirmLine(t, res)); got.Err {
		t.Fatalf(":share confirmed: %s", got.Out)
	}
	if calls.Load() != 1 {
		t.Fatalf("the service saw %d requests, want 1", calls.Load())
	}
	if sent := *body.Load(); sent != shown {
		t.Errorf("what was uploaded is not what was shown:\nshown:\n%q\nsent:\n%q", shown, sent)
	}
	// And Out carries the same bytes linearly — invariant 19, which is also
	// what makes the pipe path lose nothing.
	if !strings.Contains(res.Out, shown) {
		t.Errorf("Out does not carry the content the modal shows:\n%s", res.Out)
	}
}

// TestShareUploadsNoHistoryEnvironmentOrHostSource is task 1.2. The attached
// module is the one that matters: a session with -host can name a private
// codebase, and uploading its source would be a serious leak from a command
// the user thinks is sharing their snippet.
func TestShareUploadsNoHistoryEnvironmentOrHostSource(t *testing.T) {
	c := richCore(t)
	dir := hostDir(t)
	if _, err := c.Attach(dir); err != nil {
		t.Fatalf("attach: %v", err)
	}
	c.Submit("greet.Hello()")
	// A meta command is history, not a session entry. It must not travel.
	c.Submit(":hist")

	res := c.Submit(":share")
	if res.Err {
		t.Fatalf(":share: %s", res.Out)
	}
	content := res.Modal.Text

	// The host's package is named — that is what an attached session is for —
	// and its source is not. The fixture's only function body is `return
	// "one"`, so the body is the thing to look for.
	if !strings.Contains(content, "greet") {
		t.Fatalf("the program does not name the host package it uses:\n%s", content)
	}
	for _, never := range []string{`return "one"`, "package greet", ":hist", dir} {
		if strings.Contains(content, never) {
			t.Errorf("the upload carries %q, which is not the rendered program:\n%s", never, content)
		}
	}
	// Nothing out of gluon's own environment, either. PATH is set in every
	// process this runs in, and is the cheapest thing to look for.
	if strings.Contains(content, "PATH=") {
		t.Errorf("the upload carries the environment:\n%s", content)
	}
}

// lineOf is where a substring sits in the program the session renders to,
// read from :src rather than assumed. The refusal is only useful if the number
// it prints is the number the reader would count to.
func lineOf(t *testing.T, c *Core, want string) string {
	t.Helper()
	src := c.Submit(":src")
	if src.Err {
		t.Fatalf(":src: %s", src.Out)
	}
	for i, line := range strings.Split(src.Out, "\n") {
		if strings.Contains(line, want) {
			return strconv.Itoa(i + 1)
		}
	}
	t.Fatalf("%q is not in the rendered program:\n%s", want, src.Out)
	return ""
}

// TestShareRefusesAConnectionString and its neighbour are task 1.3, over the
// two shapes the shared secret test is built around: a DSN, which needs the
// parser, and a token, which does not.
func TestShareRefusesAConnectionString(t *testing.T) {
	c := richCore(t)
	c.Submit(`dsn := "postgres://app:hunter2@db.internal:5432/orders"`)

	calls, _ := okServer(t, "abc123")
	res := c.Submit(":share")
	if !res.Err {
		t.Fatalf(":share published a session holding a DSN: %s", res.Out)
	}
	for _, want := range []string{"refused", "***", "nothing was uploaded"} {
		if !strings.Contains(res.Out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, res.Out)
		}
	}
	// The line it names is the line of the program, which is what makes the
	// edit targeted — not the ordinal of the entry, which is a different
	// number and points somewhere else.
	if want := lineOf(t, c, "db.internal"); !strings.Contains(res.Out, "line "+want) {
		t.Errorf("the refusal does not name line %s, where the value is:\n%s", want, res.Out)
	}
	// Named, not reprinted: a refusal that echoed the password back would put
	// it somewhere else again.
	if strings.Contains(res.Out, "hunter2") {
		t.Errorf("the refusal reprints the credential:\n%s", res.Out)
	}
	if res.Modal != nil {
		t.Error("a refused share still offered a confirmation")
	}
	if calls.Load() != 0 {
		t.Errorf("a refused share made %d requests", calls.Load())
	}
}

func TestShareRefusesABearerToken(t *testing.T) {
	c := richCore(t)
	c.Submit(`auth := "Bearer ghp_wJ4bQ2xTfN8pLmR7yZ1vKcH5aD0eS3gU6iO9"`)

	calls, _ := okServer(t, "abc123")
	res := c.Submit(":share")
	if !res.Err {
		t.Fatalf(":share published a session holding a token: %s", res.Out)
	}
	if want := lineOf(t, c, "ghp_"); !strings.Contains(res.Out, "line "+want) {
		t.Errorf("the refusal does not name line %s, where the value is:\n%s", want, res.Out)
	}
	if strings.Contains(res.Out, "wJ4bQ2xTfN8pLmR7") {
		t.Errorf("the refusal reprints the credential:\n%s", res.Out)
	}
	if calls.Load() != 0 {
		t.Errorf("a refused share made %d requests", calls.Load())
	}
}

// TestShareJudgesTheValueAndNotTheName is invariant 23, checked where it would
// be easiest to break: a variable called `password` holding something that is
// not one must still share, and a variable called `note` holding a token must
// still be refused. shareSecrets has no name to consult, and this is what says
// so.
func TestShareJudgesTheValueAndNotTheName(t *testing.T) {
	for _, tc := range []struct {
		name    string
		src     string
		refused bool
	}{
		{name: "a harmless value under an alarming name", src: `password := "ask the team"`},
		{name: "a credential under a dull name", src: `note := "ghp_wJ4bQ2xTfN8pLmR7yZ1vKcH5aD0eS3gU6iO9"`, refused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := richCore(t)
			c.Submit(tc.src)
			if got := c.Submit(":share"); got.Err != tc.refused {
				t.Errorf(":share refused=%v, want %v:\n%s", got.Err, tc.refused, got.Out)
			}
		})
	}
}

// TestNoFlagLetsARefusedShareThroughAndNoneSkipsTheAsking is tasks 1.4 and 2.3.
//
// The command takes no argument at all, so there is no flag to find. The one
// argument it recognises is the token the confirmation itself issues, and the
// two things asserted here are that it is not a way in — it refuses when there
// is nothing pending, and one confirmation's token is not another's.
func TestNoFlagLetsARefusedShareThroughAndNoneSkipsTheAsking(t *testing.T) {
	// The registry says the command takes nothing, which is what :help prints
	// and what completion offers.
	cmd, ok := (&Core{}).lookup(":share")
	if !ok {
		t.Fatal(":share is not registered")
	}
	if cmd.Arg != "" {
		t.Errorf(":share advertises the argument %q; it takes none", cmd.Arg)
	}

	c := richCore(t)
	c.Submit(`dsn := "postgres://app:hunter2@db.internal:5432/orders"`)
	calls, _ := okServer(t, "abc123")

	// Nothing pending, because the share was refused — so no answer publishes
	// it, whatever the answer is.
	c.Submit(":share")
	for _, line := range []string{
		":share " + shareConfirmFlag,
		":share " + shareConfirmFlag + " yes",
		":share " + shareConfirmFlag + " 00000000000000000000000000000000",
	} {
		if got := c.Submit(line); !got.Err {
			t.Errorf("%q published a refused session: %s", line, got.Out)
		}
	}
	if calls.Load() != 0 {
		t.Errorf("a refused session was uploaded %d times", calls.Load())
	}

	// And a token is one confirmation's, not a standing permission: the second
	// :share replaces the first, and the first token no longer answers.
	c2 := richCore(t)
	c2.Submit("1 + 1")
	stale := confirmLine(t, c2.Submit(":share"))
	c2.Submit(":share")
	if got := c2.Submit(stale); !got.Err {
		t.Errorf("a superseded confirmation still uploaded: %s", got.Out)
	}
	if calls.Load() != 0 {
		t.Errorf("a stale token uploaded %d times", calls.Load())
	}
}

// TestNoConfigurationSettingReachesSharing is the other half of 2.3: the
// configuration schema is the place a pre-authorisation would be added, and
// this is the assertion that reads it. gluon has no setting that touches
// sharing, and adding one is a decision that has to break this test first.
func TestNoConfigurationSettingReachesSharing(t *testing.T) {
	for _, o := range config.Options() {
		for _, word := range []string{"share", "publish", "upload", "confirm"} {
			if strings.Contains(strings.ToLower(o.Key), word) {
				t.Errorf("the configuration defines %q — :share's consent is per-invocation "+
					"and must not be settable", o.Key)
			}
		}
	}
}

// openShareModal drives the real TUI over a real session, so the modal under
// test is the one :share actually opens rather than a spec a test wrote.
func openShareModal(t *testing.T, c *Core) (model, []tea.Msg) {
	t.Helper()
	m := newTestModel(t)
	m.core = c
	m.winW, m.winH = 80, 24

	res := c.Submit(":share")
	if res.Err {
		t.Fatalf(":share: %s", res.Out)
	}
	next, cmd := m.Update(resultMsg(res))
	return next.(model), flatten(cmd)
}

// TestShareModalHoldsInvariant19 is task 2.1, asserting both halves the way
// modal_test.go does: nothing reaches scrollback while the view is up, and
// closing it leaves exactly one line — plus the third thing the invariant
// requires and only a confirmation makes load-bearing, that Result.Out carries
// the same content linearly for the driver that cannot go full-screen.
func TestShareModalHoldsInvariant19(t *testing.T) {
	c := richCore(t)
	c.Submit("x := 41")

	res := c.Submit(":share")
	if res.Modal == nil {
		t.Fatal(":share opened no confirmation")
	}
	if !strings.Contains(res.Out, res.Modal.Text) {
		t.Errorf("Out does not carry the content linearly:\n%s", res.Out)
	}
	// Out is the program plus a line of prose, so it is not all one language.
	// Lang says the whole of Out is — the modal's text is what gets tagged.
	if res.Lang != syntax.None {
		t.Errorf("Result.Lang is set on output that is not all source")
	}

	m, msgs := openShareModal(t, c)
	if m.modal == nil {
		t.Fatal(":share's result did not open a modal")
	}
	if !hasType(msgs, "altscreen") {
		t.Errorf("opening the confirmation must enter the alt screen; got %v", typeNames(msgs))
	}
	if lines := printedLines(msgs); len(lines) != 0 {
		t.Errorf("printed while opening the confirmation: %q", lines)
	}
	// Scrolling the program is the whole reason it is shown; still nothing
	// may be printed.
	for _, k := range []string{"j", "j", "k"} {
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
		m = next.(model)
		if lines := printedLines(flatten(cmd)); len(lines) != 0 {
			t.Errorf("printed while the confirmation was open: %q", lines)
		}
	}
	// The key that publishes has to be on the screen. A confirmation whose
	// key is not named is not one that was asked.
	if view := m.View(); !strings.Contains(view, "upload it") {
		t.Errorf("the confirmation does not name the key that uploads:\n%s", view)
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if next.(model).modal != nil {
		t.Fatal("q did not close the confirmation")
	}
	lines := printedLines(flatten(cmd))
	if len(lines) != 1 {
		t.Fatalf("want exactly one summary line in scrollback, got %d: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "nothing was uploaded") {
		t.Errorf("the summary of a declined share = %q, want it to say nothing went", lines[0])
	}
}

// TestDecliningUploadsNothing is task 2.2's second half, through the same path
// a reader takes: open the view, read the program, press q.
func TestDecliningUploadsNothing(t *testing.T) {
	c := richCore(t)
	c.Submit("x := 41")
	calls, _ := okServer(t, "abc123")

	m, _ := openShareModal(t, c)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if next.(model).modal != nil {
		t.Fatal("q did not close the confirmation")
	}
	if calls.Load() != 0 {
		t.Fatalf("declining made %d requests", calls.Load())
	}
	lines := printedLines(flatten(cmd))
	if len(lines) != 1 || !strings.Contains(lines[0], "nothing was uploaded") {
		t.Errorf("declining left %q in scrollback, want a line saying nothing went", lines)
	}
}

// TestConfirmingUploadsAndSaysWhere is task 2.2's first half, driven the same
// way: enter in the open view is what publishes, and it is the only thing that
// does.
func TestConfirmingUploadsAndSaysWhere(t *testing.T) {
	c := richCore(t)
	c.Submit("x := 41")
	calls, body := okServer(t, "abc123")

	m, _ := openShareModal(t, c)
	shown := m.modal.spec.Text

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.modal == nil {
		t.Fatal("confirming closed the view; it stays open to report what happened")
	}
	// Enter hands the line back rather than running anything itself — the
	// model owns the goroutine Core may be touched from — so the test has to
	// run what the command it produced would have.
	for _, msg := range flatten(cmd) {
		next, _ := m.Update(msg)
		m = next.(model)
	}
	if calls.Load() != 1 {
		t.Fatalf("confirming made %d requests, want 1", calls.Load())
	}
	if sent := *body.Load(); sent != shown {
		t.Errorf("uploaded bytes differ from the ones shown:\nshown:\n%q\nsent:\n%q", shown, sent)
	}

	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if next.(model).modal != nil {
		t.Fatal("q did not close the confirmation")
	}
	lines := printedLines(flatten(cmd))
	if len(lines) != 1 {
		t.Fatalf("want exactly one line in scrollback, got %d: %q", len(lines), lines)
	}
	// The line scrollback keeps is what the upload answered, not the spec's
	// "nothing was uploaded" — a transcript that said nothing went after
	// something did would have lost the only part worth keeping.
	if !strings.Contains(lines[0], sharePlay+"abc123") {
		t.Errorf("the summary of a confirmed share = %q, want the address", lines[0])
	}
}

// TestShareLeavesTheHermeticEnvironmentAlone is task 3.1. The evaluator's
// environment is os.Environ() plus four fixed settings, GOPROXY=off among them
// (internal/eval/eval.go), and only :get relaxes it. This command reaches the
// network with net/http from gluon's own process, so the way to show the
// evaluator is untouched is to show the environment it is derived from is.
func TestShareLeavesTheHermeticEnvironmentAlone(t *testing.T) {
	c := richCore(t)
	c.Submit("x := 41")
	okServer(t, "abc123")

	before := os.Environ()
	res := c.Submit(":share")
	if got := c.Submit(confirmLine(t, res)); got.Err {
		t.Fatalf(":share confirmed: %s", got.Out)
	}
	if after := os.Environ(); !slices.Equal(before, after) {
		t.Errorf("sharing changed the environment the evaluator builds on:\nbefore %v\nafter  %v",
			before, after)
	}
}

// TestShareReportsTheAddressVerbatim is task 3.2. The service answers with the
// snippet's identifier; the identifier is reproduced exactly, under the
// service's own path, and nothing about it is shortened or rewritten.
func TestShareReportsTheAddressVerbatim(t *testing.T) {
	const id = "aB3-_xY9zQ"
	c := richCore(t)
	c.Submit("x := 41")
	okServer(t, id)

	res := c.Submit(confirmLine(t, c.Submit(":share")))
	if res.Err {
		t.Fatalf(":share confirmed: %s", res.Out)
	}
	if want := sharePlay + id; !strings.Contains(res.Out, want) {
		t.Errorf(":share answered %q, want the address %q", res.Out, want)
	}
}

// TestAFailedShareSaysNothingWasPublished is task 3.3, over all three ways it
// can fail. An ambiguous message after a network write is the worst outcome
// there is, because the reader cannot tell whether to worry — so the three
// failures read as one from their side, and that is deliberate.
func TestAFailedShareSaysNothingWasPublished(t *testing.T) {
	for _, tc := range []struct {
		name  string
		serve http.HandlerFunc
		// unreachable closes the server before the request, which is the one
		// case no handler can express.
		unreachable bool
	}{
		{
			name:        "the service is unreachable",
			serve:       func(w http.ResponseWriter, _ *http.Request) {},
			unreachable: true,
		},
		{
			name: "the service rejects the upload",
			serve: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				io.WriteString(w, "Snippet is too large")
			},
		},
		{
			name: "the answer names no snippet",
			serve: func(w http.ResponseWriter, _ *http.Request) {
				io.WriteString(w, "\n")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := richCore(t)
			c.Submit("x := 41")
			shareServer(t, tc.serve)
			if tc.unreachable {
				// A port nothing is listening on. The request fails in the
				// client rather than at the server, which is the path a
				// handler cannot reach.
				shareEndpoint = "http://127.0.0.1:1"
			}

			res := c.Submit(confirmLine(t, c.Submit(":share")))
			if !res.Err {
				t.Fatalf(":share reported success: %s", res.Out)
			}
			if !strings.Contains(res.Out, "nothing was published") {
				t.Errorf("a failed share answered %q, want it to say nothing was published", res.Out)
			}
			if strings.Contains(res.Out, sharePlay) {
				t.Errorf("a failed share answered with an address: %s", res.Out)
			}
		})
	}
}

// TestShareLeavesTheSessionUnchanged is task 3.4. It is not an evaluation: the
// program text already exists, and the command formats it, asks, and posts.
func TestShareLeavesTheSessionUnchanged(t *testing.T) {
	c := richCore(t)
	c.Submit(`import "strings"`)
	c.Submit(`s := strings.ToUpper("go")`)
	okServer(t, "abc123")

	entries := len(c.sess.Entries)
	src := c.Submit(":src").Out

	res := c.Submit(":share")
	if got := c.Submit(confirmLine(t, res)); got.Err {
		t.Fatalf(":share confirmed: %s", got.Out)
	}
	if now := len(c.sess.Entries); now != entries {
		t.Errorf("sharing took the session from %d entries to %d", entries, now)
	}
	if now := c.Submit(":src").Out; now != src {
		t.Errorf("sharing changed the program:\nbefore:\n%s\nafter:\n%s", src, now)
	}
}

// TestThroughAPipeItPrintsAndDeclines is task 4.1. A pipe cannot consent, and
// there is deliberately no second form to offer it the way :replay offers -run
// — so what it gets is the content and a line saying why nothing went.
func TestThroughAPipeItPrintsAndDeclines(t *testing.T) {
	c := testCore(t) // not Rich: this is the pipe, and `gluon -e`
	c.Submit("x := 41")
	calls, _ := okServer(t, "abc123")

	res := c.Submit(":share")
	if calls.Load() != 0 {
		t.Fatalf("a pipe uploaded %d times", calls.Load())
	}
	if res.Modal != nil {
		t.Error("a driver that cannot go full-screen was handed a modal to open")
	}
	// The content that would have gone is most of the value and carries no
	// risk — it is :src with a note — so the degradation is to print it, not
	// to refuse the way :edit does.
	if src := strings.TrimSpace(c.Submit(":src").Out); !strings.Contains(res.Out, src) {
		t.Errorf(":share through a pipe did not print what would have been uploaded:\n%s", res.Out)
	}
	for _, want := range []string{"nothing was uploaded", "needs a terminal"} {
		if !strings.Contains(res.Out, want) {
			t.Errorf(":share through a pipe does not say %q:\n%s", want, res.Out)
		}
	}
	// And nothing is left pending for anything to answer.
	if got := c.Submit(":share " + shareConfirmFlag + " anything"); !got.Err {
		t.Errorf("a pipe left a confirmation something could answer: %s", got.Out)
	}
	if calls.Load() != 0 {
		t.Errorf("a pipe uploaded %d times", calls.Load())
	}
}

// TestShareIsRegisteredWithNoToolName is task 4.2 at the registry. The server
// half — that no tool for sharing is listed with or without --eval — is
// cmd/gluon's TestStaticSurfaceIsExactlyTheStaticTier and
// TestEvalSurfaceAddsTheEvaluators, which write the whole surface out.
func TestShareIsRegisteredWithNoToolName(t *testing.T) {
	cmd, ok := (&Core{}).lookup(":share")
	if !ok {
		t.Fatal(":share is not registered")
	}
	if cmd.Group != groupTranscript {
		t.Errorf(":share is in group %q, want %q", cmd.Group, groupTranscript)
	}
	if cmd.MCP != "" {
		t.Errorf(":share is exposed as the tool %q — an agent does not own the "+
			"decision to publish the user's code", cmd.MCP)
	}
	if cmd.Static {
		t.Error(":share claims the static tier, which would expose it without --eval")
	}
	// The Detail is where the one fact that cannot be undone is stated, and
	// `:help :share` is where a reader meets it.
	help := (&Core{}).help(":share").Out
	for _, want := range []string{"public", "cannot be withdrawn"} {
		if !strings.Contains(help, want) {
			t.Errorf(":help :share does not say the upload is %q:\n%s", want, help)
		}
	}
}
