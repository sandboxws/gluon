package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/repl"
)

// newMCPCmd serves gluon's inspectors over MCP, so a coding agent measures
// instead of guessing: :t, :m, :ls, :layout and :doc answer from go/types in
// about a millisecond, and :bench measures rather than estimates.
//
// Two tiers, decided by each command's own registry entry. The static tier —
// tools that never build or run anything — is always exposed and is safe to
// point at any module. Everything that runs code, go_eval included, exists
// only behind --eval: it runs arbitrary code with the caller's permissions,
// and granting that belongs to the human who edits the client config, the
// same way :get is the asking for the network.
//
// Session state is per-process here, and the claim is stronger than it looks:
// gluon does have a durable session now — a scratchpad — and this server opens
// none. A pad is installed by a driver, runTUI is the only production caller
// that installs one, and there is no code path from a tool call to OpenPad. So
// an agent can neither inherit the scratchpad the user has open nor change it,
// and that holds by construction rather than by a check somebody maintains.
// The server lives as long as the client connection; session_reset starts over;
// nothing persists.
func newMCPCmd() *cobra.Command {
	var (
		hostDir   string
		allowEval bool
	)
	cmd := &cobra.Command{
		Use:     "mcp",
		Short:   "serve the session's inspectors to a coding agent (MCP over stdio)",
		GroupID: groupDiagnose,
		Long: "gluon mcp speaks the Model Context Protocol over stdio. The inspecting\n" +
			"commands become tools: go_type, go_methods, go_scope, go_layout, go_doc, and\n" +
			"the interface questions — go_implements, go_satisfies, go_cast, go_interface,\n" +
			"go_embeds, go_generics — answered from go/types in about a millisecond, by the\n" +
			"same toolchain that builds your code.\n\n" +
			"When the working directory is inside a Go module, the session attaches to\n" +
			"it — internal/ packages included — because installing the server in a\n" +
			"project's MCP config is the asking. -host picks a different module; -host\n" +
			"off serves a standalone session.\n\n" +
			"Tools that run code (go_eval, go_bench, plugin tools, session_reset) are\n" +
			"exposed only with --eval. Evaluation replays the whole session each call,\n" +
			"so side effects repeat — the tool descriptions say so, to the one reader\n" +
			"who never skims.",
		Example: "# .mcp.json\n" +
			`{"mcpServers": {"gluon": {"command": "gluon", "args": ["mcp"]}}}` + "\n" +
			"# with evaluation enabled\n" +
			`{"mcpServers": {"gluon": {"command": "gluon", "args": ["mcp", "--eval"]}}}`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return status(runMCP(hostDir, allowEval))
		},
	}
	f := cmd.Flags()
	f.StringVar(&hostDir, "host", "",
		"module to attach to (default: the module containing the working directory; off: none)")
	f.BoolVar(&allowEval, "eval", false,
		"also expose the tools that run code: go_eval, go_bench, the profilers, go_diff, go_test, plugin tools, session_reset")
	return cmd
}

func runMCP(hostDir string, allowEval bool) int {
	core, err := repl.NewCore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gluon: %v\n", err)
		return 2
	}
	defer core.Close()

	// Attach to the module the client is working in. Unlike the REPL's
	// opt-in -host, writing the server into a project's MCP config is itself
	// the asking — and the instructions say which module answered, so the
	// attachment stays visible where the caller reads.
	attached := ""
	switch hostDir {
	case "off":
		// standalone by request
	case "":
		if wd, werr := os.Getwd(); werr == nil {
			if msg, aerr := core.Attach(wd); aerr == nil {
				attached = msg
			}
			// No module around the working directory is the standalone
			// session, not an error.
		}
	default:
		msg, aerr := core.Attach(hostDir)
		if aerr != nil {
			fmt.Fprintf(os.Stderr, "gluon: %v\n", aerr)
			return 3
		}
		attached = msg
	}

	server := mcpServer(core, attached, allowEval)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		// A client hanging up is how every stdio session ends, not a failure.
		if errors.Is(err, io.EOF) {
			return 0
		}
		fmt.Fprintf(os.Stderr, "gluon: mcp: %v\n", err)
		return 1
	}
	return 0
}

// mcpServer wires a Core's commands up as tools. It is separate from runMCP so
// the tool surface can be inspected without a stdio transport: which tools a
// server without --eval exposes is the safety line this whole command turns on,
// and a test has to be able to ask.
func mcpServer(core *repl.Core, attached string, allowEval bool) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: "gluon", Version: resolveVersion()},
		&mcp.ServerOptions{Instructions: mcpInstructions(attached, allowEval)},
	)

	// Core is not safe for concurrent use — the REPL runs one Submit at a
	// time, and this mutex is the same promise kept for tool calls.
	var mu sync.Mutex

	for _, c := range core.Commands() {
		if c.MCP == "" || (!c.Static && !allowEval) {
			continue
		}
		cmd := c
		server.AddTool(mcpTool(cmd), func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args struct {
				Expr string `json:"expr"`
				// Pointers, so an absent argument is distinguishable from a
				// zero one: an explicit limit of 0 asks for nothing, which is
				// worth naming rather than reading as "everything".
				Offset *int `json:"offset"`
				Limit  *int `json:"limit"`
			}
			if len(req.Params.Arguments) > 0 {
				if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
					return toolError("arguments must be {\"expr\": string} with optional \"offset\" and \"limit\": " + err.Error()), nil
				}
			}
			offset, limit, bad := pageArgs(args.Offset, args.Limit)
			if bad != nil {
				return bad, nil
			}
			expr := stripDynamic(cmd, args.Expr)
			if cmd.MCPBare {
				// The tool is the command's bare form. mcpTool advertises no
				// argument for it, so anything here was not asked for.
				expr = ""
			}
			if expr == "" && strings.Contains(cmd.Arg, "<") {
				return toolError("this tool requires expr — its argument reads: " + cmd.Arg), nil
			}
			mu.Lock()
			res := core.Submit(strings.TrimSpace(cmd.Name + " " + expr))
			mu.Unlock()
			if res.Err {
				// An error is never paged. A client that asked for one line of
				// a failure would get the first line of the reason and be left
				// to guess the rest.
				return toolError(res.Out), nil
			}
			text, _ := page(res.Out, offset, limit)
			return toolText(text), nil
		})
	}

	if allowEval {
		server.AddTool(goEvalTool(), func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args struct {
				Code   string `json:"code"`
				Offset *int   `json:"offset"`
				Limit  *int   `json:"limit"`
			}
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil || strings.TrimSpace(args.Code) == "" {
				return toolError(`arguments must be {"code": string} with optional "offset" and "limit"`), nil
			}
			offset, limit, bad := pageArgs(args.Offset, args.Limit)
			if bad != nil {
				return bad, nil
			}
			mu.Lock()
			res := core.SubmitScript(args.Code)
			mu.Unlock()
			if res.Err {
				return toolError(res.Out), nil
			}
			if res.Out == "" {
				// Nothing to page: the declaration succeeded and said so, which
				// is the answer whatever window the client has.
				return toolText("ok — declared, nothing to print"), nil
			}
			text, _ := page(res.Out, offset, limit)
			return toolText(text), nil
		})
	}

	return server
}

// stripDynamic drops a leading -d from a static tool's argument.
//
// :t -d evaluates, and a static tool never evaluates (invariant 28), so the
// flag cannot be honoured here. Stripping it rather than erroring is for the
// agent that copied an invocation out of a REPL transcript: the static answer
// is the one this tool has always given, and the description says the dynamic
// form is at the prompt. Only a static command is touched — an evaluating tool
// has no such flag to strip.
func stripDynamic(c repl.Command, expr string) string {
	if !c.Static || !strings.Contains(c.Arg, "-d") {
		return expr
	}
	expr = strings.TrimSpace(expr)
	if expr == "-d" {
		return ""
	}
	if rest, ok := strings.CutPrefix(expr, "-d "); ok {
		return strings.TrimSpace(rest)
	}
	return expr
}

// mcpTool derives a tool definition from a registry command — the registry
// already carries a name, an argument spec and a summary, which is all a tool
// definition is.
func mcpTool(c repl.Command) *mcp.Tool {
	desc := c.Summary
	if c.Detail != "" {
		desc += "\n\n" + c.Detail
	}
	if u := usageText(c); u != "" {
		desc += "\n\n" + u
	}
	if !c.Static {
		desc += "\n\nThis tool runs code by replaying the session; side effects repeat."
	}
	if c.MCPBare && c.Arg != "" {
		// The command takes a flag this tool does not: say so, rather than
		// leave an agent to infer it from a schema with no argument in it.
		desc += "\n\nThis tool is the bare `" + c.Name + "`. Its argument form is available only at " +
			"the gluon prompt."
	}
	if c.Static && strings.Contains(c.Arg, "-d") {
		desc += "\n\nThe -d form runs the expression and is available only at the gluon prompt. " +
			"This tool is static: a leading -d is ignored and the static answer is returned."
	}
	if c.Arg == "" || c.MCPBare {
		return &mcp.Tool{
			Name:        c.MCP,
			Description: desc,
			InputSchema: withPaging(json.RawMessage(`{"type": "object", "properties": {}}`)),
		}
	}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"expr": map[string]any{
				"type":        "string",
				"description": "the argument line, exactly as the REPL command takes it: " + c.Arg,
			},
		},
	}
	if strings.Contains(c.Arg, "<") {
		schema["required"] = []string{"expr"}
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		// A map of strings cannot fail to marshal; this is unreachable.
		panic(err)
	}
	return &mcp.Tool{Name: c.MCP, Description: desc, InputSchema: withPaging(raw)}
}

// goEvalTool is go_eval's definition — the one tool that is not a command. It
// is a function so the server and the generated MCP reference read the same
// value.
func goEvalTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "go_eval",
		Description: "Evaluate Go code in the persistent session, compiled by the real Go " +
			"toolchain — the semantics are the compiler's. Multi-line constructs are fine. " +
			"The whole session replays on every call, so side effects (HTTP calls, file " +
			"writes, prints) repeat on later calls; the replayed output is suppressed, " +
			"yours is not. Bindings persist: x := 1 now, x usable in every later call.",
		InputSchema: withPaging(json.RawMessage(`{
			"type": "object",
			"properties": {
				"code": {"type": "string", "description": "Go code: expressions, statements, declarations. The trailing expression's value is printed with its type."}
			},
			"required": ["code"]
		}`)),
	}
}

// usageText is what an agent needs to write expr: the synopsis without the
// command's name, the visible flags, and the command's own examples written as
// expr values. It is the same declaration :help prints from, so a tool cannot
// describe a flag the prompt does not read.
//
// A tool pinned to its command's bare form gets none of it — it takes no
// argument, whatever the command does (invariant 28's spirit). A static tool
// marks a flag that runs code as the prompt's alone, and leaves out an example
// that uses one: stripDynamic would quietly answer a different question.
func usageText(c repl.Command) string {
	if c.MCPBare || c.Arg == "" {
		return ""
	}
	sp := c.Usage
	var b strings.Builder
	b.WriteString("expr reads: " + strings.TrimSpace(strings.TrimPrefix(sp.Line(c.Name, c.Arg), c.Name)))
	if fs := sp.Visible(); len(fs) > 0 {
		b.WriteString("\n\nFlags, written into expr as at the prompt:")
		for _, f := range fs {
			name := f.Name
			if f.Value != "" {
				name += " <" + f.Value + ">"
			}
			help := f.Help
			if f.Runs && c.Static {
				help += " (only at the gluon prompt; this tool ignores it)"
			}
			b.WriteString("\n  " + name + "  " + help)
		}
	}
	var examples []string
	for _, ex := range sp.Examples {
		_, arg, _ := strings.Cut(strings.TrimSpace(ex.Line), " ")
		arg = strings.TrimSpace(arg)
		if arg == "" || (c.Static && runsFlagIn(sp, arg)) {
			continue
		}
		line := strconv.Quote(arg)
		if ex.Says != "" {
			line += " — " + ex.Says
		}
		examples = append(examples, line)
	}
	if len(examples) > 0 {
		b.WriteString("\n\nExamples (expr):\n  " + strings.Join(examples, "\n  "))
	}
	return b.String()
}

// runsFlagIn reports whether an argument uses a flag that builds or runs.
func runsFlagIn(sp cmdspec.Spec, arg string) bool {
	for _, w := range strings.Fields(arg) {
		if f, ok := sp.Flag(w); ok && f.Runs {
			return true
		}
	}
	return false
}

// withPaging adds offset and limit to a tool schema, never to its required
// list.
//
// Every tool gets them, including the ones that take no argument at all: a
// property that exists on some tools is one an agent has to remember, and
// session_history is as able to overflow a window as go_doc is.
func withPaging(raw json.RawMessage) json.RawMessage {
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		// Every schema reaching here is built in this file.
		panic(err)
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		props = map[string]any{}
		schema["properties"] = props
	}
	props["offset"] = map[string]any{
		"type":        "integer",
		"minimum":     0,
		"description": "lines of the answer to skip before the first one returned. Omit for the whole answer.",
	}
	props["limit"] = map[string]any{
		"type":        "integer",
		"minimum":     1,
		"description": "how many lines to return, counted from offset. Omit for the whole answer.",
	}
	out, err := json.Marshal(schema)
	if err != nil {
		panic(err)
	}
	return out
}

// pageArgs validates the two paging arguments and flattens them to the ints
// page takes, or returns the tool error that names the one at fault.
//
// An absent argument is not a zero. A limit of 0 asks for no lines at all,
// which no client means; reading it as "everything" would answer a question
// that was not asked.
func pageArgs(offset, limit *int) (int, int, *mcp.CallToolResult) {
	o, l := 0, 0
	if offset != nil {
		if *offset < 0 {
			return 0, 0, toolError("offset must be 0 or greater")
		}
		o = *offset
	}
	if limit != nil {
		if *limit < 1 {
			return 0, 0, toolError("limit must be 1 or greater")
		}
		l = *limit
	}
	return o, l, nil
}

// page returns the lines of out that offset and limit ask for, and reports
// whether it sliced anything.
//
// A limit of 0 means no limit; pageArgs rejects an explicit 0, so 0 reaches
// here only from an absent argument. With neither argument the answer comes
// back untouched and ok is false — which is what keeps a client that never
// pages seeing the bytes it saw before paging existed, and is what the tests
// assert rather than comparing two strings and hoping.
//
// It slices Result.Out, which invariant 19 guarantees carries the linear form
// of every answer, modal or not. So there is nothing the TUI shows that this
// can cut in half.
//
// An offset past the end is not an error: it is the answer to "is there more",
// and the total says how much there was.
func page(out string, offset, limit int) (string, bool) {
	if offset == 0 && limit == 0 {
		return out, false
	}
	var lines []string
	if out != "" {
		// A trailing newline is a line terminator, not an empty last line, and
		// counting it would report one more line than a client can ask for.
		lines = strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	}
	total := len(lines)
	if offset >= total {
		return fmt.Sprintf("no lines at offset %d — %d lines total", offset, total), true
	}
	end := total
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	body := strings.Join(lines[offset:end], "\n")
	return fmt.Sprintf("%s\n\nlines %d–%d of %d", body, offset+1, end, total), true
}

func toolText(text string) *mcp.CallToolResult {
	if text == "" {
		text = "(no output)"
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func toolError(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
		IsError: true,
	}
}

func mcpInstructions(attached string, allowEval bool) string {
	var b strings.Builder
	b.WriteString("gluon answers questions about Go code from go/types, using the same " +
		"toolchain that builds it — measured, not guessed. Static tools (go_type, " +
		"go_methods, go_scope, go_layout, go_doc, session_source, session_history, " +
		"the interface questions go_implements, go_satisfies, go_cast, go_interface, " +
		"go_embeds, go_generics, and the test doubles go_mock and go_spy) answer in " +
		"about a millisecond and never run anything.\n")
	b.WriteString("\nEvery tool takes optional offset and limit, counted in lines of the " +
		"answer: ask for what your window holds and the reply says which lines it " +
		"gave and how many there are. Omit both for the whole answer.\n")
	if attached != "" {
		b.WriteString("\nThis session is " + attached + "\n")
		b.WriteString("Bare qualifiers resolve against that module, internal/ packages included.\n")
	} else {
		b.WriteString("\nThis session is standalone: stdlib and whatever go_eval declares.\n")
	}
	if allowEval {
		b.WriteString("\nEvaluation is enabled. go_eval compiles against the real toolchain " +
			"(~300ms a call); go_bench measures through testing.B and takes seconds, as " +
			"go_profile, go_memprof and go_trace do. go_diff compares two values from one " +
			"run, go_test writes the session's judgement down as a Go test, and " +
			"session_refresh runs the pinned entries once more. go_err_chain, go_escape, " +
			"go_slice_headers and session_undo evaluate too. The compiler's own verdicts " +
			"come from go_inline (what was flattened, and why not), go_asm (one declared " +
			"function's listing) and go_vet (the toolchain's analysers); go_race runs the " +
			"expression once under the race detector, whose first call on a machine " +
			"compiles the instrumented standard library and takes seconds. go_since is " +
			"the odd one here: it reads what each Go release added out of $GOROOT and " +
			"costs nothing, and is in this tier only because its -run flag evaluates a " +
			"language note into the session. The session " +
			"replays wholesale on every evaluating call, so side effects repeat; " +
			"session_reset starts over.\n")
	} else {
		b.WriteString("\nEvaluation is disabled — this server only inspects. Start it with " +
			"--eval to enable go_eval and go_bench.\n")
	}
	return b.String()
}
