#!/usr/bin/env bash
set -euo pipefail

export DEBIAN_FRONTEND=noninteractive
export PATH="/root/.local/bin:/usr/local/bin:/usr/bin:/bin:$PATH"

LOG_FILE="/var/log/yaver-pi-auto-update.log"
mkdir -p "$(dirname "$LOG_FILE")"
exec >>"$LOG_FILE" 2>&1

echo "[yaver-pi-auto-update] started $(date -u +"%Y-%m-%dT%H:%M:%SZ")"

apt-get update
apt-get -y upgrade
apt-get -y autoremove

python3 -m pip install --user --upgrade aider-chat pre-commit pytest ruff || true
npm install -g opencode-ai vitest eslint prettier || true

if command -v yaver >/dev/null 2>&1; then
  yaver config set auto-update true || true
  yaver update || true
fi

echo "[yaver-pi-auto-update] completed $(date -u +"%Y-%m-%dT%H:%M:%SZ")"
