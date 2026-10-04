// Package di holds the plugins for dependency-injection containers — the
// libraries whose central object had to hold a graph in order to build
// anything, and can therefore be asked what is in it.
//
// A container earns a plugin here only if it can be asked from a value already
// in scope, without building anything. That is a narrower bar than "is a DI
// library", and two well-known ones fail it in different ways. Both reasons are
// recorded here rather than only in a change document, so neither is
// rediscovered:
//
//   - wire resolves its graph at compile time and leaves no container behind.
//     wire_gen.go is ordinary generated code, and inspecting it means static
//     analysis of wire.Build provider sets — a go/types walk, not a plugin
//     rewrite. A different mechanism with different costs, and its own change
//     if it is ever worth one.
//   - fx has the graph — fx.DotGraph is provided in every app's container — but
//     exports nothing that reaches it from an *fx.App, whose whole surface is
//     Done, Err, Run, Start, StartTimeout, Stop, StopTimeout and Wait.
//     (*App).dotGraph is unexported, and the only public route,
//     fx.New(opts, fx.Populate(&g)), builds a second app and runs every
//     fx.Invoke in it. A command that constructed in order to answer would
//     construct again on every replay, and the side effects would accumulate
//     where they look like the user's own code running.
package di

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/plugin"
)

// Both plugins want both command names. Only one library is normally in a
// build list; when two are, the registry order in internal/plugins is the
// tie-break and :plugins names the loser.
//
// What they do not share is a table. dig holds a static graph and samber/do
// learns its edges as services are built, so fitting both to one column set
// would mean reporting an empty dependency column for samber/do as though it
// were a fact. Each plugin's output is its library's, which is why the header
// says who is speaking.

// header is the first line of every answer.
func header(from, what string) string { return from + " — " + what }

// nothingRegistered is the whole answer for a container with nothing in it. A
// header over an empty listing is indistinguishable from a container the
// command failed to read, and only one of those is a fact about the container.
func nothingRegistered(from string) string { return header(from, "nothing is registered") }

// dependencyGraph is :graph's header. It is a constant sentence rather than a
// count because the two libraries count different things: dig knows every edge,
// samber/do knows the ones it has seen.
const dependencyGraph = "dependency graph"

func servicesCommand(arg, detail string, rewrite func(string) (string, error)) plugin.Command {
	return plugin.Command{
		Name: ":services",
		Arg:  arg,
		Usage: cmdspec.Spec{
			Kind: cmdspec.GoExpr,
			Examples: []cmdspec.Example{
				{Line: ":services " + strings.Trim(arg, "<>"), Says: "every service the container can provide"},
			},
			See: []string{":graph"},
		},
		Text:    true,
		Summary: "what a container has registered, and where each provider came from",
		Detail:  detail,
		Rewrite: rewrite,
	}
}

func graphCommand(arg, detail string, rewrite func(string) (string, error)) plugin.Command {
	return plugin.Command{
		Name: ":graph",
		Arg:  arg,
		Usage: cmdspec.Spec{
			Kind: cmdspec.GoExpr,
			Examples: []cmdspec.Example{
				{Line: ":graph " + strings.Trim(arg, "<>"), Says: "who depends on whom"},
			},
			See: []string{":services"},
		},
		Text:    true,
		Summary: "how a container's providers depend on one another",
		Detail:  detail,
		Rewrite: rewrite,
	}
}

// needsArg is the usage line. The container is the only part of either command
// that differs between the two libraries, so the sentence is shared and the
// example is that library's conventional variable name.
func needsArg(name, arg, example string) error {
	return fmt.Errorf("usage: %s %s   e.g. %s %s", name, arg, name, example)
}

// Dig is the plugin for go.uber.org/dig.
//
// A dig container knows its whole graph before anything is built: Provide
// records each constructor's parameter types, so String prints every node with
// its dependencies and Visualize writes the same graph as DOT. Neither runs a
// constructor, which is what makes both commands safe to replay.
type Dig struct{}

func (Dig) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "dig",
		Module:  "go.uber.org/dig",
		Summary: ":services and :graph read a dig container, DOT included",
	}
}

func (Dig) Imports() []plugin.Import {
	return []plugin.Import{{Name: "dig", Path: "go.uber.org/dig"}}
}

func (Dig) Aliases() []plugin.Alias {
	return []plugin.Alias{{Name: "dig", Module: "go.uber.org/dig"}}
}

// digEdge is the arrow dig writes between a node and its dependency list.
// Counting it is how a rewrite tells an empty container from a full one — the
// only fact either command needs beyond dig's own text, so nothing here parses
// that text. If dig ever changes the format, the cost is a wrong count rather
// than a wrong listing.
const digEdge = " -> "

// dotLabel introduces the DOT block, as a label on its own line rather than a
// fence: the answer is piped about as often as it is read, and `dot -Tpng`
// wants a description that starts somewhere findable.
const dotLabel = "DOT:"

// digNoDOT reports a Visualize that declined. dig returns an error for a graph
// it cannot draw, and the readable form above it is still the answer — so the
// failure is printed next to it rather than in place of it.
const digNoDOT = "dig emitted no DOT: "

func (Dig) Commands() []plugin.Command {
	return []plugin.Command{
		servicesCommand("<c>",
			"Container.String prints one line per registered type: what it provides,\n"+
				"what its constructor takes, and that constructor's signature — which is\n"+
				"where the provider came from, in the only form dig records. This is dig's\n"+
				"own text, passed through unparsed.\n\n"+
				"The values block underneath is what the container has already built.\n"+
				"Neither command builds anything, so it holds whatever the session put\n"+
				"there and nothing more.",
			func(arg string) (string, error) {
				if strings.TrimSpace(arg) == "" {
					return "", needsArg(":services", "<c>", "c")
				}
				return "func() string { __c := " + arg + "; " +
					"__s := strings.TrimRight(__c.String(), \"\\n\"); " +
					"__n := strings.Count(__s, " + strconv.Quote(digEdge) + "); " +
					"if __n == 0 { return " + strconv.Quote(nothingRegistered("dig")) + " }; " +
					"return fmt.Sprintf(" +
					strconv.Quote(header("dig", "%d registered")+"\n\n%s") + ", __n, __s) }()", nil
			},
		),
		graphCommand("<c>",
			"dig.Visualize writes the graph as DOT, so the answer carries both forms:\n"+
				"the node listing to read here, and the description to pipe into\n"+
				"`dot -Tpng`. Neither is built by gluon — dig produces both, and it walks\n"+
				"the graph to do it rather than resolving anything.\n\n"+
				"The readable form is the same listing :services shows, because dig prints\n"+
				"each node's dependencies inline; what :graph adds is the DOT, which a\n"+
				"listing has no reason to carry.",
			func(arg string) (string, error) {
				if strings.TrimSpace(arg) == "" {
					return "", needsArg(":graph", "<c>", "c")
				}
				return "func() string { __c := " + arg + "; " +
					"__s := strings.TrimRight(__c.String(), \"\\n\"); " +
					"if strings.Count(__s, " + strconv.Quote(digEdge) + ") == 0 { return " +
					strconv.Quote(nothingRegistered("dig")) + " }; " +
					"var __b strings.Builder; " +
					"if __err := dig.Visualize(__c, &__b); __err != nil { return " +
					strconv.Quote(header("dig", dependencyGraph)+"\n\n") + " + __s + \"\\n\\n\" + " +
					strconv.Quote(digNoDOT) + " + __err.Error() }; " +
					"return " + strconv.Quote(header("dig", dependencyGraph)+"\n\n") + " + __s + " +
					strconv.Quote("\n\n"+dotLabel+"\n") + " + strings.TrimRight(__b.String(), \"\\n\") }()", nil
			},
		),
	}
}

// Do is the plugin for github.com/samber/do.
//
// An injector lists what it provides at any time, but it learns an edge only
// when a service is built: a do provider takes the injector and resolves what
// it needs by calling it, so before anything is invoked there is no static
// graph to read. :graph reports the edges the injector has actually recorded
// and says that is what they are.
type Do struct{}

func (Do) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "do",
		Module:  "github.com/samber/do",
		Summary: ":services and :graph read a samber/do injector, terminal form only",
	}
}

func (Do) Imports() []plugin.Import {
	return []plugin.Import{{Name: "do", Path: "github.com/samber/do/v2"}}
}

func (Do) Aliases() []plugin.Alias {
	return []plugin.Alias{{Name: "do", Module: "github.com/samber/do/v2"}}
}

// doFormat is samber/do's two-column listing. The second column is a scope
// rather than a constructor because a scope is what do records: services are
// registered into one, and the injector reports that, not the function.
const doFormat = "%-40s %s"

// doServicesHeader and doGraphHeader name the columns. The second column means
// something different in each command, which is why they are two headers and
// not one.
var (
	doServicesHeader = fmt.Sprintf(doFormat, "SERVICE", "SCOPE")
	doGraphHeader    = fmt.Sprintf(doFormat, "SERVICE", "DEPENDS ON")
)

// unrecordedDeps marks a service whose dependencies the injector has not
// recorded. Blank would read as "this service depends on nothing", which is a
// claim do has not made — the distinction routeUnknown draws in :routes,
// applied to an edge instead of a cell.
const unrecordedDeps = "—"

// doTerminalOnly is the sentence :graph owes the reader: samber/do emits no
// DOT, and the marker above it has a meaning worth spelling out. Assembling DOT
// from the listing instead would be gluon inventing a graph description in a
// format whose consumers treat it as exact.
var doTerminalOnly = "only the terminal form is available: samber/do emits no DOT description.\n" +
	"A " + unrecordedDeps + " is not \"no dependencies\": do records an edge when it builds a service."

func (Do) Commands() []plugin.Command {
	return []plugin.Command{
		servicesCommand("<i>",
			"ListProvidedServices names everything the injector can provide, in the\n"+
				"current scope and every ancestor, whether or not it has ever been built.\n"+
				"The scope column is do's answer to where a provider came from: it records\n"+
				"the scope a service was registered into rather than the function.\n\n"+
				"Nothing is invoked. A do provider only runs when something asks for the\n"+
				"service, and listing is not asking.",
			func(arg string) (string, error) {
				if strings.TrimSpace(arg) == "" {
					return "", needsArg(":services", "<i>", "i")
				}
				return "func() string { __i := " + arg + "; " +
					"__svcs := __i.ListProvidedServices(); " +
					"if len(__svcs) == 0 { return " + strconv.Quote(nothingRegistered("do")) + " }; " +
					"var __out []string; " +
					"for _, __s := range __svcs { __out = append(__out, fmt.Sprintf(" +
					strconv.Quote(doFormat) + ", __s.Service, __s.ScopeName)) }; " +
					"sort.Strings(__out); " +
					"return fmt.Sprintf(" +
					strconv.Quote(header("do", "%d registered")+"\n\n"+doServicesHeader+"\n%s") +
					", len(__svcs), strings.Join(__out, \"\\n\")) }()", nil
			},
		),
		graphCommand("<i>",
			"ExplainNamedService reports a service's dependencies, and do knows an edge\n"+
				"once it has built the service that has it — a provider takes the injector\n"+
				"and resolves what it needs by calling it, so there is nothing static to\n"+
				"read beforehand. A service nothing has invoked yet shows "+unrecordedDeps+".\n\n"+
				"That marker is the point of the command. Reporting no dependencies for a\n"+
				"service do has never built would be a confident answer to a question do\n"+
				"has not been asked.",
			func(arg string) (string, error) {
				if strings.TrimSpace(arg) == "" {
					return "", needsArg(":graph", "<i>", "i")
				}
				return "func() string { __i := " + arg + "; " +
					"__svcs := __i.ListProvidedServices(); " +
					"if len(__svcs) == 0 { return " + strconv.Quote(nothingRegistered("do")) + " }; " +
					"var __out []string; " +
					"for _, __s := range __svcs { " +
					"__deps := " + strconv.Quote(unrecordedDeps) + "; " +
					"if __d, __ok := do.ExplainNamedService(__i, __s.Service); __ok && len(__d.Dependencies) > 0 { " +
					"var __on []string; " +
					"for _, __dep := range __d.Dependencies { __on = append(__on, __dep.Service) }; " +
					"sort.Strings(__on); " +
					"__deps = strings.Join(__on, \", \") }; " +
					"__out = append(__out, fmt.Sprintf(" + strconv.Quote(doFormat) + ", __s.Service, __deps)) }; " +
					"sort.Strings(__out); " +
					"return " + strconv.Quote(header("do", dependencyGraph)+"\n\n"+doGraphHeader+"\n") +
					" + strings.Join(__out, \"\\n\") + " + strconv.Quote("\n\n"+doTerminalOnly) + " }()", nil
			},
		),
	}
}
