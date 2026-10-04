package rpc

import (
	"go/parser"
	"strings"
	"testing"
)

// planOf is the parsed line, for tests that care about what was read rather
// than what was generated.
func planOf(t *testing.T, arg string) call {
	t.Helper()
	c, err := parseCall(arg)
	if err != nil {
		t.Fatalf("parseCall(%q): %v", arg, err)
	}
	return c
}

// TestBothFormsProduceParseableGo. The registry runs this over every plugin
// with a Rewrite; a live command declares a plan instead, so its source needs
// the same guard or the first person to run it gets a build error naming gluon.
func TestBothFormsProduceParseableGo(t *testing.T) {
	for _, arg := range []string{
		"localhost:50051",
		"localhost:50051 -t 2s",
		"localhost:50051 pkg.Svc/Get",
		`localhost:50051 pkg.Svc/Get -d '{"id":1}' -H 'authorization: Bearer $TOKEN' -t 5s`,
		"api.example.com:443 pkg.Svc.Get -tls",
		"unix:///tmp/s.sock pkg.Svc/Get",
	} {
		c, err := plan(arg)
		if err != nil {
			t.Errorf(":grpc %s was rejected: %v", arg, err)
			continue
		}
		if _, err := parser.ParseExpr(c.Source); err != nil {
			t.Errorf(":grpc %s produced source that does not parse: %v\n%s", arg, err, c.Source)
		}
	}
}

// TestBareInvocationIsAUsageLine. Arg contains "<", which is the registry's
// contract that a bare invocation says how to use the command rather than
// dialling nothing.
func TestBareInvocationIsAUsageLine(t *testing.T) {
	for _, arg := range []string{"", "   ", "\t"} {
		_, err := plan(arg)
		if err == nil {
			t.Fatalf(":grpc %q was accepted", arg)
		}
		if !strings.HasPrefix(err.Error(), "usage:") {
			t.Errorf(":grpc %q said %q, which does not begin with usage:", arg, err)
		}
	}
}

// TestATargetWithNoPortIsRefused rather than completed. 443 and 50051 are both
// ordinary, so picking one would make the difference invisible in exactly the
// session where somebody is debugging it — the reason :http refuses a
// scheme-less URL.
func TestATargetWithNoPortIsRefused(t *testing.T) {
	_, err := parseCall("api.example.com")
	if err == nil {
		t.Fatal("a target with no port was accepted")
	}
	if !strings.Contains(err.Error(), "no port") || !strings.Contains(err.Error(), "50051") {
		t.Errorf("the refusal does not say what is missing: %v", err)
	}

	// A URL is the other shape people write, and the message turns it into the
	// target they meant rather than only saying no.
	_, err = parseCall("https://api.example.com:443/svc")
	if err == nil || !strings.Contains(err.Error(), "api.example.com:443") {
		t.Errorf("a URL was not turned into a target: %v", err)
	}

	// A resolver scheme is grpc-go's own grammar and is passed through.
	for _, target := range []string{"dns:///api.example.com:443", "unix:///tmp/s.sock"} {
		if _, err := parseCall(target); err != nil {
			t.Errorf("%s was refused: %v", target, err)
		}
	}
}

// TestAMethodIsSplitIntoItsHalves, in both the spellings people write: the
// slashed form the listing prints, and the dotted form a .proto reads as.
func TestAMethodIsSplitIntoItsHalves(t *testing.T) {
	for _, tc := range []struct{ arg, svc, method string }{
		{"h:1 pkg.Svc/Get", "pkg.Svc", "Get"},
		{"h:1 pkg.Svc.Get", "pkg.Svc", "Get"},
		{"h:1 a.b.c.Svc/Do", "a.b.c.Svc", "Do"},
	} {
		c := planOf(t, tc.arg)
		if c.Service != tc.svc || c.MethodName != tc.method {
			t.Errorf("%s split to %q/%q, want %q/%q",
				tc.arg, c.Service, c.MethodName, tc.svc, tc.method)
		}
	}
	for _, arg := range []string{"h:1 Get", "h:1 pkg.Svc/", "h:1 /Get"} {
		if _, err := parseCall(arg); err == nil {
			t.Errorf("%s was accepted as a method", arg)
		}
	}
}

// TestALiteralCredentialIsRefused, and refused by shape rather than by the name
// of the key holding it — invariant 23.
//
// Refused rather than masked: by the time the command runs, the line as typed
// is already in ~/.local/state/gluon/history, and hiding the display would
// conceal the leak rather than prevent it. It is the ground -dsn and
// `:db connect <url>` are both permanently rejected on.
func TestALiteralCredentialIsRefused(t *testing.T) {
	for _, value := range []string{
		"authorization: Bearer sq7Kd0aMzX9vLpQr2TfY",
		"x-whatever: ghp_16C7e42F292c6912E7710c838347Ae178B4a",
		"x-key: AKIAIOSFODNN7EXAMPLE",
	} {
		_, err := parseCall("localhost:1 pkg.Svc/Get -H '" + value + "'")
		if err == nil {
			t.Errorf("-H %q was accepted", value)
			continue
		}
		if !strings.Contains(err.Error(), "history") || !strings.Contains(err.Error(), "$TOKEN") {
			t.Errorf("-H %q was refused without saying what to do instead: %v", value, err)
		}
	}

	// The same header naming a variable passes, and the shape test is what
	// makes the difference — not the key's name, which is identical in both.
	c := planOf(t, "localhost:1 pkg.Svc/Get -H 'authorization: Bearer $TOKEN'")
	if len(c.Refs) != 1 || c.Refs[0] != "TOKEN" {
		t.Errorf("the reference was not read: %v", c.Refs)
	}

	// And a value that merely *looks* like an auth scheme is not a credential.
	// A redactor that hides the harmless is one people route around.
	for _, value := range []string{
		"authorization: Negotiate",
		"x-note: Bearer of bad news",
		"content-type: application/grpc",
	} {
		if _, err := parseCall("localhost:1 pkg.Svc/Get -H '" + value + "'"); err != nil {
			t.Errorf("-H %q was refused: %v", value, err)
		}
	}
}

// TestAResolvedCredentialIsNeverWrittenIntoTheSource. os.Getenv by name is the
// whole mechanism: neither <tmp>/main.go, :src, :save nor a build error — which
// quotes source — can carry a value the source never held.
func TestAResolvedCredentialIsNeverWrittenIntoTheSource(t *testing.T) {
	t.Setenv("GLUON_TEST_TOKEN", "sq7Kd0aMzX9vLpQr2TfY")

	c, err := plan("localhost:1 pkg.Svc/Get -H 'authorization: Bearer $GLUON_TEST_TOKEN'")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(c.Source, "sq7Kd0aMzX9vLpQr2TfY") {
		t.Errorf("the value reached the generated source:\n%s", c.Source)
	}
	if !strings.Contains(c.Source, `os.Getenv("GLUON_TEST_TOKEN")`) {
		t.Errorf("the source does not read the variable by name:\n%s", c.Source)
	}
	// And the plan names it, which is what gluon resolves and then masks.
	if len(c.Refs) != 1 || c.Refs[0] != "GLUON_TEST_TOKEN" {
		t.Errorf("the plan does not declare the variable it reads: %v", c.Refs)
	}
}

// TestACredentialIsNotSentInTheClearToAnotherMachine.
//
// Plaintext is the default because a REPL is usually pointed at a local
// service. What must not follow from that default is a token going out
// unencrypted to a host that is not this one, so that one combination is
// refused and names the flag that fixes it.
func TestACredentialIsNotSentInTheClearToAnotherMachine(t *testing.T) {
	_, err := parseCall("api.example.com:443 pkg.Svc/Get -H 'authorization: Bearer $T'")
	if err == nil {
		t.Fatal("metadata was allowed over plaintext to a remote host")
	}
	if !strings.Contains(err.Error(), "-tls") {
		t.Errorf("the refusal does not name the flag that fixes it: %v", err)
	}

	// -tls is the way through, and loopback needs no flag at all: there is no
	// wire to listen on.
	for _, arg := range []string{
		"api.example.com:443 pkg.Svc/Get -H 'authorization: Bearer $T' -tls",
		"localhost:50051 pkg.Svc/Get -H 'authorization: Bearer $T'",
		"127.0.0.1:50051 pkg.Svc/Get -H 'authorization: Bearer $T'",
		"[::1]:50051 pkg.Svc/Get -H 'authorization: Bearer $T'",
		"unix:///tmp/s.sock pkg.Svc/Get -H 'authorization: Bearer $T'",
	} {
		if _, err := parseCall(arg); err != nil {
			t.Errorf("%s was refused: %v", arg, err)
		}
	}
}

// TestCallFlagsNeedAMethod. -H and -d belong to a call, and accepting them on
// the listing form would quietly ignore what somebody typed.
func TestCallFlagsNeedAMethod(t *testing.T) {
	for _, arg := range []string{
		"localhost:50051 -H 'x: y'",
		`localhost:50051 -d '{"id":1}'`,
	} {
		_, err := parseCall(arg)
		if err == nil {
			t.Errorf("%s was accepted with no method", arg)
			continue
		}
		if !strings.Contains(err.Error(), "no method was named") {
			t.Errorf("%s was refused without saying why: %v", arg, err)
		}
	}
}

// TestUnreachableIsReportedWithItsCause. The listing's error path is the one
// that cannot be reached without a server, so it is asserted here over the
// marker the child would have printed.
func TestUnreachableIsReportedWithItsCause(t *testing.T) {
	ans := reportList("localhost:50051")(
		tagErr+sep+"connection error: desc = \"transport: dial tcp: connection refused\"", nil)
	if !ans.Failed {
		t.Error("an unreachable target was not reported as a failure")
	}
	if !strings.Contains(ans.Text, "localhost:50051") {
		t.Errorf("the report does not name the target: %q", ans.Text)
	}
	if !strings.Contains(ans.Text, "connection refused") {
		t.Errorf("the report does not carry the cause: %q", ans.Text)
	}
}

// TestReflectionUnavailableIsNotEmptiness. "The server exposes nothing" and
// "the server would not say" are different answers — internal/db/detect.go's
// distinction between found nothing and did not look.
func TestReflectionUnavailableIsNotEmptiness(t *testing.T) {
	unavailable := reportList("h:1")(tagNoReflect+sep+"unknown service", nil)
	if !unavailable.Failed || !strings.Contains(unavailable.Text, "no server reflection service") {
		t.Errorf("reflection being unavailable was not said: %q", unavailable.Text)
	}

	empty := reportList("h:1")("", nil)
	if empty.Failed {
		t.Error("a server that reflected and exposes nothing was reported as a failure")
	}
	if !strings.Contains(empty.Text, "and no services") {
		t.Errorf("an empty server was not reported as empty: %q", empty.Text)
	}
	// It says reflection *worked* — the opposite sentence, and the one that
	// keeps the two answers apart at a glance.
	if strings.Contains(empty.Text, "no server reflection service") {
		t.Errorf("emptiness was explained as a reflection problem: %q", empty.Text)
	}
}

// TestEveryMethodIsMarkedByWhetherOneEvaluationCanServeIt. The mark is what
// makes the streaming refusal something the reader knew before they typed the
// call rather than a surprise at it.
func TestEveryMethodIsMarkedByWhetherOneEvaluationCanServeIt(t *testing.T) {
	out := strings.Join([]string{
		row("pkg.Svc", "Get", kindUnary),
		row("pkg.Svc", "Watch", kindServer),
		row("pkg.Svc", "Upload", kindClient),
		row("pkg.Svc", "Chat", kindBidi),
	}, "\n")

	ans := reportList("h:1")(out, nil)
	if ans.Failed {
		t.Fatalf("a listing was reported as a failure: %q", ans.Text)
	}
	for _, want := range []string{
		"unary", "server stream", "client stream", "bidi stream",
		"3 of these need a connection that outlives one evaluation",
	} {
		if !strings.Contains(ans.Text, want) {
			t.Errorf("the listing is missing %q:\n%s", want, ans.Text)
		}
	}
	for _, line := range strings.Split(ans.Text, "\n") {
		marked := strings.HasPrefix(strings.TrimLeft(line, " "), "· ")
		if strings.Contains(line, "Get") && marked {
			t.Errorf("a unary method was marked uncallable: %q", line)
		}
		if strings.Contains(line, "Watch") && !marked {
			t.Errorf("a streaming method was not marked: %q", line)
		}
	}
}

// TestAnUnknownMethodListsWhatIsThere. The descriptor had to be fetched to find
// out the method was missing, so the listing is in hand and making the reader
// run it again is a step for nothing.
func TestAnUnknownMethodListsWhatIsThere(t *testing.T) {
	out := strings.Join([]string{
		tagUnknown + sep + "method" + sep + "pkg.Svc",
		row("pkg.Svc", "Get", kindUnary),
		row("pkg.Svc", "Watch", kindServer),
	}, "\n")

	ans := reportCall("h:1", "pkg.Svc/Nope")(out, nil)
	if !ans.Failed {
		t.Fatal("an unknown method was not reported as a failure")
	}
	for _, want := range []string{"exposes no Nope", "Get", "Watch"} {
		if !strings.Contains(ans.Text, want) {
			t.Errorf("the report is missing %q:\n%s", want, ans.Text)
		}
	}

	// A service that does not exist is a different answer and must not borrow
	// the other's wording.
	ans = reportCall("h:1", "pkg.Nope/Get")(tagUnknown+sep+"service"+sep+"pkg.Nope", nil)
	if !ans.Failed || !strings.Contains(ans.Text, "no service named pkg.Nope") {
		t.Errorf("an unknown service was not reported as one: %q", ans.Text)
	}
}

// TestAStatusCarriesItsCodeItsNameAndItsMessage. The number alone is a lookup
// somebody has to do; the name alone loses what a client library would report.
func TestAStatusCarriesItsCodeItsNameAndItsMessage(t *testing.T) {
	ans := reportCall("h:1", "pkg.Svc/Get")(
		tagStatus+sep+"9"+sep+"FailedPrecondition"+sep+"the ledger is not open", nil)
	if !ans.Failed {
		t.Fatal("an error status was not reported as a failure")
	}
	for _, want := range []string{"9", "FailedPrecondition", "the ledger is not open"} {
		if !strings.Contains(ans.Text, want) {
			t.Errorf("the status report is missing %q: %q", want, ans.Text)
		}
	}
}

// TestASuccessfulCallHandsTheValueBack. An empty Answer is how a Report says
// the value is the answer, which is what puts a gRPC response through the same
// renderers as every other value.
func TestASuccessfulCallHandsTheValueBack(t *testing.T) {
	ans := reportCall("h:1", "pkg.Svc/Get")("", nil)
	if ans.Failed || ans.Text != "" {
		t.Errorf("a successful call did not hand the value back: %+v", ans)
	}
}

// TestTheDetailSaysEveryCallDialsAndHangsUp. gRPC's model assumes a channel
// that outlives many calls and this one outlives none, so somebody timing this
// is not timing gRPC — and silence would produce a wrong conclusion about gRPC
// rather than about gluon.
func TestTheDetailSaysEveryCallDialsAndHangsUp(t *testing.T) {
	detail := GRPC{}.Commands()[0].Detail
	for _, want := range []string{
		"dials and hangs up",
		"is not a measurement of gRPC",
		"never cached",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("the detail does not say %q:\n%s", want, detail)
		}
	}
}

// TestTheCommandFitsTheHelpGutter, which internal/repl asserts over every
// command it can see — but only for the plugins that are active, and this one
// is active only with gRPC in the build list.
func TestTheCommandFitsTheHelpGutter(t *testing.T) {
	const helpWidth = 20 // internal/repl/command.go
	c := GRPC{}.Commands()[0]
	if w := len(c.Name) + 1 + len(c.Arg); w > helpWidth {
		t.Errorf("%s %s is %d wide, past the %d-column gutter", c.Name, c.Arg, w, helpWidth)
	}
}

// row is one method line as the child would have printed it.
func row(service, name, kind string) string {
	return tagMethod + sep + strings.Join([]string{
		service, name, kind, "pkg.In", "pkg.Out",
	}, sep)
}
