package plugins

import (
	"go/parser"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/db"
	"github.com/sandboxws/gluon/internal/plugin"
)

// These run over every compiled-in plugin, so a new one is covered the moment
// it is added to Builtin.

// TestEveryRewriteProducesParseableGo is the cheap guard against a plugin whose
// command only fails when someone runs it — and then fails with a build error
// naming gluon rather than the plugin.
func TestEveryRewriteProducesParseableGo(t *testing.T) {
	for _, p := range Builtin() {
		cm, ok := p.(plugin.Commander)
		if !ok {
			continue
		}
		for _, c := range cm.Commands() {
			if c.Query != nil || c.Live != nil {
				// A command answered against the project's database declares
				// its statement instead of producing source, and one that
				// dials declares a plan. What has to hold for either is
				// checked below, in TestEveryPluginIsWellFormed.
				continue
			}
			src, err := c.Rewrite("x")
			if err != nil {
				t.Errorf("%s/%s rejected a plain argument: %v", p.Meta().Name, c.Name, err)
				continue
			}
			if _, err := parser.ParseExpr(src); err != nil {
				t.Errorf("%s/%s produced source that does not parse: %v\n%s",
					p.Meta().Name, c.Name, err, src)
			}
		}
	}
}

// TestEveryCommandRejectsAnEmptyRequiredArgument keeps usage lines honest.
func TestEveryCommandRejectsAnEmptyRequiredArgument(t *testing.T) {
	for _, p := range Builtin() {
		cm, ok := p.(plugin.Commander)
		if !ok {
			continue
		}
		for _, c := range cm.Commands() {
			if !strings.Contains(c.Arg, "<") || c.Query != nil {
				continue
			}
			if c.Live != nil {
				// A live command rejects through its plan, before anything is
				// dialled — which is the whole point of checking here.
				if _, err := c.Live.Plan("  "); err == nil {
					t.Errorf("%s/%s accepted an empty argument", p.Meta().Name, c.Name)
				}
				continue
			}
			if _, err := c.Rewrite("  "); err == nil {
				t.Errorf("%s/%s accepted an empty argument", p.Meta().Name, c.Name)
			}
		}
	}
}

// TestEveryPluginIsWellFormed pins what :plugins and :help depend on.
func TestEveryPluginIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range Builtin() {
		m := p.Meta()
		switch {
		case m.Name == "":
			t.Error("a plugin has no name")
		case m.Summary == "":
			t.Errorf("%s has no summary, so :plugins shows a blank line", m.Name)
		case seen[m.Name]:
			t.Errorf("%s is registered twice", m.Name)
		}
		seen[m.Name] = true

		if cm, ok := p.(plugin.Commander); ok {
			for _, c := range cm.Commands() {
				if !strings.HasPrefix(c.Name, ":") {
					t.Errorf("%s/%s does not start with a colon", m.Name, c.Name)
				}
				if c.Summary == "" {
					t.Errorf("%s/%s has no summary", m.Name, c.Name)
				}
				// The three ways a command can be answered, and exactly one
				// of them per command: a Rewrite that produces source, a
				// declared statement gluon runs against the project's own
				// database, or a plan gluon dials. None is a default, so a
				// command with none is a command that cannot run.
				ways := 0
				for _, declared := range []bool{c.Rewrite != nil, c.Query != nil, c.Live != nil} {
					if declared {
						ways++
					}
				}
				switch {
				case ways == 0:
					t.Errorf("%s/%s has no rewrite, no query and no live plan", m.Name, c.Name)
				case ways > 1:
					t.Errorf("%s/%s is answered %d ways", m.Name, c.Name, ways)
				case c.Live != nil:
					if c.Live.Plan == nil {
						t.Errorf("%s/%s declares a live call with no plan", m.Name, c.Name)
					}
				case c.Query != nil:
					if c.Query.SQL == "" || c.Query.Table == "" {
						t.Errorf("%s/%s declares an incomplete query", m.Name, c.Name)
					}
					// The read-only guarantee is the allowlist :query already
					// enforces, so a declared statement has to pass it too.
					if err := db.CheckStatement(c.Query.SQL, false); err != nil {
						t.Errorf("%s/%s declares a statement that is not read-only: %v",
							m.Name, c.Name, err)
					}
				}
			}
		}
		if r, ok := p.(plugin.Renderer); ok {
			for _, rd := range r.Renders() {
				if rd.Type == "" || rd.Rich == nil {
					t.Errorf("%s has an incomplete renderer for %q", m.Name, rd.Type)
				}
			}
		}
		// A third-party plugin must offer a way to get its module, or `:get
		// <name>` does not work and the "not active" message is a dead end.
		if m.Module != "" {
			a, ok := p.(plugin.Aliaser)
			if !ok || len(a.Aliases()) == 0 {
				t.Errorf("%s names a module but offers no :get alias", m.Name)
			}
		}
	}
}

// TestStdlibPluginsActivateAlone: with an empty build list, exactly the stdlib
// plugins are active. This is what a first run looks like.
func TestStdlibPluginsActivateAlone(t *testing.T) {
	set := plugin.NewSet(Builtin())
	set.Activate(nil, nil)
	for _, p := range set.Active() {
		if p.Meta().Module != "" {
			t.Errorf("%s activated with an empty build list", p.Meta().Name)
		}
	}
	if len(set.Active()) == 0 {
		t.Error("no plugins active on a bare session")
	}
}

// routerModules is every framework that contributes :routes, paired with a
// build-list line naming the versioned import path its module actually
// publishes — chi's module is github.com/go-chi/chi and what a go.mod holds is
// github.com/go-chi/chi/v5, which is the whole reason Meta.Module is a prefix.
//
// The order is Builtin's order, which is the declared tie-break.
var routerModules = []struct{ plugin, require string }{
	{"chi", "github.com/go-chi/chi/v5 v5.0.12"},
	{"gin", "github.com/gin-gonic/gin v1.10.0"},
	{"echo", "github.com/labstack/echo/v4 v4.12.0"},
	{"fiber", "github.com/gofiber/fiber/v2 v2.52.15"},
	{"hertz", "github.com/cloudwego/hertz v0.10.6"},
	{"iris", "github.com/kataras/iris/v12 v12.2.0"},
	{"mux", "github.com/gorilla/mux v1.8.1"},
}

// TestEveryRouterActivatesOnItsPublishedModulePath. A plugin whose Module does
// not match what a go.mod really says is inert forever, and inert looks exactly
// like a broken install — which is the failure :plugins exists to prevent.
func TestEveryRouterActivatesOnItsPublishedModulePath(t *testing.T) {
	for _, rm := range routerModules {
		set := plugin.NewSet(Builtin())
		set.Activate([]string{rm.require}, nil)

		owner := ""
		for _, c := range set.Commands() {
			if c.Name == ":routes" {
				owner = c.Plugin
			}
		}
		if owner != rm.plugin {
			t.Errorf("%s in the build list gave :routes to %q, want %s (why: %q)",
				rm.require, owner, rm.plugin, set.Why(rm.plugin))
		}
	}
}

// TestRoutersConflictIsReported over every pair that could co-occur — a service
// migrating between two frameworks has both in its go.mod. Exactly one plugin
// answers, it is the earlier one in Builtin, and the other is named rather than
// silently dropped. Asserting all 21 pairs is what makes a reordering a
// deliberate act rather than a merge artefact.
func TestRoutersConflictIsReported(t *testing.T) {
	for i, first := range routerModules {
		for _, second := range routerModules[i+1:] {
			// Both orders of the build list, because the tie-break must be
			// Builtin's order and not go.mod's.
			for _, requires := range [][]string{
				{first.require, second.require},
				{second.require, first.require},
			} {
				set := plugin.NewSet(Builtin())
				set.Activate(requires, nil)

				owner, count := "", 0
				for _, c := range set.Commands() {
					if c.Name == ":routes" {
						owner, count = c.Plugin, count+1
					}
				}
				if count != 1 {
					t.Errorf("%s+%s registered :routes %d times; exactly one must win",
						first.plugin, second.plugin, count)
				}
				if owner != first.plugin {
					t.Errorf("%s+%s gave :routes to %q, want %s — Builtin's order is the tie-break",
						first.plugin, second.plugin, owner, first.plugin)
				}

				reported := false
				for _, cf := range set.Conflicts() {
					if cf.Command == ":routes" && cf.Kept == first.plugin && cf.Dropped == second.plugin {
						reported = true
					}
				}
				if !reported {
					t.Errorf("%s+%s: the dropped :routes was not reported: %v",
						first.plugin, second.plugin, set.Conflicts())
				}
			}
		}
	}
}

// TestRoutesIsAbsentWithoutAFramework, and :plugins can say why for each of the
// seven. A command that cannot work must not be offered, and a plugin that is
// merely absent must not look broken.
func TestRoutesIsAbsentWithoutAFramework(t *testing.T) {
	set := plugin.NewSet(Builtin())
	set.Activate(nil, nil)

	for _, c := range set.Commands() {
		if c.Name == ":routes" {
			t.Fatalf(":routes is offered by %s with no web framework in the build list", c.Plugin)
		}
	}
	for _, rm := range routerModules {
		why := set.Why(rm.plugin)
		if !strings.Contains(why, ":get ") {
			t.Errorf("%s does not say how to activate it: %q", rm.plugin, why)
		}
	}
}

// TestConfigConflictIsReported, and viper keeps the name. Both config libraries
// want :config; the registry order in Builtin is the declared tie-break, so
// this pins which way it falls rather than leaving it to whoever edits the list
// next.
func TestConfigConflictIsReported(t *testing.T) {
	set := plugin.NewSet(Builtin())
	set.Activate([]string{
		"github.com/spf13/viper v1.19.0",
		"github.com/knadh/koanf/v2 v2.1.0",
	}, nil)

	owner := ""
	count := 0
	for _, c := range set.Commands() {
		if c.Name == ":config" {
			owner, count = c.Plugin, count+1
		}
	}
	if count != 1 {
		t.Errorf(":config is registered %d times; exactly one must win", count)
	}
	if owner != "viper" {
		t.Errorf(":config went to %q, want viper — Builtin's order is the tie-break", owner)
	}

	var reported bool
	for _, cf := range set.Conflicts() {
		if cf.Command == ":config" && cf.Kept == "viper" && cf.Dropped == "koanf" {
			reported = true
		}
	}
	if !reported {
		t.Errorf("the dropped :config was not reported: %v", set.Conflicts())
	}
}

// TestConfigActivatesOnEitherLibraryAlone, which is the normal case: a project
// has one of them, and gets :config without knowing a plugin exists.
func TestConfigActivatesOnEitherLibraryAlone(t *testing.T) {
	for _, req := range []string{
		"github.com/spf13/viper v1.19.0",
		"github.com/knadh/koanf/v2 v2.1.0",
	} {
		set := plugin.NewSet(Builtin())
		set.Activate([]string{req}, nil)
		found := false
		for _, c := range set.Commands() {
			if c.Name == ":config" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s in the build list did not produce :config", req)
		}
	}
}

// TestConfigIsAbsentWithoutEitherLibrary, and :plugins can say why. A command
// that cannot work must not be offered.
func TestConfigIsAbsentWithoutEitherLibrary(t *testing.T) {
	set := plugin.NewSet(Builtin())
	set.Activate(nil, nil)
	for _, c := range set.Commands() {
		if c.Name == ":config" {
			t.Fatalf(":config is offered by %s with no config library in the build list", c.Plugin)
		}
	}
	for _, name := range []string{"viper", "koanf"} {
		if why := set.Why(name); !strings.Contains(why, ":get") {
			t.Errorf("%s does not say how to activate it: %q", name, why)
		}
	}
}

// migrationModules is every library that contributes :migrations, paired with a
// build-list line naming the versioned import path its module publishes.
//
// The order is Builtin's, which is the declared tie-break — and here the
// tie-break decides more than a name. The two libraries keep applied versions
// in differently shaped tables, so whichever plugin wins is also which table
// gets read.
var migrationModules = []struct{ plugin, require string }{
	{"goose", "github.com/pressly/goose/v3 v3.24.0"},
	{"migrate", "github.com/golang-migrate/migrate/v4 v4.18.1"},
}

// TestEveryMigrationLibraryActivatesOnItsPublishedModulePath. A Module that
// does not match what a go.mod really says is inert forever, and inert looks
// exactly like a broken install.
func TestEveryMigrationLibraryActivatesOnItsPublishedModulePath(t *testing.T) {
	for _, mm := range migrationModules {
		set := plugin.NewSet(Builtin())
		set.Activate([]string{mm.require}, nil)

		owner := ""
		for _, c := range set.Commands() {
			if c.Name == ":migrations" {
				owner = c.Plugin
			}
		}
		if owner != mm.plugin {
			t.Errorf("%s in the build list gave :migrations to %q, want %s (why: %q)",
				mm.require, owner, mm.plugin, set.Why(mm.plugin))
		}
	}
}

// TestMigrationsConflictIsReported, and goose keeps the name.
//
// This pins more than a label. goose reads goose_db_version and golang-migrate
// reads schema_migrations, so the plugin that loses the name is also the table
// that does not get read — which is why the loser is reported through :plugins
// rather than dropped, and why the order in Builtin is a decision rather than
// the order somebody happened to type.
func TestMigrationsConflictIsReported(t *testing.T) {
	first, second := migrationModules[0], migrationModules[1]
	for _, requires := range [][]string{
		{first.require, second.require},
		{second.require, first.require},
	} {
		set := plugin.NewSet(Builtin())
		set.Activate(requires, nil)

		owner, count := "", 0
		for _, c := range set.Commands() {
			if c.Name == ":migrations" {
				owner, count = c.Plugin, count+1
			}
		}
		if count != 1 {
			t.Errorf(":migrations is registered %d times; exactly one must win", count)
		}
		if owner != first.plugin {
			t.Errorf(":migrations went to %q, want %s — Builtin's order is the tie-break",
				owner, first.plugin)
		}
		reported := false
		for _, cf := range set.Conflicts() {
			if cf.Command == ":migrations" && cf.Kept == first.plugin && cf.Dropped == second.plugin {
				reported = true
			}
		}
		if !reported {
			t.Errorf("the dropped :migrations was not reported: %v", set.Conflicts())
		}
	}
}

// TestMigrationsIsAbsentWithoutALibrary, and :plugins can say why for each. A
// command that cannot work must not be offered — there is no table to read
// without a library that wrote one.
func TestMigrationsIsAbsentWithoutALibrary(t *testing.T) {
	set := plugin.NewSet(Builtin())
	set.Activate(nil, nil)
	for _, c := range set.Commands() {
		if c.Name == ":migrations" {
			t.Fatalf(":migrations is offered by %s with no migration library in the build list", c.Plugin)
		}
	}
	for _, mm := range migrationModules {
		if why := set.Why(mm.plugin); !strings.Contains(why, ":get ") {
			t.Errorf("%s does not say how to activate it: %q", mm.plugin, why)
		}
	}
}

// TestEachMigrationPluginReadsItsOwnTable. The two libraries' tracking schemas
// are different, and a plugin reading the other's table would report an answer
// from a tool the project stopped using.
func TestEachMigrationPluginReadsItsOwnTable(t *testing.T) {
	want := map[string]string{
		"goose":   "goose_db_version",
		"migrate": "schema_migrations",
	}
	seen := map[string]bool{}
	for _, p := range Builtin() {
		cm, ok := p.(plugin.Commander)
		if !ok {
			continue
		}
		for _, c := range cm.Commands() {
			if c.Name != ":migrations" {
				continue
			}
			name := p.Meta().Name
			seen[name] = true
			if c.Query == nil {
				t.Fatalf("%s's :migrations declares no query", name)
			}
			if c.Query.Table != want[name] {
				t.Errorf("%s reads %q, want %q", name, c.Query.Table, want[name])
			}
			if !strings.Contains(c.Query.SQL, want[name]) {
				t.Errorf("%s's statement does not read its own table: %q", name, c.Query.SQL)
			}
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("%s contributes no :migrations", name)
		}
	}
}

// TestNewDataLayerPluginsAddNoDependency is the rule internal/db states and
// this change must not break: gluon links none of the libraries its plugins
// describe. A plugin that named a type it had to import would be one gluon had
// to depend on, and the child program would stop being strictly standard
// library.
func TestNewDataLayerPluginsDescribeModulesGluonDoesNotLink(t *testing.T) {
	for _, name := range []string{"sqlx", "pgx", "ent", "goose", "migrate"} {
		found := false
		for _, p := range Builtin() {
			if p.Meta().Name != name {
				continue
			}
			found = true
			if p.Meta().Module == "" {
				t.Errorf("%s claims to be standard library", name)
			}
		}
		if !found {
			t.Errorf("%s is not registered in Builtin", name)
		}
	}
}

// containerModules is every library that contributes :services and :graph,
// paired with a build-list line naming the versioned import path its module
// actually publishes — samber/do's module is github.com/samber/do and what a
// go.mod holds is github.com/samber/do/v2, which is the whole reason
// Meta.Module is a prefix.
//
// The order is Builtin's order, which is the declared tie-break.
var containerModules = []struct{ plugin, require string }{
	{"do", "github.com/samber/do/v2 v2.1.0"},
	{"dig", "go.uber.org/dig v1.19.0"},
}

// containerCommands is the pair of names both plugins want. Two shared names
// rather than one is why the conflict assertions below run over both: a
// tie-break that fell one way for :services and the other for :graph would
// print two libraries' answers to one container.
var containerCommands = []string{":services", ":graph"}

// TestEveryContainerLibraryActivatesOnItsPublishedModulePath. A Module that
// does not match what a go.mod really says is inert forever, and inert looks
// exactly like a broken install.
func TestEveryContainerLibraryActivatesOnItsPublishedModulePath(t *testing.T) {
	for _, cm := range containerModules {
		set := plugin.NewSet(Builtin())
		set.Activate([]string{cm.require}, nil)

		for _, name := range containerCommands {
			owner := ""
			for _, c := range set.Commands() {
				if c.Name == name {
					owner = c.Plugin
				}
			}
			if owner != cm.plugin {
				t.Errorf("%s in the build list gave %s to %q, want %s (why: %q)",
					cm.require, name, owner, cm.plugin, set.Why(cm.plugin))
			}
		}
	}
}

// TestContainerConflictIsReported, and samber/do keeps both names.
//
// The order in Builtin is the whole tie-break, and it is not arbitrary: fx
// depends on dig, so a build list can hold dig without the session holding a
// dig container, while nothing in common use pulls samber/do in transitively.
// A list with both is therefore far more often a samber/do project that
// acquired dig than the reverse. Pinning it here makes a reordering a
// deliberate act rather than a merge artefact.
func TestContainerConflictIsReported(t *testing.T) {
	first, second := containerModules[0], containerModules[1]
	for _, requires := range [][]string{
		{first.require, second.require},
		{second.require, first.require},
	} {
		set := plugin.NewSet(Builtin())
		set.Activate(requires, nil)

		for _, name := range containerCommands {
			owner, count := "", 0
			for _, c := range set.Commands() {
				if c.Name == name {
					owner, count = c.Plugin, count+1
				}
			}
			if count != 1 {
				t.Errorf("%s is registered %d times; exactly one must win", name, count)
			}
			if owner != first.plugin {
				t.Errorf("%s went to %q, want %s — Builtin's order is the tie-break",
					name, owner, first.plugin)
			}
			reported := false
			for _, cf := range set.Conflicts() {
				if cf.Command == name && cf.Kept == first.plugin && cf.Dropped == second.plugin {
					reported = true
				}
			}
			if !reported {
				t.Errorf("the dropped %s was not reported: %v", name, set.Conflicts())
			}
		}
	}
}

// TestContainerCommandsAreAbsentWithoutALibrary, and :plugins can say why for
// each. There is no container to read without a library that built one, and a
// command that cannot work must not be offered.
func TestContainerCommandsAreAbsentWithoutALibrary(t *testing.T) {
	set := plugin.NewSet(Builtin())
	set.Activate(nil, nil)

	for _, c := range set.Commands() {
		for _, name := range containerCommands {
			if c.Name == name {
				t.Fatalf("%s is offered by %s with no container library in the build list",
					name, c.Plugin)
			}
		}
	}
	for _, cm := range containerModules {
		if why := set.Why(cm.plugin); !strings.Contains(why, ":get ") {
			t.Errorf("%s does not say how to activate it: %q", cm.plugin, why)
		}
	}
}

// TestNoContainerPluginDescribesACompileTimeInjector. wire resolves its graph
// before there is a process and leaves no container to ask, so no plugin may
// claim it — and fx, which does have a graph, exports no way to reach it from
// an *fx.App without building a second app and running its invocations. Both
// exclusions are reasons rather than oversights, recorded in the pack's package
// comment; this keeps a later plugin from quietly contradicting them.
func TestNoContainerPluginDescribesACompileTimeInjector(t *testing.T) {
	for _, p := range Builtin() {
		m := p.Meta()
		for _, excluded := range []string{"github.com/google/wire", "go.uber.org/fx"} {
			if m.Module == excluded {
				t.Errorf("%s claims %s, which cannot be described from a value in scope",
					m.Name, excluded)
			}
		}
	}
}

// TestEveryPluginCommandDeclaresItsUsage: a plugin's command gets the page, the
// completion and the docs entry a builtin gets, so it owes the same
// declaration — an example to type, a neighbour to go to, and a line of help
// for every flag, each one used by an example.
func TestEveryPluginCommandDeclaresItsUsage(t *testing.T) {
	for _, p := range Builtin() {
		cm, ok := p.(plugin.Commander)
		if !ok {
			continue
		}
		for _, c := range cm.Commands() {
			who := p.Meta().Name + "/" + c.Name
			if len(c.Usage.Examples) == 0 {
				t.Errorf("%s has no example", who)
			}
			if len(c.Usage.See) == 0 {
				t.Errorf("%s names no neighbour", who)
			}
			for _, f := range c.Usage.Visible() {
				if f.Help == "" {
					t.Errorf("%s %s has no help", who, f.Name)
				}
				used := false
				for _, ex := range c.Usage.Examples {
					for _, w := range strings.Fields(ex.Line) {
						used = used || w == f.Name
					}
				}
				if !used {
					t.Errorf("%s %s is in no example", who, f.Name)
				}
			}
			for _, ex := range c.Usage.Examples {
				if first, _, _ := strings.Cut(ex.Line, " "); first != c.Name {
					t.Errorf("%s's example %q is not a line for it", who, ex.Line)
				}
			}
		}
	}
}
