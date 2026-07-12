#!/usr/bin/env bash
# relay-secure.sh — ONE entry point to secure the relay box end-to-end:
#   1. resolve the box IP (from public.yaver.io, or --host)
#   2. set the RELAY_SSH_HOST GitHub secret (so CI deploys work)
#   3. rotate the shared relay password  (box-first, health-gated, auto-rollback)
#   4. lock SSH to key-only              (validated, no lockout)
#   5. (optional) install the monthly auto-rotation timer on the box
#
# Safe for the flow: mobile + the agent authenticate with PER-USER relay
# passwords via Convex, NOT the shared password this rotates — so rotation never
# breaks clients. See docs/yaver-relay-production-ops.md §4.
#
# Usage:
#   scripts/relay-secure.sh --all                 # do everything, auto-resolve host
#   scripts/relay-secure.sh --set-host-secret     # just wire RELAY_SSH_HOST
#   scripts/relay-secure.sh --rotate --harden     # pick steps
#
# Options:
#   --host <ip|dns>   Box IP (default: resolve public.yaver.io).
#   --domain <dns>    Hostname to resolve for the box (default public.yaver.io).
#   --repo <o/r>      GitHub repo (default kivanccakmak/yaver.io).
#   --ssh-key <path>  SSH private key.
#   --set-host-secret Set the RELAY_SSH_HOST GitHub secret to the box IP.
#   --rotate          Rotate the shared relay password.
#   --harden          Lock SSH to key-only.
#   --install-timer   Install the monthly box-side rotation timer.
#   --all             = --set-host-secret --rotate --harden.
#   --yes             Don't prompt.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HOST=""; DOMAIN="public.yaver.io"; REPO="kivanccakmak/yaver.io"; SSH_KEY=""
DO_HOST_SECRET=0; DO_ROTATE=0; DO_HARDEN=0; DO_TIMER=0; ASSUME_YES=0

while [ $# -gt 0 ]; do
  case "$1" in
    --host) HOST="$2"; shift 2 ;;
    --domain) DOMAIN="$2"; shift 2 ;;
    --repo) REPO="$2"; shift 2 ;;
    --ssh-key) SSH_KEY="$2"; shift 2 ;;
    --set-host-secret) DO_HOST_SECRET=1; shift ;;
    --rotate) DO_ROTATE=1; shift ;;
    --harden) DO_HARDEN=1; shift ;;
    --install-timer) DO_TIMER=1; shift ;;
    --all) DO_HOST_SECRET=1; DO_ROTATE=1; DO_HARDEN=1; shift ;;
    --yes) ASSUME_YES=1; shift ;;
    -h|--help) grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

if [ "$((DO_HOST_SECRET+DO_ROTATE+DO_HARDEN+DO_TIMER))" = 0 ]; then
  echo "nothing to do — pass --all or specific steps (see --help)"; exit 2
fi

# 1. Resolve the box IP.
if [ -z "$HOST" ]; then
  HOST="$(dig +short "$DOMAIN" A 2>/dev/null | grep -E '^[0-9.]+$' | head -1 || true)"
  [ -n "$HOST" ] || { echo "ERROR: could not resolve $DOMAIN — pass --host <ip>" >&2; exit 1; }
  echo "Resolved $DOMAIN → $HOST"
fi
export RELAY_SSH_HOST="$HOST"

echo "Plan for $HOST:"
[ "$DO_HOST_SECRET" = 1 ] && echo "  • set RELAY_SSH_HOST GitHub secret"
[ "$DO_ROTATE" = 1 ]      && echo "  • rotate shared relay password (mobile unaffected — per-user Convex path)"
[ "$DO_HARDEN" = 1 ]      && echo "  • lock SSH to key-only"
[ "$DO_TIMER" = 1 ]       && echo "  • install monthly auto-rotation timer"
if [ "$ASSUME_YES" != 1 ]; then
  read -r -p "Proceed? [y/N] " ok; case "$ok" in [yY]*) ;; *) echo "aborted"; exit 0 ;; esac
fi

PASS=(); [ -n "$SSH_KEY" ] && PASS=(--ssh-key "$SSH_KEY")
YES=(); [ "$ASSUME_YES" = 1 ] && YES=(--yes)

# AUTHORIZATION: this script is public, but it is inert without YOUR credentials.
# Every box step runs over SSH, and the box is key-only (harden-relay-ssh.sh), so
# only an authorized private key can run them. Verify that up front — an
# unauthorized runner (no key) fails here, before touching anything. The GitHub
# secret step is separately gated by repo-owner `gh` auth.
if [ "$((DO_ROTATE+DO_HARDEN+DO_TIMER))" -gt 0 ]; then
  echo "Verifying SSH access to root@$HOST (key-gated — this is the access control)…"
  if [ -n "$SSH_KEY" ]; then
    ssh_ok() { ssh -o BatchMode=yes -o ConnectTimeout=8 -o StrictHostKeyChecking=accept-new -i "$SSH_KEY" "root@$HOST" true 2>/dev/null; }
  else
    ssh_ok() { ssh -o BatchMode=yes -o ConnectTimeout=8 -o StrictHostKeyChecking=accept-new "root@$HOST" true 2>/dev/null; }
  fi
  if ! ssh_ok; then
    echo "ERROR: cannot SSH to root@$HOST with your key — not authorized to run the box steps." >&2
    echo "  This IS the access control: the relay box is key-only. Only your key works." >&2
    exit 1
  fi
  echo "✓ SSH authorized (your key)."
fi

# 2. RELAY_SSH_HOST secret.
if [ "$DO_HOST_SECRET" = 1 ]; then
  printf '%s' "$HOST" | gh secret set RELAY_SSH_HOST -R "$REPO"
  echo "✓ RELAY_SSH_HOST GitHub secret set."
fi

# 3. Rotate (delegates to the health-gated rotation script).
# NB: ${arr[@]+"${arr[@]}"} — expand safely even when the array is empty; plain
# "${arr[@]}" throws "unbound variable" under set -u on macOS's bash 3.2.
if [ "$DO_ROTATE" = 1 ]; then
  "$HERE/rotate-relay-password.sh" --host "$HOST" --repo "$REPO" ${PASS[@]+"${PASS[@]}"} ${YES[@]+"${YES[@]}"}
fi

# 4. Harden SSH.
if [ "$DO_HARDEN" = 1 ]; then
  "$HERE/harden-relay-ssh.sh" --host "$HOST" ${PASS[@]+"${PASS[@]}"} ${YES[@]+"${YES[@]}"}
fi

# 5. Timer.
if [ "$DO_TIMER" = 1 ]; then
  "$HERE/install-relay-rotation-timer.sh" --host "$HOST" ${PASS[@]+"${PASS[@]}"}
fi

echo
echo "Done. Relay secured. Next: I can run the relay-deploy-binary.yml deploy now"
echo "that RELAY_SSH_HOST is set, to land the 0.1.19 hardening live."
