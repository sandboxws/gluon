package repl

import (
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/plugin"
	"github.com/sandboxws/gluon/internal/plugins"
)

// TestAnUnsetVariableIsReportedRatherThanSentEmpty.
//
// An empty credential is not a missing one at the far end: it comes back as a
// 401 or an Unauthenticated, which is indistinguishable from a wrong token —
// and that sends somebody to rotate a secret that was fine. resolveHTTPRefs
// makes the same refusal for :http's headers.
func TestAnUnsetVariableIsReportedRatherThanSentEmpty(t *testing.T) {
	t.Setenv("GLUON_TEST_EMPTY", "")

	if _, _, err := resolveRefs([]string{"GLUON_TEST_UNSET_ENTIRELY"}); err == nil {
		t.Error("an unset variable was passed through")
	} else if !strings.Contains(err.Error(), "not set") {
		t.Errorf("the refusal does not say it is unset: %v", err)
	}

	// Set-to-nothing is the same failure by a different route, and is refused
	// with its own sentence rather than folded into the first.
	if _, _, err := resolveRefs([]string{"GLUON_TEST_EMPTY"}); err == nil {
		t.Error("a variable set to nothing was passed through")
	} else if !strings.Contains(err.Error(), "set to nothing") {
		t.Errorf("the refusal does not distinguish empty from unset: %v", err)
	}
}

// TestOneWalkBuildsBothTheEnvironmentAndTheRedactionList. A second lookup is
// how a value ends up masked in one place and not the other.
func TestOneWalkBuildsBothTheEnvironmentAndTheRedactionList(t *testing.T) {
	t.Setenv("GLUON_TEST_A", "alpha")
	t.Setenv("GLUON_TEST_B", "beta")

	// The repeat must not produce a second entry: the child's environment would
	// carry the same assignment twice, and the mask would walk it twice.
	env, secrets, err := resolveRefs([]string{"GLUON_TEST_A", "GLUON_TEST_B", "GLUON_TEST_A"})
	if err != nil {
		t.Fatal(err)
	}
	wantEnv := []string{"GLUON_TEST_A=alpha", "GLUON_TEST_B=beta"}
	if len(env) != len(wantEnv) {
		t.Fatalf("env = %v, want %v", env, wantEnv)
	}
	for i := range env {
		if env[i] != wantEnv[i] {
			t.Errorf("env[%d] = %q, want %q", i, env[i], wantEnv[i])
		}
	}
	if len(secrets) != 2 || secrets[0] != "alpha" || secrets[1] != "beta" {
		t.Errorf("secrets = %v, want the same two values the environment got", secrets)
	}
}

// grpcCommand is the plugin's command, adapted the way an active session would
// adapt it. The plugin is not active in a unit test — its module is not in any
// build list here — so it is reached through the registry rather than through
// Core.
func grpcCommand(t *testing.T) Command {
	t.Helper()
	for _, p := range plugins.Builtin() {
		if p.Meta().Name != "grpc" {
			continue
		}
		cm, ok := p.(plugin.Commander)
		if !ok {
			t.Fatal("the grpc plugin contributes no commands")
		}
		return adapt(plugin.TaggedCommand{Plugin: "grpc", Command: cm.Commands()[0]})
	}
	t.Fatal("no grpc plugin in Builtin")
	return Command{}
}

// TestGRPCHelpSaysEveryCallDialsAndHangsUp.
//
// gRPC assumes a channel that outlives many calls and gluon's child outlives
// none, so a latency measured here is the cost of connecting plus the call.
// Saying nothing would let somebody conclude something about gRPC from a
// measurement of gluon.
func TestGRPCHelpSaysEveryCallDialsAndHangsUp(t *testing.T) {
	c := &Core{extra: []Command{grpcCommand(t)}}
	out := c.help(":grpc").Out
	for _, want := range []string{
		"dials and hangs up",
		"is not a measurement of gRPC",
		"no channel",
		"From the grpc plugin.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf(":help :grpc does not say %q:\n%s", want, out)
		}
	}
}

// TestGRPCIsInactiveWithoutItsModule, and :plugins says what would activate it.
// A command that is simply absent is indistinguishable from a broken install
// unless something says which.
func TestGRPCIsInactiveWithoutItsModule(t *testing.T) {
	c := &Core{plugins: bareSet()}
	out := c.pluginList().Out

	var line string
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) > 1 && f[1] == "grpc" {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("grpc is not listed at all, which is how a plugin looks broken:\n%s", out)
	}
	if !strings.HasPrefix(strings.TrimSpace(line), "·") {
		t.Errorf("grpc is marked active with an empty build list: %q", line)
	}
	if !strings.Contains(line, ":get google.golang.org/grpc") {
		t.Errorf("grpc does not say what activates it: %q", line)
	}

	// And the command it contributes is not offered, because it cannot work.
	for _, cmd := range c.plugins.Commands() {
		if cmd.Name == ":grpc" {
			t.Errorf(":grpc is offered by %s with no gRPC in the build list", cmd.Plugin)
		}
	}
}

// TestGRPCAliasResolves. `:get grpc` is exactly the case where the plugin is
// not active yet — that is the point — so the alias comes from every known
// plugin rather than the active ones.
func TestGRPCAliasResolves(t *testing.T) {
	c := &Core{plugins: bareSet()}
	if got := c.resolveGetAlias("grpc"); got != "google.golang.org/grpc" {
		t.Errorf(`resolveGetAlias("grpc") = %q, want the module path`, got)
	}
	// A version suffix survives the expansion.
	if got := c.resolveGetAlias("grpc@v1.83.0"); got != "google.golang.org/grpc@v1.83.0" {
		t.Errorf(`resolveGetAlias("grpc@v1.83.0") = %q`, got)
	}
}
