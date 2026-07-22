#!/usr/bin/env bash
set -euo pipefail

tmp="$(mktemp -d /tmp/yaver-browser-webrtc-page.XXXXXX)"

cat > "$tmp/index.html" <<'HTML'
<!doctype html>
<html>
  <head>
    <meta charset="utf-8">
    <title>Yaver WebRTC Todo Color Smoke</title>
    <style>
      html, body {
        margin: 0;
        width: 100%;
        height: 100%;
        background: rgb(220, 20, 20);
        font-family: system-ui, sans-serif;
      }
      body.done {
        background: rgb(20, 180, 60);
      }
      main {
        width: 100vw;
        height: 100vh;
        display: grid;
        place-items: center;
      }
      button {
        width: 320px;
        height: 96px;
        border: 0;
        border-radius: 8px;
        background: #ffffff;
        color: #111827;
        font-size: 22px;
        font-weight: 700;
      }
    </style>
  </head>
  <body>
    <main>
      <button id="todo" type="button">Ship WebRTC todo</button>
    </main>
    <script>
      document.getElementById("todo").addEventListener("click", () => {
        document.body.classList.add("done");
        document.getElementById("todo").textContent = "Done";
      });
    </script>
  </body>
</html>
HTML

cat > "$tmp/server.py" <<'PY'
import functools
import http.server
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
port_file = pathlib.Path(sys.argv[2])
handler = functools.partial(http.server.SimpleHTTPRequestHandler, directory=str(root))
server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
port_file.write_text(str(server.server_address[1]) + "\n")
port_file.chmod(0o600)
server.serve_forever()
PY

python3 "$tmp/server.py" "$tmp" "$tmp/server.port" > "$tmp/server.log" 2>&1 &
echo $! > "$tmp/server.pid"

pid="$(cat "$tmp/server.pid")"
for _ in $(seq 1 50); do
  if [[ -s "$tmp/server.port" ]]; then
    break
  fi
  if ! kill -0 "$pid" >/dev/null 2>&1; then
    echo "python http server exited early" >&2
    cat "$tmp/server.log" >&2 || true
    exit 1
  fi
  sleep 0.1
done

port="$(cat "$tmp/server.port" 2>/dev/null || true)"
if [[ -z "$port" ]]; then
  echo "could not discover python http server port" >&2
  cat "$tmp/server.log" >&2 || true
  exit 1
fi

jq -cn --arg tmp "$tmp" --arg pid "$pid" --arg url "http://127.0.0.1:$port/index.html" \
  '{tmp:$tmp,pid:($pid|tonumber),url:$url}'
