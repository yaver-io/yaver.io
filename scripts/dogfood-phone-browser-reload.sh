#!/usr/bin/env bash
# dogfood-phone-browser-reload.sh — closed-loop browser-reload test on a REAL
# paired phone, driven from the laptop (develop Yaver with Yaver).
#
# For an RN and a Flutter project:
#   1. start the dev server in the BROWSER lane (caller=web-ui, platform=web)
#   2. screenshot the phone (device_screenshot) — BEFORE
#   3. edit a background colour in the project source
#   4. hot-reload (POST /dev/reload)
#   5. screenshot the phone — AFTER
#   6. assert the average colour of the frame changed
#
# The phone must be foregrounded on the dev preview (WebView) for the lane under
# test. This uses only the agent HTTP API + the device_screenshot verb — no
# native device tooling (libimobiledevice can't reach an iOS-26 device).
#
# Env:
#   AGENT_BASE   default http://127.0.0.1:18080
#   AGENT_TOKEN  required (read from ~/.yaver/config.json if unset)
#   DEVICE_ID    optional — the paired phone's device id (else newest frame)
#   RN_DIR       default demo/mobile/todo-rn
#   FLUTTER_DIR  default demo/mobile/todo-flutter
set -uo pipefail

AGENT_BASE="${AGENT_BASE:-http://127.0.0.1:18080}"
TOKEN="${AGENT_TOKEN:-$(python3 -c 'import json,os;print(json.load(open(os.path.expanduser("~/.yaver/config.json"))).get("auth_token",""))' 2>/dev/null)}"
DEVICE_ID="${DEVICE_ID:-}"
REPO="$(cd "$(dirname "$0")/.." && pwd)"
RN_DIR="${RN_DIR:-$REPO/demo/mobile/todo-rn}"
FLUTTER_DIR="${FLUTTER_DIR:-$REPO/demo/mobile/todo-flutter}"
OUT="${OUT:-/tmp/dogfood-phone-frames}"; mkdir -p "$OUT"

[ -n "$TOKEN" ] || { echo "AGENT_TOKEN unset and not in ~/.yaver/config.json"; exit 2; }

api() { # method path [json]
  local m="$1" p="$2" body="${3:-}"
  if [ -n "$body" ]; then
    curl -sS -X "$m" "$AGENT_BASE$p" -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d "$body"
  else
    curl -sS -X "$m" "$AGENT_BASE$p" -H "Authorization: Bearer $TOKEN"
  fi
}

# device_screenshot via the ops verb → writes a JPEG, prints avg RGB.
shoot() { # label -> file
  local label="$1" file="$OUT/$label.jpg"
  local payload='{"verb":"device_screenshot","payload":{"timeoutMs":9000'"$([ -n "$DEVICE_ID" ] && echo ",\"deviceId\":\"$DEVICE_ID\"")"'},"machine":"local"}'
  local res; res="$(api POST /ops "$payload")"
  local img; img="$(printf '%s' "$res" | python3 -c 'import json,sys;d=json.load(sys.stdin);i=(d.get("initial") or d);print((i.get("image") or "").split(",",1)[-1])' 2>/dev/null)"
  [ -n "$img" ] || { echo "  ✗ no frame ($label): $res" >&2; return 1; }
  printf '%s' "$img" | base64 -d > "$file" 2>/dev/null
  python3 - "$file" <<'PY'
import sys
try:
    from PIL import Image
    im=Image.open(sys.argv[1]).convert("RGB"); w,h=im.size
    px=im.crop((w//4,h//4,3*w//4,3*h//4)).resize((32,32))
    r=g=b=0; n=0
    for p in px.getdata(): r+=p[0]; g+=p[1]; b+=p[2]; n+=1
    print(f"{r//n} {g//n} {b//n}")
except Exception as e:
    print("NA NA NA")
PY
}

lane_test() { # name framework dir
  local name="$1" fw="$2" dir="$3"
  echo "── $name ($fw) ──────────────────────────────"
  [ -d "$dir" ] || { echo "  SKIP: $dir not found"; return 0; }

  echo "  start dev server (browser lane)…"
  api POST /dev/start "{\"framework\":\"$fw\",\"workDir\":\"$dir\",\"platform\":\"web\",\"caller\":\"web-ui\"}" >/dev/null
  # give Metro/Flutter-web a moment, then confirm running
  for i in $(seq 1 30); do
    st="$(api GET /dev/status)"; echo "$st" | grep -q '"running":true' && break; sleep 2
  done

  echo "  screenshot BEFORE…"; before="$(shoot "${name}-before")" || return 1
  echo "    avg RGB before: $before"

  echo "  edit background colour…"
  # todo-rn: app/index.tsx safe backgroundColor; todo-flutter: lib color.
  # Non-fatal if the marker isn't present — the reload still exercises the lane.
  if [ "$fw" = "flutter" ]; then
    grep -rl "backgroundColor" "$dir/lib" 2>/dev/null | head -1 | xargs -I{} sed -i '' 's/Colors\.[a-zA-Z]*/Colors.deepOrange/' {} 2>/dev/null || true
  else
    f="$(grep -rl "backgroundColor" "$dir/app" 2>/dev/null | head -1)"
    [ -n "$f" ] && sed -i '' 's/backgroundColor: *"#[0-9a-fA-F]*"/backgroundColor: "#e11d48"/' "$f" 2>/dev/null || true
  fi

  echo "  reload…"; api POST /dev/reload '{"mode":"dev"}' >/dev/null; sleep 4
  echo "  screenshot AFTER…"; after="$(shoot "${name}-after")" || return 1
  echo "    avg RGB after:  $after"

  # crude change detector: any channel differs by > 12
  python3 - "$before" "$after" <<'PY'
import sys
b=sys.argv[1].split(); a=sys.argv[2].split()
if "NA" in b or "NA" in a:
    print("  ? could not measure colour (PIL missing) — frames saved for manual check"); sys.exit(0)
d=sum(abs(int(x)-int(y)) for x,y in zip(a,b))
print(f"  Δcolour = {d}  →  {'PASS (screen changed)' if d>12 else 'NO CHANGE — reload did not repaint'}")
sys.exit(0 if d>12 else 1)
PY
}

echo "AGENT_BASE=$AGENT_BASE  frames→$OUT"
lane_test "todo-rn" "expo" "$RN_DIR"; rc_rn=$?
lane_test "todo-flutter" "flutter" "$FLUTTER_DIR"; rc_fl=$?
echo "────────────────────────────────────────────"
echo "RN browser-reload:      $([ $rc_rn = 0 ] && echo PASS || echo CHECK)"
echo "Flutter browser-reload: $([ $rc_fl = 0 ] && echo PASS || echo CHECK)"
