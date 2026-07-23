#!/usr/bin/env bash
# yaver-lane-loop.sh — full-stack closed loop for the preview lanes, on ONE box.
#
# This machine is both server and client: a REAL yaver agent (its own config
# dir, its own port, a stub account), a REAL dev server, a REAL browser, and a
# REAL file edit that must reach the screen. Nothing is mocked except the human.
#
# Why it exists: every previous loop tested a piece. rn-browser-loop.mjs proved
# a page paints; rn-vibe-loop.mjs proved an edit reaches the screen; the doctor
# probe proved the agent can see both. None of them went through the agent's
# HTTP surface the way the phone does — so a bug in /dev/start, in the /dev/
# proxy, or in the token-in-query auth (the 401 class the phone hits and no
# header-authenticated check can see) would pass every one of them.
#
# ISOLATION, because a shared box is the normal case here:
#   * YAVER_CONFIG_DIR      — never touches ~/.yaver
#   * --port on a high port — never touches the running agent on 18080
#   * a stub account        — never uses the operator's session
#   * the dev server it starts belongs to THIS agent instance only
# Another session's agent, dev server and tmux are left alone.
#
# Usage:
#   CONVEX_SITE_URL=https://<deployment>.convex.site ./yaver-lane-loop.sh <projectDir> [themeFile] [colorKey]
set -uo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"
REPO="$(cd .. && pwd)"
PROJECT_DIR="${1:-$HOME/Workspace/sfmg}"
THEME_FILE="${2:-src/theme/colors.ts}"
COLOR_KEY="${3:-background}"
NEW_COLOR="${NEW_COLOR:-#4B0082}"

: "${CONVEX_SITE_URL:?set CONVEX_SITE_URL to the Convex deployment (e.g. https://x.convex.site)}"
STUB_EMAIL="${STUB_EMAIL:-lane-loop@yaver.io}"
STUB_PASSWORD="${STUB_PASSWORD:-laneLoopPass2026!}"
STUB_NAME="${STUB_NAME:-Lane Loop}"

RUN_DIR="$(mktemp -d "${TMPDIR:-/tmp}/yaver-lane-loop.XXXXXX")"
AGENT_PORT="${AGENT_PORT:-18099}"
AGENT_LOG="$RUN_DIR/agent.log"
export SCRATCH="$RUN_DIR"

say() { printf '\n\033[1m>> %s\033[0m\n' "$*"; }
AGENT_PID=""
cleanup() {
  [ -n "$AGENT_PID" ] && kill "$AGENT_PID" 2>/dev/null
  # Restore the project file no matter how we exit — this loop edits real source.
  # A byte-for-byte copy, NOT a shell-variable round trip: $(cat f) strips the
  # trailing newline and printf does not put it back, so the "restore" left the
  # file one byte different and permanently dirty in git. Cost a run to find.
  [ -n "${THEME_BACKUP:-}" ] && [ -f "$THEME_BACKUP" ] && cp "$THEME_BACKUP" "$PROJECT_DIR/$THEME_FILE"
  say "cleaned up (agent stopped, $THEME_FILE restored). logs: $AGENT_LOG"
}
trap cleanup EXIT INT TERM

# ── 0. never run against a dirty target file ─────────────────────────────────
if ! git -C "$PROJECT_DIR" diff --quiet -- "$THEME_FILE" 2>/dev/null; then
  echo "REFUSING: $THEME_FILE has uncommitted changes; this loop rewrites and restores it."
  exit 2
fi
THEME_BACKUP="$RUN_DIR/theme.orig"
mkdir -p "$RUN_DIR"
cp "$PROJECT_DIR/$THEME_FILE" "$THEME_BACKUP"

# ── 1. stub account (login, else signup) ─────────────────────────────────────
say "stub account: $STUB_EMAIL"
TOKEN=$(curl -sf -X POST "$CONVEX_SITE_URL/auth/login" -H 'Content-Type: application/json' \
  -d "{\"email\":\"$STUB_EMAIL\",\"password\":\"$STUB_PASSWORD\"}" 2>/dev/null \
  | python3 -c 'import sys,json;print(json.load(sys.stdin).get("token",""))' 2>/dev/null)
if [ -z "$TOKEN" ]; then
  TOKEN=$(curl -sf -X POST "$CONVEX_SITE_URL/auth/signup" -H 'Content-Type: application/json' \
    -d "{\"email\":\"$STUB_EMAIL\",\"fullName\":\"$STUB_NAME\",\"password\":\"$STUB_PASSWORD\"}" 2>/dev/null \
    | python3 -c 'import sys,json;print(json.load(sys.stdin).get("token",""))' 2>/dev/null)
fi
if [ -z "$TOKEN" ]; then
  # Say WHICH wall we hit. Email/password auth is deliberately gated in prod —
  # it is off by default and allowlisted per address — so a bare "could not get
  # a token" sends someone hunting a bug that does not exist.
  PROBE=$(curl -s --max-time 10 -X POST "$CONVEX_SITE_URL/auth/login" \
    -H 'Content-Type: application/json' \
    -d "{\"email\":\"$STUB_EMAIL\",\"password\":\"$STUB_PASSWORD\"}" 2>/dev/null)
  echo "FAIL: no stub-account token from $CONVEX_SITE_URL"
  echo "      server said: $PROBE"
  case "$PROBE" in
    *"not enabled for this email"*)
      cat <<'HINT'

      This is a deliberate production control, not a bug
      (backend/convex/authPasswordPolicy.ts): email/password sign-in is OFF by
      default and allowlisted per address. Two honest ways forward:

        A. Point this loop at a LOCAL Convex dev deployment, where enabling
           password auth costs nothing:
             cd backend && npx convex dev
             CONVEX_SITE_URL=http://127.0.0.1:3210 ./e2e/yaver-lane-loop.sh ...

        B. Allowlist one throwaway address on the deployment you are testing:
             npx convex env set YAVER_EMAIL_PASSWORD_AUTH_ENABLED true
             npx convex env set YAVER_EMAIL_PASSWORD_AUTH_ALLOWED_EMAILS lane-loop@yaver.io

           (B) widens auth on a live multi-tenant deployment. Prefer (A) unless
           you specifically want the loop running against prod.
HINT
      ;;
  esac
  exit 1
fi
echo "   token acquired (${#TOKEN} chars)"

# ── 2. isolated agent ────────────────────────────────────────────────────────
export YAVER_CONFIG_DIR="$RUN_DIR/.yaver"
mkdir -p "$YAVER_CONFIG_DIR"
cat > "$YAVER_CONFIG_DIR/config.json" <<EOF
{"auth_token":"$TOKEN","convex_site_url":"$CONVEX_SITE_URL","device_id":"lane-loop-$$"}
EOF

say "building agent"
(cd "$REPO/desktop/agent" && go build -o "$RUN_DIR/yaver" .) || { echo "FAIL: agent build"; exit 1; }

say "starting isolated agent on :$AGENT_PORT (config: $YAVER_CONFIG_DIR)"
"$RUN_DIR/yaver" serve --port "$AGENT_PORT" > "$AGENT_LOG" 2>&1 &
AGENT_PID=$!
for _ in $(seq 1 40); do
  curl -sf --max-time 2 "http://127.0.0.1:$AGENT_PORT/health" >/dev/null 2>&1 && break
  sleep 1
done
curl -sf --max-time 3 "http://127.0.0.1:$AGENT_PORT/health" >/dev/null 2>&1 \
  || { echo "FAIL: agent never became healthy — see $AGENT_LOG"; tail -20 "$AGENT_LOG"; exit 1; }
echo "   agent healthy"

AUTH=(-H "Authorization: Bearer $TOKEN")
BASE="http://127.0.0.1:$AGENT_PORT"

# ── 3. start the web target exactly as the phone does ────────────────────────
say "POST /dev/start {platform:web, caller:web-ui}  ← the browser lane"
curl -sf --max-time 30 "${AUTH[@]}" -H 'Content-Type: application/json' \
  -X POST "$BASE/dev/start" \
  -d "{\"workDir\":\"$PROJECT_DIR\",\"platform\":\"web\",\"caller\":\"web-ui\"}" >/dev/null \
  || { echo "FAIL: /dev/start rejected"; tail -20 "$AGENT_LOG"; exit 1; }

# ── 4. the phone's exact preview URL, token in the QUERY ─────────────────────
# Query auth, not a header: a WebView cannot set one. This is the 401 class that
# every header-authenticated check is blind to, so the loop must use it.
PREVIEW_URL="$BASE/dev/?token=$TOKEN"
say "probing the browser lane through the agent proxy"
node - "$PREVIEW_URL" <<'NODE'
const { chromium } = require("playwright");
const fs = require("fs");
const url = process.argv[2];
const pred = fs.readFileSync(process.env.REPO + "/mobile/src/lib/previewReadyScript.ts", "utf8")
  .match(/export const PREVIEW_READY_PREDICATE = `([\s\S]*?)`;/)[1];
(async () => {
  const b = await chromium.launch();
  const p = await b.newPage();
  const deadline = Date.now() + 240000;
  let ok = false;
  while (Date.now() < deadline) {
    try {
      await p.goto(url, { waitUntil: "domcontentloaded", timeout: 15000 });
      ok = await p.waitForFunction((src) => {
        const f = new Function(src + "; return yaverPreviewReady;")();
        return f(document);
      }, pred, { timeout: 10000 }).then(() => true).catch(() => false);
      if (ok) break;
    } catch {}
    await new Promise((r) => setTimeout(r, 3000));
  }
  await p.screenshot({ path: process.env.SCRATCH + "/lane-loop-rendered.png" });
  await b.close();
  console.log(ok ? "   RENDERED through the agent proxy" : "   NOT RENDERED");
  process.exit(ok ? 0 : 1);
})();
NODE
RENDER_RC=$?

# ── 5. real reload: edit the theme, assert the pixel changes ─────────────────
say "vibe: $COLOR_KEY -> $NEW_COLOR (real edit, real reload)"
python3 - "$PROJECT_DIR/$THEME_FILE" "$COLOR_KEY" "$NEW_COLOR" <<'PY'
import re,sys
path,key,col=sys.argv[1],sys.argv[2],sys.argv[3]
s=open(path).read()
s2=re.sub(rf"({key}:\s*)['\"][^'\"]+['\"]", rf"\1'{col}'", s, count=1)
open(path,'w').write(s2)
print("   patched" if s2!=s else "   WARNING: no change")
PY

node - "$PREVIEW_URL" "$NEW_COLOR" <<'NODE'
const { chromium } = require("playwright");
const [url, hex] = process.argv.slice(2);
const want = (() => { const n = parseInt(hex.replace("#",""),16);
  return `rgb(${(n>>16)&255}, ${(n>>8)&255}, ${n&255})`; })();
(async () => {
  const b = await chromium.launch();
  const p = await b.newPage();
  await p.goto(url, { waitUntil: "domcontentloaded" }).catch(()=>{});
  const deadline = Date.now() + 120000;
  let hit = false;
  while (Date.now() < deadline) {
    await p.waitForTimeout(2500);
    hit = await p.evaluate((w) => [...document.querySelectorAll("*")]
      .some((el) => getComputedStyle(el).backgroundColor === w), want).catch(() => false);
    if (hit) break;
  }
  await p.screenshot({ path: process.env.SCRATCH + "/lane-loop-vibed.png" });
  await b.close();
  console.log(hit ? `   VIBE APPLIED (${want})` : `   VIBE NOT APPLIED (wanted ${want})`);
  process.exit(hit ? 0 : 1);
})();
NODE
VIBE_RC=$?

echo ""
echo "──────── yaver lane loop ────────"
echo "  $([ $RENDER_RC -eq 0 ] && echo PASS || echo FAIL)  browser lane renders through the agent (query-auth URL)"
echo "  $([ $VIBE_RC   -eq 0 ] && echo PASS || echo FAIL)  edit reaches the screen (real reload)"
echo "  screenshots: $RUN_DIR"
[ $RENDER_RC -eq 0 ] && [ $VIBE_RC -eq 0 ]
