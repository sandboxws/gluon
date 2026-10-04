package repl

import (
	"go/parser"
	"strconv"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/argline"

	"github.com/sandboxws/gluon/internal/pretty"
)

// TestHTTPArgsAreMethodThenURL. The two operands lead so the common case needs
// no flag, and everything after them is a flag — a positional third argument is
// a typo, not a second URL.
func TestHTTPArgsAreMethodThenURL(t *testing.T) {
	req, err := parseHTTPArgs(`post https://api.example.com/users -H "Content-Type: application/json" -d '{"a":1}' -t 5s`)
	if err != nil {
		t.Fatalf("parseHTTPArgs: %v", err)
	}
	if req.Method != "POST" {
		t.Errorf("method = %q, want POST — the method is matched case-insensitively", req.Method)
	}
	if req.URL != "https://api.example.com/users" {
		t.Errorf("url = %q", req.URL)
	}
	if len(req.Headers) != 1 || req.Headers[0].Name != "Content-Type" ||
		req.Headers[0].Value != "application/json" {
		t.Errorf("headers = %+v", req.Headers)
	}
	if req.Body != `{"a":1}` {
		t.Errorf("body = %q", req.Body)
	}
	if req.Timeout != "5s" {
		t.Errorf("timeout = %q", req.Timeout)
	}
}

// TestHTTPFieldsGroupOnQuotes. A header value is one argument written as four
// words, and no shell has been near the line to say so.
func TestHTTPFieldsGroupOnQuotes(t *testing.T) {
	got, err := argline.Fields(`GET https://x -H 'Authorization: Bearer $T' -d ""`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"GET", "https://x", "-H", "Authorization: Bearer $T", "-d", ""}
	if len(got) != len(want) {
		t.Fatalf("argline.Fields = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("field %d = %q, want %q", i, got[i], want[i])
		}
	}
	if _, err := argline.Fields(`GET https://x -H "unclosed`); err == nil {
		t.Error("an unclosed quote parsed silently")
	}
}

// TestUnrecognisedMethodListsTheOnesItTakes. The alternative is sending a verb
// nobody meant and learning about the typo from a server's error.
func TestUnrecognisedMethodListsTheOnesItTakes(t *testing.T) {
	_, err := parseHTTPArgs("FETCH https://example.com")
	if err == nil {
		t.Fatal("FETCH was accepted as a method")
	}
	for _, m := range []string{"GET", "POST", "DELETE"} {
		if !strings.Contains(err.Error(), m) {
			t.Errorf("the refusal does not list %s: %s", m, err)
		}
	}
}

// TestSchemelessURLIsRefusedRatherThanCompleted. http and https are different
// requests, so picking one would hide the difference in the session where
// somebody is debugging it.
func TestSchemelessURLIsRefusedRatherThanCompleted(t *testing.T) {
	_, err := parseHTTPArgs("GET example.com/users")
	if err == nil {
		t.Fatal("a scheme-less URL was accepted")
	}
	if !strings.Contains(err.Error(), "scheme") {
		t.Errorf("the refusal does not say what is missing: %s", err)
	}
	if _, err := parseHTTPArgs("GET ftp://example.com/x"); err == nil {
		t.Error("an ftp URL was accepted")
	}
}

// TestBareHTTPReportsUsage. Arg names a required operand, so the registry's own
// test demands this too; it is here because the message is this command's.
func TestBareHTTPReportsUsage(t *testing.T) {
	for _, arg := range []string{"", "   ", "GET"} {
		res := (&Core{}).metaHTTP(arg)
		if !res.Err || !strings.Contains(res.Out, "usage:") {
			t.Errorf("metaHTTP(%q) = %q (err=%v), want a usage line", arg, res.Out, res.Err)
		}
	}
}

// TestHeaderValueNamesAVariable covers the three shapes a value can have: a
// reference, a literal, and the escape that means a real dollar sign.
func TestHeaderValueNamesAVariable(t *testing.T) {
	for _, tc := range []struct {
		in    string
		parts []httpPart
	}{
		{"Bearer $TOKEN", []httpPart{{Text: "Bearer "}, {Ref: "TOKEN"}}},
		{"$TOKEN", []httpPart{{Ref: "TOKEN"}}},
		{"$A/$B", []httpPart{{Ref: "A"}, {Text: "/"}, {Ref: "B"}}},
		{"application/json", []httpPart{{Text: "application/json"}}},
		// An escaped dollar is literal, and so is one that names nothing.
		{`\$TOKEN`, []httpPart{{Text: "$TOKEN"}}},
		{"cost: $ per unit", []httpPart{{Text: "cost: $ per unit"}}},
		// A digit cannot start an identifier, so $1 is literal too.
		{"$1", []httpPart{{Text: "$1"}}},
	} {
		got := argline.SplitRefs(tc.in)
		if len(got) != len(tc.parts) {
			t.Errorf("argline.SplitRefs(%q) = %+v, want %+v", tc.in, got, tc.parts)
			continue
		}
		for i := range got {
			if got[i] != tc.parts[i] {
				t.Errorf("argline.SplitRefs(%q)[%d] = %+v, want %+v", tc.in, i, got[i], tc.parts[i])
			}
		}
	}
}

// TestALiteralCredentialIsRefused, and refused by shape rather than by the
// header's name — invariant 23. A reference is what passes, which is the whole
// point of the indirection.
func TestALiteralCredentialIsRefused(t *testing.T) {
	refused := []string{
		"Authorization: Bearer sq7Kd0aMzX9vLpQr2TfY",
		"X-Whatever: ghp_16C7e42F292c6912E7710c838347Ae178B4a",
		"Authorization: basic YWRhOmh1bnRlcjI=",
	}
	for _, h := range refused {
		if _, err := parseHTTPHeader(h); err == nil {
			t.Errorf("-H %q was accepted, so the token is now in history", h)
		} else if !strings.Contains(err.Error(), "$TOKEN") {
			t.Errorf("-H %q refused without saying how to pass it: %s", h, err)
		}
	}
	accepted := []string{
		"Authorization: Bearer $TOKEN",
		"Authorization: $AUTH",
		"Authorization: Negotiate",
		"Content-Type: application/json",
		"Accept: text/html, application/xhtml+xml",
		// A header whose *name* sounds like a secret but whose value is not.
		"X-Api-Key: ask the team",
	}
	for _, h := range accepted {
		if _, err := parseHTTPHeader(h); err != nil {
			t.Errorf("-H %q was refused: %s", h, err)
		}
	}
}

// TestHeaderNeedsAColon — `Name: value` is the wire format, and a value with no
// name is not a header.
func TestHeaderNeedsAColon(t *testing.T) {
	if _, err := parseHTTPHeader("Authorization Bearer $T"); err == nil {
		t.Error("a header with no colon was accepted")
	}
	if _, err := parseHTTPHeader(": value"); err == nil {
		t.Error("a header with no name was accepted")
	}
}

// TestAnUnsetVariableStopsTheRequest.
//
// The alternative is an empty header, which an endpoint answers with a 401 —
// indistinguishable from a wrong credential, and the thing that sends somebody
// to rotate a token that was fine.
func TestAnUnsetVariableStopsTheRequest(t *testing.T) {
	req, err := parseHTTPArgs(`GET https://example.com -H "Authorization: Bearer $GLUON_TEST_ABSENT"`)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolveHTTPRefs(&req); err == nil {
		t.Fatal("an unset variable was sent as an empty header")
	} else if !strings.Contains(err.Error(), "GLUON_TEST_ABSENT") {
		t.Errorf("the refusal does not name the variable: %s", err)
	}

	// Set but empty is the same failure with a different cause, and says so.
	t.Setenv("GLUON_TEST_EMPTY", "")
	req, err = parseHTTPArgs(`GET https://example.com -H "Authorization: Bearer $GLUON_TEST_EMPTY"`)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolveHTTPRefs(&req); err == nil {
		t.Error("a variable set to nothing was sent as an empty header")
	}
}

// TestResolvedRefsCarryTheValueAndTheRedaction. One walk fills both, because a
// second lookup is how a value ends up masked in one place and not the other.
func TestResolvedRefsCarryTheValueAndTheRedaction(t *testing.T) {
	t.Setenv("GLUON_TEST_TOKEN", "sq7Kd0aMzX9vLpQr2TfY")
	req, err := parseHTTPArgs(`GET https://example.com -H "Authorization: Bearer $GLUON_TEST_TOKEN" -H "X-Trace: $GLUON_TEST_TOKEN"`)
	if err != nil {
		t.Fatal(err)
	}
	env, secrets, err := resolveHTTPRefs(&req)
	if err != nil {
		t.Fatal(err)
	}
	// Named twice, resolved once: a duplicate in the environment is harmless
	// and a duplicate in the redaction list is a second pass over the output.
	if len(env) != 1 || env[0] != "GLUON_TEST_TOKEN=sq7Kd0aMzX9vLpQr2TfY" {
		t.Errorf("env = %q", env)
	}
	if len(secrets) != 1 || secrets[0] != "sq7Kd0aMzX9vLpQr2TfY" {
		t.Errorf("secrets = %q", secrets)
	}
	if len(req.Refs) != 1 || req.Refs[0] != "GLUON_TEST_TOKEN" {
		t.Errorf("refs = %q", req.Refs)
	}
}

// TestUnknownFlagIsNamed. :http takes three, and a fourth is a typo worth
// saying out loud rather than a value silently dropped.
func TestUnknownFlagIsNamed(t *testing.T) {
	if _, err := parseHTTPArgs("GET https://example.com -X POST"); err == nil {
		t.Error("-X was accepted")
	}
	if _, err := parseHTTPArgs("GET https://example.com -H"); err == nil {
		t.Error("-H with no value was accepted")
	}
	if _, err := parseHTTPArgs("GET https://example.com -t nonsense"); err == nil {
		t.Error("-t took a value that is not a duration")
	}
}

// TestGeneratedSourceIsAnOrdinaryGoExpression.
//
// Constraint C's claim is that the command can do nothing a typed line could
// not, and this is what makes it checkable: the rewrite has to parse as one Go
// expression, and everything it reaches has to be in the standard library —
// gluon links none of the libraries its plugins describe, so a generated
// program that needed one would not build.
func TestGeneratedSourceIsAnOrdinaryGoExpression(t *testing.T) {
	t.Setenv("GLUON_TEST_TOKEN", "sq7Kd0aMzX9vLpQr2TfY")
	req, err := parseHTTPArgs(`POST https://api.example.com/u -H "Authorization: Bearer $GLUON_TEST_TOKEN" -d '{"a":1}' -t 5s`)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolveHTTPRefs(&req); err != nil {
		t.Fatal(err)
	}
	src := httpSource(req)
	if _, err := parser.ParseExpr(src); err != nil {
		t.Fatalf("the generated source does not parse: %v\n%s", err, src)
	}
	for _, im := range httpImports(req) {
		if strings.Contains(strings.SplitN(im.Path, "/", 2)[0], ".") {
			t.Errorf("%s is not a standard library import", im.Path)
		}
	}

	// The mechanism, asserted where it is cheap to assert: the timeout the
	// user asked for, the bound applied while reading rather than after, and
	// a body that is closed whatever happens next.
	for _, want := range []string{
		"5*time.Second",
		"io.LimitReader(__resp.Body, " + strconv.Itoa(maxBody) + ")",
		"defer __resp.Body.Close()",
		`os.Getenv("GLUON_TEST_TOKEN")`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("the generated source is missing %q:\n%s", want, src)
		}
	}
	// And the thing the whole indirection exists for.
	if strings.Contains(src, "sq7Kd0aMzX9vLpQr2TfY") {
		t.Errorf("the credential is in the generated source:\n%s", src)
	}
}

// TestGeneratedSourceOmitsOSWhenNoHeaderNamesOne. An import nothing uses does
// not compile, so the list has to be exact rather than generous.
func TestGeneratedSourceOmitsOSWhenNoHeaderNamesOne(t *testing.T) {
	req, err := parseHTTPArgs("GET https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	src := httpSource(req)
	if strings.Contains(src, "os.Getenv") {
		t.Errorf("os.Getenv appears with no reference to resolve:\n%s", src)
	}
	for _, im := range httpImports(req) {
		if im.Path == "os" {
			t.Error("os is imported by a program that never names it")
		}
	}
	if _, err := parser.ParseExpr(src); err != nil {
		t.Fatalf("the generated source does not parse: %v\n%s", err, src)
	}
}

// TestGeneratedSourceCarriesTheTextualTypes. gluon owns the list and the child
// applies it; this is what stops the two becoming two lists.
func TestGeneratedSourceCarriesTheTextualTypes(t *testing.T) {
	req, err := parseHTTPArgs("GET https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	src := httpSource(req)
	for _, ct := range append(append([]string{}, textualPrefixes...), textualInfixes...) {
		if !strings.Contains(src, strconv.Quote(ct)) {
			t.Errorf("%q is in gluon's list and not in the program that applies it", ct)
		}
	}
}

// httpOut assembles what the child prints, so the render tests read as the
// response shapes they are about.
func httpOut(lines ...string) string { return strings.Join(lines, "\n") }

func httpLine(tag, rest string) string { return tag + httpSep + rest }

// TestTruncationStatesTheFullSizeOrSaysItIsUnknown.
//
// Both halves matter. With a Content-Length the notice can say what was
// skipped; without one there is no honest number, and printing an inferred
// figure would be gluon making one up.
func TestTruncationStatesTheFullSizeOrSaysItIsUnknown(t *testing.T) {
	body := strings.Repeat("x", maxBody)

	reported := parseHTTPOut(httpOut(
		httpLine(tagHTTPType, "application/json"),
		httpLine(tagHTTPLen, "1048576"),
		httpLine(tagHTTPRead, strconv.Itoa(maxBody)),
		httpLine(tagHTTPBody, body)))
	if !reported.truncated() {
		t.Fatal("a body cut at the bound was not reported as truncated")
	}
	if note := httpTruncNote(reported); !strings.Contains(note, "1048576") {
		t.Errorf("the notice does not state the full size: %s", note)
	}

	unknown := parseHTTPOut(httpOut(
		httpLine(tagHTTPType, "application/json"),
		httpLine(tagHTTPLen, "-1"),
		httpLine(tagHTTPRead, strconv.Itoa(maxBody)),
		httpLine(tagHTTPBody, body)))
	if !unknown.truncated() {
		t.Fatal("a chunked body cut at the bound was not reported as truncated")
	}
	note := httpTruncNote(unknown)
	if !strings.Contains(note, "not reported") {
		t.Errorf("the notice invents a size it was never told: %s", note)
	}

	// A body of exactly the bound whose length was reported is whole, and
	// saying otherwise would be a claim about bytes that do not exist.
	exact := parseHTTPOut(httpOut(
		httpLine(tagHTTPType, "application/json"),
		httpLine(tagHTTPLen, strconv.Itoa(maxBody)),
		httpLine(tagHTTPRead, strconv.Itoa(maxBody)),
		httpLine(tagHTTPBody, body)))
	if exact.truncated() {
		t.Error("a complete body of exactly the bound was reported as truncated")
	}
}

// TestABinaryBodyIsSummarised. Printing a PNG at a prompt is what the summary
// exists to stop; the type and the size are the two things worth knowing.
func TestABinaryBodyIsSummarised(t *testing.T) {
	c := &Core{Render: pretty.Plain}
	vals := []pretty.Value{{Type: "*http.Response", Kind: "scalar", Repr: "&{200 OK}"}}

	r := parseHTTPOut(httpOut(
		httpLine(tagHTTPType, "image/png"),
		httpLine(tagHTTPLen, "20345")))
	if r.HasBody {
		t.Fatal("a body was read for a non-textual content type")
	}
	res := c.renderHTTP(r, vals)
	if res.Err {
		t.Fatalf("%s", res.Out)
	}
	for _, want := range []string{"image/png", "20345 bytes", "not shown"} {
		if !strings.Contains(res.Out, want) {
			t.Errorf("the summary does not mention %q:\n%s", want, res.Out)
		}
	}

	// Without a Content-Length there is a type and no number, and the output
	// says so rather than printing a zero.
	sizeless := c.renderHTTP(parseHTTPOut(httpOut(
		httpLine(tagHTTPType, "application/octet-stream"),
		httpLine(tagHTTPLen, "-1"))), vals)
	if !strings.Contains(sizeless.Out, "size not reported") {
		t.Errorf("a missing length was printed as a number:\n%s", sizeless.Out)
	}
}

// TestATextualBodyIsPrintedWhole, and an empty one is named rather than left as
// a blank line — a 204 and a body gluon declined to read are different answers.
func TestATextualBodyIsPrintedWhole(t *testing.T) {
	c := &Core{Render: pretty.Plain}
	vals := []pretty.Value{{Type: "*http.Response", Kind: "scalar", Repr: "&{200 OK}"}}

	res := c.renderHTTP(parseHTTPOut(httpOut(
		httpLine(tagHTTPType, "application/json; charset=utf-8"),
		httpLine(tagHTTPLen, "13"),
		httpLine(tagHTTPRead, "13"),
		httpLine(tagHTTPBody, `{"ok":true}`))), vals)
	if res.Err {
		t.Fatalf("%s", res.Out)
	}
	if !strings.Contains(res.Out, `{"ok":true}`) {
		t.Errorf("the body is not in the output:\n%s", res.Out)
	}
	if strings.Contains(res.Out, "truncated") {
		t.Errorf("a whole body was reported as truncated:\n%s", res.Out)
	}

	empty := c.renderHTTP(parseHTTPOut(httpOut(
		httpLine(tagHTTPType, "application/json"),
		httpLine(tagHTTPLen, "0"),
		httpLine(tagHTTPRead, "0"),
		httpLine(tagHTTPBody, ""))), vals)
	if !strings.Contains(empty.Out, "no body") {
		t.Errorf("an empty body rendered as nothing at all:\n%s", empty.Out)
	}
}

// TestABodyWithNewlinesSurvivesTheWire. The body marker is last and
// unterminated precisely so that a line-oriented parse never has to guess where
// a body ended.
func TestABodyWithNewlinesSurvivesTheWire(t *testing.T) {
	body := "line one\nline two\n" + httpLine(tagHTTPLen, "999")
	r := parseHTTPOut(httpOut(
		httpLine(tagHTTPType, "text/plain"),
		httpLine(tagHTTPLen, "42"),
		httpLine(tagHTTPRead, "42"),
		httpLine(tagHTTPBody, body)))
	if r.Body != body {
		t.Errorf("body = %q, want %q", r.Body, body)
	}
	if r.Len != 42 {
		t.Errorf("a tag inside the body was read as a field: len = %d", r.Len)
	}
}

// TestAFailedRequestRendersNoResponse. Half an answer beside a failure reads as
// a response that arrived, which is the one thing that did not happen.
func TestAFailedRequestRendersNoResponse(t *testing.T) {
	c := &Core{Render: pretty.Plain}
	vals := []pretty.Value{{Type: "*http.Response", Kind: "nil", Repr: "nil"}}
	res := c.renderHTTP(parseHTTPOut(httpLine(tagHTTPErr,
		`Get "https://nope.invalid": dial tcp: no such host`)), vals)
	if !res.Err {
		t.Fatal("a failed request was not reported as an error")
	}
	if !strings.Contains(res.Out, "no such host") {
		t.Errorf("the cause is missing:\n%s", res.Out)
	}
	if strings.Contains(res.Out, "http.Response") {
		t.Errorf("a response was rendered for a request that never got one:\n%s", res.Out)
	}
}
