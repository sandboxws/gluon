package repl

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/sandboxws/gluon/internal/plugin"
	"github.com/sandboxws/gluon/internal/plugins"
	"github.com/sandboxws/gluon/internal/pretty"
)

// bareSet is the plugin set of a session with nothing in its build list — the
// state in which every third-party plugin has to explain itself.
func bareSet() *plugin.Set {
	set := plugin.NewSet(plugins.Builtin())
	set.Activate(nil, nil)
	return set
}

// TestPluginListKeepsItsColumns: every row's reason starts in the same
// column, however long the module path before it — the longest ran into the
// reason when the widths were fixed.
func TestPluginListKeepsItsColumns(t *testing.T) {
	c := &Core{plugins: bareSet()}
	at := -1
	for _, line := range strings.Split(c.pluginList().Out, "\n") {
		f := strings.Fields(line)
		// A plugin's row, and not the key under the list.
		if len(f) < 4 || (f[0] != "·" && f[0] != "✓") || f[1] == "active" {
			continue
		}
		// The module is the third field; the reason starts two spaces after
		// the module column, whatever the module's own length.
		i := strings.Index(line, f[2]) + len(f[2])
		for i < len(line) && line[i] == ' ' {
			i++
		}
		i = utf8.RuneCountInString(line[:i]) // ✓ and · are not one byte each
		if at < 0 {
			at = i
		}
		if i != at {
			t.Errorf("the reason starts at column %d here, and %d above:\n%s", i, at, line)
		}
	}
	if at < 0 {
		t.Fatal("no plugin rows")
	}
}

// TestPluginListNamesWhatActivatesAnInactivePlugin. An absent command looks
// exactly like a broken install, so :plugins lists the plugin, its module, and
// the :get line that would turn it on.
func TestPluginListNamesWhatActivatesAnInactivePlugin(t *testing.T) {
	c := &Core{plugins: bareSet()}
	lines := map[string]string{}
	for _, line := range strings.Split(c.pluginList().Out, "\n") {
		if f := strings.Fields(line); len(f) > 1 {
			lines[f[1]] = line
		}
	}

	for name, module := range map[string]string{
		"yaml":     "go.yaml.in/yaml/v3",
		"yaml.v3":  "gopkg.in/yaml.v3",
		"toml":     "github.com/BurntSushi/toml",
		"msgpack":  "github.com/vmihailenco/msgpack/v5",
		"protobuf": "google.golang.org/protobuf",
	} {
		line, ok := lines[name]
		if !ok {
			t.Errorf("%s is not listed at all, which is how a plugin looks broken", name)
			continue
		}
		if !strings.HasPrefix(strings.TrimSpace(line), "·") {
			t.Errorf("%s is marked active with an empty build list: %q", name, line)
		}
		if !strings.Contains(line, module) || !strings.Contains(line, ":get "+module) {
			t.Errorf("%s does not say what activates it: %q", name, line)
		}
	}

	// The stdlib pair is on the other side of the same line.
	for _, name := range []string{"xml", "csv"} {
		line, ok := lines[name]
		if !ok {
			t.Fatalf("%s is not listed", name)
		}
		if !strings.HasPrefix(strings.TrimSpace(line), "✓") {
			t.Errorf("%s is not active on a bare session: %q", name, line)
		}
	}
}

// TestEncodingGetAliasesResolve is the other half: the short name :plugins
// prints has to be the one :get takes, or the activation instruction is a dead
// end. The plugin that knows the path is by definition the inactive one.
func TestEncodingGetAliasesResolve(t *testing.T) {
	c := &Core{plugins: plugin.NewSet(plugins.Builtin())}
	for short, want := range map[string]string{
		"yaml":     "go.yaml.in/yaml/v3",
		"yaml.v3":  "gopkg.in/yaml.v3",
		"toml":     "github.com/BurntSushi/toml",
		"msgpack":  "github.com/vmihailenco/msgpack/v5",
		"protobuf": "google.golang.org/protobuf",
	} {
		if got := c.resolveGetAlias(short); got != want {
			t.Errorf(":get %s resolved to %q, want %q", short, got, want)
		}
		// A version suffix survives the expansion.
		if got := c.resolveGetAlias(short + "@v1.2.3"); got != want+"@v1.2.3" {
			t.Errorf(":get %s@v1.2.3 resolved to %q", short, got)
		}
	}
}

// webFrameworks is every plugin that contributes :routes, with the module a
// go.mod really names and the short name :plugins prints. Seven frameworks
// share one command, so each of these facts is load-bearing for a different
// half of the same message.
var webFrameworks = []struct{ name, module, require string }{
	{"chi", "github.com/go-chi/chi", "github.com/go-chi/chi/v5 v5.0.12"},
	{"gin", "github.com/gin-gonic/gin", "github.com/gin-gonic/gin v1.10.0"},
	{"echo", "github.com/labstack/echo", "github.com/labstack/echo/v4 v4.12.0"},
	{"fiber", "github.com/gofiber/fiber", "github.com/gofiber/fiber/v2 v2.52.15"},
	{"hertz", "github.com/cloudwego/hertz", "github.com/cloudwego/hertz v0.10.6"},
	{"iris", "github.com/kataras/iris", "github.com/kataras/iris/v12 v12.2.0"},
	{"mux", "github.com/gorilla/mux", "github.com/gorilla/mux v1.8.1"},
}

// TestPluginListExplainsEveryAbsentWebFramework. :routes is the command a web
// developer reaches for first, and seven frameworks can supply it — so a
// session with none of them must say which module turns it on, for each, rather
// than looking like seven broken installs.
func TestPluginListExplainsEveryAbsentWebFramework(t *testing.T) {
	c := &Core{plugins: bareSet()}
	out := c.pluginList().Out

	lines := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) > 1 {
			lines[f[1]] = line
		}
	}
	for _, w := range webFrameworks {
		line, ok := lines[w.name]
		if !ok {
			t.Errorf("%s is not listed at all, which is how a plugin looks broken", w.name)
			continue
		}
		if !strings.HasPrefix(strings.TrimSpace(line), "·") {
			t.Errorf("%s is marked active with an empty build list: %q", w.name, line)
		}
		if !strings.Contains(line, ":get "+w.module) {
			t.Errorf("%s does not say what activates it: %q", w.name, line)
		}
	}

	// And the command itself is gone, not merely unusable.
	for _, cmd := range c.plugins.Commands() {
		if cmd.Name == ":routes" {
			t.Errorf(":routes is offered by %s on a bare session", cmd.Plugin)
		}
	}
}

// TestPluginListNamesTheDisplacedFramework. A service migrating between two
// frameworks has both in its go.mod, and gets a route table from one of them.
// Which one is the question :plugins has to answer, or an unexpected table has
// no explanation at all.
func TestPluginListNamesTheDisplacedFramework(t *testing.T) {
	set := plugin.NewSet(plugins.Builtin())
	set.Activate([]string{
		"github.com/go-chi/chi/v5 v5.0.12",
		"github.com/gorilla/mux v1.8.1",
	}, nil)
	// Conflicts are what the last Commands call had to resolve, which is how
	// the REPL reaches this state too.
	set.Commands()

	out := (&Core{plugins: set}).pluginList().Out
	if !strings.Contains(out, ":routes is chi's; mux also defines it") {
		t.Errorf("the conflict is not explained:\n%s", out)
	}
	for _, name := range []string{"chi", "mux"} {
		if !strings.Contains(out, name) {
			t.Errorf("%s is not named in the listing:\n%s", name, out)
		}
	}
}

// TestWebGetAliasesResolve. The short name the inactive listing prints has to
// be the one :get takes, or the activation instruction is a dead end — and the
// plugin that knows the path is by definition the inactive one.
func TestWebGetAliasesResolve(t *testing.T) {
	c := &Core{plugins: plugin.NewSet(plugins.Builtin())}
	for short, want := range map[string]string{
		"chi":   "github.com/go-chi/chi/v5",
		"gin":   "github.com/gin-gonic/gin",
		"echo":  "github.com/labstack/echo/v4",
		"fiber": "github.com/gofiber/fiber/v2",
		"hertz": "github.com/cloudwego/hertz",
		"iris":  "github.com/kataras/iris/v12",
		"mux":   "github.com/gorilla/mux",
	} {
		if got := c.resolveGetAlias(short); got != want {
			t.Errorf(":get %s resolved to %q, want %q", short, got, want)
		}
		if got := c.resolveGetAlias(short + "@v1.2.3"); got != want+"@v1.2.3" {
			t.Errorf(":get %s@v1.2.3 resolved to %q", short, got)
		}
	}

	// Every alias resolves to a path the plugin's Module is a prefix of, or
	// `:get <short>` adds a module that does not activate the plugin that
	// named it — the dead end this test exists to close, one indirection later.
	for _, w := range webFrameworks {
		got := c.resolveGetAlias(w.name)
		if got != w.module && !strings.HasPrefix(got, w.module+"/") {
			t.Errorf(":get %s adds %q, which does not activate %s (module %q)",
				w.name, got, w.name, w.module)
		}
	}
}

// TestGuideForEntIsPrinted. :guide answered "the gorm plugin ships no guide"
// for every built-in until ent, whose whole reason for having one is that its
// command list cannot say which question it does not answer.
func TestGuideForEntIsPrinted(t *testing.T) {
	c := &Core{plugins: bareSet()}
	res := c.guide("ent")
	if res.Err {
		t.Fatalf(":guide ent errored: %s", res.Out)
	}
	if strings.Contains(res.Out, "ships no guide") {
		t.Fatalf(":guide ent fell through to the no-guide message: %s", res.Out)
	}
	for _, want := range []string{":sql", ":schema", "querySpec"} {
		if !strings.Contains(res.Out, want) {
			t.Errorf(":guide ent does not mention %q:\n%s", want, res.Out)
		}
	}
	// A guide is reachable for a plugin that is not active — which is the
	// normal case, since the point of reading one is deciding whether to
	// :get the module at all.
	if why := c.plugins.Why("ent"); !strings.Contains(why, ":get") {
		t.Errorf("ent is active in a bare session: %q", why)
	}
}

// TestPluginListNamesTheDisplacedMigrationLibrary.
//
// Here the conflict costs more than a name. goose reads goose_db_version and
// golang-migrate reads schema_migrations, so a session with both in its build
// list is being told which of its two tracking tables the answer came from —
// and the alternative, silence, is an answer from a tool the project may have
// stopped using.
func TestPluginListNamesTheDisplacedMigrationLibrary(t *testing.T) {
	set := plugin.NewSet(plugins.Builtin())
	set.Activate([]string{
		"github.com/pressly/goose/v3 v3.24.0",
		"github.com/golang-migrate/migrate/v4 v4.18.1",
	}, nil)
	set.Commands()

	out := (&Core{plugins: set}).pluginList().Out
	if !strings.Contains(out, ":migrations is goose's; migrate also defines it") {
		t.Errorf("the conflict is not explained:\n%s", out)
	}
}

// TestDataLayerGetAliasesResolve. The short name the inactive listing prints
// has to be the one :get takes, or every "not active — :get ..." line for
// these five is a dead end.
func TestDataLayerGetAliasesResolve(t *testing.T) {
	c := &Core{plugins: plugin.NewSet(plugins.Builtin())}
	for short, want := range map[string]string{
		"sqlx":    "github.com/jmoiron/sqlx",
		"pgx":     "github.com/jackc/pgx/v5",
		"ent":     "entgo.io/ent",
		"goose":   "github.com/pressly/goose/v3",
		"migrate": "github.com/golang-migrate/migrate/v4",
	} {
		if got := c.resolveGetAlias(short); got != want {
			t.Errorf(":get %s resolved to %q, want %q", short, got, want)
		}
	}
}

// TestPluginListNamesTheDisplacedContainerLibrary, for both shared names.
//
// dig and samber/do both want :services and :graph, and neither can answer for
// the other's container: the rewrites name different methods, so the losing
// library's user gets a build error on their own line rather than a wrong table.
// That makes the listing the only place the displacement is visible, and it has
// to be visible for both commands — a tie-break that fell one way for :services
// and the other for :graph would be worse than either.
func TestPluginListNamesTheDisplacedContainerLibrary(t *testing.T) {
	set := plugin.NewSet(plugins.Builtin())
	set.Activate([]string{
		"github.com/samber/do/v2 v2.1.0",
		"go.uber.org/dig v1.19.0",
	}, nil)
	set.Commands()

	out := (&Core{plugins: set}).pluginList().Out
	for _, want := range []string{
		":services is do's; dig also defines it",
		":graph is do's; dig also defines it",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the conflict is not explained:\n%s\nwant %q", out, want)
		}
	}
}

// TestContainerPluginsAreListedInactiveWithTheirModule. Neither command exists
// without a container library, and a command that is simply absent looks exactly
// like a broken install — so the listing carries both plugins and the :get line
// that would turn each on.
func TestContainerPluginsAreListedInactiveWithTheirModule(t *testing.T) {
	c := &Core{plugins: bareSet()}
	lines := map[string]string{}
	for _, line := range strings.Split(c.pluginList().Out, "\n") {
		if f := strings.Fields(line); len(f) > 1 {
			lines[f[1]] = line
		}
	}
	for name, module := range map[string]string{
		"dig": "go.uber.org/dig",
		"do":  "github.com/samber/do",
	} {
		line, ok := lines[name]
		if !ok {
			t.Errorf("%s is not listed at all, which is how a plugin looks broken", name)
			continue
		}
		if !strings.HasPrefix(strings.TrimSpace(line), "·") {
			t.Errorf("%s is marked active with an empty build list: %q", name, line)
		}
		if !strings.Contains(line, ":get "+module) {
			t.Errorf("%s does not say what activates it: %q", name, line)
		}
	}
}

// TestContainerGetAliasesResolve. The short name the inactive listing prints has
// to be the one :get takes, or both "not active — :get ..." lines are dead ends.
func TestContainerGetAliasesResolve(t *testing.T) {
	c := &Core{plugins: plugin.NewSet(plugins.Builtin())}
	for short, want := range map[string]string{
		"dig": "go.uber.org/dig",
		"do":  "github.com/samber/do/v2",
	} {
		if got := c.resolveGetAlias(short); got != want {
			t.Errorf(":get %s resolved to %q, want %q", short, got, want)
		}
		// A version suffix survives the expansion.
		if got := c.resolveGetAlias(short + "@v1.2.3"); got != want+"@v1.2.3" {
			t.Errorf(":get %s@v1.2.3 resolved to %q", short, got)
		}
	}

	// Each alias has to resolve to a path the plugin's Module is a prefix of,
	// or `:get do` adds a module that does not activate the plugin that named
	// it — the dead end this test exists to close, one indirection later.
	for name, module := range map[string]string{
		"dig": "go.uber.org/dig",
		"do":  "github.com/samber/do",
	} {
		got := c.resolveGetAlias(name)
		if got != module && !strings.HasPrefix(got, module+"/") {
			t.Errorf(":get %s adds %q, which does not activate %s (module %q)",
				name, got, name, module)
		}
	}
}

// TestAContainerGraphLongerThanTheScreenIsPageable is invariant 19 for the two
// commands most likely to produce a long answer: a real dependency graph is
// dozens of lines, and dig's :graph carries a DOT description underneath it.
//
// adapt gives every Text command `Modal: pageable(...)` alongside the linear
// Out, so what is asserted here is the half that decides: past the threshold
// there is something to scroll, the modal holds the whole answer, and Out is
// unchanged either way — a pipe reading `gluon -e ':graph c'` must not lose the
// DOT because the terminal would have scrolled it. The end-to-end routing
// through a real evaluation is asserted in di_integration_test.go.
func TestAContainerGraphLongerThanTheScreenIsPageable(t *testing.T) {
	short := "dig — dependency graph\n\nnodes: {\n\t *app.DB -> deps: [], ctor: func() *app.DB\n}"
	if m := pageable(":graph c", short); m != nil {
		t.Errorf("a graph that fits opened a modal: %+v", m)
	}

	var b strings.Builder
	b.WriteString("dig — dependency graph\n\nnodes: {\n")
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "\t *app.T%d -> deps: [*app.DB], ctor: func(*app.DB) *app.T%d\n", i, i)
	}
	b.WriteString("}\n\nDOT:\ndigraph {\n\trankdir=RL;\n}")
	long := b.String()

	m := pageable(":graph c", long)
	if m == nil {
		t.Fatalf("a %d-line graph did not open a modal", strings.Count(long, "\n")+1)
	}
	if m.Text != long {
		t.Error("the modal does not hold the whole answer")
	}
	if !strings.Contains(m.Text, "DOT:") {
		t.Error("the DOT description did not reach the modal")
	}
	if !strings.Contains(m.Summary, ":graph c") {
		t.Errorf("the modal does not say what it is showing: %q", m.Summary)
	}
}

// TestEveryPluginExampleParses: a plugin's example is read by what answers
// the command — its Rewrite, the database argument :query shares, or the plan
// a live call makes before it dials. None of the three touches anything.
func TestEveryPluginExampleParses(t *testing.T) {
	for _, p := range plugins.Builtin() {
		cm, ok := p.(plugin.Commander)
		if !ok {
			continue
		}
		for _, pc := range cm.Commands() {
			for _, ex := range pc.Usage.Examples {
				_, arg := argOf(ex.Line)
				var err error
				switch {
				case pc.Query != nil:
					_, err = parseDBQueryArg(pc.Name, arg)
				case pc.Live != nil:
					_, err = pc.Live.Plan(arg)
				default:
					_, err = pc.Rewrite(arg)
				}
				if err != nil {
					t.Errorf("%s/%s: %q does not parse: %v", p.Meta().Name, pc.Name, ex.Line, err)
				}
			}
		}
	}
}

// TestPluginListNamesEachPluginsCommands: the listing is where somebody finds
// out what a library would add, so each plugin's commands are under its name,
// with its guide when it has one.
func TestPluginListNamesEachPluginsCommands(t *testing.T) {
	out := (&Core{plugins: bareSet()}).pluginList().Out
	for _, want := range []string{"    :sql", "    :schema · :guide ent", "    :services :graph"} {
		if !strings.Contains(out, want) {
			t.Errorf(":plugins does not list %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, ":help <command> explains any of these") {
		t.Error(":plugins does not say that :help reads an inactive plugin's command")
	}
}

// TestGuideForAnUnknownPlugin: "no such plugin" and "ships no guide" are
// different answers, and a typo deserves the first.
func TestGuideForAnUnknownPlugin(t *testing.T) {
	c := &Core{plugins: bareSet()}
	if res := c.guide("nope"); !res.Err || !strings.Contains(res.Out, "no plugin named nope") {
		t.Errorf(":guide nope: %q", res.Out)
	}
	if res := c.guide("gorm"); !res.Err || !strings.Contains(res.Out, "ships no guide") {
		t.Errorf(":guide gorm: %q", res.Out)
	}
}

// TestReferenceHasEveryProvider: the docs are generated from Reference, so it
// holds every builtin, and a command once per plugin that provides it.
func TestReferenceHasEveryProvider(t *testing.T) {
	ref := Reference()
	routes := map[string]bool{}
	names := map[string]bool{}
	for _, c := range ref {
		names[c.Name] = true
		if c.Name == ":routes" {
			routes[c.Plugin] = true
		}
	}
	if len(routes) != 7 {
		t.Errorf(":routes has %d providers in Reference, want 7: %v", len(routes), routes)
	}
	for _, b := range builtinCommands() {
		if !names[b.Name] {
			t.Errorf("%s is missing from Reference", b.Name)
		}
	}
}

// TestARendererSeesPluginsThatActivateLater: the terminal's renderer is
// installed as the TUI starts, and plugins activate afterwards — at -host,
// :use, :get, when a scratchpad restores its modules. It reads the hooks when
// it draws, so a renderer that arrived with a plugin is the one used.
func TestARendererSeesPluginsThatActivateLater(t *testing.T) {
	c := &Core{}
	installRender(c, pretty.PlainStyles(), pretty.Options{})
	v := []pretty.Value{{Type: "main.T", Kind: "struct"}}
	before := c.Render(v)
	c.hooks = map[string]pretty.Hook{"main.T": {Rich: func(pretty.Value, pretty.Styles) (string, bool) {
		return "drawn by a plugin", true
	}}}
	if got := c.Render(v); got == before || !strings.Contains(got, "drawn by a plugin") {
		t.Errorf("a renderer that arrived after the terminal's was installed was not used: %q", got)
	}
}
