//go:build integration

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sandboxws/gluon/internal/repl"
)

// typeTools are the tools a server started without --eval must expose. The
// registry decides the tier and TestMCPTierIsIntentional pins it; this is the
// other end of that promise — that the tier is what actually reaches a client.
var typeTools = []string{
	"go_implements", "go_satisfies", "go_cast", "go_interface", "go_embeds", "go_generics",
}

// connect starts an in-memory client against a server built from core.
func connect(t *testing.T, core *repl.Core, allowEval bool) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverT, clientT := mcp.NewInMemoryTransports()

	ss, err := mcpServer(core, "", allowEval).Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { ss.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// TestTypeToolsNeedNoEval: the six interface commands neither build nor run, so
// they are static tier and an agent gets them without the flag that grants
// arbitrary execution.
func TestTypeToolsNeedNoEval(t *testing.T) {
	core, err := repl.NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	defer core.Close()

	cs := connect(t, core, false)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	listed := map[string]bool{}
	for _, tool := range res.Tools {
		listed[tool.Name] = true
	}
	for _, name := range typeTools {
		if !listed[name] {
			t.Errorf("%s is not listed by a server started without --eval", name)
		}
	}
	// The same listing is the proof the tier line still holds at all.
	for _, name := range []string{"go_eval", "go_bench", "go_slice_headers"} {
		if listed[name] {
			t.Errorf("%s runs code and must not be listed without --eval", name)
		}
	}
}

// TestTypeToolsHaveNoSideEffects: a static tool answers from the type checker,
// so calling one leaves the session exactly as it found it — same entries, same
// imports. An inspector that quietly appended to the transcript would corrupt
// every later answer.
func TestTypeToolsHaveNoSideEffects(t *testing.T) {
	core, err := repl.NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	defer core.Close()

	for _, line := range []string{
		"type Shape interface{ Area() float64 }",
		"type Sq struct{ side float64 }",
		"func (s Sq) Area() float64 { return s.side * s.side }",
		"type List[T any] struct{ items []T }",
		"x := 3",
	} {
		if res := core.Submit(line); res.Err {
			t.Fatalf("setup %q: %s", line, res.Out)
		}
	}

	// :hist numbers the entries and :src renders the whole program, import
	// block included. Between them they see anything a tool call could have
	// left behind.
	entriesBefore := core.Submit(":hist").Out
	sourceBefore := core.Submit(":src").Out

	cs := connect(t, core, false)
	calls := []struct{ tool, expr string }{
		{"go_implements", "Sq, Shape"},
		{"go_satisfies", "x, Shape"},
		{"go_cast", "x, Shape"},
		{"go_interface", "Shape"},
		{"go_embeds", "Sq"},
		{"go_generics", "List"},
	}
	for _, call := range calls {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      call.tool,
			Arguments: map[string]any{"expr": call.expr},
		})
		if err != nil {
			t.Fatalf("%s: %v", call.tool, err)
		}
		if res.IsError {
			t.Errorf("%s(%q) reported an error: %s", call.tool, call.expr, toolTextOf(res))
		}
		if text := toolTextOf(res); strings.TrimSpace(text) == "" {
			t.Errorf("%s(%q) returned nothing", call.tool, call.expr)
		}
	}

	if got := core.Submit(":hist").Out; got != entriesBefore {
		t.Errorf("the session's entries changed:\nbefore:\n%s\nafter:\n%s", entriesBefore, got)
	}
	if got := core.Submit(":src").Out; got != sourceBefore {
		t.Errorf("the generated program changed:\nbefore:\n%s\nafter:\n%s", sourceBefore, got)
	}
}

// TestGoTypeIgnoresTheDynamicFlag: :t -d evaluates, and a static tool never
// evaluates (invariant 28). An agent that copied an invocation out of a REPL
// transcript gets the static answer rather than a failure about a flag it
// cannot use — and gets it byte for byte, so nothing about the flag reaches the
// answer either.
func TestGoTypeIgnoresTheDynamicFlag(t *testing.T) {
	core, err := repl.NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	defer core.Close()

	for _, line := range []string{"var buf bytes.Buffer", "var r io.Reader = &buf"} {
		if res := core.Submit(line); res.Err {
			t.Fatalf("setup %q: %s", line, res.Out)
		}
	}
	sourceBefore := core.Submit(":src").Out

	cs := connect(t, core, false)
	call := func(expr string) string {
		t.Helper()
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "go_type",
			Arguments: map[string]any{"expr": expr},
		})
		if err != nil {
			t.Fatalf("go_type(%q): %v", expr, err)
		}
		if res.IsError {
			t.Fatalf("go_type(%q) reported an error: %s", expr, toolTextOf(res))
		}
		return toolTextOf(res)
	}

	static, dynamic := call("r"), call("-d r")
	if static != dynamic {
		t.Errorf("go_type answered -d differently:\nwithout:\n%s\nwith:\n%s", static, dynamic)
	}
	for _, leak := range []string{"static ", ", dynamic ", "*bytes.Buffer"} {
		if strings.Contains(dynamic, leak) {
			t.Errorf("go_type(-d r) reported %q, so something ran:\n%s", leak, dynamic)
		}
	}
	if got := core.Submit(":src").Out; got != sourceBefore {
		t.Errorf("the generated program changed:\nbefore:\n%s\nafter:\n%s", sourceBefore, got)
	}
}

func toolTextOf(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
