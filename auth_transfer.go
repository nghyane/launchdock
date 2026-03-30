package launchdock

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

	authpkg "github.com/nghiahoang/launchdock/internal/auth"
	httpxpkg "github.com/nghiahoang/launchdock/internal/httpx"
)

type authExportPayload struct {
	Version     int                        `json:"version"`
	ExportedAt  string                     `json:"exported_at"`
	Credentials []authpkg.ConfigCredential `json:"credentials"`
}

func exportableCredentials(ids []string) ([]authpkg.ConfigCredential, error) {
	lookup := map[string]bool{}
	if len(ids) > 0 {
		for _, id := range ids {
			lookup[id] = true
		}
	}

	var out []authpkg.ConfigCredential
	seen := map[string]bool{}

	appendCred := func(cc authpkg.ConfigCredential) {
		key := cc.Provider + "|" + cc.AccountID + "|" + cc.Email + "|" + cc.Label
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, cc)
	}

	// Managed credentials first.
	managed, err := managedConfigCredentials(nil)
	if err == nil {
		for _, cc := range managed {
			if len(lookup) > 0 && !lookup[cc.ID] {
				continue
			}
			appendCred(cc)
			delete(lookup, cc.ID)
		}
	}

	// Export local OAuth credentials too.
	for _, cred := range authpkg.LoadAllCredentials() {
		if cred.AuthType != authpkg.AuthOAuth {
			continue
		}
		if cred.RefreshToken == "" {
			continue
		}
		if len(lookup) > 0 && cred.ID != "" && !lookup[cred.ID] {
			continue
		}
		appendCred(authpkg.ConfigCredential{
			ID:           authpkg.GenerateCredentialID(),
			Label:        cred.Label,
			Provider:     cred.Provider,
			Kind:         cred.Kind,
			RefreshToken: cred.RefreshToken,
			AccountID:    cred.AccountID,
			Email:        cred.Email,
			Disabled:     false,
		})
		if cred.ID != "" {
			delete(lookup, cred.ID)
		}
	}

	if len(ids) > 0 && len(lookup) > 0 {
		var missing []string
		for id := range lookup {
			missing = append(missing, id)
		}
		return nil, fmt.Errorf("unknown credential ids: %s", strings.Join(missing, ", "))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no credentials to export")
	}
	return out, nil
}

func managedConfigCredentials(ids []string) ([]authpkg.ConfigCredential, error) {
	cfg := authpkg.LoadConfig()
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
	var out []authpkg.ConfigCredential
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
	creds, err := exportableCredentials(ids)
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

func mergeImportedCredentials(imported []authpkg.ConfigCredential) (int, error) {
	cfg := authpkg.LoadConfig()
	index := map[string]int{}
	for i, cc := range cfg.Credentials {
		index[cc.ID] = i
	}
	count := 0
	for _, cc := range imported {
		if cc.ID == "" {
			cc.ID = authpkg.GenerateCredentialID()
		}
		if i, ok := index[cc.ID]; ok {
			cfg.Credentials[i] = cc
		} else {
			cfg.Credentials = append(cfg.Credentials, cc)
			index[cc.ID] = len(cfg.Credentials) - 1
		}
		count++
	}
	return count, authpkg.SaveConfig(cfg)
}

func handleAuthExport() {
	if isTerminal(int(os.Stdout.Fd())) {
		fmt.Fprintln(os.Stderr, "Refusing to print credential export to an interactive terminal.")
		fmt.Fprintln(os.Stderr, "Pipe it to a file or another command, for example:")
		fmt.Fprintln(os.Stderr, "  launchdock auth export > launchdock-auth.json")
		fmt.Fprintln(os.Stderr, "  launchdock auth push user@server.example.com")
		os.Exit(1)
	}
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
	fmt.Printf("Pushed credential(s) to %s\n", target)
}

func ensureRemoteLaunchdock(target string) error {
	version := currentVersion()
	if version == "" || version == "dev" {
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
command -v curl >/dev/null 2>&1 || { echo "missing required command: curl" >&2; exit 1; }
command -v tar >/dev/null 2>&1 || { echo "missing required command: tar" >&2; exit 1; }
command -v install >/dev/null 2>&1 || { echo "missing required command: install" >&2; exit 1; }
ARCHIVE_PATH="$HOME/.cache/launchdock/${NAME}.tar.gz"
ASSET_NAME="${NAME}.tar.gz"
curl -fsSL "$URL" -o "$ARCHIVE_PATH"
curl -fsSL "$CHECKSUM_URL" -o "$HOME/.cache/launchdock/checksums.txt"
cd "$HOME/.cache/launchdock"
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum -c checksums.txt --ignore-missing
elif command -v shasum >/dev/null 2>&1; then
  EXPECTED=$(awk -v asset="$ASSET_NAME" '$2 == asset {print $1}' checksums.txt)
  ACTUAL=$(shasum -a 256 "$ARCHIVE_PATH" | awk '{print $1}')
  [ "$EXPECTED" = "$ACTUAL" ] || { echo "checksum mismatch" >&2; exit 1; }
elif command -v openssl >/dev/null 2>&1; then
  EXPECTED=$(awk -v asset="$ASSET_NAME" '$2 == asset {print $1}' checksums.txt)
  ACTUAL=$(openssl dgst -sha256 "$ARCHIVE_PATH" | awk '{print $NF}')
  [ "$EXPECTED" = "$ACTUAL" ] || { echo "checksum mismatch" >&2; exit 1; }
else
  echo "missing checksum tool: sha256sum, shasum, or openssl" >&2
  exit 1
fi
rm -rf "$HOME/.cache/launchdock/unpack"
mkdir -p "$HOME/.cache/launchdock/unpack"
tar -xzf "$ARCHIVE_PATH" -C "$HOME/.cache/launchdock/unpack"
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
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/repos/nghyane/launchdock/releases/latest", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "launchdock/"+currentVersion())
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := httpxpkg.APIClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
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
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "launchdock/"+currentVersion())
	resp, err := httpxpkg.APIClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("download failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
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
