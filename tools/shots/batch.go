package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// quitKeys end a gluon run from wherever it is: esc closes a view or leaves
// vim's insert mode, ctrl-c abandons a line — or closes a view, or quits from
// an empty prompt — and ctrl-d quits from an empty one. Window.Quit stops at
// the first that works, so none is left waiting for the next run.
var quitKeys = []string{`text:\x1b`, `text:\x03`, `text:\x03`, `text:\x04`}

// prompt is gluon's prompt in either input mode.
var prompt = regexp.MustCompile(`gluon(\[[in]\])?> `)

// settle is how long the window must print nothing before it counts as done.
// It sits between the spinner's frame, a tenth of a second, and the prompt
// cursor's blink, which redraws the line every 530ms while gluon waits — so a
// quiet this long means nothing is running, and a blink never looks busy.
const settle = 350 * time.Millisecond

// unset is what no shot inherits: an editor of the person's, colour turned
// off, gluon's own knobs.
var unset = []string{"EDITOR", "VISUAL", "NO_COLOR", "GLUON_NO_TYPECHECK", "GLUON_MAX_ITEMS", "GLUON_MAX_DEPTH", "SHOP_CONFIG"}

// A Batch is one run of shots through one window. Each shot gets a fresh
// gluon — its own config, history and scratchpads — in a home laid out the
// way a reader's is, so a path it prints reads ~/src/shop, and a fresh copy
// of the project it stands in.
type Batch struct {
	ws       *Workspace
	w        *Window
	home     string // HOME for every run
	pristine string // the fixture, copied and seeded once
	goenv    map[string]string
	shopd    *exec.Cmd
	raw      string // where captures go

	cellW, cellH float64 // a cell, in points
	padX, padY   int     // the window's padding, in points
	titlebar     int     // in points
	right, top   int     // where the window's top-right corner sits
	maxRows      int     // the most this screen holds
}

// runShots takes every shot named, or all of them, in each palette named.
func runShots(root string, ids []string, pals []Palette) error {
	shots, err := pick(root, ids)
	if err != nil {
		return err
	}
	ws, err := prepare(root)
	if err != nil {
		return err
	}
	b := &Batch{ws: ws, raw: filepath.Join(root, "dist", "shots", "raw")}
	if err := os.MkdirAll(b.raw, 0o755); err != nil {
		return err
	}
	if b.goenv, err = goEnv(); err != nil {
		return err
	}
	serve := false
	for _, s := range shots {
		serve = serve || s.Serve
	}
	if err := b.stage(root, serve); err != nil {
		return err
	}
	defer b.unserve()

	// A new window takes the keyboard. It opens when the screen is there to
	// be shot and nobody has typed for a while, so it takes no keystrokes
	// meant for something else.
	if err := waitLive(20 * time.Second); err != nil {
		return err
	}
	fmt.Printf("shots: opening one Ghostty window for %d shots × %d palettes; it closes itself when they are done\n", len(shots), len(pals))
	ctl := filepath.Join(ws.Dir, "ctl-"+time.Now().Format("150405"))
	b.w, err = Open(ctl, b.home, fontSize, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err := b.w.Close(quitKeys); err != nil {
			fmt.Fprintln(os.Stderr, "shots:", err)
		}
		if err := b.w.Unchanged(); err != nil {
			fmt.Fprintln(os.Stderr, "shots:", err)
		}
	}()
	if err := b.calibrate(); err != nil {
		return err
	}
	var failed []string
	for _, s := range shots {
		for _, p := range pals {
			if err := waitLive(0); err != nil {
				return err
			}
			start := time.Now()
			if err := b.shoot(s, p); err != nil {
				fmt.Printf("FAIL %-18s %-4s %v\n", s.ID, p.Name, err)
				failed = append(failed, s.ID+"."+p.Name)
				continue
			}
			fmt.Printf("ok   %-18s %-4s %.1fs\n", s.ID, p.Name, time.Since(start).Seconds())
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d failed: %s", len(failed), strings.Join(failed, " "))
	}
	return nil
}

// pick is the shots named, in the order named, or every shot.
func pick(root string, ids []string) ([]*Shot, error) {
	all, err := loadShots(root)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return all, nil
	}
	byID := map[string]*Shot{}
	for _, s := range all {
		byID[s.ID] = s
	}
	var out []*Shot
	for _, id := range ids {
		s, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("no shot %s in tapes/", id)
		}
		out = append(out, s)
	}
	return out, nil
}

// fontSize is the window's, in points: a shot's text at 2x is 26 pixels high,
// legible in a promo image scaled to a phone.
const fontSize = 13

// goEnv is where the toolchain's caches really are, so a shot builds warm with
// HOME moved.
func goEnv() (map[string]string, error) {
	keys := []string{"GOPATH", "GOMODCACHE", "GOCACHE", "GOFLAGS", "GONOSUMDB", "GOPRIVATE", "GOTOOLCHAIN"}
	out, err := exec.Command("go", append([]string{"env", "-json"}, keys...)...).Output()
	if err != nil {
		return nil, fmt.Errorf("go env: %v", err)
	}
	env := map[string]string{}
	return env, json.Unmarshal(out, &env)
}

// stage lays out the home, copies and seeds the fixture once, and starts its
// servers when a shot needs them.
func (b *Batch) stage(root string, serve bool) error {
	b.home = filepath.Join(b.ws.Dir, "home")
	if err := os.RemoveAll(b.home); err != nil {
		return err
	}
	b.pristine = filepath.Join(b.ws.Dir, "fixture", "shop")
	if err := os.RemoveAll(filepath.Dir(b.pristine)); err != nil {
		return err
	}
	if err := os.MkdirAll(b.home, 0o755); err != nil {
		return err
	}
	if err := os.CopyFS(b.pristine, os.DirFS(filepath.Join(root, "testdata", "shop"))); err != nil {
		return err
	}
	// The top of a repository, as a real checkout has, so gluon's project
	// search stops here.
	if err := os.MkdirAll(filepath.Join(b.pristine, ".git"), 0o755); err != nil {
		return err
	}
	if out, err := b.command(b.pristine, "go", "run", "-buildvcs=false", "./cmd/seed").CombinedOutput(); err != nil {
		return fmt.Errorf("seeding the fixture: %v\n%s", err, out)
	}
	if !serve {
		return nil
	}
	for _, addr := range []string{"127.0.0.1:8765", "127.0.0.1:50051"} {
		if c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
			c.Close()
			return fmt.Errorf("something is already listening on %s — the fixture's servers need it", addr)
		}
	}
	bin := filepath.Join(b.ws.Dir, "bin", "shopd")
	if out, err := b.command(b.pristine, "go", "build", "-buildvcs=false", "-o", bin, "./cmd/shopd").CombinedOutput(); err != nil {
		return fmt.Errorf("building shopd: %v\n%s", err, out)
	}
	b.shopd = exec.Command(bin)
	b.shopd.Dir = b.pristine
	if err := b.shopd.Start(); err != nil {
		return err
	}
	for _, addr := range []string{"127.0.0.1:8765", "127.0.0.1:50051"} {
		deadline := time.Now().Add(20 * time.Second)
		for {
			c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
			if err == nil {
				c.Close()
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("shopd never answered on %s", addr)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	return nil
}

func (b *Batch) unserve() {
	if b.shopd != nil && b.shopd.Process != nil {
		_ = b.shopd.Process.Kill()
		_, _ = b.shopd.Process.Wait()
	}
}

// command is a process run with the shots' toolchain settings: the real
// caches, and no network — a module comes from the cache or not at all.
func (b *Batch) command(dir, name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for k, v := range b.goenv {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Env = append(cmd.Env, "GOPROXY=off", "GOSUMDB=off")
	return cmd
}

// env is what a shot's gluon runs with.
func (b *Batch) env(s *Shot, host string) []string {
	env := []string{
		"HOME=" + b.home,
		"XDG_CONFIG_HOME=" + filepath.Join(b.home, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(b.home, ".local", "share"),
		"XDG_STATE_HOME=" + filepath.Join(b.home, ".local", "state"),
		"PATH=" + b.path(),
		"GOPROXY=off", "GOSUMDB=off", "TZ=UTC", "LANG=en_US.UTF-8",
	}
	for k, v := range b.goenv {
		if v != "" {
			env = append(env, k+"="+v)
		}
	}
	for _, kv := range s.Env {
		env = append(env, strings.ReplaceAll(kv, "$HOST", host))
	}
	return env
}

// path is the shots' PATH: the public gluon first, then the toolchain, then
// the system's.
func (b *Batch) path() string {
	gobin, _ := exec.LookPath("go")
	gobin, _ = filepath.EvalSymlinks(gobin)
	return strings.Join([]string{filepath.Dir(b.ws.Bin), filepath.Dir(gobin), "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"}, ":")
}

// fresh gives a shot a gluon that has never run — no config but the shot's,
// no history but the shot's, no scratchpads — and a project nobody has
// touched. It answers where the shot starts, and its environment.
func (b *Batch) fresh(s *Shot, p Palette) (string, []string, error) {
	for _, d := range []string{".config", ".local"} {
		if err := os.RemoveAll(filepath.Join(b.home, d)); err != nil {
			return "", nil, err
		}
	}
	dir, host := b.home, ""
	if s.Host != "" {
		if s.Host != "shop" {
			return "", nil, fmt.Errorf("the only fixture is shop, not %s", s.Host)
		}
		host = filepath.Join(b.home, "src", "shop")
		if err := os.RemoveAll(host); err != nil {
			return "", nil, err
		}
		if err := os.CopyFS(host, os.DirFS(b.pristine)); err != nil {
			return "", nil, err
		}
		dir = host
	}
	env := b.env(s, host)
	settings := append([]string{"banner " + s.Banner, "theme.name " + p.Theme}, s.Config...)
	args := []string{}
	for _, kv := range settings {
		args = append(args, "-e", ":settings "+kv)
	}
	if out, err := b.gluon(b.home, env, args...); err != nil {
		return "", nil, fmt.Errorf("writing its config: %v\n%s", err, out)
	}
	if len(s.History) > 0 {
		file := filepath.Join(b.home, ".local", "state", "gluon", "history")
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return "", nil, err
		}
		if err := os.WriteFile(file, []byte(strings.Join(s.History, "\n")+"\n"), 0o600); err != nil {
			return "", nil, err
		}
	}
	for _, line := range s.Setup {
		cmd := exec.Command("/bin/sh", "-c", line)
		cmd.Dir, cmd.Env = dir, env
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", nil, fmt.Errorf("setup %q: %v\n%s", line, err, out)
		}
	}
	return dir, env, nil
}

// gluon runs the shots' gluon outside the window.
func (b *Batch) gluon(dir string, env []string, args ...string) ([]byte, error) {
	cmd := exec.Command(b.ws.Bin, args...)
	cmd.Dir, cmd.Env = dir, env
	return cmd.CombinedOutput()
}

// calibrate reads the cell size from the window as it opened — maximized, by
// the person's config — and parks it at the top right of that screen, floating
// so that nothing covering it can stop Ghostty drawing it.
func (b *Batch) calibrate() error {
	x, y, width, height, err := b.w.Frame()
	if err != nil {
		return err
	}
	rows, cols := b.w.Geom()
	if rows == 0 || cols == 0 {
		return errors.New("the window reported no size")
	}
	b.padX, b.padY = ghosttyPadding()
	b.titlebar = 28
	b.cellW = float64(width-2*b.padX) / float64(cols)
	b.cellH = float64(height-b.titlebar-2*b.padY) / float64(rows)
	b.right, b.top = x+width-24, y+24
	b.maxRows = rows - 1
	// The window server's number first: it is found by the nonce title, and
	// a floating window is on another layer.
	if b.w.CGID, err = cgWindowID(b.w.Title); err != nil {
		return err
	}
	if err := b.w.Act("toggle_window_float_on_top"); err != nil {
		fmt.Fprintln(os.Stderr, "shots: the window could not float; keep it uncovered while it works:", err)
	}
	return nil
}

// ghosttyPadding is the window padding the person's config sets, in points.
func ghosttyPadding() (int, int) {
	x, y := 2, 2
	out, err := exec.Command("/Applications/Ghostty.app/Contents/MacOS/ghostty", "+show-config").Output()
	if err != nil {
		return x, y
	}
	for _, l := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(strings.Fields(strings.TrimSpace(v) + " 0")[0])
		if err != nil {
			continue
		}
		switch strings.TrimSpace(k) {
		case "window-padding-x":
			x = n
		case "window-padding-y":
			y = n
		}
	}
	return x, y
}

// fit sizes the window to a grid, between runs, by its frame: an estimate
// from the cell size, then corrected by what the terminal reports until it
// is exact.
func (b *Batch) fit(cols, rows int) error {
	if rows > b.maxRows {
		return fmt.Errorf("%d rows do not fit this screen; it holds %d", rows, b.maxRows)
	}
	if r, c := b.w.Geom(); r == rows && c == cols {
		return nil
	}
	width := int(math.Ceil(float64(cols)*b.cellW)) + 2*b.padX + 1
	height := int(math.Ceil(float64(rows)*b.cellH)) + b.titlebar + 2*b.padY + 1
	var r, c int
	for try := 0; try < 12; try++ {
		if try == 8 {
			// A frame that will not change is usually a window somebody is
			// holding — dragging it out of the way, say. Let go of it first.
			time.Sleep(3 * time.Second)
		}
		if err := b.w.Resize(b.right-width, b.top, width, height); err != nil {
			return err
		}
		for i := 0; i < 20; i++ {
			time.Sleep(50 * time.Millisecond)
			if r, c = b.w.Geom(); r == rows && c == cols {
				return nil
			}
		}
		width += nudge(cols-c, b.cellW)
		height += nudge(rows-r, b.cellH)
	}
	return fmt.Errorf("could not size the window to %dx%d; it is %dx%d", cols, rows, c, r)
}

// nudge is how far to move a frame's edge to gain or lose cells: whole cells
// when far off, and a few points when one cell out, since the estimate of a
// cell is only ever a little wrong.
func nudge(cells int, cell float64) int {
	switch {
	case cells == 0:
		return 0
	case cells == 1 || cells == -1:
		return cells * int(math.Max(2, cell/3))
	}
	return int(math.Round(float64(cells) * cell))
}

// shoot takes one shot in one palette.
func (b *Batch) shoot(s *Shot, p Palette) error {
	dir, env, err := b.fresh(s, p)
	if err != nil {
		return err
	}
	if err := b.fit(s.Cols, s.Rows); err != nil {
		return err
	}
	prog := Program{Dir: dir, Env: env, Unset: unset, Argv: append([]string{b.ws.Bin}, s.Args...), Palette: p}
	start := func() error {
		if err := b.w.Run(prog); err != nil {
			return err
		}
		if _, err := b.w.WaitFor(prompt, 0, 2*time.Minute); err != nil {
			return err
		}
		b.w.Settle(settle, time.Minute)
		return nil
	}
	if err := start(); err != nil {
		return err
	}
	defer func() {
		if b.w.running() {
			_ = b.w.Quit(quitKeys)
		}
	}()
	sent := 0 // the log's length when the last input went in
	input := func(f func() error) error {
		sent = len(b.w.plainLog())
		err := f()
		time.Sleep(60 * time.Millisecond)
		return err
	}
	for _, st := range s.Steps {
		var err error
		switch st.Op {
		case "Line":
			if err = input(func() error { return b.w.Type(st.Arg) }); err == nil {
				if err = input(func() error { return b.w.Act(keyAction("Enter", "")) }); err == nil {
					time.Sleep(150 * time.Millisecond)
					b.w.Settle(settle, 3*time.Minute)
				}
			}
		case "Type":
			err = input(func() error { return b.w.Type(st.Arg) })
		case "Keys":
			// A key at a time: a view reads / as the key that starts a
			// filter, and "/http" arriving in one read is not that key.
			err = input(func() error {
				for _, r := range st.Arg {
					if err := b.w.Type(string(r)); err != nil {
						return err
					}
					time.Sleep(40 * time.Millisecond)
				}
				return nil
			})
		case "Paste":
			err = input(func() error { return b.w.Paste(st.Arg) })
		case "Enter", "Tab", "Esc", "Backspace", "Up", "Down", "Left", "Right", "PgUp", "PgDn", "Ctrl":
			err = input(func() error { return b.w.Act(keyAction(st.Op, st.Arg)) })
			if st.Op == "Esc" {
				// A lone escape is a key only if nothing follows it in the
				// same read.
				time.Sleep(150 * time.Millisecond)
			}
		case "Wait":
			if st.Arg == "Prompt" {
				time.Sleep(150 * time.Millisecond)
				b.w.Settle(settle, 3*time.Minute)
				break
			}
			re, rerr := regexp.Compile(strings.TrimSuffix(strings.TrimPrefix(st.Arg, "/"), "/"))
			if rerr != nil {
				return fmt.Errorf("line %d: %v", st.Line, rerr)
			}
			_, err = b.w.WaitFor(re, sent, 3*time.Minute)
		case "Sleep":
			d, _ := time.ParseDuration(st.Arg)
			time.Sleep(d)
		case "Screenshot":
			err = b.screenshot(st.Arg, p)
		case "Relaunch":
			if err = b.w.Quit(quitKeys); err == nil {
				err = start()
			}
		}
		if err != nil {
			return fmt.Errorf("line %d (%s): %w", st.Line, st.Op, err)
		}
	}
	return b.w.Quit(quitKeys)
}

// keyAction is the bytes a key sends, as a Ghostty action.
func keyAction(op, arg string) string {
	switch op {
	case "Enter":
		return `text:\r`
	case "Tab":
		return `text:\t`
	case "Esc":
		return `text:\x1b`
	case "Backspace":
		return `text:\x7f`
	case "Up":
		return "csi:A"
	case "Down":
		return "csi:B"
	case "Right":
		return "csi:C"
	case "Left":
		return "csi:D"
	case "PgUp":
		return "csi:5~"
	case "PgDn":
		return "csi:6~"
	case "Ctrl":
		return "text:" + escapeText(string(rune(arg[0]-'a'+1)))
	}
	return ""
}

// screenshot captures the window until the prompt cursor's blink has been
// seen both ways, and keeps the capture where it is lit. gluon's cursor
// blinks — 530ms on, 530ms off — and a still image of it caught dark reads as
// a prompt with no cursor at all. Captures a third of a blink apart cannot all
// land in one half of it, so four of them are enough; four that are all the
// same mean nothing is blinking — a view, or vim's normal mode — and the first
// is kept.
func (b *Batch) screenshot(image string, p Palette) error {
	if live, _ := screenLive(); !live {
		// Nothing on the window changes while the screen is away; once it is
		// back, Ghostty draws it again and the capture is of what is there.
		if err := waitLive(0); err != nil {
			return err
		}
		time.Sleep(time.Second)
	}
	b.w.Settle(settle, time.Minute)
	out := filepath.Join(b.raw, image+"."+p.Name+".png")
	var frames []string
	defer func() {
		for _, f := range frames {
			os.Remove(f)
		}
	}()
	lit := ""
	for i := 0; i < 4 && lit == ""; i++ {
		if i > 0 {
			time.Sleep(180 * time.Millisecond)
		}
		f := fmt.Sprintf("%s.%d.png", out, i)
		if err := capture(b.w.CGID, f, false); err != nil {
			return err
		}
		frames = append(frames, f)
		if i == 0 {
			continue
		}
		pick, note, err := litCapture(frames[0], f)
		if err != nil {
			return err
		}
		if note != "" {
			fmt.Printf("     %s: %s\n", image, note)
		}
		if note != "" || !sameFile(frames[0], f) {
			lit = pick
		}
	}
	if lit == "" {
		lit = frames[0]
	}
	if err := os.Rename(lit, out); err != nil {
		return err
	}
	return os.WriteFile(strings.TrimSuffix(out, ".png")+".log", []byte(b.w.plainLog()), 0o644)
}

// sameFile says whether two captures are the same pixels.
func sameFile(a, b string) bool {
	ia, err := load(a)
	if err != nil {
		return false
	}
	ib, err := load(b)
	if err != nil || ia.Bounds() != ib.Bounds() {
		return false
	}
	r := ia.Bounds()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if !sameColor(ia.At(x, y), ib.At(x, y)) {
				return false
			}
		}
	}
	return true
}

// waitLive waits until the screen can be shot — and, when idle is not 0, until
// nobody has touched the keyboard or mouse for that long — saying once what
// it is waiting for. It gives up after two hours.
func waitLive(idle time.Duration) error {
	said := ""
	deadline := time.Now().Add(2 * time.Hour)
	for {
		live, why := screenLive()
		if live && idle > 0 && idleFor() < idle {
			live, why = false, "someone is typing"
		}
		if live {
			if said != "" {
				fmt.Println("shots: the screen is back")
			}
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("gave up waiting: %s", why)
		}
		if why != said {
			fmt.Printf("shots: waiting — %s\n", why)
			said = why
		}
		time.Sleep(5 * time.Second)
	}
}
