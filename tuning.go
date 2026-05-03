package shredstream

import (
	"runtime"
	"syscall"
)

func PinThreadToCPU(cpu int) error {
	return pinThreadToCPU(cpu)
}

func LockOSThread() {
	runtime.LockOSThread()
}

func setBusyPoll(fd uintptr, micros uint32) error {
	return setBusyPollSyscall(fd, micros)
}

func setRecvBuf(fd uintptr, size int) error {
	return syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, size)
}

func getRecvBuf(fd uintptr) (int, error) {
	return syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF)
}
