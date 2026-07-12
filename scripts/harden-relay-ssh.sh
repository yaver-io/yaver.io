#!/usr/bin/env bash
# harden-relay-ssh.sh — lock SSH on the relay box down to key-only, so nobody
# can connect without a private key you control. Optionally restrict the SSH
# port at the network layer (Hetzner Cloud Firewall) to specific source IPs.
#
# What it does on the box (idempotent, validated before restart):
#   - PasswordAuthentication no      (no password logins — brute force is moot)
#   - KbdInteractiveAuthentication no / ChallengeResponseAuthentication no
#   - PubkeyAuthentication yes
#   - PermitRootLogin prohibit-password  (root only via key, never password)
#   - runs `sshd -t` to VALIDATE the config, and only restarts if valid
#     (a bad sshd_config + restart = lockout — this guards against that)
#   - prints the current authorized_keys fingerprints so you can audit/prune
#     (it does NOT auto-delete keys — you decide which stay: yours + the CI
#     deploy key that relay-deploy-binary.yml uses)
#
# Usage:
#   scripts/harden-relay-ssh.sh --host <RELAY_BOX_IP> [options]
#
# Options:
#   --host <ip|dns>     Relay box (default: $RELAY_SSH_HOST). Required.
#   --ssh-user <user>   SSH user (default: root).
#   --ssh-key <path>    SSH private key (default: your agent/config).
#   --allow-ip <cidr>   Also apply a Hetzner Cloud Firewall allowing SSH (22)
#                       ONLY from this CIDR (repeatable). Needs $HCLOUD_TOKEN and
#                       --server-id. WARNING: this blocks CI deploys unless you
#                       also allow GitHub Actions egress — prefer key-only +
#                       leaving 22 reachable, or a bastion, if CI must SSH.
#   --server-id <id>    Hetzner server id (for --allow-ip firewall).
#   --yes               Don't prompt.
#
# Real-world note: the deploy pipeline (relay-deploy-binary.yml) SSHes in with
# HCLOUD_SSH_PRIVATE_KEY, so "only me" in practice = "only MY key + the CI
# deploy key, key-only, no passwords." Keeping those two keys and disabling
# passwords is the enforceable version of "nobody else can connect."
set -euo pipefail

HOST="${RELAY_SSH_HOST:-}"
SSH_USER="root"
SSH_KEY=""
ASSUME_YES=0
ALLOW_IPS=()
SERVER_ID=""

while [ $# -gt 0 ]; do
  case "$1" in
    --host)      HOST="$2";      shift 2 ;;
    --ssh-user)  SSH_USER="$2";  shift 2 ;;
    --ssh-key)   SSH_KEY="$2";   shift 2 ;;
    --allow-ip)  ALLOW_IPS+=("$2"); shift 2 ;;
    --server-id) SERVER_ID="$2"; shift 2 ;;
    --yes)       ASSUME_YES=1;   shift ;;
    -h|--help)   grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

[ -n "$HOST" ] || { echo "ERROR: --host (or \$RELAY_SSH_HOST) required" >&2; exit 2; }

echo "Hardening SSH on $SSH_USER@$HOST (key-only, no passwords)."
if [ "$ASSUME_YES" != 1 ]; then
  read -r -p "Proceed? [y/N] " ok; case "$ok" in [yY]*) ;; *) echo "aborted"; exit 0 ;; esac
fi

SSH_OPTS=(-o StrictHostKeyChecking=accept-new)
[ -n "$SSH_KEY" ] && SSH_OPTS+=(-i "$SSH_KEY")

ssh "${SSH_OPTS[@]}" "$SSH_USER@$HOST" bash -s <<'REMOTE'
set -euo pipefail
CFG=/etc/ssh/sshd_config
DROPIN=/etc/ssh/sshd_config.d/00-yaver-harden.conf
mkdir -p /etc/ssh/sshd_config.d

# Prefer a drop-in (survives package upgrades to the main config).
cat > "$DROPIN" <<'CONF'
# Managed by scripts/harden-relay-ssh.sh — key-only SSH.
PasswordAuthentication no
KbdInteractiveAuthentication no
ChallengeResponseAuthentication no
PubkeyAuthentication yes
PermitRootLogin prohibit-password
CONF

# If the distro's sshd ignores the drop-in dir, fall back to editing the main
# file (idempotent).
if ! grep -q 'Include /etc/ssh/sshd_config.d/\*' "$CFG" 2>/dev/null; then
  echo "sshd_config has no Include for the drop-in dir; patching main config."
  for kv in "PasswordAuthentication no" "KbdInteractiveAuthentication no" \
            "ChallengeResponseAuthentication no" "PubkeyAuthentication yes" \
            "PermitRootLogin prohibit-password"; do
    key="${kv%% *}"
    if grep -qiE "^\s*#?\s*${key}\b" "$CFG"; then
      sed -i "s|^\s*#\?\s*${key}\b.*|${kv}|I" "$CFG"
    else
      echo "$kv" >> "$CFG"
    fi
  done
fi

# VALIDATE before restart — a bad config + restart would lock us out.
if ! sshd -t; then
  echo "ERROR: sshd config invalid — NOT restarting (no lockout). Reverting drop-in."
  rm -f "$DROPIN"
  exit 1
fi

systemctl reload ssh 2>/dev/null || systemctl reload sshd 2>/dev/null || service ssh reload || true
echo "✓ sshd hardened + reloaded (key-only, no passwords, root key-only)."

echo "=== authorized_keys on this box (AUDIT — keep only yours + the CI deploy key) ==="
for f in /root/.ssh/authorized_keys /home/*/.ssh/authorized_keys; do
  [ -f "$f" ] || continue
  echo "--- $f ---"
  ssh-keygen -lf "$f" 2>/dev/null || cat "$f"
done
REMOTE

echo "✓ SSH hardened. Passwords are off; only holders of an authorized private key can connect."

# --- optional network-layer restriction via Hetzner Cloud Firewall ---------
if [ "${#ALLOW_IPS[@]}" -gt 0 ]; then
  : "${HCLOUD_TOKEN:?--allow-ip needs HCLOUD_TOKEN in the environment}"
  [ -n "$SERVER_ID" ] || { echo "ERROR: --allow-ip needs --server-id" >&2; exit 2; }
  echo "→ applying Hetzner Cloud Firewall: SSH(22) only from ${ALLOW_IPS[*]}"
  echo "  WARNING: this will block CI deploys unless a rule also allows their egress."
  src_json=$(printf '"%s",' "${ALLOW_IPS[@]}"); src_json="[${src_json%,}]"
  rules=$(printf '[{"direction":"in","protocol":"tcp","port":"22","source_ips":%s}]' "$src_json")
  fw=$(curl -s -X POST "https://api.hetzner.cloud/v1/firewalls" \
        -H "Authorization: Bearer $HCLOUD_TOKEN" -H "Content-Type: application/json" \
        -d "{\"name\":\"yaver-relay-ssh-lock\",\"rules\":$rules}")
  fwid=$(printf '%s' "$fw" | grep -oE '"id":[0-9]+' | head -1 | cut -d: -f2)
  [ -n "$fwid" ] || { echo "firewall create failed: $fw" >&2; exit 1; }
  curl -s -X POST "https://api.hetzner.cloud/v1/firewalls/${fwid}/actions/apply_to_resources" \
    -H "Authorization: Bearer $HCLOUD_TOKEN" -H "Content-Type: application/json" \
    -d "{\"apply_to\":[{\"type\":\"server\",\"server\":{\"id\":${SERVER_ID}}}]}" >/dev/null
  echo "✓ Hetzner firewall ${fwid} applied — SSH restricted to ${ALLOW_IPS[*]}."
fi

echo
echo "Done. Nobody can SSH in without an authorized private key."
echo "  • Audit the authorized_keys output above; keep only your key + the CI deploy key."
echo "  • Keep a second logged-in session open until you've confirmed a fresh key login works."
