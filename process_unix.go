//go:build darwin || linux

package main

import (
	"os"
	"syscall"
)

func signalProcess0(proc *os.Process) error {
	return proc.Signal(syscall.Signal(0))
}

func terminateProcess(proc *os.Process) error {
	return proc.Signal(syscall.SIGTERM)
}
