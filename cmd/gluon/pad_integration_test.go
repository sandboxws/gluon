//go:build integration

// The command line's half of the scratchpad: what the REPL wrote is what
// `gluon scratch` lists, and a saved scratchpad is a scratch `gluon run`
// reaches by name. Both are claims about two processes and a directory, so
// neither can be shown without really writing one. Run with:
//
//	go test -tags=integration ./cmd/gluon/
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/repl"
	"github.com/sandboxws/gluon/internal/scratch"
)

func TestGluonScratchListsWhatTheReplWrote(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// A REPL session, doing what a REPL session does.
	c, err := repl.NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	if res, _ := c.OpenPad("parser-bug"); res.Err {
		t.Fatalf("opening: %s", res.Out)
	}
	if res := c.Submit("x := 1"); res.Err {
		t.Fatalf("x := 1: %s", res.Out)
	}
	if res := c.Submit("x + 1"); res.Err {
		t.Fatalf("x + 1: %s", res.Out)
	}
	c.Close()

	// And the command line, which shares nothing with it but the tree.
	out := captureStdout(t, func() {
		if code := listPads(); code != 0 {
			t.Fatalf("gluon scratch exited %d", code)
		}
	})
	if !strings.Contains(out, "parser-bug") {
		t.Errorf("the listing does not name what the REPL wrote:\n%s", out)
	}
	if !strings.Contains(out, "2 entries") {
		t.Errorf("the listing does not count what the REPL wrote:\n%s", out)
	}

	// And `show` prints the lines themselves, running none of them.
	shown := captureStdout(t, func() {
		if code := showPad("parser-bug"); code != 0 {
			t.Fatalf("gluon scratch show exited %d", code)
		}
	})
	for _, want := range []string{"x := 1", "x + 1"} {
		if !strings.Contains(shown, want) {
			t.Errorf("show is missing %q:\n%s", want, shown)
		}
	}
}

// TestGluonRunReachesAPadByName: a scratchpad is not a runnable scratch until
// it has a program, and once :save has given it one it is a scratch like any
// other.
func TestGluonRunReachesAPadByName(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	c, err := repl.NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	if res, _ := c.OpenPad("runnable"); res.Err {
		t.Fatalf("opening: %s", res.Out)
	}
	if res := c.Submit(`fmt.Println("from the pad")`); res.Err {
		t.Fatalf("the line: %s", res.Out)
	}

	// Before :save there is no program, and the runner says so rather than
	// reporting a missing go.mod.
	if _, err := resolveTarget(scratch.PadDir("runnable")); err == nil {
		t.Error("a pad with no program resolved as something to run")
	}

	if res := c.Submit(":save"); res.Err {
		t.Fatalf(":save: %s", res.Out)
	}
	c.Close()

	if _, err := os.Stat(filepath.Join(scratch.PadDir("runnable"), "main.go")); err != nil {
		t.Fatalf("the pad has no program after :save: %v", err)
	}
	target, err := resolveTarget(scratch.PadDir("runnable"))
	if err != nil {
		t.Fatalf("a saved pad does not resolve as a scratch: %v", err)
	}
	if code := runScratch(target, nil, false); code != 0 {
		t.Errorf("running the saved pad exited %d", code)
	}
}
