#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  remote-runtime-smoke.sh \
    --agent http://127.0.0.1:18080 \
    --token <bearer> \
    --workdir /opt/yaver/tests/fixtures/native-android-kotlin \
    --framework kotlin \
    --target android-emulator \
    --expect enabled

  remote-runtime-smoke.sh \
    --agent http://127.0.0.1:18080 \
    --token <bearer> \
    --workdir /opt/yaver/tests/fixtures/native-ios-swift \
    --framework swift \
    --target ios-simulator \
    --expect disabled

Flags:
  --agent <url>           Agent base URL.
  --token <bearer>        Agent bearer token.
  --workdir <path>        Fixture/project path on the remote host.
  --framework <name>      Project framework: swift, kotlin, or flutter.
  --target <id>           Remote runtime target id.
  --expect <state>        enabled | disabled
  --transport <mode>      direct-webrtc | relay-jpeg-poll (default: relay-jpeg-poll)
  --check-frame           Create a session and fetch one JPEG frame.
  --prepare-android       Run `yaver install remote-runtime` before checks.
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
FRAMEWORK=""
TARGET=""
EXPECT_STATE=""
TRANSPORT="relay-jpeg-poll"
CHECK_FRAME=0
PREPARE_ANDROID=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --agent) AGENT="${2:-}"; shift 2 ;;
    --token) TOKEN="${2:-}"; shift 2 ;;
    --workdir) WORKDIR="${2:-}"; shift 2 ;;
    --framework) FRAMEWORK="${2:-}"; shift 2 ;;
    --target) TARGET="${2:-}"; shift 2 ;;
    --expect) EXPECT_STATE="${2:-}"; shift 2 ;;
    --transport) TRANSPORT="${2:-}"; shift 2 ;;
    --check-frame) CHECK_FRAME=1; shift ;;
    --prepare-android) PREPARE_ANDROID=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown arg: $1" >&2; usage; exit 2 ;;
  esac
done

[[ -n "$AGENT" && -n "$TOKEN" && -n "$WORKDIR" && -n "$FRAMEWORK" && -n "$TARGET" && -n "$EXPECT_STATE" ]] || {
  usage
  exit 2
}

need curl
need jq

auth=(-H "Authorization: Bearer $TOKEN")

echo "== agent info =="
curl -fsS "${auth[@]}" "$AGENT/info" | jq '{ok, hostname, version, workDir}'

if [[ "$PREPARE_ANDROID" == "1" ]]; then
  need yaver
  echo "== preparing remote-runtime host tools =="
  yaver install remote-runtime
  echo "== android toolchain snapshot =="
  command -v adb >/dev/null 2>&1 && adb version | head -2 || true
  command -v emulator >/dev/null 2>&1 && emulator -list-avds || true
  if [[ -x "$HOME/.yaver/runtimes/android-sdk/platform-tools/adb" ]]; then
    "$HOME/.yaver/runtimes/android-sdk/platform-tools/adb" version | head -2 || true
  fi
  if [[ -x "$HOME/.yaver/runtimes/android-sdk/emulator/emulator" ]]; then
    "$HOME/.yaver/runtimes/android-sdk/emulator/emulator" -list-avds || true
  fi
fi

echo "== remote runtime capabilities =="
encoded_workdir="$(python3 -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1]))' "$WORKDIR")"
encoded_framework="$(python3 -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1]))' "$FRAMEWORK")"
caps_json="$(curl -fsS "${auth[@]}" \
  "$AGENT/remote-runtime/capabilities?workDir=$encoded_workdir&framework=$encoded_framework")"
echo "$caps_json" | jq .

target_json="$(echo "$caps_json" | jq -c --arg target "$TARGET" '.targets[] | select(.id == $target)')"
[[ -n "$target_json" ]] || {
  echo "target $TARGET not found in capabilities" >&2
  exit 1
}

enabled="$(echo "$target_json" | jq -r '.enabled')"
runtime_host_class="$(echo "$target_json" | jq -r '.runtimeHostClass // ""')"
reason="$(echo "$target_json" | jq -r '.reason // ""')"

echo "== target verdict =="
echo "$target_json" | jq '{id, label, enabled, runtimeHostClass, reason, requiredCli}'

case "$EXPECT_STATE" in
  enabled)
    [[ "$enabled" == "true" ]] || {
      echo "expected target $TARGET to be enabled, got disabled: $reason" >&2
      exit 1
    }
    ;;
  disabled)
    [[ "$enabled" == "false" ]] || {
      echo "expected target $TARGET to be disabled" >&2
      exit 1
    }
    ;;
  *)
    echo "unsupported --expect value: $EXPECT_STATE" >&2
    exit 2
    ;;
esac

if [[ "$FRAMEWORK" == "swift" ]]; then
  [[ "$runtime_host_class" == "macos-ios" ]] || {
    echo "swift runtimeHostClass should be macos-ios, got: $runtime_host_class" >&2
    exit 1
  }
fi

if [[ "$FRAMEWORK" == "kotlin" ]]; then
  [[ "$runtime_host_class" == *"android" ]] || {
    echo "kotlin runtimeHostClass should be an android host class, got: $runtime_host_class" >&2
    exit 1
  }
fi

if [[ "$CHECK_FRAME" == "1" ]]; then
  [[ "$EXPECT_STATE" == "enabled" ]] || {
    echo "--check-frame only makes sense for enabled targets" >&2
    exit 2
  }
  echo "== create remote-runtime session =="
  session_json="$(curl -fsS "${auth[@]}" \
    -H 'Content-Type: application/json' \
    -d "$(jq -cn --arg workDir "$WORKDIR" --arg framework "$FRAMEWORK" --arg targetId "$TARGET" --arg transportMode "$TRANSPORT" '{workDir:$workDir, framework:$framework, targetId:$targetId, transportMode:$transportMode}')" \
    "$AGENT/remote-runtime/sessions")"
  echo "$session_json" | jq '{id, targetId, deviceId, transportMode, frameTransport, status, note}'
  session_id="$(echo "$session_json" | jq -r '.id')"
  [[ -n "$session_id" && "$session_id" != "null" ]] || {
    echo "session creation failed" >&2
    exit 1
  }
  cleanup() {
    curl -fsS "${auth[@]}" -X DELETE "$AGENT/remote-runtime/sessions/$session_id" >/dev/null || true
  }
  trap cleanup EXIT

  echo "== fetch relay frame =="
  tmp_jpg="$(mktemp /tmp/yaver-remote-runtime-frame.XXXXXX.jpg)"
  curl -fsS "${auth[@]}" "$AGENT/remote-runtime/sessions/$session_id/frame" -o "$tmp_jpg"
  bytes="$(wc -c < "$tmp_jpg" | tr -d ' ')"
  echo "frame_bytes=$bytes"
  [[ "$bytes" -gt 0 ]] || {
    echo "captured frame is empty" >&2
    exit 1
  }
  rm -f "$tmp_jpg"
fi

echo "remote-runtime smoke ok"
