//go:build integration

package repl

import (
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/plugin"
)

// configLike is the :config command adapted exactly as the viper and koanf
// plugins are, with a Rewrite that answers from the standard library.
//
// The real plugins name viper and koanf, and gluon links neither — constraint
// B, the reason they are plugins at all — so a test that ran their Rewrite
// would have to `:get` a module over the network. What is under test here is
// gluon's half: that a Redact command's output is redacted on display, that the
// redaction is not in the generated source, and that running one leaves the
// session exactly as it was. The library's own API is the half gluon cannot
// hold an opinion about.
func configLike() Command {
	return adapt(plugin.TaggedCommand{
		Plugin: "viper",
		Command: plugin.Command{
			Name: ":config", Arg: "<v>", Text: true, Redact: true,
			Summary: "the settings this config library resolved",
			Rewrite: func(arg string) (string, error) {
				return "func() string { return strings.Join([]string{" +
					"\"database.url = postgres://ada:hunter2@db.internal:5432/app\", " +
					"\"database.pool = 25\", " +
					"\"api.token = Bearer sq7Kd0aMzX9vLpQr2TfY\", " +
					"\"log.level = debug\"}, \"\\n\") }()", nil
			},
		},
	})
}

// TestConfigDoesNotMutateTheSession is invariant 14 for :config. It evaluates
// through EvalTransient like every plugin command, so it must leave no entry,
// no import and no changed `it` behind.
func TestConfigDoesNotMutateTheSession(t *testing.T) {
	c, err := NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	defer c.Close()

	c.extra = append(c.extra, configLike())
	c.invalidateCommands()

	if res := c.Submit(`x := []int{1, 2, 3}`); res.Err {
		t.Fatalf("setup failed: %s", res.Out)
	}
	entriesBefore := len(c.sess.Entries)
	importsBefore := len(c.ev.Imports())

	res := c.Submit(`:config v`)
	if res.Err {
		t.Fatalf(":config failed: %s", res.Out)
	}

	if got := len(c.sess.Entries); got != entriesBefore {
		t.Errorf("the session gained %d entries", got-entriesBefore)
	}
	if got := len(c.ev.Imports()); got != importsBefore {
		t.Errorf("the session gained %d imports; strings must not leak in",
			got-importsBefore)
	}
	if res := c.Submit(`x`); res.Err || !strings.Contains(res.Out, "[1 2 3]") {
		t.Errorf("session broken after :config: %q", res.Out)
	}
}

// TestConfigRedactsOnDisplay, through the real toolchain: the child prints the
// settings whole and what reaches the screen is redacted.
func TestConfigRedactsOnDisplay(t *testing.T) {
	c, err := NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	defer c.Close()

	c.extra = append(c.extra, configLike())
	c.invalidateCommands()

	res := c.Submit(`:config v`)
	if res.Err {
		t.Fatalf(":config failed: %s", res.Out)
	}
	for _, secret := range []string{"hunter2", "sq7Kd0aMzX9vLpQr2TfY"} {
		if strings.Contains(res.Out, secret) {
			t.Errorf("%s reached the screen:\n%s", secret, res.Out)
		}
	}
	for _, want := range []string{
		"database.url = postgres://ada:***@db.internal:5432/app",
		"api.token = Bearer ***",
		"database.pool = 25",
		"log.level = debug",
	} {
		if !strings.Contains(res.Out, want) {
			t.Errorf("missing %q in:\n%s", want, res.Out)
		}
	}
	if !strings.Contains(res.Out, "redacted by shape") {
		t.Errorf("the output does not say values were redacted:\n%s", res.Out)
	}
}

// TestConfigLeavesTheTranscriptAlone. :src is what a session can be rebuilt
// from, so a command that answered a question must not appear in it.
func TestConfigLeavesTheTranscriptAlone(t *testing.T) {
	c, err := NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	defer c.Close()

	c.extra = append(c.extra, configLike())
	c.invalidateCommands()

	if res := c.Submit(`x := 1`); res.Err {
		t.Fatalf("setup failed: %s", res.Out)
	}
	before := c.Submit(`:src`).Out
	if res := c.Submit(`:config v`); res.Err {
		t.Fatalf(":config failed: %s", res.Out)
	}
	after := c.Submit(`:src`).Out
	if before != after {
		t.Errorf(":config changed the transcript:\n--- before ---\n%s\n--- after ---\n%s",
			before, after)
	}
	if strings.Contains(after, "database.url") || strings.Contains(after, "AllKeys") {
		t.Errorf(":config's own source reached the transcript:\n%s", after)
	}
}
