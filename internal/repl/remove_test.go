package repl

import (
	"strings"
	"testing"
)

// :get -rm names no module. The Arg spec carries no "<", so the usage test
// that walks the registry cannot cover this one — the flag is what makes the
// operand required, and only the command knows that.
func TestBareRemoveReportsUsage(t *testing.T) {
	c := testCore(t)
	res := c.Submit(":get -rm")
	if !res.Err || !strings.HasPrefix(res.Out, "usage:") {
		t.Errorf(":get -rm = %q (err=%v), want a usage line", res.Out, res.Err)
	}
}

// A module the session never added and one the host owns are different
// answers, and the difference is the whole of what the user does next.
func TestRemoveSaysWhichKindOfNotRequired(t *testing.T) {
	c := testCore(t)
	res := c.Submit(":get -rm example.com/never/added")
	if !res.Err {
		t.Fatalf(":get -rm of a module nothing requires succeeded: %q", res.Out)
	}
	if !strings.Contains(res.Out, "not required by this session") {
		t.Errorf("got %q, want it to say the session does not require it", res.Out)
	}
	if !strings.Contains(res.Out, "example.com/never/added") {
		t.Errorf("got %q, want the module named", res.Out)
	}
}

// -rm is a whole word. `-rmdir/x` is a module path, not a flag and an
// argument, and reading it as one would remove something never named.
func TestRemoveFlagIsAWholeWord(t *testing.T) {
	for _, arg := range []string{"-rm", "-rm ", "-rm  x"} {
		if _, ok := cutFlag(strings.TrimSpace(arg), "-rm"); !ok {
			t.Errorf("cutFlag(%q) did not see the flag", arg)
		}
	}
	for _, arg := range []string{"-rmdir/x", "-rmx", "x -rm", ""} {
		if _, ok := cutFlag(arg, "-rm"); ok {
			t.Errorf("cutFlag(%q) read a flag that is not there", arg)
		}
	}
	if rest, _ := cutFlag("-rm  github.com/a/b", "-rm"); rest != "github.com/a/b" {
		t.Errorf("cutFlag returned %q", rest)
	}
}

// Bare :reset is unchanged, byte for byte. It has an MCP tool and a decade of
// muscle memory behind it; growing a flag must not alter what it says.
func TestBareResetIsUnchanged(t *testing.T) {
	c := testCore(t)
	if res := c.Submit("x := 1"); res.Err {
		t.Fatal(res.Out)
	}
	res := c.Submit(":reset")
	if res.Err || res.Out != "session cleared" {
		t.Errorf(":reset = %q (err=%v), want exactly \"session cleared\"", res.Out, res.Err)
	}
	if len(c.sess.Entries) != 0 {
		t.Errorf("%d entries left after :reset", len(c.sess.Entries))
	}
}

// A flag :reset does not have must be refused rather than silently treated as
// the bare form: clearing the modules is not undoable by another command.
func TestResetRefusesAnUnknownFlag(t *testing.T) {
	c := testCore(t)
	res := c.Submit(":reset -all")
	if !res.Err || !strings.HasPrefix(res.Out, "usage:") {
		t.Errorf(":reset -all = %q (err=%v), want a usage line", res.Out, res.Err)
	}
}

// -deps on a session with nothing to remove still clears the session, and says
// only that. The removals are reported when there are removals.
func TestResetDepsWithNoModules(t *testing.T) {
	c := testCore(t)
	if res := c.Submit("x := 1"); res.Err {
		t.Fatal(res.Out)
	}
	res := c.Submit(":reset -deps")
	if res.Err || res.Out != "session cleared" {
		t.Errorf(":reset -deps = %q (err=%v), want exactly \"session cleared\"", res.Out, res.Err)
	}
	if len(c.sess.Entries) != 0 {
		t.Errorf("%d entries left after :reset -deps", len(c.sess.Entries))
	}
}

// The help gutter is what makes :help readable, and both entries grew.
func TestModuleCommandsStillFitTheHelpGutter(t *testing.T) {
	c := &Core{}
	for _, name := range []string{":get", ":reset"} {
		cmd, ok := c.lookup(name)
		if !ok {
			t.Fatalf("%s is not in the registry", name)
		}
		if w := len(cmd.Name) + 1 + len(cmd.Arg); w > helpWidth {
			t.Errorf("%s %s is %d wide, past the %d-column gutter", cmd.Name, cmd.Arg, w, helpWidth)
		}
	}
}

// TestWhatIsLeftIsCountedInWords: one entry left is one entry, not one
// entries.
func TestWhatIsLeftIsCountedInWords(t *testing.T) {
	c := testCore(t)
	for _, l := range []string{"x := 1", "y := 2"} {
		if res := c.Submit(l); res.Err {
			t.Fatalf("%s: %s", l, res.Out)
		}
	}
	if out := c.Submit(":undo").Out; out != "undone (1 entry left)" {
		t.Errorf(":undo = %q", out)
	}
	if res := c.Submit("z := 3"); res.Err {
		t.Fatal(res.Out)
	}
	if out := c.Submit(":drop 1").Out; out != "dropped 1 (1 entry left)" {
		t.Errorf(":drop 1 = %q", out)
	}
	if out := c.Submit(":undo").Out; out != "undone (0 entries left)" {
		t.Errorf(":undo = %q", out)
	}
}
