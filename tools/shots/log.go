package main

import (
	"fmt"
	"regexp"
	"time"
)

// ansiRe is a terminal escape sequence: CSI, OSC and the two-byte forms.
var ansiRe = regexp.MustCompile("\x1b(\\[[0-?]*[ -/]*[@-~]|\\][^\x07\x1b]*(\x07|\x1b\\\\)|[@-Z\\\\-_])")

// plainLog is what the current run has printed, escapes removed.
func (w *Window) plainLog() string {
	return ansiRe.ReplaceAllString(w.rawLog(), "")
}

// rawLog is what the current run has printed, as the terminal received it.
func (w *Window) rawLog() string { return read(w.file(w.n, "log")) }

// WaitFor waits until the program has printed something matching re since the
// log was at offset from, and returns the log's new length.
func (w *Window) WaitFor(re *regexp.Regexp, from int, d time.Duration) (int, error) {
	deadline := time.Now().Add(d)
	for {
		log := w.plainLog()
		if from < len(log) && re.MatchString(log[from:]) {
			return len(log), nil
		}
		if time.Now().After(deadline) {
			tail := log
			if len(tail) > 400 {
				tail = tail[len(tail)-400:]
			}
			return len(log), fmt.Errorf("waited %v for %s; the window last printed:\n%s", d, re, tail)
		}
		time.Sleep(60 * time.Millisecond)
	}
}

// Settle waits until the program has printed nothing new for quiet.
func (w *Window) Settle(quiet, max time.Duration) {
	deadline := time.Now().Add(max)
	last, since := -1, time.Now()
	for time.Now().Before(deadline) {
		n := len(w.rawLog())
		if n != last {
			last, since = n, time.Now()
		} else if time.Since(since) >= quiet {
			return
		}
		time.Sleep(40 * time.Millisecond)
	}
}
