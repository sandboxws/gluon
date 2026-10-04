package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// preflight checks, without opening a window or asking for anything, what
// the shots need: Ghostty and the tools, and the three permissions macOS
// grants the app this runs from — Automation to drive Ghostty and System
// Events, Accessibility to size a window, Screen Recording to read a window's
// title and capture it.
func preflight() error {
	failed := 0
	check := func(name string, err error, detail string) {
		mark := "ok  "
		if err != nil {
			mark, detail = "FAIL", err.Error()
			failed++
		}
		fmt.Printf("%s %-30s %s\n", mark, name, detail)
	}
	_, err := os.Stat("/Applications/Ghostty.app")
	check("Ghostty is installed", err, "")
	for _, tool := range []string{"osascript", "screencapture", "magick", "cwebp", "go", "script"} {
		_, err := exec.LookPath(tool)
		check(tool, err, "")
	}
	ws, err := windows()
	check("Automation: Ghostty", err, fmt.Sprintf("%d windows", len(ws)))
	_, err = osa("saver")
	check("Automation: System Events", err, "")
	names, err := windowNames()
	if err == nil && len(ws) > 0 && len(names) == 0 {
		err = fmt.Errorf("Ghostty's windows have no titles here — grant Screen Recording to the app this runs in, and restart it")
	}
	check("Screen Recording", err, strings.Join(names, ", "))
	live, why := screenLive()
	if !live {
		err = fmt.Errorf("%s", why)
	} else {
		err = nil
	}
	check("the screen is live", err, "")
	if failed > 0 {
		return fmt.Errorf("%d checks failed", failed)
	}
	return nil
}

// windowNames is the title of every Ghostty window on screen, as the window
// server reports them — empty without Screen Recording.
func windowNames() ([]string, error) {
	const js = `
function run() {
  ObjC.import("CoreGraphics");
  var raw = $.CGWindowListCopyWindowInfo($.kCGWindowListOptionOnScreenOnly, $.kCGNullWindowID);
  var list = ObjC.deepUnwrap(ObjC.castRefToObject(raw));
  var out = [];
  for (var i = 0; i < list.length; i++) {
    var w = list[i];
    if (w.kCGWindowOwnerName === "Ghostty" && w.kCGWindowName) out.push(w.kCGWindowName);
  }
  return out.join("\n");
}`
	cmd := exec.Command("osascript", "-l", "JavaScript", "-")
	cmd.Stdin = strings.NewReader(js)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, n := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if n != "" {
			names = append(names, n)
		}
	}
	return names, nil
}
