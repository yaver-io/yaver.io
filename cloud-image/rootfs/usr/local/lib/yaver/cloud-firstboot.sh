#!/usr/bin/env bash
# Cloud-image first-boot: provisioner-agnostic. Same script runs whether
# the box was launched from a Hetzner snapshot, GCP custom image, or AWS
# AMI built by scripts/build-cloud-image.sh.
#
# Detects provider from cloud-init metadata for telemetry (best-effort,
# never required), creates the yaver service account, wires Docker, and
# enables yaver-agent.service. The agent itself is responsible for the
# `yaver auth` UX — this script just gets it to a state where the first
# SSH session can run `yaver auth --headless`.
set -euo pipefail

STATE_DIR="/var/lib/yaver"
DONE_FILE="$STATE_DIR/.firstboot-complete"
LOG_FILE="/var/log/yaver/cloud-firstboot.log"
RELEASE_FILE="/etc/yaver/cloud-image-release.json"

mkdir -p "$STATE_DIR" "$(dirname "$LOG_FILE")"
exec > >(tee -a "$LOG_FILE") 2>&1

if [[ -f "$DONE_FILE" ]]; then
  echo "[yaver-cloud-firstboot] already completed"
  exit 0
fi

echo "[yaver-cloud-firstboot] starting at $(date -u +%FT%TZ)"

# Detect provider — best-effort, used only for the MOTD + telemetry. The
# firstboot logic doesn't branch on it; provider-specific bootstrap (e.g.
# Hetzner network drives) belongs in cloud-init or the build script.
PROVIDER="unknown"
if curl -fsS --connect-timeout 1 -H "Metadata-Flavor: Google" \
     http://metadata.google.internal/computeMetadata/v1/ >/dev/null 2>&1; then
  PROVIDER="gcp"
elif curl -fsS --connect-timeout 1 \
     http://169.254.169.254/latest/meta-data/ >/dev/null 2>&1; then
  # IMDSv1; AWS + Hetzner both expose this. Differentiate by user-data.
  if curl -fsS --connect-timeout 1 \
       http://169.254.169.254/hetzner/v1/metadata 2>/dev/null \
       | grep -q hostname; then
    PROVIDER="hetzner"
  else
    PROVIDER="aws"
  fi
fi
echo "[yaver-cloud-firstboot] detected provider=$PROVIDER"

# yaver user — non-root home for the agent + workspaces. Mirrors the Pi
# image setup; same privacy reason (homedir leak into Convex).
if ! id yaver >/dev/null 2>&1; then
  useradd --create-home --shell /bin/bash --comment "Yaver agent" yaver
fi
install -d -m 0755 -o yaver -g yaver "$STATE_DIR/workspaces" /home/yaver/Workspace
install -m 0440 /dev/stdin /etc/sudoers.d/90-yaver <<'EOF'
yaver ALL=(ALL) NOPASSWD: ALL
EOF

# Docker — already installed by cloud-init packages: block. Add yaver
# to the docker group so the agent can spawn containers without sudo.
if id yaver >/dev/null 2>&1; then
  usermod -aG docker yaver || true
fi
systemctl enable docker || true
systemctl start docker || true

# Runner sandbox prereqs (matches ci/remote/bootstrap.sh). Required for
# codex bwrap + opencode user-namespace sandboxes.
cat >/etc/sysctl.d/99-yaver-runner-sandbox.conf <<'EOF'
kernel.unprivileged_userns_clone=1
user.max_user_namespaces=1048576
EOF
if [ -f /proc/sys/kernel/apparmor_restrict_unprivileged_userns ]; then
  echo "kernel.apparmor_restrict_unprivileged_userns=0" >> /etc/sysctl.d/99-yaver-runner-sandbox.conf
fi
sysctl --system >/dev/null 2>&1 || true

if ! command -v yaver >/dev/null 2>&1; then
  echo "[yaver-cloud-firstboot] yaver binary missing at /usr/local/bin/yaver" >&2
  exit 1
fi

# Record metadata under /etc/yaver — the agent reads this so `yaver doctor`
# and `yaver status` can surface "Yaver Cloud Image vX.Y on hetzner".
install -d -m 0755 /etc/yaver
if [[ -f "$RELEASE_FILE" ]]; then
  jq --arg provider "$PROVIDER" \
     --arg bootedAt "$(date -u +%FT%TZ)" \
     '. + {detectedProvider:$provider, firstBootAt:$bootedAt}' \
     "$RELEASE_FILE" > "${RELEASE_FILE}.new" && mv "${RELEASE_FILE}.new" "$RELEASE_FILE"
fi

# Pre-authorized device-code from `yaver launch <provider>` — cloud-init
# wrote it via write_files. Move it under the yaver user's home so the
# `yaver auth --headless --background-wait` started below picks it up.
# When this file is absent the agent goes through the normal bootstrap
# pairing flow (LAN beacon + manual `yaver auth`), which is the fallback
# for users who downloaded the image directly instead of using
# `yaver launch`.
if [[ -f /etc/yaver/pending-auth.json ]]; then
  install -d -m 0700 -o yaver -g yaver /home/yaver/.yaver
  install -m 0600 -o yaver -g yaver /etc/yaver/pending-auth.json \
    /home/yaver/.yaver/pending-auth.json
  rm -f /etc/yaver/pending-auth.json
  CONVEX_URL="$(jq -r .convexUrl /home/yaver/.yaver/pending-auth.json 2>/dev/null || true)"
  if [[ -n "${CONVEX_URL:-}" && "$CONVEX_URL" != "null" ]]; then
    # Foreground the background-wait so the agent has a valid token in
    # config.json BEFORE yaver-agent.service starts. ~30s typical; bounded
    # by the device-code TTL (15 min) so this can't hang forever.
    sudo -u yaver -H /usr/local/bin/yaver auth --headless --background-wait \
      --convex-url "$CONVEX_URL" >>"$LOG_FILE" 2>&1 || true
  fi
fi

systemctl daemon-reload
systemctl enable yaver-agent.service
systemctl restart yaver-agent.service

cat >/etc/motd <<EOF

  Yaver Cloud Image — provider=$PROVIDER

  Next steps:
    yaver auth --headless         # sign in (subscription OAuth)
    yaver status                  # confirm agent is reachable

  Agent: systemctl status yaver-agent
  Logs:  journalctl -u yaver-agent -f

EOF

touch "$DONE_FILE"
echo "[yaver-cloud-firstboot] complete at $(date -u +%FT%TZ)"
