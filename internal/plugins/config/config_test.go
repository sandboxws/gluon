package config

import (
	"go/parser"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/plugin"
)

// TestBothProduceADottedListing. The two libraries are asked different
// questions — viper by key, koanf by map — and the answer has to read the same,
// because it is the same answer and :conf already prints it in this form.
func TestBothProduceADottedListing(t *testing.T) {
	for _, p := range []plugin.Plugin{Viper{}, Koanf{}} {
		name := p.Meta().Name
		cmds := p.(plugin.Commander).Commands()
		if len(cmds) != 1 || cmds[0].Name != ":config" {
			t.Fatalf("%s does not contribute exactly :config: %v", name, cmds)
		}
		src, err := cmds[0].Rewrite("v")
		if err != nil {
			t.Fatalf("%s rejected a plain argument: %v", name, err)
		}
		if _, err := parser.ParseExpr(src); err != nil {
			t.Errorf("%s produced source that does not parse: %v\n%s", name, err, src)
		}
		if !strings.Contains(src, `"%s = %v"`) {
			t.Errorf("%s does not produce the key = value form redaction reads:\n%s", name, src)
		}
		if !strings.Contains(src, "sort.Strings") {
			t.Errorf("%s does not sort, so the listing is in map order:\n%s", name, src)
		}
	}
}

// TestTheGeneratedSourceDoesNotRedact is the load-bearing half of
// plugin.Command.Redact: the child prints the settings and gluon hides what
// needs hiding, so there is one definition of "secret" and :src shows the
// question rather than the machinery.
func TestTheGeneratedSourceDoesNotRedact(t *testing.T) {
	for _, p := range []plugin.Plugin{Viper{}, Koanf{}} {
		cmds := p.(plugin.Commander).Commands()
		if !cmds[0].Redact {
			t.Errorf("%s does not ask gluon to redact, so secrets would print", p.Meta().Name)
		}
		src, err := cmds[0].Rewrite("v")
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"***", "redact", "Redact", "secret", "password"} {
			if strings.Contains(src, forbidden) {
				t.Errorf("%s generates redaction logic (%q):\n%s",
					p.Meta().Name, forbidden, src)
			}
		}
	}
}

// TestEmptyArgumentIsRejected, with a usage line naming the command.
func TestEmptyArgumentIsRejected(t *testing.T) {
	for _, p := range []plugin.Plugin{Viper{}, Koanf{}} {
		cmds := p.(plugin.Commander).Commands()
		_, err := cmds[0].Rewrite("   ")
		if err == nil {
			t.Fatalf("%s accepted an empty argument", p.Meta().Name)
		}
		if !strings.Contains(err.Error(), "usage:") {
			t.Errorf("%s: %q does not read as a usage line", p.Meta().Name, err)
		}
	}
}

// TestEachDeclaresItsModuleAndAlias. Without the alias the "not active — :get
// …" message is a dead end, which registry_test.go also enforces across every
// builtin; this states it where the plugin is written.
func TestEachDeclaresItsModuleAndAlias(t *testing.T) {
	for _, p := range []plugin.Plugin{Viper{}, Koanf{}} {
		m := p.Meta()
		if m.Module == "" {
			t.Errorf("%s claims to be stdlib", m.Name)
		}
		aliases := p.(plugin.Aliaser).Aliases()
		if len(aliases) == 0 {
			t.Fatalf("%s offers no :get alias", m.Name)
		}
		if aliases[0].Name != m.Name {
			t.Errorf("%s's alias is %q; :get <plugin name> should work", m.Name, aliases[0].Name)
		}
		imports := p.(plugin.Importer).Imports()
		if len(imports) == 0 || imports[0].Name != m.Name {
			t.Errorf("%s preloads no qualifier of its own name: %v", m.Name, imports)
		}
	}
}
