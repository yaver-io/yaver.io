#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

usage() {
  cat <<'EOF'
Usage:
  scripts/qemu-hermes-cycle.sh [options]

Options:
  --guest <user@host>         SSH target, default ubuntu@127.0.0.1
  --ssh-port <port>           SSH port, default 2223
  --identity <path>           SSH private key, default .tmp-qemu/x64-guest/id_ed25519
  --work-root <path>          Guest work root override
  --skip-vm                   Do not start/wait for the local VM first
EOF
}

log() {
  printf '[qemu-hermes] %s\n' "$*"
}

fail() {
  printf '[qemu-hermes FAIL] %s\n' "$*" >&2
  exit 1
}

guest="ubuntu@127.0.0.1"
ssh_port="2223"
identity="$ROOT_DIR/.tmp-qemu/x64-guest/id_ed25519"
work_root=""
skip_vm=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --guest)
      guest="${2:-}"
      shift 2
      ;;
    --ssh-port)
      ssh_port="${2:-}"
      shift 2
      ;;
    --identity)
      identity="${2:-}"
      shift 2
      ;;
    --work-root)
      work_root="${2:-}"
      shift 2
      ;;
    --skip-vm)
      skip_vm=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      fail "unknown argument: $1"
      ;;
  esac
done

if [[ -z "$work_root" ]]; then
  stamp="$(date +%Y%m%d-%H%M%S)"
  work_root="/home/ubuntu/yaver-qemu-hermes-${stamp}"
fi

if [[ "$skip_vm" != "1" && "$guest" == "ubuntu@127.0.0.1" ]]; then
  log "ensuring local x86_64 QEMU guest is running"
  "$SCRIPT_DIR/qemu-local-x64-vm.sh" init
  vm_pid_file="$ROOT_DIR/.tmp-qemu/x64-guest/qemu.pid"
  if [[ ! -f "$vm_pid_file" ]] || ! kill -0 "$(cat "$vm_pid_file")" 2>/dev/null; then
    "$SCRIPT_DIR/qemu-local-x64-vm.sh" start
  fi
  "$SCRIPT_DIR/qemu-local-x64-vm.sh" wait-ssh
fi

pre_build_cmd="$(cat <<'EOF'
python3 - <<'PY'
from pathlib import Path
path = Path("apps/mobile/App.tsx")
text = path.read_text()
old = '            MOBILE-FIRST STARTER'
new = '            MOBILE-FIRST STARTER · HERMES X64'
if old not in text:
    raise SystemExit("expected starter marker not found")
path.write_text(text.replace(old, new))
print("patched", path)
PY
python3 - <<'PY'
import json
import os
from pathlib import Path
cfg = {
    "auth_token": "qemu-hermes-offline-token",
    "device_id": "qemu-hermes-offline-device",
    "convex_site_url": "https://app.yaver.io",
    "web_base_url": "https://app.yaver.io",
}
cfg_dir = Path.home() / ".yaver"
cfg_dir.mkdir(parents=True, exist_ok=True)
(cfg_dir / "config.json").write_text(json.dumps(cfg, indent=2) + "\n")
print("wrote", cfg_dir / "config.json")
PY
EOF
)"

build_cmd="$(cat <<'EOF'
set -euo pipefail
export PATH="$HOME/.yaver/runtimes/node/bin:$HOME/.local/bin:$HOME/.npm-global/bin:$PATH"
mobile_dir="$PWD/apps/mobile"
node --version
npm --version
npm install
test -x "$YAVER_QEMU_SESSION_ROOT/tools/yaver"
timeout_cmd=""
if command -v timeout >/dev/null 2>&1; then
  timeout_cmd="timeout 240"
fi
mkdir -p .qemu-hermes
"$YAVER_QEMU_SESSION_ROOT/tools/yaver" serve --debug --port 18080 --no-quic --no-relay --no-tls --work-dir "$PWD" > .qemu-hermes/serve.log 2>&1 &
serve_pid=$!
cleanup() {
  if kill -0 "$serve_pid" 2>/dev/null; then
    kill "$serve_pid" || true
    wait "$serve_pid" || true
  fi
}
trap cleanup EXIT
for _ in $(seq 1 120); do
  if curl -sf http://127.0.0.1:18080/health >/dev/null; then
    break
  fi
  sleep 2
done
curl -sf http://127.0.0.1:18080/health >/dev/null
auth_header='Authorization: Bearer qemu-hermes-offline-token'
curl -sf -H "$auth_header" -H 'Content-Type: application/json' \
  -d '{"framework":"expo","workDir":"'"$mobile_dir"'","platform":"android"}' \
  http://127.0.0.1:18080/dev/start > .qemu-hermes/dev-start.json
$timeout_cmd curl -sf -H "$auth_header" -H 'Content-Type: application/json' \
  -d '{"platform":"android"}' \
  http://127.0.0.1:18080/dev/build-native > .qemu-hermes/build-native.json
python3 - <<'PY'
import json
from pathlib import Path
data = json.loads(Path(".qemu-hermes/build-native.json").read_text())
if data.get("status") != "ok":
    raise SystemExit(f"unexpected build-native payload: {data}")
if not data.get("bundleUrl"):
    raise SystemExit("bundleUrl missing from build-native response")
if not data.get("bcVersion"):
    raise SystemExit("bcVersion missing from build-native response")
print("build-native ok", data["bundleUrl"], data["bcVersion"])
PY
curl -sf -D .qemu-hermes/native-bundle.headers http://127.0.0.1:18080/dev/native-bundle -o .qemu-hermes/native-bundle.hbc
python3 - <<'PY'
import json
from pathlib import Path
bundle = Path(".qemu-hermes/native-bundle.hbc").read_bytes()
if len(bundle) < 12:
    raise SystemExit(f"bundle too short: {len(bundle)}")
magic = int.from_bytes(bundle[4:8], "little")
bc = int.from_bytes(bundle[8:12], "little")
if magic != 0x1F1903C1:
    raise SystemExit(f"unexpected Hermes magic: 0x{magic:08X}")
build = json.loads(Path(".qemu-hermes/build-native.json").read_text())
if bc != build["bcVersion"]:
    raise SystemExit(f"bundle BC mismatch: got {bc}, expected {build['bcVersion']}")
headers = Path(".qemu-hermes/native-bundle.headers").read_text()
if "X-Yaver-Bundle-Metadata:" not in headers and "x-yaver-bundle-metadata:" not in headers:
    raise SystemExit("bundle metadata header missing")
print("bundle validated", len(bundle), bc)
PY
test -f apps/mobile/.yaver-build/main.jsbundle
test -d apps/mobile/.yaver-build/assets
grep -q 'HERMES X64' apps/mobile/App.tsx
echo hermes-cycle-ok
EOF
)"

log "running Hermes bundle cycle in x86_64 QEMU"
"$SCRIPT_DIR/qemu-phone-fullstack.sh" \
  --guest "$guest" \
  --ssh-port "$ssh_port" \
  --identity "$identity" \
  --mode remote-dev \
  --source wizard-quick \
  --answers-json "$ROOT_DIR/demos/bento/.yaver-wizard-answers.json" \
  --guest-work-root "$work_root" \
  --guest-goarch amd64 \
  --install-node \
  --pre-build-cmd "$pre_build_cmd" \
  --build-cmd "$build_cmd"
