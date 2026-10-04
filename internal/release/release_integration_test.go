//go:build integration

package release

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/render"
	"github.com/sandboxws/gluon/internal/session"
)

// The curated notes claim two things a reader will act on: that a snippet works
// at the directive it names, and that it does not below it. Both are checked
// here by really compiling, because the alternative is a recollection.
//
// A snippet is assembled through gluon's own session and renderer rather than
// pasted into a main, so what is compiled is what the REPL would have built.

func TestEverySnippetBuildsAtTheDirectiveItClaims(t *testing.T) {
	installed := installedMinor(t)
	for _, v := range CuratedVersions() {
		notes, _ := notesFor(v)
		for _, n := range notes {
			t.Run(v+"/"+n.Name, func(t *testing.T) {
				if Compare(n.Needs, installed) > 0 {
					t.Skipf("no go toolchain new enough: needs %s, have %s", n.Needs, installed)
				}
				if out, err := buildAt(t, n.Snippet, n.Needs); err != nil {
					t.Errorf("%s does not build at go %s:\n%s", n.Name, n.Needs, out)
				}
			})
		}
	}
}

func TestAGatedSnippetIsRejectedBelowItsDirective(t *testing.T) {
	installed := installedMinor(t)
	for _, v := range CuratedVersions() {
		notes, _ := notesFor(v)
		for _, n := range notes {
			if !n.Gated() {
				continue
			}
			t.Run(v+"/"+n.Name, func(t *testing.T) {
				if Compare(n.Needs, installed) > 0 {
					t.Skipf("no go toolchain new enough: needs %s, have %s", n.Needs, installed)
				}
				below := minorBelow(t, n.Needs)
				if out, err := buildAt(t, n.Snippet, below); err == nil {
					t.Errorf("%s claims below=%q but builds at go %s:\n%s",
						n.Name, n.Below, below, out)
				}
			})
		}
	}
}

// A note that says "behaves differently" is claiming two things: that the
// snippet still builds below its directive, and that it means something else
// there. Both are checked, the second by running it and requiring the output to
// differ — which is the whole content of the claim, and the reason it is not
// enough to compile it twice.
func TestAnUngatedSnippetMeansSomethingElseBelowItsDirective(t *testing.T) {
	installed := installedMinor(t)
	for _, v := range CuratedVersions() {
		notes, _ := notesFor(v)
		for _, n := range notes {
			if n.Gated() || n.ToolchainOnly() {
				continue
			}
			t.Run(v+"/"+n.Name, func(t *testing.T) {
				if Compare(n.Needs, installed) > 0 {
					t.Skipf("no go toolchain new enough: needs %s, have %s", n.Needs, installed)
				}
				below := minorBelow(t, n.Needs)
				if Compare(below, runtimeFloor) < 0 {
					t.Skipf("gluon's own runtime does not build below go %s", runtimeFloor)
				}
				at, err := runAt(t, n.Snippet, n.Needs)
				if err != nil {
					t.Fatalf("running at go %s: %v\n%s", n.Needs, err, at)
				}
				under, err := runAt(t, n.Snippet, below)
				if err != nil {
					t.Fatalf("%s claims below=%q but is rejected at go %s: %v\n%s",
						n.Name, n.Below, below, err, under)
				}
				if at == under {
					t.Errorf("%s claims below=%q, but go %s and go %s both print:\n%s",
						n.Name, n.Below, n.Needs, below, at)
				}
			})
		}
	}
}

// A "toolchain only" note claims no go directive gates it. That half is
// checkable and is checked: the snippet must build under a directive well below
// the release it is filed under. What cannot be checked here is which toolchain
// first accepted it — one machine has one toolchain — so the note's own text
// says where that attribution comes from.
func TestAToolchainOnlyNoteIsNotGatedByAnyDirective(t *testing.T) {
	installed := installedMinor(t)
	for _, v := range CuratedVersions() {
		notes, _ := notesFor(v)
		for _, n := range notes {
			if !n.ToolchainOnly() {
				continue
			}
			t.Run(v+"/"+n.Name, func(t *testing.T) {
				if Compare(n.Needs, installed) > 0 {
					t.Skipf("no go toolchain new enough: needs %s, have %s", n.Needs, installed)
				}
				if out, err := buildAt(t, n.Snippet, n.Needs); err != nil {
					t.Fatalf("%s does not build at go %s:\n%s", n.Name, n.Needs, out)
				}
				// Far below, not one minor below: the claim is that no
				// directive gates it, and one step down could pass by accident.
				const low = "1.21"
				if out, err := buildAt(t, n.Snippet, low); err != nil {
					t.Errorf("%s claims below=%q, but go %s rejects it — it is "+
						"directive-gated after all:\n%s", n.Name, n.Below, low, out)
				}
			})
		}
	}
}

// runtimeFloor is the lowest go directive gluon's own injected runtime
// compiles under. render.RuntimeFiles() calls clear, which the compiler gates
// at go1.21, and unsafe.StringData, gated at go1.20. It is recorded here
// because it is a real limit on what a session can be asked to run, not an
// artefact of this test.
const runtimeFloor = "1.21"

// runAt builds with gluon's real runtime and runs the result, returning what
// the child wrote. The behaviour test needs the real printer: a stub that
// swallows its arguments would make every release print the same nothing.
func runAt(t *testing.T, snippet, directive string) (string, error) {
	t.Helper()
	dir, err := moduleAt(t, snippet, directive, true)
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "out")
	if out, err := build(dir, bin); err != nil {
		return string(out), err
	}
	out, err := exec.Command(bin).CombinedOutput()
	return string(out), err
}

// buildAt renders the snippet the way the REPL would and compiles it with a
// go.mod pinned to the given directive.
func buildAt(t *testing.T, snippet, directive string) (string, error) {
	t.Helper()
	dir, err := moduleAt(t, snippet, directive, false)
	if err != nil {
		return "", err
	}
	out, err := build(dir, filepath.Join(dir, "out"))
	return string(out), err
}

// moduleAt writes a temp module holding the rendered snippet at one directive.
//
// realRuntime picks between gluon's injected printer and a stub. The stub is
// the default because render.RuntimeFiles() does not compile below go 1.21, and
// a note about Go 1.18 must be failed by Go 1.18 rather than by gluon's floor.
// The user's own code is assembled by gluon's renderer either way, which is the
// half that can differ from a hand-written program.
func moduleAt(t *testing.T, snippet, directive string, realRuntime bool) (string, error) {
	t.Helper()
	src, err := program(snippet)
	if err != nil {
		return "", err
	}
	dir := t.TempDir()
	mod := fmt.Sprintf("module gluon.local/notecheck\n\ngo %s\n", directive)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"stubrt.go": stubRuntime}
	if realRuntime {
		files = render.RuntimeFiles()
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir, nil
}

// build compiles the module at dir. Invariant 1: never a bare go build, and
// never with a repo cwd.
func build(dir, out string) ([]byte, error) {
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=", "GOWORK=off", "GOPROXY=off", "GOTOOLCHAIN=local")
	return cmd.CombinedOutput()
}

// program feeds the snippet through session.Classify the way Core.submitAll
// does, then renders it with gluon's own renderer. Notes import nothing, so
// there are no import specs to resolve.
func program(snippet string) (string, error) {
	s := &session.Session{}
	var buf []string
	for _, line := range strings.Split(snippet, "\n") {
		buf = append(buf, line)
		joined := strings.Join(buf, "\n")
		if strings.TrimSpace(joined) == "" {
			buf = nil
			continue
		}
		if session.IsIncomplete(joined) {
			continue
		}
		buf = nil
		e, err := session.Classify(joined)
		if err != nil {
			return "", fmt.Errorf("classify %q: %w", joined, err)
		}
		// What the evaluator learns from a build, and predictNoValue seeds for
		// the builtins: a call returning nothing is emitted bare rather than
		// wrapped in a print. Without it `clear(m)` renders as a value.
		if callee, ok := render.Callee(e.Src); ok && noValue[callee] {
			e.NoValue = true
		}
		s.Append(e)
	}
	if len(buf) > 0 {
		return "", fmt.Errorf("snippet ends mid-construct: %q", strings.Join(buf, "\n"))
	}
	return render.Main(s, nil)
}

// noValue is the builtin half of the evaluator's seedNoValue.
var noValue = map[string]bool{
	"clear": true, "close": true, "delete": true,
	"panic": true, "print": true, "println": true,
}

// stubRuntime satisfies the calls gluon's renderer emits, and nothing more.
const stubRuntime = `package main

func __gluonMute()           {}
func __gluonUnmute()         {}
func __gluonDrain()          {}
func __gluonPrint(vs ...any) { _ = vs }
`

func installedMinor(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOVERSION").Output()
	if err != nil {
		t.Skip("no go toolchain on PATH")
	}
	v := strings.TrimSpace(string(out))
	v = strings.TrimPrefix(v, "go")
	major, rest, _ := strings.Cut(v, ".")
	minor, _, _ := strings.Cut(rest, ".")
	return major + "." + minor
}

func minorBelow(t *testing.T, v string) string {
	t.Helper()
	major, minor, ok := split(v)
	if !ok || minor == 0 {
		t.Fatalf("cannot step below %q", v)
	}
	return strconv.Itoa(major) + "." + strconv.Itoa(minor-1)
}
