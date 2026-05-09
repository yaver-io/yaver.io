#!/usr/bin/env bash
# One-shot migration: move an existing yaver remote box from running everything
# as root to running it as a non-root `yaver` user.
#
# Run as root (or with sudo) on the target box. Idempotent — safe to rerun.
#
# What it does:
#   1. Creates `yaver` system user with /home/yaver (if missing)
#   2. Moves /root/Workspace -> /home/yaver/Workspace (rsync + chown)
#   3. Moves runner configs (~/.codex, ~/.opencode, ~/.aider, ~/.config/gh, ~/.config/glab) to yaver
#   4. Reinstalls /etc/systemd/system/yaver-agent.service with User=yaver
#   5. Restarts the agent
#
# Does NOT touch: /root/.ssh, /root/.bash_history (host-level admin stuff stays).
set -euo pipefail

if [ "$(id -u)" != "0" ]; then
  echo "must run as root" >&2
  exit 1
fi

log() { printf '\n=== %s ===\n' "$*"; }

log "ensure yaver user"
if ! id yaver >/dev/null 2>&1; then
  useradd --create-home --shell /bin/bash --comment "Yaver agent" yaver
fi
install -d -m 0755 -o yaver -g yaver /home/yaver/Workspace

log "passwordless sudo for yaver"
install -m 0440 /dev/stdin /etc/sudoers.d/90-yaver <<'EOF'
yaver ALL=(ALL) NOPASSWD: ALL
EOF

log "docker group membership"
if getent group docker >/dev/null 2>&1; then
  usermod -aG docker yaver || true
fi

log "migrate /root/Workspace -> /home/yaver/Workspace"
if [ -d /root/Workspace ] && [ ! -L /root/Workspace ]; then
  rsync -aHAX --remove-source-files /root/Workspace/ /home/yaver/Workspace/
  find /root/Workspace -depth -type d -empty -delete || true
  chown -R yaver:yaver /home/yaver/Workspace
fi
# Anything left at /root/<repo> at the top level (e.g. /root/carrotbet) — best
# effort, only move dirs that look like git repos.
for d in /root/*/; do
  [ -d "$d/.git" ] || continue
  base="$(basename "$d")"
  case "$base" in Workspace|workspace|snap|.cache|.config|.local) continue;; esac
  log "moving repo $d -> /home/yaver/Workspace/$base"
  rsync -aHAX --remove-source-files "$d" "/home/yaver/Workspace/$base/"
  find "$d" -depth -type d -empty -delete || true
done
chown -R yaver:yaver /home/yaver/Workspace

log "migrate runner configs"
for cfg in .codex .opencode .aider .config/gh .config/glab .gitconfig .ssh; do
  src="/root/$cfg"
  dst="/home/yaver/$cfg"
  [ -e "$src" ] || continue
  if [ -e "$dst" ]; then
    echo "  skip $cfg — already at $dst"
    continue
  fi
  install -d -m 0755 -o yaver -g yaver "$(dirname "$dst")"
  rsync -aHAX "$src/" "$dst/" 2>/dev/null || cp -a "$src" "$dst"
  chown -R yaver:yaver "$dst"
  echo "  moved $cfg"
done
# .ssh perms are picky
if [ -d /home/yaver/.ssh ]; then
  chmod 700 /home/yaver/.ssh
  find /home/yaver/.ssh -type f -exec chmod 600 {} \;
fi

log "reinstall yaver-agent.service as User=yaver"
# Find the existing unit (might be under /etc/systemd/system or /lib/systemd/system).
unit_path=""
for p in /etc/systemd/system/yaver-agent.service /lib/systemd/system/yaver-agent.service; do
  [ -f "$p" ] && unit_path="$p" && break
done
if [ -z "$unit_path" ]; then
  unit_path=/etc/systemd/system/yaver-agent.service
  cat >"$unit_path" <<'EOF'
[Unit]
Description=Yaver Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=yaver
Group=yaver
Environment=HOME=/home/yaver
ExecStart=/usr/local/bin/yaver serve --port 18080 --debug
Restart=always
RestartSec=5
WorkingDirectory=/home/yaver
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF
else
  # Patch User=/Group=/HOME=. Don't rewrite ExecStart in case the user
  # customized flags (e.g. --multi-user, --work-dir).
  sed -i \
    -e 's|^User=.*|User=yaver|' \
    -e 's|^Environment=HOME=.*|Environment=HOME=/home/yaver|' \
    "$unit_path"
  grep -q '^Group=' "$unit_path" || sed -i '/^User=yaver/a Group=yaver' "$unit_path"
  grep -q '^Environment=HOME=' "$unit_path" || sed -i '/^Group=yaver/a Environment=HOME=/home/yaver' "$unit_path"
  # If WorkingDirectory points at /root or /var/lib/yaver, leave it alone — both
  # remain readable by yaver after we chown below.
fi

# /var/lib/yaver may exist for the watchdog beacon + workspace dir; chown so
# the agent can write under it.
[ -d /var/lib/yaver ] && chown -R yaver:yaver /var/lib/yaver || true

systemctl daemon-reload
systemctl restart yaver-agent.service || true
systemctl status --no-pager yaver-agent.service | head -15 || true

log "done"
echo "Verify with:  sudo -iu yaver yaver status"
echo "Workspace is now at: /home/yaver/Workspace"
