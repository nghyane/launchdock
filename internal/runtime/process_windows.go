//go:build windows

package runtime

import (
	"os"
	"syscall"
)

func signalProcess0(proc *os.Process) error {
	return proc.Signal(syscall.Signal(0))
}

func terminateProcess(proc *os.Process) error {
	return proc.Kill()
}
