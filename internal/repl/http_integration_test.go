//go:build integration

package repl

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sandboxws/gluon/internal/pretty"
)

// The unit tests assert on generated source. These run it: a real child
// process, against a real listener, which is the only way to find out whether
// the mechanism the source describes actually held.

// TestHTTPDoesNotWriteTheCredentialIntoTheGeneratedSource.
//
// The value is in the child's environment and the source names the variable,
// so <tmp>/main.go — the file the build reads, the text :src renders and the
// key the result cache would have used — holds nothing. This asserts it
// against the file on disk rather than against httpSource's return value,
// because the question is whether the mechanism survived the whole path.
func TestHTTPDoesNotWriteTheCredentialIntoTheGeneratedSource(t *testing.T) {
	const token = "sq7Kd0aMzX9vLpQr2TfY"
	t.Setenv("GLUON_TEST_TOKEN", token)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	c := testCore(t)
	res := c.Submit(`:http GET ` + srv.URL + ` -H "Authorization: Bearer $GLUON_TEST_TOKEN"`)
	if res.Err {
		t.Fatalf("the request failed: %s", res.Out)
	}

	src, err := os.ReadFile(filepath.Join(c.ev.Dir(), "main.go"))
	if err != nil {
		t.Fatalf("reading the generated program: %v", err)
	}
	if strings.Contains(string(src), token) {
		t.Errorf("the credential is in the generated source:\n%s", src)
	}
	if !strings.Contains(string(src), `os.Getenv("GLUON_TEST_TOKEN")`) {
		t.Errorf("the generated program does not name the variable:\n%s", src)
	}
	// And the session's own program, which is what :src and :save render.
	if out := c.Submit(":src"); strings.Contains(out.Out, token) {
		t.Errorf(":src carries the credential:\n%s", out.Out)
	}
}

// TestHTTPIsLiveAcrossTwoIdenticalRequests.
//
// The result cache is keyed on program text alone, so two identical :http
// lines render byte-identical programs. Served from the cache the second would
// replay the first response — an answer from whenever you first asked,
// presented as current, which is the failure :query documents in its own
// words. A counter in the handler makes a cached answer repeat and a live one
// not.
func TestHTTPIsLiveAcrossTwoIdenticalRequests(t *testing.T) {
	var n int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, "request %d", atomic.AddInt64(&n, 1))
	}))
	defer srv.Close()

	c := testCore(t)
	var seen []string
	for i := 0; i < 3; i++ {
		res := c.Submit(":http GET " + srv.URL)
		if res.Err {
			t.Fatalf("run %d: %s", i, res.Out)
		}
		seen = append(seen, res.Out)
	}
	if seen[0] == seen[1] || seen[1] == seen[2] {
		t.Errorf("identical requests returned identical answers — the response was cached:\n%s", seen[0])
	}
	if got := atomic.LoadInt64(&n); got != 3 {
		t.Errorf("the endpoint saw %d requests, want 3 — one of them never left gluon", got)
	}
}

// TestAnEchoedCredentialIsMasked.
//
// The source cannot leak the value, so the child's own output is the one
// surface left — and an endpoint that reflects what you sent is an ordinary
// thing, not a contrived one: every debug echo endpoint does it.
func TestAnEchoedCredentialIsMasked(t *testing.T) {
	const token = "sq7Kd0aMzX9vLpQr2TfY"
	t.Setenv("GLUON_TEST_TOKEN", token)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, "you sent %s", r.Header.Get("Authorization"))
	}))
	defer srv.Close()

	c := testCore(t)
	res := c.Submit(`:http GET ` + srv.URL + ` -H "Authorization: Bearer $GLUON_TEST_TOKEN"`)
	if res.Err {
		t.Fatalf("the request failed: %s", res.Out)
	}
	if strings.Contains(res.Out, token) {
		t.Errorf("the echoed credential reached the screen:\n%s", res.Out)
	}
	if !strings.Contains(res.Out, "you sent Bearer") {
		t.Errorf("the body is not in the output at all, so the mask proves nothing:\n%s", res.Out)
	}
	if !strings.Contains(res.Out, "***") {
		t.Errorf("the credential was removed rather than masked:\n%s", res.Out)
	}
}

// TestSessionIsUnchangedAfterARequest is invariant 14.
//
// A transient evaluation must not mutate evaluator state. The imports matter
// as much as the entries here: :http supplies six of its own, and one left
// behind in e.imports would make the next ordinary line write an import it
// does not use, fail to build, and recover only by paying a full goimports
// pass on a line that had nothing to do with the request.
func TestSessionIsUnchangedAfterARequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()

	c := testCore(t)
	// One ordinary line first, so the comparison is against a resolved import
	// set rather than an empty one.
	if res := c.Submit(`strings.ToUpper("before")`); res.Err {
		t.Fatalf("%s", res.Out)
	}
	entries, imports := len(c.sess.Entries), len(c.ev.Imports())

	if res := c.Submit(":http GET " + srv.URL); res.Err {
		t.Fatalf("the request failed: %s", res.Out)
	}
	if got := len(c.sess.Entries); got != entries {
		t.Errorf("the session has %d entries after a request, had %d", got, entries)
	}
	if got := len(c.ev.Imports()); got != imports {
		t.Errorf("the import set has %d entries after a request, had %d — a transient "+
			"evaluation mutated evaluator state", got, imports)
	}

	// The proof that matters: the next ordinary line still builds.
	res := c.Submit(`strings.ToUpper("after")`)
	if res.Err {
		t.Fatalf("an ordinary line failed after a request: %s", res.Out)
	}
	if !strings.Contains(res.Out, "AFTER") {
		t.Errorf("unexpected output: %s", res.Out)
	}
	src, err := c.source()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(src, srv.URL) {
		t.Errorf("the request leaked into the session's program:\n%s", src)
	}
}

// TestTwoRequestsShareNoState.
//
// Constraint A, made visible. The child dies after every line, so there is no
// cookie jar and no connection to reuse — and the command must not imply
// otherwise. A server that sets a cookie on the first response is the cheapest
// way to ask whether the second request carried anything forward.
func TestTwoRequestsShareNoState(t *testing.T) {
	var carried atomic.Bool
	var seen int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt64(&seen, 1) > 1 && r.Header.Get("Cookie") != "" {
			carried.Store(true)
		}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc123"})
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	c := testCore(t)
	for i := 0; i < 2; i++ {
		if res := c.Submit(":http GET " + srv.URL); res.Err {
			t.Fatalf("run %d: %s", i, res.Out)
		}
	}
	if carried.Load() {
		t.Error("the second request carried the first response's cookie — :http kept state it has nowhere to keep")
	}
}

// TestABodyOverTheBoundSaysWhatWasSkipped, against a real response rather than
// a hand-built report.
//
// Both halves of the notice are here because a real server produces both:
// Content-Length when it knows the size, and chunked encoding when it does
// not — which is what net/http does for anything past its sniff buffer unless
// the handler says otherwise.
func TestABodyOverTheBoundSaysWhatWasSkipped(t *testing.T) {
	const size = maxBody * 2
	body := strings.Repeat("x", size)

	sized := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", strconv.Itoa(size))
		w.Write([]byte(body))
	}))
	defer sized.Close()
	chunked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(body))
	}))
	defer chunked.Close()

	c := testCore(t)
	res := c.Submit(":http GET " + sized.URL)
	if res.Err {
		t.Fatalf("the request failed: %s", res.Out)
	}
	if !strings.Contains(res.Out, "truncated") {
		t.Fatalf("a body twice the bound was shown without saying so:\n%s", res.Out[:min(len(res.Out), 400)])
	}
	if !strings.Contains(res.Out, fmt.Sprint(size)) {
		t.Errorf("the notice does not state the full size the response reported")
	}
	// The bound is what the child read, so what crossed the pipe is bounded
	// too — that is the whole reason it is applied there.
	if n := strings.Count(res.Out, "x"); n > maxBody+64 {
		t.Errorf("%d bytes of body came back, past the %d-byte bound", n, maxBody)
	}

	// No Content-Length, so there is no honest number for what was skipped,
	// and the notice says that rather than inferring one.
	res = c.Submit(":http GET " + chunked.URL)
	if res.Err {
		t.Fatalf("the request failed: %s", res.Out)
	}
	if !strings.Contains(res.Out, "truncated") {
		t.Fatalf("a chunked body twice the bound was shown without saying so")
	}
	if !strings.Contains(res.Out, "not reported") {
		t.Errorf("a size nobody reported was printed as a number:\n%s", res.Out[:min(len(res.Out), 400)])
	}
}

// TestANonTextualBodyIsNotRead. The summary is not a display choice made after
// the fact: bytes nobody will look at never leave the endpoint.
func TestANonTextualBodyIsNotRead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 512)))
	}))
	defer srv.Close()

	c := testCore(t)
	res := c.Submit(":http GET " + srv.URL)
	if res.Err {
		t.Fatalf("the request failed: %s", res.Out)
	}
	if !strings.Contains(res.Out, "image/png") || !strings.Contains(res.Out, "not shown") {
		t.Errorf("a PNG was not summarised:\n%s", res.Out)
	}
	if strings.Contains(res.Out, "PNG\r\n") {
		t.Errorf("the PNG's bytes were printed:\n%q", res.Out)
	}
}

// TestAnUnreachableEndpointReportsTheCause, and renders no response — half an
// answer beside a failure reads as a response that arrived.
func TestAnUnreachableEndpointReportsTheCause(t *testing.T) {
	// A closed listener: a port nothing is on refuses immediately, which keeps
	// the test off the timeout path and off the network.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	c := testCore(t)
	res := c.Submit(":http GET " + url)
	if !res.Err {
		t.Fatalf("a request to a closed listener succeeded:\n%s", res.Out)
	}
	if !strings.Contains(res.Out, "the request failed") {
		t.Errorf("the failure is not named:\n%s", res.Out)
	}
	if strings.Contains(res.Out, "http.Response") {
		t.Errorf("a response was rendered for a request that never got one:\n%s", res.Out)
	}
}

// TestTheResponseGoesThroughTheOrdinaryValuePath.
//
// :http renders no response itself. In a terminal the http plugin's renderer
// draws the status line and the header table, and through a pipe pretty.Plain
// writes the same struct it writes for any other value — invariant 21, asserted
// for this command because it is the one that made a plugin-owned renderer part
// of a builtin's output.
func TestTheResponseGoesThroughTheOrdinaryValuePath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "abc-123")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()

	c := testCore(t)
	styles := pretty.PlainStyles()
	hooks := c.Hooks()
	c.Render = func(v []pretty.Value) string { return pretty.RichWith(v, styles, hooks) }

	rich := c.Submit(":http GET " + srv.URL)
	if rich.Err {
		t.Fatalf("the request failed: %s", rich.Out)
	}
	for _, want := range []string{"(*http.Response)", "200 OK", "X-Request-Id", `{"ok":true}`} {
		if !strings.Contains(rich.Out, want) {
			t.Errorf("the rich rendering is missing %q:\n%s", want, rich.Out)
		}
	}
	// The renderer's whole job: a header's values without their slice brackets.
	if strings.Contains(rich.Out, `["abc-123"]`) {
		t.Errorf("the header table was not used:\n%s", rich.Out)
	}

	c.Render = pretty.Plain
	plain := c.Submit(":http GET " + srv.URL)
	if plain.Err {
		t.Fatalf("the request failed: %s", plain.Out)
	}
	if !strings.Contains(plain.Out, `"X-Request-Id":["abc-123"]`) {
		t.Errorf("Plain did not get the ordinary struct form:\n%s", plain.Out)
	}
	// The body and the notices are gluon's own text, so they survive the pipe.
	if !strings.Contains(plain.Out, `{"ok":true}`) {
		t.Errorf("the body did not reach a piped reader:\n%s", plain.Out)
	}
}
