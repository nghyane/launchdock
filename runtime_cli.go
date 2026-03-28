package launchdock

import (
	"fmt"
	"os"

	runtimepkg "github.com/nghiahoang/launchdock/internal/runtime"
)

func ensureServerRunning(cfg LaunchConfig) error { return runtimepkg.EnsureServerRunning(cfg.RawURL) }

func handlePSCommand() {
	cfg := resolveLaunchConfig()
	status, pid := runtimepkg.DaemonStatus(cfg.RawURL)
	fmt.Print("launchdock ps\n\n")
	fmt.Printf("status: %s\n", status)
	if pid > 0 {
		fmt.Printf("pid:    %d\n", pid)
	}
	fmt.Printf("url:    %s\n", cfg.RawURL)
	fmt.Printf("log:    %s\n\n", runtimepkg.DaemonLogPath())
}

func handleStopCommand() {
	if err := runtimepkg.StopServer(); err != nil {
		fmt.Fprintf(os.Stderr, "✗ %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✓ launchdock stopped")
}

func handleRestartCommand() {
	cfg := resolveLaunchConfig()
	_ = runtimepkg.StopServer()
	if err := runtimepkg.EnsureServerRunning(cfg.RawURL); err != nil {
		fmt.Fprintf(os.Stderr, "✗ %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ launchdock restarted at %s\n", cfg.RawURL)
}

func handleStartCommand() {
	cfg := resolveLaunchConfig()
	if err := runtimepkg.EnsureServerRunning(cfg.RawURL); err != nil {
		fmt.Fprintf(os.Stderr, "✗ %v\n", err)
		os.Exit(1)
	}
	status, pid := runtimepkg.DaemonStatus(cfg.RawURL)
	if pid > 0 {
		fmt.Printf("✓ launchdock %s at %s (pid %d)\n", status, cfg.RawURL, pid)
		return
	}
	fmt.Printf("✓ launchdock %s at %s\n", status, cfg.RawURL)
}

func handleLogsCommand() {
	path := runtimepkg.DaemonLogPath()
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
