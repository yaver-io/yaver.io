#!/usr/bin/env bash
set -euo pipefail

IFS= read -r GLM_API_KEY
export GLM_API_KEY

CONFIG=""
for c in /home/yaver/.yaver/config.json /root/.yaver/config.json; do
  [ -f "$c" ] && CONFIG="$c" && break
done
[ -n "$CONFIG" ] || { echo "no agent config" >&2; exit 2; }
TOKEN=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['auth_token'])" "$CONFIG")
[ -n "$TOKEN" ] || { echo "agent auth_token empty" >&2; exit 2; }

AGENT="http://127.0.0.1:18080"
chmod +x \
  /opt/yaver/scripts/remote-runtime-smoke.sh \
  /opt/yaver/scripts/remote-browser-color-vibe-smoke.sh \
  /opt/yaver/scripts/remote-opencode-hermes-smoke.sh

echo "== yaver version =="
yaver --version

echo "== browser color background vibing =="
/opt/yaver/scripts/remote-browser-color-vibe-smoke.sh \
  --agent "$AGENT" \
  --token "$TOKEN"

echo "== opencode glm + hermes reload =="
/opt/yaver/scripts/remote-opencode-hermes-smoke.sh \
  --agent "$AGENT" \
  --token "$TOKEN" \
  --workdir /tmp/yaver-ci-hermes-fixture

echo "== preparing managed remote-runtime tooling =="
if id yaver >/dev/null 2>&1; then
  runuser -u yaver -- /usr/bin/env PATH="/usr/local/bin:/usr/bin:/bin" yaver install remote-runtime
else
  yaver install remote-runtime
fi

echo "== swift / linux-host verdict =="
/opt/yaver/scripts/remote-runtime-smoke.sh \
  --agent "$AGENT" \
  --token "$TOKEN" \
  --workdir /opt/yaver/tests/fixtures/native-ios-swift \
  --framework swift \
  --target ios-simulator \
  --expect disabled

echo "== kotlin / android-emulator frame + control =="
if id yaver >/dev/null 2>&1; then
  su - yaver -c "/opt/yaver/scripts/remote-runtime-smoke.sh \
    --agent '$AGENT' \
    --token '$TOKEN' \
    --workdir /opt/yaver/tests/fixtures/native-android-kotlin \
    --framework kotlin \
    --target android-emulator \
    --expect enabled \
    --transport relay-jpeg-poll \
    --check-frame \
    --check-control"
else
  /opt/yaver/scripts/remote-runtime-smoke.sh \
    --agent "$AGENT" \
    --token "$TOKEN" \
    --workdir /opt/yaver/tests/fixtures/native-android-kotlin \
    --framework kotlin \
    --target android-emulator \
    --expect enabled \
    --transport relay-jpeg-poll \
    --check-frame \
    --check-control
fi

echo "remote vibing smoke complete"
