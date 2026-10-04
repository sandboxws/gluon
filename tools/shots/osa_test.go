package main

import "testing"

func TestEveryTemplateIsClean(t *testing.T) {
	for name, src := range templates {
		if err := lint(name, src); err != nil {
			t.Error(err)
		}
	}
}

func TestTheLintRefusesWhatWouldReachAnotherWindow(t *testing.T) {
	for _, src := range []string{
		`tell application "Ghostty" to quit`,
		`tell application "System Events" to keystroke "q"`,
		`tell application "Ghostty" to close window 1`,
		`tell application "Ghostty" to close front window`,
		`tell application "Finder" to get name`,
		`do shell script "rm -rf /"`,
		`tell application "Ghostty" to close window (first window whose name is "x")`,
	} {
		if lint("bad", src) == nil {
			t.Errorf("lint passed %q", src)
		}
	}
}

func TestOnlyPtyActionsArePerformed(t *testing.T) {
	for _, a := range []string{"text:ls", `csi:A`, "esc:b", "toggle_window_float_on_top"} {
		if err := checkAction(a); err != nil {
			t.Errorf("%q refused: %v", a, err)
		}
	}
	for _, a := range []string{"quit", "close_surface", "toggle_fullscreen", "new_window", "write_screen_file:copy", "toggle_window_float_on_top:x", "toggle_quick_terminal"} {
		if checkAction(a) == nil {
			t.Errorf("%q allowed", a)
		}
	}
}
