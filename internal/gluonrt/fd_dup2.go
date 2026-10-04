//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package gluonrt

import (
	"os"
	"syscall"
)

// Every evaluation replays the whole session, so everything before the newest
// entry is a re-run and its output is noise. Muting at the file-descriptor
// level rather than diffing the output afterwards is what makes this correct
// for nondeterministic programs: map range order, time, and rand all differ
// between runs, and a text diff mistakes that for new output.
//
// The gate lives in a per-platform file because the syscall that retargets a
// descriptor is not spelled the same everywhere: this file uses Dup2, which
// linux/arm64 does not have — that syscall table was assembled after dup2 was
// superseded, so only dup3 exists there. fd_dup3.go is the Linux spelling.
// The two files must not drift in behaviour: render emits the one matching
// the host and the checker parses that same one, so a difference between them
// would surface as platform-dependent output.
var __gluonSaved [2]int

func __gluonMute() {
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return
	}
	defer null.Close()
	for i, fd := range []int{1, 2} {
		if saved, err := syscall.Dup(fd); err == nil {
			__gluonSaved[i] = saved
			syscall.Dup2(int(null.Fd()), fd)
		}
	}
}

func __gluonUnmute() {
	for i, fd := range []int{1, 2} {
		if __gluonSaved[i] > 0 {
			syscall.Dup2(__gluonSaved[i], fd)
			syscall.Close(__gluonSaved[i])
			__gluonSaved[i] = 0
		}
	}
}
