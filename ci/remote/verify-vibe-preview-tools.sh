#!/usr/bin/env bash
# Runs on the remote ephemeral box. Rebuilds the yaver agent from the
# latest /opt/yaver source, installs it, runs `yaver install
# vibe-preview` (best-effort tool provisioning), then reports which
# tools are present so the caller can see whether the install path
# actually delivers what the feature needs.
#
# Why bother — the user's concern: "make sure that go agent would be
# installed/upgraded will have it … so we wont have a production-alike
# errors for later on." This script is the proof: we're testing the
# real upgrade path, not a hand-curated bootstrap.
set -euo pipefail

REPO=/opt/yaver
mkdir -p /var/log/yaver-ci
LOG=/var/log/yaver-ci/verify-vibe-preview.log
exec > >(tee -a "$LOG") 2>&1

banner() { printf '\n========== %s ==========\n' "$*"; }

banner "before"
yaver --version 2>&1 | head -1 || echo "yaver: not installed"

banner "rebuild + install yaver from source"
cd "$REPO/desktop/agent"
export PATH=/usr/local/go/bin:$PATH
go version
go build -o /tmp/yaver-new .
install -m 0755 /tmp/yaver-new /usr/local/bin/yaver
rm -f /tmp/yaver-new

banner "after"
yaver --version 2>&1 | head -1

banner "yaver install vibe-preview"
# Best-effort: failures are logged but don't abort. The whole point of
# this run is to surface what the install path can and can't deliver.
yaver install vibe-preview 2>&1 || echo "(install vibe-preview returned non-zero — see log above)"

banner "tool availability report"
report() {
  local name="$1"; shift
  for cand in "$@"; do
    if command -v "$cand" >/dev/null 2>&1; then
      printf '  ✓ %-12s -> %s\n' "$name" "$(command -v "$cand")"
      return 0
    fi
  done
  printf '  ✗ %-12s missing\n' "$name"
  return 1
}

missing=0
report chromium chromium chromium-browser google-chrome google-chrome-stable || missing=$((missing+1))
report ffmpeg   ffmpeg                                                       || missing=$((missing+1))
report maestro  maestro                                                      || missing=$((missing+1))
report appium   appium                                                       || missing=$((missing+1))
report adb      adb                                                          || missing=$((missing+1))

banner "summary"
if [ "$missing" -eq 0 ]; then
  echo "All vibe-preview tools present. Install path verified."
else
  echo "$missing tool(s) missing — see report above."
  echo "(Some are platform-specific: appium needs npm; android-sdk on"
  echo " Linux ships only the platform-tools subset; sim-ios needs Xcode."
  echo " The user-facing meta-target should still cover at least the apt"
  echo " path on Ubuntu — investigate any apt-installable misses above.)"
fi
exit 0
