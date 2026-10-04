package scratch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDebugWritesTheLaunchConfiguration: the file appears when the option is
// set, and only then. Two scratches are scaffolded so the absent case is
// asserted against the same scaffolding rather than an empty tree.
func TestDebugWritesTheLaunchConfiguration(t *testing.T) {
	isolate(t)

	plain, err := New(Options{Topic: "quiet"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(DebugConfigPath(filepath.Dir(plain))); !os.IsNotExist(err) {
		t.Errorf("a scratch scaffolded without Debug has a launch configuration: %v", err)
	}

	p, err := New(Options{Topic: "loud", Debug: true})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(p)
	data, err := os.ReadFile(DebugConfigPath(dir))
	if err != nil {
		t.Fatalf("no launch configuration was written: %v", err)
	}
	// The debugger has to be pointed at the module, and the module is the
	// directory the scratch was scaffolded into.
	if !strings.Contains(string(data), dir) {
		t.Errorf("the configuration does not name the module:\n%s", data)
	}
}

// TestDebugConfigIsEncodedNotConcatenated: a path with a quote in it still
// produces a file an editor can parse. The scratch root comes from
// XDG_DATA_HOME, so this is reachable from a home directory alone.
func TestDebugConfigIsEncodedNotConcatenated(t *testing.T) {
	dir := `/tmp/a "quoted" dir/2026-01-01-x`
	data, err := debugConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got launchFile
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("the configuration is not valid JSON: %v\n%s", err, data)
	}
	if len(got.Configurations) != 1 || got.Configurations[0].Program != dir {
		t.Errorf("the path did not survive the round trip: %+v", got)
	}
}

// TestDebugRefusesTheFlatForm: the two cannot both hold, and the refusal
// happens before anything is written.
func TestDebugRefusesTheFlatForm(t *testing.T) {
	root := isolate(t)

	_, err := New(Options{Topic: "both", Debug: true, Flat: true})
	if err == nil {
		t.Fatal("New accepted Debug together with Flat")
	}
	// The message has to say why, not just that.
	if !strings.Contains(err.Error(), "go.mod") {
		t.Errorf("the refusal does not name the reason: %v", err)
	}
	if ents, err := os.ReadDir(root); err == nil && len(ents) > 0 {
		t.Errorf("the refused scaffold still wrote %d entries", len(ents))
	}
}

// TestDebugLeavesTheProgramAlone is the promise that lets -debug be a flag on
// an existing command rather than a different one: it adds a file, it does not
// change the program.
func TestDebugLeavesTheProgramAlone(t *testing.T) {
	isolate(t)
	const body = "package main\n\nfunc main() { println(1) }\n"

	plain, err := New(Options{Topic: "same", Body: body})
	if err != nil {
		t.Fatal(err)
	}
	debug, err := New(Options{Topic: "same", Body: body, Debug: true})
	if err != nil {
		t.Fatal(err)
	}

	a, err := os.ReadFile(plain)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(debug)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Errorf("the program differs with -debug:\n%s\n---\n%s", a, b)
	}
}
