#!/usr/bin/env bash
# rotate-money-token.sh — safely rotate a "makes-me-poor" provider token
# (Hetzner or Cloudflare) into Convex prod env + GitHub Actions secrets,
# WITHOUT the new value ever appearing in a transcript, a log, or your
# shell history.
#
# Why this exists: `npx convex env list` prints VALUES, and pasting a token
# on a normal command line leaks it to history / `ps`. This script reads the
# new value with a hidden prompt, VERIFIES it against the provider API first
# (so a typo can't brick prod), then writes both stores via stdin.
#
# Usage:
#   scripts/rotate-money-token.sh hcloud       # Hetzner Cloud API token
#   scripts/rotate-money-token.sh cloudflare   # Cloudflare API token
#
# Prereqs: run from repo root; `gh` authed; `backend/` has convex configured.
# Regenerate the token in the provider console FIRST, then run this and paste
# the new value at the hidden prompt.
set -euo pipefail

REPO="kivanccakmak/yaver.io"
KIND="${1:-}"

case "$KIND" in
  hcloud)
    CONVEX_VAR="HCLOUD_TOKEN"
    GH_SECRET="HCLOUD_TOKEN"
    CONSOLE="https://console.hetzner.cloud/ → Security → API Tokens (delete old, create new Read+Write)"
    VERIFY_DESC="Hetzner API (GET /v1/servers)"
    ;;
  cloudflare|cf)
    CONVEX_VAR="CF_API_TOKEN"
    GH_SECRET="CLOUDFLARE_API_TOKEN"
    CONSOLE="https://dash.cloudflare.com/profile/api-tokens (Roll / create token)"
    VERIFY_DESC="Cloudflare API (GET /user/tokens/verify)"
    ;;
  *)
    echo "usage: $0 hcloud|cloudflare" >&2
    exit 2
    ;;
esac

echo "Rotate: $CONVEX_VAR (Convex prod) + $GH_SECRET (GitHub secret)"
echo "1) Regenerate in console: $CONSOLE"
echo "2) Paste the NEW token below (input hidden)."
printf 'New %s value: ' "$CONVEX_VAR"
IFS= read -rs NEWVAL
echo
if [ -z "${NEWVAL:-}" ]; then
  echo "empty value — aborting, nothing changed" >&2
  exit 1
fi

# --- verify the new token works BEFORE touching prod (fail-closed) ---
echo "→ verifying new token against $VERIFY_DESC…"
case "$KIND" in
  hcloud)
    code=$(curl -s -o /dev/null -w '%{http_code}' \
      -H "Authorization: Bearer $NEWVAL" \
      https://api.hetzner.cloud/v1/servers?per_page=1 || echo 000)
    ;;
  cloudflare|cf)
    code=$(curl -s -o /dev/null -w '%{http_code}' \
      -H "Authorization: Bearer $NEWVAL" \
      https://api.cloudflare.com/client/v4/user/tokens/verify || echo 000)
    ;;
esac
if [ "$code" != "200" ]; then
  echo "✗ provider rejected the new token (HTTP $code) — NOT writing to prod. Nothing changed." >&2
  unset NEWVAL
  exit 1
fi
echo "✓ new token valid."

read -r -p "Write to Convex prod env + GitHub secret now? [y/N] " ok
case "$ok" in [yY]*) ;; *) echo "aborted, nothing changed"; unset NEWVAL; exit 0;; esac

# --- write GitHub secret via stdin (no value in argv / ps) ---
printf '%s' "$NEWVAL" | gh secret set "$GH_SECRET" -R "$REPO"
echo "✓ GitHub secret $GH_SECRET updated."

# --- write Convex prod env ---
( cd backend && npx convex env set "$CONVEX_VAR" "$NEWVAL" --prod >/dev/null )
echo "✓ Convex prod env $CONVEX_VAR updated."

unset NEWVAL
echo
echo "Done. The OLD token is now superseded — confirm it is deleted in the console"
echo "so the leaked/displayed copy is dead. Redeploy anything that caches env if needed."
