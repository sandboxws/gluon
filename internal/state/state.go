// Package state is where gluon keeps the files a session leaves behind that are
// neither configuration nor data: the REPL's history today.
//
// It is its own package rather than a helper in internal/repl because the XDG
// walk is the kind of rule that drifts when it is copied. internal/find's own
// doc records what happens when a walk gets a second copy instead of a second
// caller: the copies drift before anyone notices. One directory rule, in one
// place, before a second user is written rather than after.
package state

import (
	"os"
	"path/filepath"
)

// Dir is gluon's XDG state directory, created if it is not there.
//
// History is state, not config and not data: it is written by the program, it
// is not a thing the user edits, and losing it costs a convenience rather than
// a setting. $XDG_STATE_HOME wins when it is set — which is what lets a test
// point the whole tree at a t.TempDir() — and ~/.local/state is the fallback
// the spec names.
//
// It answers "" rather than an error when there is no home directory and none
// can be made: every caller's fallback is to do without the file, and a state
// directory that cannot exist is not a reason to refuse to start a REPL.
func Dir() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".local", "state")
	}
	dir := filepath.Join(base, "gluon")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	return dir
}

// File is one file inside Dir, or "" when there is no state directory — so a
// caller writes the same `if path == ""` guard either way.
func File(name string) string {
	dir := Dir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, name)
}
