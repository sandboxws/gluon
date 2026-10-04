//go:build linux

package gluonrt

import (
	"os"
	"syscall"
)

// The Linux spelling of the fd gate — fd_dup2.go says why the gate exists and
// why it is split by platform. Linux uses Dup3 because linux/arm64 has no
// dup2 in its syscall table at all. Dup3 with no flags is dup2 on every
// kernel Go supports, except that it refuses oldfd == newfd — a case the gate
// never produces, since the descriptor being retargeted is 1 or 2 and the
// other side is always a freshly allocated one.
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
			syscall.Dup3(int(null.Fd()), fd, 0)
		}
	}
}

func __gluonUnmute() {
	for i, fd := range []int{1, 2} {
		if __gluonSaved[i] > 0 {
			syscall.Dup3(__gluonSaved[i], fd, 0)
			syscall.Close(__gluonSaved[i])
			__gluonSaved[i] = 0
		}
	}
}
