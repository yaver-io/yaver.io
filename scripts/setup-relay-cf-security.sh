#!/usr/bin/env bash
# setup-relay-cf-security.sh — put Cloudflare's edge in front of the free relay
# as the FIRST line of defense: DDoS (automatic), a per-IP rate-limit rule, the
# Cloudflare Managed WAF ruleset, Bot Fight Mode, and a raised security level.
#
# This complements the in-relay hardening (trusted-proxy IP gating, brute-force
# throttle, per-user stream caps) — the relay already treats Cloudflare as its
# trusted proxy by default (RELAY_TRUSTED_PROXIES). With CF in front, a flood is
# absorbed/challenged at the edge before it ever reaches the box.
#
# SECRETLESS by design: the CF API token is read from the environment or a
# --cf-token flag and NEVER written to a file or logged. Nothing here embeds a
# secret — safe to keep in the open-source repo.
#
# Usage:
#   CF_TOKEN=... ./scripts/setup-relay-cf-security.sh \
#       --relay-host relay.yaver.io --cf-zone yaver.io
#
# CF API token scope: Zone:Zone Settings:Edit + Zone:Firewall Services:Edit on
# the target zone. Each step is best-effort and independent — a feature that is
# plan-gated (e.g. rate-limiting rules on Free allow only one) logs a warning
# and the script continues.
set -euo pipefail

CF_TOKEN="${CF_TOKEN:-}"
CF_ZONE=""
RELAY_HOST=""
RL_REQUESTS="${RELAY_RL_REQUESTS:-300}"   # requests...
RL_PERIOD="${RELAY_RL_PERIOD:-60}"        # ...per this many seconds, per IP

while [ $# -gt 0 ]; do
  case "$1" in
    --cf-token)    CF_TOKEN="$2";    shift 2 ;;
    --cf-zone)     CF_ZONE="$2";     shift 2 ;;
    --relay-host)  RELAY_HOST="$2";  shift 2 ;;
    -h|--help)
      grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

[ -n "$CF_TOKEN" ]   || { echo "CF_TOKEN (or --cf-token) required" >&2; exit 2; }
[ -n "$CF_ZONE" ]    || { echo "--cf-zone required (e.g. yaver.io)" >&2; exit 2; }
[ -n "$RELAY_HOST" ] || { echo "--relay-host required (e.g. relay.yaver.io)" >&2; exit 2; }

log()  { echo "  $*"; }
ok()   { echo "✓ $*"; }
warn() { echo "⚠ $*" >&2; }

cf_api() {
  local method="$1" path="$2"; shift 2
  curl -fsS -X "$method" "https://api.cloudflare.com/client/v4${path}" \
    -H "Authorization: Bearer ${CF_TOKEN}" \
    -H "Content-Type: application/json" "$@"
}

# --- resolve zone id -------------------------------------------------------
ZONE_RESP=$(cf_api GET "/zones?name=${CF_ZONE}")
ZONE_ID=$(printf '%s' "$ZONE_RESP" | grep -o '"id":"[a-f0-9]\{32\}"' | head -1 | cut -d'"' -f4)
[ -n "$ZONE_ID" ] || { warn "zone '$CF_ZONE' not found (is the token scoped to it?)"; exit 1; }
ok "zone $CF_ZONE (${ZONE_ID:0:8}…)"

# --- 1. security level = high ---------------------------------------------
if cf_api PATCH "/zones/${ZONE_ID}/settings/security_level" --data '{"value":"high"}' >/dev/null 2>&1; then
  ok "security level → high"
else warn "could not set security level (plan/permission?)"; fi

# --- 2. browser integrity check on ----------------------------------------
if cf_api PATCH "/zones/${ZONE_ID}/settings/browser_check" --data '{"value":"on"}' >/dev/null 2>&1; then
  ok "browser integrity check → on"
else warn "could not enable browser integrity check"; fi

# --- 3. Bot Fight Mode -----------------------------------------------------
if cf_api PUT "/zones/${ZONE_ID}/bot_management" --data '{"fight_mode":true}' >/dev/null 2>&1; then
  ok "Bot Fight Mode → on"
else warn "could not enable Bot Fight Mode (may need Bot Management add-on)"; fi

# --- 4. Cloudflare Managed WAF ruleset ------------------------------------
# Deploy the Cloudflare Managed Ruleset at the managed-firewall phase entrypoint.
MANAGED_WAF_ID="efb7b8c949ac4650a09736fc376e9aee"  # Cloudflare Managed Ruleset (stable id)
WAF_PAYLOAD=$(printf '{"rules":[{"action":"execute","expression":"true","description":"Yaver relay: Cloudflare Managed Ruleset","action_parameters":{"id":"%s"}}]}' "$MANAGED_WAF_ID")
if cf_api PUT "/zones/${ZONE_ID}/rulesets/phases/http_request_firewall_managed/entrypoint" --data "$WAF_PAYLOAD" >/dev/null 2>&1; then
  ok "Cloudflare Managed WAF ruleset → deployed"
else warn "could not deploy Managed WAF ruleset (WAF may be plan-gated)"; fi

# --- 5. per-IP rate-limit rule on the relay host --------------------------
# New rulesets API, http_ratelimit phase. managed_challenge (not block) so a
# legit burst is challenged rather than hard-dropped. Free plan = 1 rule.
RL_EXPR="(http.host eq \"${RELAY_HOST}\")"
RL_PAYLOAD=$(printf '{"rules":[{"action":"managed_challenge","description":"Yaver relay per-IP rate limit","expression":"%s","ratelimit":{"characteristics":["ip.src","cf.colo.id"],"period":%s,"requests_per_period":%s,"mitigation_timeout":%s}}]}' \
  "$RL_EXPR" "$RL_PERIOD" "$RL_REQUESTS" "$RL_PERIOD")
if cf_api PUT "/zones/${ZONE_ID}/rulesets/phases/http_ratelimit/entrypoint" --data "$RL_PAYLOAD" >/dev/null 2>&1; then
  ok "rate limit → ${RL_REQUESTS} req / ${RL_PERIOD}s per IP on ${RELAY_HOST} (managed_challenge)"
else warn "could not set rate-limit rule (Free plan allows only one; check the dashboard)"; fi

echo
ok "Cloudflare edge hardening applied to ${RELAY_HOST}."
log "Defense-in-depth: CF absorbs/challenges floods at the edge; the relay's own"
log "trusted-proxy gating + brute-force throttle + per-user caps handle what gets through."
log "For an active attack, also flip the zone to 'Under Attack' mode in the dashboard."
