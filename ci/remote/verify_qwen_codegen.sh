#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="${REPO_DIR:-/opt/yaver}"
MODEL="${MODEL:-qwen2.5-coder:1.5b}"
LOG_DIR="/var/log/yaver-ci"
mkdir -p "$LOG_DIR"
LOG_FILE="${LOG_DIR}/verify-qwen-codegen.log"
exec > >(tee -a "$LOG_FILE") 2>&1

tmp_dir="$(mktemp -d -t yaver-qwen-codegen-XXXXXX)"
started_ollama=false

cleanup() {
  if $started_ollama; then
    pkill -f "^ollama serve$" 2>/dev/null || true
  fi
  rm -rf "$tmp_dir"
}
trap cleanup EXIT

log() {
  printf '\033[36m[qwen-codegen]\033[0m %s\n' "$*"
}

fail() {
  printf '\033[31m[qwen-codegen FAIL]\033[0m %s\n' "$*" >&2
  exit 1
}

ensure_ollama() {
  if ! curl -fsS http://127.0.0.1:11434/api/tags >/dev/null 2>&1; then
    log "starting ollama daemon"
    ollama serve >/tmp/yaver-qwen-codegen-ollama.log 2>&1 &
    started_ollama=true
    for _ in $(seq 1 20); do
      if curl -fsS http://127.0.0.1:11434/api/tags >/dev/null 2>&1; then
        break
      fi
      sleep 1
    done
  fi
  curl -fsS http://127.0.0.1:11434/api/tags >/dev/null 2>&1 \
    || fail "ollama daemon never became ready"
  if ! ollama list 2>/dev/null | grep -q "^${MODEL}[[:space:]]"; then
    log "pulling ${MODEL}"
    ollama pull "$MODEL"
  fi
}

extract_python() {
  python3 -c '
import re, sys
text = sys.stdin.read()
m = re.search(r"```(?:python)?\n(.*?)```", text, re.S | re.I)
if m:
    print(m.group(1).strip())
    raise SystemExit(0)
for line in text.splitlines():
    s = line.strip()
    if s.startswith("print("):
        print(s)
        raise SystemExit(0)
raise SystemExit(1)
'
}

run_attempt() {
  local attempt="$1"
  local prompt raw code output script_path
  prompt="Write a single-line Python program that prints exactly hello yaver. Output only code."
  log "attempt ${attempt}: prompting ${MODEL}"
  raw="$(ollama run "${MODEL}" "${prompt}" </dev/null || true)"
  printf '%s\n' "$raw" > "${tmp_dir}/raw-${attempt}.txt"

  if ! code="$(printf '%s' "$raw" | extract_python)"; then
    log "attempt ${attempt}: could not extract python from model output"
    tail -n 20 "${tmp_dir}/raw-${attempt}.txt" || true
    return 1
  fi

  script_path="${tmp_dir}/hello_yaver_${attempt}.py"
  printf '%s\n' "$code" > "$script_path"
  output="$(python3 "$script_path" 2>&1 || true)"
  printf '%s\n' "$output" > "${tmp_dir}/run-${attempt}.txt"
  if printf '%s' "$output" | grep -q "hello yaver"; then
    log "attempt ${attempt}: generated code executed successfully"
    return 0
  fi

  log "attempt ${attempt}: generated code ran but output was unexpected"
  cat "${tmp_dir}/run-${attempt}.txt"
  return 1
}

log "repo dir: ${REPO_DIR}"
ensure_ollama

for attempt in 1 2; do
  if run_attempt "$attempt"; then
    printf '\033[32m[qwen-codegen PASS]\033[0m %s\n' "model generated runnable hello_yaver python"
    exit 0
  fi
done

fail "model did not produce runnable hello_yaver code after 2 attempts"
