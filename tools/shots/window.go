package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// A Window is one Ghostty window this tool opened, and only such a window. It
// is ours because our own `new window` returned its id and that id was not
// among the windows before we asked; everything else here refuses a window
// that is not.
type Window struct {
	ID       string // Ghostty's window id
	Terminal string // its one terminal's id
	TTY      string
	Title    string // the nonce the wrapper set with OSC 2
	CGID     int    // the window server's number, for screencapture
	Ctl      string // the control directory the wrapper and the driver share
	n        int    // the runs started in it, so the current one's files are n.*
	baseline map[string]bool
}

// windows is every Ghostty window's id and tab count.
func windows() (map[string]int, error) {
	out, err := osa("windows")
	if err != nil {
		return nil, err
	}
	ws := map[string]int{}
	for _, l := range strings.Split(out, "\n") {
		id, n, ok := strings.Cut(strings.TrimSpace(l), "\t")
		if !ok {
			continue
		}
		c, _ := strconv.Atoi(n)
		ws[id] = c
	}
	return ws, nil
}

// nonce is a title nothing else on the screen has.
func nonce() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "gluon-shot-" + hex.EncodeToString(b)
}

// Open makes one new window running the wrapper, and checks it is ours and
// has one tab. A surface that landed as a tab of somebody else's window is
// aborted by making its own process exit — nothing is closed, and no other
// window is touched. The window runs nothing else until Run.
func Open(ctl, wd string, fontSize float64, env []string) (*Window, error) {
	before, err := windows()
	if err != nil {
		return nil, err
	}
	w := &Window{Title: nonce(), Ctl: ctl, baseline: map[string]bool{}}
	for id := range before {
		w.baseline[id] = true
	}
	if err := os.MkdirAll(ctl, 0o755); err != nil {
		return nil, err
	}
	wrapper := filepath.Join(ctl, "run.sh")
	if err := os.WriteFile(wrapper, []byte(wrapperScript), 0o755); err != nil {
		return nil, err
	}
	env = append(env,
		"SHOTS_NONCE="+w.Title,
		"SHOTS_DRIVER="+strconv.Itoa(os.Getpid()),
	)
	command := launchCommand(wrapper, ctl)
	args := append([]string{command, wd, strconv.FormatFloat(fontSize, 'f', -1, 64)}, env...)
	out, err := osa("open", args...)
	f := strings.Split(out, "\t")
	if err != nil || len(f) != 4 {
		// A window may exist that nothing here can address yet. Find it —
		// new since the baseline, and running nothing or our wrapper — and
		// end it, rather than leave it on somebody's screen.
		w.abort()
		w.strays()
		if err == nil {
			err = fmt.Errorf("open answered %q", out)
		}
		return nil, err
	}
	w.ID, w.Terminal, w.TTY = f[0], f[2], f[3]
	tabs, _ := strconv.Atoi(f[1])
	switch {
	case w.baseline[w.ID]:
		w.abort()
		return nil, fmt.Errorf("the new surface landed in an existing window (%s) — aborted it by ending its process; nothing else was touched", w.ID)
	case tabs != 1:
		w.abort()
		return nil, fmt.Errorf("the new window has %d tabs, not one — aborted", tabs)
	}
	if err := w.waitFile("ready", 20*time.Second); err != nil {
		w.abort()
		return nil, fmt.Errorf("the wrapper never started: %v", err)
	}
	if got := strings.TrimSpace(read(filepath.Join(ctl, "tty"))); got != w.TTY {
		w.abort()
		return nil, fmt.Errorf("the wrapper runs on %s but the window's terminal is %s — aborted", got, w.TTY)
	}
	// The title is how System Events finds the window, and Ghostty applies
	// one from its terminal's output in its own time: the wrapper having
	// printed it is not the window having it.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, _, _, _, err := w.Frame(); err == nil {
			break
		} else if time.Now().After(deadline) {
			w.retire()
			return nil, fmt.Errorf("the window never took its title: %v", err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	return w, nil
}

// retire ends a window that was opened but will not be used: its wrapper is
// told to stop, and once its process has gone, a window Ghostty keeps open
// anyway — it can, with nothing in it — is closed by its id. Closing a
// window with no process asks nothing. One still running is left alone.
func (w *Window) retire() {
	w.abort()
	for i := 0; i < 50; i++ {
		if !w.exists() {
			return
		}
		out, err := osa("process", w.ID)
		if pid, _, _ := strings.Cut(out, "\t"); err == nil && strings.TrimSpace(pid) == "0" {
			if _, err := osa("close", w.ID); err == nil {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	fmt.Fprintf(os.Stderr, "shots: window %s is still open and was not closed — it runs nothing of ours; close it by hand\n", w.ID)
}

// strays closes the windows that appeared since the baseline and run nothing:
// a surface whose command could not start is held open by Ghostty to show why,
// with no process, so closing it asks nothing. A new window that is running
// something is left alone and reported; it may not be ours.
func (w *Window) strays() {
	time.Sleep(1500 * time.Millisecond) // the wrapper, if it started, sees abort
	now, err := windows()
	if err != nil {
		return
	}
	for id := range now {
		if w.baseline[id] {
			continue
		}
		out, err := osa("process", id)
		pid, _, _ := strings.Cut(out, "\t")
		if err == nil && strings.TrimSpace(pid) == "0" {
			if _, err := osa("close", id); err == nil {
				fmt.Fprintf(os.Stderr, "shots: closed window %s, which this run opened and whose command never ran\n", id)
				continue
			}
		}
		fmt.Fprintf(os.Stderr, "shots: window %s appeared during this run and is still running something — left alone\n", id)
	}
}

// launchCommand is the command a new window runs: the wrapper, under /bin/sh.
// Ghostty adds its shell integration to a window's command, and one configured
// as shell-integration = nushell adds nushell's arguments whatever the command
// is — /bin/sh rejects them — so then the wrapper is exec'd from nu -c, which
// runs its command and ignores the rest.
func launchCommand(wrapper, ctl string) string {
	inner := "/bin/sh " + shellQuote(wrapper) + " " + shellQuote(ctl)
	if nu := ghosttyNushell(); nu != "" {
		return nu + " -n -c " + doubleQuote("exec "+inner)
	}
	return inner
}

// ghosttyNushell is the nu binary to launch through when Ghostty's config
// forces nushell integration, or "".
func ghosttyNushell() string {
	out, err := exec.Command("/Applications/Ghostty.app/Contents/MacOS/ghostty", "+show-config").Output()
	if err != nil {
		return ""
	}
	integration, command := "", ""
	for _, l := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "shell-integration":
			integration = strings.TrimSpace(v)
		case "command":
			command = strings.TrimSpace(v)
		}
	}
	if integration != "nushell" && !(integration == "" || integration == "detect") {
		return ""
	}
	if integration == "" || integration == "detect" {
		return "" // detected from the command, which is /bin/sh
	}
	if strings.HasSuffix(command, "/nu") {
		return command
	}
	if p, err := exec.LookPath("nu"); err == nil {
		return p
	}
	return ""
}

// doubleQuote quotes a word with double quotes, for Ghostty's command line.
func doubleQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// abort ends the wrapper before it starts anything, so its window closes by
// itself.
func (w *Window) abort() { _ = os.WriteFile(filepath.Join(w.Ctl, "abort"), nil, 0o644) }

// A Program is one run of something in the window: where it starts, its
// environment, its command line, and how the window is painted around it.
type Program struct {
	Dir   string
	Env   []string // KEY=value, exported over the wrapper's own
	Unset []string // names removed from it
	Argv  []string
	Palette
	Title string
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Run starts the next program. The wrapper sources its file in a subshell —
// the directory, the environment, the command — paints the window, and runs
// it under script(1). The window must already be the size the program wants:
// it starts at that size, so what it draws first is drawn for it.
func (w *Window) Run(p Program) error {
	if w.running() {
		return fmt.Errorf("run %d has not exited", w.n)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "cd %s || exit 97\n", shellQuote(p.Dir))
	for _, name := range p.Unset {
		if !envName.MatchString(name) {
			return fmt.Errorf("%q is not a variable name", name)
		}
		fmt.Fprintf(&b, "unset %s\n", name)
	}
	for _, kv := range p.Env {
		k, v, _ := strings.Cut(kv, "=")
		if !envName.MatchString(k) {
			return fmt.Errorf("%q is not a variable name", k)
		}
		fmt.Fprintf(&b, "export %s=%s\n", k, shellQuote(v))
	}
	title := p.Title
	if title == "" {
		title = "gluon"
	}
	fmt.Fprintf(&b, "SHOTS_FG=%s SHOTS_BG=%s SHOTS_CURSOR=%s SHOTS_TITLE=%s\n",
		shellQuote(p.FG), shellQuote(p.BG), shellQuote(p.Cursor), shellQuote(title))
	b.WriteString("set --")
	for _, a := range p.Argv {
		b.WriteString(" " + shellQuote(a))
	}
	b.WriteString("\n")
	n := w.n + 1
	if err := os.WriteFile(w.file(n, "env"), []byte(b.String()), 0o644); err != nil {
		return err
	}
	w.n = n
	return os.WriteFile(w.file(n, "go"), nil, 0o644)
}

// file is one of run n's files in the control directory.
func (w *Window) file(n int, ext string) string {
	return filepath.Join(w.Ctl, strconv.Itoa(n)+"."+ext)
}

// running says whether the current run has started and not exited.
func (w *Window) running() bool {
	if w.n == 0 {
		return false
	}
	_, err := os.Stat(w.file(w.n, "exited"))
	return err != nil
}

// Exited waits for the current run to end.
func (w *Window) Exited(d time.Duration) error {
	return w.waitFile(strconv.Itoa(w.n)+".exited", d)
}

// Quit ends the current run by sending keys, one at a time, until it exits. A
// key is never sent after the run has gone: it would wait in the terminal and
// be the next run's first input.
func (w *Window) Quit(keys []string) error {
	for _, k := range keys {
		if !w.running() {
			return nil
		}
		if err := w.Act(k); err != nil {
			return err
		}
		if w.Exited(2*time.Second) == nil {
			return nil
		}
	}
	if !w.running() {
		return nil
	}
	return fmt.Errorf("run %d did not quit", w.n)
}

// waitFile waits for the wrapper to create a file in the control directory.
func (w *Window) waitFile(name string, d time.Duration) error {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(w.Ctl, name)); err == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("%s did not appear in %v", name, d)
}

// Geom is the terminal's size in cells, as the wrapper last read it.
func (w *Window) Geom() (rows, cols int) {
	f := strings.Fields(read(filepath.Join(w.Ctl, "geom")))
	if len(f) == 2 {
		rows, _ = strconv.Atoi(f[0])
		cols, _ = strconv.Atoi(f[1])
	}
	return rows, cols
}

// Frame is the window's position and size in points.
func (w *Window) Frame() (x, y, width, height int, err error) {
	out, err := osa("frame", w.Title)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	f := strings.Split(out, "\t")
	if len(f) != 4 {
		return 0, 0, 0, 0, fmt.Errorf("frame answered %q", out)
	}
	n := make([]int, 4)
	for i := range f {
		n[i], _ = strconv.Atoi(strings.TrimSpace(f[i]))
	}
	return n[0], n[1], n[2], n[3], nil
}

// Resize sets the window's frame, by its title. Between runs the wrapper puts
// the nonce back as the title, and Ghostty takes it in its own time, so a
// window not found is looked for again, briefly.
func (w *Window) Resize(x, y, width, height int) error {
	var err error
	for i := 0; i < 20; i++ {
		if _, err = osa("resize", w.Title, strconv.Itoa(x), strconv.Itoa(y), strconv.Itoa(width), strconv.Itoa(height)); err == nil {
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return err
}

// Act sends bytes to the window's terminal through a Ghostty action.
func (w *Window) Act(action string) error {
	if err := checkAction(action); err != nil {
		return err
	}
	_, err := osa("act", w.Terminal, action)
	return err
}

// Paste sends text the way a paste arrives.
func (w *Window) Paste(s string) error {
	_, err := osa("input", w.Terminal, s)
	return err
}

// Type sends text as typed, escaped for Ghostty's text action.
func (w *Window) Type(s string) error { return w.Act("text:" + escapeText(s)) }

// escapeText writes s as the Zig-style string Ghostty's text action reads.
func escapeText(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Close ends the window the way a person would: the program quits, the
// wrapper is told it is done and exits, and Ghostty closes the window of a
// process that has exited without asking anything. Only if the window
// outlives its process is it closed by id — and only a window this tool
// opened, whose process is gone, so no confirmation is waiting to be clicked.
// Nothing is ever clicked.
func (w *Window) Close(quit []string) error {
	pid, _ := strconv.Atoi(strings.TrimSpace(read(filepath.Join(w.Ctl, "pid"))))
	ours := pid > 0 && ttyOf(pid) == w.TTY
	if w.running() {
		if w.Quit(quit) != nil && ours {
			// The program did not quit. End it — the wrapper's child, while
			// the wrapper is still on this window's terminal.
			_ = exec.Command("pkill", "-TERM", "-P", strconv.Itoa(pid), "script").Run()
			_ = w.Exited(5 * time.Second)
		}
	}
	_ = os.WriteFile(filepath.Join(w.Ctl, "done"), nil, 0o644)
	for i := 0; i < 60; i++ {
		if !w.exists() {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	if ours && ttyOf(pid) == w.TTY {
		_ = syscall.Kill(pid, syscall.SIGTERM)
		time.Sleep(2 * time.Second)
		if ttyOf(pid) == w.TTY {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			time.Sleep(time.Second)
		}
	}
	if !w.exists() {
		return nil
	}
	if pid > 0 && processAlive(pid) {
		return errors.New("the window's process is still running and was not closed — close it by hand; nothing else was touched")
	}
	if _, err := osa("close", w.ID); err != nil {
		return err
	}
	if w.exists() {
		return errors.New("the window is still open after its process exited — close it by hand")
	}
	return nil
}

// exists says whether the window is still there.
func (w *Window) exists() bool {
	out, err := osa("exists", w.ID)
	return err == nil && out == "true"
}

// ttyOf is the controlling terminal of a process, as ps names it.
func ttyOf(pid int) string {
	out, err := exec.Command("ps", "-o", "tty=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	t := strings.TrimSpace(string(out))
	if t == "" || t == "??" {
		return ""
	}
	return "/dev/" + t
}

func processAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// Unchanged checks that Ghostty has exactly the windows it had before this
// window was opened.
func (w *Window) Unchanged() error {
	now, err := windows()
	if err != nil {
		return err
	}
	for id := range now {
		if !w.baseline[id] {
			return fmt.Errorf("window %s is open and was not before", id)
		}
	}
	for id := range w.baseline {
		if _, ok := now[id]; !ok {
			return fmt.Errorf("window %s was open before and is not now", id)
		}
	}
	return nil
}

func read(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

// shellQuote quotes a word for /bin/sh.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// wrapperScript is the window's program. It names the window with the nonce,
// says where it is running, and then runs what the driver hands it, one run at
// a time: run n is $ctl/n.env, sourced in a subshell — the directory, the
// environment, the command — and started when $ctl/n.go appears, under
// script(1), whose log is how the driver sees what was printed. Between runs
// the window is idle and titled with the nonce, which is how System Events
// finds it to resize. It exits when the driver says done — and when the driver
// dies: the guard ends the run, and the wrapper's exit closes the window, as
// any exit does.
const wrapperScript = `#!/bin/sh
ctl=$1
printf '\033]2;%s\007' "$SHOTS_NONCE"
echo $$ > "$ctl/pid"
tty > "$ctl/tty"
stty size > "$ctl/geom"
: > "$ctl/ready"
( while kill -0 "$SHOTS_DRIVER" 2>/dev/null; do
    [ -e "$ctl/done" ] && exit 0
    sleep 1
  done
  : > "$ctl/abort"
  pkill -TERM -P $$ script ) &
n=1
idle=0
while :; do
  while [ ! -e "$ctl/$n.go" ]; do
    if [ -e "$ctl/done" ] || [ -e "$ctl/abort" ]; then exit 0; fi
    idle=$((idle+1)); [ $idle -gt 36000 ] && exit 0
    stty size > "$ctl/geom.tmp" && mv "$ctl/geom.tmp" "$ctl/geom"
    sleep 0.05
  done
  idle=0
  stty size > "$ctl/geom"
  ( . "$ctl/$n.env"
    printf '\033]10;%s\007\033]11;%s\007\033]12;%s\007' "$SHOTS_FG" "$SHOTS_BG" "$SHOTS_CURSOR"
    printf '\033]2;%s\007' "$SHOTS_TITLE"
    printf '\033[2J\033[3J\033[H'
    exec script -q -F "$ctl/$n.log" "$@" )
  echo $? > "$ctl/$n.status"
  printf '\033]2;%s\007' "$SHOTS_NONCE"
  : > "$ctl/$n.exited"
  n=$((n+1))
done
`
