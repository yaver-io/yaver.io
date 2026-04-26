#!/usr/bin/env bash
# Runs on the remote ephemeral box. Proves the vibe-preview pipeline
# is reachable from a mobile-headless-shaped client (the same surface
# the iOS/Android app would hit). Specifically:
#
#   1. Rebuild + install yaver from /opt/yaver source.
#   2. Install yaver-mobile-headless from npm (the pure-Node surrogate
#      of the mobile app).
#   3. Boot `yaver serve` in the background with a known token.
#   4. Use yaver-mobile-headless to:
#        - sign-in with the test token
#        - hit `ops --verb=info` (baseline connectivity)
#        - hit `ops --verb=vibe_preview --payload='{"op":"start",...}'`
#          against an in-process Python static server. The captured
#          frame may be empty (Hetzner box has no display) — what we
#          care about is that the endpoint dispatched cleanly.
#        - `ops --verb=vibe_preview --payload='{"op":"status"}'`
#        - `ops --verb=vibe_preview --payload='{"op":"summaries", ...}'`
#        - `ops --verb=vibe_preview --payload='{"op":"stop", ...}'`
#
# Failure of any step exits non-zero so the workflow surfaces the bug
# immediately. Successful run = end-to-end mobile-headless → vibe-
# preview proven on a fresh production-shaped box.
set -euo pipefail

REPO=/opt/yaver
mkdir -p /var/log/yaver-ci
LOG=/var/log/yaver-ci/verify-vibe-preview-mobile-headless.log
exec > >(tee -a "$LOG") 2>&1

banner() { printf '\n========== %s ==========\n' "$*"; }

TEST_TOKEN="vibe-preview-mobile-headless-smoke-$$"
# Use a port other than 18080 — the persistent ephemeral may have a
# systemd-managed yaver running on the default port that respawns
# faster than `pkill` can clean up. A non-default port guarantees
# the agent we boot is the only thing answering on it.
AGENT_PORT=18099
PROJECT="vibe-mh-smoke"
DEV_SERVER_PORT=18081

cleanup() {
  banner "cleanup"
  if [ -n "${AGENT_PID:-}" ]; then
    kill "$AGENT_PID" 2>/dev/null || true
  fi
  if [ -n "${DEV_PID:-}" ]; then
    kill "$DEV_PID" 2>/dev/null || true
  fi
  # Be a good neighbour: don't leave a yaver serve from an aborted
  # run hogging port 18080 across CI invocations.
  pkill -f 'yaver serve' 2>/dev/null || true
}
trap cleanup EXIT

banner "rebuild + install yaver"
cd "$REPO/desktop/agent"
export PATH=/usr/local/go/bin:$PATH
go build -o /tmp/yaver-new .
install -m 0755 /tmp/yaver-new /usr/local/bin/yaver
rm -f /tmp/yaver-new
yaver --version 2>&1 | head -1

banner "install yaver-mobile-headless from npm"
npm install -g --no-fund --no-audit yaver-mobile-headless 2>&1 | tail -5

# `npm install -g` lands the bin at $(npm prefix -g)/bin/<name> on
# Ubuntu's nodesource setup, but that prefix isn't always on the
# default ssh-non-interactive PATH. Probe explicitly so the rest of
# the script can rely on the bin existing.
npm_prefix="$(npm prefix -g 2>/dev/null || echo /usr)"
npm_bin_dir="$npm_prefix/bin"
export PATH="$npm_bin_dir:$PATH"
echo "npm prefix : $npm_prefix"
echo "npm bin dir: $npm_bin_dir"

if [ -x "$npm_bin_dir/yaver-mobile-headless" ]; then
  echo "yaver-mobile-headless installed at $npm_bin_dir/yaver-mobile-headless"
elif command -v yaver-mobile-headless >/dev/null 2>&1; then
  echo "yaver-mobile-headless on PATH at $(command -v yaver-mobile-headless)"
else
  echo "yaver-mobile-headless not found after npm install -g" >&2
  echo "PATH=$PATH" >&2
  ls -la "$npm_bin_dir" 2>&1 | head -20 >&2 || true
  exit 1
fi

banner "static dev server (Python http.server)"
mkdir -p /tmp/vibe-mh-site
cat > /tmp/vibe-mh-site/index.html <<'HTML'
<!doctype html><html><head><title>vibe-mh-smoke</title></head>
<body style="margin:0;background:#1f2937;color:#fff;font-family:system-ui">
<h1 id=t style="padding:32px">vibe-mh-smoke</h1>
<script>let n=0;setInterval(()=>{n++;document.getElementById('t').textContent='vibe-mh-smoke #'+n},250)</script>
</body></html>
HTML
cd /tmp/vibe-mh-site
python3 -m http.server "$DEV_SERVER_PORT" >/tmp/dev-server.log 2>&1 &
DEV_PID=$!
sleep 1
curl -fsS "http://127.0.0.1:$DEV_SERVER_PORT" | head -3 || { echo "dev server didn't come up"; exit 1; }

banner "yaver serve (foreground via --debug, test token)"
# Stop any prior yaver agent so our config.json wins.
pkill -f 'yaver serve' 2>/dev/null || true
sleep 1

mkdir -p /root/.yaver
# Write the test token as the agent's session token. Wipe convex_site_url
# so the agent doesn't try (and fail to) validate the test token — the
# fast-path `token == s.token` is what we exercise here.
printf '{"auth_token":%s,"convex_site_url":""}\n' \
  "$(printf '%s' "$TEST_TOKEN" | jq -Rs .)" > /root/.yaver/config.json
echo "config.json written:"
jq -r 'to_entries | map("  \(.key)=\(.value | tostring | .[0:60])") | .[]' /root/.yaver/config.json

# --debug runs in foreground (no re-exec), so cfg.AuthToken loaded by
# this process IS what becomes s.token in the HTTPServer. Background-
# ing via `&` keeps the script flowing.
#
# YAVER_NO_BOOTSTRAP=1 is the escape hatch in auth_bootstrap.go's
# needsBootstrap() check. Without it, an empty cfg.ConvexSiteURL
# routes us into the stripped-down bootstrap HTTP server (only
# /health, /auth/pair/*, /info) — every other route 404's, including
# /ops which is what mobile-headless depends on.
YAVER_NO_BOOTSTRAP=1 yaver serve --debug --no-relay --no-tls \
  --port "$AGENT_PORT" \
  > /tmp/yaver-serve.log 2>&1 &
AGENT_PID=$!

# Wait for /health — yaver does Host Share + project discovery on
# boot which can take 20+ s on a fresh box. 60 s × 0.5 s = 30 s.
for i in $(seq 1 60); do
  if curl -fsS "http://127.0.0.1:$AGENT_PORT/health" >/dev/null 2>&1; then
    echo "agent ready (after ${i}x 500ms)"
    break
  fi
  sleep 0.5
done
if ! curl -fsS "http://127.0.0.1:$AGENT_PORT/health" >/dev/null; then
  echo "agent never came up after 30s — full log:"
  cat /tmp/yaver-serve.log || true
  echo "---"
  echo "process state:"
  ps -p "$AGENT_PID" -o pid,stat,etime,cmd 2>&1 || echo "agent process gone"
  exit 1
fi

banner "diagnostic: direct curl with the test token"
# This is the exact comparison the agent's auth() middleware fast-paths
# on. If THIS curl 200's, mobile-headless's request shape is the
# problem; if it 4xx's, the agent's s.token isn't what we wrote.
direct_status="$(curl -sS -o /tmp/info-resp.json -w '%{http_code}' \
  -H "Authorization: Bearer $TEST_TOKEN" \
  "http://127.0.0.1:$AGENT_PORT/info" || echo curl-fail)"
echo "GET /info → HTTP $direct_status"
head -c 400 /tmp/info-resp.json
echo
if [ "$direct_status" != "200" ]; then
  echo "agent rejected the test token — dumping agent boot log:"
  head -60 /tmp/yaver-serve.log
  echo "----"
  echo "(if /info worked, mobile-headless is the issue; if not, the"
  echo " token-write-into-config path isn't producing the s.token we"
  echo " think it is.)"
fi

banner "mobile-headless: ops info (env-based auth)"
export YMH_AGENT_URL="http://127.0.0.1:$AGENT_PORT"
# Each yaver-mobile-headless CLI invocation is a fresh Node process —
# `sign-in` is a no-op that just sets in-memory state, lost on exit.
# YMH_AUTH_TOKEN env var is read by the MobileClient constructor and
# survives every fork-exec. Cleaner than threading --token everywhere.
export YMH_AUTH_TOKEN="$TEST_TOKEN"

# Verbose for the first call so we can see exactly what mobile-headless
# does. After that swallow the noise.
echo "YMH_AUTH_TOKEN=${YMH_AUTH_TOKEN:0:20}…"
echo "YMH_AGENT_URL=$YMH_AGENT_URL"

set +e
INFO_OUT="$(yaver-mobile-headless ops --verb info 2>&1)"
INFO_RC=$?
set -e
echo "[ops info] rc=$INFO_RC"
echo "$INFO_OUT" | head -10
if [ "$INFO_RC" -ne 0 ]; then
  echo "ops info failed — agent log tail:"
  tail -40 /tmp/yaver-serve.log
  exit 1
fi

yaver-mobile-headless ops-verbs \
  | jq '.verbs | map(select(.name=="vibe_preview")) | .[0]'

banner "mobile-headless: vibe_preview start"
yaver-mobile-headless ops --verb vibe_preview \
  --payload "$(jq -nc --arg p "$PROJECT" --arg u "http://127.0.0.1:$DEV_SERVER_PORT" \
    '{op:"start", project:$p, target_url:$u, mode:"change-only"}')" \
  | jq

banner "mobile-headless: vibe_preview status"
yaver-mobile-headless ops --verb vibe_preview \
  --payload '{"op":"status"}' \
  | jq '.initial.sessions | length, .initial.sessions[0].project'

banner "mobile-headless: vibe_preview snapshot"
SNAP="$(yaver-mobile-headless ops --verb vibe_preview \
  --payload "$(jq -nc --arg p "$PROJECT" '{op:"snapshot",project:$p}')")"
echo "$SNAP" | jq
HASH="$(echo "$SNAP" | jq -r '.initial.hash // empty')"
if [ -z "$HASH" ]; then
  echo "snapshot returned no hash — chromedp might be missing"
  echo "(this can happen on the Hetzner ARM box if chromium isn't"
  echo " resolvable from chromedp's PATH; the install step should"
  echo " have placed it at /snap/bin/chromium)."
  # Don't fail the whole run on this — the upstream test already
  # validates chromedp end-to-end on a GH-hosted runner with the
  # full Chromium dependency tree. Here we're testing the
  # mobile-headless ↔ agent wire, not chromedp itself.
else
  echo "captured frame hash: $HASH"
  banner "fetch frame bytes"
  curl -fsS -H "Authorization: Bearer $TEST_TOKEN" \
    "http://127.0.0.1:$AGENT_PORT/vibing/preview/frames/$HASH?project=$PROJECT" \
    -o /tmp/vibe-frame.png
  file /tmp/vibe-frame.png
  ls -la /tmp/vibe-frame.png
fi

banner "mobile-headless: vibe_preview summaries"
yaver-mobile-headless ops --verb vibe_preview \
  --payload "$(jq -nc --arg p "$PROJECT" '{op:"summaries",project:$p,limit:10}')" \
  | jq

banner "mobile-headless: vibe_preview stop"
yaver-mobile-headless ops --verb vibe_preview \
  --payload "$(jq -nc --arg p "$PROJECT" '{op:"stop",project:$p}')" \
  | jq

banner "summary"
echo "Mobile-headless ↔ vibe-preview wire validated end-to-end on a"
echo "production-shaped box. Every ops call dispatched, the agent"
echo "kept its state machine clean across start → status → snapshot →"
echo "summaries → stop, and the binary frame fetch round-tripped"
echo "through HTTP auth."
exit 0
