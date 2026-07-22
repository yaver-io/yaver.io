#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  remote-opencode-hermes-smoke.sh \
    --agent http://127.0.0.1:18080 \
    --token <bearer> \
    --workdir /tmp/yaver-hermes-fixture

Environment:
  GLM_API_KEY                 Required for --check-opencode (default on).
  YAVER_OPENCODE_MODEL        Optional, defaults to glm/glm-4.5-air.
  YAVER_SKIP_OPENCODE_SMOKE   Set 1 to skip the Opencode GLM write test.
  YAVER_SKIP_HERMES_SMOKE     Set 1 to skip /dev/build-native + reload checks.

This script is for protected/manual CI. It never writes the API key to disk and
prints only structural results from the agent.
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
WORKDIR=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --agent) AGENT="${2:-}"; shift 2 ;;
    --token) TOKEN="${2:-}"; shift 2 ;;
    --workdir) WORKDIR="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown arg: $1" >&2; usage; exit 2 ;;
  esac
done

[[ -n "$AGENT" && -n "$TOKEN" && -n "$WORKDIR" ]] || {
  usage
  exit 2
}

need curl
need jq
need node
need npm

auth=(-H "Authorization: Bearer $TOKEN")

write_hermes_fixture() {
  local dir="$1"
  mkdir -p "$dir"
  cat > "$dir/package.json" <<'JSON'
{
  "name": "yaver-ci-hermes-fixture",
  "version": "0.0.0",
  "private": true,
  "main": "expo/AppEntry.js",
  "scripts": {
    "start": "expo start"
  },
  "dependencies": {
    "expo": "~52.0.0",
    "react": "18.3.1",
    "react-native": "0.76.9"
  },
  "devDependencies": {
    "@babel/core": "^7.25.2"
  }
}
JSON
  cat > "$dir/app.json" <<'JSON'
{
  "expo": {
    "name": "Yaver CI Hermes Fixture",
    "slug": "yaver-ci-hermes-fixture",
    "ios": { "bundleIdentifier": "io.yaver.ci.hermes" },
    "android": { "package": "io.yaver.ci.hermes" }
  }
}
JSON
  cat > "$dir/App.js" <<'JS'
import { Text, View } from "react-native";

export default function App() {
  return (
    <View style={{ flex: 1, alignItems: "center", justifyContent: "center" }}>
      <Text>Yaver CI Hermes Fixture</Text>
    </View>
  );
}
JS
}

run_opencode_glm_smoke() {
  if [[ "${YAVER_SKIP_OPENCODE_SMOKE:-}" == "1" ]]; then
    echo "== opencode glm smoke skipped =="
    return
  fi
  if [[ -z "${GLM_API_KEY:-}" ]]; then
    echo "GLM_API_KEY is required for opencode smoke" >&2
    exit 2
  fi
  if ! command -v opencode >/dev/null 2>&1; then
    echo "== installing opencode-ai =="
    npm install -g opencode-ai >/tmp/yaver-opencode-install.log 2>&1 || {
      echo "opencode install failed; log retained on remote at /tmp/yaver-opencode-install.log" >&2
      exit 1
    }
  fi

  local tmp
  tmp="$(mktemp -d /tmp/yaver-opencode-glm.XXXXXX)"
  trap 'rm -rf "$tmp"' RETURN
  printf '{"name":"yaver-opencode-glm-fixture","version":"0.0.0"}\n' > "$tmp/package.json"

  local config
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
      "Create a file named remote_glm_result.txt containing exactly: hello from remote glm ci" \
      </dev/null >/tmp/yaver-opencode-glm.log 2>&1 || {
        echo "opencode glm run failed; log retained on remote at /tmp/yaver-opencode-glm.log" >&2
        exit 1
      }
    grep -qx "hello from remote glm ci" remote_glm_result.txt
  )
  echo "opencode glm smoke ok"
}

run_hermes_reload_smoke() {
  if [[ "${YAVER_SKIP_HERMES_SMOKE:-}" == "1" ]]; then
    echo "== hermes reload smoke skipped =="
    return
  fi
  write_hermes_fixture "$WORKDIR"

  echo "== rescan mobile projects =="
  curl -fsS "${auth[@]}" -X POST "$AGENT/projects/mobile" | jq '{ok, count: (.projects | length? // 0)}'

  echo "== build native hermes contract =="
  build_json="$(curl -fsS "${auth[@]}" \
    --max-time 900 \
    -H 'Content-Type: application/json' \
    -d "$(jq -cn --arg projectPath "$WORKDIR" '{platform:"android", projectPath:$projectPath, consumerVersion:"remote-ci", consumerBuild:"remote-ci", consumerSdkVersion:"remote-ci", consumerHermesBCVersion:96}')" \
    "$AGENT/dev/build-native")"
  echo "$build_json" | jq '{status, code, platform, bcVersion, bundleUrl, error, helpHint}'
  if [[ "$(echo "$build_json" | jq -r '.code // ""')" == "PROJECT_REQUIRED" ]]; then
    echo "build-native regressed to active-dev-server inference" >&2
    exit 1
  fi
  if [[ "$(echo "$build_json" | jq -r '.code // ""')" == "FRAMEWORK_NOT_SUPPORTED" ]]; then
    echo "Hermes fixture was not recognized as Expo/RN" >&2
    exit 1
  fi

  echo "== reload app bundle path =="
  reload_json="$(curl -fsS "${auth[@]}" \
    --max-time 900 \
    -H 'Content-Type: application/json' \
    -d "$(jq -cn --arg projectPath "$WORKDIR" '{mode:"bundle", projectPath:$projectPath, platform:"android"}')" \
    "$AGENT/dev/reload-app")"
  echo "$reload_json" | jq '{ok, mode, deliveredTo, buildStatus: .build.status, error, code}'
  if [[ "$(echo "$reload_json" | jq -r '.code // ""')" == "PROJECT_REQUIRED" ]]; then
    echo "reload-app did not pass the project path into build-native" >&2
    exit 1
  fi

  echo "hermes reload smoke ok"
}

echo "== agent info =="
curl -fsS "${auth[@]}" "$AGENT/info" | jq '{hostname, version, workDir}'

run_opencode_glm_smoke
run_hermes_reload_smoke

echo "remote opencode/hermes smoke ok"
