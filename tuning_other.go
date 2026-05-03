//go:build !linux && !darwin

package shredstream

func pinThreadToCPU(cpu int) error    { return nil }
func setBusyPollSyscall(uintptr, uint32) error { return nil }
