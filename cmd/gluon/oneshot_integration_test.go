//go:build integration

package main

import (
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// captureOneShot runs runOneShot with stdout captured. stderr is left alone —
// errors land there and the tests that want them assert on the exit code.
func captureOneShot(t *testing.T, lines []string, asJSON bool) (string, int) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	code := runOneShot(lines, "", asJSON)
	os.Stdout = saved
	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out), code
}

// TestOneShotPlainShapeIsPinned pins the exact bytes of the README's own
// invocation. This output is a compatibility surface: pipes and scripts read
// it, and it predates every internal rearrangement of how -e evaluates.
func TestOneShotPlainShapeIsPinned(t *testing.T) {
	out, code := captureOneShot(t, []string{`strings.ToUpper("hi")`}, false)
	if want := "(string) \"HI\"  len=2\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
}

// TestOneShotRunsMetaCommands pins the fix for a day-one asymmetry:
// `echo ':t x' | gluon` always worked, while `gluon -e ':t x'` was rejected
// by Classify before Core ever saw it.
func TestOneShotRunsMetaCommands(t *testing.T) {
	out, code := captureOneShot(t, []string{":t http.Handler"}, false)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (output %q)", code, out)
	}
	if !strings.Contains(out, "http.Handler") {
		t.Errorf("output = %q, want the type named", out)
	}
}

// TestOneShotBatchPrintsEveryEntry pins the batch semantics the launch
// freezes: every printed expression prints, one line per entry, while a
// single entry returning a tuple keeps its one comma-joined line.
func TestOneShotBatchPrintsEveryEntry(t *testing.T) {
	out, code := captureOneShot(t, []string{"x := []int{3, 1, 2}", "len(x)", "cap(x)"}, false)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (output %q)", code, out)
	}
	if want := "(int) 3\n(int) 3\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}

	out, code = captureOneShot(t, []string{"f := func() (int, int) { return 1, 2 }", "f()"}, false)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (output %q)", code, out)
	}
	if want := "(int) 1, (int) 2\n"; out != want {
		t.Errorf("tuple output = %q, want %q", out, want)
	}
}

// TestOneShotMetaJSONEnvelope pins the metaJSON shape: ok, command, text.
// Fields are add-only from here on, the same contract as every envelope.
func TestOneShotMetaJSONEnvelope(t *testing.T) {
	out, code := captureOneShot(t, []string{":t strings.Builder"}, true)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (output %q)", code, out)
	}
	for _, want := range []string{`"ok": true`, `"command": ":t"`, `"text": "strings.Builder`} {
		if !strings.Contains(out, want) {
			t.Errorf("envelope %q missing %q", out, want)
		}
	}
}

// TestOneShotMixedCodeAndMeta pins the flush order: code before a meta lands
// before the meta runs, so the meta answers against the session those lines
// built.
func TestOneShotMixedCodeAndMeta(t *testing.T) {
	out, code := captureOneShot(t, []string{"type P struct{ X, Y int }", ":layout P"}, false)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (output %q)", code, out)
	}
	if !strings.Contains(out, "16 bytes") {
		t.Errorf("output = %q, want the layout of the type the batch declared", out)
	}
}

// TestHelpThroughE: a command's page is an ordinary meta result, so a script
// asking for it gets exit 0, and the -json envelope keeps its shape.
func TestHelpThroughE(t *testing.T) {
	out, code := captureOneShot(t, []string{":http --help"}, false)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (output %q)", code, out)
	}
	if !strings.Contains(out, "issue a request") {
		t.Errorf(":http --help printed %q, want its page", out)
	}

	out, code = captureOneShot(t, []string{":http --help"}, true)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (output %q)", code, out)
	}
	for _, want := range []string{`"ok": true`, `"command": ":http"`, `"text": "  :http`} {
		if !strings.Contains(out, want) {
			t.Errorf("envelope %q missing %q", out, want)
		}
	}
}
