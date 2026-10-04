package plugins

import (
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/plugin"
)

// The serialization plugins are split by one rule — whether the encoder is in
// the standard library — and these are the tests that say so. :xml and :csv are
// there on a bare session; the rest appear exactly when the session's own build
// list already has the library, because gluon links none of them.

// commandsOf is the active command set for a synthetic build list.
func commandsOf(requires ...string) map[string]string {
	set := plugin.NewSet(Builtin())
	set.Activate(requires, nil)
	out := map[string]string{}
	for _, c := range set.Commands() {
		out[c.Name] = c.Plugin
	}
	return out
}

// TestStdlibEncodingsAreAlwaysActive: :xml and :csv are stdlib plugins, so a
// session with nothing in its build list still has them — the same footing
// :json has always been on.
func TestStdlibEncodingsAreAlwaysActive(t *testing.T) {
	cmds := commandsOf()
	for name, want := range map[string]string{":xml": "xml", ":csv": "csv", ":json": "json"} {
		if got := cmds[name]; got != want {
			t.Errorf("%s came from %q on a bare session, want %q", name, got, want)
		}
	}
}

// TestThirdPartyEncodingsAreAbsentWithoutTheirModule. A command that cannot
// work must not be offered: the encoder lives in the user's module, and gluon
// does not link it.
func TestThirdPartyEncodingsAreAbsentWithoutTheirModule(t *testing.T) {
	cmds := commandsOf("github.com/google/uuid v1.6.0")
	for _, name := range []string{":yaml", ":toml", ":msgpack", ":pb"} {
		if owner, ok := cmds[name]; ok {
			t.Errorf("%s is offered by %s with no encoding library in the build list", name, owner)
		}
	}
}

// TestEachThirdPartyEncodingActivatesOnItsOwnModule, one at a time, which is
// what a real project looks like.
func TestEachThirdPartyEncodingActivatesOnItsOwnModule(t *testing.T) {
	for _, tc := range []struct{ module, command, plugin string }{
		{"go.yaml.in/yaml/v3 v3.0.4", ":yaml", "yaml"},
		{"gopkg.in/yaml.v3 v3.0.1", ":yaml", "yaml.v3"},
		{"github.com/BurntSushi/toml v1.6.0", ":toml", "toml"},
		{"github.com/vmihailenco/msgpack/v5 v5.4.1", ":msgpack", "msgpack"},
		{"google.golang.org/protobuf v1.36.0", ":pb", "protobuf"},
	} {
		cmds := commandsOf(tc.module)
		if got := cmds[tc.command]; got != tc.plugin {
			t.Errorf("%s in the build list gave %s to %q, want %q",
				tc.module, tc.command, got, tc.plugin)
		}
	}
}

// TestMaintainedYAMLKeepsTheCommand. The two yaml plugins are the same library
// under two module paths, so a session with both must not be told it is using
// the archived one. Builtin's order is the whole tie-break; this pins which way
// it falls rather than leaving it to whoever edits the list next.
func TestMaintainedYAMLKeepsTheCommand(t *testing.T) {
	set := plugin.NewSet(Builtin())
	set.Activate([]string{"gopkg.in/yaml.v3 v3.0.1", "go.yaml.in/yaml/v3 v3.0.4"}, nil)

	owner, count := "", 0
	for _, c := range set.Commands() {
		if c.Name == ":yaml" {
			owner, count = c.Plugin, count+1
		}
	}
	if count != 1 {
		t.Errorf(":yaml is registered %d times; exactly one must win", count)
	}
	if owner != "yaml" {
		t.Errorf(":yaml went to %q, want the maintained path's plugin", owner)
	}

	var reported bool
	for _, cf := range set.Conflicts() {
		if cf.Command == ":yaml" && cf.Kept == "yaml" && cf.Dropped == "yaml.v3" {
			reported = true
		}
	}
	if !reported {
		t.Errorf("the dropped :yaml was not reported: %v", set.Conflicts())
	}
}

// TestAYAMLMajorVersionDoesNotActivateTheOther. Both yaml modules carry their
// major version in the path, so neither may be matched by prefix: a v4 session
// activating the v3 plugin would preload a path that is not in its build list
// and turn a missing command into a build error.
func TestAYAMLMajorVersionDoesNotActivateTheOther(t *testing.T) {
	if cmds := commandsOf("go.yaml.in/yaml/v4 v4.0.0"); cmds[":yaml"] != "" {
		t.Errorf(":yaml activated for a v4 session, from the %q plugin", cmds[":yaml"])
	}
	if cmds := commandsOf("github.com/vmihailenco/msgpack/v4 v4.3.12"); cmds[":msgpack"] != "" {
		t.Errorf(":msgpack activated for a v4 session, from the %q plugin", cmds[":msgpack"])
	}
}

// TestInactiveEncodingsNameTheModuleThatActivatesThem, because a command that
// is simply absent is indistinguishable from a broken install. This is the text
// :plugins prints.
func TestInactiveEncodingsNameTheModuleThatActivatesThem(t *testing.T) {
	set := plugin.NewSet(Builtin())
	set.Activate(nil, nil)
	for name, module := range map[string]string{
		"yaml":     "go.yaml.in/yaml/v3",
		"yaml.v3":  "gopkg.in/yaml.v3",
		"toml":     "github.com/BurntSushi/toml",
		"msgpack":  "github.com/vmihailenco/msgpack/v5",
		"protobuf": "google.golang.org/protobuf",
	} {
		why := set.Why(name)
		if !strings.Contains(why, ":get "+module) {
			t.Errorf("%s says %q, which does not name the module that activates it", name, why)
		}
	}
}

// TestEveryEncodingCommandIsReachableByItsAlias closes the loop the "not
// active — :get …" message opens: the short name it prints has to resolve.
func TestEveryEncodingCommandIsReachableByItsAlias(t *testing.T) {
	aliases := plugin.NewSet(Builtin()).Aliases()
	for name, want := range map[string]string{
		"yaml":     "go.yaml.in/yaml/v3",
		"yaml.v3":  "gopkg.in/yaml.v3",
		"toml":     "github.com/BurntSushi/toml",
		"msgpack":  "github.com/vmihailenco/msgpack/v5",
		"protobuf": "google.golang.org/protobuf",
	} {
		if got := aliases[name]; got != want {
			t.Errorf(":get %s resolves to %q, want %q", name, got, want)
		}
	}
}
