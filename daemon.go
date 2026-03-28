package launchdock

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

func ensureServerRunning(cfg LaunchConfig) error {
	if isServerHealthy(cfg.RawURL) {
		return nil
	}
	if pid, err := readDaemonPID(); err == nil {
		if processAlive(pid) {
			return waitForHealthy(cfg.RawURL, 5*time.Second)
		}
		removeDaemonPID()
	}
	if err := startBackgroundServer(); err != nil {
		return err
	}
	return waitForHealthy(cfg.RawURL, 10*time.Second)
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

func stopServer() error {
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

func daemonStatus(rawURL string) (string, int) {
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

func handlePSCommand() {
	cfg := resolveLaunchConfig()
	status, pid := daemonStatus(cfg.RawURL)
	fmt.Print("launchdock ps\n\n")
	fmt.Printf("status: %s\n", status)
	if pid > 0 {
		fmt.Printf("pid:    %d\n", pid)
	}
	fmt.Printf("url:    %s\n", cfg.RawURL)
	fmt.Printf("log:    %s\n\n", daemonLogPath())
}

func handleStopCommand() {
	if err := stopServer(); err != nil {
		fmt.Fprintf(os.Stderr, "✗ %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✓ launchdock stopped")
}

func handleRestartCommand() {
	cfg := resolveLaunchConfig()
	_ = stopServer()
	if err := startBackgroundServer(); err != nil {
		fmt.Fprintf(os.Stderr, "✗ %v\n", err)
		os.Exit(1)
	}
	if err := waitForHealthy(cfg.RawURL, 10*time.Second); err != nil {
		fmt.Fprintf(os.Stderr, "✗ %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ launchdock restarted at %s\n", cfg.RawURL)
}

func handleStartCommand() {
	cfg := resolveLaunchConfig()
	if err := ensureServerRunning(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "✗ %v\n", err)
		os.Exit(1)
	}
	status, pid := daemonStatus(cfg.RawURL)
	if pid > 0 {
		fmt.Printf("✓ launchdock %s at %s (pid %d)\n", status, cfg.RawURL, pid)
		return
	}
	fmt.Printf("✓ launchdock %s at %s\n", status, cfg.RawURL)
}

func handleLogsCommand() {
	path := daemonLogPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "✗ no log file at %s\n", path)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "✗ read log failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(string(data))
}
