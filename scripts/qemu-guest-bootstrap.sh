#!/usr/bin/env bash
set -euo pipefail

# qemu-guest-bootstrap.sh
#
# Runs *inside* a Linux QEMU guest after the host copies over:
# - a yaver binary
# - an env file
# - either a phone-export tgz or a wizard answers JSON
#
# This script intentionally stays generic:
# - it does not assume apt/brew/dnf availability
# - it does not auto-install the whole Android SDK
# - it allows the caller to inject build and AI commands
#
# The first goal is to prove the exported/scaffolded project can live on
# another clean machine and keep developing there.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RUN_ENV="${1:-$SCRIPT_DIR/run.env.sh}"

if [[ ! -f "$RUN_ENV" ]]; then
  echo "missing env file: $RUN_ENV" >&2
  exit 1
fi

# shellcheck source=/dev/null
source "$RUN_ENV"

MODE="${YAVER_QEMU_MODE:-remote-dev}"
SOURCE_KIND="${YAVER_QEMU_SOURCE:-phone-export}"
SESSION_ROOT="${YAVER_QEMU_SESSION_ROOT:-$HOME/.cache/yaver-qemu}"
TOOLS_DIR="$SESSION_ROOT/tools"
PAYLOAD_DIR="$SESSION_ROOT/payload"
WORK_ROOT="${YAVER_QEMU_WORK_ROOT:-$HOME/yaver-qemu-work}"
LOCAL_HOME="${YAVER_QEMU_HOME:-$HOME}"
RESULT_JSON="$SESSION_ROOT/result.json"
YAVER_BIN="$TOOLS_DIR/yaver"

mkdir -p "$TOOLS_DIR" "$PAYLOAD_DIR" "$WORK_ROOT"

log() {
  printf '[qemu-guest] %s\n' "$*"
}

fail() {
  printf '[qemu-guest FAIL] %s\n' "$*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

activate_managed_node() {
  local extras=()
  if [[ -d "$HOME/.yaver/runtimes/node/bin" ]]; then
    extras+=("$HOME/.yaver/runtimes/node/bin")
  fi
  if [[ -d "$HOME/.local/bin" ]]; then
    extras+=("$HOME/.local/bin")
  fi
  if [[ -d "$HOME/.npm-global/bin" ]]; then
    extras+=("$HOME/.npm-global/bin")
  fi
  if [[ ${#extras[@]} -gt 0 ]]; then
    local joined
    joined="$(IFS=:; printf '%s' "${extras[*]}")"
    export PATH="$joined:$PATH"
  fi
}

for tool in bash git tar python3; do
  require_cmd "$tool"
done

[[ -x "$YAVER_BIN" ]] || fail "missing uploaded yaver binary at $YAVER_BIN"

if [[ "${YAVER_QEMU_INSTALL_NODE:-0}" == "1" ]]; then
  log "installing managed node runtime"
  HOME="$LOCAL_HOME" "$YAVER_BIN" install node
fi

activate_managed_node

imported_slug=""
project_dir=""
generator_dir=""

case "$SOURCE_KIND" in
  phone-export)
    [[ -f "$PAYLOAD_DIR/project.tgz" ]] || fail "missing payload project.tgz"
    log "importing phone project bundle"
    import_output="$(
      HOME="$LOCAL_HOME" "$YAVER_BIN" phone import "$PAYLOAD_DIR/project.tgz" --conflict overwrite 2>&1
    )" || {
      printf '%s\n' "$import_output" >&2
      fail "phone import failed"
    }
    printf '%s\n' "$import_output"
    imported_slug="$(printf '%s\n' "$import_output" | awk '/Imported as /{print $3}' | tail -n1)"
    [[ -n "$imported_slug" ]] || fail "could not parse imported slug"
    project_dir="$LOCAL_HOME/.yaver/phone-projects/$imported_slug"
    [[ -d "$project_dir" ]] || fail "imported project dir missing: $project_dir"
    ;;
  wizard-quick)
    [[ -f "$PAYLOAD_DIR/answers.json" ]] || fail "missing payload answers.json"
    log "generating fullstack monorepo with yaver new --quick"
    generate_output="$(
      HOME="$LOCAL_HOME" "$YAVER_BIN" new --quick "$PAYLOAD_DIR/answers.json" "$WORK_ROOT" 2>&1
    )" || {
      printf '%s\n' "$generate_output" >&2
      fail "yaver new --quick failed"
    }
    printf '%s\n' "$generate_output"
    generator_dir="$(
      printf '%s\n' "$generate_output" | python3 -c 'import json,sys; print(json.load(sys.stdin)["directory"])'
    )" || fail "could not parse generated directory from quick scaffold output"
    [[ -d "$generator_dir" ]] || fail "generated directory missing: $generator_dir"
    project_dir="$generator_dir"
    ;;
  *)
    fail "unknown source kind: $SOURCE_KIND"
    ;;
esac

[[ -n "$project_dir" ]] || fail "project directory not resolved"

log "project directory: $project_dir"

if [[ -n "${YAVER_QEMU_PRE_BUILD_CMD:-}" ]]; then
  log "running pre-build command"
  (
    cd "$project_dir"
    bash -lc "$YAVER_QEMU_PRE_BUILD_CMD"
  )
fi

if [[ "$MODE" == "openai-key" ]]; then
  [[ -n "${OPENAI_API_KEY:-}" ]] || fail "OPENAI_API_KEY is required for openai-key mode"
  export OPENAI_API_KEY
  autodev_prompt="${YAVER_QEMU_AUTODEV_PROMPT:-stabilize the project, keep changes minimal, then leave the tree in a buildable state}"
  autodev_cmd="${YAVER_QEMU_AUTODEV_CMD:-$YAVER_BIN autodev --engine codex --hours 1 --max-iterations 1 --prompt \"$autodev_prompt\"}"
  log "running guest-side AI command"
  (
    cd "$project_dir"
    bash -lc "$autodev_cmd"
  )
fi

if [[ -n "${YAVER_QEMU_BUILD_CMD:-}" ]]; then
  log "running build command"
  (
    cd "$project_dir"
    bash -lc "$YAVER_QEMU_BUILD_CMD"
  )
else
  log "no build command supplied; skipping compile step"
fi

python3 - <<PY > "$RESULT_JSON"
import json
print(json.dumps({
  "ok": True,
  "mode": ${MODE@Q},
  "source": ${SOURCE_KIND@Q},
  "projectDir": ${project_dir@Q},
  "importedSlug": ${imported_slug@Q},
  "generatedDir": ${generator_dir@Q},
}, indent=2))
PY

cat "$RESULT_JSON"
