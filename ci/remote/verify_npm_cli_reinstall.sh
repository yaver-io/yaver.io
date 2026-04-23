#!/usr/bin/env bash
# npm package smoke for the CLI bundle:
#   - npm pack the current cli/ tree
#   - uninstall any previous global yaver-cli install
#   - install the packed tarball globally
#   - verify yaver/yaver-push/yaver-mcp resolve
#   - reinstall the same tarball to catch postinstall / preuninstall drift
set -euo pipefail

REPO="${REPO:-/opt/yaver}"
LOG_DIR="${NPM_CLI_SMOKE_LOG_DIR:-/var/log/yaver-ci}"
mkdir -p "$LOG_DIR"
LOG="$LOG_DIR/npm-cli-reinstall.log"
exec > >(tee -a "$LOG") 2>&1

banner() { printf '\n========== %s ==========\n' "$*"; }

cd "$REPO/cli"

banner "toolchain"
node --version
npm --version

banner "pack cli"
PKG_FILE="$(npm pack | tail -n 1)"
[ -f "$PKG_FILE" ] || { echo "npm pack did not produce a tarball"; exit 1; }
echo "tarball: $PKG_FILE"

banner "clean previous global install"
npm uninstall -g yaver-cli yaver-mobile-headless >/dev/null 2>&1 || true
hash -r || true

banner "install packed cli globally"
npm install -g "./${PKG_FILE}"
hash -r || true
command -v yaver
command -v yaver-push
command -v yaver-mcp
yaver --version

banner "reinstall packed cli globally"
npm install -g "./${PKG_FILE}"
hash -r || true
yaver --version
yaver --help 2>&1 | head -20

banner "npm cli reinstall smoke passed"
echo "package=${PKG_FILE}"
