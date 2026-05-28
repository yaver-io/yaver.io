#!/usr/bin/env bash
#
# yaver.io/install — single-command Yaver installer.
#
# Usage:
#   curl -fsSL https://yaver.io/install | bash
#   curl -fsSL https://yaver.io/install | bash -s -- --help
#
# Idempotent. Detects:
#   * Platform (macOS / Linux / WSL2)
#   * Architecture (amd64 / arm64)
#   * Existing Node.js (>=18 required by yaver-cli; uses the system one
#     if present, else installs Node 22 via NodeSource on Debian/Ubuntu
#     or homebrew on macOS, else falls back to a per-user nvm install).
#   * Whether yaver-cli is already installed — if so, runs `npm install
#     -g yaver-cli@latest` to upgrade.
#
# What this is NOT: a binary-bundle installer. yaver-cli is shipped via
# npm per CLAUDE.md ("npm install -g yaver-cli is the ONLY supported
# install path"). This script just removes the "install Node first"
# friction for users who don't have it.

set -euo pipefail

YAVER_INSTALL_DEBUG="${YAVER_INSTALL_DEBUG:-0}"
NODE_MAJOR_REQUIRED=18
NODE_MAJOR_PREFERRED=22

if [ "$YAVER_INSTALL_DEBUG" = "1" ]; then set -x; fi

# ─── ANSI niceties ────────────────────────────────────────────────────
if [ -t 1 ]; then
  BOLD=$'\033[1m'; DIM=$'\033[2m'; GREEN=$'\033[32m'; YELLOW=$'\033[33m'
  RED=$'\033[31m'; CYAN=$'\033[36m'; RESET=$'\033[0m'
else
  BOLD=""; DIM=""; GREEN=""; YELLOW=""; RED=""; CYAN=""; RESET=""
fi

say()  { printf '%s\n' "$*"; }
info() { printf '%s▶%s %s\n' "$CYAN" "$RESET" "$*"; }
ok()   { printf '%s✓%s %s\n' "$GREEN" "$RESET" "$*"; }
warn() { printf '%s⚠%s %s\n' "$YELLOW" "$RESET" "$*"; }
die()  { printf '%s✗ %s%s\n' "$RED" "$*" "$RESET" >&2; exit 1; }

# ─── Platform detection ───────────────────────────────────────────────
OS="$(uname -s)"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) die "Unsupported architecture: $ARCH (yaver-cli supports amd64 + arm64)" ;;
esac

case "$OS" in
  Darwin) PLATFORM="darwin" ;;
  Linux)
    if grep -qi microsoft /proc/version 2>/dev/null; then
      PLATFORM="wsl2"
    else
      PLATFORM="linux"
    fi
    ;;
  *)
    die "Unsupported OS: $OS. yaver-cli supports macOS, Linux, and WSL2."
    ;;
esac

info "Detected: $PLATFORM/$ARCH"

# ─── Node check ───────────────────────────────────────────────────────
have_node_recent() {
  command -v node >/dev/null 2>&1 || return 1
  local major
  major="$(node -p 'process.versions.node.split(".")[0]' 2>/dev/null || echo 0)"
  [ "$major" -ge "$NODE_MAJOR_REQUIRED" ]
}

install_node_macos() {
  if command -v brew >/dev/null 2>&1; then
    info "Installing Node $NODE_MAJOR_PREFERRED via Homebrew"
    brew install node@$NODE_MAJOR_PREFERRED >/dev/null
    # node@N keg is keg-only; symlink the binaries into PATH
    brew link --overwrite --force node@$NODE_MAJOR_PREFERRED >/dev/null 2>&1 || true
  else
    warn "Homebrew not found. Installing Node $NODE_MAJOR_PREFERRED via nvm into ~/.nvm"
    install_node_via_nvm
  fi
}

install_node_debian_family() {
  # Need sudo for apt; if running as root we skip the prefix.
  local sudo=""
  if [ "$(id -u)" -ne 0 ]; then
    if command -v sudo >/dev/null 2>&1; then sudo="sudo"; else
      warn "Not root and sudo missing — falling back to nvm"
      install_node_via_nvm; return
    fi
  fi
  info "Installing Node $NODE_MAJOR_PREFERRED via NodeSource"
  curl -fsSL "https://deb.nodesource.com/setup_${NODE_MAJOR_PREFERRED}.x" | $sudo bash - >/dev/null
  $sudo apt-get install -y -qq nodejs >/dev/null
}

install_node_redhat_family() {
  local sudo=""
  if [ "$(id -u)" -ne 0 ]; then
    if command -v sudo >/dev/null 2>&1; then sudo="sudo"; else
      install_node_via_nvm; return
    fi
  fi
  info "Installing Node $NODE_MAJOR_PREFERRED via NodeSource"
  curl -fsSL "https://rpm.nodesource.com/setup_${NODE_MAJOR_PREFERRED}.x" | $sudo bash - >/dev/null
  $sudo dnf install -y -q nodejs >/dev/null 2>&1 || $sudo yum install -y -q nodejs >/dev/null
}

install_node_via_nvm() {
  # Per-user fallback. Touches only ~/.nvm and ~/.bashrc-equivalent.
  info "Installing nvm + Node $NODE_MAJOR_PREFERRED under ~/.nvm"
  export NVM_DIR="$HOME/.nvm"
  curl -fsSL "https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.1/install.sh" | bash >/dev/null
  # shellcheck disable=SC1091
  . "$NVM_DIR/nvm.sh"
  nvm install "$NODE_MAJOR_PREFERRED" >/dev/null
  nvm alias default "$NODE_MAJOR_PREFERRED" >/dev/null
  nvm use default >/dev/null
  warn "nvm installed. Re-open your shell or 'source ~/.nvm/nvm.sh' so 'node' works in new sessions."
}

if have_node_recent; then
  ok "Node $(node --version) detected — using system Node"
else
  case "$PLATFORM" in
    darwin)
      install_node_macos
      ;;
    linux|wsl2)
      if command -v apt-get >/dev/null 2>&1; then
        install_node_debian_family
      elif command -v dnf >/dev/null 2>&1 || command -v yum >/dev/null 2>&1; then
        install_node_redhat_family
      else
        install_node_via_nvm
      fi
      ;;
  esac
  ok "Node $(node --version) installed"
fi

# ─── yaver-cli install / upgrade ──────────────────────────────────────
if command -v yaver >/dev/null 2>&1; then
  info "yaver already installed: $(yaver --version 2>/dev/null | head -1) — upgrading to latest"
fi

# `npm install -g` writes under the npm prefix. On macOS with Homebrew
# node + Linux NodeSource that's /usr/local or /opt/homebrew (writable
# by the user). For system Node installs we may need sudo.
NPM_PREFIX="$(npm config get prefix 2>/dev/null || echo /usr/local)"
if [ -w "$NPM_PREFIX" ] || [ "$(id -u)" -eq 0 ]; then
  npm install -g yaver-cli@latest >/dev/null
else
  warn "npm prefix $NPM_PREFIX requires sudo for global install"
  sudo npm install -g yaver-cli@latest >/dev/null
fi

ok "yaver-cli $(yaver --version 2>/dev/null | head -1) installed"

# ─── Next steps ───────────────────────────────────────────────────────
say
say "${BOLD}Next steps:${RESET}"
say
say "  ${CYAN}yaver auth${RESET}                    # one-time OAuth sign-in"
say "  ${CYAN}yaver launch hetzner${RESET}          # spin up a Yaver-ready box (also: aws, gcp)"
say "  ${CYAN}yaver launch ssh user@host${RESET}    # adopt an existing Linux box"
say
say "${DIM}Docs: https://yaver.io/docs${RESET}"
