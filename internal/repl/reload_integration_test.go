//go:build integration

// These build a real host module, evaluate against it, and edit it underneath
// the session. Run with:
//
//	go test -tags=integration ./internal/repl/
package repl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeGreeter builds a host whose one function returns a string a test can
// recognise, and returns the path of the file to edit.
func writeGreeter(t *testing.T, answer string) (dir, file string) {
	t.Helper()
	dir = t.TempDir()
	files := map[string]string{
		"go.mod":                  "module example.com/proj\n\ngo 1.24\n",
		"internal/greet/greet.go": "package greet\n\nfunc Hello() string { return \"" + answer + "\" }\n",
	}
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir, filepath.Join(dir, "internal", "greet", "greet.go")
}

func rewriteGreeter(t *testing.T, file, answer string) {
	t.Helper()
	if err := os.WriteFile(file, []byte("package greet\n\nfunc Hello() string { return \""+answer+"\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The whole point of the capability, and the failure it exists to prevent: the
// result cache is keyed on program text alone, so a line evaluated before the
// edit and typed again after it renders to the same bytes. Without the reload
// the second answer is the first one, served from before the source changed —
// wrong in a way that looks exactly like right.
func TestReloadMakesTheSameLineSeeNewSource(t *testing.T) {
	dir, file := writeGreeter(t, "one")

	c, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Attach(dir); err != nil {
		t.Fatal(err)
	}

	const line = "greet.Hello()"
	if res := c.Submit(line); res.Err || !strings.Contains(res.Out, "one") {
		t.Fatalf("first evaluation = %q (err=%v), want the host's answer", res.Out, res.Err)
	}
	// Undo it, so the line typed after the reload is byte-identical to the
	// one already in the cache.
	if res := c.Submit(":undo"); res.Err {
		t.Fatalf(":undo: %s", res.Out)
	}

	rewriteGreeter(t, file, "two")

	if res := c.Submit(":reload"); res.Err {
		t.Fatalf(":reload: %s", res.Out)
	}
	res := c.Submit(line)
	if res.Err {
		t.Fatalf("after :reload: %s", res.Out)
	}
	if !strings.Contains(res.Out, "two") {
		t.Errorf("after :reload the same line answered %q, want the edited source's answer", res.Out)
	}
}

// Without the reload the stale answer is served, which is what makes the
// command worth having rather than a courtesy.
func TestWithoutReloadTheStaleAnswerIsServed(t *testing.T) {
	dir, file := writeGreeter(t, "one")

	c, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Attach(dir); err != nil {
		t.Fatal(err)
	}

	const line = "greet.Hello()"
	c.Submit(line)
	c.Submit(":undo")
	rewriteGreeter(t, file, "two")

	if res := c.Submit(line); !strings.Contains(res.Out, "one") {
		t.Skipf("the cache did not serve the pre-edit answer (%q), so this test no longer describes the failure", res.Out)
	}
}

// A pinned entry is one the user said had already happened. When the source
// underneath it changes, :refresh is what brings it back in step — and it must
// not be answered from the cache, because the program text is unchanged by
// definition and the answer is the thing that moved.
func TestRefreshRunsPinnedEntriesAgainstNewSource(t *testing.T) {
	dir, file := writeGreeter(t, "one")

	c, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Attach(dir); err != nil {
		t.Fatal(err)
	}

	if res := c.Submit("said := greet.Hello()"); res.Err {
		t.Fatalf("binding the host's answer: %s", res.Out)
	}
	if res := c.Submit("len(said)"); res.Err {
		t.Fatalf("len(said): %s", res.Out)
	}
	// Pinning 1 is refused while entry 2 reads what it bound, which is the
	// honest half of :pin. Drop the reader first.
	if res := c.Submit(":drop 2"); res.Err {
		t.Fatalf(":drop 2: %s", res.Out)
	}
	if res := c.Submit(":pin 1"); res.Err {
		t.Fatalf(":pin 1: %s", res.Out)
	}
	if res := c.Submit("1 + 1"); res.Err {
		t.Fatalf("1 + 1: %s", res.Out)
	}

	first := c.Submit(":refresh")
	if first.Err {
		t.Fatalf("first :refresh: %s", first.Out)
	}

	rewriteGreeter(t, file, "three")
	if res := c.Submit(":reload"); res.Err {
		t.Fatalf(":reload: %s", res.Out)
	}

	second := c.Submit(":refresh")
	if second.Err {
		t.Fatalf("second :refresh: %s", second.Out)
	}
	// The pinned entry ran against the new source. Its binding is what proves
	// it: the session still holds the pin, so only :refresh could have run it.
	if res := c.Submit(":unpin 1"); res.Err {
		t.Fatalf(":unpin 1: %s", res.Out)
	}
	if res := c.Submit("said"); res.Err || !strings.Contains(res.Out, "three") {
		t.Errorf("after the edit the pinned entry binds %q, want the new source's answer", res.Out)
	}
	if !strings.Contains(second.Out, "pinned entry ran again") {
		t.Errorf("second :refresh = %q, want it to say what it re-ran", second.Out)
	}
}

// The pins are what the user set, and a command that runs them once must not
// be a command that quietly unsets them.
func TestRefreshLeavesThePinsAsItFoundThem(t *testing.T) {
	dir, _ := writeGreeter(t, "one")

	c, err := NewCore()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Attach(dir); err != nil {
		t.Fatal(err)
	}

	c.Submit("said := greet.Hello()")
	c.Submit("1 + 1")
	if res := c.Submit(":pin 1"); res.Err {
		t.Fatalf(":pin 1: %s", res.Out)
	}
	before := c.Submit(":hist").Out

	if res := c.Submit(":refresh"); res.Err {
		t.Fatalf(":refresh: %s", res.Out)
	}
	if after := c.Submit(":hist").Out; after != before {
		t.Errorf("the transcript changed across a refresh:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
