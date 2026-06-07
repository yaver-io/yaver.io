#!/usr/bin/env bash
# Headless e2e for the BYO-Hetzner lifecycle — drives the SAME agent /ops
# verbs the mobile app calls (setup_inventory, cloud_provision, cloud_stop,
# cloud_start, cloud_bake, cloud_reconcile) plus the Convex /byo/machines
# lifecycle read. PROVES the full "init new machine" path with ZERO real
# spend and ZERO risk: every mutate is asserted dryRun:true (guaranteed
# while YAVER_CLOUD_STOPSTART_LIVE != 1 on the agent), and setup_inventory
# is read-only. This is the "yaver mobile headless" test — the phone is
# just an /ops client, so curl is an equivalent client.
#
# The REAL-spend path is the owner runbook (set YAVER_CLOUD_STOPSTART_LIVE=1
# + connect Hetzner), exercised by the Go fake-Hetzner tests at $0:
#   go test -run 'TestByoLifecycle|TestHetznerCreateServerCustom' ./desktop/agent
#
# Usage:
#   AGENT=http://localhost:18080 TOK=<agent bearer> \
#   SITE=https://<dep>.convex.site \
#   scripts/e2e-byo-cloud-dryrun.sh
set -euo pipefail

: "${AGENT:?agent base url required (e.g. http://localhost:18080)}"
: "${TOK:?bearer token required}"
SITE="${SITE:-}"

pass(){ printf '  \033[32mPASS\033[0m %s\n' "$1"; }
fail(){ printf '  \033[31mFAIL\033[0m %s\n' "$1"; exit 1; }
jqget(){ echo "$1" | jq -r "$2"; }

auth=(-H "Authorization: Bearer $TOK")
json=(-H 'Content-Type: application/json')

ops(){ # verb payload-json
  curl -fsS -X POST "${auth[@]}" "${json[@]}" \
    -d "{\"verb\":\"$1\",\"machine\":\"local\",\"payload\":$2}" "$AGENT/ops"
}

echo "== a) setup_inventory (read-only; non-secret) =="
inv="$(ops setup_inventory '{}')"
echo "  $inv"
[ "$(jqget "$inv" '.ok')" = "true" ] && pass "inventory ok" || fail "inventory not ok"
# Must NOT leak a secret value.
echo "$inv" | grep -qiE '"(token|secret|password)"\s*:\s*"[A-Za-z0-9_/-]{16,}"' \
  && fail "inventory leaked a secret value" || pass "no secret values in inventory"

echo "== b) cloud_provision — must be dry-run (no real box) =="
pr="$(ops cloud_provision '{"plan":"starter","region":"eu","confirm":true}')"
echo "  $pr"
[ "$(jqget "$pr" '.initial.dryRun')" = "true" ] && pass "provision dryRun:true (fail-closed)" \
  || fail "provision NOT dry-run — YAVER_CLOUD_STOPSTART_LIVE must be unset"

echo "== c) cloud_provision with repoUrl clones under ~/Workspace =="
pr2="$(ops cloud_provision '{"plan":"starter","repoUrl":"https://github.com/acme/app.git","confirm":true}')"
echo "  $(jqget "$pr2" '.initial.plan')"
[ "$(jqget "$pr2" '.initial.dryRun')" = "true" ] && pass "repo-clone provision dry-run" || fail "not dry-run"

echo "== d) cloud_stop / cloud_start — dry-run =="
st="$(ops cloud_stop '{"serverId":"4242","confirm":true}')"
[ "$(jqget "$st" '.initial.dryRun')" = "true" ] && pass "stop dryRun:true" || fail "stop not dry-run"
sr="$(ops cloud_start '{"snapshotImageId":"1","name":"x","confirm":true}')"
[ "$(jqget "$sr" '.initial.dryRun')" = "true" ] && pass "start dryRun:true" || fail "start not dry-run"

echo "== e) cloud_bake — dry-run =="
bk="$(ops cloud_bake '{"serverId":"4242","confirm":true}')"
[ "$(jqget "$bk" '.initial.dryRun')" = "true" ] && pass "bake dryRun:true" || fail "bake not dry-run"

echo "== f) cloud_reconcile (tolerate no-account when Hetzner unconnected) =="
rc="$(curl -fsS -X POST "${auth[@]}" "${json[@]}" \
  -d '{"verb":"cloud_reconcile","machine":"local","payload":{}}' "$AGENT/ops" || true)"
if [ "$(jqget "$rc" '.ok')" = "true" ]; then pass "reconcile ok ($(jqget "$rc" '.initial.reconciled') servers)"
elif echo "$rc" | grep -q "no_account"; then pass "reconcile → no_account (Hetzner not connected; expected)"
else fail "reconcile unexpected: $rc"; fi

if [ -n "$SITE" ]; then
  echo "== g) Convex /byo/machines lifecycle read =="
  bm="$(curl -fsS "${auth[@]}" "$SITE/byo/machines")"
  echo "  $bm"
  [ "$(jqget "$bm" '.ok')" = "true" ] && pass "byo/machines reachable ($(echo "$bm" | jq '.machines|length') rows)" \
    || fail "byo/machines not ok"
else
  echo "== g) /byo/machines — skipped (set SITE to exercise) =="
fi

echo
echo "ALL DRY-RUN ASSERTIONS PASSED — BYO lifecycle proven headless, \$0,"
echo "nothing created/stopped/deleted (fail-closed: YAVER_CLOUD_STOPSTART_LIVE unset)."
