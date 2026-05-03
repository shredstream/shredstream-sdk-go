//go:build darwin

package shredstream

func pinThreadToCPU(cpu int) error {
	return nil
}

func setBusyPollSyscall(fd uintptr, micros uint32) error {
	return nil
}
