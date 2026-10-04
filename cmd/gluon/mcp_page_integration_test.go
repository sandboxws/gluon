//go:build integration

package main

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sandboxws/gluon/internal/repl"
)

// rangeLine matches the trailing line paging appends: lines a–b of n.
var rangeLine = regexp.MustCompile(`(?m)^lines (\d+)–(\d+) of (\d+)$`)

// callTool is one tool call against an in-memory server, failing the test on a
// transport error and handing back the result for the caller to read.
func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s(%v): %v", name, args, err)
	}
	return res
}

// TestPagingDeliversALongAnswerInParts: an answer longer than a client's window
// is the case ROADMAP.md recorded as "the client's problem". It is not any
// more: the client asks for the lines it can hold and is told how many there
// are, so it knows whether to ask again.
func TestPagingDeliversALongAnswerInParts(t *testing.T) {
	core, err := repl.NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	defer core.Close()

	cs := connect(t, core, false)

	// A symbol whose documentation is comfortably longer than one page.
	const symbol = "strings"
	whole := toolTextOf(callTool(t, cs, "go_doc", map[string]any{"expr": symbol}))
	total := len(strings.Split(strings.TrimSuffix(whole, "\n"), "\n"))
	if total < 40 {
		t.Skipf("go_doc %s is only %d lines; too short to page", symbol, total)
	}
	if rangeLine.MatchString(whole) {
		t.Fatalf("an unpaged answer carries a range line:\n%s", whole)
	}

	const limit = 20
	first := toolTextOf(callTool(t, cs, "go_doc", map[string]any{"expr": symbol, "limit": limit}))
	m := rangeLine.FindStringSubmatch(first)
	if m == nil {
		t.Fatalf("no range line in the first page:\n%s", first)
	}
	if got := fmt.Sprintf("lines %s–%s of %s", m[1], m[2], m[3]); got != fmt.Sprintf("lines 1–%d of %d", limit, total) {
		t.Errorf("first page reports %q, want lines 1–%d of %d", got, limit, total)
	}

	// The page is the answer's own first lines, not a re-render of it.
	body := strings.TrimSuffix(first, "\n\n"+m[0])
	if want := strings.Join(strings.Split(whole, "\n")[:limit], "\n"); body != want {
		t.Errorf("first page body is not the answer's first %d lines:\ngot:\n%s\nwant:\n%s", limit, body, want)
	}

	// The last page runs short and says so, and the two ends meet: paging
	// through the whole answer reproduces it.
	lastOffset := total - total%limit
	if lastOffset == total {
		lastOffset = total - limit
	}
	last := toolTextOf(callTool(t, cs, "go_doc", map[string]any{
		"expr": symbol, "offset": lastOffset, "limit": limit,
	}))
	m = rangeLine.FindStringSubmatch(last)
	if m == nil {
		t.Fatalf("no range line in the last page:\n%s", last)
	}
	if end, _ := strconv.Atoi(m[2]); end != total {
		t.Errorf("the last page ends at line %d, want %d", end, total)
	}

	// Past the end is the answer to "is there more", not an error.
	past := callTool(t, cs, "go_doc", map[string]any{"expr": symbol, "offset": total, "limit": limit})
	if past.IsError {
		t.Errorf("an offset past the end reported an error: %s", toolTextOf(past))
	}
	if text := toolTextOf(past); !strings.Contains(text, strconv.Itoa(total)+" lines total") {
		t.Errorf("past the end does not state the total: %q", text)
	}
}

// TestAnErrorIsNeverPaged: a client that asked for one line of a failure would
// be given the first line of the reason and left to guess the rest. The whole
// error comes back whatever paging was asked for.
func TestAnErrorIsNeverPaged(t *testing.T) {
	core, err := repl.NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	defer core.Close()

	cs := connect(t, core, false)
	args := map[string]any{"expr": "NoSuchSymbolAnywhere"}
	plain := callTool(t, cs, "go_doc", args)
	if !plain.IsError {
		t.Skipf("go_doc on an unknown symbol did not fail: %s", toolTextOf(plain))
	}

	args["limit"] = 1
	paged := callTool(t, cs, "go_doc", args)
	if !paged.IsError {
		t.Fatal("the same call with limit 1 was not an error")
	}
	if got, want := toolTextOf(paged), toolTextOf(plain); got != want {
		t.Errorf("limit 1 changed the error:\ngot:\n%s\nwant:\n%s", got, want)
	}
	if rangeLine.MatchString(toolTextOf(paged)) {
		t.Error("the error carries a range line")
	}
}

// TestMockNeedsNoEval: :mock is go/types over the method set :m already
// computes — it runs nothing, which is what puts it in the static tier. The
// proof that the answer is worth having is that it parses as Go.
func TestMockNeedsNoEval(t *testing.T) {
	core, err := repl.NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	defer core.Close()

	sourceBefore := core.Submit(":src").Out

	cs := connect(t, core, false)
	res := callTool(t, cs, "go_mock", map[string]any{"expr": "io.ReadWriter"})
	if res.IsError {
		t.Fatalf("go_mock(io.ReadWriter): %s", toolTextOf(res))
	}
	src := toolTextOf(res)

	// The mock is a declaration, not a file, so it is parsed inside one.
	if _, err := parser.ParseFile(token.NewFileSet(), "mock.go", "package p\n\n"+src, parser.AllErrors); err != nil {
		t.Fatalf("go_mock did not answer with Go: %v\n%s", err, src)
	}
	// io.ReadWriter embeds two interfaces; both methods must be there, which is
	// the reason this interface was chosen.
	for _, method := range []string{"Read", "Write"} {
		if !strings.Contains(src, method) {
			t.Errorf("the mock has no %s method:\n%s", method, src)
		}
	}
	if got := core.Submit(":src").Out; got != sourceBefore {
		t.Errorf("go_mock changed the generated program:\nbefore:\n%s\nafter:\n%s", sourceBefore, got)
	}
}
