package repl

import (
	"sort"
	"strings"

	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/syntax"
)

// A Command is one meta command, defined once.
//
// Before this, adding a command meant editing four lists that did not reference
// each other: the dispatch switch, the help text, the completion candidates,
// and the README table. Three of those failed silently — a command missing from
// the completion list is simply never offered, and one missing from help is
// undiscoverable. The registry below is the single place, and the tests assert
// that dispatch, help and completion are generated from it.
type Command struct {
	// Name is the canonical spelling, with its colon.
	Name string
	// Aliases dispatch to the same handler. They are offered for completion
	// only when they are not merely a shorter prefix of Name.
	Aliases []string
	// Arg is how the argument reads in help — "<exp>", "[-v] <exp>", "a, b".
	// Empty means the command takes none, and the usage test relies on that.
	Arg string
	// Group orders the help. Plugins add their own.
	Group string
	// Summary is the one line help prints.
	Summary string
	// Detail is the longer form, for `:help <command>`.
	Detail string
	// Usage is what the command takes — its kind of argument, operands, flags
	// and examples — declared once for :help, --help, completion, the usage
	// hint, the MCP descriptions and the docs. See internal/cmdspec.
	Usage cmdspec.Spec
	// Plugin names the plugin a command came from, and is empty for a builtin.
	Plugin string
	// Run does the work. It runs on a background goroutine and must not touch
	// the terminal — everything it wants the UI to do goes back in Result.
	Run func(c *Core, arg string) Result

	// MCP names this command as a tool of `gluon mcp` — "go_type" for :t. A
	// command with no MCP name is not a tool: :help reads like UI, not API,
	// and :use, :get and :edit act on things a tool caller does not own.
	MCP string
	// MCPBare pins a tool to the command's bare form: the adapter passes no
	// argument and the tool advertises none, whatever the command's own Arg
	// reads. It exists for a flag that does more than the tool is pinned as
	// doing — :reset -deps removes the session's modules, and session_reset is
	// pinned as clearing the session (invariant 28's spirit: a tool must not
	// do more than its pin says).
	MCPBare bool
	// Static marks a tool that answers from go/types or the transcript
	// without building or running anything. gluon mcp exposes static tools
	// unconditionally and everything else only behind --eval, so the tier is
	// part of the command's definition rather than a list beside it.
	Static bool
}

// Command groups, in the order help prints them.
const (
	groupSession    = "the session"
	groupInspect    = "inspecting"
	groupModule     = "the module it can see"
	groupTranscript = "the transcript"
	groupPlugin     = "from plugins"
)

var groupOrder = []string{groupSession, groupInspect, groupModule, groupTranscript, groupPlugin}

// builtins is every command gluon ships with.
//
// Assigned in init rather than at declaration: the Run closures name methods
// that reach lookup, which reads builtins, and Go's initialization-cycle
// detector follows that chain even though nothing is called until dispatch.
var builtins []Command

// paintKind is derived here rather than in its own init, so the order does not
// depend on which file the compiler saw first: from the one list, once.
func init() {
	builtins = builtinCommands()
	paintKind = argKinds(builtins)
}

func builtinCommands() []Command {
	return []Command{
		{
			Name: ":help", Aliases: []string{":h"}, Arg: "[command]", Group: groupSession,
			Usage: cmdspec.Spec{
				Kind:   cmdspec.Words,
				Params: []cmdspec.Param{{Name: "command", Optional: true, Values: cmdspec.Values{Source: cmdspec.Commands}}},
				Examples: []cmdspec.Example{
					{Line: ":help :http", Says: "one command's page — :http --help prints the same"},
					{Line: ":help", Says: "every command, and at a terminal a view to browse them"},
				},
				See: []string{":plugins", ":guide"},
			},
			Summary: "this, or detail on one command",
			Run:     func(c *Core, arg string) Result { return c.help(arg) },
		},
		{
			Name: ":q", Aliases: []string{":quit", ":exit"}, Group: groupSession,
			Usage: cmdspec.Spec{
				Kind:     cmdspec.NoArg,
				Examples: []cmdspec.Example{{Line: ":q", Says: "ctrl-d does the same"}},
				See:      []string{":scratch"},
			},
			Summary: "quit",
			Run:     func(*Core, string) Result { return Result{Quit: true} },
		},
		{
			Name: ":clear", Aliases: []string{":cls"}, Group: groupSession,
			Usage: cmdspec.Spec{
				Kind:     cmdspec.NoArg,
				Examples: []cmdspec.Example{{Line: ":clear", Says: "ctrl-l does the same, and the session is untouched"}},
				See:      []string{":reset"},
			},
			Summary: "clear the screen (ctrl-l), keeping the session",
			Run:     func(*Core, string) Result { return Result{Clear: true} },
		},
		{
			Name: ":reset", Arg: "[-deps]", Group: groupSession,
			Usage: cmdspec.Spec{
				Kind:  cmdspec.Words,
				Flags: []cmdspec.Flag{{Name: "-deps", Help: "remove the modules :get added, too"}},
				Examples: []cmdspec.Example{
					{Line: ":reset", Says: "no entries, the same modules"},
					{Line: ":reset -deps", Says: "no entries and no added modules"},
				},
				See: []string{":undo", ":get", ":scratch"},
			},
			Summary: "clear the session, and with -deps its modules too",
			Detail: "Bare :reset clears the entries and leaves the requirements alone, so a\n" +
				"session cleared to try the same library a different way does not have to\n" +
				"fetch it again.\n\n" +
				"-deps clears both, removing every module :get added — which deactivates\n" +
				"the plugins that depended on them. A requirement the attached host owns is\n" +
				"left alone: it belongs to the host's go.mod, which gluon never edits.",
			Run: func(c *Core, arg string) Result { return c.reset(arg) },
			MCP: "session_reset",
			// The tool is bare :reset. Clearing the session is what
			// session_reset is pinned as doing, and removing the session's
			// modules is more than that — so the flag is not reachable from a
			// tool call, whatever the command's own Arg reads.
			MCPBare: true,
		},

		{
			Name: ":theme", Aliases: []string{":themes"}, Arg: "[name]", Group: groupSession,
			Usage: cmdspec.Spec{
				Kind:   cmdspec.Words,
				Params: []cmdspec.Param{{Name: "name", Optional: true, Values: cmdspec.Values{Source: cmdspec.Themes}}},
				Examples: []cmdspec.Example{
					{Line: ":theme", Says: "the picker: the session repaints as the selection moves"},
					{Line: ":theme gruvppuccin-mocha", Says: "switch, and write it into config.toml"},
				},
				See: []string{":settings"},
			},
			Summary: "the colour themes, and switch between them",
			Detail: "With a name, switches to it and writes `[theme] name` into config.toml, so\n" +
				"the next session starts in it too.\n\n" +
				"With none, opens the picker: moving the selection repaints the session\n" +
				"underneath it, because the only useful preview of a palette is your own\n" +
				"code in it. Enter keeps the highlighted theme, esc puts back the one you\n" +
				"started in.\n\n" +
				"Per-role overrides in config.toml still apply on top of whichever theme is\n" +
				"chosen. `gluon theme import` is how an editor theme becomes one of these.",
			Run: func(c *Core, arg string) Result { return c.themeCmd(arg) },
		},
		{
			Name: ":settings", Aliases: []string{":set"}, Arg: "[key=val]", Group: groupSession,
			Usage: cmdspec.Spec{
				Kind:     cmdspec.Words,
				Synopsis: "[key [value | -]]",
				Params: []cmdspec.Param{
					{Name: "key", Optional: true, Values: cmdspec.Values{Source: cmdspec.Settings}},
					{Name: "value", Optional: true, Values: cmdspec.Values{Source: cmdspec.SettingValues},
						Help: "what to set it to, or - for its default; key=value works too"},
				},
				Examples: []cmdspec.Example{
					{Line: ":settings", Says: "every setting, its value, and where the value came from"},
					{Line: ":settings value.form", Says: "one setting explained"},
					{Line: ":settings timeout 2m", Says: "written into config.toml as one line"},
					{Line: ":settings timeout -", Says: "back to its default"},
				},
				See: []string{":theme"},
			},
			Summary: "every setting gluon has, and change one",
			Detail: "With nothing, lists them: what each does, what it is now, what it would be\n" +
				"otherwise, what it accepts, and whether the value came from config.toml or\n" +
				"from a default.\n\n" +
				"With a key, explains that one. With a key and a value, writes it into\n" +
				"config.toml — one line, leaving every other byte alone, so the comments you\n" +
				"put there survive. `:settings <key> -` puts it back to its default.\n\n" +
				"Both spellings work: `:settings timeout 2m` and `:settings timeout=2m`.\n\n" +
				"A few settings are listed and not settable — an array is not one line, and\n" +
				"rewriting one wholesale would take the comments inside it. Those say so.\n\n" +
				"`:settings value.form` is the one that changes what you see: table is the\n" +
				"bordered box, and tree, columns, line and literal are the other four shapes\n" +
				"a value can take. It changes what a terminal draws and nothing else — a\n" +
				"pipe, `gluon -e` and the -json envelopes never see a form.",
			Run: func(c *Core, arg string) Result { return c.settingsCmd(arg) },
		},
		{
			Name: ":plugins", Group: groupSession,
			Usage: cmdspec.Spec{
				Kind:     cmdspec.NoArg,
				Examples: []cmdspec.Example{{Line: ":plugins"}},
				See:      []string{":guide", ":get", ":help"},
			},
			Summary: "which library plugins are active here, and why",
			Detail: "A plugin is inert until the session can actually see its library, so a\n" +
				"command that is simply absent looks the same as a broken install. This is\n" +
				"what says which. [plugins] disable = [...] in config.toml turns one off.",
			Run: func(c *Core, _ string) Result { return c.pluginList() },
		},
		{
			Name: ":guide", Arg: "<plugin>", Group: groupSession,
			Usage: cmdspec.Spec{
				Kind:     cmdspec.Words,
				Params:   []cmdspec.Param{{Name: "plugin", Values: cmdspec.Values{Source: cmdspec.Guides}}},
				Examples: []cmdspec.Example{{Line: ":guide ent", Says: "the ent plugin's cheatsheet"}},
				See:      []string{":plugins"},
			},
			Summary: "a plugin's cheatsheet",
			Run:     func(c *Core, arg string) Result { return c.guide(arg) },
		},

		{
			Name: ":t", Aliases: []string{":type"}, Arg: "[-v|-d] <exp>", Group: groupInspect,
			Usage: cmdspec.Spec{
				Kind: cmdspec.GoExpr,
				Flags: []cmdspec.Flag{
					{Name: "-v", Mode: true, Help: "add the underlying type and its kind"},
					{Name: "-d", Mode: true, Runs: true, Help: "run it once for the dynamic type inside an interface"},
				},
				Examples: []cmdspec.Example{
					{Line: `:t strconv.Atoi("12")`, Says: "two values: (int, error)"},
					{Line: ":t -v time.Second", Says: "the named type, what it is underneath, and its kind"},
					{Line: ":t -d r", Says: "what is inside the interface r — this one costs a build"},
				},
				See: []string{":m", ":ls", ":doc"},
			},
			Summary: "the type of an expression      (Ruby's .class)",
			Detail: "Answers from go/types in gluon's own process, so it never builds and comes\n" +
				"back in about a millisecond.\n\n" +
				"-d is the exception: it reports the dynamic type — what is actually inside\n" +
				"an interface value — which nothing but a run can know. It costs a build and\n" +
				"an execution, and it runs your expression, so a call with a side effect has\n" +
				"it. Only at the prompt: the MCP tool stays static and ignores the flag.",
			Run: func(c *Core, arg string) Result { return c.describe(arg) },
			MCP: "go_type", Static: true,
		},
		{
			Name: ":m", Aliases: []string{":methods"}, Arg: "<exp>", Group: groupInspect,
			Usage: cmdspec.Spec{
				Kind: cmdspec.GoExpr,
				Examples: []cmdspec.Example{
					{Line: ":m strings.Builder{}", Says: "its methods, the pointer-only ones marked — for a Builder, all of them"},
				},
				See: []string{":t", ":impl", ":iface"},
			},
			Summary: "its method set and interfaces  (Ruby's .methods)",
			Detail: "Lists the value and pointer method sets together, marking the pointer-only\n" +
				"ones — the rule that puts them in *T's set and not T's is the thing that\n" +
				"trips people up. Satisfaction is checked against error, a few stdlib\n" +
				"interfaces, and any interface you declared this session.",
			Run: func(c *Core, arg string) Result { return c.methods(arg) },
			MCP: "go_methods", Static: true,
		},
		{
			Name: ":impl", Arg: "<t>, <i>", Group: groupInspect,
			Usage: cmdspec.Spec{
				Kind: cmdspec.GoName,
				Params: []cmdspec.Param{
					{Name: "t", Help: "the type"},
					{Name: "i", Sep: ",", Help: "the interface, from anywhere the session can see"},
				},
				Examples: []cmdspec.Example{
					{Line: ":impl *bytes.Buffer, io.Reader"},
					{Line: ":impl bytes.Buffer, io.Writer", Says: "no: Write is on *bytes.Buffer, and the answer says so"},
				},
				See: []string{":sat", ":iface", ":m"},
			},
			Summary: "does a type implement an interface, and what is missing",
			Detail: "Answers against the interface you name, not a fixed list — anything the\n" +
				"session can see: declared here, imported, or in the attached module.\n\n" +
				"A no names the first method in the way and the signature the interface\n" +
				"wants, and says when *T would have satisfied it even though T does not —\n" +
				"which is what the compiler's own error leaves you to guess.",
			Run: func(c *Core, arg string) Result { return c.implements(arg) },
			MCP: "go_implements", Static: true,
		},
		{
			Name: ":sat", Arg: "<exp>, <i>", Group: groupInspect,
			Usage: cmdspec.Spec{
				Kind: cmdspec.GoExpr,
				Params: []cmdspec.Param{
					{Name: "exp", Help: "the value"},
					{Name: "i", Sep: ",", Help: "the interface"},
				},
				Examples: []cmdspec.Example{{Line: ":sat os.Stdout, io.Writer"}},
				See:      []string{":impl", ":cast"},
			},
			Summary: "the same question, asked of a value",
			Detail: "The verdict is about the expression's static type. When that type is itself\n" +
				"an interface the answer says so: what is actually inside an interface value\n" +
				"is not known without running it.",
			Run: func(c *Core, arg string) Result { return c.satisfies(arg) },
			MCP: "go_satisfies", Static: true,
		},
		{
			Name: ":cast", Arg: "<e>, <t>", Group: groupInspect,
			Usage: cmdspec.Spec{
				Kind: cmdspec.GoExpr,
				Params: []cmdspec.Param{
					{Name: "e", Help: "an interface value"},
					{Name: "t", Sep: ",", Help: "the type to assert"},
				},
				Examples: []cmdspec.Example{
					{Line: ":cast any(1), string", Says: "legal, and would fail when it runs"},
					{Line: ":cast io.Reader(nil), *strings.Builder", Says: "rejected outright: *strings.Builder has no Read"},
				},
				See: []string{":sat", ":impl"},
			},
			Summary: "whether e.(t) is legal, and why not",
			Detail: "Separates the assertion the compiler rejects outright — no value of e's type\n" +
				"can ever be a t — from the one that compiles and may still fail at run\n" +
				"time. The first is the useful one: Go reports it as a method you never\n" +
				"mentioned, and this names it.",
			Run: func(c *Core, arg string) Result { return c.cast(arg) },
			MCP: "go_cast", Static: true,
		},
		{
			Name: ":iface", Arg: "<name>", Group: groupInspect,
			Usage: cmdspec.Spec{
				Kind:     cmdspec.GoName,
				Params:   []cmdspec.Param{{Name: "name", Help: "an interface: declared here, imported, or in the attached module"}},
				Examples: []cmdspec.Example{{Line: ":iface io.Writer", Says: "its methods, and the types the session can see that implement it"}},
				See:      []string{":impl", ":mock"},
			},
			Summary: "an interface's methods, and who implements it",
			Detail: "The implementor search covers what the session can name: its own types, the\n" +
				"packages its program imports, and the attached module's packages already\n" +
				"loaded. It never scans the module cache, and it says how many types it\n" +
				"looked at — so \"nothing implements it\" cannot be mistaken for \"nothing\n" +
				"was searched\".",
			Run: func(c *Core, arg string) Result { return c.iface(arg) },
			MCP: "go_interface", Static: true,
		},
		{
			Name: ":embeds", Arg: "<type>", Group: groupInspect,
			Usage: cmdspec.Spec{
				Kind:     cmdspec.GoName,
				Examples: []cmdspec.Example{{Line: ":embeds bufio.ReadWriter", Says: "*Reader and *Writer, and the methods each promotes"}},
				See:      []string{":m", ":layout"},
			},
			Summary: "embedded fields, and the methods they promote",
			Detail: "A method two embedded types both provide at the same depth is reported as\n" +
				"ambiguous rather than resolved: that selector does not compile, and naming\n" +
				"a winner would teach something false.",
			Run: func(c *Core, arg string) Result { return c.embeds(arg) },
			MCP: "go_embeds", Static: true,
		},
		{
			Name: ":gen", Arg: "<type>", Group: groupInspect,
			Usage: cmdspec.Spec{
				Kind:     cmdspec.GoName,
				Examples: []cmdspec.Example{{Line: ":gen atomic.Pointer", Says: "its type parameter, and the constraint on it"}},
				See:      []string{":t", ":doc"},
			},
			Summary: "a generic's type parameters and their constraints",
			Detail: "Answers from go/types and never builds. It reads the declaration, so a\n" +
				"generic nobody has instantiated yet can be asked what it would accept.",
			Run: func(c *Core, arg string) Result { return c.generics(arg) },
			MCP: "go_generics", Static: true,
		},
		{
			Name: ":mock", Arg: "<type>", Group: groupInspect,
			Usage: cmdspec.Spec{
				Kind:     cmdspec.GoName,
				Examples: []cmdspec.Example{{Line: ":mock io.ReadCloser", Says: "a struct with a func field per method, to paste in"}},
				See:      []string{":spy", ":iface", ":test"},
			},
			Summary: "a mock implementation of an interface",
			Detail: "Emits the source and declares nothing: the mock enters the session only if\n" +
				"you paste it, so it cannot collide with a name you already have. One\n" +
				"settable func field per method, and methods that dispatch to them —\n" +
				"including the ones an embedded interface promotes, because the method set\n" +
				"is the one :m computes.\n\n" +
				"A method whose field is unset panics naming itself. Returning zero values\n" +
				"instead is how a mock makes a test pass for the wrong reason: the assertion\n" +
				"holds because nothing was called, and nothing says so.\n\n" +
				"Nothing is built and nothing is run — this is go/types, like :iface.",
			Run: func(c *Core, arg string) Result { return c.metaMock(arg, false) },
			MCP: "go_mock", Static: true,
		},
		{
			Name: ":spy", Arg: "<type>", Group: groupInspect,
			Usage: cmdspec.Spec{
				Kind:     cmdspec.GoName,
				Examples: []cmdspec.Example{{Line: ":spy io.Writer", Says: "the mock, plus every call's arguments and a Count()"}},
				See:      []string{":mock"},
			},
			Summary: "the same, recording what each method was called with",
			Detail: "A mock plus, for each method, the arguments of every call in order and a\n" +
				"Count() that reads back how many there were. The count is len(Calls)\n" +
				"rather than a second field, so the two cannot disagree.",
			Run: func(c *Core, arg string) Result { return c.metaMock(arg, true) },
			MCP: "go_spy", Static: true,
		},
		{
			Name: ":ls", Aliases: []string{":vars"}, Group: groupInspect,
			Usage: cmdspec.Spec{
				Kind:     cmdspec.NoArg,
				Examples: []cmdspec.Example{{Line: ":ls", Says: "every name in scope with its type, and the values _1, _2… carry"}},
				See:      []string{":hist", ":t"},
			},
			Summary: "what is in scope, with types   (pry's ls)",
			Run:     func(c *Core, _ string) Result { return c.ls() },
			MCP:     "go_scope", Static: true,
		},
		{
			Name: ":doc", Aliases: []string{":d"}, Arg: "[flags] <s>", Group: groupInspect,
			Usage: cmdspec.Spec{
				Kind:   cmdspec.GoName,
				Params: []cmdspec.Param{{Name: "name", Help: "a package, a symbol or a method, as go doc reads it"}},
				Flags: []cmdspec.Flag{
					{Name: "-src", Mode: true, Help: "the implementation rather than the documentation"},
					{Name: "-examples", Mode: true, Help: "the package's runnable examples, read off disk"},
					{Name: "-url", Mode: true, Help: "the pkg.go.dev address at the version in use"},
					{Name: "-pkg", Mode: true, Help: "read the name as a package, never as a symbol"},
				},
				Examples: []cmdspec.Example{
					{Line: ":doc strings.Cut"},
					{Line: ":doc -src strings.Cut", Says: "the source — pry's show-source"},
					{Line: ":doc -examples strings"},
					{Line: ":doc -url github.com/google/uuid.New", Says: "an address at the version the build list has; nothing is fetched"},
					{Line: ":doc -pkg errors", Says: "the package, where a symbol could have answered instead"},
				},
				See: []string{":since", ":t"},
			},
			Summary: "documentation, or the source   (ri / pry's show-source)",
			Detail: "-examples shows the package's runnable examples, each labelled with the\n" +
				"identifier it is an example of. `go doc` does not report them, so they are\n" +
				"read from the sources already on disk — a package whose sources were never\n" +
				"downloaded says so rather than reporting that it has none.\n\n" +
				"-url prints the pkg.go.dev address at the version the build list has. It\n" +
				"prints and nothing else: no fetch, no browser. When the version cannot be\n" +
				"determined — the standard library, a replaced module, a session with no\n" +
				"requirements — the address is version-independent and says so.\n\n" +
				"-pkg says the argument names a package, so a name that is also a symbol\n" +
				"resolves to the package or is reported as unresolved, rather than quietly\n" +
				"answering about the other one.\n\n" +
				"One flag at a time: they are four questions about a name, not four\n" +
				"adjustments to one answer. Output past a screenful is paged, and is still\n" +
				"printed in full through a pipe.",
			Run: func(c *Core, arg string) Result { return c.doc(arg) },
			MCP: "go_doc", Static: true,
		},
		{
			Name: ":since", Arg: "[version]", Group: groupInspect,
			Usage: cmdspec.Spec{
				Kind:    cmdspec.Words,
				FlagsAt: cmdspec.Anywhere,
				Params:  []cmdspec.Param{{Name: "version", Optional: true, Values: cmdspec.Values{Source: cmdspec.Releases}}},
				Flags: []cmdspec.Flag{
					{Name: "-lang", Mode: true, Help: "the language changes alone, as prose with a snippet"},
					{Name: "-pkg", Value: "path", Mode: true, Help: "every addition to one import path, across releases"},
					{Name: "-run", Value: "name", Mode: true, Runs: true, Help: "evaluate a language note's snippet in this session"},
				},
				Examples: []cmdspec.Example{
					{Line: ":since", Says: "every release the installed toolchain describes"},
					{Line: ":since 1.23 -lang"},
					{Line: ":since -pkg maps", Says: "which release added maps.Keys, and what came with it"},
					{Line: ":since 1.23 -run range-over-func"},
				},
				See: []string{":doc"},
			},
			Summary: "what each Go release added, from the toolchain you have",
			Detail: "With no argument, every release the installed toolchain describes, newest\n" +
				"first. With a version, that release: its language changes, its GODEBUG\n" +
				"entries, and its standard library additions a package at a time.\n\n" +
				"The additions are read from $GOROOT/api/go1.N.txt and the behaviour changes\n" +
				"from $GOROOT/doc/godebug.md — the release record the distribution already\n" +
				"ships. Nothing is fetched. Every api line since Go 1.19 carries the number\n" +
				"of the proposal that accepted it, so the go.dev/issue link beside a\n" +
				"declaration is a fact on disk rather than a guess.\n\n" +
				"-lang is the language changes alone, as prose with a runnable snippet. No\n" +
				"api file describes a change to the language, so these are written by hand,\n" +
				"and a release nobody has written up says so rather than reporting that its\n" +
				"syntax did not move.\n\n" +
				"-pkg <path> is every addition to one import path across every release —\n" +
				"which release added maps.Keys, and what else arrived with it.\n\n" +
				"-run <name> evaluates a language note's snippet into this session. It is\n" +
				"what the run choice on a note hands back, and it adds to the session and\n" +
				"the scratchpad like any line you typed, so :branch first if that matters.\n" +
				"A note needing a go directive above the one this session builds at is\n" +
				"refused with which of the two numbers is short, rather than left to the\n" +
				"compiler to report against a temp path you never wrote.",
			Run: func(c *Core, arg string) Result { return c.since(arg) },
			MCP: "go_since",
		},
		{
			Name: ":slice", Arg: "a, b", Group: groupInspect,
			Usage: cmdspec.Spec{
				Kind: cmdspec.GoExpr,
				Params: []cmdspec.Param{
					{Name: "exp", Help: "a slice or a string"},
					{Name: "exp", Optional: true, Repeat: true, Sep: ","},
				},
				Examples: []cmdspec.Example{{Line: ":slice x, x[1:3]", Says: "both headers, and whether they share a backing array"}},
				See:      []string{":diff", ":layout"},
			},
			Summary: "slice headers, and whether they share a backing array",
			Detail: "Shows the runtime triple — pointer, len, cap — and says when two slices are\n" +
				"looking at the same memory. That sharing is invisible in the printed values\n" +
				"and obvious in the pointers, which is the whole point.",
			Run: func(c *Core, arg string) Result { return c.slice(arg) },
			MCP: "go_slice_headers",
		},
		{
			Name: ":diff", Arg: "a, b", Group: groupInspect,
			Usage: cmdspec.Spec{
				Kind: cmdspec.GoExpr,
				Params: []cmdspec.Param{
					{Name: "a"},
					{Name: "b", Sep: ","},
				},
				Examples: []cmdspec.Example{{Line: ":diff got, want", Says: "only what differs, each with the path to it"}},
				See:      []string{":test", ":slice"},
			},
			Summary: "what differs between two values, and where",
			Detail: "Reports only the differences, each with the path to it — .Name, [3],\n" +
				"or . for the value itself. Two structs of a dozen fields that differ in\n" +
				"one produce one row, which is the reason to have this rather than print\n" +
				"both and read them.\n\n" +
				"A difference in shape is reported as one: different types stop the\n" +
				"comparison there rather than calling every field different, and a\n" +
				"length or a missing key is stated as itself.\n\n" +
				"Both values come from one evaluation, so a value derived from anything\n" +
				"non-deterministic is compared against the same run it was taken from.\n" +
				"The value printer caps a large collection, and when it did the report\n" +
				"says the comparison covers only the part it could see.",
			Run: func(c *Core, arg string) Result { return c.diff(arg) },
			MCP: "go_diff",
		},
		{
			Name: ":layout", Arg: "<type>", Group: groupInspect,
			Usage: cmdspec.Spec{
				Kind:   cmdspec.GoExpr,
				Params: []cmdspec.Param{{Name: "type", Help: "a struct type, or a value of one"}},
				Examples: []cmdspec.Example{
					{Line: ":layout struct{ a bool; b int64; c bool }", Says: "24 bytes, and the order that would take 16"},
				},
				See: []string{":embeds", ":slice"},
			},
			Summary: "field offsets, padding, and what reordering would save",
			Detail: "Answers from go/types and never builds: sizes, offsets and alignment are\n" +
				"the compiler's own for the platform gluon runs on. The suggested order is\n" +
				"the one that wastes least padding, and the saving is stated in bytes.",
			Run: func(c *Core, arg string) Result { return c.layout(arg) },
			MCP: "go_layout", Static: true,
		},
		{
			Name: ":err", Arg: "<exp>", Group: groupInspect,
			Usage: cmdspec.Spec{
				Kind:     cmdspec.GoExpr,
				Examples: []cmdspec.Example{{Line: `:err fmt.Errorf("load: %w", fs.ErrNotExist)`, Says: "each level of the chain, with its type"}},
				See:      []string{":t"},
			},
			Summary: "the wrapped error chain, with the type at each level",
			Detail: "Runs the expression once and walks the chain from the error it returned:\n" +
				"errors.Unwrap a level at a time, and every branch of a joined error, each\n" +
				"with its dynamic type — every level is something errors.Is can match.",
			Run: func(c *Core, arg string) Result { return c.errChain(arg) },
			MCP: "go_err_chain",
		},
		{
			Name: ":bench", Arg: "[flags] a[,b]", Group: groupInspect,
			Usage: cmdspec.Spec{
				Kind: cmdspec.GoExpr,
				Params: []cmdspec.Param{
					{Name: "exp"},
					{Name: "other", Optional: true, Sep: ",", Help: "a second expression, measured beside the first"},
				},
				Flags: []cmdspec.Flag{
					{Name: "-count", Value: "n", Help: "run it n times; report min, median and max"},
					{Name: "-cpu", Value: "list", Help: "once per GOMAXPROCS value, each labelled: 1,2,4"},
					{Name: "-profile", Help: "keep the run's CPU profile, and say where it went"},
				},
				Examples: []cmdspec.Example{
					{Line: `:bench strings.Repeat("ab", 64)`},
					{Line: ":bench -count 5 fmt.Sprint(42), strconv.Itoa(42)", Says: "side by side, with the ratio between them"},
					{Line: `:bench -cpu 1,4 -profile strings.Repeat("ab", 1024)`},
				},
				See: []string{":profile", ":memprof", ":esc"},
			},
			Summary: "ns/op, B/op and allocs/op, through testing.B",
			Detail: "B/op and allocs/op are always reported; there is no -mem flag because\n" +
				"there is nothing for it to turn on. The three flags say how the\n" +
				"benchmark is run, never what is measured. -count exists so a\n" +
				"measurement can be judged for consistency; -cpu is the only way to see\n" +
				"a contention effect from the REPL.\n\n" +
				"Flags are read only while they lead: everything from the first word\n" +
				"that is not one of them is the expression, verbatim.\n\n" +
				"Two expressions separated by a top-level comma are benchmarked side by\n" +
				"side with the ratio between them — the comparison you would otherwise\n" +
				"run twice and hold in your head. Both are measured in one evaluation,\n" +
				"so neither is timed against a different replay of the session, and a\n" +
				"comma inside brackets, parentheses, braces or a literal is not the\n" +
				"separator.",
			Run: func(c *Core, arg string) Result { return c.bench(arg) },
			MCP: "go_bench",
		},
		{
			Name: ":profile", Arg: "<exp>", Group: groupInspect,
			Usage: cmdspec.Spec{
				Kind:     cmdspec.GoExpr,
				Examples: []cmdspec.Example{{Line: ":profile sha256.Sum256(make([]byte, 64<<20))", Says: "the functions that took the time, and the pprof command for the rest"}},
				See:      []string{":memprof", ":bench"},
			},
			Summary: "where the time went, from a real run",
			Detail: "Evaluates the expression once with the CPU profiler running, lists the\n" +
				"functions that accounted for the most time, and names the file the\n" +
				"profile went to and the `go tool pprof` command that opens it.\n\n" +
				"The sample count is printed every time, and it is the first thing to\n" +
				"read. Go samples at 100 Hz, so an expression that finishes in a\n" +
				"millisecond contributes no samples at all and the ranking under it means\n" +
				"nothing. Below a hundred samples the report says so.\n\n" +
				"There is no :profile <port>. The child process exits when the evaluation\n" +
				"ends, so there is nothing left to serve a live profile from — this\n" +
				"captures one from a run that really happened instead.",
			Run: func(c *Core, arg string) Result { return c.profile(arg) },
			MCP: "go_profile",
		},
		{
			Name: ":memprof", Arg: "<exp>", Group: groupInspect,
			Usage: cmdspec.Spec{
				Kind:     cmdspec.GoExpr,
				Examples: []cmdspec.Example{{Line: `:memprof strings.Repeat("x", 1<<20)`, Says: "the sites that allocated the most bytes"}},
				See:      []string{":profile", ":bench", ":esc"},
			},
			Summary: "where the allocations went, from a real run",
			Detail: "The same capture as :profile against the heap profiler: the sites that\n" +
				"accounted for the most allocation, and the file that holds the rest.\n\n" +
				"A heap profile carries four measures that answer different questions.\n" +
				"This reports allocated bytes, which is the one that pairs with the B/op\n" +
				":bench prints, and the report says so rather than showing an unlabelled\n" +
				"number. The other three are in the file, under -sample_index.",
			Run: func(c *Core, arg string) Result { return c.memprof(arg) },
			MCP: "go_memprof",
		},
		{
			Name: ":trace", Arg: "<exp>", Group: groupInspect,
			Usage: cmdspec.Spec{
				Kind:     cmdspec.GoExpr,
				Examples: []cmdspec.Example{{Line: ":trace fib(10)", Says: "the stack it runs on, each frame named by the entry it came from"}},
				See:      []string{":err", ":save"},
			},
			Summary: "the call stack the expression is evaluated on",
			Detail: "Captures the stack where the expression is evaluated and names each frame\n" +
				"by the session entry it came from, not the temp file the program was\n" +
				"generated into.\n\n" +
				"It holds the expression's callers, not its callees: a function it is about\n" +
				"to call has not been entered yet, and by the time that call returns its\n" +
				"frame is gone. gluon compiles each line into a program that exits, so\n" +
				"there is no process to pause and no in-REPL breakpoint to set. For what\n" +
				"happens underneath a call, :save -debug is the way out.",
			Run: func(c *Core, arg string) Result { return c.trace(arg) },
			MCP: "go_trace",
		},
		{
			Name: ":esc", Arg: "<exp>", Group: groupInspect,
			Usage: cmdspec.Spec{
				Kind:     cmdspec.GoExpr,
				Examples: []cmdspec.Example{{Line: ":esc make([]byte, 32)", Says: "whether the backing array reaches the heap, in the compiler's words"}},
				See:      []string{":inline", ":bench"},
			},
			Summary: "whether the value reaches the heap",
			Detail: "Builds and never runs, so it costs a build but not the run. The answer\n" +
				"depends on context and says so: &T{} kept in a local that dies at the end\n" +
				"of its block does not escape, and the same expression returned from a\n" +
				"function does. For the flatter question — does this allocate — :bench's\n" +
				"allocs/op is the better tool: it is measured, and context-free.",
			Run: func(c *Core, arg string) Result { return c.escape(arg) },
			MCP: "go_escape",
		},
		{
			Name: ":inline", Arg: "<exp|func>", Group: groupInspect,
			Usage: cmdspec.Spec{
				Kind:   cmdspec.GoExpr,
				Params: []cmdspec.Param{{Name: "exp|func", Help: "a line whose calls to report, or a function this session declared"}},
				Examples: []cmdspec.Example{
					{Line: ":inline add(1, 2)", Says: "every call on the line, and whether it was flattened"},
					{Line: ":inline big", Says: "whether a function you declared can be inlined, and its cost"},
				},
				See: []string{":esc", ":asm"},
			},
			Summary: "what the compiler decided about inlining",
			Detail: "With an expression, reports every call on that line the compiler did or\n" +
				"did not flatten. With the name of a function this session declared, reports\n" +
				"whether it can be inlined at all, what it costs, and — when it cannot — the\n" +
				"compiler's own reason, which is usually a cost against a budget.\n\n" +
				"It builds and never runs, exactly as :esc does: inlining is decided at\n" +
				"compile time, so there is nothing an execution could add. The two commands\n" +
				"are the same build asked for a different half of its output.\n\n" +
				"The wording is the compiler's, not a paraphrase of it. A decision about a\n" +
				"function declared on an earlier line is answered by naming that function,\n" +
				"not by asking about a line that happens to call it.",
			Run: func(c *Core, arg string) Result { return c.inline(arg) },
			MCP: "go_inline",
		},
		{
			Name: ":asm", Arg: "<func>", Group: groupInspect,
			Usage: cmdspec.Spec{
				Kind:     cmdspec.GoName,
				Params:   []cmdspec.Param{{Name: "func", Help: "a function or method this session declared: F, T.M or (*T).M"}},
				Examples: []cmdspec.Example{{Line: ":asm add"}, {Line: ":asm (*Stack).Push"}},
				See:      []string{":inline", ":esc"},
			},
			Summary: "the assembly emitted for one declared function",
			Detail: "Shows the listing for one function or method this session declared, and\n" +
				"nothing else. A method is named T.M or (*T).M; either spelling finds it,\n" +
				"because which one is right is the receiver's business rather than yours.\n\n" +
				"Every position in the listing names the entry and line it came from, so no\n" +
				"path under the temp directory appears. Long listings open in a pane; the\n" +
				"same bytes are always in the linear output too.\n\n" +
				"Only the session's own functions. The listing flag applies to the package\n" +
				"named on the command line, deliberately — widening it to the standard\n" +
				"library would recompile it under different flags and throw away the warm\n" +
				"build cache every line in this REPL depends on. So :asm strings.Index is\n" +
				"refused rather than answered slowly.\n\n" +
				"A function inlined at every call site is dropped by the linker and has no\n" +
				"listing of its own. That is reported as what it is.",
			Run: func(c *Core, arg string) Result { return c.asm(arg) },
			MCP: "go_asm",
		},
		{
			Name: ":vet", Arg: "[exp]", Group: groupInspect,
			Usage: cmdspec.Spec{
				Kind: cmdspec.GoExpr,
				Examples: []cmdspec.Example{
					{Line: ":vet", Says: "the session, each finding against the entry it belongs to"},
					{Line: `:vet fmt.Printf("%d", "x")`, Says: "the session with this line appended"},
				},
				See: []string{":race"},
			},
			Summary: "what go vet says about the session",
			Detail: "Runs the toolchain's own analysers over the session — with the expression\n" +
				"appended when you give one — and reports each finding against the entry and\n" +
				"line it belongs to. Nothing is built and nothing is run: vet type-checks the\n" +
				"package itself, which is why it costs about half a second rather than a\n" +
				"build.\n\n" +
				"Findings in the code gluon generates around your lines are never shown. One\n" +
				"there would be gluon's bug, and there is nothing you could do about it.\n\n" +
				"It vets the session, not the host. `go vet .` names the main package, and an\n" +
				"attached host's packages are compiled into it rather than analysed — their\n" +
				"findings belong to that project's own `go vet ./...`.\n\n" +
				"There is no :lint. staticcheck and its neighbours are dependencies gluon\n" +
				"does not have; vet is the analyser the toolchain already ships.",
			Run: func(c *Core, arg string) Result { return c.vet(arg) },
			MCP: "go_vet",
		},
		{
			Name: ":race", Arg: "<exp>", Group: groupInspect,
			Usage: cmdspec.Spec{
				Kind:     cmdspec.GoExpr,
				Examples: []cmdspec.Example{{Line: ":race incrementTwice()", Says: "any race in that run, each frame named by its entry"}},
				See:      []string{":vet", ":trace"},
			},
			Summary: "run it once under the race detector",
			Detail: "Builds the session with -race, evaluates the expression once, and names\n" +
				"every frame of any data race by the entry it came from. The expression's own\n" +
				"value is printed either way: a race is something that happened during the\n" +
				"run, not instead of it.\n\n" +
				"Finding nothing is not proof of anything, and the output says so every time.\n" +
				"The detector observes one interleaving of one run; a race that needs a\n" +
				"different schedule is still there.\n\n" +
				"The first :race on a machine compiles the race-instrumented standard\n" +
				"library, which takes several seconds. Later ones reuse it and cost about\n" +
				"what an ordinary line does. The instrumented binary is built beside the\n" +
				"ordinary one and goes with the session directory.\n\n" +
				"On a platform the detector does not support, the toolchain's own message is\n" +
				"shown and nothing is retried without the flag — a value from a run nothing\n" +
				"was watching, under a command called :race, would be worse than no answer.",
			Run: func(c *Core, arg string) Result { return c.race(arg) },
			MCP: "go_race",
		},
		{
			Name: ":env", Arg: "[pattern]", Group: groupInspect,
			Usage: cmdspec.Spec{
				Kind:   cmdspec.Words,
				Params: []cmdspec.Param{{Name: "pattern", Optional: true, Help: "part of a variable's name, any case"}},
				Examples: []cmdspec.Example{
					{Line: ":env"},
					{Line: ":env database", Says: "the variables whose name contains it, secrets masked"},
				},
				See: []string{":conf", ":db"},
			},
			Summary: "the process environment, redacted   (Rails.env's neighbour)",
			Detail: "Sorted by name, and filtered to the names containing pattern. Nothing is\n" +
				"built and nothing is run — this is gluon's own environment, which is what\n" +
				"every line it evaluates inherits.\n\n" +
				"A value is hidden when its *shape* says it carries a secret: a connection\n" +
				"string, a bearer token, a vendor-prefixed key. The name of the variable is\n" +
				"never consulted and never hidden — a NOTE holding a DSN is redacted and a\n" +
				"PASSWORD_HINT holding \"ask the team\" is not.\n\n" +
				"Shape is not a guarantee. A secret in a format gluon does not recognise is\n" +
				"shown; a value hidden that was not one is recoverable, because the name\n" +
				"beside it always is. There is no way to set a variable from here: it would\n" +
				"change every later line while leaving no trace in :src.",
			Run: func(c *Core, arg string) Result { return c.metaEnv(arg) },
		},
		{
			Name: ":conf", Arg: "[file]", Group: groupInspect,
			Usage: cmdspec.Spec{
				Kind:   cmdspec.Words,
				Params: []cmdspec.Param{{Name: "file", Optional: true, Help: "a TOML, YAML or JSON file"}},
				Examples: []cmdspec.Example{
					{Line: ":conf", Says: "the config files the project search found, and where it looked"},
					{Line: ":conf config/app.yaml", Says: "its keys, dotted, secrets masked"},
				},
				See: []string{":env", ":db", ":settings"},
			},
			Summary: "read a project config file, redacted",
			Detail: "With a file, reads TOML, YAML or JSON and lists its keys in dotted form —\n" +
				"database.primary.url — with the same shape-based redaction :env uses. A\n" +
				"file that does not parse shows no keys at all: a configuration displayed\n" +
				"as half read hides exactly the part that is missing.\n\n" +
				"With no file, lists what the project search found and where it looked, so\n" +
				"that \"found nothing\" and \"did not look\" are different answers. It is the\n" +
				"same bounded search :db runs, ceilinged at the repository root, and the\n" +
				"same cached result — paid for once per :use rather than per line.\n\n" +
				"It only reads a project's config. `gluon init` writes a project one, and\n" +
				":settings changes gluon's own.",
			Run: func(c *Core, arg string) Result { return c.metaConf(arg) },
		},
		{
			Name: ":inspect", Aliases: []string{":i"}, Arg: "[-n k] <e>", Group: groupInspect,
			Usage: cmdspec.Spec{
				Kind:  cmdspec.GoExpr,
				Flags: []cmdspec.Flag{{Name: "-n", Value: "k", Help: "describe k elements, for this one view"}},
				Examples: []cmdspec.Example{
					{Line: ":inspect rows", Says: "a view that scrolls; one line is left behind"},
					{Line: ":inspect -n 5000 rows"},
				},
				See: []string{":settings"},
			},
			Summary: "browse a large value in a view that scrolls",
			Detail: "Opens the one thing a printed table cannot be. It takes the whole screen,\n" +
				"prints nothing while it is up, and leaves one line in scrollback on close.\n" +
				"Through a pipe it prints the ordinary rendering instead, so nothing is lost.\n\n" +
				"-n k describes k elements of the collection for this one invocation, so a\n" +
				"long slice can be browsed whole without the session's value.items moving.\n" +
				"It is the item bound only: depth is not what a table of rows shows, and\n" +
				"the budget on the whole value still applies, so a collection of large\n" +
				"elements may stop short of k.",
			Run: func(c *Core, arg string) Result { return c.inspect(arg) },
		},
		{
			Name: ":http", Arg: "<m> <url>", Group: groupInspect,
			Usage: cmdspec.Spec{
				Kind:    cmdspec.Words,
				FlagsAt: cmdspec.After,
				Params: []cmdspec.Param{
					{Name: "method", Values: cmdspec.Values{Fixed: httpMethods, Fold: true}},
					{Name: "url", Help: "http:// or https://, written out whole"},
				},
				Flags: []cmdspec.Flag{
					{Name: "-H", Value: "header", Repeat: true, Help: "a header, written 'Name: value'; repeat it for another"},
					{Name: "-d", Value: "body", Help: "the request body"},
					{Name: "-t", Value: "duration", Help: "how long to wait: 5s, 500ms, 1m (" + defaultHTTPTimeout + " by default)"},
				},
				Examples: []cmdspec.Example{
					{Line: ":http GET https://api.github.com/zen"},
					{Line: `:http POST https://httpbin.org/post -d '{"a": 1}' -t 5s`},
					{Line: ":http GET https://api.github.com/user -H 'Authorization: Bearer $GH_TOKEN'", Says: "the token is named, so its value reaches no history file"},
				},
				See: []string{":grpc", ":env", ":query"},
			},
			Summary: "issue a request, and render what came back",
			Detail: "Quote anything with a space in it: no shell has been near the line.\n\n" +
				"A header value may name a variable rather than carry one. -H 'Authorization:\n" +
				"Bearer $TOKEN' resolves $TOKEN in the child, so the value reaches neither\n" +
				":src, nor :save, nor ~/.local/state/gluon/history. A value whose *shape*\n" +
				"says it is a credential is refused instead of sent, because by then the line\n" +
				"as typed is already in the history file and masking the display would not\n" +
				"take it back. That is the whole of what this protects: a token written into\n" +
				"an http.Get you typed yourself is not something gluon can catch.\n\n" +
				"The body is read to 64 KiB and the notice says what was skipped. A\n" +
				"non-textual content type is summarised — its type and size — rather than\n" +
				"printed.\n\n" +
				"Nothing survives between requests: no cookie jar, no reused connection, no\n" +
				"client. The child process dies after every line, so there is nowhere to keep\n" +
				"one, and a command implying otherwise would teach a lie.\n\n" +
				"The response is never cached. The cache is keyed on program text alone, so\n" +
				"the same request would otherwise answer with what it first saw.",
			Run: func(c *Core, arg string) Result { return c.metaHTTP(arg) },
		},

		{
			Name: ":use", Arg: "[dir|-off]", Group: groupModule,
			Usage: cmdspec.Spec{
				Kind:   cmdspec.Words,
				Params: []cmdspec.Param{{Name: "dir", Optional: true, Help: "a directory inside the module; . is where you are"}},
				Flags:  []cmdspec.Flag{{Name: "-off", Mode: true, Alone: true, Help: "detach, and be standalone again"}},
				Examples: []cmdspec.Example{
					{Line: ":use .", Says: "the module you are standing in, internal/ packages included"},
					{Line: ":use ~/src/acme/api"},
					{Line: ":use -off"},
				},
				See: []string{":reload", ":watch", ":get"},
			},
			Summary: "attach to the Go module at dir, so its packages import",
			Detail: "Attaches the session to the module containing dir, so its packages —\n" +
				"internal/ ones included — import like any other, and its requirements join\n" +
				"the build list, which may activate plugins. It says how many packages are\n" +
				"importable and re-runs a non-empty session against the module, so a break\n" +
				"shows now rather than on your next line; the attach is kept either way.\n\n" +
				"gluon -host <dir> does the same at startup.",
			Run: func(c *Core, arg string) Result { return c.use(arg) },
		},
		{
			Name: ":get", Arg: "[-rm] [module]", Group: groupModule,
			Usage: cmdspec.Spec{
				Kind:   cmdspec.Words,
				Params: []cmdspec.Param{{Name: "module", Optional: true, Values: cmdspec.Values{Source: cmdspec.GetAliases}}},
				Flags:  []cmdspec.Flag{{Name: "-rm", Mode: true, Help: "drop a requirement again, fetching nothing"}},
				Examples: []cmdspec.Example{
					{Line: ":get", Says: "what the session requires"},
					{Line: ":get uuid", Says: "a plugin's short name for github.com/google/uuid"},
					{Line: ":get -rm github.com/google/uuid"},
				},
				See: []string{":plugins", ":reset"},
			},
			Summary: "add or remove a module, or list them  (the only one that fetches)",
			Detail: "A command rather than a fallback: an automatic `go get` would mutate go.mod,\n" +
				"reach the network and take seconds without being asked, on a line that may\n" +
				"simply be a typo. Bare :get lists what the session already requires.\n\n" +
				"-rm drops a requirement again, without fetching anything. It refuses when a\n" +
				"session entry still imports a package of that module, naming the entries —\n" +
				"a command that left the session unable to build would be a worse answer\n" +
				"than a refusal you can undo with :drop. A module the attached host requires\n" +
				"is refused too: it belongs to the host's go.mod, which gluon never edits.\n\n" +
				"Removing a module drops everything the session derived from the old build\n" +
				"list — the type checker's export data, the import cache, every cached\n" +
				"result — and deactivates a plugin whose module has gone.",
			Run: func(c *Core, arg string) Result { return c.get(arg) },
		},
		{
			Name: ":reload", Group: groupModule,
			Usage: cmdspec.Spec{
				Kind:     cmdspec.NoArg,
				Examples: []cmdspec.Example{{Line: ":reload", Says: "the next line sees the attached module as it is on disk now"}},
				See:      []string{":watch", ":use"},
			},
			Summary: "re-read the attached module after editing it  (Rails' reload!)",
			Detail: "Drops everything the session derived from the host's source — the type\n" +
				"checker's export data, the import cache, and every cached result — so the\n" +
				"next line is answered against the code on disk now.\n\n" +
				"The result cache is the one that matters: it is keyed on program text\n" +
				"alone, so without this a line you have already run is answered from before\n" +
				"the edit, and looks exactly like a line that ran.\n\n" +
				"Nothing is built and nothing is run, and your entries are untouched.",
			Run: func(c *Core, _ string) Result { return c.reloadHost() },
		},
		{
			Name: ":watch", Arg: "[on|off]", Group: groupModule,
			Usage: cmdspec.Spec{
				Kind:   cmdspec.Words,
				Params: []cmdspec.Param{{Name: "on|off", Optional: true, Values: cmdspec.Values{Fixed: []string{"on", "off"}}}},
				Examples: []cmdspec.Example{
					{Line: ":watch on", Says: "reload whenever one of the host's .go files changes"},
					{Line: ":watch", Says: "whether it is on"},
				},
				See: []string{":reload", ":use"},
			},
			Summary: "reload by itself when the host's source changes",
			Detail: "Polls the attached module's .go files and runs :reload when one moves,\n" +
				"saying which file it saw. Off by default; a bare :watch reports the state.\n\n" +
				"It stats on an interval rather than linking a filesystem-notification\n" +
				"library, which is the same choice `gluon watch` makes. A reload never\n" +
				"interrupts an evaluation: it waits for the one in flight to finish.\n\n" +
				"It needs a terminal — through a pipe there is no event loop to deliver on.",
			Run: func(c *Core, arg string) Result { return c.watch(arg) },
		},
		{
			Name: ":db", Arg: "[name]", Group: groupModule,
			Usage: cmdspec.Spec{
				Kind:   cmdspec.Words,
				Params: []cmdspec.Param{{Name: "name", Optional: true, Values: cmdspec.Values{Source: cmdspec.Databases}}},
				Examples: []cmdspec.Example{
					{Line: ":db", Says: "each database the project configures, and where that came from"},
					{Line: ":db primary"},
				},
				See: []string{":query", ":conf", ":env"},
			},
			Summary: "the databases this project is configured to reach",
			Detail: "Reports what gluon can connect to, and where each answer came from.\n" +
				"Nothing is opened: :db reads configuration, and :query runs a statement.\n\n" +
				"A connection string is never printed with its password, and gluon never\n" +
				"stores one — a config names the variable or the file a secret lives in,\n" +
				"and the value is read at the moment a query runs.",
			Run: func(c *Core, arg string) Result { return c.metaDB(arg) },
		},
		{
			Name: ":query", Aliases: []string{":qq"}, Arg: "[flags] <sql>", Group: groupModule,
			Usage: cmdspec.Spec{
				Kind:   cmdspec.SQL,
				Params: []cmdspec.Param{{Name: "sql", Help: "one statement, sent exactly as written"}},
				Flags: []cmdspec.Flag{
					{Name: "-d", Value: "name", Values: cmdspec.Values{Source: cmdspec.Databases},
						Help: "which database, when the project configures several"},
					{Name: "-w", Help: "allow a statement that writes"},
					{Name: "-json", Help: "rows as JSON, under gluon -e and through a pipe"},
				},
				Examples: []cmdspec.Example{
					{Line: ":query SELECT id, email FROM users ORDER BY id LIMIT 5"},
					{Line: ":query -d analytics SELECT count(*) FROM events"},
					{Line: ":query -w UPDATE users SET plan = 'pro' WHERE id = 7"},
					{Line: ":query -json SELECT id, email FROM users", Says: "column names, declared types and rows; NULL is null"},
				},
				See: []string{":db", ":conf"},
			},
			Summary: "run a statement against the project's database",
			Detail: "-d <name> picks one when a project configures several; with one, it is\n" +
				"the one.\n\n" +
				"The driver comes from the host module's own build list — gluon never links\n" +
				"one, exactly as it never links gorm. Without a driver the message says which\n" +
				"module to add.\n\n" +
				"Read-only by default: the first word must be SELECT, WITH, EXPLAIN, SHOW,\n" +
				"PRAGMA or DESCRIBE, and -w runs anything. That is a keyword check and not a\n" +
				"parse — it is there to catch the paste you did not mean, not to prove a\n" +
				"statement safe. The statement itself is never rewritten, so no LIMIT is\n" +
				"added to a query that may already have one.\n\n" +
				"The result is never cached. The cache is keyed on program text alone, so the\n" +
				"same query would otherwise answer with the rows it first saw.\n\n" +
				"-json answers with a structured envelope instead of a table, under gluon -e\n" +
				"and through a pipe: the column names, the type the database declared for\n" +
				"each, and the rows as arrays of cells. A cell that was SQL NULL is JSON\n" +
				"null, which a rendered table cannot say. At a terminal the table is printed\n" +
				"as always and a line says where the flag applies.",
			Run: func(c *Core, arg string) Result { return c.metaQuery(arg) },
		},

		{
			Name: ":src", Group: groupTranscript,
			Usage: cmdspec.Spec{
				Kind:     cmdspec.NoArg,
				Examples: []cmdspec.Example{{Line: ":src", Says: "the whole program the session renders into"}},
				See:      []string{":save", ":edit", ":share"},
			},
			Summary: "show the real Go program your session became",
			Run: func(c *Core, _ string) Result {
				src, err := c.source()
				if err != nil {
					return Result{Out: "error: " + err.Error(), Err: true}
				}
				text := strings.TrimRight(src, "\n")
				return sourceResult("the program this session became", text, syntax.Go)
			},
			MCP: "session_source", Static: true,
		},
		{
			Name: ":undo", Group: groupTranscript,
			Usage: cmdspec.Spec{
				Kind:     cmdspec.NoArg,
				Examples: []cmdspec.Example{{Line: ":undo"}},
				See:      []string{":drop", ":hist", ":bookmark"},
			},
			Summary: "drop the last entry",
			Detail: "Removes the newest entry and runs the session without it. It undoes the\n" +
				"entry, not its effects: a line that wrote a file or sent a request did so,\n" +
				"and the replay simply stops doing it again.",
			Run: func(c *Core, _ string) Result { return c.undo() },
			MCP: "session_undo",
		},
		{
			Name: ":test", Arg: "[-table] <exp>", Group: groupTranscript,
			Usage: cmdspec.Spec{
				Kind:  cmdspec.GoExpr,
				Flags: []cmdspec.Flag{{Name: "-table", Help: "the same assertion, shaped so a second case can join"}},
				Examples: []cmdspec.Example{
					{Line: `:test strings.ToUpper("hi")`, Says: "a Go test asserting the value it has now"},
					{Line: `:test -table strings.Fields(" a b ")`},
				},
				See: []string{":save", ":diff"},
			},
			Summary: "the session's own judgement, written down as a Go test",
			Detail: "You tried an expression, you looked at the answer, and you decided the\n" +
				"answer was right. That is got, want and a judgement, which is the whole\n" +
				"content of a Go test — and gluon already holds the expression, its type\n" +
				"and its value, so the only thing left is the typing.\n\n" +
				"The expectation is the value observed now, and the doc comment says so:\n" +
				"the test records what happened, and does not claim it was correct. That\n" +
				"judgement stays yours, which is why this prints rather than writes.\n" +
				":save -test keeps what has been generated.\n\n" +
				"A value with no literal form — a pointer, a channel, a func — is refused\n" +
				"with the part of the value that caused it, rather than emitted as a test\n" +
				"that does not compile. A value whose type is not comparable with == gets\n" +
				"reflect.DeepEqual and a comment saying why.",
			Run: func(c *Core, arg string) Result { return c.metaTest(arg) },
			MCP: "go_test",
		},
		{
			Name: ":save", Arg: "[flags|topic]", Group: groupTranscript,
			Usage: cmdspec.Spec{
				Kind:   cmdspec.Words,
				Params: []cmdspec.Param{{Name: "topic", Optional: true, Help: "names the directory; bare, in a scratchpad, refreshes it"}},
				Flags: []cmdspec.Flag{
					{Name: "-debug", Help: "add a debugger launch configuration beside it"},
					{Name: "-test", Help: "write what :test generated as a test file"},
				},
				Examples: []cmdspec.Example{
					{Line: ":save parser-bug", Says: "a runnable module, with its own go.mod so gopls works in it"},
					{Line: ":save -debug parser-bug"},
					{Line: ":save -test"},
				},
				See: []string{":scratch", ":test", ":share"},
			},
			Summary: "write the session out as a runnable scratch module",
			Detail: "A directory with its own go.mod, so gopls stays alive in it — completion,\n" +
				"hover, jump-to-definition. The topic names the directory. Flags read\n" +
				"before it, so both fit on one line: :save -debug parser-bug.\n\n" +
				"-debug writes a debugger launch configuration beside the module and names\n" +
				"the command that starts a session from a terminal. It is the answer to\n" +
				"\"I need to step through this\": gluon compiles each line into a program\n" +
				"that exits, so there is no process to pause, and the way out is a real\n" +
				"debugger on a real module.\n\n" +
				"-test writes whatever :test has generated as a test file beside the\n" +
				"program. The program itself is byte-for-byte what a plain :save writes.\n\n" +
				"Inside a scratchpad a bare :save refreshes that scratchpad's own program\n" +
				"in place, so gopls and a debugger open the work in progress rather than a\n" +
				"dated copy of it. :save <topic> writes a new dated scratch either way.",
			Run: func(c *Core, arg string) Result { return c.save(arg) },
		},
		{
			Name: ":scratch", Arg: "[flag|name]", Group: groupTranscript,
			Usage: cmdspec.Spec{
				Kind:     cmdspec.Words,
				Synopsis: "[name] | -rm <name> | -mv <name> | -off | -edit",
				Params:   []cmdspec.Param{{Name: "name", Optional: true, Values: cmdspec.Values{Source: cmdspec.Pads}}},
				Flags: []cmdspec.Flag{
					{Name: "-rm", Mode: true, Help: "show a scratchpad, then remove it once you confirm"},
					{Name: "-mv", Mode: true, Help: "rename the one you are in"},
					{Name: "-off", Mode: true, Alone: true, Help: "stop writing this session down"},
					{Name: "-edit", Mode: true, Alone: true, Help: "open the scratchpad's own file, pins and all"},
					// What the confirmation view submits. Typing it is not a way in.
					{Name: "-force", Hidden: true},
				},
				Examples: []cmdspec.Example{
					{Line: ":scratch", Says: "every scratchpad, opened from a view"},
					{Line: ":scratch parser-bug", Says: "open it, creating it if it is new; it replays"},
					{Line: ":scratch -mv parser-fix"},
					{Line: ":scratch -rm old-idea"},
					{Line: ":scratch -off"},
					{Line: ":scratch -edit"},
				},
				See: []string{":save", ":bookmark"},
			},
			Summary: "the scratchpad this session is saved in, and the way between them",
			Detail: `A scratchpad is a named session on disk. gluon lands on ` + "`default`" + `, and
every line you type into it is written down — the entries as you typed
them, what is pinned, the host you are attached to, and the modules the
session acquired. Tomorrow's gluon opens it and you are where you left
off.

Opening replays. The entries are installed as they were recorded and run
as one program — one build for the whole session, not one per entry — so
an entry that had effects has them again unless it is pinned, and pins
are part of what is recorded. ` + "`gluon scratch show <name>`" + ` reads one
without running any of it.

Nothing is fetched. A recorded module is restored from the local module
cache and never from the network; one the cache no longer holds is named
along with the :get line that would bring it back. :get stays the only
command that goes online.

A bare :save inside a scratchpad refreshes that scratchpad's own
program, so gopls and a debugger open the work in progress. :save
<topic> writes a new dated scratch, exactly as before.

A scratchpad that will not open is never written to: the session
continues, the reason is said again when your next line lands, and the
file is left byte-for-byte as it was.`,
			Run: func(c *Core, arg string) Result { return c.scratchCmd(arg) },
		},
		{
			Name: ":hist", Group: groupTranscript,
			Usage: cmdspec.Spec{
				Kind:     cmdspec.NoArg,
				Examples: []cmdspec.Example{{Line: ":hist", Says: "the entries, numbered for :drop and :pin"}},
				See:      []string{":drop", ":pin", ":replay"},
			},
			Summary: "number the session's entries",
			Run:     func(c *Core, _ string) Result { return Result{Out: c.hist()} },
			MCP:     "session_history", Static: true,
		},
		{
			Name: ":drop", Arg: "<n>", Group: groupTranscript,
			Usage: cmdspec.Spec{
				Kind:     cmdspec.Words,
				Params:   []cmdspec.Param{{Name: "n", Values: cmdspec.Values{Source: cmdspec.Entries}}},
				Examples: []cmdspec.Example{{Line: ":drop 3"}},
				See:      []string{":undo", ":hist", ":pin"},
			},
			Summary: "remove one of them",
			Detail: "Removes one entry by the number :hist shows and runs the rest. It is\n" +
				"refused when a later line reads `it`, or an _N the removal would renumber,\n" +
				"because that line would silently start meaning something else. If what is\n" +
				"left no longer builds, the entry is put back.",
			Run: func(c *Core, arg string) Result { return c.drop(arg) },
		},
		{
			Name: ":pin", Arg: "[n]", Group: groupTranscript,
			Usage: cmdspec.Spec{
				Kind:   cmdspec.Words,
				Params: []cmdspec.Param{{Name: "n", Optional: true, Values: cmdspec.Values{Source: cmdspec.Entries}}},
				Examples: []cmdspec.Example{
					{Line: ":pin 2", Says: "entry 2 has happened; replay stops running it"},
					{Line: ":pin", Says: "what is pinned"},
				},
				See: []string{":unpin", ":refresh", ":hist"},
			},
			Summary: "stop an entry re-running on replay",
			Detail: `Every line re-renders the whole session and runs it again, so a line
that fetched a URL or wrote a file does it again on every subsequent
line. :pin <n> takes that entry out of the program: it has already
happened, and it will not happen again.

A bare :pin lists what is pinned. :unpin <n> puts one back.

It is refused when a later entry uses what the pinned one bound, and
says which entry that is. gluon does not invent the missing value —
the printer's output is a display form, not a literal, so rebuilding
one from it would be a guess.`,
			Run: func(c *Core, arg string) Result { return c.pin(arg) },
		},
		{
			Name: ":unpin", Arg: "<n>", Group: groupTranscript,
			Usage: cmdspec.Spec{
				Kind:     cmdspec.Words,
				Params:   []cmdspec.Param{{Name: "n", Values: cmdspec.Values{Source: cmdspec.Pinned}}},
				Examples: []cmdspec.Example{{Line: ":unpin 2"}},
				See:      []string{":pin", ":refresh"},
			},
			Summary: "put a pinned entry back into the program",
			Run:     func(c *Core, arg string) Result { return c.unpin(arg) },
		},
		{
			Name: ":refresh", Group: groupTranscript,
			Usage: cmdspec.Spec{
				Kind:     cmdspec.NoArg,
				Examples: []cmdspec.Example{{Line: ":refresh", Says: "the pinned entries run once more; the pins stay"}},
				See:      []string{":pin", ":reload"},
			},
			Summary: "run the pinned entries once more",
			Detail: `:pin says an entry has already happened, so replay stops running it.
That is right until the code underneath it changes, at which point the
session is holding a result computed against source that is gone.

:refresh runs the program once with every pin lifted, and puts the pins
back afterwards whether it succeeded or not. It is never served from the
cached results: the program text is identical to last time — that is what
a replay is — and the answer is what may have changed.

Pinned entries were pinned for a reason. Running them again does whatever
they did the first time.`,
			Run: func(c *Core, _ string) Result { return c.refresh() },
			MCP: "session_refresh",
		},
		{
			Name: ":load", Arg: "<file>", Group: groupTranscript,
			Usage: cmdspec.Spec{
				Kind:     cmdspec.Words,
				Params:   []cmdspec.Param{{Name: "file", Help: "Go source, entered one construct at a time"}},
				Examples: []cmdspec.Example{{Line: ":load snippets/setup.go"}},
				See:      []string{":edit", ":buf", ":replay"},
			},
			Summary: "read a file in, line by line",
			Detail: "Reads the file and submits it one construct at a time, as though each were\n" +
				"typed, stopping at the first that fails. An unfinished construct at the end\n" +
				"is left out rather than reported against text that may still be being\n" +
				"written. The file is read once; :load it again after editing it.",
			Run: func(c *Core, arg string) Result { return c.load(arg) },
		},
		{
			Name: ":bookmark", Arg: "[name]", Group: groupTranscript,
			Usage: cmdspec.Spec{
				Kind:   cmdspec.Words,
				Params: []cmdspec.Param{{Name: "name", Optional: true, Values: cmdspec.Values{Source: cmdspec.Bookmarks}}},
				Examples: []cmdspec.Example{
					{Line: ":bookmark before-refactor"},
					{Line: ":bookmark", Says: "the snapshots taken this session"},
				},
				See: []string{":branch", ":restore", ":scratch"},
			},
			Summary: "snapshot the session under a name, or list the snapshots",
			Detail: `A snapshot of the entries, their order and what is pinned, recorded
under a name and taking nothing away from the session you are in.
:restore <name> puts it back. A bare :bookmark lists what has been
taken.

Snapshots last for this session and no longer. Nothing about them
reaches the disk, so a new gluon starts with none — :scratch is what
makes a session durable, and it keeps the entries themselves rather
than the program they render into.

Reusing a name replaces what was under it, and the line says so.`,
			Run: func(c *Core, arg string) Result { return c.bookmark(arg) },
		},
		{
			Name: ":branch", Arg: "[name]", Group: groupTranscript,
			Usage: cmdspec.Spec{
				Kind:   cmdspec.Words,
				Params: []cmdspec.Param{{Name: "name", Optional: true}},
				Examples: []cmdspec.Example{
					{Line: ":branch", Says: "a snapshot under a name made up for you, printed"},
					{Line: ":branch try-generics"},
				},
				See: []string{":bookmark", ":restore"},
			},
			Summary: "snapshot and keep going, naming it for you if you like",
			Detail: `:bookmark, for the case where you had not decided to bookmark. It
records where the session is and leaves it exactly there, so the state
before an experiment is recoverable without having thought of it first
— and with no name it generates a short one and prints it, because
having to invent a name is the thing it exists to remove.

Snapshots last for this session and no longer. :scratch is what makes
a session durable.`,
			Run: func(c *Core, arg string) Result { return c.branch(arg) },
		},
		{
			Name: ":restore", Arg: "<name>", Group: groupTranscript,
			Usage: cmdspec.Spec{
				Kind:     cmdspec.Words,
				Params:   []cmdspec.Param{{Name: "name", Values: cmdspec.Values{Source: cmdspec.Bookmarks}}},
				Examples: []cmdspec.Example{{Line: ":restore before-refactor"}},
				See:      []string{":bookmark", ":branch"},
			},
			Summary: "put a snapshot back, and replay it",
			Detail: `Replaces the session with the snapshot and runs it, reinstating the
entries, their order and what was pinned.

If the replay fails, the session you were leaving is put back and the
line says so: a restore that could destroy the state it was moving away
from would be worse than no restore at all. It is the same path :edit
uses to reload an edited session.

An unknown name lists the ones there are.

Snapshots last for this session and no longer; :scratch is what makes
a session durable.`,
			Run: func(c *Core, arg string) Result { return c.restore(arg) },
		},
		{
			Name: ":replay", Arg: "<pattern>", Group: groupTranscript,
			Usage: cmdspec.Spec{
				Kind:   cmdspec.Words,
				Params: []cmdspec.Param{{Name: "pattern", Help: "text in the history line, any case"}},
				Flags:  []cmdspec.Flag{{Name: "-run", Help: "bring them in without showing them first"}},
				Examples: []cmdspec.Example{
					{Line: ":replay http.Get", Says: "the matching lines, shown before any of them runs"},
					{Line: ":replay -run strings.Fields"},
				},
				See: []string{":hist", ":load"},
			},
			Summary: "find lines in the persisted history and bring them in",
			Detail: `Ctrl-R searches the history one line at a time and :hist numbers the
session; neither reaches the five thousand lines in the file, so the
expression you got right in yesterday's session is somewhere you cannot
get to without a text editor. This searches that file, case
insensitively, and brings the matches into the session in the order the
file holds them.

They are shown before any of them runs. A history line is a line
somebody typed, some of them wrote something, and the session replays
every entry on every later line — so what is about to happen is printed
verbatim rather than counted. Enter in the view brings them in; q
leaves without running anything.

The view submits :replay -run <pattern>, which is also the form to type
when you already know what the pattern matches, and the form a pipe has
— confirming needs a terminal, but listing does not.

Meta commands are skipped. History holds them as typed, and a line
beginning with a colon is not a session entry: replaying :reset would
clear the session it was being replayed into.`,
			Run: func(c *Core, arg string) Result { return c.replay(arg) },
		},
		{
			Name: ":share", Group: groupTranscript,
			Usage: cmdspec.Spec{
				Kind: cmdspec.NoArg,
				// What the confirmation view submits once the program has been read.
				// Typing it is not a way past the reading.
				Flags:    []cmdspec.Flag{{Name: shareConfirmFlag, Value: "token", Hidden: true}},
				Examples: []cmdspec.Example{{Line: ":share", Says: "the program, then a question; nothing is sent until you answer"}},
				See:      []string{":src", ":save"},
			},
			Summary: "show the program, then publish it to " + shareService,
			Detail: `The program :src shows, uploaded to ` + shareService + `, with the
address it can be read at. It is the answer to "look at this" — the path
that is otherwise :save, open the file, copy it, paste it into a browser.

The upload is public and cannot be withdrawn. That is why the
confirmation shows the program itself rather than a count of its lines:
what you read is byte-for-byte what is sent. There is no flag and no
setting that skips it, because a confirmation that can be pre-authorised
is one that has stopped being asked.

Only the rendered program goes. Not the history, not the environment,
and not the attached module's source — the program names a host's
packages, and naming them is not uploading them.

A program carrying anything that looks like a credential is refused
outright, naming the line, rather than warned about. Sharing is the one
thing here that cannot be undone.

Confirming needs a terminal. Through a pipe it prints what would have
gone and uploads nothing.`,
			Run: func(c *Core, arg string) Result { return c.share(arg) },
			// No MCP name, in either tier. Invariant 28 is about tools that
			// evaluate; this is a different risk, and --eval's documented
			// meaning is about running code rather than about publishing it.
			// The exclusion is the one this file already names: a tool caller
			// does not own the decision to put the user's code on a public
			// URL.
		},
		{
			Name: ":edit", Group: groupTranscript,
			Usage: cmdspec.Spec{
				Kind:     cmdspec.NoArg,
				Examples: []cmdspec.Example{{Line: ":edit"}},
				See:      []string{":buf", ":src", ":load"},
			},
			Summary: "open the session in $EDITOR and reload it  (pry's edit)",
			Detail: "Opens what you typed — the entries, not the program they render into — in\n" +
				"$EDITOR, or in gluon's own editor with editor = \"builtin\", and replaces the\n" +
				"session with the file when it closes. If the edited session does not\n" +
				"replay, the one you had is put back. :src is the real program, and :buf is\n" +
				"somewhere to write code that has not run yet.",
			Run: func(c *Core, _ string) Result { return c.edit() },
		},
		{
			Name: ":buf", Arg: "[flag]", Group: groupTranscript,
			Usage: cmdspec.Spec{
				Kind: cmdspec.Words,
				Flags: []cmdspec.Flag{
					{Name: "-check", Mode: true, Alone: true, Help: "does it compile against the session? builds nothing"},
					{Name: "-run", Mode: true, Alone: true, Runs: true, Help: "evaluate it — what closing the editor does"},
					{Name: "-show", Mode: true, Alone: true, Help: "print it"},
					{Name: "-clear", Mode: true, Alone: true, Help: "empty it"},
				},
				Examples: []cmdspec.Example{
					{Line: ":buf", Says: "open it in the editor; closing the editor runs it"},
					{Line: ":buf -check"},
					{Line: ":buf -run"},
					{Line: ":buf -show"},
					{Line: ":buf -clear"},
				},
				See: []string{":edit", ":load"},
			},
			Summary: "a buffer you edit and run as one evaluation",
			Detail: "A place to write Go that has not run yet. :buf opens it in your\n" +
				"editor — the same one :edit uses — and closing the editor runs the\n" +
				"whole buffer as a single evaluation: one build for however many\n" +
				"constructs it holds, exactly as a pasted block costs one.\n\n" +
				"It is not :edit. :edit opens the session and replaces it; the buffer\n" +
				"is somewhere else entirely, and running it appends. What you write\n" +
				"stays there across opens, so you can run it, fix it and run it\n" +
				"again; nothing about it reaches the disk when the session ends.\n\n" +
				"-check is an opinion, not a gate. When the type checker cannot\n" +
				"answer it says so rather than guessing, and -run never asks it:\n" +
				"the compiler decides, which is why there is nothing to override.",
			Run: func(c *Core, arg string) Result { return c.buffer(arg) },
		},
		{
			Name: ":time", Arg: "[on|off]", Group: groupTranscript,
			Usage: cmdspec.Spec{
				Kind:   cmdspec.Words,
				Params: []cmdspec.Param{{Name: "on|off", Optional: true, Values: cmdspec.Values{Fixed: []string{"on", "off"}}}},
				Examples: []cmdspec.Example{
					{Line: ":time on", Says: "every line reports gofmt, type check, build and run"},
					{Line: ":time off"},
				},
				See: []string{":bench"},
			},
			Summary: "show where each line's latency went",
			Detail: "With it on, every line reports where its latency went: gofmt, the type\n" +
				"check, the build and the run. A line the checker answers shows no build,\n" +
				"which is the whole design, measured.",
			Run: func(c *Core, arg string) Result { return c.time(arg) },
		},
	}
}

// Commands is every command this Core dispatches: the builtins, plus whatever
// the active plugins contribute.
func (c *Core) Commands() []Command {
	if len(c.extra) == 0 {
		return builtins
	}
	out := make([]Command, 0, len(builtins)+len(c.extra))
	out = append(out, builtins...)
	out = append(out, c.extra...)
	return out
}

// lookup resolves a name or alias.
func (c *Core) lookup(name string) (Command, bool) {
	for _, cmd := range c.Commands() {
		if cmd.Name == name {
			return cmd, true
		}
		for _, a := range cmd.Aliases {
			if a == name {
				return cmd, true
			}
		}
	}
	return Command{}, false
}

// MetaNames is the completion list: every name and every alias that is not
// already reachable by completing the canonical name.
//
// Generated rather than maintained, which is the point of the registry: a
// command added and not offered was previously a silent omission.
//
// Cached, because this sits on the keystroke path whose whole budget is 12-22µs
// and building it costs ~1µs. The command set changes only when plugins
// activate, which is :get and :use — invalidateCommands is what says so.
// Invariant 15 keeps this free of locks: completion is never asked while an
// evaluation runs, and nothing else writes it.
func (c *Core) MetaNames() []string {
	if c.metaNames == nil {
		c.metaNames = c.computeMetaNames()
	}
	return c.metaNames
}

// invalidateCommands drops what is derived from the command set, and the cached
// completion values with it: both are answers about what this session can see,
// and :get and :use are the two points where that changes.
func (c *Core) invalidateCommands() {
	c.metaNames, c.comp.values = nil, nil
}

func (c *Core) computeMetaNames() []string {
	var out []string
	for _, cmd := range c.Commands() {
		out = append(out, cmd.Name)
		for _, a := range cmd.Aliases {
			// ":q" already completes ":quit" away, so offering both is noise.
			if !strings.HasPrefix(a, cmd.Name) {
				out = append(out, a)
			}
		}
	}
	sort.Strings(out)
	return out
}
