package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// The AppleScript this tool runs is these templates and nothing else. Each is
// embedded, takes its values only through argv — never by splicing text into
// the source — and is linted before it runs: nothing here may quit Ghostty,
// type into an application, run a shell, or reach a window by its position.
// A window is addressed by the id its own `new window` returned, and nothing
// else is ever resized, activated or closed.
var templates = map[string]string{
	// windows lists every Ghostty window: its id and how many tabs it has.
	"windows": `on run argv
	set sep to character id 9
	set nl to character id 10
	tell application "Ghostty"
		set out to {}
		repeat with w in windows
			set end of out to ((id of w) as text) & sep & ((count of tabs of w) as text)
		end repeat
	end tell
	set AppleScript's text item delimiters to nl
	return out as text
end run`,

	// open makes one new window running a command, and answers with the
	// window's id, its tab count, and its terminal's id and tty.
	"open": `on run argv
	set cmd to item 1 of argv
	set wd to item 2 of argv
	set fs to (item 3 of argv) as real
	set envs to {}
	if (count of argv) > 3 then set envs to items 4 thru -1 of argv
	set sep to character id 9
	tell application "Ghostty"
		set cfg to {command:cmd, initial working directory:wd, font size:fs, environment variables:envs}
		set w to new window with configuration cfg
		set t to focused terminal of selected tab of w
		return ((id of w) as text) & sep & ((count of tabs of w) as text) & sep & ((id of t) as text) & sep & ((tty of t) as text)
	end tell
end run`,

	// process is a window's terminal's foreground pid and title.
	"process": `on run argv
	set wid to item 1 of argv
	set sep to character id 9
	tell application "Ghostty"
		set t to focused terminal of selected tab of (first window whose id is wid)
		return ((pid of t) as text) & sep & ((name of t) as text)
	end tell
end run`,

	// tabs is how many tabs one window has now.
	"tabs": `on run argv
	set wid to item 1 of argv
	tell application "Ghostty"
		return (count of tabs of (first window whose id is wid)) as text
	end tell
end run`,

	// exists says whether a window is still there.
	"exists": `on run argv
	set wid to item 1 of argv
	tell application "Ghostty"
		return ((count of (windows whose id is wid)) > 0) as text
	end tell
end run`,

	// act performs a Ghostty action on one terminal: text and escape
	// sequences, sent to the pty the way typing sends them.
	"act": `on run argv
	set tid to item 1 of argv
	set act to item 2 of argv
	tell application "Ghostty"
		return (perform action act on (first terminal whose id is tid)) as text
	end tell
end run`,

	// input sends text to one terminal as if it were pasted, which is how a
	// program that asked for bracketed paste receives a pasted block.
	"input": `on run argv
	set tid to item 1 of argv
	set txt to item 2 of argv
	tell application "Ghostty"
		input text txt to (first terminal whose id is tid)
	end tell
end run`,

	// close closes one window, by its id. It is only ever asked of a window
	// whose process has already exited, so no confirmation is waiting.
	"close": `on run argv
	set wid to item 1 of argv
	tell application "Ghostty"
		close window (first window whose id is wid)
	end tell
end run`,

	// saver says whether the screen saver is running.
	"saver": `on run argv
	tell application "System Events"
		return (running of screen saver preferences) as text
	end tell
end run`,

	// frame and resize read and set a window's frame through System Events,
	// by the unique title the wrapper gave it: Ghostty's dictionary has no
	// bounds property.
	"frame": `on run argv
	set title to item 1 of argv
	set sep to character id 9
	tell application "System Events"
		tell process "Ghostty"
			set w to first window whose name is title
			set p to position of w
			set s to size of w
			return ((item 1 of p) as text) & sep & ((item 2 of p) as text) & sep & ((item 1 of s) as text) & sep & ((item 2 of s) as text)
		end tell
	end tell
end run`,
	"resize": `on run argv
	set title to item 1 of argv
	set x to (item 2 of argv) as integer
	set y to (item 3 of argv) as integer
	set wd to (item 4 of argv) as integer
	set ht to (item 5 of argv) as integer
	tell application "System Events"
		tell process "Ghostty"
			set w to first window whose name is title
			set position of w to {x, y}
			set size of w to {wd, ht}
		end tell
	end tell
end run`,
}

// forbidden is what no template may say. `quit` is the one that matters most:
// the user's other windows are in the same Ghostty, and nothing here may ever
// end it. `keystroke` and `key code` type into whatever is frontmost, `do
// shell script` is a shell with no argv discipline, and the window-by-position
// forms could name a window that is not ours.
var forbidden = []string{
	"quit", "keystroke", "key code", "do shell script", "front window",
	"window 1", "last window", "click", "delete", "close tab", "activate",
	"run script", "load script", "store script", "clipboard",
}

// targets are the only applications a template may tell anything.
var targets = map[string]bool{"Ghostty": true, "System Events": true}

var tellRe = regexp.MustCompile(`tell application "([^"]+)"`)

// lint reports what is wrong with a template, or nil.
func lint(name, src string) error {
	low := strings.ToLower(src)
	for _, f := range forbidden {
		if regexp.MustCompile(`\b` + regexp.QuoteMeta(f) + `\b`).MatchString(low) {
			return fmt.Errorf("template %s says %q, which this tool never does", name, f)
		}
	}
	for _, m := range tellRe.FindAllStringSubmatch(src, -1) {
		if !targets[m[1]] {
			return fmt.Errorf("template %s tells %q, which is not Ghostty or System Events", name, m[1])
		}
	}
	if regexp.MustCompile(`&\s*tab\s*&`).MatchString(src) {
		return fmt.Errorf("template %s joins with tab, which inside Ghostty's tell block is its tab class — use a separator set outside it", name)
	}
	if strings.Contains(src, "close window") && !strings.Contains(src, "whose id is wid") {
		return fmt.Errorf("template %s closes a window it does not name by id", name)
	}
	return nil
}

// actions are the only Ghostty actions `act` may perform: bytes to the pty,
// and floating the window it is in above the others — which is only ever one
// of ours, since an action goes to a terminal by its id.
var actions = []string{"text:", "csi:", "esc:"}

var exactActions = map[string]bool{"toggle_window_float_on_top": true}

// checkAction refuses an action outside the allowlist.
func checkAction(a string) error {
	if exactActions[a] {
		return nil
	}
	for _, p := range actions {
		if strings.HasPrefix(a, p) {
			return nil
		}
	}
	return fmt.Errorf("action %q is not one this tool performs", a)
}

// dryRun prints every call instead of making it.
var dryRun bool

// osa runs one template with argv and returns what it printed.
func osa(name string, argv ...string) (string, error) {
	src, ok := templates[name]
	if !ok {
		return "", fmt.Errorf("no template %s", name)
	}
	if err := lint(name, src); err != nil {
		return "", err
	}
	if dryRun {
		fmt.Printf("osa %s %q\n", name, argv)
		return "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "osascript", append([]string{"-"}, argv...)...)
	cmd.Stdin = strings.NewReader(src)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("osascript %s: %v: %s", name, err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}
