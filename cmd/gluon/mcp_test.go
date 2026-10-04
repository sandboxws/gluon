package main

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sandboxws/gluon/internal/repl"
)

// go_type is the one static tool whose REPL command grew a flag that runs
// code. Invariant 28 says a tool that evaluates is never exposed without
// --eval, so the flag cannot be honoured here — and an agent that copied an
// invocation out of a REPL transcript should still get the static answer
// rather than a failure about a flag it cannot use.

// TestStripDynamicLeavesTheStaticQuestion: -d is cut, and nothing else is.
func TestStripDynamicLeavesTheStaticQuestion(t *testing.T) {
	typeCmd := repl.Command{Name: ":t", Arg: "[-v|-d] <exp>", Static: true}
	for _, tc := range []struct{ in, want string }{
		{in: "-d r", want: "r"},
		{in: "  -d   buf.Bytes()  ", want: "buf.Bytes()"},
		{in: "-d", want: ""},
		{in: "r", want: "r"},
		{in: "-v r", want: "-v r"},
		// A unary minus is not a flag: -dx negates dx.
		{in: "-dx", want: "-dx"},
		// Only a leading -d is a flag; one inside an expression is arithmetic.
		{in: "a -d b", want: "a -d b"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			if got := stripDynamic(typeCmd, tc.in); got != tc.want {
				t.Errorf("stripDynamic(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestStripDynamicTouchesNothingElse: the strip is keyed on the registry entry,
// so a command with no -d in its argument spec — and every evaluating tool,
// which has no such flag to strip — passes its argument through verbatim.
func TestStripDynamicTouchesNothingElse(t *testing.T) {
	for _, cmd := range []repl.Command{
		{Name: ":m", Arg: "<exp>", Static: true},
		{Name: ":bench", Arg: "[-count n] <exp>"},
	} {
		if got := stripDynamic(cmd, "-d x"); got != "-d x" {
			t.Errorf("%s: stripDynamic rewrote %q to %q", cmd.Name, "-d x", got)
		}
	}
}

// TestGoTypeToolSaysWhereTheDynamicFormLives: stripping a flag silently would
// leave an agent believing it asked for the dynamic type and got it. The
// description is where that is corrected, and it is derived from the registry
// rather than written twice.
func TestGoTypeToolSaysWhereTheDynamicFormLives(t *testing.T) {
	c := &repl.Core{}
	var found bool
	for _, cmd := range c.Commands() {
		if cmd.MCP != "go_type" {
			continue
		}
		found = true
		if !cmd.Static {
			t.Fatal("go_type must stay static: -d is the only thing under :t that runs")
		}
		desc := mcpTool(cmd).Description
		for _, want := range []string{"-d", "only at the gluon prompt", "ignored"} {
			if !strings.Contains(desc, want) {
				t.Errorf("go_type's description is missing %q:\n%s", want, desc)
			}
		}
		if strings.Contains(desc, "This tool runs code") {
			t.Errorf("go_type must not be described as running code:\n%s", desc)
		}
	}
	if !found {
		t.Fatal("no command is exposed as go_type")
	}
}

// TestSessionResetToolIsTheBareCommand: :reset grew -deps, which removes the
// session's modules. session_reset is pinned as clearing the session, and a
// tool that did more than its pin says would make the pin worthless — so the
// tool advertises no argument and the adapter passes none.
func TestSessionResetToolIsTheBareCommand(t *testing.T) {
	c := &repl.Core{}
	var found bool
	for _, cmd := range c.Commands() {
		if cmd.MCP != "session_reset" {
			continue
		}
		found = true
		if !cmd.MCPBare {
			t.Fatal("session_reset must be pinned to the bare command: -deps removes modules")
		}
		raw, ok := mcpTool(cmd).InputSchema.(json.RawMessage)
		if !ok {
			t.Fatalf("session_reset's schema is %T, not raw JSON", mcpTool(cmd).InputSchema)
		}
		schema := string(raw)
		if strings.Contains(schema, "expr") {
			t.Errorf("session_reset advertises an argument: %s", schema)
		}
		// Every tool takes offset and limit, so "no argument" means those two
		// and nothing else — not an empty property set.
		if props := schemaProperties(t, raw); !slices.Equal(props, []string{"limit", "offset"}) {
			t.Errorf("session_reset advertises %v, want only the paging arguments", props)
		}
		desc := mcpTool(cmd).Description
		if !strings.Contains(desc, "only at the gluon prompt") {
			t.Errorf("session_reset's description does not say where -deps lives:\n%s", desc)
		}
	}
	if !found {
		t.Fatal("no command is exposed as session_reset")
	}
}

// TestMCPBareIsOnlyForCommandsThatNeedIt: the field turns off an argument a
// command otherwise takes, so it must never be set on one that has none to
// turn off — that would read as a decision where there was none to make.
func TestMCPBareIsOnlyForCommandsThatNeedIt(t *testing.T) {
	c := &repl.Core{}
	for _, cmd := range c.Commands() {
		if cmd.MCPBare && cmd.Arg == "" {
			t.Errorf("%s: MCPBare on a command that takes no argument does nothing", cmd.Name)
		}
		if cmd.MCPBare && cmd.MCP == "" {
			t.Errorf("%s: MCPBare on a command that is not a tool does nothing", cmd.Name)
		}
	}
}

// schemaProperties reads the property names out of a tool's input schema,
// sorted, so a test can state the whole advertised argument set rather than
// grep for one name and miss what else appeared.
func schemaProperties(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("schema is not an object: %v — %s", err, raw)
	}
	names := slices.Collect(maps.Keys(schema.Properties))
	slices.Sort(names)
	return names
}

// schemaRequired reads a tool schema's required list.
func schemaRequired(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var schema struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("schema is not an object: %v — %s", err, raw)
	}
	return schema.Required
}

// TestEveryToolSchemaTakesPaging: paging is a property of the transport, not of
// any one command, so it is on every schema — the one with an argument, the one
// without, and go_eval's hand-written one. A property that exists on some tools
// is one an agent has to remember which.
func TestEveryToolSchemaTakesPaging(t *testing.T) {
	fixtures := []repl.Command{
		{Name: ":t", Arg: "<exp>", MCP: "go_type", Static: true},
		{Name: ":hist", MCP: "session_history", Static: true},
		{Name: ":reset", Arg: "[-deps]", MCP: "session_reset", MCPBare: true},
	}
	for _, cmd := range fixtures {
		raw, ok := mcpTool(cmd).InputSchema.(json.RawMessage)
		if !ok {
			t.Fatalf("%s: schema is %T, not raw JSON", cmd.Name, mcpTool(cmd).InputSchema)
		}
		props := schemaProperties(t, raw)
		for _, want := range []string{"offset", "limit"} {
			if !slices.Contains(props, want) {
				t.Errorf("%s: schema has no %q property: %v", cmd.Name, want, props)
			}
		}
		for _, req := range schemaRequired(t, raw) {
			if req == "offset" || req == "limit" {
				t.Errorf("%s: %q is required; paging must cost nothing to ignore", cmd.Name, req)
			}
		}
	}
}

// TestPageArgsNamesTheArgumentAtFault: an absent argument is not a zero, and a
// limit of 0 asks for no lines at all. Reading it as "everything" would answer
// a question the client did not ask.
func TestPageArgsNamesTheArgumentAtFault(t *testing.T) {
	ptr := func(n int) *int { return &n }

	if _, _, bad := pageArgs(nil, nil); bad != nil {
		t.Errorf("both absent is the ordinary call, not an error: %s", toolTextOfResult(bad))
	}
	if o, l, bad := pageArgs(ptr(0), ptr(1)); bad != nil || o != 0 || l != 1 {
		t.Errorf("offset 0 limit 1 = (%d, %d, %v), want (0, 1, nil)", o, l, bad)
	}

	for _, tc := range []struct {
		name          string
		offset, limit *int
		want          string
	}{
		{"negative offset", ptr(-1), nil, "offset"},
		{"zero limit", nil, ptr(0), "limit"},
		{"negative limit", nil, ptr(-5), "limit"},
	} {
		_, _, bad := pageArgs(tc.offset, tc.limit)
		if bad == nil {
			t.Errorf("%s was accepted", tc.name)
			continue
		}
		if !bad.IsError {
			t.Errorf("%s: result is not an error", tc.name)
		}
		if text := toolTextOfResult(bad); !strings.Contains(text, tc.want) {
			t.Errorf("%s: %q does not name %q", tc.name, text, tc.want)
		}
	}
}

// TestPageWithoutArgumentsIsByteIdentical is the promise the whole feature
// rests on: a client that never pages sees exactly what it saw before paging
// existed. The bool is the witness — comparing the strings alone would pass if
// page had sliced and rejoined them.
func TestPageWithoutArgumentsIsByteIdentical(t *testing.T) {
	for _, out := range []string{
		"",
		"one line",
		"one\ntwo\nthree",
		"trailing newline\n",
		"\n\nblank lines\n\n",
	} {
		got, paged := page(out, 0, 0)
		if paged {
			t.Errorf("page(%q, 0, 0) reported paging", out)
		}
		if got != out {
			t.Errorf("page(%q, 0, 0) = %q, want it untouched", out, got)
		}
	}
}

// TestPageSlicesByLine covers the four shapes a page comes in: the first, one
// in the middle, a last one that runs short, and an offset past the end — which
// is the answer to "is there more", not an error.
func TestPageSlicesByLine(t *testing.T) {
	lines := make([]string, 300)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	long := strings.Join(lines, "\n")

	for _, tc := range []struct {
		name          string
		offset, limit int
		wantFirst     string
		wantLast      string
		wantRange     string
	}{
		{"first page", 0, 40, "line 1", "line 40", "lines 1–40 of 300"},
		{"middle page", 100, 40, "line 101", "line 140", "lines 101–140 of 300"},
		{"last partial page", 280, 40, "line 281", "line 300", "lines 281–300 of 300"},
		{"offset only", 295, 0, "line 296", "line 300", "lines 296–300 of 300"},
		{"limit past the end", 0, 1000, "line 1", "line 300", "lines 1–300 of 300"},
	} {
		got, paged := page(long, tc.offset, tc.limit)
		if !paged {
			t.Errorf("%s: page reported no paging", tc.name)
		}
		body, gotRange, found := strings.Cut(got, "\n\nlines ")
		if !found {
			t.Errorf("%s: no range line in:\n%s", tc.name, got)
			continue
		}
		if gotRange = "lines " + gotRange; gotRange != tc.wantRange {
			t.Errorf("%s: range line %q, want %q", tc.name, gotRange, tc.wantRange)
		}
		bodyLines := strings.Split(body, "\n")
		if bodyLines[0] != tc.wantFirst {
			t.Errorf("%s: first line %q, want %q", tc.name, bodyLines[0], tc.wantFirst)
		}
		if last := bodyLines[len(bodyLines)-1]; last != tc.wantLast {
			t.Errorf("%s: last line %q, want %q", tc.name, last, tc.wantLast)
		}
	}
}

// TestPagePastTheEndStatesTheTotal: an offset at or beyond the end returns no
// lines and is not an error — the total is how a client learns it has read
// everything.
func TestPagePastTheEndStatesTheTotal(t *testing.T) {
	for _, tc := range []struct {
		out           string
		offset, limit int
		want          string
	}{
		{"a\nb\nc", 3, 10, "no lines at offset 3 — 3 lines total"},
		{"a\nb\nc", 99, 0, "no lines at offset 99 — 3 lines total"},
		{"", 0, 10, "no lines at offset 0 — 0 lines total"},
	} {
		got, paged := page(tc.out, tc.offset, tc.limit)
		if !paged {
			t.Errorf("page(%q, %d, %d) reported no paging", tc.out, tc.offset, tc.limit)
		}
		if got != tc.want {
			t.Errorf("page(%q, %d, %d) = %q, want %q", tc.out, tc.offset, tc.limit, got, tc.want)
		}
	}
}

// toolTextOfResult reads the text out of a tool result.
func toolTextOfResult(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// toolNames lists what a server actually serves, sorted. The registry decides
// the tier and TestMCPTierIsIntentional pins it; this asks the server itself,
// which is the end a client sees.
func toolNames(t *testing.T, allowEval bool) []string {
	t.Helper()
	ctx := context.Background()
	serverT, clientT := mcp.NewInMemoryTransports()

	// The tool surface is built from the command registry alone, so a Core
	// with no evaluator answers this — which is why it is a unit test.
	ss, err := mcpServer(&repl.Core{}, "", allowEval).Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { ss.Close() })

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })

	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

// staticTools is the whole surface of a server started without --eval. It is
// written out rather than derived, because a set derived from the registry
// would agree with the registry however the registry changed — and the point
// of this list is that a command joining the static tier is a decision someone
// made here, in a diff a reviewer reads.
var staticTools = []string{
	"go_cast", "go_doc", "go_embeds", "go_generics", "go_implements",
	"go_interface", "go_layout", "go_methods", "go_mock", "go_satisfies",
	"go_scope", "go_spy", "go_type", "session_history", "session_source",
}

// evalTools is what --eval adds: the inspectors that build or run something,
// and go_eval itself.
var evalTools = []string{
	"go_asm", "go_bench", "go_diff", "go_err_chain", "go_escape", "go_eval",
	"go_inline", "go_memprof", "go_profile", "go_race", "go_since",
	"go_slice_headers", "go_test", "go_trace", "go_vet",
	"session_refresh", "session_reset", "session_undo",
}

// TestStaticSurfaceIsExactlyTheStaticTier: without --eval, nothing that builds
// or runs a program is served. An extra name here is invariant 28 broken, and
// a missing one is a tool an agent was promised.
func TestStaticSurfaceIsExactlyTheStaticTier(t *testing.T) {
	got := toolNames(t, false)
	if !slices.Equal(got, staticTools) {
		t.Errorf("a server without --eval serves:\n%v\nwant:\n%v", got, staticTools)
	}
}

// TestEvalSurfaceAddsTheEvaluators: --eval adds exactly the evaluating tools
// and takes nothing away.
func TestEvalSurfaceAddsTheEvaluators(t *testing.T) {
	want := slices.Concat(staticTools, evalTools)
	slices.Sort(want)

	got := toolNames(t, true)
	if !slices.Equal(got, want) {
		t.Errorf("a server with --eval serves:\n%v\nwant:\n%v", got, want)
	}
}

// TestExcludedCommandsReachNoClient is the other half of the tier decision,
// checked where it matters: at the server. :http, :query, :env and :conf are
// excluded on their own grounds, and the rest re-point what the server is
// attached to — which would make the initialize instructions a lie. :share is
// excluded on grounds of its own that are not a tier at all: --eval's
// documented meaning is about running code, and an agent does not own the
// decision to put the user's session on a public URL that cannot be withdrawn.
func TestExcludedCommandsReachNoClient(t *testing.T) {
	served := toolNames(t, true)
	for _, cmd := range (&repl.Core{}).Commands() {
		switch cmd.Name {
		case ":http", ":share", ":query", ":env", ":conf", ":use", ":get", ":db", ":reload", ":watch":
		default:
			continue
		}
		if cmd.MCP != "" && slices.Contains(served, cmd.MCP) {
			t.Errorf("%s is served as %q even under --eval", cmd.Name, cmd.MCP)
		}
	}
}

// TestInstructionsNameTheToolsTheyPromise: an agent reads the instruction text
// once, at initialize, and takes it for the tool list. A name that appears
// there and nowhere else is a tool an agent will call and not find.
func TestInstructionsNameTheToolsTheyPromise(t *testing.T) {
	withEval := mcpInstructions("", true)
	for _, name := range slices.Concat(staticTools, evalTools) {
		if !strings.Contains(withEval, name) {
			t.Errorf("the --eval instructions do not name %s", name)
		}
	}

	// Without --eval the evaluating tools are not there to name, and naming
	// them would send an agent after a tool the server does not serve.
	withoutEval := mcpInstructions("", false)
	for _, name := range staticTools {
		if !strings.Contains(withoutEval, name) {
			t.Errorf("the static instructions do not name %s", name)
		}
	}
	for _, name := range evalTools {
		if name == "go_eval" || name == "go_bench" {
			// Named as what --eval would enable, which is the flag's own help.
			continue
		}
		if strings.Contains(withoutEval, name) {
			t.Errorf("the static instructions name %s, which that server does not serve", name)
		}
	}
}

// TestInstructionsSayEveryToolPages: paging is on every schema, and an agent
// that does not know it exists will keep asking for answers it cannot hold.
func TestInstructionsSayEveryToolPages(t *testing.T) {
	for _, allowEval := range []bool{false, true} {
		text := mcpInstructions("", allowEval)
		for _, want := range []string{"offset", "limit"} {
			if !strings.Contains(text, want) {
				t.Errorf("instructions (eval=%v) do not mention %q:\n%s", allowEval, want, text)
			}
		}
	}
}

// TestToolDescriptionsCarryFlagsAndExamples: an agent writes expr from the
// description alone, so the flags and the lines that use them are in it, from
// the same declaration :help prints.
func TestToolDescriptionsCarryFlagsAndExamples(t *testing.T) {
	c := &repl.Core{}
	for _, cmd := range c.Commands() {
		if cmd.MCP != "go_doc" {
			continue
		}
		desc := mcpTool(cmd).Description
		for _, want := range []string{
			"expr reads: [-src | -examples | -url | -pkg] <name>",
			"  -src  ", "  -examples  ", "  -url  ", "  -pkg  ",
			"Examples (expr):", `"strings.Cut"`,
		} {
			if !strings.Contains(desc, want) {
				t.Errorf("go_doc's description is missing %q:\n%s", want, desc)
			}
		}
		return
	}
	t.Fatal("no command is exposed as go_doc")
}

// TestNoHiddenFlagReachesATool: a hidden flag is a confirmation a view submits
// on the reader's behalf. In a tool description it would be an invitation to
// skip the question.
func TestNoHiddenFlagReachesATool(t *testing.T) {
	for _, cmd := range repl.Reference() {
		if cmd.MCP == "" {
			continue
		}
		desc := mcpTool(cmd).Description
		for _, f := range cmd.Usage.Flags {
			if f.Hidden && strings.Contains(desc, f.Name) {
				t.Errorf("%s's description shows the hidden %s", cmd.MCP, f.Name)
			}
		}
	}
}

// TestBareToolsListNoFlags: a tool pinned to its command's bare form takes no
// argument, so a flag in its description would be one it cannot pass.
func TestBareToolsListNoFlags(t *testing.T) {
	for _, cmd := range repl.Reference() {
		if !cmd.MCPBare {
			continue
		}
		desc := mcpTool(cmd).Description
		if strings.Contains(desc, "Flags, written into expr") || strings.Contains(desc, "Examples (expr)") {
			t.Errorf("%s is bare and its description lists usage:\n%s", cmd.MCP, desc)
		}
	}
}

// TestStaticToolsStripEveryFlagThatRuns guards invariant 28 from the other
// side: a static tool never builds, and stripDynamic is the only thing that
// takes a running flag off its argument — it knows -d and nothing else. So a
// static command may declare no other flag that runs, and one that declares -d
// must spell it in its Arg, where stripDynamic looks.
func TestStaticToolsStripEveryFlagThatRuns(t *testing.T) {
	for _, cmd := range repl.Reference() {
		if !cmd.Static {
			continue
		}
		for _, f := range cmd.Usage.Flags {
			if !f.Runs {
				continue
			}
			if f.Name != "-d" {
				t.Errorf("%s is static and declares %s, which runs code and which stripDynamic "+
					"does not know", cmd.Name, f.Name)
			}
			if !strings.Contains(cmd.Arg, "-d") {
				t.Errorf("%s declares -d and its Arg %q does not spell it, so stripDynamic "+
					"never engages", cmd.Name, cmd.Arg)
			}
		}
	}
}
