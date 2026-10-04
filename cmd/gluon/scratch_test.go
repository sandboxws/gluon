package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/scratch"
	"github.com/sandboxws/gluon/internal/session"
)

// runNew drives `gluon new` the way main does, through normalizeArgs, so the
// single-dash spelling the documentation prints is what the test actually
// exercises.
func runNew(t *testing.T, args ...string) error {
	t.Helper()
	root := newRootCmd()
	root.SetArgs(normalizeArgs(root, args))
	root.SetOut(os.Stderr)
	return root.Execute()
}

// TestNewDebugWritesTheLaunchConfiguration: the flag scaffolds a scratch a
// debugger can open, in its single-dash long form — the one every invocation
// the documentation prints uses.
func TestNewDebugWritesTheLaunchConfiguration(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)

	if err := runNew(t, "new", "parser bug", "-debug", "-no-edit", "-no-prompt"); err != nil {
		t.Fatalf("gluon new -debug: %v", err)
	}

	root := filepath.Join(data, "gluon", "scratch")
	ents, err := os.ReadDir(root)
	if err != nil || len(ents) != 1 {
		t.Fatalf("expected one scratch under %s: %v %v", root, ents, err)
	}
	dir := filepath.Join(root, ents[0].Name())
	if _, err := os.Stat(scratch.DebugConfigPath(dir)); err != nil {
		t.Errorf("no launch configuration in the scratch: %v", err)
	}
}

// TestNewDebugRefusesTheFlatForm: the flat form is not a module, so there is
// nothing for a debugger to build. The command says so and writes nothing.
func TestNewDebugRefusesTheFlatForm(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)

	err := runNew(t, "new", "x", "-debug", "-flat", "-no-edit", "-no-prompt")
	if err == nil {
		t.Fatal("gluon new -debug -flat was accepted")
	}
	// A usage conflict, not a failed scaffold — the same status -host with
	// -flat returns.
	if code, ok := err.(exitErr); !ok || code != 2 {
		t.Errorf("exit status = %v, want 2", err)
	}
	if _, err := os.Stat(filepath.Join(data, "gluon", "scratch")); !os.IsNotExist(err) {
		t.Errorf("the refused scaffold wrote into the scratch root: %v", err)
	}
}

// TestNewDebugNamesTheCommand: gluon writes a configuration and runs nothing,
// so the command that starts a debug session has to be printed.
func TestNewDebugNamesTheCommand(t *testing.T) {
	if !strings.Contains(scratch.DebugCommand("/tmp/x"), "dlv") {
		t.Errorf("DebugCommand does not name a debugger: %q", scratch.DebugCommand("/tmp/x"))
	}
}

// TestRunNamesAPadWithNoProgram: a scratchpad is a directory in the scratch
// tree that is deliberately not a module until :save has run in it, so "no
// go.mod" is true and tells the reader nothing about what to do.
func TestRunNamesAPadWithNoProgram(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)

	p := &scratch.Pad{Name: "notes", Dir: scratch.PadDir("notes"), Sess: &session.Session{}}
	if err := scratch.WritePad(p); err != nil {
		t.Fatal(err)
	}

	_, err := resolveTarget(scratch.PadDir("notes"))
	if err == nil {
		t.Fatal("a scratchpad with no program resolved as something to run")
	}
	if !strings.Contains(err.Error(), "scratchpad") || !strings.Contains(err.Error(), ":save") {
		t.Errorf("the message does not say what it is or how to give it a program: %v", err)
	}
}
