//go:build integration

package repl

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/plugin"
)

// containerLike is the :services and :graph pair adapted exactly as the dig and
// samber/do plugins are, with rewrites that answer from the standard library.
//
// The real plugins name go.uber.org/dig and github.com/samber/do, and gluon
// links neither — constraint B, the reason they are plugins at all — so a test
// that ran their Rewrite would have to :get a module over the network. What is
// under test here is gluon's half: that a command which only reads a structure
// runs none of the functions in it, that a long answer is still whole in Out,
// and that running either leaves the session exactly as it was.
//
// Whether dig's String and samber/do's ListProvidedServices construct anything
// is the libraries' half. internal/plugins/di's TestNoRewriteConstructsAnything
// holds the only part of that gluon can hold: that no rewrite names one of the
// calls that would.
//
// The stand-in container is shaped like the real ones on purpose — a provider
// that knows what it provides, where it came from, and how to build it — so
// that a rewrite reaching for the build step is expressible here, and caught.
func containerLike(name string) Command {
	return adapt(plugin.TaggedCommand{
		Plugin: "stand-in",
		Command: plugin.Command{
			Name: name, Arg: "<c>", Text: true,
			Summary: "what a container has registered",
			Rewrite: func(arg string) (string, error) {
				if strings.TrimSpace(arg) == "" {
					return "", fmt.Errorf("usage: %s <c>", name)
				}
				return "func() string { __c := " + arg + "; " +
					"if len(__c) == 0 { return \"stand-in — nothing is registered\" }; " +
					"var __out []string; " +
					"for _, __p := range __c { __out = append(__out, __p.Name + \" -> ctor: \" + __p.Ctor) }; " +
					"return fmt.Sprintf(\"stand-in — %d registered\\n\\n%s\", " +
					"len(__out), strings.Join(__out, \"\\n\")) }()", nil
			},
		},
	})
}

// container is the session line that builds the stand-in: n providers, each of
// which writes marker when it is built. Nothing in either command may reach
// Build, and the file is how the test finds out if one did.
func container(marker string, n int) string {
	var b strings.Builder
	b.WriteString("container := []struct{ Name, Ctor string; Build func() error }{")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "{Name: \"*app.T%d\", Ctor: \"app.NewT%d\", "+
			"Build: func() error { return os.WriteFile(%s, []byte(\"built\"), 0o600) }},",
			i, i, strconv.Quote(marker))
	}
	b.WriteString("}")
	return b.String()
}

func withContainerCommands(t *testing.T) (*Core, string) {
	t.Helper()
	c, err := NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	t.Cleanup(func() { c.Close() })

	c.extra = append(c.extra, containerLike(":services"), containerLike(":graph"))
	c.invalidateCommands()

	marker := filepath.Join(t.TempDir(), "provider-ran")
	if res := c.Submit(container(marker, 30)); res.Err {
		t.Fatalf("setup failed: %s", res.Out)
	}
	return c, marker
}

// TestContainerInspectionRunsNoProvider is the sharpest guarantee in the
// capability, end to end.
//
// It is also the failure that would be invisible without a test: a rewrite that
// built something would look like the user's own code running, and — because
// the session replays every entry — would run again on every line after it,
// accumulating side effects nobody attributed to a listing command.
func TestContainerInspectionRunsNoProvider(t *testing.T) {
	c, marker := withContainerCommands(t)

	for _, line := range []string{":services container", ":graph container"} {
		res := c.Submit(line)
		if res.Err {
			t.Fatalf("%s failed: %s", line, res.Out)
		}
		// A command that answered nothing would pass the file check for the
		// wrong reason, so the answer has to be an answer.
		if !strings.Contains(res.Out, "*app.T0") || !strings.Contains(res.Out, "30 registered") {
			t.Fatalf("%s did not list the container:\n%s", line, res.Out)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("%s built a provider", line)
		}
	}

	// And the marker works: building one really does write it, so the absence
	// above is a fact about the commands rather than about the fixture.
	if res := c.Submit(`container[0].Build()`); res.Err {
		t.Fatalf("building a provider failed: %s", res.Out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the marker never appears, so the check above proves nothing: %v", err)
	}
}

// TestContainerInspectionDoesNotMutateTheSession is invariant 14 for both
// commands. They evaluate through EvalTransient like every plugin command, so
// they must leave no entry, no import and a working session behind.
func TestContainerInspectionDoesNotMutateTheSession(t *testing.T) {
	c, _ := withContainerCommands(t)

	entriesBefore := len(c.sess.Entries)
	importsBefore := len(c.ev.Imports())

	for _, line := range []string{":services container", ":graph container"} {
		if res := c.Submit(line); res.Err {
			t.Fatalf("%s failed: %s", line, res.Out)
		}
	}

	if got := len(c.sess.Entries); got != entriesBefore {
		t.Errorf("the session gained %d entries", got-entriesBefore)
	}
	if got := len(c.ev.Imports()); got != importsBefore {
		t.Errorf("the session gained %d imports; strings and fmt must not leak in",
			got-importsBefore)
	}
	if res := c.Submit(`len(container)`); res.Err || !strings.Contains(res.Out, "30") {
		t.Errorf("session broken after inspection: %q", res.Out)
	}
}

// TestALongContainerAnswerIsWholeInOut is invariant 19 through a real
// evaluation. pageable opens a modal past the height of a screen, and the
// answer stays whole in Out — a pipe reading `gluon -e ':graph c'` must get the
// same bytes the modal shows.
func TestALongContainerAnswerIsWholeInOut(t *testing.T) {
	c, _ := withContainerCommands(t)

	res := c.Submit(":graph container")
	if res.Err {
		t.Fatalf(":graph failed: %s", res.Out)
	}
	if res.Modal == nil {
		t.Fatalf("a 30-provider answer did not open a modal:\n%s", res.Out)
	}
	if res.Modal.Text != res.Out {
		t.Error("the modal and the linear answer are not the same text")
	}
	for _, want := range []string{"*app.T0", "*app.T29"} {
		if !strings.Contains(res.Out, want) {
			t.Errorf("Out is missing %q, so a pipe would lose it:\n%s", want, res.Out)
		}
	}
}
