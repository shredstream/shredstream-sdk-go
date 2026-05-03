//go:build linux

package shredstream

import (
	"syscall"

	"golang.org/x/sys/unix"
)

const soBusyPoll = 0x2E

func pinThreadToCPU(cpu int) error {
	if cpu < 0 {
		return syscall.EINVAL
	}
	var set unix.CPUSet
	set.Zero()
	set.Set(cpu)
	return unix.SchedSetaffinity(0, &set)
}

func setBusyPollSyscall(fd uintptr, micros uint32) error {
	val := int(micros)
	return syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, soBusyPoll, val)
}
