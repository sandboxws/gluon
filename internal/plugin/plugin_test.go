package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/pretty"
)

type fake struct {
	meta     Meta
	imports  []Import
	commands []Command
	renders  []Render
	aliases  []Alias
}

func (f fake) Meta() Meta          { return f.meta }
func (f fake) Imports() []Import   { return f.imports }
func (f fake) Commands() []Command { return f.commands }
func (f fake) Renders() []Render   { return f.renders }
func (f fake) Aliases() []Alias    { return f.aliases }

func lib(name, module string) fake {
	return fake{meta: Meta{Name: name, Module: module}}
}

// TestStdlibPluginsAreAlwaysActive: an empty Module means the standard
// library, which is always there.
func TestStdlibPluginsAreAlwaysActive(t *testing.T) {
	s := NewSet([]Plugin{lib("time", "")})
	s.Activate(nil, nil)
	if len(s.Active()) != 1 {
		t.Fatalf("stdlib plugin not active with an empty build list")
	}
}

// TestModulePluginsWaitForTheirModule is what stops a session with no gorm from
// having a :sql that cannot work.
func TestModulePluginsWaitForTheirModule(t *testing.T) {
	s := NewSet([]Plugin{lib("uuid", "github.com/google/uuid")})

	s.Activate(nil, nil)
	if len(s.Active()) != 0 {
		t.Error("activated without its module in the build list")
	}
	if why := s.Why("uuid"); !strings.Contains(why, ":get") {
		t.Errorf("an inactive plugin should say how to get it, said %q", why)
	}

	// Requirements arrive as "path version", the way go.mod lists them.
	s.Activate([]string{"github.com/google/uuid v1.6.0"}, nil)
	if len(s.Active()) != 1 {
		t.Error("did not activate when its module arrived")
	}
}

// TestSubmoduleActivates: a plugin named for a module should activate when
// something beneath it is required, which is how driver packages arrive.
func TestSubmoduleActivates(t *testing.T) {
	s := NewSet([]Plugin{lib("gorm", "gorm.io")})
	s.Activate([]string{"gorm.io/gorm v1.25.0"}, nil)
	if len(s.Active()) != 1 {
		t.Error("a requirement beneath the module did not activate it")
	}
}

// TestDisableWins, whatever the build list says.
func TestDisableWins(t *testing.T) {
	s := NewSet([]Plugin{lib("time", ""), lib("uuid", "github.com/google/uuid")})
	s.Activate([]string{"github.com/google/uuid v1.6.0"}, map[string]bool{"time": true, "uuid": true})
	if len(s.Active()) != 0 {
		t.Errorf("disabled plugins activated: %d", len(s.Active()))
	}
	if why := s.Why("time"); !strings.Contains(why, "disabled") {
		t.Errorf("why should say it was disabled, said %q", why)
	}
}

// TestAliasesComeFromEveryKnownPlugin, not the active ones. `:get uuid` is
// exactly the case where the plugin is not active yet — that is the point.
func TestAliasesComeFromEveryKnownPlugin(t *testing.T) {
	p := lib("uuid", "github.com/google/uuid")
	p.aliases = []Alias{{Name: "uuid", Module: "github.com/google/uuid"}}
	s := NewSet([]Plugin{p})
	s.Activate(nil, nil) // deliberately not active

	if got := s.Aliases()["uuid"]; got != "github.com/google/uuid" {
		t.Errorf("alias from an inactive plugin was dropped: %q", got)
	}
}

// TestFirstRegistrationWins keeps the tie-break stable and declared: the order
// in the registry decides, not map iteration.
func TestFirstRegistrationWins(t *testing.T) {
	first := lib("a", "")
	first.renders = []Render{{Type: "T", Rich: func(pretty.Value, pretty.Styles) (string, bool) {
		return "first", true
	}}}
	second := lib("b", "")
	second.renders = []Render{{Type: "T", Rich: func(pretty.Value, pretty.Styles) (string, bool) {
		return "second", true
	}}}

	s := NewSet([]Plugin{first, second})
	s.Activate(nil, nil)
	out, _ := s.Renders()["T"].Rich(pretty.Value{}, pretty.Styles{})
	if out != "first" {
		t.Errorf("later registration won the type: %q", out)
	}
}

// TestInactivePluginsContributeNothing: no commands, no renderers, no imports.
func TestInactivePluginsContributeNothing(t *testing.T) {
	p := lib("uuid", "github.com/google/uuid")
	p.imports = []Import{{Name: "uuid", Path: "github.com/google/uuid"}}
	p.commands = []Command{{Name: ":uuid", Summary: "x"}}
	p.renders = []Render{{Type: "uuid.UUID"}}

	s := NewSet([]Plugin{p})
	s.Activate(nil, nil)

	if n := len(s.Imports()); n != 0 {
		t.Errorf("inactive plugin contributed %d imports", n)
	}
	if n := len(s.Commands()); n != 0 {
		t.Errorf("inactive plugin contributed %d commands", n)
	}
	if n := len(s.Renders()); n != 0 {
		t.Errorf("inactive plugin contributed %d renderers", n)
	}
}

func writePlugin(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLoadTOMLPlugin covers the whole declarative surface.
func TestLoadTOMLPlugin(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "sqlc.toml", `
name    = "sqlc"
module  = "github.com/example/sqlc"
summary = "a test plugin"
imports = ["github.com/example/sqlc"]

[[command]]
name    = ":sqlc"
arg     = "<query>"
summary = "the SQL a query builds"
rewrite = "{{.Arg}}.ToSQL()"
`)
	loaded, errs := LoadDir(dir)
	if len(errs) != 0 {
		t.Fatalf("errors loading: %v", errs)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d plugins, want 1", len(loaded))
	}
	p := loaded[0]
	if p.Meta().Name != "sqlc" {
		t.Errorf("name = %q", p.Meta().Name)
	}

	cmds := p.(Commander).Commands()
	src, err := cmds[0].Rewrite("db.Where(1)")
	if err != nil {
		t.Fatal(err)
	}
	if src != "db.Where(1).ToSQL()" {
		t.Errorf("rewrite = %q", src)
	}
	if _, err := cmds[0].Rewrite(""); err == nil {
		t.Error("a required argument was not enforced")
	}

	// A module with no explicit alias still answers to its own name.
	if got := p.(Aliaser).Aliases(); len(got) != 1 || got[0].Name != "sqlc" {
		t.Errorf("implicit alias missing: %v", got)
	}
	if im := p.(Importer).Imports(); len(im) != 1 || im[0].Name != "sqlc" {
		t.Errorf("import qualifier wrong: %v", im)
	}
}

// TestTOMLErrorsAreReported, not skipped. A file someone wrote and gluon
// silently ignored is the failure mode config.Load already refuses to have.
func TestTOMLErrorsAreReported(t *testing.T) {
	cases := map[string]string{
		"unknown key":        "name = \"x\"\nnope = 1\n",
		"no name":            "module = \"example.com/m\"\n",
		"command no colon":   "name = \"x\"\n[[command]]\nname = \"sql\"\nsummary = \"s\"\nrewrite = \"{{.Arg}}\"\n",
		"command no rewrite": "name = \"x\"\n[[command]]\nname = \":sql\"\nsummary = \"s\"\n",
		"command no summary": "name = \"x\"\n[[command]]\nname = \":sql\"\nrewrite = \"{{.Arg}}\"\n",
		"bad template":       "name = \"x\"\n[[command]]\nname = \":sql\"\nsummary = \"s\"\nrewrite = \"{{.Arg\"\n",
		"bad import":         "name = \"x\"\nimports = [\"has space\"]\n",
	}
	for label, body := range cases {
		t.Run(label, func(t *testing.T) {
			dir := t.TempDir()
			writePlugin(t, dir, "p.toml", body)
			loaded, errs := LoadDir(dir)
			if len(errs) == 0 {
				t.Errorf("accepted a broken plugin; loaded %d", len(loaded))
			}
		})
	}
}

// TestMissingPluginDirIsNotAnError: gluon has to work with no config at all.
func TestMissingPluginDirIsNotAnError(t *testing.T) {
	loaded, errs := LoadDir(filepath.Join(t.TempDir(), "nope"))
	if len(errs) != 0 || len(loaded) != 0 {
		t.Errorf("a missing plugin dir should be silent, got %v / %v", loaded, errs)
	}
}

// TestPackageName covers the two import paths that do not end in their own
// package name.
func TestPackageName(t *testing.T) {
	cases := map[string]string{
		"strings":                "strings",
		"net/http":               "http",
		"github.com/foo/bar/v2":  "bar",
		"gopkg.in/yaml.v3":       "yaml",
		"github.com/google/uuid": "uuid",
	}
	for in, want := range cases {
		if got := packageName(in); got != want {
			t.Errorf("packageName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestTOMLCommandUsageLoads: a user's plugin declares what its command takes
// the way a builtin does, and gets the same page for it.
func TestTOMLCommandUsageLoads(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "sqlc.toml", `
name    = "sqlc"
summary = "a test plugin"

[[command]]
name    = ":sqlc"
arg     = "<query> [dialect]"
kind    = "words"
summary = "the SQL a query builds"
rewrite = "{{.Arg}}.ToSQL()"
see     = [":query"]

[[command.param]]
name = "query"
help = "the query builder"

[[command.param]]
name     = "dialect"
optional = true
values   = ["postgres", "sqlite"]

[[command.flag]]
name  = "-pretty"
help  = "indent the statement"

[[command.example]]
line = ":sqlc q postgres -pretty"
says = "the statement, indented"
`)
	loaded, errs := LoadDir(dir)
	if len(errs) != 0 {
		t.Fatalf("errors loading: %v", errs)
	}
	u := loaded[0].(Commander).Commands()[0].Usage
	if u.Kind != cmdspec.Words || len(u.Params) != 2 || len(u.Flags) != 1 ||
		len(u.Examples) != 1 || len(u.See) != 1 {
		t.Fatalf("usage did not load whole: %+v", u)
	}
	if got := u.Params[1].Values.Fixed; len(got) != 2 || got[0] != "postgres" {
		t.Errorf("a param's values did not load: %v", got)
	}
	if got := u.Line(":sqlc", "<query> [dialect]"); got != ":sqlc [-pretty] <query> [dialect]" {
		t.Errorf("synopsis = %q", got)
	}
}

// TestTOMLUsageIsValidated: each rule is one a reader would otherwise meet as
// a page that lies.
func TestTOMLUsageIsValidated(t *testing.T) {
	head := "name = \"x\"\n[[command]]\nname = \":c\"\nsummary = \"s\"\nrewrite = \"{{.Arg}}\"\n"
	cases := map[string]string{
		"unknown kind":            head + "kind = \"shell\"\n",
		"example for another":     head + "[[command.example]]\nline = \":other x\"\n",
		"flag without a dash":     head + "[[command.flag]]\nname = \"pretty\"\n",
		"see without a colon":     head + "see = [\"query\"]\n",
		"param without a name":    head + "[[command.param]]\nhelp = \"h\"\n",
		"unknown key in the flag": head + "[[command.flag]]\nname = \"-p\"\nnope = 1\n",
	}
	for label, body := range cases {
		t.Run(label, func(t *testing.T) {
			dir := t.TempDir()
			writePlugin(t, dir, "p.toml", body)
			if _, errs := LoadDir(dir); len(errs) == 0 {
				t.Error("accepted a declaration that would print a wrong page")
			}
		})
	}
}

// TestAGuidelessTOMLPluginHasNoGuide: every file-backed plugin is a Guider,
// because its Go type has to be. One that names no guide must not answer
// :guide with an empty page and no error.
func TestAGuidelessTOMLPluginHasNoGuide(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "bare.toml", "name = \"bare\"\n")
	writePlugin(t, dir, "guided.toml", "name = \"guided\"\nguide = \"guide.md\"\n")
	if err := os.WriteFile(filepath.Join(dir, "guide.md"), []byte("# how\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, errs := LoadDir(dir)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	s := NewSet(loaded)
	if _, ok := s.Guide("bare"); ok {
		t.Error("a plugin with no guide reported one")
	}
	if text, ok := s.Guide("guided"); !ok || !strings.Contains(text, "how") {
		t.Errorf("the guide that exists was not read: %q %v", text, ok)
	}
	if got := s.Guides(); len(got) != 1 || got[0] != "guided" {
		t.Errorf("Guides() = %v, want only the one that has one", got)
	}
	if !s.Known("bare") || s.Known("nope") {
		t.Error("Known does not tell a plugin from a name nobody defined")
	}
}
