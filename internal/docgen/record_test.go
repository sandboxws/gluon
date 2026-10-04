//go:build sessions

package docgen

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sandboxws/gluon/internal/pretty"
	"github.com/sandboxws/gluon/internal/repl"
	"github.com/sandboxws/gluon/internal/session"
	"github.com/sandboxws/gluon/internal/state"
)

// TestRecordSessions runs every script in site/sessions through a real session
// and writes what gluon printed beside it, as <name>.out. It is the slow, real
// half of the guides' transcripts, behind the sessions build tag so that
// neither `just test` nor `just docs` ever runs it: `just docs-sessions`
// records everything, and `just docs-sessions <name>...` the scripts named.
//
// Nothing a recording does leaves the machine. The Go proxy is off, so a
// module comes from the local cache or not at all, and a script that would
// publish something — :share — only ever shows what it would send.
func TestRecordSessions(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	var scripts []string
	for _, ext := range []string{"*.gl", "*.sh"} {
		m, err := filepath.Glob(filepath.Join(root, "site", "sessions", ext))
		if err != nil {
			t.Fatal(err)
		}
		scripts = append(scripts, m...)
	}
	sort.Strings(scripts)
	only := map[string]bool{}
	for _, n := range strings.Fields(os.Getenv("SESSIONS")) {
		only[n] = true
	}
	goenv := realGoEnv(t)
	bin := buildGluon(t, root)
	ran := 0
	for _, path := range scripts {
		name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if len(only) > 0 && !only[name] {
			continue
		}
		ran++
		t.Run(name, func(t *testing.T) { record(t, root, path, goenv, bin) })
	}
	if ran == 0 {
		t.Fatalf("no script in site/sessions is named %v", os.Getenv("SESSIONS"))
	}
}

// record runs one script and writes its transcript.
func record(t *testing.T, root, path string, goenv map[string]string, bin string) {
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	script, err := ParseScript(string(src), filepath.Ext(path) == ".sh")
	if err != nil {
		t.Fatal(err)
	}

	// gluon's own state is the test's; the Go toolchain's caches are the
	// machine's, or every line would build cold. The proxy is not: a module
	// the cache does not hold is an error here rather than a download.
	// Laid out as a reader's home is, so a path gluon prints, and one a
	// script names, reads ~/.config/gluon/config.toml.
	home := t.TempDir()
	t.Setenv("HOME", home)
	for v, dir := range map[string]string{
		"XDG_CONFIG_HOME": ".config", "XDG_DATA_HOME": ".local/share", "XDG_STATE_HOME": ".local/state",
	} {
		t.Setenv(v, filepath.Join(home, dir))
	}
	for k, v := range goenv {
		t.Setenv(k, v)
	}
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOSUMDB", "off")
	for _, v := range []string{"EDITOR", "VISUAL", "NO_COLOR", "GLUON_NO_TYPECHECK", "GLUON_MAX_ITEMS", "GLUON_MAX_DEPTH"} {
		t.Setenv(v, "")
	}
	t.Setenv("TZ", "UTC")
	t.Setenv("PATH", filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))

	// A session starts where its reader would start one: in the fixture it is
	// about, or at home.
	host, attach := "", false
	var history []string
	cwd := home
	for _, d := range script.Directives {
		word, rest, _ := strings.Cut(d, " ")
		switch word {
		case "env":
			// $HOST is the fixture's copy, so a variable can name a file in it.
			k, v, _ := strings.Cut(rest, "=")
			t.Setenv(k, strings.ReplaceAll(v, "$HOST", host))
		case "host":
			host = copyFixture(t, root, rest)
			cwd = host
		case "attach":
			attach = true
		case "serve":
			if host == "" {
				t.Fatal("the serve directive needs a host directive before it")
			}
			serve(t, host)
		case "history":
			history = append(history, rest)
		default:
			t.Fatalf("unknown directive %%%s", d)
		}
	}
	t.Chdir(cwd)
	if len(history) > 0 {
		file := state.File("history")
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(strings.Join(history, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var c *repl.Core
	if !allShell(script) {
		c, err = repl.NewCore()
		if err != nil {
			t.Skip("no evaluator:", err)
		}
		defer c.Close()
		// What a terminal draws — tables, forms, the plugins' renderers — and
		// not the one-line form a pipe gets, because a guide's session is read
		// as what the prompt showed.
		c.RenderAsTerminal(pretty.PlainStyles(), c.Config().ValueOptions())
		if attach {
			if host == "" {
				t.Fatal("the attach directive needs a host directive before it")
			}
			if _, err := c.Attach(host); err != nil {
				t.Fatalf("attaching to the fixture: %v", err)
			}
		}
	}

	var entries []Entry
	for _, st := range script.Steps {
		if st.Shell {
			out, failed := shell(t, cwd, st.Src)
			if st.Hidden {
				if failed {
					t.Fatalf("setup command %q failed:\n%s", st.Src, out)
				}
				continue
			}
			entries = append(entries, Entry{In: st.Src, Out: hide(strings.TrimRight(out, "\n"), host), Shell: true, Clip: st.Clip})
			continue
		}

		var res repl.Result
		var echoed []Entry
		if st.Paste {
			// A paste is echoed a construct at a time, as the prompt echoes
			// it, and evaluated as one batch.
			var items, pending []string
			for _, l := range strings.Split(st.Src, "\n") {
				pending = append(pending, l)
				if src := strings.Join(pending, "\n"); !session.IsIncomplete(src) {
					items = append(items, src)
					echoed = append(echoed, Entry{In: src})
					pending = nil
				}
			}
			res = c.SubmitBatch(items)
		} else {
			res = c.Submit(st.Src)
			echoed = []Entry{{In: st.Src}}
		}
		carry(c, res)

		last := &echoed[len(echoed)-1]
		if res.Edit != "" {
			// The line handed the terminal to an editor. What the person would
			// have written is the script's; what happens when the editor
			// closes is the driver's, and is done here the way the TUI does it.
			if !st.HasEditor {
				t.Fatalf("%q opens an editor: give it an %%editor block saying what is written there", st.Src)
			}
			writeEditor(t, res.Edit, st.Editor)
			if res.EditThen != "" {
				res = c.Submit(res.EditThen)
			} else {
				res = c.Reload(res.Edit)
			}
			carry(c, res)
			last.Editor = st.Editor
		} else if st.HasEditor {
			t.Fatalf("%q has an %%editor block but opened no editor: %s", st.Src, res.Out)
		}

		if st.Hidden {
			if res.Err {
				t.Fatalf("setup line %q failed: %s", st.Src, res.Out)
			}
			continue
		}
		last.Out, last.Err, last.Lang, last.Clip = hide(res.Out, host), res.Err, res.Lang, st.Clip
		last.View = res.Modal != nil || (res.Theme != nil && res.Theme.Apply == "")
		entries = append(entries, echoed...)
	}
	dst := strings.TrimSuffix(path, filepath.Ext(path)) + ".out"
	if err := os.WriteFile(dst, []byte(FormatTranscript(entries)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// carry does what the TUI does with a result before the next line: a command
// that changed the shape values are drawn in changes it for what follows.
func carry(c *repl.Core, res repl.Result) {
	if res.Values != nil {
		c.RenderAsTerminal(pretty.PlainStyles(), res.Values.Options)
	}
}

// allShell reports whether a script never reaches the gluon prompt, so no
// session need be started for it.
func allShell(s Script) bool {
	for _, st := range s.Steps {
		if !st.Shell {
			return false
		}
	}
	return true
}

// writeEditor writes what the script says was typed into the file an editor
// was opened on. A buffer's file is scaffolding around a region the buffer is
// read back from, so the text goes into that region and nowhere else; any
// other file is the text.
func writeEditor(t *testing.T, path, text string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")
	code, end := -1, -1
	for i, l := range lines {
		l = strings.TrimSpace(l)
		switch {
		case code < 0 && strings.HasPrefix(l, "//gluon:code"):
			code = i
		case code >= 0 && end < 0 && strings.HasPrefix(l, "//gluon:end"):
			end = i
		}
	}
	out := text + "\n"
	if code >= 0 && end > code {
		kept := append(append([]string{}, lines[:code+1]...), strings.Split(text, "\n")...)
		out = strings.Join(append(kept, lines[end:]...), "\n")
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
}

// shell runs one command the way a person at a shell would, and returns what
// it printed to either stream, in the order it printed it.
func shell(t *testing.T, dir, command string) (string, bool) {
	t.Helper()
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if err != nil {
		if _, exited := err.(*exec.ExitError); !exited {
			t.Fatalf("%s: %v", command, err)
		}
	}
	return out.String(), err != nil
}

// buildGluon builds the public gluon this checkout makes, at the release the
// site names, for the scripts that run it from a shell.
func buildGluon(t *testing.T, root string) string {
	t.Helper()
	m, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "bin", "gluon")
	run(t, root, "go", "build", "-trimpath", "-ldflags", "-X main.version="+m.Site.Release, "-o", bin, "./cmd/gluon")
	return bin
}

// hide writes the machine's own paths the way a reader's would read: the
// fixture as ~/src/shop, gluon's state directories under ~, the temp directory
// as $TMPDIR. Nothing else in the output changes.
func hide(out, host string) string {
	// No colour reaches a recording — the styles are plain and the test has
	// no terminal — but a stray escape would be a byte the page prints.
	out = ansi.ReplaceAllString(out, "")
	var ps [][2]string
	add := func(from, to string) {
		if from == "" {
			return
		}
		ps = append(ps, [2]string{from, to})
		if real, err := filepath.EvalSymlinks(from); err == nil && real != from {
			ps = append(ps, [2]string{real, to})
		}
	}
	add(host, "~/src/shop")
	add(os.Getenv("XDG_CONFIG_HOME"), "~/.config")
	add(os.Getenv("XDG_DATA_HOME"), "~/.local/share")
	add(os.Getenv("XDG_STATE_HOME"), "~/.local/state")
	add(os.Getenv("HOME"), "~")
	add(filepath.Clean(os.TempDir()), "$TMPDIR")
	add(realHome, "~")
	return Rewrite(out, ps)
}

// ansi is an escape sequence that sets a colour or a style.
var ansi = regexp.MustCompile("\x1b\\[[0-9;:]*m")

// realHome is the home directory before the test moved HOME.
var realHome, _ = os.UserHomeDir()

// realGoEnv is the toolchain settings to keep once HOME has moved: where the
// module cache and the build cache are.
func realGoEnv(t *testing.T) map[string]string {
	t.Helper()
	keys := []string{"GOPATH", "GOMODCACHE", "GOCACHE", "GOFLAGS", "GONOSUMDB", "GOPRIVATE", "GOTOOLCHAIN"}
	out, err := exec.Command("go", append([]string{"env", "-json"}, keys...)...).Output()
	if err != nil {
		t.Skip("no go toolchain:", err)
	}
	env := map[string]string{}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatal(err)
	}
	return env
}

// copyFixture copies testdata/<name> to a temp directory and marks it as the
// top of a repository, so gluon's project search stops there as it would in a
// real checkout. A fixture with a cmd/seed writes its own data first.
func copyFixture(t *testing.T, root, name string) string {
	t.Helper()
	src := filepath.Join(root, "testdata", name)
	dst := filepath.Join(t.TempDir(), name)
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dst, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "cmd", "seed", "main.go")); err == nil {
		run(t, dst, "go", "run", "-buildvcs=false", "./cmd/seed")
	}
	return dst
}

// serve builds and starts the fixture's servers for the rest of the test.
func serve(t *testing.T, host string) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "shopd")
	run(t, host, "go", "build", "-buildvcs=false", "-o", bin, "./cmd/shopd")
	cmd := exec.Command(bin)
	cmd.Dir = host
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	for _, addr := range []string{"127.0.0.1:8765", "127.0.0.1:50051"} {
		deadline := time.Now().Add(20 * time.Second)
		for {
			conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
			if err == nil {
				conn.Close()
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s never answered", addr)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out.String())
	}
}
