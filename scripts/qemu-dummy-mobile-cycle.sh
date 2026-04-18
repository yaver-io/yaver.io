#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

usage() {
  cat <<'EOF'
Usage:
  scripts/qemu-dummy-mobile-cycle.sh [options]

Options:
  --guest <user@host>         SSH target, default ubuntu@127.0.0.1
  --ssh-port <port>           SSH port, default 2222
  --identity <path>           SSH private key, default .tmp-qemu/arm64-guest/id_ed25519
  --mode <mode>               auto | remote-dev | openai-key, default auto
  --work-root <path>          Guest work root override
  --skip-vm                   Do not start/wait for the local VM first
EOF
}

log() {
  printf '[qemu-dummy] %s\n' "$*"
}

fail() {
  printf '[qemu-dummy FAIL] %s\n' "$*" >&2
  exit 1
}

guest="ubuntu@127.0.0.1"
ssh_port="2222"
identity="$ROOT_DIR/.tmp-qemu/arm64-guest/id_ed25519"
mode="auto"
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
    --mode)
      mode="${2:-}"
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

case "$mode" in
  auto)
    if [[ -n "${OPENAI_API_KEY:-}" ]]; then
      run_mode="openai-key"
    else
      run_mode="remote-dev"
    fi
    ;;
  remote-dev|openai-key)
    run_mode="$mode"
    ;;
  *)
    fail "--mode must be auto, remote-dev, or openai-key"
    ;;
esac

if [[ -z "$work_root" ]]; then
  stamp="$(date +%Y%m%d-%H%M%S)"
  suffix="${run_mode//-/_}"
  work_root="/home/ubuntu/yaver-qemu-dummy-${suffix}-${stamp}"
fi

if [[ "$skip_vm" != "1" && "$guest" == "ubuntu@127.0.0.1" ]]; then
  log "ensuring local QEMU guest is running"
  "$SCRIPT_DIR/qemu-local-arm64-vm.sh" init
  vm_pid_file="$ROOT_DIR/.tmp-qemu/arm64-guest/qemu.pid"
  if [[ ! -f "$vm_pid_file" ]] || ! kill -0 "$(cat "$vm_pid_file")" 2>/dev/null; then
    "$SCRIPT_DIR/qemu-local-arm64-vm.sh" start
  fi
  "$SCRIPT_DIR/qemu-local-arm64-vm.sh" wait-ssh
fi

pre_build_cmd="$(cat <<'EOF'
python3 - <<'PY'
from pathlib import Path
path = Path("apps/mobile/App.tsx")
text = path.read_text()
old = '            MOBILE-FIRST STARTER'
new = '            MOBILE-FIRST STARTER · QEMU LOOP'
if old not in text:
    raise SystemExit("expected starter marker not found")
path.write_text(text.replace(old, new))
print("patched", path)
PY
EOF
)"

build_cmd="$(cat <<'EOF'
set -e
export PATH="$HOME/.yaver/runtimes/node/bin:$HOME/.local/bin:$HOME/.npm-global/bin:$PATH"
node --version
npm --version
npm install
mkdir -p "$HOME/.local/lib"
(
  cd apps/mobile
  ../../node_modules/.bin/expo export --platform android --no-bytecode --output-dir ../../.qemu-expo-export
)
test -d .qemu-expo-export
test -f apps/mobile/App.tsx
grep -q 'QEMU LOOP' apps/mobile/App.tsx
echo dummy-mobile-cycle-ok
EOF
)"

autodev_prompt="Fix the mobile timer bug in apps/mobile/app/components/CookTimer.tsx by replacing the unsafe undefined duration handling with a sensible fallback, then leave the project typecheckable."

log "running dummy mobile cycle in $run_mode mode"
args=(
  --guest "$guest"
  --ssh-port "$ssh_port"
  --identity "$identity"
  --mode "$run_mode"
  --source wizard-quick
  --answers-json "$ROOT_DIR/demos/bento/.yaver-wizard-answers.json"
  --guest-work-root "$work_root"
  --install-node
  --build-cmd "$build_cmd"
)

if [[ "$run_mode" == "remote-dev" ]]; then
  args+=(--pre-build-cmd "$pre_build_cmd")
else
  args+=(--autodev-prompt "$autodev_prompt")
fi

"$SCRIPT_DIR/qemu-phone-fullstack.sh" "${args[@]}"
