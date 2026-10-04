package repl

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sandboxws/gluon/internal/argline"
	"github.com/sandboxws/gluon/internal/pretty"
	"github.com/sandboxws/gluon/internal/redact"
	"github.com/sandboxws/gluon/internal/render"
	"github.com/sandboxws/gluon/internal/session"
)

// :http issues one request and renders what came back.
//
// It is sugar over a line the session could already type. The rewrite below is
// an ordinary net/http call and nothing else, which is what keeps a plugin
// command's rule — it can do nothing a typed line could not — literally true,
// and what makes :src worth reading afterwards.
//
// Two things it does that typing does not. A credential is *named* rather than
// typed, because the line as typed is what reaches
// ~/.local/state/gluon/history in plaintext: that is the same ground a -dsn
// flag was rejected on. And the body is bounded inside the child, because an
// unbounded read pulls an arbitrary payload into the child's memory and then
// across the pipe before anyone has decided it was worth showing.

const (
	// maxBody is how much of a response body one request reads.
	//
	// It is a display policy, the way db.MaxRows is, rather than a limit the
	// encoder imposes: what it bounds is how much of a payload is useful at a
	// prompt. 64 KiB is several screens of JSON and costs nothing to read;
	// past it the notice says what was skipped rather than letting the output
	// imply the response was this size.
	maxBody = 64 << 10

	// defaultHTTPTimeout bounds the request inside the child.
	//
	// The evaluator's own timeout would eventually fire too, but it is
	// measured for compiling and running a program, not for waiting on a
	// network. An endpoint that never answers should say so in seconds rather
	// than wedge the prompt for the full session timeout with no explanation.
	defaultHTTPTimeout = "30s"

	httpUsage = "usage: :http <method> <url>   e.g. :http GET https://api.github.com/zen"
)

// Wire markers. The child describes; gluon formats — the same split :query
// runs on, and the reason the truncation notice below states a fact rather
// than an inference.
//
// body is last and unterminated: everything after its marker is body, because
// a body has newlines in it and a line-oriented parse would have to guess
// where it ended.
const (
	httpSep     = "\x1f"
	tagHTTPErr  = "err"  // the request failed, with the cause
	tagHTTPType = "type" // Content-Type, verbatim
	tagHTTPLen  = "len"  // Content-Length as reported, or -1
	tagHTTPRead = "read" // bytes actually read, which the bound may have cut
	tagHTTPBody = "body"
)

// Source-literal forms of the markers, for the same reason internal/db quotes
// its own: \x1f has to reach the generated program as an escape sequence. A
// raw control byte compiles, and is invisible in the :src that gofmt just ran
// over.
var (
	litHTTPErr  = strconv.Quote(tagHTTPErr + httpSep)
	litHTTPType = strconv.Quote(tagHTTPType + httpSep)
	litHTTPLen  = strconv.Quote(tagHTTPLen + httpSep)
	litHTTPRead = strconv.Quote(tagHTTPRead + httpSep)
	litHTTPBody = strconv.Quote("\n" + tagHTTPBody + httpSep)
)

// httpMethods is what the first argument may be.
//
// A closed list rather than "whatever you typed": a mistyped verb would
// otherwise be sent, and a server's answer to a method nobody meant is a
// confusing way to find out about a typo. CONNECT is left out because it
// addresses a proxy rather than a URL, which http.NewRequest cannot express.
var httpMethods = []string{
	"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "TRACE",
}

// textualPrefixes and textualInfixes are the content types whose body is worth
// printing.
//
// They are matched on a prefix because a content type carries parameters —
// `application/json; charset=utf-8` is the ordinary form — and on an infix for
// the structured-suffix convention, where `application/vnd.api+json` is JSON
// and names no JSON media type.
//
// The lists are written into the generated program rather than consulted here,
// so a body that would only be summarised is never read at all. Bytes nobody
// will look at should not cross the pipe first.
var (
	textualPrefixes = []string{
		"text/",
		"application/json",
		"application/xml",
		"application/javascript",
		"application/x-www-form-urlencoded",
		"application/graphql",
	}
	textualInfixes = []string{"+json", "+xml"}
)

// httpPart is one piece of a header value: literal text, or the name of an
// environment variable standing in for it.
//
// An alias rather than a type of its own. :grpc splits its metadata values by
// exactly this rule and runs the same shape test over the result, and two
// copies of that would eventually disagree — see internal/argline.
type httpPart = argline.Part

// httpHeader is one -H, already split into what was typed and what was named.
type httpHeader struct {
	Name string
	// Value is the value as typed, references intact. It is what a message
	// quotes: telling somebody about a value they did not write sends them
	// looking for the wrong thing.
	Value string
	Parts []httpPart
}

// httpRequest is a parsed :http line.
type httpRequest struct {
	Method  string
	URL     string
	Headers []httpHeader
	Body    string
	Timeout string
	// Refs is every variable the headers name, first-seen order, filled by
	// resolveHTTPRefs. The child's environment and the redaction list are
	// built from this one walk, so the two cannot disagree about which
	// values are secret.
	Refs []string
}

// parseHTTPArgs reads `<method> <url>` and the flags that follow them.
//
// The two operands are positional and lead, so the common case — :http GET
// <url> — needs no flag at all. Flags come after, unlike :query's, because
// there the statement is the tail and here the tail is a flag list.
func parseHTTPArgs(arg string) (httpRequest, error) {
	req := httpRequest{Timeout: defaultHTTPTimeout}
	words, err := argline.Fields(arg)
	if err != nil {
		return req, err
	}
	if len(words) == 0 {
		return req, fmt.Errorf("%s", httpUsage)
	}

	req.Method = strings.ToUpper(words[0])
	if !knownHTTPMethod(req.Method) {
		return req, fmt.Errorf("%s is not an HTTP method — one of %s",
			words[0], strings.Join(httpMethods, ", "))
	}
	if len(words) < 2 {
		return req, fmt.Errorf("%s", httpUsage)
	}
	req.URL = words[1]
	if err := checkHTTPURL(req.URL); err != nil {
		return req, err
	}

	for i := 2; i < len(words); i++ {
		flag := words[i]
		switch flag {
		case "-H", "-d", "-t":
		default:
			return req, fmt.Errorf(
				"unknown flag %s — :http takes -H <name: value>, -d <body> and -t <duration>", flag)
		}
		if i+1 >= len(words) {
			return req, fmt.Errorf("%s needs a value", flag)
		}
		value := words[i+1]
		i++
		switch flag {
		case "-H":
			h, err := parseHTTPHeader(value)
			if err != nil {
				return req, err
			}
			req.Headers = append(req.Headers, h)
		case "-d":
			req.Body = value
		case "-t":
			if d, err := time.ParseDuration(value); err != nil || d <= 0 {
				return req, fmt.Errorf("-t %s is not a duration — 5s, 500ms, 1m", value)
			}
			req.Timeout = value
		}
	}
	return req, nil
}

func knownHTTPMethod(m string) bool {
	for _, k := range httpMethods {
		if k == m {
			return true
		}
	}
	return false
}

// checkHTTPURL refuses a URL gluon would have to complete.
//
// A scheme-less host is the one place guessing is tempting and wrong: http://
// and https:// are different requests, and picking one silently would make the
// difference invisible in exactly the session where somebody is debugging it.
func checkHTTPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s is not a URL: %v", raw, err)
	}
	switch {
	case u.Scheme == "":
		return fmt.Errorf("%s has no scheme — write https://%s. "+
			"gluon will not pick one: http and https are different requests", raw, raw)
	case u.Scheme != "http" && u.Scheme != "https":
		return fmt.Errorf("%s is a %s URL — :http speaks http and https", raw, u.Scheme)
	case u.Host == "":
		return fmt.Errorf("%s names no host", raw)
	}
	return nil
}

// parseHTTPHeader splits one -H into a name and a value, and the value into
// the literal text and the variables it names.
func parseHTTPHeader(s string) (httpHeader, error) {
	// The first colon is the separator: a header value routinely contains
	// more of them, starting with every URL.
	name, value, ok := strings.Cut(s, ":")
	if !ok {
		return httpHeader{}, fmt.Errorf("-H %q has no colon — a header is written `Name: value`", s)
	}
	name, value = strings.TrimSpace(name), strings.TrimSpace(value)
	if name == "" {
		return httpHeader{}, fmt.Errorf("-H %q has no name", s)
	}
	parts := argline.SplitRefs(value)

	// The shape test runs on what was typed, with the references still
	// standing as $NAME — which is exactly why a referenced credential passes
	// and a typed one does not, without the test having to know what a
	// reference means. Invariant 23 holds either way: the header's *name* is
	// never consulted, so an X-Anything carrying a bearer token is refused and
	// an Authorization carrying "Negotiate" is not.
	if redact.Secret(argline.Probe(parts)) {
		return httpHeader{}, fmt.Errorf(
			"%s looks like a credential typed on the line, and the line is already in "+
				"~/.local/state/gluon/history.\n"+
				"      Masking the display would not take it back. Name a variable instead:  "+
				"-H '%s: $TOKEN'  — it is read in the child and written nowhere",
			name, name)
	}
	return httpHeader{Name: name, Value: value, Parts: parts}, nil
}

// resolveHTTPRefs looks up every variable the headers name, and refuses when
// one has no value.
//
// An unset variable would otherwise send an empty header, which an endpoint
// answers with a 401 — a failure indistinguishable from a wrong credential,
// which sends somebody to rotate a token that was fine.
//
// It returns what the child's environment gets and what must not appear in the
// child's output. Both come from this one walk: a second lookup is how a value
// ends up masked in one place and not the other.
func resolveHTTPRefs(req *httpRequest) (env, secrets []string, err error) {
	seen := map[string]bool{}
	for _, h := range req.Headers {
		for _, p := range h.Parts {
			if p.Ref == "" || seen[p.Ref] {
				continue
			}
			seen[p.Ref] = true
			val, ok := os.LookupEnv(p.Ref)
			switch {
			case !ok:
				return nil, nil, fmt.Errorf(
					"%s names $%s, which is not set — gluon reads it from its own "+
						"environment, and will not send the header empty", h.Name, p.Ref)
			case val == "":
				return nil, nil, fmt.Errorf(
					"%s names $%s, which is set to nothing — an empty header reads as a "+
						"wrong credential rather than a missing one", h.Name, p.Ref)
			}
			req.Refs = append(req.Refs, p.Ref)
			env = append(env, p.Ref+"="+val)
			secrets = append(secrets, val)
		}
	}
	return env, secrets, nil
}

// httpImports are what the generated program needs.
//
// Supplied rather than left to goimports, which costs ~135ms, and exact
// because an import nothing names does not compile: os appears only when a
// header actually calls os.Getenv.
func httpImports(req httpRequest) []render.ImportSpec {
	imps := []render.ImportSpec{
		{Path: "fmt"},
		{Path: "io"},
		{Path: "net/http"},
		{Path: "strconv"},
		{Path: "strings"},
		{Path: "time"},
	}
	if len(req.Refs) > 0 {
		imps = append(imps, render.ImportSpec{Path: "os"})
	}
	return imps
}

// httpTimeout renders a duration as Go source.
//
// Whole seconds become 30*time.Second rather than a nanosecond count, because
// :src shows this program to the person who typed the line and nobody reads
// 30000000000. It is internal/db's goDuration reasoning rather than its code:
// sharing eight lines is not worth a dependency between the database layer and
// this one.
func httpTimeout(spec string) string {
	d, err := time.ParseDuration(spec)
	if err != nil || d <= 0 {
		d, _ = time.ParseDuration(defaultHTTPTimeout)
	}
	if d%time.Second == 0 {
		return strconv.FormatInt(int64(d/time.Second), 10) + "*time.Second"
	}
	if d%time.Millisecond == 0 {
		return strconv.FormatInt(int64(d/time.Millisecond), 10) + "*time.Millisecond"
	}
	return "time.Duration(" + strconv.FormatInt(int64(d), 10) + ")"
}

// headerExpr is the Go expression a header value becomes.
//
// A referenced value is os.Getenv("NAME") and never the value itself, so
// neither <tmp>/main.go, :src, :save nor a build error — which quotes source —
// can carry it. That leaves the child's own output, which EvalLive's mask
// covers.
func headerExpr(parts []httpPart) string {
	if len(parts) == 0 {
		return `""`
	}
	terms := make([]string, 0, len(parts))
	for _, p := range parts {
		if p.Ref != "" {
			terms = append(terms, "os.Getenv("+strconv.Quote(p.Ref)+")")
			continue
		}
		terms = append(terms, strconv.Quote(p.Text))
	}
	return strings.Join(terms, "+")
}

// goStrings renders a []string as a Go composite literal.
func goStrings(xs []string) string {
	quoted := make([]string, len(xs))
	for i, x := range xs {
		quoted[i] = strconv.Quote(x)
	}
	return "[]string{" + strings.Join(quoted, ", ") + "}"
}

// httpSource is the program the request becomes.
//
// It returns (*http.Response, error) because that is the shape of the line it
// stands in for — http.Get's own — and the response value is what gluon then
// renders through the ordinary value path, so the http plugin draws it in a
// terminal and pretty.Plain writes it through a pipe.
//
// The description printed alongside it is separate on purpose. A body has
// newlines and a length, a content type decides whether printing it is even
// useful, and none of that is expressible as a Go value the encoder would
// carry — so the child states the facts and gluon formats them, which is the
// split :query already runs on.
func httpSource(req httpRequest) string {
	body := "nil"
	if req.Body != "" {
		body = "strings.NewReader(" + strconv.Quote(req.Body) + ")"
	}

	var b strings.Builder
	b.WriteString(`func() (*http.Response, error) {
	__fail := func(__e error) (*http.Response, error) {
		fmt.Print(` + litHTTPErr + ` + __e.Error())
		return nil, __e
	}
	__req, __err := http.NewRequest(` + strconv.Quote(req.Method) + `, ` +
		strconv.Quote(req.URL) + `, ` + body + `)
	if __err != nil {
		return __fail(__err)
	}
`)
	for _, h := range req.Headers {
		b.WriteString("	__req.Header.Set(" + strconv.Quote(h.Name) + ", " +
			headerExpr(h.Parts) + ")\n")
	}
	b.WriteString(`	__client := &http.Client{Timeout: ` + httpTimeout(req.Timeout) + `}
	// Deferred before the body is, so it runs after it: a connection returns to
	// the idle pool only once the body is closed. Without this the transport's
	// keep-alive goroutine outlives main, and gluon waits two seconds for it and
	// then reports it — on every request. Nothing here would reuse the
	// connection anyway; the process dies after this line.
	defer __client.CloseIdleConnections()
	__resp, __err := __client.Do(__req)
	if __err != nil {
		return __fail(__err)
	}
	defer __resp.Body.Close()
	__ct := __resp.Header.Get("Content-Type")
	__desc := []string{
		` + litHTTPType + ` + __ct,
		` + litHTTPLen + ` + strconv.FormatInt(__resp.ContentLength, 10),
	}
	// A response with no content type is read: an endpoint that omits it is
	// usually serving text, and refusing to show a body over a missing header
	// would be a worse guess than showing one.
	__textual := __ct == ""
	for _, __p := range ` + goStrings(textualPrefixes) + ` {
		if strings.HasPrefix(__ct, __p) {
			__textual = true
		}
	}
	for _, __p := range ` + goStrings(textualInfixes) + ` {
		if strings.Contains(__ct, __p) {
			__textual = true
		}
	}
	if !__textual {
		fmt.Print(strings.Join(__desc, "\n"))
		return __resp, nil
	}
	__read, __rerr := io.ReadAll(io.LimitReader(__resp.Body, ` + strconv.Itoa(maxBody) + `))
	if __rerr != nil {
		return __fail(__rerr)
	}
	__desc = append(__desc, ` + litHTTPRead + ` + strconv.Itoa(len(__read)))
	fmt.Print(strings.Join(__desc, "\n") + ` + litHTTPBody + ` + string(__read))
	return __resp, nil
}()`)
	return b.String()
}

// httpReport is what the child said about the response, beside the response
// itself.
type httpReport struct {
	Err  string
	Type string
	// Len is Content-Length as the response reported it, and -1 when it did
	// not report one.
	Len int64
	// Read is how many bytes were actually read. HasBody says whether the body
	// was read at all — a 204 and a skipped binary body are different answers,
	// and an empty string cannot tell them apart.
	Read    int
	Body    string
	HasBody bool
}

// parseHTTPOut reads back what httpSource's program printed.
func parseHTTPOut(out string) httpReport {
	r := httpReport{Len: -1}
	head := out
	if i := strings.Index(out, "\n"+tagHTTPBody+httpSep); i >= 0 {
		head = out[:i]
		r.Body = out[i+len("\n"+tagHTTPBody+httpSep):]
		r.HasBody = true
	}
	for _, line := range strings.Split(head, "\n") {
		tag, rest, ok := strings.Cut(line, httpSep)
		if !ok {
			continue
		}
		switch tag {
		case tagHTTPErr:
			r.Err = rest
		case tagHTTPType:
			r.Type = rest
		case tagHTTPLen:
			if n, err := strconv.ParseInt(rest, 10, 64); err == nil {
				r.Len = n
			}
		case tagHTTPRead:
			if n, err := strconv.Atoi(rest); err == nil {
				r.Read = n
			}
		}
	}
	return r
}

// truncated reports whether the bound cut the body.
//
// Reading exactly maxBody bytes is ambiguous on its own — a body of precisely
// that size looks the same as one that was cut — so a reported Content-Length
// settles it, and its absence leaves the answer at "it was cut", which is the
// safe half of the ambiguity: claiming a whole body was shown when it was not
// is the failure worth avoiding.
func (r httpReport) truncated() bool {
	return r.HasBody && r.Read >= maxBody && (r.Len < 0 || r.Len > int64(maxBody))
}

// metaHTTP is `:http`.
//
// A builtin rather than a plugin command for the reason :query is one: a
// plugin's only mechanism is Rewrite(arg string), which sees its argument and
// nothing else — it cannot reach the environment to resolve a reference, nor
// EvalLive to keep the answer out of the result cache.
func (c *Core) metaHTTP(arg string) Result {
	if strings.TrimSpace(arg) == "" {
		return Result{Out: httpUsage, Err: true}
	}
	req, err := parseHTTPArgs(arg)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	env, secrets, err := resolveHTTPRefs(&req)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}

	// EvalLive, not EvalTransient, for two independent reasons. The cache is
	// keyed on program text alone, so the same request twice would answer with
	// the first response forever — the identical failure :query documents. And
	// it is what carries a child-only environment and applies mask to what
	// comes back.
	res, err := c.ev.EvalLive(c.sess,
		session.Entry{Kind: session.KindExpr, Src: httpSource(req)},
		httpImports(req), env, secrets)
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	out, vals := pretty.Parse(res.Output)
	return c.renderHTTP(parseHTTPOut(out), vals)
}

// renderHTTP lays out the response, then what is true about its body.
func (c *Core) renderHTTP(r httpReport, vals []pretty.Value) Result {
	st := c.styles()
	if r.Err != "" {
		// Nothing of the response is drawn. Half a rendered answer beside a
		// failure reads as a response that arrived, which is the one thing
		// that did not happen.
		return Result{Out: "error: the request failed — " + r.Err, Err: true}
	}
	if len(vals) == 0 {
		return Result{Out: "error: the request produced no answer", Err: true}
	}

	var b strings.Builder
	// Only vals[0]. The second result is the error, which is nil on this path,
	// and rendering a nil beside a response is noise. The response itself goes
	// through the ordinary value path, so the http plugin's renderer draws it
	// in a terminal and pretty.Plain writes it byte for byte through a pipe —
	// invariant 21.
	b.WriteString(c.Render(vals[:1]))

	switch {
	case !r.HasBody:
		b.WriteString("\n" + st.Note.Render("  body not shown — "+httpBodyNote(r)))
	case r.Body == "":
		b.WriteString("\n" + st.Annot.Render("  no body"))
	default:
		b.WriteString("\n" + r.Body)
	}
	if r.truncated() {
		b.WriteString("\n" + st.Note.Render("  "+httpTruncNote(r)))
	}
	return Result{Out: b.String()}
}

// httpBodyNote says what was not printed, and how much of it there was.
func httpBodyNote(r httpReport) string {
	kind := r.Type
	if kind == "" {
		kind = "no content type"
	}
	if r.Len < 0 {
		return kind + ", size not reported"
	}
	return fmt.Sprintf("%s, %d bytes", kind, r.Len)
}

// httpTruncNote states the cut, and states it as a fact or not at all.
//
// Without a Content-Length there is no honest number for what was skipped, and
// inferring one from the bound would be gluon printing a figure it made up.
func httpTruncNote(r httpReport) string {
	if r.Len < 0 {
		return fmt.Sprintf("body truncated at %d bytes — the full size was not reported", maxBody)
	}
	return fmt.Sprintf("body truncated at %d bytes — %d in total", maxBody, r.Len)
}
