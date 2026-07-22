#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${GLM_API_KEY:-}" ]]; then
  echo "GLM_API_KEY is required" >&2
  exit 2
fi

if ! command -v opencode >/dev/null 2>&1; then
  echo "== installing opencode-ai =="
  npm install -g opencode-ai >/tmp/yaver-opencode-install.log 2>&1 || {
    echo "opencode install failed; log retained at /tmp/yaver-opencode-install.log" >&2
    exit 1
  }
fi

tmp="$(mktemp -d /tmp/yaver-opencode-glm.XXXXXX)"
cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT

printf '{"name":"yaver-opencode-glm-fixture","version":"0.0.0"}\n' > "$tmp/package.json"

config="$(cat <<'JSON'
{
  "$schema": "https://opencode.ai/config.json",
  "model": "{env:YAVER_OPENCODE_MODEL}",
  "provider": {
    "glm": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "GLM Coding Plan",
      "options": {
        "baseURL": "https://api.z.ai/api/coding/paas/v4",
        "apiKey": "{env:GLM_API_KEY}"
      },
      "models": {
        "glm-4.5-air": {
          "name": "GLM-4.5-Air",
          "limit": { "context": 131072, "output": 98304 }
        }
      }
    }
  }
}
JSON
)"

echo "== opencode glm writes fixture =="
(
  cd "$tmp"
  export OPENCODE_CONFIG_CONTENT="$config"
  export OPENCODE_DATA_DIR="$tmp/.opencode-data"
  export YAVER_OPENCODE_MODEL="${YAVER_OPENCODE_MODEL:-glm/glm-4.5-air}"
  mkdir -p "$OPENCODE_DATA_DIR"
  opencode run --pure --dangerously-skip-permissions --model "$YAVER_OPENCODE_MODEL" \
    "Create a file named remote_glm_result.txt containing exactly: hello from opencode glm ci" \
    </dev/null >/tmp/yaver-opencode-glm.log 2>&1 || {
      echo "opencode glm run failed; log retained at /tmp/yaver-opencode-glm.log" >&2
      exit 1
    }
  grep -qx "hello from opencode glm ci" remote_glm_result.txt
)

echo "opencode glm smoke ok"
