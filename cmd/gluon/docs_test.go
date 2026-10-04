package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/sandboxws/gluon/internal/docgen"
	"github.com/sandboxws/gluon/internal/repl"
)

// The docs site is rendered by a test rather than a subcommand, so nothing of
// it is linked into the binary, and so that generating it and checking it are
// one code path. `just docs` runs this with -update; `just test` runs it
// without, and fails while docs/ says something the code no longer does.

var updateDocs = flag.Bool("update", false, "rewrite docs/ from site/ and the registries")

func TestDocs(t *testing.T) {
	root := repoRoot(t)
	hermetic(t)

	m, err := docgen.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	m.Reg = docgen.ReadRegistry()
	m.Reg.CLI = cliDocs()
	m.Reg.MCP = mcpDocs(t)
	for _, tier := range m.Reg.MCP.Tiers {
		switch tier.Anchor {
		case "static":
			m.Reg.Counts.StaticTools = len(tier.Tools)
		case "eval":
			m.Reg.Counts.EvalTools = len(tier.Tools)
		}
	}
	m.Track = docgen.NewTracker()
	out, err := docgen.Build(m, root)
	if err != nil {
		t.Fatal(err)
	}

	// Every command is shown being typed on a guide page, with what it
	// printed — the guides are the full usage, and a command added to the
	// registry without a worked example fails here, naming it.
	var names []string
	seen := map[string]bool{}
	for _, c := range repl.Reference() {
		if !seen[c.Name] && !notWorkable[c.Name] {
			seen[c.Name] = true
			names = append(names, c.Name)
		}
	}
	for _, c := range m.Track.Unworked(names) {
		t.Errorf("%s is never worked through on a guide page — add a session for it to site/sessions and the guide it belongs to", c)
	}

	// Every page well formed and every link landing, before anything is
	// written: a broken page is not worth committing.
	for _, e := range docgen.Check(out, root) {
		t.Error(e)
	}
	for _, e := range docgen.CheckReadme(out["README.md"], m.Site.BaseURL, out) {
		t.Error(e)
	}

	if *updateDocs {
		for rel, content := range out {
			path := filepath.Join(root, rel)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			// 0644 and not atomicfile's 0600: these are published pages, and a
			// rendering is regenerated whole, so an interrupted run is fixed by
			// running it again.
			if err := os.WriteFile(path, content, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		for _, stale := range staleDocs(t, root, out) {
			if err := os.Remove(filepath.Join(root, stale)); err != nil {
				t.Fatal(err)
			}
		}
		return
	}

	for rel, want := range out {
		got, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Errorf("%s is missing — run `just docs`", rel)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is out of date — run `just docs`\n%s", rel, firstDifference(got, want))
		}
	}
	for _, stale := range staleDocs(t, root, out) {
		t.Errorf("%s was generated from a page that no longer exists — run `just docs`", stale)
	}
}

// notWorkable are the commands a transcript cannot show: they act on the
// terminal rather than print anything.
var notWorkable = map[string]bool{":q": true, ":clear": true}

// cliDocs is the cobra tree as the CLI reference prints it: every command a
// user can run, with its own flags. Long flags are written with one dash, the
// way the README has always written them and normalizeArgs still reads them
// (invariant 20).
func cliDocs() []docgen.CLICommand {
	root := newRootCmd()
	root.InitDefaultCompletionCmd()
	var out []docgen.CLICommand
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Hidden || c.Name() == "help" {
			return
		}
		d := docgen.CLICommand{
			Path:    c.CommandPath(),
			Anchor:  strings.ReplaceAll(strings.TrimPrefix(c.CommandPath(), "gluon"), " ", "-"),
			Use:     c.UseLine(),
			Short:   c.Short,
			Long:    strings.TrimSpace(c.Long),
			Example: strings.TrimSpace(c.Example),
		}
		if d.Anchor == "" {
			d.Anchor = "gluon"
		} else {
			d.Anchor = strings.TrimPrefix(d.Anchor, "-")
		}
		c.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if f.Hidden || f.Name == "help" {
				return
			}
			value, usage := pflag.UnquoteUsage(f)
			label := "-" + f.Name
			if f.Shorthand != "" {
				label = "-" + f.Shorthand + ", " + label
			}
			if value != "" {
				label += " <" + value + ">"
			}
			if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "[]" && f.DefValue != "0" {
				usage += " (default " + f.DefValue + ")"
			}
			d.Flags = append(d.Flags, docgen.Row{Label: label, Anchor: d.Anchor + "-" + f.Name, Help: usage})
		})
		out = append(out, d)
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	return out
}

// mcpDocs is the tool surface as a client receives it: each tool's definition
// is mcpTool's, the one the server itself registers.
func mcpDocs(t *testing.T) docgen.MCPDoc {
	t.Helper()
	tool := func(name, command, desc string, schema any) docgen.Tool {
		raw, err := json.MarshalIndent(schema, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if rm, ok := schema.(json.RawMessage); ok {
			var buf bytes.Buffer
			if json.Indent(&buf, rm, "", "  ") == nil {
				raw = buf.Bytes()
			}
		}
		return docgen.Tool{Name: name, Command: command, Description: desc, Schema: string(raw)}
	}
	var static, eval, fromPlugins []docgen.Tool
	for _, c := range (&repl.Core{}).Commands() {
		if c.MCP == "" {
			continue
		}
		mt := mcpTool(c)
		if c.Static {
			static = append(static, tool(mt.Name, c.Name, mt.Description, mt.InputSchema))
		} else {
			eval = append(eval, tool(mt.Name, c.Name, mt.Description, mt.InputSchema))
		}
	}
	ev := goEvalTool()
	eval = append(eval, tool(ev.Name, "", ev.Description, ev.InputSchema))
	seen := map[string]bool{}
	for _, c := range repl.Reference() {
		if c.Plugin == "" || c.MCP == "" || seen[c.MCP] {
			continue
		}
		seen[c.MCP] = true
		mt := mcpTool(c)
		fromPlugins = append(fromPlugins, tool(mt.Name, c.Name, mt.Description, mt.InputSchema))
	}
	return docgen.MCPDoc{
		Tiers: []docgen.Tier{
			{Name: "Static tools", Anchor: "static", Tools: static,
				Lead: "Served by a bare gluon mcp. Each answers from go/types or the transcript and never builds or runs anything."},
			{Name: "With --eval", Anchor: "eval", Tools: eval,
				Lead: "Served only when the client config says gluon mcp --eval: these build and run code, and granting that belongs to the person who edits the config (invariant 28)."},
			{Name: "From plugins", Anchor: "plugin", Tools: fromPlugins,
				Lead: "Present with --eval while the plugin's library is in the session's build list."},
		},
		StaticInstructions: mcpInstructions("", false),
		EvalInstructions:   mcpInstructions("", true),
	}
}

// staleDocs is every generated file under docs/ that Build no longer writes.
// A hand-written file there — the stylesheet, the script, the images — carries
// no marker and is never touched.
func staleDocs(t *testing.T, root string, out map[string][]byte) []string {
	t.Helper()
	var stale []string
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".html") {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if _, ok := out[rel]; ok {
			return nil
		}
		if b, err := os.ReadFile(path); err == nil && docgen.Generated(b) {
			stale = append(stale, rel)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return stale
}

// firstDifference is the first line where two renderings part, with a little
// context — enough to see what moved without printing a page.
func firstDifference(got, want []byte) string {
	g, w := strings.Split(string(got), "\n"), strings.Split(string(want), "\n")
	for i := 0; i < len(g) || i < len(w); i++ {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			return "  line " + itoa(i+1) + "\n  on disk:  " + clip(gl) + "\n  rendered: " + clip(wl)
		}
	}
	return "  (they differ only in their final newline)"
}

func clip(s string) string {
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}

func itoa(n int) string {
	var b [20]byte
	i := len(b)
	for n >= 10 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	i--
	b[i] = byte('0' + n)
	return string(b[i:])
}

// repoRoot is the module root, two directories up from this package.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("no go.mod at %s: %v", root, err)
	}
	return root
}

// hermetic keeps the machine running the test out of what it renders: a theme
// list read from the user's config directory, a scratchpad from their data
// directory, an editor from their environment.
func hermetic(t *testing.T) {
	t.Helper()
	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "HOME"} {
		t.Setenv(v, t.TempDir())
	}
	for _, v := range []string{"EDITOR", "VISUAL", "GLUON_MAX_ITEMS", "GLUON_MAX_DEPTH", "NO_COLOR"} {
		t.Setenv(v, "")
	}
}
