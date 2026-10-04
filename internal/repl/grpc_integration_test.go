//go:build integration

package repl

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The :grpc tests run against a real reflective server, because every part of
// this command that could be wrong is a part a stub would have to fake: what
// reflection returns, which methods are streaming, what a status error looks
// like, and what protojson says about a field that is not there.
//
// The server is its own module under testdata for the reason its own comment
// gives — gluon links neither gRPC nor protobuf, and a test binary importing
// them would put both in gluon's go.mod. testdata is invisible to the go tool,
// so it stays out of the module graph, `go build ./...` and `go vet ./...`.
//
// Both halves need the network the first time: the session has to :get gRPC to
// have it in its build list at all, and the server module has to resolve its
// own requirements. Both are cached afterwards.

const grpcModule = "google.golang.org/grpc"

// probe is the built test server, built once for the whole package.
//
// Built rather than `go run`: `go run` execs the program as a *child* of
// itself, so killing what the test started leaves the server listening and
// holding the pipe the test is reading — which ends as a package that passes
// every test and then hangs until the harness gives up on its I/O. Killing the
// binary directly is the whole fix, and building once is the bonus.
var probe struct {
	sync.Once
	path string
	err  error
}

func probeBinary(t *testing.T) string {
	t.Helper()
	probe.Once.Do(func() {
		dir, err := os.MkdirTemp("", "gluon-probe")
		if err != nil {
			probe.err = err
			return
		}
		probe.path = filepath.Join(dir, "probe")
		build := exec.Command("go", "build", "-o", probe.path, ".")
		build.Dir = "testdata/grpcserver"
		out, err := build.CombinedOutput()
		if err != nil {
			probe.err = fmt.Errorf("%v: %s", err, out)
		}
	})
	if probe.err != nil {
		t.Skip("cannot build the probe server:", probe.err)
	}
	return probe.path
}

// startProbe runs the test server and returns the address it is listening on.
//
// The address is read from the server rather than chosen here, because a port
// picked in advance is a port something else on the machine may already hold —
// and a test that fails for that reason fails in a way that looks like a bug in
// the code under test.
func startProbe(t *testing.T, args ...string) string {
	t.Helper()
	online(t)
	bin := probeBinary(t)

	cmd := exec.Command(bin, args...)
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Skip("cannot run the probe server:", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})

	type line struct {
		text string
		err  error
	}
	ch := make(chan line, 1)
	go func() {
		s := bufio.NewScanner(out)
		if s.Scan() {
			ch <- line{text: strings.TrimSpace(s.Text())}
			return
		}
		ch <- line{err: s.Err()}
	}()
	select {
	case l := <-ch:
		if l.text == "" {
			t.Skip("the probe server printed no address:", l.err)
		}
		return l.text
	case <-time.After(30 * time.Second):
		t.Skip("the probe server did not start in time")
	}
	return ""
}

// withGRPC is a session that can see gRPC, which is what makes :grpc exist at
// all — the plugin activates off the build list and nothing else.
func withGRPC(t *testing.T) *Core {
	t.Helper()
	online(t)
	c, err := NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if res := c.Submit(":grpc localhost:1"); !res.Err ||
		!strings.Contains(res.Out, "not active") {
		t.Fatalf(":grpc exists before gRPC is in the build list: %q", res.Out)
	}
	if res := c.Submit(":get " + grpcModule); res.Err {
		t.Skip("cannot fetch gRPC:", res.Out)
	}
	if _, ok := c.lookup(":grpc"); !ok {
		t.Fatal(":grpc did not appear after gRPC entered the build list")
	}
	return c
}

// TestGRPCListsAReflectiveServer, with each method marked by whether one
// evaluation can serve it. The mark is the whole point of the listing: a
// streaming method that is listed and then refused at the call is a surprise,
// and one listed as uncallable is a fact the reader has beforehand.
func TestGRPCListsAReflectiveServer(t *testing.T) {
	addr := startProbe(t)
	c := withGRPC(t)

	res := c.Submit(":grpc " + addr)
	if res.Err {
		t.Fatalf(":grpc %s failed: %s", addr, res.Out)
	}
	for _, want := range []string{
		"gluontest.Probe",
		"Echo", "Fail", "Watch", "Upload", "Chat",
		"unary", "server stream", "client stream", "bidi stream",
	} {
		if !strings.Contains(res.Out, want) {
			t.Errorf("the listing does not mention %q:\n%s", want, res.Out)
		}
	}
	// The three streams are marked, and the note says why they cannot be
	// called rather than leaving the mark to be guessed at.
	if !strings.Contains(res.Out, "· ") || !strings.Contains(res.Out, "outlives one evaluation") {
		t.Errorf("the listing does not say why some methods cannot be called:\n%s", res.Out)
	}
}

// TestGRPCReportsReflectionSeparatelyFromEmptiness. "The server exposes
// nothing" and "the server would not say" are different answers, and reporting
// the second as the first sends somebody looking for a bug in a server that is
// working. It is the distinction internal/db/detect.go draws between found
// nothing and did not look.
func TestGRPCReportsReflectionSeparatelyFromEmptiness(t *testing.T) {
	addr := startProbe(t, "-reflect=false")
	c := withGRPC(t)

	res := c.Submit(":grpc " + addr)
	if !res.Err {
		t.Fatalf(":grpc against a server with no reflection succeeded: %s", res.Out)
	}
	if !strings.Contains(res.Out, "no server reflection service") {
		t.Errorf("the failure does not name reflection: %q", res.Out)
	}
	if strings.Contains(res.Out, "no services") || strings.Contains(res.Out, "exposes nothing") {
		t.Errorf("reflection being unavailable was reported as emptiness: %q", res.Out)
	}
}

// TestGRPCReportsAnUnreachableTarget with the cause, rather than as an empty
// answer.
func TestGRPCReportsAnUnreachableTarget(t *testing.T) {
	c := withGRPC(t)

	// Port 1 on loopback: nothing listens there, and the refusal is immediate
	// rather than a timeout, which keeps the test off the wait path.
	res := c.Submit(":grpc 127.0.0.1:1 -t 5s")
	if !res.Err {
		t.Fatalf(":grpc against a closed port succeeded: %s", res.Out)
	}
	if !strings.Contains(res.Out, "could not be reached") {
		t.Errorf("the failure does not say the target was unreachable: %q", res.Out)
	}
	if !strings.Contains(strings.ToLower(res.Out), "connection refused") {
		t.Errorf("the failure does not carry its cause: %q", res.Out)
	}
}

// TestGRPCCallsAUnaryMethod, and the response is a value — so the renderers
// that apply to a value anywhere apply to it. took is a
// google.protobuf.Duration, which reaches the terminal as a time.Duration and
// is drawn by the renderer that draws every other duration.
func TestGRPCCallsAUnaryMethod(t *testing.T) {
	addr := startProbe(t)
	c := withGRPC(t)

	res := c.Submit(`:grpc ` + addr + ` gluontest.Probe/Echo -d '{"text":"hello"}'`)
	if res.Err {
		t.Fatalf("the call failed: %s", res.Out)
	}
	if !strings.Contains(res.Out, "hello") {
		t.Errorf("the response does not carry the request's text: %q", res.Out)
	}
	// 90 minutes, which the duration renderer states as a total beside the
	// carry chain. Either spelling proves the field arrived as a duration
	// rather than as protojson's "5400s" string.
	if !strings.Contains(res.Out, "1h30m0s") {
		t.Errorf("the duration field did not render through the duration renderer: %q", res.Out)
	}
}

// TestGRPCRefusesAStreamingMethod, with the reason. Reading one message and
// hanging up would return a number that looks like an answer and is not.
func TestGRPCRefusesAStreamingMethod(t *testing.T) {
	addr := startProbe(t)
	c := withGRPC(t)

	for _, m := range []string{"Watch", "Upload", "Chat"} {
		res := c.Submit(":grpc " + addr + " gluontest.Probe/" + m)
		if !res.Err {
			t.Errorf("%s was called: %s", m, res.Out)
			continue
		}
		if !strings.Contains(res.Out, "stream") ||
			!strings.Contains(res.Out, "outlives the evaluation") {
			t.Errorf("%s was refused without the reason: %q", m, res.Out)
		}
	}
}

// TestGRPCReportsAnErrorStatus with its code, the code's meaning and the
// message. The number alone is a lookup somebody has to do; the name alone
// loses what a client library would report.
func TestGRPCReportsAnErrorStatus(t *testing.T) {
	addr := startProbe(t)
	c := withGRPC(t)

	res := c.Submit(":grpc " + addr + " gluontest.Probe/Fail")
	if !res.Err {
		t.Fatalf("a failing method reported success: %s", res.Out)
	}
	for _, want := range []string{"9", "FailedPrecondition", "the ledger is not open"} {
		if !strings.Contains(res.Out, want) {
			t.Errorf("the status report is missing %q: %q", want, res.Out)
		}
	}
}

// TestGRPCReportsAnUnknownMethod and lists what the service does expose. The
// descriptor had to be fetched to find out the method was missing, so the
// listing is already in hand and making the reader run it again is a step for
// nothing.
func TestGRPCReportsAnUnknownMethod(t *testing.T) {
	addr := startProbe(t)
	c := withGRPC(t)

	res := c.Submit(":grpc " + addr + " gluontest.Probe/Nope")
	if !res.Err {
		t.Fatalf("an unknown method reported success: %s", res.Out)
	}
	if !strings.Contains(res.Out, "exposes no Nope") {
		t.Errorf("the failure does not name the method: %q", res.Out)
	}
	for _, want := range []string{"Echo", "Fail", "Watch"} {
		if !strings.Contains(res.Out, want) {
			t.Errorf("the failure does not list %q as available: %q", want, res.Out)
		}
	}

	// A service that does not exist is a different answer from a method that
	// does not, and must not be reported as the other.
	res = c.Submit(":grpc " + addr + " gluontest.Nope/Nope")
	if !res.Err || !strings.Contains(res.Out, "no service named gluontest.Nope") {
		t.Errorf("an unknown service was not reported as one: %q (err=%v)", res.Out, res.Err)
	}
}

// TestGRPCChecksTheRequestBeforeCalling. A server's own complaint names its
// field paths and costs a round trip, which is a slow way to find out about a
// request you got wrong on your own machine — so the check is local and the
// message names the user's field.
//
// That no call was made is asserted through the server's own call counter: Echo
// reports how many calls this process has served, so a malformed request
// between two good ones must leave the count consecutive.
func TestGRPCChecksTheRequestBeforeCalling(t *testing.T) {
	addr := startProbe(t)
	c := withGRPC(t)

	first := echoCount(t, c, addr)

	res := c.Submit(`:grpc ` + addr + ` gluontest.Probe/Echo -d '{"nope":1}'`)
	if !res.Err {
		t.Fatalf("a malformed request was sent: %s", res.Out)
	}
	if !strings.Contains(res.Out, `"nope"`) {
		t.Errorf("the failure does not name the offending field: %q", res.Out)
	}

	if second := echoCount(t, c, addr); second != first+1 {
		t.Errorf("the count went %d → %d; the malformed request reached the server",
			first, second)
	}
}

// TestGRPCIsNeverServedFromTheCache. The result cache is keyed on program text
// alone, so the same call twice is byte-identical source — and without EvalLive
// the second would answer with what the first saw. Echo's counter is what makes
// that observable.
func TestGRPCIsNeverServedFromTheCache(t *testing.T) {
	addr := startProbe(t)
	c := withGRPC(t)

	first, second := echoCount(t, c, addr), echoCount(t, c, addr)
	if first == second {
		t.Errorf("two identical calls both answered %d — the second came from the cache",
			first)
	}
}

// TestGRPCCallsShareNoConnection. gRPC's model assumes a channel that outlives
// many calls; gluon's child outlives one. The command's Detail says so, and
// this is the assertion behind it: the server reports the connection each call
// arrived on, and two calls never share one.
func TestGRPCCallsShareNoConnection(t *testing.T) {
	addr := startProbe(t)
	c := withGRPC(t)

	first, second := echoConn(t, c, addr), echoConn(t, c, addr)
	if first == "" || second == "" {
		t.Fatalf("the server did not report a connection: %q, %q", first, second)
	}
	if first == second {
		t.Errorf("both calls arrived on connection %s; state carried between them", first)
	}
}

// TestGRPCResolvesACredentialInTheChild. The value reaches the child's
// environment and nothing else: not the generated program, not what :src shows,
// not what :save would write. What does reach the child's output is masked on
// the way back, because a server that echoes a credential is a real thing and
// the echo is not gluon's to leak.
func TestGRPCResolvesACredentialInTheChild(t *testing.T) {
	addr := startProbe(t)
	c := withGRPC(t)

	const secret = "sq7Kd0aMzX9vLpQr2TfY"
	t.Setenv("GLUON_TEST_TOKEN", secret)

	res := c.Submit(`:grpc ` + addr + ` gluontest.Probe/Echo ` +
		`-d '{"text":"x","metaKey":"authorization"}' ` +
		`-H 'authorization: Bearer $GLUON_TEST_TOKEN'`)
	if res.Err {
		t.Fatalf("the call failed: %s", res.Out)
	}
	// The server read the metadata back, so the credential really was sent —
	// and what came back is masked rather than printed.
	if strings.Contains(res.Out, secret) {
		t.Errorf("the credential was echoed unmasked: %q", res.Out)
	}
	if !strings.Contains(res.Out, "***") {
		t.Errorf("the echoed credential was not masked: %q", res.Out)
	}

	// Asserted against the file on disk rather than against the plan's return
	// value, because the question is whether the mechanism survived the whole
	// path: <tmp>/main.go is what the build reads, what :src renders and what
	// the result cache would have keyed on.
	src, err := os.ReadFile(filepath.Join(c.ev.Dir(), "main.go"))
	if err != nil {
		t.Fatalf("reading the generated program: %v", err)
	}
	if strings.Contains(string(src), secret) {
		t.Errorf("the credential is in the generated source:\n%s", src)
	}
	if !strings.Contains(string(src), `os.Getenv("GLUON_TEST_TOKEN")`) {
		t.Errorf("the generated program does not name the variable:\n%s", src)
	}
	// And the session's own program, which is what :src and :save render.
	if out := c.Submit(":src"); strings.Contains(out.Out, secret) {
		t.Errorf(":src carries the credential:\n%s", out.Out)
	}
}

// TestGRPCLeavesTheSessionUnchanged is invariant 14 for a command that dials.
// EvalLive snapshots imports, resolved and healthy exactly as EvalTransient
// does and pops the entry; this is the assertion that it really does.
func TestGRPCLeavesTheSessionUnchanged(t *testing.T) {
	addr := startProbe(t)
	c := withGRPC(t)

	if res := c.Submit(`x := 1`); res.Err {
		t.Fatalf("setup failed: %s", res.Out)
	}
	entries, imports := len(c.sess.Entries), len(c.ev.Imports())

	for _, line := range []string{
		":grpc " + addr,
		":grpc " + addr + ` gluontest.Probe/Echo -d '{"text":"y"}'`,
		":grpc " + addr + " gluontest.Probe/Fail",
	} {
		if res := c.Submit(line); res.Out == "" {
			t.Fatalf("%s produced nothing", line)
		}
		if got := len(c.sess.Entries); got != entries {
			t.Fatalf("%s left the session with %d entries, want %d", line, got, entries)
		}
		if got := len(c.ev.Imports()); got != imports {
			t.Fatalf("%s left the session with %d imports, want %d", line, got, imports)
		}
	}
	// The session still works, and x still means what it did.
	if res := c.Submit(`x`); res.Err || !strings.Contains(res.Out, "1") {
		t.Errorf("session broken after :grpc: %q", res.Out)
	}
}

// echoCount calls Echo and returns the counter the server reported.
func echoCount(t *testing.T, c *Core, addr string) int {
	t.Helper()
	return field(t, c, addr, "count")
}

// echoConn calls Echo and returns the connection the server saw it arrive on.
func echoConn(t *testing.T, c *Core, addr string) string {
	t.Helper()
	res := c.Submit(`:grpc ` + addr + ` gluontest.Probe/Echo -d '{"text":"c"}'`)
	if res.Err {
		t.Fatalf("the call failed: %s", res.Out)
	}
	return value(t, res.Out, "conn")
}

// field reads one integer field out of a rendered response.
func field(t *testing.T, c *Core, addr, name string) int {
	t.Helper()
	res := c.Submit(`:grpc ` + addr + ` gluontest.Probe/Echo -d '{"text":"n"}'`)
	if res.Err {
		t.Fatalf("the call failed: %s", res.Out)
	}
	raw := value(t, res.Out, name)
	n, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("%s is %q, which is not a number, in:\n%s", name, raw, res.Out)
	}
	return n
}

// value pulls one map entry out of rendered output.
//
// The rendering is gluon's ordinary map rendering — that is the point of the
// response being a value — so the test reads what that produced rather than
// re-deriving the shape: `map["conn":"127.0.0.1:1" "count":1 ...]`, one line.
func value(t *testing.T, out, name string) string {
	t.Helper()
	key := `"` + name + `":`
	i := strings.Index(out, key)
	if i < 0 {
		t.Fatalf("no %s in:\n%s", name, out)
	}
	rest := out[i+len(key):]
	end := strings.IndexAny(rest, " ]")
	if end < 0 {
		end = len(rest)
	}
	return strings.Trim(rest[:end], `"`)
}
