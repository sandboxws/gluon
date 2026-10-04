package repl

import (
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/plugin"
	"github.com/sandboxws/gluon/internal/plugins"
)

// The registry exists so that dispatch, help and completion cannot disagree.
// These are the tests that say so — without them the refactor only moved the
// four lists into one file rather than making them one list.

func testCore(t *testing.T) *Core {
	t.Helper()
	c, err := NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// TestEveryCommandDispatches: a command in the registry, and every alias it
// declares, resolves to itself.
func TestEveryCommandDispatches(t *testing.T) {
	c := &Core{}
	for _, cmd := range c.Commands() {
		got, ok := c.lookup(cmd.Name)
		if !ok || got.Name != cmd.Name {
			t.Errorf("%s does not dispatch to itself", cmd.Name)
		}
		for _, a := range cmd.Aliases {
			got, ok := c.lookup(a)
			if !ok || got.Name != cmd.Name {
				t.Errorf("alias %s does not dispatch to %s", a, cmd.Name)
			}
		}
	}
}

// TestEveryCommandIsInHelp. A command missing from help is undiscoverable,
// which is how :inspect would have shipped invisible before the registry.
func TestEveryCommandIsInHelp(t *testing.T) {
	c := &Core{}
	help := c.helpAll()
	for _, cmd := range c.Commands() {
		if !strings.Contains(help, cmd.Name) {
			t.Errorf("%s is not in :help", cmd.Name)
		}
	}
}

// TestEveryCommandIsOffered. A command missing from the completion list is
// simply never suggested — the silent failure the old parallel slice invited.
func TestEveryCommandIsOffered(t *testing.T) {
	c := &Core{}
	offered := map[string]bool{}
	for _, n := range c.MetaNames() {
		offered[n] = true
	}
	for _, cmd := range c.Commands() {
		if !offered[cmd.Name] {
			t.Errorf("%s is not offered for completion", cmd.Name)
		}
	}
}

// TestNamesAreUnique catches the copy-paste that makes one command shadow
// another. lookup takes the first match, so the loser would be unreachable.
func TestNamesAreUnique(t *testing.T) {
	c := &Core{}
	seen := map[string]string{}
	for _, cmd := range c.Commands() {
		for _, n := range append([]string{cmd.Name}, cmd.Aliases...) {
			if prev, dup := seen[n]; dup {
				t.Errorf("%s is claimed by both %s and %s", n, prev, cmd.Name)
			}
			seen[n] = cmd.Name
		}
	}
}

// TestNoBuiltinShadowsAPluginCommand.
//
// lookup takes the first match and builtins come first, so a builtin sharing a
// name with a plugin's command makes the plugin's unreachable — silently, and
// only in a session that actually has the library, which is the session least
// likely to be the one under test.
//
// TestNamesAreUnique cannot see this: it runs on a Core with no plugins loaded,
// so c.extra is empty and only builtins are compared against each other. This
// is the check that would have caught :settings being called :config, which is
// viper's and koanf's.
func TestNoBuiltinShadowsAPluginCommand(t *testing.T) {
	c := &Core{}
	for _, p := range plugins.Builtin() {
		cmder, ok := p.(plugin.Commander)
		if !ok {
			continue
		}
		for _, cmd := range cmder.Commands() {
			if got, taken := c.lookup(cmd.Name); taken {
				t.Errorf("%s is a builtin (%q) and the %s plugin's — the plugin's is unreachable",
					cmd.Name, got.Summary, p.Meta().Name)
			}
		}
	}
}

// TestCommandsAreWellFormed pins the shape help depends on.
func TestCommandsAreWellFormed(t *testing.T) {
	c := &Core{}
	for _, cmd := range c.Commands() {
		if !strings.HasPrefix(cmd.Name, ":") {
			t.Errorf("%s does not start with a colon", cmd.Name)
		}
		if cmd.Summary == "" {
			t.Errorf("%s has no summary, so :help would show a blank line", cmd.Name)
		}
		if cmd.Group == "" {
			t.Errorf("%s has no group", cmd.Name)
		}
		if cmd.Run == nil {
			t.Errorf("%s has no handler", cmd.Name)
		}
		// The help columns line up only while the widest entry fits.
		width := len(cmd.Name)
		if cmd.Arg != "" {
			width += 1 + len(cmd.Arg)
		}
		if width > helpWidth {
			t.Errorf("%s %s is %d wide, past the %d-column help gutter",
				cmd.Name, cmd.Arg, width, helpWidth)
		}
	}
}

// TestRequiredArgumentsReportUsage replaces the hand-maintained list that only
// ever covered :layout and :bench. A command whose Arg names a required
// operand must say so rather than failing obscurely.
func TestRequiredArgumentsReportUsage(t *testing.T) {
	c := testCore(t)
	for _, cmd := range c.Commands() {
		if !strings.Contains(cmd.Arg, "<") {
			continue // optional, or none
		}
		res := c.Submit(cmd.Name)
		if !res.Err || !strings.Contains(res.Out, "usage:") {
			t.Errorf("%s with no argument: got %q (err=%v), want a usage line",
				cmd.Name, res.Out, res.Err)
		}
	}
}

// TestHelpForOneCommand covers the `:help <command>` path, including the
// courtesy of accepting the name without its colon.
func TestHelpForOneCommand(t *testing.T) {
	c := &Core{}
	for _, arg := range []string{":slice", "slice"} {
		res := c.help(arg)
		if res.Err {
			t.Fatalf("help(%q) errored: %s", arg, res.Out)
		}
		if !strings.Contains(res.Out, "backing array") {
			t.Errorf("help(%q) did not render the summary:\n%s", arg, res.Out)
		}
	}
	if res := c.help(":nope"); !res.Err {
		t.Error("help for an unknown command should be an error")
	}
}

// TestGroupsAreOrdered keeps :help from listing a group gluon knows about in
// whatever order the map iterated.
func TestGroupsAreOrdered(t *testing.T) {
	c := &Core{}
	help := c.helpAll()
	last := -1
	for _, g := range groupOrder {
		i := strings.Index(help, g)
		if i < 0 {
			continue
		}
		if i < last {
			t.Errorf("group %q appears out of declared order", g)
		}
		last = i
	}
}

// TestMCPTierIsIntentional pins which commands are MCP tools and which tier
// each belongs to. The static tier is the safety line — a static tool never
// builds or runs anything, so `gluon mcp` exposes it without --eval — and a
// command joining it must be a deliberate act here, not a copied field.
func TestMCPTierIsIntentional(t *testing.T) {
	wantStatic := map[string]string{
		":t":      "go_type",
		":m":      "go_methods",
		":impl":   "go_implements",
		":sat":    "go_satisfies",
		":cast":   "go_cast",
		":iface":  "go_interface",
		":embeds": "go_embeds",
		":gen":    "go_generics",
		":ls":     "go_scope",
		":layout": "go_layout",
		":doc":    "go_doc",
		":src":    "session_source",
		":mock":   "go_mock",
		":spy":    "go_spy",
		":hist":   "session_history",
	}
	wantEval := map[string]string{
		":slice": "go_slice_headers",
		":err":   "go_err_chain",
		":bench": "go_bench",
		":esc":   "go_escape",
		// All four build or run something. Invariant 28 draws the static line
		// at "never builds or runs", and a build is a build.
		":inline":  "go_inline",
		":asm":     "go_asm",
		":vet":     "go_vet",
		":race":    "go_race",
		":undo":    "session_undo",
		":reset":   "session_reset",
		":diff":    "go_diff",
		":profile": "go_profile",
		":memprof": "go_memprof",
		":trace":   "go_trace",
		":test":    "go_test",
		":refresh": "session_refresh",
		// :since reads two files out of $GOROOT and would be static but for
		// -run, which evaluates a snippet into the session. Invariant 28 puts
		// the tier on the command, not on the argument, so the whole command
		// is eval-tier: an MCP client without --eval reads no release notes.
		// Splitting it into a browsing half and a running half to recover that
		// would be two commands where one flag does — the trade ROADMAP's
		// :since entry records — and a tier that depended on which flag was
		// passed would be exactly the per-argument decision the invariant
		// exists to prevent.
		":since": "go_since",
	}

	seen := map[string]bool{}
	for _, cmd := range builtins {
		if cmd.MCP == "" {
			if cmd.Static {
				t.Errorf("%s: Static without an MCP name does nothing", cmd.Name)
			}
			continue
		}
		seen[cmd.Name] = true
		if name, ok := wantStatic[cmd.Name]; ok {
			if !cmd.Static {
				t.Errorf("%s: expected static tier", cmd.Name)
			}
			if cmd.MCP != name {
				t.Errorf("%s: MCP name %q, want %q", cmd.Name, cmd.MCP, name)
			}
			continue
		}
		if name, ok := wantEval[cmd.Name]; ok {
			if cmd.Static {
				t.Errorf("%s runs code; it must not be in the static tier", cmd.Name)
			}
			if cmd.MCP != name {
				t.Errorf("%s: MCP name %q, want %q", cmd.Name, cmd.MCP, name)
			}
			continue
		}
		t.Errorf("%s: exposed as MCP tool %q but not in this pin — decide its tier here", cmd.Name, cmd.MCP)
	}
	for name := range wantStatic {
		if !seen[name] {
			t.Errorf("%s: pinned as a static tool but not exposed", name)
		}
	}
	for name := range wantEval {
		if !seen[name] {
			t.Errorf("%s: pinned as an eval tool but not exposed", name)
		}
	}
}

// TestExcludedCommandsHaveNoMCPName pins the other half of the tier decision:
// commands deliberately kept off the tool surface entirely, in either tier.
//
// :http reaches the network and :query reaches a database that outlives the
// session — both make the server act on an agent's choice with the user's
// credentials. :env and :conf hand over the environment gluon was started in,
// and redaction is by shape, which :env's own help says is not a guarantee.
// The rest re-point what the server is attached to, which would make the
// initialize instructions a lie. A name added to any of them is a decision to
// be made here, not a field copied from the entry above.
func TestExcludedCommandsHaveNoMCPName(t *testing.T) {
	excluded := map[string]string{
		":http":   "makes requests from the user's machine with the user's credentials",
		":share":  "publishes the user's code to a public URL that cannot be withdrawn",
		":query":  "reaches a database that outlives the session",
		":env":    "hands over the environment gluon was started in",
		":conf":   "hands over the project's configuration",
		":use":    "changes what the server is attached to",
		":get":    "changes what the server is attached to",
		":db":     "changes what the server is attached to",
		":reload": "changes what the server is attached to",
		":watch":  "changes what the server is attached to",
		// A tool caller does not own the user's durable session. The pad is
		// what makes the interactive session survive the process; a tool that
		// could open, replace or remove one would reach across from an agent's
		// conversation into the work somebody has been keeping for a week.
		":scratch": "acts on the user's durable session, which a tool caller does not own",
		// The buffer needs a terminal to hand over, and a tool server has
		// none — :buf without one is a refusal whichever way it is reached.
		// The flags that would work over a tool call are not worth a name
		// either: -run evaluates, so invariant 28 governs it, and a tool that
		// could write into the buffer somebody is editing would be reaching
		// into a window the user is looking at.
		":buf": "hands over the user's terminal, which a tool caller has none of",
	}

	seen := map[string]bool{}
	for _, cmd := range builtins {
		why, ok := excluded[cmd.Name]
		if !ok {
			continue
		}
		seen[cmd.Name] = true
		if cmd.MCP != "" {
			t.Errorf("%s: exposed as MCP tool %q — it %s", cmd.Name, cmd.MCP, why)
		}
	}
	for name := range excluded {
		if !seen[name] {
			t.Errorf("%s: pinned as excluded but no such command — update this pin", name)
		}
	}
}

// TestPluginCommandsAreNeverStatic holds invariant-shaped ground: a plugin
// command evaluates code through EvalTransient, so none may claim the static
// tier, and each gets its MCP name derived from its own.
func TestPluginCommandsAreNeverStatic(t *testing.T) {
	got := adapt(plugin.TaggedCommand{
		Plugin: "probe",
		Command: plugin.Command{
			Name:    ":probe",
			Summary: "a probe",
			Rewrite: func(arg string) (string, error) { return arg, nil },
		},
	})
	if got.Static {
		t.Error("a plugin command must not be static: it runs code")
	}
	if got.MCP != "go_probe" {
		t.Errorf("MCP name = %q, want go_probe", got.MCP)
	}
}

// stripStyles removes the escape codes a rich rendering carries, so a test can
// assert on what a reader sees. Shared by both tiers: the unit tests compare a
// Render twin against its Plain one, and the integration tests read a path out
// of a styled report.
func stripStyles(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
