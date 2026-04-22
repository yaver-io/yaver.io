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

log "clear volatile remote paths under /opt/yaver"
ssh -i "$HCLOUD_SSH_PRIVATE_KEY_PATH" \
  -o StrictHostKeyChecking=no \
  -o UserKnownHostsFile=/dev/null \
  "root@$ip" '
    rm -rf \
      /opt/yaver/mobile/android/.gradle \
      /opt/yaver/mobile/android/app/.cxx \
      /opt/yaver/mobile/android/app/build \
      /opt/yaver/mobile/assets/models \
      /opt/yaver/scripts/screenshots/output* \
      /opt/yaver/scripts/generate-demo-videos/output \
      /opt/yaver/relay/relay-linux-amd64 \
      /opt/yaver/cli/hermesc \
      /opt/yaver/demo \
      /opt/yaver/demos \
      /opt/yaver/videos
    mkdir -p /opt/yaver
  '

log "rsync $repo_root -> root@$ip:/opt/yaver"
rsync -az --delete-after \
  -e "ssh -i $HCLOUD_SSH_PRIVATE_KEY_PATH -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null" \
  --exclude '.git' \
  --exclude 'node_modules' \
  --exclude '.next' \
  --exclude 'web/.open-next' \
  --exclude 'dist' \
  --exclude 'videos' \
  --exclude 'demo' \
  --exclude 'demos' \
  --exclude 'mobile/.expo' \
  --exclude 'mobile/ios/Pods' \
  --exclude 'mobile/android/.gradle' \
  --exclude 'mobile/android/app/.cxx' \
  --exclude 'mobile/android/app/build' \
  --exclude 'mobile/assets/models' \
  --exclude 'scripts/screenshots/output*' \
  --exclude 'scripts/generate-demo-videos/output' \
  --exclude 'relay/relay-linux-amd64' \
  --exclude 'cli/hermesc' \
  --exclude 'sdk/**/build' \
  --exclude 'desktop/installer/dist' \
  --exclude 'desktop/agent/agent' \
  --exclude 'desktop/agent/agent.test' \
  --exclude 'desktop/agent/yaver' \
  --exclude 'desktop/agent/yaver-*' \
  --exclude 'ci/.artifacts' \
  "$repo_root/" "root@$ip:/opt/yaver/"
