#!/usr/bin/env bash
# smoke-guest-share-dummy.sh — two-dummy-account variant of the guest-share
# smoke test. Does NOT require you to be signed in. Exercises only the Convex
# side of the flow (invite, findByCode, accept-code, hosts/list, revoke).
#
# Useful in CI and when you just want to prove the Convex contract works.

set -euo pipefail

CONVEX_URL="${CONVEX_URL:-https://perceptive-minnow-557.eu-west-1.convex.site}"
if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required" >&2; exit 1
fi

say() { printf "\n== %s ==\n" "$*"; }
rand() { head -c16 /dev/urandom | od -An -tx1 | tr -d ' \n'; }

mkuser() {
  local email="e2e-$(rand)@yaver.test"
  local name="E2E User $1"
  local pw="$(rand)"
  local r
  r=$(curl -fsS -X POST "$CONVEX_URL/auth/signup" \
    -H 'Content-Type: application/json' \
    -d "$(jq -nc --arg e "$email" --arg n "$name" --arg p "$pw" '{email:$e,fullName:$n,password:$p}')")
  local t u
  t=$(echo "$r" | jq -r '.token')
  u=$(echo "$r" | jq -r '.userId')
  if [ -z "$t" ] || [ "$t" = "null" ]; then
    echo "signup failed: $r" >&2; exit 1
  fi
  echo "$email|$t|$u"
}

say "create two dummy accounts"
H="$(mkuser host)"; HEMAIL="${H%%|*}"; HTMP="${H#*|}"; HTOK="${HTMP%%|*}"; HUID="${H##*|}"
C="$(mkuser guest)"; CEMAIL="${C%%|*}"; CTMP="${C#*|}"; CTOK="${CTMP%%|*}"; CUID="${C##*|}"
echo "  host  $HEMAIL  $HUID"
echo "  guest $CEMAIL  $CUID"

say "host invites guest by userId (no device scope)"
INV=$(curl -fsS -X POST "$CONVEX_URL/guests/invite" \
  -H "Authorization: Bearer $HTOK" -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg u "$CUID" '{userId:$u}')")
CODE=$(echo "$INV" | jq -r '.inviteCode')
echo "  invite code: $CODE"
echo "  guestRegistered: $(echo "$INV" | jq -r '.guestRegistered')"

say "guest previews the invite (find-by-code)"
PRV=$(curl -fsS "$CONVEX_URL/guests/find-by-code?code=$CODE" -H "Authorization: Bearer $CTOK")
echo "  preview: $(echo "$PRV" | jq -c '{hostName, invitedByUserId, devices:(.hostDevices|length)}')"

say "guest /users/lookup for host's userId (for display)"
LKUP=$(curl -fsS "$CONVEX_URL/users/lookup?userId=$HUID" -H "Authorization: Bearer $CTOK")
echo "  lookup: $(echo "$LKUP" | jq -c '{fullName, email}')"

say "guest accepts"
ACC=$(curl -fsS -X POST "$CONVEX_URL/guests/accept-code" \
  -H "Authorization: Bearer $CTOK" -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg c "$CODE" '{code:$c}')")
echo "  accepted: $(echo "$ACC" | jq -c)"

say "guest sees host in /guests/hosts"
HOSTS=$(curl -fsS "$CONVEX_URL/guests/hosts" -H "Authorization: Bearer $CTOK")
echo "  $(echo "$HOSTS" | jq -c '{active:(.active|length), pending:(.pending|length)}')"
if [ "$(echo "$HOSTS" | jq -r '.active | length')" != "1" ]; then
  echo "FAIL: expected 1 active host" >&2; exit 1
fi

say "host lists guests"
LIST=$(curl -fsS "$CONVEX_URL/guests/list" -H "Authorization: Bearer $HTOK")
echo "  $(echo "$LIST" | jq -c '.guests[0] | {email, status, userId, invitedByUserId}')"

say "host revokes by userId"
curl -fsS -X POST "$CONVEX_URL/guests/revoke" \
  -H "Authorization: Bearer $HTOK" -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg u "$CUID" '{userId:$u}')" >/dev/null

AFTER=$(curl -fsS "$CONVEX_URL/guests/hosts" -H "Authorization: Bearer $CTOK")
AFTER_ACT=$(echo "$AFTER" | jq -r '.active | length')
echo "  after revoke — cousin sees $AFTER_ACT active hosts"
if [ "$AFTER_ACT" != "0" ]; then
  echo "FAIL: expected 0 active hosts after revoke" >&2; exit 1
fi

echo
echo "OK — dummy guest-share flow passed."
