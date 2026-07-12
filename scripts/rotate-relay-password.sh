#!/usr/bin/env bash
# rotate-relay-password.sh — safely rotate the SHARED relay password on the
# public relay box AND the GitHub secret, with a health-checked auto-rollback so
# a bad rotation can't leave the relay unable to auth.
#
# Why this exists: the shared relay password gates the /d/ proxy + admin paths.
# Rotating it by hand risks (a) leaking it to shell history / `ps`, and (b)
# bricking the relay if the systemd unit ends up malformed. This script reads/
# generates the value without echoing it, updates the BOX FIRST (backup unit →
# swap → restart → probe /health → ROLLBACK if unhealthy), and only writes the
# GitHub secret once the box is confirmed healthy.
#
# Usage:
#   scripts/rotate-relay-password.sh --host <RELAY_BOX_IP> [options]
#
# Options:
#   --host <ip|dns>     Relay box (default: $RELAY_SSH_HOST). Required.
#   --password <value>  Use this value instead of generating one (else a fresh
#                       256-bit hex password is generated). ALPHANUMERIC only.
#   --ssh-user <user>   SSH user (default: root).
#   --ssh-key <path>    SSH private key (default: your ssh-agent / config).
#   --repo <owner/repo> GitHub repo for the secret (default: kivanccakmak/yaver.io).
#   --skip-gh           Don't touch the GitHub secret (box only).
#   --yes               Don't prompt for confirmation.
#
# Prereqs: run from repo root; SSH access to the box; `gh` authed (unless
# --skip-gh). NOTE: rotating a SHARED secret breaks any client still using the
# OLD shared password until it re-fetches. The device-signature migration
# retires the shared password entirely — that's the real fix.
set -euo pipefail

HOST="${RELAY_SSH_HOST:-}"
NEWVAL=""
SSH_USER="root"
SSH_KEY=""
REPO="kivanccakmak/yaver.io"
SKIP_GH=0
ASSUME_YES=0
HTTP_PORT="${RELAY_HTTP_PORT:-8080}"

while [ $# -gt 0 ]; do
  case "$1" in
    --host)      HOST="$2";     shift 2 ;;
    --password)  NEWVAL="$2";   shift 2 ;;
    --ssh-user)  SSH_USER="$2"; shift 2 ;;
    --ssh-key)   SSH_KEY="$2";  shift 2 ;;
    --repo)      REPO="$2";     shift 2 ;;
    --http-port) HTTP_PORT="$2"; shift 2 ;;
    --skip-gh)   SKIP_GH=1;     shift ;;
    --yes)       ASSUME_YES=1;  shift ;;
    -h|--help)   grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

[ -n "$HOST" ] || { echo "ERROR: --host (or \$RELAY_SSH_HOST) required" >&2; exit 2; }

# Generate a fresh 256-bit hex password if none supplied. Hex is sed/shell-safe.
if [ -z "$NEWVAL" ]; then
  NEWVAL="$(openssl rand -hex 32)"
  GENERATED=1
else
  GENERATED=0
  case "$NEWVAL" in
    *[!A-Za-z0-9]*) echo "ERROR: --password must be alphanumeric (safe in the systemd unit)." >&2; exit 2 ;;
  esac
fi

masked="${NEWVAL:0:4}…${NEWVAL: -4} (${#NEWVAL} chars)"
echo "Rotating relay password on $SSH_USER@$HOST"
[ "$GENERATED" = 1 ] && echo "  generated a new 256-bit password: $masked" || echo "  using supplied password: $masked"
[ "$SKIP_GH" = 1 ] && echo "  GitHub secret: SKIPPED (--skip-gh)" || echo "  GitHub secret: RELAY_PASSWORD in $REPO"
echo "  NOTE: clients using the OLD shared password will break until they re-fetch."

if [ "$ASSUME_YES" != 1 ]; then
  read -r -p "Proceed? [y/N] " ok
  case "$ok" in [yY]*) ;; *) echo "aborted, nothing changed"; unset NEWVAL; exit 0 ;; esac
fi

SSH_OPTS=(-o StrictHostKeyChecking=accept-new)
[ -n "$SSH_KEY" ] && SSH_OPTS+=(-i "$SSH_KEY")

# --- BOX FIRST: backup → swap → restart → health-gate → rollback -----------
# The new value rides the heredoc body over the encrypted SSH channel (not argv,
# so it isn't visible in `ps` on the box).
echo "→ updating the box (health-gated, auto-rollback on failure)…"
if ! ssh "${SSH_OPTS[@]}" "$SSH_USER@$HOST" NP="$NEWVAL" HP="$HTTP_PORT" bash -s <<'REMOTE'
set -euo pipefail
UNIT=/etc/systemd/system/yaver-relay.service
[ -f "$UNIT" ] || { echo "no yaver-relay.service on box"; exit 3; }
cp "$UNIT" "$UNIT.pre-rotate"
# Replace both --password <v> and --password=<v> forms, and an
# Environment=RELAY_PASSWORD=<v> line if the box uses env instead of a flag.
sed -i \
  -e "s|--password [^ ]*|--password $NP|g" \
  -e "s|--password=[^ ]*|--password=$NP|g" \
  -e "s|^\(Environment=RELAY_PASSWORD=\).*|\1$NP|g" \
  "$UNIT"
if ! grep -q "$NP" "$UNIT"; then
  echo "ERROR: could not find a --password / RELAY_PASSWORD to replace in the unit"
  cp "$UNIT.pre-rotate" "$UNIT"; rm -f "$UNIT.pre-rotate"; exit 4
fi
systemctl daemon-reload
systemctl restart yaver-relay
sleep 3
if curl -fsS -m 5 "http://127.0.0.1:${HP}/health" >/dev/null 2>&1; then
  echo "relay healthy with rotated password"
  rm -f "$UNIT.pre-rotate"
else
  echo "UNHEALTHY after rotation — ROLLING BACK to pre-rotate unit"
  cp "$UNIT.pre-rotate" "$UNIT"; rm -f "$UNIT.pre-rotate"
  systemctl daemon-reload; systemctl restart yaver-relay; sleep 3
  curl -fsS -m 5 "http://127.0.0.1:${HP}/health" >/dev/null 2>&1 \
    && echo "rolled back — healthy on the OLD password" \
    || echo "ROLLBACK ALSO UNHEALTHY — manual intervention needed"
  exit 5
fi
REMOTE
then
  echo "✗ box rotation failed (rolled back if possible). GitHub secret NOT changed." >&2
  unset NEWVAL
  exit 1
fi
echo "✓ box now serving with the rotated password."

# --- GitHub secret (only after the box is confirmed healthy) ---------------
if [ "$SKIP_GH" != 1 ]; then
  printf '%s' "$NEWVAL" | gh secret set RELAY_PASSWORD -R "$REPO"
  echo "✓ GitHub secret RELAY_PASSWORD updated."
fi

unset NEWVAL
echo
echo "Done. Rotated the shared relay password."
echo "  • Clients on the OLD shared password must re-fetch (official relay uses"
echo "    per-user Convex passwords, so blast radius is the fallback + admin path)."
echo "  • The leaked value is in git history — scrub with git filter-repo when able."
echo "  • Real fix: finish the device-signature rollout and drop shared-password"
echo "    auth (watch /authmix sigPercent → ~100), so this class is gone for good."
