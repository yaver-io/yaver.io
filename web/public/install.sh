#!/bin/sh
# Yaver CLI installer — https://yaver.io
# Usage: curl -fsSL https://yaver.io/install.sh | sh
set -e

REPO="kivanccakmak/yaver.io"

# Detect OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$OS" in
  darwin) OS="darwin" ;;
  linux)  OS="linux" ;;
  *)      echo "Unsupported OS: $OS"; exit 1 ;;
esac

case "$ARCH" in
  x86_64|amd64)  ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *)             echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

if [ -z "${INSTALL_DIR:-}" ]; then
  if [ "$OS" = "linux" ]; then
    INSTALL_DIR="$HOME/.local/bin"
  else
    INSTALL_DIR="/usr/local/bin"
  fi
fi

echo "Installing yaver for ${OS}/${ARCH}..."

latest_cli_release() {
  curl -fsSL "https://api.github.com/repos/${REPO}/releases?per_page=100" |
    grep -o '"tag_name": *"v[0-9][^"]*"' |
    head -n 1 |
    sed 's/.*"\(v[^"]*\)"/\1/'
}

# Get latest release tag
LATEST=$(latest_cli_release)
if [ -z "$LATEST" ]; then
  echo "Error: could not determine latest CLI version"
  exit 1
fi
echo "Latest version: ${LATEST}"

ARCHIVE="yaver-${LATEST}-${OS}-${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${LATEST}/${ARCHIVE}"
echo "Downloading ${URL}..."

TMPDIR=$(mktemp -d)
ARCHIVE_PATH="$TMPDIR/$ARCHIVE"
curl -fsSL "$URL" -o "$ARCHIVE_PATH"
tar -xzf "$ARCHIVE_PATH" -C "$TMPDIR"
EXTRACTED_BIN="$TMPDIR/yaver-${OS}-${ARCH}"
if [ ! -f "$EXTRACTED_BIN" ]; then
  echo "Error: extracted archive did not contain yaver-${OS}-${ARCH}"
  exit 1
fi
chmod +x "$EXTRACTED_BIN"

mkdir -p "$INSTALL_DIR"

# Install
if [ -w "$INSTALL_DIR" ]; then
  mv "$EXTRACTED_BIN" "${INSTALL_DIR}/yaver"
else
  echo "Installing to ${INSTALL_DIR} (requires sudo)..."
  sudo mkdir -p "$INSTALL_DIR"
  sudo mv "$EXTRACTED_BIN" "${INSTALL_DIR}/yaver"
fi

rm -rf "$TMPDIR"

echo ""
echo "yaver installed to ${INSTALL_DIR}/yaver"
echo ""
"${INSTALL_DIR}/yaver" version
echo ""
echo "Get started:"
echo "  yaver auth    Sign in & start the agent"

# Auto-wire PATH for non-standard INSTALL_DIR so `yaver` works in the
# NEXT shell without the user having to read the hint below.
ensure_path_in_rc() {
  rc="$1"
  [ -f "$rc" ] || return 0
  grep -Fq "# yaver-cli PATH" "$rc" 2>/dev/null && return 0
  {
    echo ""
    echo "# yaver-cli PATH (added by https://yaver.io/install.sh)"
    echo "case \":\$PATH:\" in *\":${INSTALL_DIR}:\"*) ;; *) export PATH=\"${INSTALL_DIR}:\$PATH\" ;; esac"
  } >> "$rc"
  echo "Added ${INSTALL_DIR} to PATH in ${rc}"
}

if ! printf "%s" ":$PATH:" | grep -q ":$INSTALL_DIR:"; then
  echo ""
  for rc in "$HOME/.bashrc" "$HOME/.zshrc" "$HOME/.profile"; do
    ensure_path_in_rc "$rc"
  done
  echo "Restart your shell (or 'exec \$SHELL -l') so 'yaver' is on PATH."
fi

# `yaver push` (React Native to device) shells out to `npm exec --package
# yaver-cli@<version>` to pull the hermes bundler on first use. Warn if
# Node/npm is missing so users know they'll need it for push-to-device.
if ! command -v npm >/dev/null 2>&1; then
  echo ""
  echo "Note: yaver push (React Native to device) requires Node.js/npm."
  echo "      The agent, relay, and AI-runner features work without it."
  echo "      Install Node: https://nodejs.org or your package manager."
fi
