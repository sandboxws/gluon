package stdlib

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/plugin"
	"github.com/sandboxws/gluon/internal/pretty"
)

// parse builds a pretty.Value the way the child would send it, so these tests
// exercise the same input the renderer sees at runtime.
func parse(t *testing.T, blob string) pretty.Value {
	t.Helper()
	var v pretty.Value
	if err := json.Unmarshal([]byte(blob), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func styles() pretty.Styles { return pretty.Styles{} }

// TestDurationNanos covers the parse of Go's own duration form, including the
// case the unit table exists for: "m" must not swallow the "m" of "ms".
func TestDurationNanos(t *testing.T) {
	cases := map[string]int64{
		"0s":         0,
		"1ns":        1,
		"1s":         1e9,
		"1ms":        1e6,
		"1m0s":       60e9,
		"1m30s":      90e9,
		"2h15m0s":    8100e9,
		"1.5s":       1500e6,
		"-3s":        -3e9,
		"1h0m0.001s": 3600001e6,
	}
	for in, want := range cases {
		got, ok := durationNanos(in)
		if !ok {
			t.Errorf("durationNanos(%q) failed to parse", in)
			continue
		}
		if got != want {
			t.Errorf("durationNanos(%q) = %d, want %d", in, got, want)
		}
	}
	if _, ok := durationNanos("not a duration"); ok {
		t.Error("garbage parsed as a duration")
	}
}

// TestDurationRenderKeepsTheReadableForm: the child already rendered String(),
// and the plugin adds magnitude rather than replacing it.
func TestDurationRenderKeepsTheReadableForm(t *testing.T) {
	r := Time{}.Renders()[0]
	v := parse(t, `{"t":"time.Duration","k":"scalar","r":"2h15m0s"}`)
	out, ok := r.Rich(v, styles())
	if !ok {
		t.Fatal("the duration renderer declined a duration")
	}
	if !strings.Contains(out, "2h15m0s") {
		t.Errorf("dropped the readable form: %q", out)
	}
	if !strings.Contains(out, "= 8100s") {
		t.Errorf("did not add the total: %q", out)
	}
}

// TestDurationRenderDeclinesWhenItHasNothingToAdd. A hook that returns false
// falls through to gluon's own rendering, which is how a plugin stays out of
// the way — "(time.Duration) 3s  = 3s" is noise.
func TestDurationRenderDeclinesWhenItHasNothingToAdd(t *testing.T) {
	r := Time{}.Renders()[0]
	for _, repr := range []string{"3s", "0s"} {
		v := parse(t, `{"t":"time.Duration","k":"scalar","r":"`+repr+`"}`)
		if out, ok := r.Rich(v, styles()); ok {
			t.Errorf("%s was annotated with nothing useful: %q", repr, out)
		}
	}
}

// TestDurationRenderBelowASecondUsesNanoseconds, where the exact figure is the
// point and the number is small enough to read.
func TestDurationRenderBelowASecondUsesNanoseconds(t *testing.T) {
	r := Time{}.Renders()[0]
	v := parse(t, `{"t":"time.Duration","k":"scalar","r":"1.5ms"}`)
	out, ok := r.Rich(v, styles())
	if !ok {
		t.Fatal("declined a sub-second duration")
	}
	if !strings.Contains(out, "1,500,000ns") {
		t.Errorf("want the nanosecond total, got %q", out)
	}
}

// TestRenderersDeclineRatherThanBreak is invariant 19's other half: a hook that
// does not understand its input must fall through, not emit something wrong.
func TestRenderersDeclineRatherThanBreak(t *testing.T) {
	junk := []string{
		`{"t":"time.Duration","k":"scalar","r":"wat"}`,
		`{"t":"http.Header","k":"scalar","r":"nil"}`,
		`{"t":"http.Header","k":"map"}`,
		`{"t":"*http.Response","k":"scalar","r":"nil"}`,
		`{"t":"*http.Response","k":"ptr","i":[{"t":"http.Response","k":"struct"}]}`,
		`{"t":"json.RawMessage","k":"scalar","r":"nil"}`,
		`{"t":"json.RawMessage","k":"list","i":[{"k":"scalar","r":"not a byte"}]}`,
	}
	var all []plugin.Render
	all = append(all, Time{}.Renders()...)
	all = append(all, HTTP{}.Renders()...)
	all = append(all, JSON{}.Renders()...)

	for _, blob := range junk {
		v := parse(t, blob)
		for _, r := range all {
			if r.Type != v.Type {
				continue
			}
			if out, ok := r.Rich(v, styles()); ok {
				t.Errorf("%s accepted malformed input %s and produced %q", r.Type, blob, out)
			}
		}
	}
}

// TestHeaderRendersAsHeaders: the default map rendering shows a header's values
// with their slice brackets, which is not what the header says.
func TestHeaderRendersAsHeaders(t *testing.T) {
	v := parse(t, `{"t":"http.Header","k":"map","l":2,
		"ky":[{"k":"string","r":"Content-Type"},{"k":"string","r":"Accept"}],
		"i":[{"k":"list","i":[{"k":"string","r":"application/json"}]},
		     {"k":"list","i":[{"k":"string","r":"text/html"},{"k":"string","r":"*/*"}]}]}`)
	out, ok := renderHeader(v, styles())
	if !ok {
		t.Fatal("the header renderer declined a header")
	}
	if strings.Contains(out, "[application/json]") {
		t.Errorf("kept the slice brackets: %q", out)
	}
	if !strings.Contains(out, "text/html, */*") {
		t.Errorf("did not join a repeated header: %q", out)
	}
	// Sorted, because Go randomises map order and unstable output is noise.
	if strings.Index(out, "Accept") > strings.Index(out, "Content-Type") {
		t.Errorf("headers are not sorted: %q", out)
	}
}

// TestResponseLeadsWithStatus pulls the three fields that answer "what came
// back" out of the twenty a response carries.
func TestResponseLeadsWithStatus(t *testing.T) {
	v := parse(t, `{"t":"*http.Response","k":"ptr","i":[{"t":"http.Response","k":"struct","f":[
		{"n":"Status","v":{"k":"string","r":"404 Not Found"}},
		{"n":"StatusCode","v":{"k":"scalar","r":"404"}},
		{"n":"Proto","v":{"k":"string","r":"HTTP/1.1"}},
		{"n":"ContentLength","v":{"k":"scalar","r":"1234"}},
		{"n":"Header","v":{"k":"map","ky":[{"k":"string","r":"Server"}],
			"i":[{"k":"list","i":[{"k":"string","r":"nginx"}]}]}}]}]}`)
	out, ok := renderResponse(v, styles())
	if !ok {
		t.Fatal("the response renderer declined a response")
	}
	first := strings.SplitN(out, "\n", 2)[0]
	if !strings.Contains(first, "404 Not Found") {
		t.Errorf("status is not on the first line: %q", first)
	}
	if !strings.Contains(first, "1234 bytes") {
		t.Errorf("content length missing: %q", first)
	}
	if !strings.Contains(out, "Server") || !strings.Contains(out, "nginx") {
		t.Errorf("headers were dropped: %q", out)
	}
	// The nested table must not repeat its own "(http.Header)" heading, since
	// the response line above it is the heading.
	if strings.Contains(out, "(http.Header)") {
		t.Errorf("nested header table kept its heading: %q", out)
	}
}

// TestRawMessageReadsAsText. json.RawMessage is a []byte, so the value printer
// shows a list of small integers — the least useful rendering of something
// whose whole content is text.
func TestRawMessageReadsAsText(t *testing.T) {
	// `{"a":1}` as the byte renderer sends it.
	var items []string
	for _, b := range []byte(`{"a":1}`) {
		items = append(items, `{"k":"scalar","r":"`+itoa(int(b))+` (0x00)"}`)
	}
	v := parse(t, `{"t":"json.RawMessage","k":"list","i":[`+strings.Join(items, ",")+`]}`)
	out, ok := JSON{}.Renders()[0].Rich(v, styles())
	if !ok {
		t.Fatal("the RawMessage renderer declined")
	}
	if !strings.Contains(out, `{"a":1}`) {
		t.Errorf("did not reassemble the text: %q", out)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestCommandsRejectAnEmptyArgument keeps every plugin command's usage line
// honest, the way the registry test does for the builtins.
func TestCommandsRejectAnEmptyArgument(t *testing.T) {
	for _, p := range []plugin.Plugin{Time{}, JSON{}, Slog{}, HTTP{}} {
		cm, ok := p.(plugin.Commander)
		if !ok {
			continue
		}
		for _, c := range cm.Commands() {
			if !strings.Contains(c.Arg, "<") {
				continue
			}
			if _, err := c.Rewrite(""); err == nil {
				t.Errorf("%s accepted an empty argument", c.Name)
			}
		}
	}
}

// TestRewritesParseAsGo is the cheap guard against a command that only fails
// once someone runs it: every rewrite must produce something Go can parse.
func TestRewritesParseAsGo(t *testing.T) {
	for _, p := range []plugin.Plugin{Time{}, JSON{}, Slog{}} {
		cm := p.(plugin.Commander)
		for _, c := range cm.Commands() {
			src, err := c.Rewrite("x")
			if err != nil {
				t.Errorf("%s rejected a plain argument: %v", c.Name, err)
				continue
			}
			if err := parsesAsExpr(src); err != nil {
				t.Errorf("%s produced source that does not parse: %v\n%s", c.Name, err, src)
			}
		}
	}
}

// TestDurationInlineOmitsTheTypePrefix. Inline renders inside a table cell,
// where the column heading has already said which field this is.
func TestDurationInlineOmitsTheTypePrefix(t *testing.T) {
	r := Time{}.Renders()[0]
	v := parse(t, `{"t":"time.Duration","k":"scalar","r":"2h15m0s"}`)
	out, ok := r.Inline(v, styles())
	if !ok {
		t.Fatal("the inline duration form declined")
	}
	if strings.Contains(out, "(time.Duration)") {
		t.Errorf("inline form repeated the type: %q", out)
	}
	if !strings.Contains(out, "2h15m0s") || !strings.Contains(out, "8100s") {
		t.Errorf("inline form lost information: %q", out)
	}
}

// TestInlineFormsDeclineTogetherWithRich. Both forms rest on the same parse, so
// a value one cannot read the other must not claim either.
func TestInlineFormsDeclineTogetherWithRich(t *testing.T) {
	for _, r := range append(Time{}.Renders(), JSON{}.Renders()...) {
		if r.Inline == nil {
			continue
		}
		v := parse(t, `{"t":"`+r.Type+`","k":"scalar","r":"nonsense"}`)
		if out, ok := r.Inline(v, styles()); ok {
			t.Errorf("%s inline accepted nonsense and produced %q", r.Type, out)
		}
	}
}
