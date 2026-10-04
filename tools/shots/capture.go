package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// cgWindowID finds the window server's number for the window with this title,
// through CGWindowListCopyWindowInfo. Reading another window's title needs
// Screen Recording, which is also what screencapture needs, so this failing is
// the first sign the permission is missing.
func cgWindowID(title string) (int, error) {
	const js = `
function run(argv) {
  ObjC.import("CoreGraphics");
  var raw = $.CGWindowListCopyWindowInfo($.kCGWindowListOptionAll, $.kCGNullWindowID);
  var list = ObjC.deepUnwrap(ObjC.castRefToObject(raw));
  for (var i = 0; i < list.length; i++) {
    var w = list[i];
    if (w.kCGWindowOwnerName === "Ghostty" && w.kCGWindowName === argv[0] && w.kCGWindowLayer >= 0 && w.kCGWindowLayer <= 3) return String(w.kCGWindowNumber);
  }
  return "";
}`
	if dryRun {
		fmt.Printf("jxa cgWindowID %q\n", title)
		return 1, nil
	}
	cmd := exec.Command("osascript", "-l", "JavaScript", "-", title)
	cmd.Stdin = strings.NewReader(js)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("window list: %v: %s", err, strings.TrimSpace(errb.String()))
	}
	s := strings.TrimSpace(out.String())
	if s == "" {
		return 0, fmt.Errorf("no window titled %s is on screen", title)
	}
	return strconv.Atoi(s)
}

// capture writes one window, and only that window, to a PNG at the display's
// own scale: -o leaves the shadow off (the docs' images sit on a page that
// draws its own frame), and a promo capture keeps it.
func capture(cgid int, path string, shadow bool) error {
	args := []string{"-x", "-r"}
	if !shadow {
		args = append(args, "-o")
	}
	args = append(args, "-l", strconv.Itoa(cgid), path)
	if dryRun {
		fmt.Printf("screencapture %q\n", args)
		return nil
	}
	out, err := exec.Command("screencapture", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("screencapture: %v: %s", err, out)
	}
	return nil
}

// imageSize is a PNG's pixel size, from ImageMagick.
func imageSize(path string) (w, h int, err error) {
	out, err := exec.Command("magick", "identify", "-format", "%w %h", path).Output()
	if err != nil {
		return 0, 0, err
	}
	_, err = fmt.Sscanf(string(out), "%d %d", &w, &h)
	return w, h, err
}

// pixel is one pixel's colour as #RRGGBB.
func pixel(path string, x, y int) (string, error) {
	out, err := exec.Command("magick", path, "-format", fmt.Sprintf("%%[hex:u.p{%d,%d}]", x, y), "info:").Output()
	if err != nil {
		return "", err
	}
	h := strings.TrimSpace(string(out))
	if len(h) > 6 {
		h = h[:6]
	}
	return "#" + strings.ToUpper(h), nil
}

// screenLive says whether the screen can be shot: unlocked, the display
// awake, and no screen saver over it. Behind any of those, System Events sees
// no windows and Ghostty may stop drawing the one it has, so a capture would
// be of nothing, or of a frame from before.
func screenLive() (bool, string) {
	if dryRun {
		return true, ""
	}
	const js = `
ObjC.import("CoreGraphics");
function run() {
  var raw = $.CGSessionCopyCurrentDictionary();
  var d = raw ? ObjC.deepUnwrap(ObjC.castRefToObject(raw)) : {};
  return (d && d.CGSSessionScreenIsLocked ? "locked" : "unlocked") + " " +
    ($.CGDisplayIsAsleep($.CGMainDisplayID()) ? "asleep" : "awake");
}`
	cmd := exec.Command("osascript", "-l", "JavaScript", "-")
	cmd.Stdin = strings.NewReader(js)
	out, err := cmd.Output()
	if err != nil {
		return false, "the screen's state could not be read"
	}
	switch f := strings.Fields(string(out)); {
	case len(f) != 2:
		return false, "the screen's state could not be read"
	case f[0] == "locked":
		return false, "the screen is locked"
	case f[1] == "asleep":
		return false, "the display is asleep"
	}
	if saver, err := osa("saver"); err == nil && saver == "true" {
		return false, "the screen saver is running"
	}
	return true, ""
}

// idleFor is how long since the last key or mouse event.
func idleFor() time.Duration {
	out, err := exec.Command("ioreg", "-c", "IOHIDSystem").Output()
	if err != nil {
		return 0
	}
	for _, l := range strings.Split(string(out), "\n") {
		if i := strings.Index(l, `"HIDIdleTime" = `); i >= 0 {
			ns, _ := strconv.ParseInt(strings.TrimSpace(l[i+len(`"HIDIdleTime" = `):]), 10, 64)
			return time.Duration(ns)
		}
	}
	return 0
}
