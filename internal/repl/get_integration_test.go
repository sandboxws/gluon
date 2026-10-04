//go:build integration

package repl

import (
	"net"
	"strings"
	"testing"
	"time"
)

// online reports whether the module proxy is reachable. :get is the one thing
// gluon does that leaves the machine, so its test is the one that can be
// legitimately skipped.
func online(t *testing.T) {
	t.Helper()
	c, err := net.DialTimeout("tcp", "proxy.golang.org:443", 3*time.Second)
	if err != nil {
		t.Skip("no network")
	}
	c.Close()
}

func TestGetAddsAModuleAndKeepsWorking(t *testing.T) {
	online(t)
	c, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if res := c.Submit(":get"); res.Err || !strings.Contains(res.Out, "no modules added") {
		t.Fatalf(":get on a fresh session = %q (err=%v)", res.Out, res.Err)
	}

	// A module that is small, stable and has no dependencies of its own.
	res := c.Submit(":get github.com/google/uuid")
	if res.Err {
		t.Fatalf(":get failed: %s", res.Out)
	}
	// The resolved version is the thing worth reporting, and the thing the
	// argument does not say.
	if !strings.Contains(res.Out, "github.com/google/uuid v") {
		t.Errorf(":get did not report the resolved version: %q", res.Out)
	}

	// The point of all this: the package is now usable.
	res = c.Submit(`len(uuid.NewString())`)
	if res.Err {
		t.Fatalf("the fetched module is not usable: %s", res.Out)
	}
	if !strings.Contains(res.Out, "36") {
		t.Errorf("got %q, want a 36-character uuid", res.Out)
	}

	if res := c.Submit(":get"); !strings.Contains(res.Out, "github.com/google/uuid") {
		t.Errorf(":get does not list what it added: %q", res.Out)
	}
}

// A failed fetch must leave the session exactly as it was. It reaches the
// network, so it is the command most likely to fail for reasons that have
// nothing to do with the code.
func TestFailedGetLeavesTheSessionAlone(t *testing.T) {
	c, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if res := c.Submit("x := 21"); res.Err {
		t.Fatal(res.Out)
	}
	res := c.Submit(":get example.invalid/definitely/not/a/module")
	if !res.Err {
		t.Fatalf(":get on a bad module succeeded: %q", res.Out)
	}
	if res := c.Submit("x * 2"); res.Err {
		t.Fatalf("the session broke after a failed :get: %s", res.Out)
	} else if !strings.Contains(res.Out, "42") {
		t.Errorf("got %q, want 42", res.Out)
	}
}

// pluginState is the ✓/· :plugins prints beside a plugin's name.
func pluginState(t *testing.T, c *Core, name string) string {
	t.Helper()
	for _, line := range strings.Split(c.Submit(":plugins").Out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[1] == name {
			return f[0]
		}
	}
	t.Fatalf("%s is not in :plugins", name)
	return ""
}

// The whole of :get -rm, end to end: a module that is in use cannot be
// removed, and one that is no longer in use can — taking its plugin with it.
func TestRemoveRefusesWhileInUseAndSucceedsAfterwards(t *testing.T) {
	online(t)
	c, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if res := c.Submit(":get github.com/google/uuid"); res.Err {
		t.Fatalf(":get failed: %s", res.Out)
	}
	if got := pluginState(t, c, "uuid"); got != "✓" {
		t.Fatalf("the uuid plugin is %q after :get, want active", got)
	}
	if res := c.Submit(`len(uuid.NewString())`); res.Err {
		t.Fatalf("the fetched module is not usable: %s", res.Out)
	}

	// A command that left the session unable to build would be a worse answer
	// than a refusal, so it refuses — and names the entry, because "something
	// uses it" is not actionable and "entry 1" is.
	res := c.Submit(":get -rm github.com/google/uuid")
	if !res.Err {
		t.Fatalf(":get -rm removed a module an entry imports: %q", res.Out)
	}
	for _, want := range []string{"github.com/google/uuid", "entry 1", ":drop"} {
		if !strings.Contains(res.Out, want) {
			t.Errorf("the refusal does not mention %q: %q", want, res.Out)
		}
	}
	// Refused means nothing happened, not partly happened.
	if res := c.Submit(":get"); !strings.Contains(res.Out, "github.com/google/uuid") {
		t.Errorf("the requirement changed on a refusal: %q", res.Out)
	}

	if res := c.Submit(":drop 1"); res.Err {
		t.Fatalf(":drop 1: %s", res.Out)
	}
	res = c.Submit(":get -rm github.com/google/uuid")
	if res.Err {
		t.Fatalf(":get -rm after :drop: %s", res.Out)
	}
	if !strings.Contains(res.Out, "removed github.com/google/uuid v") {
		t.Errorf(":get -rm did not name what it removed with its version: %q", res.Out)
	}
	if res := c.Submit(":get"); !strings.Contains(res.Out, "no modules added") {
		t.Errorf(":get still lists something after the removal: %q", res.Out)
	}
	// The build list is what decides activation, so a plugin whose module has
	// gone must have gone with it (invariant 22).
	if got := pluginState(t, c, "uuid"); got != "·" {
		t.Errorf("the uuid plugin is %q after its module was removed, want inactive", got)
	}
}

// The result cache is keyed on program text alone, so the same line after a
// removal must be built again rather than answered from before it — invariant
// 18's third, and the one that fails invisibly.
//
// :reset is what clears the session between the three runs: it re-evaluates
// nothing, so the phases :time reports belong to the line under test and to
// nothing else.
func TestALineIsRebuiltAfterARemoval(t *testing.T) {
	online(t)
	c, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if res := c.Submit(":get github.com/google/uuid"); res.Err {
		t.Fatalf(":get failed: %s", res.Out)
	}
	if res := c.Submit(":time"); res.Err {
		t.Fatalf(":time: %s", res.Out)
	}

	// A call the type checker cannot fold: it has to be built and run, so the
	// second identical line has something to be served from the cache instead.
	const line = `strings.Repeat("ab", 2)`

	// Twice, because the first run resolves imports with goimports and the
	// second renders from the cached import set — very slightly different text,
	// and the cache is keyed on the text. The second is the one whose program
	// every later run reproduces.
	for i := 0; i < 2; i++ {
		res := c.Submit(line)
		if res.Err {
			t.Fatalf("%s: %s", line, res.Out)
		}
		if !strings.Contains(res.Out, "build ") {
			t.Fatalf("run %d did not build, so there is no cache to test:\n%s", i, res.Out)
		}
		c.Submit(":reset")
	}

	cached := c.Submit(line)
	if strings.Contains(cached.Out, "build ") {
		t.Fatalf("the same line was built twice, so the cache is not answering:\n%s", cached.Out)
	}
	if !strings.Contains(cached.Out, "cache-hit") {
		t.Fatalf("the second run reports no cache hit:\n%s", cached.Out)
	}

	c.Submit(":reset")
	if res := c.Submit(":get -rm github.com/google/uuid"); res.Err {
		t.Fatalf(":get -rm: %s", res.Out)
	}
	after := c.Submit(line)
	if after.Err {
		t.Fatalf("%s after a removal: %s", line, after.Out)
	}
	if strings.Contains(after.Out, "cache-hit") || !strings.Contains(after.Out, "build ") {
		t.Errorf("the same line was served from the cache across a removal:\n%s", after.Out)
	}
}

// The moment a user most wants to see what a module offers is the moment it
// arrives, and reading its export data costs ~150ms. After :get it is read off
// the keystroke path, so the first `pkg.` answers from the cache Loaded reads.
func TestGetWarmsTheModulesRootPackage(t *testing.T) {
	online(t)
	c, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if res := c.Submit(":get github.com/google/uuid"); res.Err {
		t.Fatalf(":get failed: %s", res.Out)
	}
	// No entry has imported it: this is the warm-up's work or nothing.
	if len(c.sess.Entries) != 0 {
		t.Fatalf("the session has %d entries", len(c.sess.Entries))
	}

	deadline := time.Now().Add(10 * time.Second)
	for c.ev.Loaded("github.com/google/uuid") == nil {
		if time.Now().After(deadline) {
			t.Fatal("the module's root package was never loaded, so the first uuid. still pays for it")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// And the point of loading it: completion answers from what is there.
	got := c.Complete("uuid.NewStr")
	if len(got) != 1 || got[0] != "uuid.NewString" {
		t.Errorf("Complete(\"uuid.NewStr\") = %v, want [uuid.NewString]", got)
	}
}

// :reset -deps is the other half of removal: the entries and the requirements
// go together, and the plugins that depended on them go with the requirements.
func TestResetDepsClearsTheModulesToo(t *testing.T) {
	online(t)
	c, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if res := c.Submit(":get github.com/google/uuid"); res.Err {
		t.Fatalf(":get failed: %s", res.Out)
	}
	if res := c.Submit(`len(uuid.NewString())`); res.Err {
		t.Fatalf("the fetched module is not usable: %s", res.Out)
	}

	// Bare :reset clears the entries and leaves the modules, so a session
	// cleared to try the same library a different way need not fetch it again.
	if res := c.Submit(":reset"); res.Err || res.Out != "session cleared" {
		t.Fatalf(":reset = %q (err=%v)", res.Out, res.Err)
	}
	if res := c.Submit(":get"); !strings.Contains(res.Out, "github.com/google/uuid") {
		t.Fatalf("a bare :reset dropped the requirements: %q", res.Out)
	}
	if got := pluginState(t, c, "uuid"); got != "✓" {
		t.Fatalf("the uuid plugin is %q after a bare :reset, want still active", got)
	}

	if res := c.Submit(`len(uuid.NewString())`); res.Err {
		t.Fatalf("the module is no longer usable after :reset: %s", res.Out)
	}

	res := c.Submit(":reset -deps")
	if res.Err {
		t.Fatalf(":reset -deps: %s", res.Out)
	}
	if !strings.HasPrefix(res.Out, "session cleared") {
		t.Errorf(":reset -deps = %q, want it to say the session was cleared first", res.Out)
	}
	if !strings.Contains(res.Out, "removed github.com/google/uuid v") {
		t.Errorf(":reset -deps did not name what it removed: %q", res.Out)
	}
	if len(c.sess.Entries) != 0 {
		t.Errorf("%d entries left after :reset -deps", len(c.sess.Entries))
	}
	if res := c.Submit(":get"); !strings.Contains(res.Out, "no modules added") {
		t.Errorf(":get still lists something after :reset -deps: %q", res.Out)
	}
	if got := pluginState(t, c, "uuid"); got != "·" {
		t.Errorf("the uuid plugin is %q after :reset -deps, want inactive", got)
	}
}
