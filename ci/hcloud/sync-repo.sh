#!/usr/bin/env bash
# Rsync the checked-out repo to the remote box under /opt/yaver.
# Skips heavy build/test output dirs to keep the copy fast.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=/dev/null
source "$here/common.sh"

: "${HCLOUD_SSH_PRIVATE_KEY_PATH:?set HCLOUD_SSH_PRIVATE_KEY_PATH to the CI key}"

ip="$(cat "$CI_ARTIFACTS/server-ip")"
repo_root="${REPO_ROOT:-$(pwd)}"

log "rsync $repo_root -> root@$ip:/opt/yaver"
rsync -az --delete \
  -e "ssh -i $HCLOUD_SSH_PRIVATE_KEY_PATH -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null" \
  --exclude '.git' \
  --exclude 'node_modules' \
  --exclude '.next' \
  --exclude 'web/.open-next' \
  --exclude 'mobile/ios/Pods' \
  --exclude 'mobile/android/.gradle' \
  --exclude 'mobile/android/app/build' \
  --exclude 'desktop/agent/yaver' \
  --exclude 'desktop/agent/yaver-*' \
  --exclude 'ci/.artifacts' \
  "$repo_root/" "root@$ip:/opt/yaver/"
