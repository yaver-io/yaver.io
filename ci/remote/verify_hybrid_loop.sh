#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="${REPO_DIR:-/opt/yaver}"
MODEL="${MODEL:-qwen2.5-coder:1.5b}"
LITELLM_MODEL="ollama_chat/${MODEL}"
OLLAMA_URL="${OLLAMA_URL:-http://127.0.0.1:11434}"
LOG_DIR="/var/log/yaver-ci"
mkdir -p "$LOG_DIR"
LOG_FILE="${LOG_DIR}/verify-hybrid-loop.log"
exec > >(tee -a "$LOG_FILE") 2>&1

tmp_root="$(mktemp -d -t yaver-remote-hybrid-XXXXXX)"
work_dir="${tmp_root}/work"
stub_dir="${tmp_root}/stub"
mkdir -p "$work_dir" "$stub_dir"
started_ollama=false
export PATH="/usr/local/go/bin:$PATH"

cleanup() {
  if $started_ollama; then
    pkill -f "^ollama serve$" 2>/dev/null || true
  fi
  rm -rf "$tmp_root"
}
trap cleanup EXIT

log() {
  printf '\033[36m[hybrid-loop]\033[0m %s\n' "$*"
}

fail() {
  printf '\033[31m[hybrid-loop FAIL]\033[0m %s\n' "$*" >&2
  exit 1
}

ensure_ollama() {
  if ! curl -fsS "${OLLAMA_URL}/api/tags" >/dev/null 2>&1; then
    log "starting ollama daemon"
    ollama serve >/tmp/yaver-hybrid-loop-ollama.log 2>&1 &
    started_ollama=true
    for _ in $(seq 1 20); do
      if curl -fsS "${OLLAMA_URL}/api/tags" >/dev/null 2>&1; then
        break
      fi
      sleep 1
    done
  fi
  curl -fsS "${OLLAMA_URL}/api/tags" >/dev/null 2>&1 \
    || fail "ollama daemon never became ready"
  if ! curl -fsS "${OLLAMA_URL}/api/tags" | grep -q "\"${MODEL}\""; then
    log "pulling ${MODEL}"
    ollama pull "${MODEL}"
  fi
}

YAVER_BIN="${YAVER_BIN:-$(command -v yaver || true)}"
[ -n "$YAVER_BIN" ] || fail "yaver binary not found on PATH"
command -v aider >/dev/null 2>&1 || fail "aider not found on PATH"

ensure_ollama

cat > "${stub_dir}/claude" <<'STUB'
#!/usr/bin/env bash
cat <<'PLAN'
{"subtasks":[
  {"title":"Create calc.py","files":["calc.py"],"prompt":"Create a file named calc.py at the project root. It should contain Python functions add(a,b), sub(a,b), mul(a,b), and div(a,b). Keep it minimal. Make div raise ValueError(\"division by zero\") when b == 0."}
]}
PLAN
STUB
chmod +x "${stub_dir}/claude"

cd "$work_dir"
git init -q .
git config user.email "ci@yaver.test"
git config user.name "yaver-ci"
git commit --allow-empty -qm "init"

log "running hybrid loop"
log "  planner: stub claude"
log "  implementer: aider-ollama with ${LITELLM_MODEL}"

PATH="${stub_dir}:$PATH" \
OLLAMA_API_BASE="${OLLAMA_URL}" \
"${YAVER_BIN}" hybrid \
  --planner claude \
  --implementer aider-ollama \
  --model "${LITELLM_MODEL}" \
  --base-url "${OLLAMA_URL}" \
  --workdir "${work_dir}" \
  --timeout 20m \
  "Write a calculator module" 2>&1 | tee "${tmp_root}/hybrid-run.log"

[ -s "${work_dir}/calc.py" ] || fail "hybrid run completed but calc.py was not created"
grep -q "def add" "${work_dir}/calc.py" || fail "calc.py exists but does not contain add()"

if git status --short | grep -q "calc.py"; then
  log "git reports calc.py as a new file"
else
  fail "hybrid run did not leave a visible file change"
fi

printf '\033[32m[hybrid-loop PASS]\033[0m %s\n' "hybrid planner/implementer loop created calc.py"
