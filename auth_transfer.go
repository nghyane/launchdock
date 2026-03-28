package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type authExportPayload struct {
	Version     int                `json:"version"`
	ExportedAt  string             `json:"exported_at"`
	Credentials []ConfigCredential `json:"credentials"`
}

func managedConfigCredentials(ids []string) ([]ConfigCredential, error) {
	cfg := loadConfig()
	if len(ids) == 0 {
		if len(cfg.Credentials) == 0 {
			return nil, fmt.Errorf("no managed credentials to export")
		}
		return cfg.Credentials, nil
	}
	lookup := map[string]bool{}
	for _, id := range ids {
		lookup[id] = true
	}
	var out []ConfigCredential
	for _, cc := range cfg.Credentials {
		if lookup[cc.ID] {
			out = append(out, cc)
			delete(lookup, cc.ID)
		}
	}
	if len(lookup) > 0 {
		var missing []string
		for id := range lookup {
			missing = append(missing, id)
		}
		return nil, fmt.Errorf("unknown credential ids: %s", strings.Join(missing, ", "))
	}
	return out, nil
}

func exportManagedCredentials(ids []string) ([]byte, error) {
	creds, err := managedConfigCredentials(ids)
	if err != nil {
		return nil, err
	}
	payload := authExportPayload{
		Version:     1,
		ExportedAt:  nowRFC3339(),
		Credentials: creds,
	}
	return json.MarshalIndent(payload, "", "  ")
}

func importManagedCredentials(r io.Reader) (int, error) {
	var payload authExportPayload
	if err := json.NewDecoder(r).Decode(&payload); err != nil {
		return 0, fmt.Errorf("parse import payload: %w", err)
	}
	if payload.Version != 1 {
		return 0, fmt.Errorf("unsupported payload version: %d", payload.Version)
	}
	if len(payload.Credentials) == 0 {
		return 0, fmt.Errorf("no credentials in import payload")
	}
	return mergeImportedCredentials(payload.Credentials)
}

func mergeImportedCredentials(imported []ConfigCredential) (int, error) {
	cfg := loadConfig()
	index := map[string]int{}
	for i, cc := range cfg.Credentials {
		index[cc.ID] = i
	}
	count := 0
	for _, cc := range imported {
		if cc.ID == "" {
			cc.ID = generateCredentialID()
		}
		if i, ok := index[cc.ID]; ok {
			cfg.Credentials[i] = cc
		} else {
			cfg.Credentials = append(cfg.Credentials, cc)
			index[cc.ID] = len(cfg.Credentials) - 1
		}
		count++
	}
	return count, saveConfig(cfg)
}

func handleAuthExport() {
	data, err := exportManagedCredentials(os.Args[3:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Export failed: %v\n", err)
		os.Exit(1)
	}
	_, _ = os.Stdout.Write(data)
	_, _ = os.Stdout.Write([]byte("\n"))
}

func handleAuthImport() {
	n, err := importManagedCredentials(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Import failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Imported %d managed credential(s).\n", n)
}

func handleAuthPush() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "Usage: launchdock auth push <ssh-target> [credential-id ...]")
		os.Exit(1)
	}
	target := os.Args[3]
	ids := os.Args[4:]
	payload, err := exportManagedCredentials(ids)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Push failed: %v\n", err)
		os.Exit(1)
	}
	if err := ensureRemoteLaunchdock(target); err != nil {
		fmt.Fprintf(os.Stderr, "Remote install failed: %v\n", err)
		os.Exit(1)
	}
	cmd := exec.Command("ssh", target, "$HOME/.local/bin/launchdock auth import")
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Push failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Pushed managed credential(s) to %s\n", target)
}

func ensureRemoteLaunchdock(target string) error {
	version := currentVersion()
	if version == "" {
		version = latestReleaseVersion()
	}
	check := exec.Command("ssh", target, "$HOME/.local/bin/launchdock version 2>/dev/null || launchdock version 2>/dev/null || true")
	out, _ := check.Output()
	remoteVersion := strings.TrimSpace(string(out))
	if remoteVersion == version && remoteVersion != "" {
		return nil
	}
	return installRemoteLaunchdock(target, version)
}

func installRemoteLaunchdock(target, version string) error {
	if version == "" {
		return fmt.Errorf("could not resolve launchdock release version")
	}
	installScript := fmt.Sprintf(`set -e
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *) echo "unsupported arch: $ARCH" >&2; exit 1 ;;
esac
NAME="launchdock-%s-${OS}-${ARCH}"
URL="https://github.com/nghyane/launchdock/releases/download/%s/${NAME}.tar.gz"
CHECKSUM_URL="https://github.com/nghyane/launchdock/releases/download/%s/checksums-${OS}-${ARCH}.txt"
mkdir -p "$HOME/.local/bin" "$HOME/.cache/launchdock"
curl -fsSL "$URL" -o "$HOME/.cache/launchdock/launchdock.tgz"
curl -fsSL "$CHECKSUM_URL" -o "$HOME/.cache/launchdock/checksums.txt"
cd "$HOME/.cache/launchdock"
sha256sum -c checksums.txt --ignore-missing
rm -rf "$HOME/.cache/launchdock/unpack"
mkdir -p "$HOME/.cache/launchdock/unpack"
tar -xzf "$HOME/.cache/launchdock/launchdock.tgz" -C "$HOME/.cache/launchdock/unpack"
install "$HOME/.cache/launchdock/unpack/launchdock" "$HOME/.local/bin/launchdock"
$HOME/.local/bin/launchdock version >/dev/null
`, version, version, version)
	cmd := exec.Command("ssh", target, installScript)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func currentVersion() string {
	if version != "" && version != "dev" {
		return version
	}
	cmd := exec.Command("git", "describe", "--tags", "--exact-match")
	out, err := cmd.Output()
	if err == nil {
		return string(bytes.TrimSpace(out))
	}
	return version
}

func latestReleaseVersion() string {
	resp, err := http.Get("https://api.github.com/repos/nghyane/launchdock/releases/latest")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var result struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ""
	}
	return result.TagName
}

func handleUpdateCommand() {
	version := ""
	args := os.Args[2:]
	for i, arg := range args {
		if arg == "--version" && i+1 < len(args) {
			version = args[i+1]
		}
	}
	current := currentVersion()
	if version == "" {
		version = latestReleaseVersion()
	}
	if version == "" {
		fmt.Fprintln(os.Stderr, "Update failed: could not resolve release version")
		os.Exit(1)
	}
	fmt.Printf("Current: %s\nLatest:  %s\n", current, version)
	if current == version && current != "dev" {
		fmt.Println("Already up to date.")
		return
	}
	if runtime.GOOS == "windows" {
		fmt.Fprintln(os.Stderr, "Update is not supported automatically on Windows yet.")
		os.Exit(1)
	}
	if !runConfirm("Update launchdock to " + version + "?") {
		fmt.Println("Aborted.")
		return
	}
	if err := installLocalLaunchdock(version); err != nil {
		fmt.Fprintf(os.Stderr, "Update failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Updated launchdock to %s\n", version)
}

func handleVersionCommand() {
	fmt.Println(currentVersion())
}

func installLocalLaunchdock(version string) error {
	asset := releaseAssetName(version, runtime.GOOS, runtime.GOARCH)
	if asset == "" {
		return fmt.Errorf("unsupported platform: %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	url := fmt.Sprintf("https://github.com/nghyane/launchdock/releases/download/%s/%s", version, asset)
	checksumURL := fmt.Sprintf("https://github.com/nghyane/launchdock/releases/download/%s/%s", version, releaseChecksumAsset(runtime.GOOS, runtime.GOARCH))
	tmpDir, err := os.MkdirTemp("", "launchdock-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	archivePath := filepath.Join(tmpDir, asset)
	if err := downloadFile(url, archivePath); err != nil {
		return err
	}
	checksumPath := filepath.Join(tmpDir, "checksums.txt")
	if err := downloadFile(checksumURL, checksumPath); err != nil {
		return err
	}
	if err := verifyChecksum(archivePath, checksumPath); err != nil {
		return err
	}
	binPath, err := extractReleaseBinary(archivePath, tmpDir)
	if err != nil {
		return err
	}
	execPath, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.Rename(binPath, execPath+".new"); err != nil {
		return err
	}
	return os.Rename(execPath+".new", execPath)
}

func releaseChecksumAsset(goos, goarch string) string {
	return fmt.Sprintf("checksums-%s-%s.txt", goos, goarch)
}

func releaseAssetName(version, goos, goarch string) string {
	switch goos {
	case "linux", "darwin":
		if goarch == "amd64" || goarch == "arm64" {
			return fmt.Sprintf("launchdock-%s-%s-%s.tar.gz", version, goos, goarch)
		}
	case "windows":
		if goarch == "amd64" {
			return fmt.Sprintf("launchdock-%s-%s-%s.zip", version, goos, goarch)
		}
	}
	return ""
}

func downloadFile(url, path string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download failed: %s", resp.Status)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

func extractReleaseBinary(archivePath, dir string) (string, error) {
	cmd := exec.Command("tar", "-xzf", archivePath, "-C", dir)
	if err := cmd.Run(); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "launchdock")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("launchdock binary not found in archive")
}

func verifyChecksum(archivePath, checksumPath string) error {
	archiveName := filepath.Base(archivePath)
	checksums, err := os.ReadFile(checksumPath)
	if err != nil {
		return err
	}
	expected := ""
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == archiveName {
			expected = fields[0]
			break
		}
	}
	if expected == "" {
		return fmt.Errorf("checksum not found for %s", archiveName)
	}
	data, err := os.ReadFile(archivePath)
	if err != nil {
		return err
	}
	sum := fmt.Sprintf("%x", sha256.Sum256(data))
	if sum != expected {
		return fmt.Errorf("checksum mismatch for %s", archiveName)
	}
	return nil
}

func nowRFC3339() string {
	return nowFunc().UTC().Format("2006-01-02T15:04:05Z07:00")
}

var nowFunc = func() time.Time { return time.Now() }
