#!/usr/bin/env bash
# Sync a React Native / Expo project from the local dev machine to
# yaver-test-ephemeral so the Yaver agent on the box can run Metro +
# hermesc against it. Pairs with `/dev/build-native` push to the iPhone.
#
# Usage:
#   ./scripts/sync-rn-project-to-test-box.sh <local-path> [remote-path]
#
# Defaults to syncing to /home/yaver/<basename> with ownership yaver:yaver.
# Skips ios/, android/, .expo/, and node_modules (reinstalled remotely
# with the correct linux-arm64 native bindings).
#
# Required env:
#   SSH_KEY      — private key (default ~/.ssh/hetzner_ci_ed25519)
#   REMOTE_HOST  — agent hostname or IP (default 157.180.114.179)
#   REMOTE_USER  — SSH user (default root)
#
# After sync, runs `npm install --no-audit --no-fund` remotely so Metro
# resolves modules with the right native bindings.

set -euo pipefail

SSH_KEY="${SSH_KEY:-$HOME/.ssh/hetzner_ci_ed25519}"
REMOTE_HOST="${REMOTE_HOST:-157.180.114.179}"
REMOTE_USER="${REMOTE_USER:-root}"

if [ "$#" -lt 1 ]; then
  echo "usage: $0 <local-path> [remote-path]" >&2
  exit 2
fi

local_path="$(cd "$1" && pwd)"
project_name="$(basename "$local_path")"
remote_path="${2:-/home/yaver/$project_name}"

if [ ! -f "$local_path/package.json" ]; then
  echo "error: $local_path has no package.json — not an RN project" >&2
  exit 2
fi

ssh_opts=(
  -i "$SSH_KEY"
  -o StrictHostKeyChecking=no
  -o UserKnownHostsFile=/dev/null
  -o LogLevel=ERROR
)

echo "==> Preparing $remote_path on $REMOTE_HOST"
ssh "${ssh_opts[@]}" "$REMOTE_USER@$REMOTE_HOST" "
  mkdir -p '$remote_path' &&
  chown -R yaver:yaver \"\$(dirname '$remote_path')\" 2>/dev/null || true
"

echo "==> Rsync $local_path/ -> $REMOTE_USER@$REMOTE_HOST:$remote_path/"
rsync -az --delete-after \
  -e "ssh ${ssh_opts[*]}" \
  --exclude '.git' \
  --exclude 'node_modules' \
  --exclude 'ios/Pods' \
  --exclude 'ios/build' \
  --exclude 'ios/DerivedData' \
  --exclude 'android/.gradle' \
  --exclude 'android/.cxx' \
  --exclude 'android/app/build' \
  --exclude 'android/app/.cxx' \
  --exclude 'android/build' \
  --exclude '.expo' \
  --exclude 'dist' \
  --exclude '.next' \
  --exclude '*.xcarchive' \
  --exclude '*.ipa' \
  --exclude '*.aab' \
  --exclude '*.apk' \
  --exclude '.DS_Store' \
  --exclude 'coverage' \
  "$local_path/" "$REMOTE_USER@$REMOTE_HOST:$remote_path/"

echo "==> Fixing ownership"
ssh "${ssh_opts[@]}" "$REMOTE_USER@$REMOTE_HOST" "chown -R yaver:yaver '$remote_path'"

echo "==> Running npm install on box (Linux arm64 native bindings)"
ssh "${ssh_opts[@]}" "$REMOTE_USER@$REMOTE_HOST" "
  cd '$remote_path' &&
  sudo -u yaver -H bash -lc 'npm install --no-audit --no-fund --legacy-peer-deps 2>&1 | tail -25'
"

echo "==> Verifying"
ssh "${ssh_opts[@]}" "$REMOTE_USER@$REMOTE_HOST" "
  cd '$remote_path' &&
  echo 'project root:' &&
  ls -la | head -10 &&
  echo &&
  echo 'package.json name:' &&
  node -e \"console.log(require('./package.json').name + '@' + require('./package.json').version)\" &&
  echo &&
  echo 'react-native version:' &&
  ls node_modules/react-native/package.json 2>&1 &&
  node -e \"console.log('rn ' + require('./node_modules/react-native/package.json').version)\" 2>/dev/null
"

echo "==> Done. Project ready at $REMOTE_USER@$REMOTE_HOST:$remote_path"
echo "    Trigger from local Mac:"
echo "      curl -X POST http://$REMOTE_HOST:18080/dev/start \\"
echo "        -H 'Authorization: Bearer <token>' \\"
echo "        -H 'Content-Type: application/json' \\"
echo "        -d '{\"framework\":\"expo\",\"workDir\":\"$remote_path\"}'"
