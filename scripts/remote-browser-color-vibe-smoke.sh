#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  remote-browser-color-vibe-smoke.sh \
    --agent http://127.0.0.1:18080 \
    --token <bearer>

Creates a tiny local HTML page on the remote host. The page starts red and turns
green on click. The smoke opens it in a browser-window remote-runtime session,
sends control commands through the agent, fetches frames, and asserts the image
pixels changed from red-dominant to green-dominant.
EOF
}

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required tool: $1" >&2
    exit 2
  }
}

AGENT=""
TOKEN=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --agent) AGENT="${2:-}"; shift 2 ;;
    --token) TOKEN="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown arg: $1" >&2; usage; exit 2 ;;
  esac
done

[[ -n "$AGENT" && -n "$TOKEN" ]] || {
  usage
  exit 2
}

need curl
need jq
need python3

auth=(-H "Authorization: Bearer $TOKEN")

ensure_pillow() {
  if python3 - <<'PY' >/dev/null 2>&1
from PIL import Image
PY
  then
    return
  fi
  echo "== installing pillow for frame pixel assertions =="
  python3 -m pip install --user --quiet pillow
}

avg_rgb() {
  local image_path="$1"
  python3 - "$image_path" <<'PY'
from PIL import Image
import sys

img = Image.open(sys.argv[1]).convert("RGB")
w, h = img.size
box = (
    max(0, w // 2 - 120),
    max(0, h // 2 - 120),
    min(w, w // 2 + 120),
    min(h, h // 2 + 120),
)
crop = img.crop(box)
pixels = list(crop.getdata())
n = max(1, len(pixels))
r = sum(p[0] for p in pixels) // n
g = sum(p[1] for p in pixels) // n
b = sum(p[2] for p in pixels) // n
print(f"{r} {g} {b}")
PY
}

assert_red() {
  local label="$1" r="$2" g="$3" b="$4"
  echo "$label rgb=$r,$g,$b"
  if (( r < 150 || g > 110 || b > 110 || r <= g + 40 )); then
    echo "$label frame is not red-dominant" >&2
    exit 1
  fi
}

assert_green() {
  local label="$1" r="$2" g="$3" b="$4"
  echo "$label rgb=$r,$g,$b"
  if (( g < 130 || r > 130 || b > 130 || g <= r + 40 )); then
    echo "$label frame is not green-dominant" >&2
    exit 1
  fi
}

ensure_pillow

tmp="$(mktemp -d /tmp/yaver-browser-color.XXXXXX)"
server_pid=""
session_id=""
cleanup() {
  if [[ -n "$session_id" ]]; then
    curl -fsS "${auth[@]}" -X DELETE "$AGENT/remote-runtime/sessions/$session_id" >/dev/null || true
  fi
  if [[ -n "$server_pid" ]]; then
    kill "$server_pid" >/dev/null 2>&1 || true
  fi
  rm -rf "$tmp"
}
trap cleanup EXIT

cat > "$tmp/index.html" <<'HTML'
<!doctype html>
<html>
  <head>
    <meta charset="utf-8">
    <title>Yaver Browser Color Vibe Smoke</title>
    <style>
      html, body {
        margin: 0;
        width: 100%;
        height: 100%;
        background: rgb(220, 20, 20);
      }
      body.green {
        background: rgb(20, 180, 60);
      }
      main {
        width: 100vw;
        height: 100vh;
      }
    </style>
  </head>
  <body>
    <main aria-label="click target"></main>
    <script>
      document.addEventListener("click", () => {
        document.body.classList.add("green");
      });
    </script>
  </body>
</html>
HTML

(
  cd "$tmp"
  python3 -m http.server 0 --bind 127.0.0.1 > "$tmp/server.log" 2>&1 &
  echo $! > "$tmp/server.pid"
)
server_pid="$(cat "$tmp/server.pid")"

for _ in $(seq 1 50); do
  if grep -Eo 'Serving HTTP on 127\.0\.0\.1 port [0-9]+' "$tmp/server.log" >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "$server_pid" >/dev/null 2>&1; then
    echo "python http server exited early" >&2
    cat "$tmp/server.log" >&2 || true
    exit 1
  fi
  sleep 0.1
done
port="$(grep -Eo 'Serving HTTP on 127\.0\.0\.1 port [0-9]+' "$tmp/server.log" | tail -1 | awk '{print $NF}')"
[[ -n "$port" ]] || {
  echo "could not discover python http server port" >&2
  cat "$tmp/server.log" >&2 || true
  exit 1
}
url="http://127.0.0.1:$port/index.html"

echo "== agent info =="
curl -fsS "${auth[@]}" "$AGENT/info" | jq '{hostname, version, workDir}'

echo "== browser runtime capabilities =="
caps="$(curl -fsS "${auth[@]}" "$AGENT/remote-runtime/capabilities?framework=browser&workDir=$tmp")"
echo "$caps" | jq '{executionMode, targets: [.targets[] | {id, enabled, reason}]}'
if [[ "$(echo "$caps" | jq -r '.targets[] | select(.id=="browser-window") | .enabled')" != "true" ]]; then
  echo "browser-window target is not enabled on this host" >&2
  exit 1
fi

echo "== create browser-window session =="
session_json="$(curl -fsS "${auth[@]}" \
  -H 'Content-Type: application/json' \
  -d "$(jq -cn --arg workDir "$tmp" '{workDir:$workDir, framework:"browser", targetId:"browser-window", transportMode:"relay-jpeg-poll"}')" \
  "$AGENT/remote-runtime/sessions")"
echo "$session_json" | jq '{id, targetId, deviceId, status, note, transportMode, frameTransport}'
session_id="$(echo "$session_json" | jq -r '.id')"
[[ -n "$session_id" && "$session_id" != "null" ]] || {
  echo "browser session creation failed" >&2
  exit 1
}

echo "== navigate browser to color page =="
curl -fsS "${auth[@]}" \
  -H 'Content-Type: application/json' \
  -d "$(jq -cn --arg url "$url" '{action:"navigate", url:$url, clientId:"ci-browser-color", clientLabel:"CI browser color smoke"}')" \
  "$AGENT/remote-runtime/sessions/$session_id/control" | jq '{ok, session: {status: .session.status, lastCommand: .session.lastCommand, note: .session.note}}'
sleep 1

before="$tmp/before.jpg"
after="$tmp/after.jpg"
curl -fsS "${auth[@]}" "$AGENT/remote-runtime/sessions/$session_id/frame" -o "$before"
read -r br bg bb < <(avg_rgb "$before")
assert_red "before-click" "$br" "$bg" "$bb"

echo "== tap page through remote-runtime control =="
curl -fsS "${auth[@]}" \
  -H 'Content-Type: application/json' \
  -d '{"action":"tap","x":640,"y":400,"clientId":"ci-browser-color","clientLabel":"CI browser color smoke"}' \
  "$AGENT/remote-runtime/sessions/$session_id/control" | jq '{ok, session: {status: .session.status, lastCommand: .session.lastCommand, note: .session.note}}'
sleep 1

curl -fsS "${auth[@]}" "$AGENT/remote-runtime/sessions/$session_id/frame" -o "$after"
read -r ar ag ab < <(avg_rgb "$after")
assert_green "after-click" "$ar" "$ag" "$ab"

echo "browser color vibe smoke ok"
