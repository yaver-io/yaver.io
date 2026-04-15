#!/usr/bin/env bash
# End-to-end test for yaver's hybrid mode using ONLY local tooling:
# aider + ollama + a small Qwen model. No frontier-API keys required.
#
# The test bypasses the real planner (which would need Claude / Codex
# access) by pointing yaver at a bash-script stub that emits a canned
# plan for writing a calculator module. That plan is then handed to
# the real implementer pipeline (Aider + Qwen via Ollama), so what
# actually runs is the *implementation* end of the hybrid loop — the
# part that matters for the "$0 per feature" claim.
#
# After the run we import calc.py and assert add/sub/mul/div behave.
# Any failure — the model never wrote the file, the file has a syntax
# error, a function is missing, or the division-by-zero guard is gone
# — fails the test.
#
# Prerequisites (install via `yaver install hybrid`):
#   - aider on PATH
#   - ollama daemon running (or startable via `ollama serve`)
#   - at least one qwen2.5-coder model pulled
#
# Usage:
#   scripts/test-hybrid-local.sh              # default model
#   MODEL=qwen2.5-coder:7b scripts/test-hybrid-local.sh
#
# Designed to be cheap enough to run on GitHub Actions' free tier
# (with the 1.5b model, ~1 GB download, fits in 7 GB RAM).

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODEL="${MODEL:-qwen2.5-coder:1.5b}"
LITELLM_MODEL="ollama_chat/${MODEL}"
OLLAMA_URL="${OLLAMA_URL:-http://127.0.0.1:11434}"
WORK_DIR="$(mktemp -d -t yaver-hybrid-XXXXXX)"
STUB_DIR="$(mktemp -d -t yaver-hybrid-stub-XXXXXX)"
YAVER_BIN="${YAVER_BIN:-}"

cleanup() {
  rm -rf "$WORK_DIR" "$STUB_DIR"
}
trap cleanup EXIT

log() { echo -e "\033[36m[hybrid-local]\033[0m $*"; }
fail() { echo -e "\033[31m[hybrid-local FAIL]\033[0m $*" >&2; exit 1; }

# ── Build yaver if the caller didn't pre-build one ──────────────────
if [[ -z "${YAVER_BIN}" ]]; then
  log "Building yaver binary..."
  pushd "${REPO_ROOT}/desktop/agent" >/dev/null
  go build -o "$STUB_DIR/yaver" .
  popd >/dev/null
  YAVER_BIN="$STUB_DIR/yaver"
fi

# ── Dependency preflight — fail fast with actionable hints ──────────
log "Preflight..."
command -v aider >/dev/null || fail "aider not on PATH. Run: $YAVER_BIN install aider"

# Start ollama if it's not already listening. Detaches so the script
# can exit cleanly; the caller is responsible for stopping it outside
# CI (in CI the runner dies anyway).
if ! curl -fsS "${OLLAMA_URL}/api/tags" >/dev/null 2>&1; then
  log "Starting ollama daemon..."
  command -v ollama >/dev/null || fail "ollama not on PATH. Run: $YAVER_BIN install ollama"
  ollama serve >/dev/null 2>&1 &
  for i in {1..20}; do
    if curl -fsS "${OLLAMA_URL}/api/tags" >/dev/null 2>&1; then break; fi
    sleep 1
  done
  curl -fsS "${OLLAMA_URL}/api/tags" >/dev/null 2>&1 \
    || fail "ollama daemon never came up at $OLLAMA_URL"
fi

# Pull model if missing. This is the slow step on a clean runner.
if ! curl -fsS "${OLLAMA_URL}/api/tags" | grep -q "\"$MODEL\""; then
  log "Pulling $MODEL (this may take a few minutes on a cold runner)..."
  ollama pull "$MODEL"
fi

# ── Canned plan: create calc.py with four functions + guard ─────────
# The implementer is intentionally told EVERYTHING it needs to know —
# exact identifiers, signature, docstring-free, specific error type.
# This is the contract the planner *should* produce; we hard-code it
# here so the test doesn't depend on Claude/Codex being available.
CALC_PROMPT='Create a new file calc.py at the project root with EXACTLY these four top-level functions and nothing else:

```python
def add(a, b):
    return a + b

def sub(a, b):
    return a - b

def mul(a, b):
    return a * b

def div(a, b):
    if b == 0:
        raise ValueError("division by zero")
    return a / b
```

The file MUST be exactly this content. Do not add imports, comments, tests, or a __main__ block.'

# Build a bash stub planner that echoes our canned JSON plan.
cat > "$STUB_DIR/claude" <<STUB
#!/usr/bin/env bash
cat <<'PLAN'
{"subtasks":[
  {"title":"Create calc.py","files":["calc.py"],"prompt":$(python3 -c "import json,sys;print(json.dumps(sys.argv[1]))" "$CALC_PROMPT")}
]}
PLAN
STUB
chmod +x "$STUB_DIR/claude"

# ── Scaffold an empty project and invoke the hybrid run ─────────────
cd "$WORK_DIR"
git init -q .
git config user.email "ci@yaver.test"
git config user.name "yaver-ci"
git commit --allow-empty -qm "init"

log "Running hybrid on workdir: $WORK_DIR"
log "  planner: canned stub (at $STUB_DIR/claude)"
log "  implementer: aider + $LITELLM_MODEL"

PATH="$STUB_DIR:$PATH" \
OLLAMA_API_BASE="$OLLAMA_URL" \
"$YAVER_BIN" hybrid \
  --planner claude \
  --implementer aider-ollama \
  --model "$LITELLM_MODEL" \
  --base-url "$OLLAMA_URL" \
  --workdir "$WORK_DIR" \
  --timeout 20m \
  "Write a calculator module" 2>&1 | tee "$STUB_DIR/run.log" || true

# ── Assertions — did the implementer produce something that works? ─
[[ -f "$WORK_DIR/calc.py" ]] || fail "calc.py was never created. Run log: $STUB_DIR/run.log"

log "Verifying calc.py..."
python3 - <<'PY' || fail "calc.py imports but functions do not behave correctly"
import sys, importlib.util, traceback
spec = importlib.util.spec_from_file_location("calc", "calc.py")
m = importlib.util.module_from_spec(spec)
try:
    spec.loader.exec_module(m)
except Exception:
    traceback.print_exc(); sys.exit(2)
checks = [
    ("add(2,3) == 5",    lambda: m.add(2, 3) == 5),
    ("sub(10,4) == 6",   lambda: m.sub(10, 4) == 6),
    ("mul(3,4) == 12",   lambda: m.mul(3, 4) == 12),
    ("div(10,2) == 5.0", lambda: m.div(10, 2) == 5),
]
for name, fn in checks:
    try:
        if not fn():
            print(f"FAIL: {name}"); sys.exit(1)
    except Exception as e:
        print(f"FAIL: {name} raised {e!r}"); sys.exit(1)
try:
    m.div(1, 0)
    print("FAIL: div(1,0) did not raise"); sys.exit(1)
except ValueError:
    pass
except Exception as e:
    print(f"FAIL: div(1,0) raised {type(e).__name__}, want ValueError"); sys.exit(1)
print("calc.py passes all behavioural checks.")
PY

echo -e "\033[32m[hybrid-local PASS]\033[0m End-to-end hybrid run produced a working calculator."
