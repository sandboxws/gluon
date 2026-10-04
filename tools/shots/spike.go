package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// spike opens one throwaway window and checks every primitive the shots
// depend on, printing a line for each. The window is closed by its program
// quitting, and the run ends by checking Ghostty has exactly the windows it
// had before.
func spike(root string) error {
	ws, err := prepare(root)
	if err != nil {
		return err
	}
	p := palettes[0]
	env, err := ws.home("spike", "banner = \"compact\"\n\n[theme]\nname = \""+p.Theme+"\"\n")
	if err != nil {
		return err
	}
	ctl := filepath.Join(ws.Dir, "ctl-spike-"+time.Now().Format("150405"))
	check := func(name string, err error, detail string) bool {
		mark := "ok  "
		if err != nil {
			mark, detail = "FAIL", err.Error()
		}
		fmt.Printf("%s %-28s %s\n", mark, name, detail)
		return err == nil
	}

	w, err := Open(ctl, ws.Dir, 13, nil)
	if !check("a new window, ours, one tab", err, "") {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = w.Close(quitKeys)
		}
	}()
	check("the wrapper's tty is the window's", nil, w.TTY)

	x, y, width, height, err := w.Frame()
	check("found by title (System Events)", err, fmt.Sprintf("%d,%d %dx%d", x, y, width, height))
	rows, cols := w.Geom()
	check("initial grid", nil, fmt.Sprintf("%dx%d cells", cols, rows))

	w.CGID, err = cgWindowID(w.Title)
	check("CGWindowID (screen recording)", err, fmt.Sprint(w.CGID))

	// Calibrate: a cell's size from the frame and the grid, then aim at 80x24.
	const padding, titlebar = 20, 28
	cw := float64(width-2*padding) / float64(cols)
	ch := float64(height-titlebar-2*padding) / float64(rows)
	tw, th := int(80*cw)+2*padding+2, int(24*ch)+titlebar+2*padding+2
	err = w.Resize(x+60, y+40, tw, th)
	time.Sleep(600 * time.Millisecond)
	rows, cols = w.Geom()
	check("resized to 80x24", err, fmt.Sprintf("frame %dx%d → %dx%d cells (cell %.2fx%.2f pt)", tw, th, cols, rows, cw, ch))

	if err := w.Run(Program{Dir: ws.Dir, Env: env, Argv: []string{ws.Bin, "-no-scratch"}, Palette: p}); err != nil {
		return err
	}
	at, err := w.WaitFor(regexp.MustCompile(`gluon>`), 0, 30*time.Second)
	if !check("gluon reached its prompt", err, "") {
		return err
	}
	w.Settle(300*time.Millisecond, 3*time.Second)
	err = w.Type("1 + 1")
	check("typed with a text action", err, "")
	err = w.Act("text:\\r")
	_, err2 := w.WaitFor(regexp.MustCompile(`\(int\) 2`), at, 20*time.Second)
	if err == nil {
		err = err2
	}
	check("enter, and the answer printed", err, "")

	at = len(w.plainLog())
	err = w.Paste("x := 40\nx + 2\n")
	_, err2 = w.WaitFor(regexp.MustCompile(`\(int\) 42`), at, 20*time.Second)
	if err == nil {
		err = err2
	}
	check("a two-line paste", err, "")
	w.Settle(300*time.Millisecond, 3*time.Second)

	dir := filepath.Join(ws.Dir, "spike")
	_ = os.MkdirAll(dir, 0o755)
	plain, shadow := filepath.Join(dir, "plain.png"), filepath.Join(dir, "shadow.png")
	err = capture(w.CGID, plain, false)
	pw, ph, _ := imageSize(plain)
	check("capture, no shadow", err, fmt.Sprintf("%dx%d px %s", pw, ph, plain))
	err = capture(w.CGID, shadow, true)
	sw, sh, _ := imageSize(shadow)
	check("capture, with shadow", err, fmt.Sprintf("%dx%d px", sw, sh))
	bg, err := pixel(plain, pw-12, ph-12)
	if err == nil && !strings.EqualFold(bg, p.BG) {
		err = fmt.Errorf("the corner is %s, not %s — OSC 11 did not reach the padding", bg, p.BG)
	}
	check("background is the palette's", err, bg)

	err = w.Close(quitKeys)
	closed = true
	check("closed by quitting, no dialog", err, "")
	err = w.Unchanged()
	check("Ghostty has the windows it had", err, "")
	return nil
}
