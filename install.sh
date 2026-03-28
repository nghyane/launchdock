#!/bin/sh
set -eu

REPO="nghyane/launchdock"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
LAUNCHDOCK_VERSION="${LAUNCHDOCK_VERSION:-}"

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf 'Missing required command: %s\n' "$1" >&2
    exit 1
  fi
}

need_cmd curl
need_cmd tar

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *)
    printf 'Unsupported architecture: %s\n' "$ARCH" >&2
    exit 1
    ;;
esac

case "$OS" in
  linux|darwin) ;;
  *)
    printf 'Unsupported OS for install.sh: %s\n' "$OS" >&2
    exit 1
    ;;
esac

if [ -z "$LAUNCHDOCK_VERSION" ]; then
  need_cmd sed
  LAUNCHDOCK_VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)
fi

if [ -z "$LAUNCHDOCK_VERSION" ]; then
  printf 'Could not determine latest release version\n' >&2
  exit 1
fi

ASSET="launchdock-${LAUNCHDOCK_VERSION}-${OS}-${ARCH}.tar.gz"
CHECKSUMS="checksums-${OS}-${ARCH}.txt"
BASE_URL="https://github.com/$REPO/releases/download/$LAUNCHDOCK_VERSION"

TMP_DIR=$(mktemp -d)
cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT INT TERM

ARCHIVE_PATH="$TMP_DIR/$ASSET"
CHECKSUM_PATH="$TMP_DIR/$CHECKSUMS"

printf 'Installing launchdock %s for %s/%s\n' "$LAUNCHDOCK_VERSION" "$OS" "$ARCH"
curl -fsSL "$BASE_URL/$ASSET" -o "$ARCHIVE_PATH"
curl -fsSL "$BASE_URL/$CHECKSUMS" -o "$CHECKSUM_PATH"

EXPECTED=$(awk -v asset="$ASSET" '$2 == asset {print $1}' "$CHECKSUM_PATH")
if [ -z "$EXPECTED" ]; then
  printf 'Checksum not found for %s\n' "$ASSET" >&2
  exit 1
fi

ACTUAL=""
if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL=$(sha256sum "$ARCHIVE_PATH" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  ACTUAL=$(shasum -a 256 "$ARCHIVE_PATH" | awk '{print $1}')
elif command -v openssl >/dev/null 2>&1; then
  ACTUAL=$(openssl dgst -sha256 "$ARCHIVE_PATH" | awk '{print $NF}')
else
  printf 'Need sha256sum, shasum, or openssl for checksum verification\n' >&2
  exit 1
fi

if [ "$EXPECTED" != "$ACTUAL" ]; then
  printf 'Checksum mismatch for %s\n' "$ASSET" >&2
  exit 1
fi

mkdir -p "$TMP_DIR/unpack" "$INSTALL_DIR"
tar -xzf "$ARCHIVE_PATH" -C "$TMP_DIR/unpack"
install "$TMP_DIR/unpack/launchdock" "$INSTALL_DIR/launchdock"

printf 'Installed to %s/launchdock\n' "$INSTALL_DIR"
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    printf 'Add this to your shell profile if needed:\n'
    printf '  export PATH="%s:$PATH"\n' "$INSTALL_DIR"
    ;;
esac

"$INSTALL_DIR/launchdock" version || true
