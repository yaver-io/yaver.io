#!/usr/bin/env bash
# Dry-run e2e for the à-la-carte capability shelf (the "Build cockpit" —
# docs/yaver-normie-concierge-fair-metering.md). PROVES the /managed/*
# routes end-to-end with ZERO spend and ZERO risk: it only reads the
# caller's own opt-in + ledger and flips one switch on/off (state on the
# user's own userSettings row — no provisioning, no metering, no money).
#
# Asserts:
#   - GET  /managed/services returns all six capability keys as booleans
#   - POST /managed/services toggles one capability for the caller
#   - GET  /managed/cockpit returns balance + enabled + runway fields
#   - GET  /managed/burn returns an honest per-capability breakdown
#   - unknown service key is rejected (400)
#   - unauthenticated access is rejected (401)
#   - leaves the row exactly as it found it (cleanup)
#
# Usage:
#   TOK=<session bearer> SITE=https://<dep>.convex.site \
#   scripts/e2e-capability-shelf-dryrun.sh
set -euo pipefail

: "${TOK:?session bearer required}"
: "${SITE:?Convex site URL required (e.g. https://<dep>.convex.site)}"

pass(){ printf '  \033[32mPASS\033[0m %s\n' "$1"; }
fail(){ printf '  \033[31mFAIL\033[0m %s\n' "$1"; exit 1; }
jqget(){ echo "$1" | jq -r "$2"; }

auth=(-H "Authorization: Bearer $TOK")
json=(-H 'Content-Type: application/json')

# Probe the capability we'll toggle, and remember its starting state so
# we restore it at the end (idempotent — safe to re-run).
PROBE="reload"

echo "== a) GET /managed/services lists all six capabilities =="
s0=$(curl -fsS "${auth[@]}" "$SITE/managed/services")
echo "  $s0"
for k in reload backend web agentBox inference publish; do
  v=$(jqget "$s0" ".services.$k")
  [ "$v" = "true" ] || [ "$v" = "false" ] || fail "capability '$k' missing/non-boolean (got '$v')"
done
pass "all six capability keys present as booleans"
START=$(jqget "$s0" ".services.$PROBE")

echo "== b) POST /managed/services turns '$PROBE' ON =="
on=$(curl -fsS -X POST "${auth[@]}" "${json[@]}" \
  -d "{\"service\":\"$PROBE\",\"enabled\":true}" "$SITE/managed/services")
echo "  $on"
[ "$(jqget "$on" '.ok')" = "true" ] && [ "$(jqget "$on" ".services.$PROBE")" = "true" ] \
  && pass "$PROBE enabled" || fail "enable did not take"

echo "== c) GET /managed/services reflects the toggle =="
s1=$(curl -fsS "${auth[@]}" "$SITE/managed/services")
[ "$(jqget "$s1" ".services.$PROBE")" = "true" ] && pass "persisted" || fail "not persisted"

echo "== d) GET /managed/cockpit returns balance + enabled + runway =="
ck=$(curl -fsS "${auth[@]}" "$SITE/managed/cockpit")
echo "  $ck"
[ "$(jqget "$ck" '.ok')" = "true" ] || fail "cockpit not ok"
[ "$(jqget "$ck" '.balanceCents')" != "null" ] || fail "no balanceCents"
[ "$(jqget "$ck" ".enabled.$PROBE")" = "true" ] || fail "cockpit.enabled out of sync"
[ "$(jqget "$ck" '.anyEnabled')" = "true" ] || fail "anyEnabled should be true"
pass "cockpit summary well-formed"

echo "== e) GET /managed/burn returns honest per-capability breakdown =="
bn=$(curl -fsS "${auth[@]}" "$SITE/managed/burn")
echo "  $bn"
[ "$(jqget "$bn" '.ok')" = "true" ] || fail "burn not ok"
[ "$(jqget "$bn" '.rows | type')" = "array" ] || fail "burn.rows not an array"
[ "$(jqget "$bn" '.totalChargedCents')" != "null" ] || fail "no totalChargedCents"
pass "burn breakdown well-formed (rows=$(jqget "$bn" '.rows | length'))"

echo "== f) unknown service key is rejected (400) =="
uc=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "${auth[@]}" "${json[@]}" \
  -d '{"service":"definitely-not-a-capability","enabled":true}' "$SITE/managed/services")
[ "$uc" = "400" ] && pass "unknown capability rejected (400)" || fail "expected 400, got $uc"

echo "== g) unauthenticated access is rejected (401) =="
un=$(curl -sS -o /dev/null -w '%{http_code}' "$SITE/managed/services")
[ "$un" = "401" ] && pass "no-bearer rejected (401)" || fail "expected 401, got $un"

echo "== h) cleanup — restore '$PROBE' to its starting state ($START) =="
curl -fsS -X POST "${auth[@]}" "${json[@]}" \
  -d "{\"service\":\"$PROBE\",\"enabled\":$START}" "$SITE/managed/services" >/dev/null
nowv=$(curl -fsS "${auth[@]}" "$SITE/managed/services" | jq -r ".services.$PROBE")
[ "$nowv" = "$START" ] && pass "restored ($PROBE=$START)" || fail "cleanup failed (now=$nowv)"

echo
printf '\033[32mAll capability-shelf dry-run checks passed.\033[0m No spend, no provisioning.\n'
