package repl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/scratch"
)

// TestParseSaveArgs covers the shapes :save takes. The topic is never
// rewritten: whatever follows the flags is the topic verbatim, spaces
// included, because Slug is what normalises it. Two flags share one parse, so
// they compose in either order and neither can define "topic" differently.
func TestParseSaveArgs(t *testing.T) {
	tests := []struct {
		in        string
		wantDebug bool
		wantTests bool
		wantTopic string
	}{
		{"", false, false, ""},
		{"parser bug", false, false, "parser bug"},
		{"-debug", true, false, ""},
		{"-debug parser bug", true, false, "parser bug"},
		{"  -debug   spaced  ", true, false, "spaced"},
		{"-test", false, true, ""},
		{"-test parser bug", false, true, "parser bug"},
		{"-debug -test", true, true, ""},
		{"-test -debug", true, true, ""},
		{"-debug -test parser bug", true, true, "parser bug"},
		// Only a leading flag is a flag, so a topic keeps its own words.
		{"notes -debug", false, false, "notes -debug"},
		{"notes -test", false, false, "notes -test"},
	}
	for _, tc := range tests {
		debug, tests, topic := parseSaveArgs(tc.in)
		if debug != tc.wantDebug || tests != tc.wantTests || topic != tc.wantTopic {
			t.Errorf("parseSaveArgs(%q) = (%v, %v, %q), want (%v, %v, %q)",
				tc.in, debug, tests, topic, tc.wantDebug, tc.wantTests, tc.wantTopic)
		}
	}
}

// TestSaveDebugReportsThePathAndTheCommand: gluon does not run a debugger, so
// the output has to carry both halves of what to do next — where the module is,
// and the one command that starts a session on it.
func TestSaveDebugReportsThePathAndTheCommand(t *testing.T) {
	c := testCore(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	res := c.save("-debug parser bug")
	if res.Err {
		t.Fatalf(":save -debug failed: %s", res.Out)
	}

	// The path is reported, and it is the module directory that was written.
	var dir string
	for _, line := range strings.Split(res.Out, "\n") {
		if rest, ok := strings.CutPrefix(line, "saved → "); ok {
			dir = filepath.Dir(rest)
		}
	}
	if dir == "" {
		t.Fatalf(":save -debug did not report a path:\n%s", res.Out)
	}
	if _, err := os.Stat(scratch.DebugConfigPath(dir)); err != nil {
		t.Errorf("no launch configuration beside the module: %v", err)
	}
	if !strings.Contains(res.Out, scratch.DebugCommand(dir)) {
		t.Errorf("the output does not name the command that starts a debug session:\n%s", res.Out)
	}
}

// TestSaveWithoutTheFlagWritesNoConfiguration is the other half of the same
// promise: :save's existing behaviour is untouched when the flag is absent.
func TestSaveWithoutTheFlagWritesNoConfiguration(t *testing.T) {
	c := testCore(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	res := c.save("quiet")
	if res.Err {
		t.Fatalf(":save failed: %s", res.Out)
	}
	if strings.Contains(res.Out, "dlv") {
		t.Errorf(":save named a debugger without being asked:\n%s", res.Out)
	}
	path := strings.TrimPrefix(strings.TrimSpace(res.Out), "saved → ")
	if _, err := os.Stat(scratch.DebugConfigPath(filepath.Dir(path))); !os.IsNotExist(err) {
		t.Errorf("a plain :save wrote a launch configuration: %v", err)
	}
}
