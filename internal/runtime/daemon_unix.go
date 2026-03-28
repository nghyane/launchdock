//go:build darwin || linux

package runtime

import (
	"os"
	"os/exec"
	"syscall"
)

func startBackgroundServer() error {
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(daemonDir(), 0755); err != nil {
		return err
	}
	logf, err := os.OpenFile(daemonLogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	cmd := exec.Command(bin)
	cmd.Env = os.Environ()
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		_ = logf.Close()
		return err
	}
	if err := writeDaemonPID(cmd.Process.Pid); err != nil {
		return err
	}
	return logf.Close()
}
