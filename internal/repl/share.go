package repl

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"go/scanner"
	"go/token"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sandboxws/gluon/internal/dsn"
	"github.com/sandboxws/gluon/internal/syntax"
)

// :share is the one command that publishes.
//
// Every other network-touching command in the set is the user's own program
// reaching out — http.Get always compiled, and :http and :grpc only make it
// shorter. This is gluon uploading the user's code to a third party, which is
// a different act and the second thing gluon does over the network on its own
// account, after :get fetching a module.
//
// It cannot be taken back. That single fact decides the whole design: the
// confirmation shows the bytes rather than a summary of them, there is no flag
// or setting that skips it, and content carrying anything the shared secret
// test recognises is refused rather than warned about — a warning with a way
// past it optimises for the case where the check is wrong, and the costs are
// not symmetric. A false refusal costs an edit; a false pass costs a published
// credential.
//
// Nothing here builds or runs anything. The program text already exists from
// Core.source(), and the request is made with net/http from gluon's own
// process — so the evaluator's hermetic environment, and its GOPROXY=off, are
// untouched.

// shareEndpoint is where the program is posted, and sharePlay is the address
// its identifier names. Package variables rather than constants so the tests
// can point them at a fixture server: the upload is the one part of this that
// cannot be exercised against the real service.
var (
	shareEndpoint = "https://go.dev/_/share"
	sharePlay     = "https://go.dev/play/p/"
)

// shareService is what the confirmation calls the destination. The user is
// being asked about a specific third party, and "a third-party service" is not
// something anybody can answer yes to.
const shareService = "the Go Playground"

// shareTimeout bounds the request. gluon has no other network call of its own,
// so there is no shared client to inherit one from — and a share that hangs
// leaves the reader unable to tell whether anything was published, which is
// the one outcome this command is built to avoid.
const shareTimeout = 15 * time.Second

// shareConfirmFlag carries the answer back from the view that asked.
//
// It is not a way to skip the confirmation and cannot become one: the token
// after it is generated when the content is displayed, is never printed, and
// names one specific set of bytes. Typed without a :share in front of it, it
// refuses. A flag that could be typed ahead of time — or written into a
// config file, or put in a :load script — would be exactly the pre-authorised
// consent this command exists not to have.
const shareConfirmFlag = "-confirm"

// pendingShare is content that has been shown and not yet answered.
type pendingShare struct {
	// token is what the view submits back. Unguessable so that confirming is
	// something only the view that displayed the content can do.
	token string
	// content is the bytes that were displayed, frozen. The upload sends this
	// rather than rendering the session again: what was approved and what is
	// sent must be one string, not two renderings that could differ.
	content string
}

// share is :share — publish the session's rendered program, having shown it.
func (c *Core) share(arg string) Result {
	arg = strings.TrimSpace(arg)
	if token, ok := cutFlag(arg, shareConfirmFlag); ok {
		return c.shareConfirmed(token)
	}
	if arg != "" {
		return Result{Out: "usage: :share   it takes no argument, and asks before it uploads", Err: true}
	}

	src, err := c.source()
	if err != nil {
		return Result{Out: "error: " + err.Error(), Err: true}
	}
	// Held exactly as source() rendered it. :src trims for display; this must
	// not, because the string that is shown is the string that is sent.
	content := src

	if found := shareSecrets(content); len(found) > 0 {
		// Refused, and the finding names the line so the edit is targeted.
		// Nothing is held: there is no pending share to confirm, so no answer
		// from any view can publish this.
		c.pending = nil
		return Result{Out: shareRefusal(found), Err: true}
	}

	if !c.Rich {
		// :replay's degradation rather than :edit's refusal. Printing what
		// would be uploaded is most of the value and carries no risk — it is
		// :src with a note — while a pipe cannot consent, and there is
		// deliberately no second form to offer it the way :replay offers
		// -run.
		c.pending = nil
		return Result{Out: content + "\nnothing was uploaded — confirming a share needs a terminal"}
	}

	token, err := shareToken()
	if err != nil {
		// Without a token there is no way for the view to answer, and the
		// alternative — a confirmation anything could send back — is worse
		// than not offering to share.
		return Result{Out: "error: " + err.Error() + " — nothing was published", Err: true}
	}
	c.pending = &pendingShare{token: token, content: content}

	// Out carries the same content linearly whatever the driver does with
	// Modal: invariant 19. Lang is deliberately not set on the Result — Out is
	// the program plus a line of prose, and Lang says the whole of Out is one
	// language. The modal's Text is the program alone, so it is tagged.
	return Result{
		Out: content + "\n" + shareAsk,
		Modal: &ModalSpec{
			Title:   "share this session",
			Summary: "the program was shown — nothing was uploaded",
			Text:    content,
			Lang:    syntax.Go,
			Note:    shareNote,
			Confirm: &ModalConfirm{
				Label: "upload it",
				Run:   ":share " + shareConfirmFlag + " " + token,
			},
		},
	}
}

// shareAsk and shareNote say the same thing to the two halves of invariant 19.
// Both name the destination and both say it cannot be withdrawn, because the
// reader of either one is about to answer the question.
const (
	shareAsk  = "enter uploads this to " + shareService + " — nothing has been sent yet"
	shareNote = "uploading publishes this to " + shareService + ", where it is public and cannot be withdrawn"
)

// shareConfirmed publishes the content that was shown, and only that.
func (c *Core) shareConfirmed(token string) Result {
	p := c.pending
	if p == nil || token == "" || token != p.token {
		return Result{Out: "nothing to confirm — :share shows what would be uploaded and asks", Err: true}
	}

	addr, err := uploadShare(p.content)
	if err != nil {
		// The consent stands. It was given for exactly these bytes and they
		// have not changed, so a network that was down is not a reason to make
		// the reader read the program again — which is how a reader learns to
		// skim it. What must never be ambiguous is whether anything went.
		return Result{Out: "error: " + err.Error() + " — nothing was published", Err: true}
	}
	// Answered, so the consent is spent. A second enter asks again.
	c.pending = nil
	return Result{Out: "shared → " + addr}
}

// shareToken is one confirmation's name.
func shareToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("no way to name this confirmation: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// A shareFinding is one value in the program that the shared secret test
// recognised, and the line it is on.
type shareFinding struct {
	Line int
	// Shown is the redacted form. The refusal names what was found without
	// reprinting it: a message that echoed the credential back would put it
	// somewhere else again, which is the direction this command does not go.
	Shown string
}

// shareSecrets is the shared shape-based secret test, run over a program.
//
// dsn.RedactValue judges one value. A Go program is not a value, so the
// question is which parts of it are: a credential in source is a string
// literal, and go/scanner hands those over with their positions and nothing
// else. The name of the variable it is assigned to is never consulted and is
// not available here — invariant 23, held by construction rather than by
// discipline.
//
// The scanner rather than the parser because source() can return a program
// that does not parse: on a syntax error it hands back what the build error
// carried, and that is exactly the session somebody might try to share.
// scanner.ErrorHandler swallowing errors is right for the same reason — a
// token stream with a bad token in it still has every other token in it.
func shareSecrets(src string) []shareFinding {
	var out []shareFinding
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))

	var s scanner.Scanner
	s.Init(file, []byte(src), func(token.Position, string) {}, 0)
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			return out
		}
		if tok != token.STRING {
			continue
		}
		v, err := strconv.Unquote(lit)
		if err != nil {
			// Unterminated, or an escape the scanner recovered from. There is
			// no value to judge.
			continue
		}
		if shown, hid := dsn.RedactValue(v, ""); hid {
			out = append(out, shareFinding{Line: fset.Position(pos).Line, Shown: shown})
		}
	}
}

// shareRefusal says what was found and where, and does not offer a way past.
func shareRefusal(found []shareFinding) string {
	var b strings.Builder
	head := "refused: a value in the program looks like a credential"
	if len(found) > 1 {
		head = fmt.Sprintf("refused: %d values in the program look like credentials", len(found))
	}
	fmt.Fprintf(&b, "%s, so nothing was uploaded\n", head)
	for _, f := range found {
		fmt.Fprintf(&b, "  line %d  %s\n", f.Line, f.Shown)
	}
	b.WriteString("a share cannot be withdrawn, so this is a refusal and not a warning — " +
		":drop or :edit the line, and share the session without it")
	return b.String()
}

// uploadShare posts the program and returns the address it can be read at.
//
// One request, from gluon's own process. Success is a 2xx carrying the
// snippet's identifier; the identifier is reproduced exactly and the address
// is that identifier under the service's documented path, so nothing about
// what came back is shortened or rewritten. Everything else — unreachable,
// rejected, or answered with no identifier — is the same outcome from the
// reader's side, and is reported as one: nothing was published.
func uploadShare(content string) (string, error) {
	client := &http.Client{Timeout: shareTimeout}
	res, err := client.Post(shareEndpoint, "text/plain; charset=utf-8", strings.NewReader(content))
	if err != nil {
		return "", fmt.Errorf("%s could not be reached", shareService)
	}
	defer res.Body.Close()

	// Bounded: this is a snippet id, and a body that arrived instead of one is
	// not something to read to the end of.
	body, err := io.ReadAll(io.LimitReader(res.Body, 4096))
	if err != nil {
		return "", fmt.Errorf("%s answered, and the answer could not be read", shareService)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return "", fmt.Errorf("%s refused the upload (%s)", shareService, res.Status)
	}
	id := strings.TrimSpace(string(body))
	if id == "" || strings.ContainsAny(id, " \t\n/") {
		// A 200 with nothing usable in it. Reported as a failure rather than
		// as an address, because an address gluon invented is one nobody can
		// open.
		return "", fmt.Errorf("%s accepted the upload and named no snippet", shareService)
	}
	return sharePlay + id, nil
}
