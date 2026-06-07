#!/usr/bin/env bash
# Dry-run e2e for the managed-cloud metered prepaid + stop/start
# lifecycle (P0–P6). PROVES the full path with ZERO real spend and
# ZERO risk to the target machine: it only calls the fail-closed
# routes, and asserts every stop/start came back dryRun:true (which
# is guaranteed while HCLOUD_TOKEN is unset on the Convex deployment).
#
# It NEVER destroys/provisions anything. The real-spend test is the
# separate owner runbook: docs/managed-cloud-go-live-runbook.md
#
# Usage:
#   TOK=<owner session bearer> MID=<machineId> \
#   SITE=https://perceptive-minnow-557.convex.site \
#   CRON_BEARER=<CRON_TRIGGER_SECRET> \
#   scripts/e2e-managed-cloud-dryrun.sh
set -euo pipefail

: "${TOK:?owner session bearer required}"
: "${MID:?machineId required}"
: "${SITE:?Convex site URL required (e.g. https://<dep>.convex.site)}"
CRON_BEARER="${CRON_BEARER:-}"

pass(){ printf '  \033[32mPASS\033[0m %s\n' "$1"; }
fail(){ printf '  \033[31mFAIL\033[0m %s\n' "$1"; exit 1; }
jqget(){ echo "$1" | jq -r "$2"; }

auth=(-H "Authorization: Bearer $TOK")
json=(-H 'Content-Type: application/json')

echo "== a) balance (P6) =="
b0=$(curl -fsS "${auth[@]}" "$SITE/billing/yaver-cloud/balance")
echo "  $b0"
bal0=$(jqget "$b0" '.balanceCents // 0')

echo "== b) owner-dev top-up +5000c (P6 -> P0 ledger) =="
tu=$(curl -fsS -X POST "${auth[@]}" "${json[@]}" \
  -d '{"amountCents":5000}' "$SITE/billing/yaver-cloud/topup-dev")
echo "  $tu"
bal1=$(curl -fsS "${auth[@]}" "$SITE/billing/yaver-cloud/balance" | jq -r '.balanceCents')
[ "$bal1" -ge $((bal0 + 5000)) ] && pass "balance rose by >=5000 ($bal0 -> $bal1)" \
  || fail "balance did not rise (got $bal1)"

echo "== b2) credit-pack catalog (prepaid front door) =="
pk=$(curl -fsS "${auth[@]}" "$SITE/billing/credits/packs")
echo "  $pk"
[ "$(jqget "$pk" '.packs | length')" -ge 1 ] && pass "credit packs listed" \
  || fail "no credit packs returned"

echo "== b3) credit-pack checkout (unconfigured variant => 503, configured => url) =="
co=$(curl -sS -o /tmp/co.json -w '%{http_code}' -X POST "${auth[@]}" "${json[@]}" \
  -d '{"packId":"p25"}' "$SITE/billing/credits/checkout")
echo "  HTTP $co $(cat /tmp/co.json)"
if [ "$co" = "200" ]; then
  [ "$(jq -r '.url' /tmp/co.json)" != "null" ] && pass "checkout url returned (LS configured)" \
    || fail "200 but no url"
elif [ "$co" = "503" ]; then
  pass "checkout 503 — pack variant not configured yet (expected pre-launch)"
else
  fail "unexpected checkout status $co"
fi

echo "== b3b) SECURITY: forged (unsigned) webhook must NOT mint credit =="
balB=$(curl -fsS "${auth[@]}" "$SITE/billing/yaver-cloud/balance" | jq -r '.balanceCents')
fc=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "${json[@]}" \
  -d '{"meta":{"event_name":"order_created","custom_data":{"user_email":"attacker@evil.test","product_type":"credit-pack","pack_id":"p100"}},"data":{"id":"forged-1","attributes":{"status":"paid","total":1,"first_order_item":{"variant_id":"999999","price":1}}}}' \
  "$SITE/webhooks/lemonsqueezy")
echo "  forged webhook HTTP $fc"
[ "$fc" = "401" ] && pass "forged webhook rejected (fail-closed signature)" \
  || fail "forged webhook returned $fc — expected 401 (set WEBHOOK_SECRET, leave ALLOW_UNSIGNED unset)"
balA=$(curl -fsS "${auth[@]}" "$SITE/billing/yaver-cloud/balance" | jq -r '.balanceCents')
[ "$balA" = "$balB" ] && pass "balance unchanged by forged webhook ($balB)" \
  || fail "balance moved on forged webhook ($balB -> $balA) — CREDIT MINT VULN"

echo "== b4) usage ledger (wallet activity) =="
us=$(curl -fsS "${auth[@]}" "$SITE/billing/yaver-cloud/usage")
echo "  $us"
[ "$(jqget "$us" '.ok')" = "true" ] && pass "usage ledger reachable" \
  || fail "usage ledger not ok"

echo "== c) STOP — must be dry-run, machine NOT destroyed (P3->P2) =="
st=$(curl -fsS -X POST "${auth[@]}" "${json[@]}" \
  -d "{\"machineId\":\"$MID\"}" "$SITE/billing/yaver-cloud/stop")
echo "  $st"
[ "$(jqget "$st" '.dryRun')" = "true" ] && pass "stop dryRun:true (fail-closed proven)" \
  || fail "stop was NOT dry-run — HCLOUD_TOKEN must be UNSET for this test"

echo "== d) START — canStart gate then dry-run active (P3->P2) =="
sr=$(curl -fsS -X POST "${auth[@]}" "${json[@]}" \
  -d "{\"machineId\":\"$MID\"}" "$SITE/billing/yaver-cloud/start")
echo "  $sr"
[ "$(jqget "$sr" '.dryRun')" = "true" ] && pass "start dryRun:true" \
  || fail "start was NOT dry-run"

if [ -n "$CRON_BEARER" ]; then
  echo "== e) METER tick (P2 cron path) =="
  curl -fsS -X POST -H "Authorization: Bearer $CRON_BEARER" "${json[@]}" \
    -d '{"name":"cloudMeter"}' "$SITE/crons/run" >/dev/null && pass "cloudMeter dispatched"
  sleep 3
  bal2=$(curl -fsS "${auth[@]}" "$SITE/billing/yaver-cloud/balance" | jq -r '.balanceCents')
  [ "$bal2" -le "$bal1" ] && pass "meter decremented/held balance ($bal1 -> $bal2)" \
    || fail "meter raised balance unexpectedly"
else
  echo "== e) METER — skipped (set CRON_BEARER to exercise) =="
fi

echo
echo "ALL DRY-RUN ASSERTIONS PASSED — P0–P6 path validated, \$0 spent,"
echo "machine $MID untouched (fail-closed: HCLOUD_TOKEN unset)."
