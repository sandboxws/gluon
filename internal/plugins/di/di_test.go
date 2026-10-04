package di

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/plugin"
)

// containers is every plugin in this pack, with the argument name its usage
// line suggests. Adding one here is what puts it under every test below.
var containers = []struct {
	name string
	p    plugin.Plugin
	arg  string
}{
	{"dig", Dig{}, "c"},
	{"do", Do{}, "i"},
}

func rewrite(t *testing.T, p plugin.Plugin, name, arg string) string {
	t.Helper()
	for _, c := range p.(plugin.Commander).Commands() {
		if c.Name != name {
			continue
		}
		src, err := c.Rewrite(arg)
		if err != nil {
			t.Fatalf("%s rejected %q: %v", name, arg, err)
		}
		return src
	}
	t.Fatalf("%T contributes no %s", p, name)
	return ""
}

// literals is every string constant in a generated expression. All four
// commands build their answer out of these, so reading them is how a test
// checks an output shape without a toolchain and two third-party modules.
func literals(t *testing.T, src string) []string {
	t.Helper()
	expr, err := parser.ParseExpr(src)
	if err != nil {
		t.Fatalf("generated source does not parse: %v\n%s", err, src)
	}
	var out []string
	ast.Inspect(expr, func(n ast.Node) bool {
		if b, ok := n.(*ast.BasicLit); ok && b.Kind == token.STRING {
			if s, err := strconv.Unquote(b.Value); err == nil {
				out = append(out, s)
			}
		}
		return true
	})
	return out
}

// constructing is every API on either library that builds, resolves, runs or
// registers a provider.
//
// The check is on the generated source rather than on what a command prints,
// because that is where the failure would be invisible: a rewrite that called
// one of these would look like the user's own code running, and would run again
// on every replay of the session.
//
// "Invoke" covers dig's Container.Invoke and every do.Invoke form, and it also
// rules out do's ListInvokedServices — which constructs nothing, but reports
// only what has already been built and would quietly answer a narrower question
// than :services asks. "Provide(" does not match ListProvidedServices, which is
// the describing call do's :services is built on.
var constructing = []string{
	"Invoke",      // dig's Container.Invoke, do's Invoke family, do's ListInvokedServices
	"Populate",    // fx's only public route to a graph, which builds an app to reach it
	"Provide(",    // registering a provider, as opposed to listing the ones there are
	"Decorate",    // dig's graph mutation
	"Scope(",      // child-scope creation in both libraries, which mutates the container
	"Shutdown",    // do's teardown, which runs every registered hook
	"HealthCheck", // do's probe, which builds each service in order to ask it
	"fx.",         // the library this pack excludes; see the package comment
}

// TestNoRewriteConstructsAnything is the sharpest guarantee in the pack, and
// the cheapest place to hold it.
func TestNoRewriteConstructsAnything(t *testing.T) {
	for _, c := range containers {
		for _, name := range []string{":services", ":graph"} {
			src := rewrite(t, c.p, name, c.arg)
			for _, bad := range constructing {
				if strings.Contains(src, bad) {
					t.Errorf("%s's %s reaches %q, which builds rather than describes:\n%s",
						c.name, name, bad, src)
				}
			}
		}
	}
}

// TestGraphCarriesDOTWhereThereIsOneAndSaysSoWhereThereIsNot. dig produces a
// real DOT description, so :graph carries it; samber/do produces none, so
// :graph says that rather than assembling something DOT-shaped from a listing.
// A consumer of a graph description treats it as exact, which is what makes the
// second half as important as the first.
func TestGraphCarriesDOTWhereThereIsOneAndSaysSoWhereThereIsNot(t *testing.T) {
	digSrc := rewrite(t, Dig{}, ":graph", "c")
	if !strings.Contains(digSrc, "dig.Visualize(") {
		t.Errorf("dig's :graph does not call Visualize, so it emits no DOT:\n%s", digSrc)
	}
	if !strings.Contains(strings.Join(literals(t, digSrc), "\x00"), dotLabel) {
		t.Errorf("dig's :graph does not label its DOT block:\n%s", digSrc)
	}

	doSrc := rewrite(t, Do{}, ":graph", "i")
	if strings.Contains(doSrc, "digraph") {
		t.Errorf("do's :graph assembles DOT syntax samber/do does not produce:\n%s", doSrc)
	}
	var said bool
	for _, lit := range literals(t, doSrc) {
		if strings.Contains(lit, doTerminalOnly) {
			said = true
		}
	}
	if !said {
		t.Errorf("do's :graph does not say only the terminal form is available:\n%s", doSrc)
	}
	for _, want := range []string{"only the terminal form", "no DOT", unrecordedDeps} {
		if !strings.Contains(doTerminalOnly, want) {
			t.Errorf("the note omits %q: %q", want, doTerminalOnly)
		}
	}
}

// TestEveryAnswerNamesThePluginThatGaveIt. Two plugins share both command
// names and each reports its own library's shape, so an answer that did not say
// which library produced it would be a table the reader cannot place. Every
// branch is covered: the populated answer and the empty one both carry it.
func TestEveryAnswerNamesThePluginThatGaveIt(t *testing.T) {
	for _, c := range containers {
		for _, name := range []string{":services", ":graph"} {
			src := rewrite(t, c.p, name, c.arg)
			var named int
			for _, lit := range literals(t, src) {
				if strings.HasPrefix(lit, c.name+" — ") {
					named++
				}
			}
			if named < 2 {
				t.Errorf("%s's %s has %d answers naming the plugin, want both branches:\n%s",
					c.name, name, named, src)
			}
		}
	}
}

// TestAnEmptyContainerIsAStatementNotAnEmptyTable. A header over nothing is
// indistinguishable from a container the command failed to read, and only one
// of those is a fact about the container.
func TestAnEmptyContainerIsAStatementNotAnEmptyTable(t *testing.T) {
	for _, c := range containers {
		for _, name := range []string{":services", ":graph"} {
			src := rewrite(t, c.p, name, c.arg)
			var found bool
			for _, lit := range literals(t, src) {
				if lit == nothingRegistered(c.name) {
					found = true
				}
			}
			if !found {
				t.Errorf("%s's %s has no empty-container answer, so it would print a bare header:\n%s",
					c.name, name, src)
			}
		}
	}
	if got := nothingRegistered("dig"); !strings.Contains(got, "nothing is registered") {
		t.Errorf("the empty answer does not say so: %q", got)
	}
}

// TestBothPluginsContributeBothCommands, so a rename cannot quietly leave one
// library answering half the capability.
func TestBothPluginsContributeBothCommands(t *testing.T) {
	for _, c := range containers {
		var names []string
		for _, cmd := range c.p.(plugin.Commander).Commands() {
			names = append(names, cmd.Name)
			if !cmd.Text {
				t.Errorf("%s's %s is not Text, so it would print as (string) %q…",
					c.name, cmd.Name, "")
			}
		}
		if len(names) != 2 || names[0] != ":services" || names[1] != ":graph" {
			t.Errorf("%s contributes %v, want [:services :graph]", c.name, names)
		}
	}
}

// TestEveryCommandRejectsAnEmptyArgumentWithUsage. Both Args contain "<", so a
// bare invocation must return the usage line rather than source that reads the
// zero value of nothing.
func TestEveryCommandRejectsAnEmptyArgumentWithUsage(t *testing.T) {
	for _, c := range containers {
		for _, cmd := range c.p.(plugin.Commander).Commands() {
			_, err := cmd.Rewrite("  ")
			if err == nil {
				t.Fatalf("%s's %s accepted an empty argument", c.name, cmd.Name)
			}
			if !strings.HasPrefix(err.Error(), "usage:") {
				t.Errorf("%s's %s does not begin with usage: %q", c.name, cmd.Name, err)
			}
			if !strings.Contains(err.Error(), cmd.Name) || !strings.Contains(err.Error(), c.arg) {
				t.Errorf("%s's %s usage line names neither the command nor an example: %q",
					c.name, cmd.Name, err)
			}
		}
	}
}
