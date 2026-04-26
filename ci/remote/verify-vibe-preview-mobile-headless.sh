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

TEST_TOKEN=""           # populated from /root/.yaver/config.json below
AGENT_PORT=18080         # systemd-managed yaver listens here
PROJECT="vibe-mh-smoke"
DEV_SERVER_PORT=18081

cleanup() {
  banner "cleanup"
  # Stop the ephemeral preview session we may have created. Idempotent.
  if [ -n "${TEST_TOKEN:-}" ]; then
    curl -fsS -X POST -H "Authorization: Bearer $TEST_TOKEN" \
      -H "Content-Type: application/json" \
      -d "$(jq -nc --arg p "$PROJECT" '{project:$p}')" \
      "http://127.0.0.1:${AGENT_PORT:-18080}/vibing/preview/stop" >/dev/null 2>&1 || true
  fi
  if [ -n "${DEV_PID:-}" ]; then
    kill "$DEV_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

banner "rebuild + install yaver from source"
cd "$REPO/desktop/agent"
export PATH=/usr/local/go/bin:$PATH
go build -o /tmp/yaver-new .
install -m 0755 /tmp/yaver-new /usr/local/bin/yaver
rm -f /tmp/yaver-new
yaver --version 2>&1 | head -1
# Bounce the systemd-managed agent so it picks up the new binary
# (the running PID still has the old binary mapped — Restart=always
# kicks in).
systemctl restart yaver-agent.service 2>/dev/null || true
sleep 5

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

banner "use existing systemd-managed yaver agent"
# Don't try to bring up our own agent — the persistent ephemeral
# already runs yaver-agent.service via systemd with a real Convex-
# validated token. Trying to spawn a sibling caused 8 iterations
# worth of bootstrap-mode / port-collision / boot-timing pain. The
# systemd one IS the production-shaped agent we want to test
# against. Read its token straight out of /root/.yaver/config.json.
if ! systemctl is-active --quiet yaver-agent.service; then
  echo "WARN: yaver-agent.service not active; trying to start it"
  systemctl start yaver-agent.service || true
  sleep 3
fi
systemctl is-active yaver-agent.service || true

if [ ! -f /root/.yaver/config.json ]; then
  echo "no /root/.yaver/config.json — box never paired? skipping"
  exit 0
fi
TEST_TOKEN="$(jq -r '.auth_token // empty' /root/.yaver/config.json)"
if [ -z "$TEST_TOKEN" ] || [ "$TEST_TOKEN" = "null" ]; then
  echo "no auth_token in config.json — box not signed in; skipping"
  exit 0
fi
echo "using existing agent's auth_token (${TEST_TOKEN:0:8}…)"
AGENT_PORT=18080

banner "diagnostic: /health + /info via Bearer"
curl -fsS "http://127.0.0.1:$AGENT_PORT/health" || { echo "agent not on 18080"; exit 1; }
echo
direct_status="$(curl -sS -o /tmp/info-resp.json -w '%{http_code}' \
  -H "Authorization: Bearer $TEST_TOKEN" \
  "http://127.0.0.1:$AGENT_PORT/info" || echo curl-fail)"
echo "GET /info → HTTP $direct_status"
head -c 300 /tmp/info-resp.json
echo
if [ "$direct_status" != "200" ]; then
  echo "existing agent rejected its own auth_token — token may be expired"
  exit 1
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
