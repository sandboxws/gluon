package check

import (
	"fmt"
	"go/constant"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/render"
)

// newTestChecker builds the same one-file temp module eval uses, so the
// checker is exercised against a real export set rather than a mock.
func newTestChecker(t *testing.T) *Checker {
	t.Helper()
	dir := t.TempDir()

	out, err := exec.Command("go", "env", "GOVERSION").Output()
	if err != nil {
		t.Skip("no go toolchain on PATH")
	}
	v := strings.TrimPrefix(strings.TrimSpace(string(out)), "go")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		t.Fatalf("unparseable go version %q", v)
	}
	mod := fmt.Sprintf("module gluon.local/session\n\ngo %s.%s\n", parts[0], parts[1])
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, src := range render.RuntimeFiles() {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return New(Options{
		Dir:     dir,
		Env:     append(os.Environ(), "GOFLAGS=", "GOWORK=off", "GOPROXY=off", "GOTOOLCHAIN=local"),
		Runtime: render.RuntimeFiles(),
	})
}

func check(t *testing.T, c *Checker, src string) *Result {
	t.Helper()
	res, err := c.Check([]byte(src))
	if err != nil {
		t.Fatalf("Check(%q): %v", src, err)
	}
	return res
}

func TestCheckCleanProgram(t *testing.T) {
	c := newTestChecker(t)
	res := check(t, c, `package main

import "strings"

func main() {
	x := strings.ToUpper("hi")
	_ = x
	__gluonPrint(x)
}
`)
	if len(res.Errs) != 0 {
		t.Fatalf("expected no diagnostics, got %v", res.Errs)
	}
	tail, ok := res.Printed(render.PrintFunc)
	if !ok {
		t.Fatal("no trailing print found")
	}
	if got := tail.TV.Type.String(); got != "string" {
		t.Errorf("tail type = %q, want string", got)
	}
	if tail.Void || tail.Const {
		t.Errorf("tail Void=%v Const=%v, want both false", tail.Void, tail.Const)
	}
}

func TestCheckReportsAllErrors(t *testing.T) {
	c := newTestChecker(t)
	res := check(t, c, `package main

func main() {
	__gluonPrint(nope1)
	__gluonPrint(nope2)
}
`)
	if len(res.Errs) < 2 {
		t.Fatalf("want at least 2 diagnostics (the Error hook must not stop at the first), got %d: %v", len(res.Errs), res.Errs)
	}
	// The message must read like the compiler's, so one rewriter serves both.
	if got := res.Errs[0].Error(); !strings.Contains(got, "main.go:4:") || !strings.Contains(got, "undefined: nope1") {
		t.Errorf("diagnostic = %q, want main.go:4:...undefined: nope1", got)
	}
}

// A call with no results cannot be printed. The checker has to say so even
// though wrapping it in the printer is itself the error being reported.
func TestVoidCallIsTypedDespiteTheError(t *testing.T) {
	c := newTestChecker(t)
	res := check(t, c, `package main

import "slices"

func main() {
	x := []int{3, 1, 2}
	_ = x
	__gluonPrint(slices.Sort(x))
}
`)
	if len(res.Errs) == 0 {
		t.Fatal("expected the (no value) used as value diagnostic")
	}
	tail, ok := res.Printed(render.PrintFunc)
	if !ok {
		t.Fatal("no trailing print found")
	}
	if !tail.Void {
		t.Errorf("tail.Void = false, want true (type %v)", tail.TV.Type)
	}
	if voids := res.VoidCalls(render.PrintFunc); len(voids) != 1 {
		t.Errorf("VoidCalls returned %d, want 1", len(voids))
	}
}

// The entry needing re-rendering is often not the newest one: a mid-session
// sort is replayed on every later line.
func TestVoidCallMidSession(t *testing.T) {
	c := newTestChecker(t)
	res := check(t, c, `package main

import "slices"

func main() {
	x := []int{3, 1, 2}
	_ = x
	__gluonPrint(slices.Sort(x))
	__gluonPrint(x)
}
`)
	voids := res.VoidCalls(render.PrintFunc)
	if len(voids) != 1 {
		t.Fatalf("VoidCalls returned %d, want 1", len(voids))
	}
	tail, _ := res.Printed(render.PrintFunc)
	if tail.Void {
		t.Error("the trailing entry is x, which is not void")
	}
}

func TestMultiValueTail(t *testing.T) {
	c := newTestChecker(t)
	res := check(t, c, `package main

import "strconv"

func main() {
	__gluonPrint(strconv.Atoi("12"))
}
`)
	if len(res.Errs) != 0 {
		t.Fatalf("unexpected diagnostics: %v", res.Errs)
	}
	tail, _ := res.Printed(render.PrintFunc)
	tup, ok := tail.TV.Type.(*types.Tuple)
	if !ok {
		t.Fatalf("tail type = %T (%v), want *types.Tuple", tail.TV.Type, tail.TV.Type)
	}
	if tup.Len() != 2 {
		t.Errorf("tuple len = %d, want 2", tup.Len())
	}
}

func TestConstantTail(t *testing.T) {
	c := newTestChecker(t)
	for _, tc := range []struct {
		src  string
		want string
		kind constant.Kind
	}{
		{"1 + 2", "3", constant.Int},
		{`len("héllo")`, "6", constant.Int}, // bytes, not runes: the documented gotcha
		{"'a'", "97", constant.Int},
		{`"ab" + "cd"`, `"abcd"`, constant.String},
		{"1 < 2", "true", constant.Bool},
	} {
		res := check(t, c, "package main\n\nfunc main() {\n\t__gluonPrint("+tc.src+")\n}\n")
		if len(res.Errs) != 0 {
			t.Fatalf("%s: unexpected diagnostics %v", tc.src, res.Errs)
		}
		tail, _ := res.Printed(render.PrintFunc)
		if !tail.Const {
			t.Errorf("%s: Const = false, want true", tc.src)
			continue
		}
		if got := tail.TV.Value.String(); got != tc.want {
			t.Errorf("%s: value = %s, want %s", tc.src, got, tc.want)
		}
		if got := tail.TV.Value.Kind(); got != tc.kind {
			t.Errorf("%s: kind = %v, want %v", tc.src, got, tc.kind)
		}
	}
}

// A value that only exists at run time must never be mistaken for a constant.
func TestNonConstantTail(t *testing.T) {
	c := newTestChecker(t)
	res := check(t, c, `package main

import "time"

func main() {
	__gluonPrint(time.Now())
}
`)
	tail, _ := res.Printed(render.PrintFunc)
	if tail.Const {
		t.Error("time.Now() reported as a constant")
	}
}

// The import set only has to be loaded when it grows. That is what keeps the
// steady-state check in the microseconds.
func TestExportSetLoadedOnlyWhenImportsGrow(t *testing.T) {
	c := newTestChecker(t)
	prog := func(imp, expr string) string {
		return "package main\n\nimport \"" + imp + "\"\n\nfunc main() {\n\t__gluonPrint(" + expr + ")\n}\n"
	}
	check(t, c, prog("strings", `strings.ToUpper("a")`))
	first := len(c.exports)
	if first == 0 {
		t.Fatal("no export data loaded")
	}
	check(t, c, prog("strings", `strings.ToLower("A")`))
	if len(c.exports) != first {
		t.Errorf("export set changed on an unchanged import set: %d -> %d", first, len(c.exports))
	}
	// encoding/hex is deliberately not a transitive dependency of the injected
	// runtime; slices is, via sort, so it would already be loaded.
	check(t, c, prog("encoding/hex", `hex.EncodeToString([]byte{1})`))
	if len(c.exports) <= first {
		t.Errorf("export set did not grow for a new import: %d -> %d", first, len(c.exports))
	}
}

func TestSyntaxErrorIsReturnedNotDisabling(t *testing.T) {
	c := newTestChecker(t)
	if _, err := c.Check([]byte("package main\n\nfunc main() {\n\tx := \n}\n")); err == nil {
		t.Fatal("want a parse error")
	}
	if c.Disabled() != nil {
		t.Errorf("a syntax error disabled the checker: %v", c.Disabled())
	}
	check(t, c, "package main\n\nfunc main() {\n\t__gluonPrint(1)\n}\n")
}

// Warm is what makes `pkg.` answer on the first keystroke after :get. The
// promise it has to keep is Loaded's: not that the export data was read, but
// that the types are in the cache Loaded reads.
func TestWarmPutsThePackageWhereLoadedFindsIt(t *testing.T) {
	c := newTestChecker(t)
	if c.Loaded("strings") != nil {
		t.Fatal("strings is loaded before anything asked for it")
	}
	c.Warm("strings")
	pkg := c.Loaded("strings")
	if pkg == nil {
		t.Fatal("Loaded is still nil after Warm, so completion would still pay for the load")
	}
	if pkg.Scope().Lookup("Builder") == nil {
		t.Error("the warmed package has no Builder, so it is not really loaded")
	}
	// Warming twice is the ordinary case — a module re-added, a repeat :get —
	// and the second must be free rather than another `go list`.
	c.Warm("strings")
	if c.Loaded("strings") != pkg {
		t.Error("a second Warm replaced the package instead of leaving it")
	}
}

// A module root need not be a package: k8s.io/api has no code in it. That is
// not a condition to report, and above all it must not disable the checker —
// every later line would then pay v1's prices for a warm-up nobody asked to be
// authoritative.
func TestWarmOnSomethingThatIsNotAPackageChangesNothing(t *testing.T) {
	c := newTestChecker(t)
	c.Warm("example.invalid/not/a/package")
	if err := c.Disabled(); err != nil {
		t.Fatalf("a failed warm-up disabled the checker: %v", err)
	}
	if c.Loaded("example.invalid/not/a/package") != nil {
		t.Error("something was loaded for a path that is not a package")
	}
	// The checker still works, which is the whole claim.
	res, err := c.Check([]byte("package main\n\nfunc main() { _ = 1 }\n"))
	if err != nil {
		t.Fatalf("Check after a failed warm-up: %v", err)
	}
	if len(res.Errs) != 0 {
		t.Errorf("Check reported %v", res.Errs)
	}
}

// The warm-up runs on a goroutine while the user types the next line, so the
// caches it fills are the ones a Check reads. Without the mutex this is the
// test that reports a data race.
func TestWarmIsSafeBesideACheck(t *testing.T) {
	c := newTestChecker(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, p := range []string{"strings", "sort", "errors", "bytes"} {
			c.Warm(p)
		}
	}()
	for i := 0; i < 4; i++ {
		if _, err := c.Check([]byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Print(1) }\n")); err != nil {
			t.Fatalf("Check beside a warm-up: %v", err)
		}
	}
	<-done
}
