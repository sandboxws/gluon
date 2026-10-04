//go:build integration

package repl

import (
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/pretty"
)

// TestPluginCommandDoesNotMutateTheSession is invariant 14 for plugins.
//
// A plugin command evaluates through EvalTransient, the same path :bench, :err
// and :esc use, precisely so that running one leaves nothing behind: not an
// entry, not an import the session did not ask for, not a changed `it`.
func TestPluginCommandDoesNotMutateTheSession(t *testing.T) {
	c, err := NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	defer c.Close()

	if res := c.Submit(`x := []int{1, 2, 3}`); res.Err {
		t.Fatalf("setup failed: %s", res.Out)
	}
	entriesBefore := len(c.sess.Entries)
	importsBefore := len(c.ev.Imports())

	res := c.Submit(`:json x`)
	if res.Err {
		t.Fatalf(":json failed: %s", res.Out)
	}
	if !strings.Contains(res.Out, "[\n  1,\n  2,\n  3\n]") {
		t.Errorf(":json produced %q", res.Out)
	}

	if got := len(c.sess.Entries); got != entriesBefore {
		t.Errorf("the session gained %d entries", got-entriesBefore)
	}
	if got := len(c.ev.Imports()); got != importsBefore {
		t.Errorf("the session gained %d imports; encoding/json must not leak in",
			got-importsBefore)
	}

	// The session still works, and `it` still means what it did.
	if res := c.Submit(`x`); res.Err || !strings.Contains(res.Out, "[1 2 3]") {
		t.Errorf("session broken after a plugin command: %q", res.Out)
	}
}

// TestPluginPreloadSkipsGoimports. A plugin's imports are a name→path map, so a
// command naming json. resolves without the ~135ms goimports pass — and an
// import nothing names is never written at all (invariant 17).
func TestPluginPreloadSkipsGoimports(t *testing.T) {
	c, err := NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	defer c.Close()

	// net/http is preloaded by the http plugin but named by nothing yet.
	if res := c.Submit(`y := 1`); res.Err {
		t.Fatalf("setup failed: %s", res.Out)
	}
	for _, im := range c.ev.Imports() {
		if im.Path == "net/http" {
			t.Error("a preloaded import was written into a program that never named it")
		}
	}

	// Naming it resolves it without goimports having to discover it.
	res := c.Submit(`http.StatusTeapot`)
	if res.Err {
		t.Fatalf("a preloaded qualifier did not resolve: %s", res.Out)
	}
	if !strings.Contains(res.Out, "418") {
		t.Errorf("got %q", res.Out)
	}
}

// TestPluginCommandsAreDispatchable end to end, through Submit rather than the
// registry directly.
func TestPluginCommandsAreDispatchable(t *testing.T) {
	c, err := NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	defer c.Close()

	res := c.Submit(`:when time.Date(2026, 8, 28, 10, 30, 0, 0, time.UTC)`)
	if res.Err {
		t.Fatalf(":when failed: %s", res.Out)
	}
	// The whole reason :when exists: the printed form of a time.Time is
	// {wall:… ext:… loc:…}, and this is not that.
	if strings.Contains(res.Out, "wall:") {
		t.Errorf(":when fell back to the struct rendering: %q", res.Out)
	}
	if !strings.Contains(res.Out, "2026-08-28T10:30:00Z") {
		t.Errorf(":when did not format the time: %q", res.Out)
	}
}

// TestGetActivatesAPluginAndItsRenderer is the whole third-party path end to
// end: an alias resolves, the module arrives, the plugin activates, and its
// renderer is installed — none of which happens per line.
func TestGetActivatesAPluginAndItsRenderer(t *testing.T) {
	online(t)
	c, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if _, ok := c.Hooks()["uuid.UUID"]; ok {
		t.Fatal("the uuid renderer was installed before its module was in the build list")
	}
	for _, cmd := range c.Commands() {
		if cmd.Name == ":sql" {
			t.Fatal("gorm's :sql exists in a session with no gorm")
		}
	}

	// The short name is a plugin alias, and the plugin that knows it is by
	// definition not active yet.
	res := c.Submit(":get uuid")
	if res.Err {
		t.Fatalf(":get uuid failed: %s", res.Out)
	}
	if !strings.Contains(res.Out, "github.com/google/uuid") {
		t.Errorf("the alias did not resolve: %q", res.Out)
	}

	if _, ok := c.Hooks()["uuid.UUID"]; !ok {
		t.Fatal("the uuid renderer was not installed after :get")
	}

	// And it renders a real UUID. Core.Render defaults to Plain because there
	// is no terminal here, so install the rich renderer the way the TUI does —
	// which is also the point: renderers reach Rich and nothing else.
	styles := pretty.PlainStyles()
	hooks := c.Hooks()
	c.Render = func(v []pretty.Value) string { return pretty.RichWith(v, styles, hooks) }

	res = c.Submit(`uuid.MustParse("ee7a1a96-f9ef-4c40-a295-1dc5984b4292")`)
	if res.Err {
		t.Fatalf("uuid.MustParse failed: %s", res.Out)
	}
	if !strings.Contains(res.Out, "ee7a1a96-f9ef-4c40-a295-1dc5984b4292") {
		t.Errorf("the uuid renderer did not produce the canonical form: %q", res.Out)
	}
	if !strings.Contains(res.Out, "v4 random") {
		t.Errorf("the version was not decoded: %q", res.Out)
	}

	// Plain is untouched by any of it — invariant 21.
	c.Render = pretty.Plain
	res = c.Submit(`uuid.MustParse("ee7a1a96-f9ef-4c40-a295-1dc5984b4292")`)
	if strings.Contains(res.Out, "ee7a1a96-f9ef-4c40-a295-1dc5984b4292") {
		t.Errorf("a plugin renderer reached Plain, which pipes and scripts read: %q", res.Out)
	}
}

// TestRenderersReachNestedValues closes the gap the two-form Hook exists for: a
// duration inside a struct used to read differently from one on its own line.
func TestRenderersReachNestedValues(t *testing.T) {
	c, err := NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	defer c.Close()

	styles := pretty.PlainStyles()
	hooks := c.Hooks()
	c.Render = func(v []pretty.Value) string { return pretty.RichWith(v, styles, hooks) }

	if res := c.Submit(`type Job struct{ Name string; Took time.Duration }`); res.Err {
		t.Fatalf("setup failed: %s", res.Out)
	}
	res := c.Submit(`Job{"index", 2*time.Hour + 15*time.Minute}`)
	if res.Err {
		t.Fatalf("eval failed: %s", res.Out)
	}
	if !strings.Contains(res.Out, "8100s") {
		t.Errorf("the duration renderer did not reach the struct field:\n%s", res.Out)
	}

	// A slice of them too.
	res = c.Submit(`[]time.Duration{90*time.Second}`)
	if res.Err {
		t.Fatalf("eval failed: %s", res.Out)
	}
	if !strings.Contains(res.Out, "90s") {
		t.Errorf("the duration renderer did not reach a list element:\n%s", res.Out)
	}
}

// TestRoutesLeavesTheSessionUnchanged is invariant 14 for the one plugin
// command that takes a whole application as its argument.
//
// gorilla/mux is the framework under test because it has no dependencies of its
// own, so what this measures is gluon's behaviour rather than a module graph.
// The rewrite runs through EvalTransient like every other plugin command: it
// reads the router the session already built and leaves no entry, no import and
// no changed `it` behind.
func TestRoutesLeavesTheSessionUnchanged(t *testing.T) {
	online(t)
	c, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	for _, cmd := range c.Commands() {
		if cmd.Name == ":routes" {
			t.Fatal(":routes exists in a session with no web framework")
		}
	}

	// The short name is the plugin's alias, and the plugin that knows the path
	// is by definition the one that is not active yet.
	if res := c.Submit(":get mux"); res.Err {
		t.Fatalf(":get mux failed: %s", res.Out)
	}
	found := false
	for _, cmd := range c.Commands() {
		if cmd.Name == ":routes" {
			found = true
		}
	}
	if !found {
		t.Fatal(":routes did not appear after its module reached the build list")
	}

	for _, line := range []string{
		`r := mux.NewRouter()`,
		`r.HandleFunc("/users/{id}", func(w http.ResponseWriter, q *http.Request) {}).Methods("GET")`,
		`r.HandleFunc("/health", func(w http.ResponseWriter, q *http.Request) {})`,
	} {
		if res := c.Submit(line); res.Err {
			t.Fatalf("setup line %q failed: %s", line, res.Out)
		}
	}

	entriesBefore := len(c.sess.Entries)
	importsBefore := len(c.ev.Imports())

	res := c.Submit(`:routes r`)
	if res.Err {
		t.Fatalf(":routes failed: %s", res.Out)
	}
	for _, want := range []string{
		"METHOD", "PATH", "HANDLER", // the common header
		"GET     /users/{id}",
		"ANY     /health", // no method restriction is ANY, not blank
		"—",               // mux hands over the handler, never its name
	} {
		if !strings.Contains(res.Out, want) {
			t.Errorf(":routes output is missing %q:\n%s", want, res.Out)
		}
	}

	if got := len(c.sess.Entries); got != entriesBefore {
		t.Errorf("the session gained %d entries", got-entriesBefore)
	}
	if got := len(c.ev.Imports()); got != importsBefore {
		t.Errorf("the session gained %d imports", got-importsBefore)
	}

	// A router with no routes says so, rather than printing a header over
	// nothing — the two are different facts about the application.
	if res := c.Submit(`empty := mux.NewRouter()`); res.Err {
		t.Fatalf("setup failed: %s", res.Out)
	}
	res = c.Submit(`:routes empty`)
	if res.Err {
		t.Fatalf(":routes on an empty router failed: %s", res.Out)
	}
	if !strings.Contains(res.Out, "no routes registered") {
		t.Errorf("an empty router did not say so:\n%s", res.Out)
	}
	if strings.Contains(res.Out, "METHOD") {
		t.Errorf("an empty router printed the header anyway:\n%s", res.Out)
	}

	// And the session still works.
	if res := c.Submit(`1 + 1`); res.Err || !strings.Contains(res.Out, "2") {
		t.Errorf("session broken after :routes: %q", res.Out)
	}
}
