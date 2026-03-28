package runtime

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func daemonDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".launchdock")
}

func daemonPIDPath() string {
	return filepath.Join(daemonDir(), "launchdock.pid")
}

func daemonLogPath() string {
	return filepath.Join(daemonDir(), "launchdock.log")
}

func isServerHealthy(rawURL string) bool {
	client := &http.Client{Timeout: 800 * time.Millisecond}
	resp, err := client.Get(rawURL + "/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func readDaemonPID() (int, error) {
	data, err := os.ReadFile(daemonPIDPath())
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, err
	}
	return pid, nil
}

func writeDaemonPID(pid int) error {
	if err := os.MkdirAll(daemonDir(), 0755); err != nil {
		return err
	}
	return os.WriteFile(daemonPIDPath(), []byte(strconv.Itoa(pid)), 0644)
}

func removeDaemonPID() {
	_ = os.Remove(daemonPIDPath())
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return signalProcess0(proc) == nil
}

func EnsureServerRunning(rawURL string) error {
	if isServerHealthy(rawURL) {
		return nil
	}
	if pid, err := readDaemonPID(); err == nil {
		if processAlive(pid) {
			return waitForHealthy(rawURL, 5*time.Second)
		}
		removeDaemonPID()
	}
	if err := startBackgroundServer(); err != nil {
		return err
	}
	return waitForHealthy(rawURL, 10*time.Second)
}

func waitForHealthy(rawURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if isServerHealthy(rawURL) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("server did not become healthy at %s", rawURL)
}

func StopServer() error {
	pid, err := readDaemonPID()
	if err != nil {
		return fmt.Errorf("no running daemon")
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		removeDaemonPID()
		return fmt.Errorf("stale pid file")
	}
	if err := terminateProcess(proc); err != nil {
		removeDaemonPID()
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			removeDaemonPID()
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("process %d did not stop", pid)
}

func DaemonStatus(rawURL string) (string, int) {
	pid, err := readDaemonPID()
	if err != nil {
		if isServerHealthy(rawURL) {
			return "running (unmanaged)", 0
		}
		return "stopped", 0
	}
	if !processAlive(pid) {
		removeDaemonPID()
		if isServerHealthy(rawURL) {
			return "running (unmanaged)", 0
		}
		return "stopped", 0
	}
	if isServerHealthy(rawURL) {
		return "running", pid
	}
	return "starting", pid
}

func DaemonLogPath() string { return daemonLogPath() }
