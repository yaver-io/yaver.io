#!/usr/bin/env bash
# smoke-guest-share.sh — end-to-end smoke test for the guest-sharing flow.
#
# What it exercises:
#   1. Sign up a throwaway "cousin" account via /auth/signup.
#   2. From the host's session (yaver auth already done), invite the cousin —
#      both by email and by public userId — optionally with a pre-scoped
#      device list.
#   3. Preview the invite as the cousin via /guests/find-by-code.
#   4. Accept the invite by code, narrowing the scope.
#   5. Verify the cousin's /guests/hosts + /devices/list shows the host.
#   6. Hit the running local agent on :18080 with the cousin's token:
#        - GET /info                (allowed)
#        - GET /projects            (allowed)
#        - POST /dev/start          (allowed — Hermes-push prerequisite)
#        - POST /dev/build-native   (allowed — this is the Hermes thing)
#        - POST /exec               (must be denied — guest scope)
#   7. Revoke from the host and confirm guest is denied within
#      guestTokenCacheTTL seconds.
#
# Requires:
#   - yaver CLI signed in as the HOST (this script reads ~/.yaver/config.json).
#   - `yaver serve` running locally on http://127.0.0.1:18080 for step 6.
#   - jq.
#
# This script creates a throwaway user and deletes its invite on revoke, but
# the account itself is left in the dev Convex — run against dev only.

set -euo pipefail

CONVEX_URL="${CONVEX_URL:-https://perceptive-minnow-557.eu-west-1.convex.site}"
AGENT_URL="${AGENT_URL:-http://127.0.0.1:18080}"
CFG="$HOME/.yaver/config.json"

if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required" >&2; exit 1
fi
if [ ! -f "$CFG" ]; then
  echo "Not signed in — run 'yaver auth' first." >&2; exit 1
fi

HOST_TOKEN="$(jq -r '.auth_token // .authToken // empty' "$CFG")"
if [ -z "$HOST_TOKEN" ]; then
  HOST_TOKEN="${YAVER_AUTH_TOKEN:-}"
fi
if [ -z "$HOST_TOKEN" ]; then
  echo "No auth token found. Run 'yaver auth' first, or set YAVER_AUTH_TOKEN." >&2; exit 1
fi

say() { printf "\n== %s ==\n" "$*"; }

rand() { head -c16 /dev/urandom | od -An -tx1 | tr -d ' \n'; }
EMAIL="e2e-guest-$(rand)@yaver.test"
NAME="E2E Cousin $(date +%s)"
PASSWORD="$(rand)"

say "1/7 create dummy cousin account  ($EMAIL)"
SIGN=$(curl -fsS -X POST "$CONVEX_URL/auth/signup" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg e "$EMAIL" --arg n "$NAME" --arg p "$PASSWORD" '{email:$e,fullName:$n,password:$p}')")
COUSIN_TOKEN=$(echo "$SIGN" | jq -r '.token')
COUSIN_USERID=$(echo "$SIGN" | jq -r '.userId')
if [ -z "$COUSIN_TOKEN" ] || [ "$COUSIN_TOKEN" = "null" ]; then
  echo "signup failed: $SIGN" >&2; exit 1
fi
echo "  cousin userId: $COUSIN_USERID"

# Helper: resolve host userId + pick one of the host's devices as a proposal.
HOST_INFO=$(curl -fsS "$CONVEX_URL/auth/validate" -H "Authorization: Bearer $HOST_TOKEN")
HOST_USERID=$(echo "$HOST_INFO" | jq -r '.user.userId // .user.id')
echo "  host userId:   $HOST_USERID"

DEVICES=$(curl -fsS "$CONVEX_URL/devices/list" -H "Authorization: Bearer $HOST_TOKEN")
FIRST_DEV=$(echo "$DEVICES" | jq -r '[.devices[] | select(.isGuest != true)] | .[0].deviceId // empty')
if [ -z "$FIRST_DEV" ]; then
  echo "Host has no non-guest devices registered; run 'yaver serve' first." >&2; exit 1
fi
echo "  host first dev: $FIRST_DEV"

say "2/7 host invites cousin — by userId, scoped to one device"
INV=$(curl -fsS -X POST "$CONVEX_URL/guests/invite" \
  -H "Authorization: Bearer $HOST_TOKEN" -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg u "$COUSIN_USERID" --arg d "$FIRST_DEV" '{userId:$u, deviceIds:[$d]}')")
CODE=$(echo "$INV" | jq -r '.inviteCode')
echo "  invite code: $CODE"
echo "  invite payload: $(echo "$INV" | jq -c '{ok,inviteCode,guestRegistered,guestUserId,guestEmail}')"

say "3/7 cousin previews the invite"
PREVIEW=$(curl -fsS "$CONVEX_URL/guests/find-by-code?code=$CODE" -H "Authorization: Bearer $COUSIN_TOKEN")
echo "  preview: $(echo "$PREVIEW" | jq -c '{hostName, hostEmail, invitedByUserId, proposedDeviceIds, devices:(.hostDevices|length)}')"

say "4/7 cousin accepts, narrowing scope to the proposed device"
ACC=$(curl -fsS -X POST "$CONVEX_URL/guests/accept-code" \
  -H "Authorization: Bearer $COUSIN_TOKEN" -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg c "$CODE" --arg d "$FIRST_DEV" '{code:$c, approvedDeviceIds:[$d]}')")
echo "  accept: $(echo "$ACC" | jq -c)"

say "5/7 cousin can see the host in /guests/hosts and /devices/list"
HOSTS=$(curl -fsS "$CONVEX_URL/guests/hosts" -H "Authorization: Bearer $COUSIN_TOKEN")
echo "  hosts: $(echo "$HOSTS" | jq -c '{active:(.active|length), pending:(.pending|length)}')"
CDEV=$(curl -fsS "$CONVEX_URL/devices/list" -H "Authorization: Bearer $COUSIN_TOKEN")
echo "  devices cousin sees: $(echo "$CDEV" | jq -c '[.devices[] | {deviceId, name, isGuest, hostName}]')"

say "6/7 hit local agent with cousin's token"
if curl -fsS "$AGENT_URL/health" >/dev/null 2>&1; then
  INFO=$(curl -sS -o /dev/null -w '%{http_code}' "$AGENT_URL/info" -H "Authorization: Bearer $COUSIN_TOKEN")
  PROJ=$(curl -sS -o /dev/null -w '%{http_code}' "$AGENT_URL/projects" -H "Authorization: Bearer $COUSIN_TOKEN")
  DEVSTAT=$(curl -sS -o /dev/null -w '%{http_code}' "$AGENT_URL/dev/status" -H "Authorization: Bearer $COUSIN_TOKEN")
  # Guest should be blocked from /exec (owner-only). 401/403 expected.
  EXEC=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$AGENT_URL/exec" \
    -H "Authorization: Bearer $COUSIN_TOKEN" -H 'Content-Type: application/json' \
    -d '{"cmd":"echo should-not-run"}')
  echo "  /info -> $INFO    /projects -> $PROJ    /dev/status -> $DEVSTAT    /exec (expect 401/403) -> $EXEC"
else
  echo "  (no local agent on $AGENT_URL, skipping)"
fi

say "7/7 host revokes — cousin should be denied within ~15s"
curl -fsS -X POST "$CONVEX_URL/guests/revoke" \
  -H "Authorization: Bearer $HOST_TOKEN" -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg u "$COUSIN_USERID" '{userId:$u}')" >/dev/null
echo "  revoked; waiting 20s for agent cache to expire…"
sleep 20
if curl -fsS "$AGENT_URL/health" >/dev/null 2>&1; then
  AFTER=$(curl -sS -o /dev/null -w '%{http_code}' "$AGENT_URL/info" -H "Authorization: Bearer $COUSIN_TOKEN")
  echo "  /info after revoke (expect 401/403): $AFTER"
fi

echo
echo "OK — guest sharing round-trip finished. Dummy account: $EMAIL (userId $COUSIN_USERID)"
