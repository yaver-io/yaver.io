#!/usr/bin/env bash
set -euo pipefail

# qemu-phone-fullstack.sh
#
# Host-side orchestrator for QEMU-backed phone-first fullstack testing.
#
# It supports two execution modes:
# - remote-dev  : treat the guest as the clean "other machine"
# - openai-key  : same, but also run a guest-side AI/autodev pass
#
# It supports two source shapes:
# - phone-export: export a phone project tgz and import it in the guest
# - wizard-quick: copy a wizard answers JSON and generate a monorepo in the guest
#
# This is the first harness layer, not the final product abstraction.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
GUEST_BOOTSTRAP_LOCAL="$SCRIPT_DIR/qemu-guest-bootstrap.sh"

usage() {
  cat <<'EOF'
Usage:
  scripts/qemu-phone-fullstack.sh --guest user@host [options]

Required:
  --guest <user@host>                 SSH target for the QEMU guest

Source selection:
  --source phone-export              Export a phone project and import it in guest
  --phone-slug <slug>                Required for --source phone-export

  --source wizard-quick              Generate a monorepo from wizard answers in guest
  --answers-json <path>              Required for --source wizard-quick

Modes:
  --mode remote-dev                  Guest acts as remote continuation machine
  --mode openai-key                  Guest also runs an AI/autodev pass

Optional:
  --ssh-port <port>                  SSH port, default 22
  --identity <path>                  SSH identity file
  --guest-work-root <path>           Guest work root, default ~/yaver-qemu-work
  --guest-goos <os>                  Guest GOOS for yaver binary, default linux
  --guest-goarch <arch>              Guest GOARCH for yaver binary, default arm64
  --install-node                     Run `yaver install node` inside the guest before build steps
  --install-codex                    Run `npm install -g @openai/codex` inside the guest before AI steps
  --include-data                     Include local.db in phone export
  --containerize                     Include Docker/compose scaffold in phone export
  --build-cmd <cmd>                  Command to run inside guest project dir
  --pre-build-cmd <cmd>              Command to run before AI/build in guest dir
  --autodev-prompt <text>            Prompt used in openai-key mode
  --autodev-cmd <cmd>                Full guest-side AI command override
  --local-yaver <path>               Reuse an existing local yaver binary

Environment:
  OPENAI_API_KEY                     Required for --mode openai-key

Examples:
  scripts/qemu-phone-fullstack.sh \
    --guest ubuntu@127.0.0.1 --ssh-port 2222 \
    --mode remote-dev \
    --source phone-export --phone-slug my-todos \
    --include-data --containerize \
    --build-cmd 'ls -la && test -f schema.yaml'

  scripts/qemu-phone-fullstack.sh \
    --guest ubuntu@127.0.0.1 --ssh-port 2222 \
    --mode openai-key \
    --source wizard-quick --answers-json demos/bento/.yaver-wizard-answers.json \
    --build-cmd 'npm install && npm test' \
    --autodev-prompt 'stabilize onboarding and keep builds green'
EOF
}

log() {
  printf '[qemu-host] %s\n' "$*"
}

fail() {
  printf '[qemu-host FAIL] %s\n' "$*" >&2
  exit 1
}

quote() {
  printf '%q' "$1"
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

mode="remote-dev"
source_kind=""
guest=""
ssh_port="22"
identity=""
guest_goos="linux"
guest_goarch="arm64"
answers_json=""
phone_slug=""
guest_work_root=""
install_node=0
install_codex=0
build_cmd=""
pre_build_cmd=""
autodev_prompt=""
autodev_cmd=""
include_data=0
containerize=0
local_yaver=""

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
    --guest-goos)
      guest_goos="${2:-}"
      shift 2
      ;;
    --guest-goarch)
      guest_goarch="${2:-}"
      shift 2
      ;;
    --mode)
      mode="${2:-}"
      shift 2
      ;;
    --source)
      source_kind="${2:-}"
      shift 2
      ;;
    --answers-json)
      answers_json="${2:-}"
      shift 2
      ;;
    --phone-slug)
      phone_slug="${2:-}"
      shift 2
      ;;
    --guest-work-root)
      guest_work_root="${2:-}"
      shift 2
      ;;
    --install-node)
      install_node=1
      shift
      ;;
    --install-codex)
      install_codex=1
      shift
      ;;
    --build-cmd)
      build_cmd="${2:-}"
      shift 2
      ;;
    --pre-build-cmd)
      pre_build_cmd="${2:-}"
      shift 2
      ;;
    --autodev-prompt)
      autodev_prompt="${2:-}"
      shift 2
      ;;
    --autodev-cmd)
      autodev_cmd="${2:-}"
      shift 2
      ;;
    --include-data)
      include_data=1
      shift
      ;;
    --containerize)
      containerize=1
      shift
      ;;
    --local-yaver)
      local_yaver="${2:-}"
      shift 2
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

[[ -n "$guest" ]] || fail "--guest is required"
[[ "$mode" == "remote-dev" || "$mode" == "openai-key" ]] || fail "--mode must be remote-dev or openai-key"
[[ "$source_kind" == "phone-export" || "$source_kind" == "wizard-quick" ]] || fail "--source must be phone-export or wizard-quick"

if [[ "$source_kind" == "phone-export" ]]; then
  [[ -n "$phone_slug" ]] || fail "--phone-slug is required for phone-export"
fi
if [[ "$source_kind" == "wizard-quick" ]]; then
  [[ -n "$answers_json" ]] || fail "--answers-json is required for wizard-quick"
  [[ -f "$answers_json" ]] || fail "answers json not found: $answers_json"
fi
if [[ "$mode" == "openai-key" ]]; then
  [[ -n "${OPENAI_API_KEY:-}" ]] || fail "OPENAI_API_KEY is required for openai-key mode"
fi

require_cmd ssh
require_cmd scp
require_cmd mktemp

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/yaver-qemu-XXXXXX")"
trap 'rm -rf "$TMP_DIR"' EXIT

SSH_OPTS=(-p "$ssh_port" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null)
SCP_OPTS=(-P "$ssh_port" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null)
if [[ -n "$identity" ]]; then
  SSH_OPTS+=(-i "$identity")
  SCP_OPTS+=(-i "$identity")
fi

remote_home="$(ssh "${SSH_OPTS[@]}" "$guest" 'printf %s "$HOME"')"
[[ -n "$remote_home" ]] || fail "could not resolve remote home directory"
REMOTE_STAGE="$remote_home/.cache/yaver-qemu-stage"

if [[ -n "$local_yaver" ]]; then
  [[ -x "$local_yaver" ]] || fail "local yaver binary not executable: $local_yaver"
  LOCAL_YAVER="$local_yaver"
else
  LOCAL_YAVER="$TMP_DIR/yaver"
  log "building guest yaver binary for $guest_goos/$guest_goarch"
  (
    cd "$ROOT_DIR/desktop/agent"
    GOOS="$guest_goos" GOARCH="$guest_goarch" go build -o "$LOCAL_YAVER" .
  )
fi

PAYLOAD_DIR="$TMP_DIR/payload"
mkdir -p "$PAYLOAD_DIR"

case "$source_kind" in
  phone-export)
    export_args=(phone export "$phone_slug" --out "$PAYLOAD_DIR/project.tgz")
    if [[ "$include_data" -eq 1 ]]; then
      export_args+=(--include-data)
    fi
    if [[ "$containerize" -eq 1 ]]; then
      export_args+=(--containerize)
    fi
    log "exporting phone project: $phone_slug"
    "$LOCAL_YAVER" "${export_args[@]}"
    ;;
  wizard-quick)
    cp "$answers_json" "$PAYLOAD_DIR/answers.json"
    ;;
esac

ENV_FILE="$TMP_DIR/run.env.sh"
{
  printf 'export YAVER_QEMU_MODE=%s\n' "$(quote "$mode")"
  printf 'export YAVER_QEMU_SOURCE=%s\n' "$(quote "$source_kind")"
  if [[ -n "$guest_work_root" ]]; then
    printf 'export YAVER_QEMU_WORK_ROOT=%s\n' "$(quote "$guest_work_root")"
  fi
  printf 'export YAVER_QEMU_INSTALL_NODE=%s\n' "$(quote "$install_node")"
  printf 'export YAVER_QEMU_INSTALL_CODEX=%s\n' "$(quote "$install_codex")"
  printf 'export YAVER_QEMU_PRE_BUILD_CMD=%s\n' "$(quote "$pre_build_cmd")"
  printf 'export YAVER_QEMU_BUILD_CMD=%s\n' "$(quote "$build_cmd")"
  printf 'export YAVER_QEMU_AUTODEV_PROMPT=%s\n' "$(quote "$autodev_prompt")"
  printf 'export YAVER_QEMU_AUTODEV_CMD=%s\n' "$(quote "$autodev_cmd")"
  if [[ "$mode" == "openai-key" ]]; then
    printf 'export OPENAI_API_KEY=%s\n' "$(quote "${OPENAI_API_KEY}")"
  fi
} > "$ENV_FILE"

log "preparing guest stage directory"
ssh "${SSH_OPTS[@]}" "$guest" "rm -rf $REMOTE_STAGE && mkdir -p $REMOTE_STAGE/tools $REMOTE_STAGE/payload"

log "copying harness files to guest"
scp "${SCP_OPTS[@]}" "$LOCAL_YAVER" "$guest:$REMOTE_STAGE/tools/yaver" >/dev/null
scp "${SCP_OPTS[@]}" "$GUEST_BOOTSTRAP_LOCAL" "$guest:$REMOTE_STAGE/qemu-guest-bootstrap.sh" >/dev/null
scp "${SCP_OPTS[@]}" "$ENV_FILE" "$guest:$REMOTE_STAGE/run.env.sh" >/dev/null
if [[ "$source_kind" == "phone-export" ]]; then
  scp "${SCP_OPTS[@]}" "$PAYLOAD_DIR/project.tgz" "$guest:$REMOTE_STAGE/payload/project.tgz" >/dev/null
else
  scp "${SCP_OPTS[@]}" "$PAYLOAD_DIR/answers.json" "$guest:$REMOTE_STAGE/payload/answers.json" >/dev/null
fi

log "running guest bootstrap"
ssh "${SSH_OPTS[@]}" "$guest" "chmod +x $REMOTE_STAGE/tools/yaver $REMOTE_STAGE/qemu-guest-bootstrap.sh && YAVER_QEMU_SESSION_ROOT=$REMOTE_STAGE bash $REMOTE_STAGE/qemu-guest-bootstrap.sh $REMOTE_STAGE/run.env.sh"
