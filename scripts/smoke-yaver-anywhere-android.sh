#!/usr/bin/env bash
# Smoke test for the Yaver Anywhere Android phone-node path.
#
# This is intentionally evidence-oriented: it does not factory reset, does not
# uninstall apps, and does not require secrets. It checks that a physical Android
# device is attached, the Yaver app is installed, the sandbox service can be
# inspected, and any agent listener is relay-only/loopback rather than LAN-wide.
#
# Optional:
#   SERIAL=<adb-serial> ./scripts/smoke-yaver-anywhere-android.sh
#   START_HOME_HOST=1 ./scripts/smoke-yaver-anywhere-android.sh

set -uo pipefail

PKG="${PKG:-io.yaver.mobile}"
SERVICE="${SERVICE:-io.yaver.mobile/.sandbox.SandboxService}"
ACTION_HOME_HOST="${ACTION_HOME_HOST:-io.yaver.mobile.sandbox.START_HOME_HOST}"
PORT="${PORT:-18080}"
LOG_LINES="${LOG_LINES:-120}"

PASSES=0
FAILS=0
WARNINGS=0

note() { printf "%s %s\n" "$(date +%H:%M:%S)" "$*" >&2; }
ok() { PASSES=$((PASSES + 1)); note "[PASS] $*"; }
bad() { FAILS=$((FAILS + 1)); note "[FAIL] $*"; }
warn() { WARNINGS=$((WARNINGS + 1)); note "[WARN] $*"; }

adb_cmd() {
  if [ -n "${SERIAL:-}" ]; then
    adb -s "$SERIAL" "$@"
  else
    adb "$@"
  fi
}

pick_device() {
  if [ -n "${SERIAL:-}" ]; then
    return 0
  fi
  local devices
  devices="$(adb devices | awk 'NR > 1 && $2 == "device" {print $1}')"
  local count
  count="$(printf "%s\n" "$devices" | sed '/^$/d' | wc -l | tr -d ' ')"
  if [ "$count" = "1" ]; then
    SERIAL="$(printf "%s\n" "$devices" | sed '/^$/d' | head -1)"
    return 0
  fi
  if [ "$count" = "0" ]; then
    bad "no adb device attached"
  else
    bad "multiple adb devices attached; set SERIAL=<serial>"
    printf "%s\n" "$devices" >&2
  fi
  return 1
}

note "step 1 - adb availability"
if command -v adb >/dev/null 2>&1; then
  ok "adb at $(command -v adb)"
else
  bad "adb missing from PATH"
  exit 1
fi

note "step 2 - pick physical device"
if pick_device; then
  ok "using $SERIAL"
else
  note "summary: PASSES=$PASSES WARNINGS=$WARNINGS FAILS=$FAILS"
  exit 1
fi

note "step 3 - device state"
state="$(adb_cmd get-state 2>/dev/null | tr -d '\r\n ')"
if [ "$state" = "device" ]; then
  ok "adb state=device"
else
  bad "adb state=$state"
fi

model="$(adb_cmd shell getprop ro.product.model 2>/dev/null | tr -d '\r')"
release="$(adb_cmd shell getprop ro.build.version.release 2>/dev/null | tr -d '\r')"
sdk="$(adb_cmd shell getprop ro.build.version.sdk 2>/dev/null | tr -d '\r')"
note "device: ${model:-unknown}, Android ${release:-?} / SDK ${sdk:-?}"

note "step 4 - package installed"
if adb_cmd shell pm path "$PKG" 2>/dev/null | grep -q "^package:"; then
  ok "$PKG installed"
else
  bad "$PKG not installed; install the current Yaver Android build first"
fi

note "step 5 - optional home-host start"
if [ "${START_HOME_HOST:-0}" = "1" ]; then
  if adb_cmd shell am start-foreground-service -n "$SERVICE" -a "$ACTION_HOME_HOST" >/tmp/yaver-anywhere-am.out 2>&1; then
    ok "requested home-host foreground service"
    sleep 3
  else
    warn "could not start service via adb; open the app and use the home-host toggle"
    sed -n '1,8p' /tmp/yaver-anywhere-am.out >&2
  fi
else
  warn "START_HOME_HOST not set; inspect-only run"
fi

note "step 6 - service visibility"
svc="$(adb_cmd shell dumpsys activity services "$PKG" 2>/dev/null | tr -d '\r')"
if printf "%s\n" "$svc" | grep -q "SandboxService"; then
  ok "SandboxService visible in dumpsys"
else
  warn "SandboxService not visible; start home-host in the app and re-run"
fi

note "step 7 - listener binding"
listeners="$(adb_cmd shell "ss -ltnp 2>/dev/null | grep ':$PORT ' || netstat -ltn 2>/dev/null | grep ':$PORT ' || true" | tr -d '\r')"
if [ -z "$listeners" ]; then
  warn "no listener on port $PORT observed"
else
  printf "%s\n" "$listeners" >&2
  if printf "%s\n" "$listeners" | grep -Eq "(^|[[:space:]])(0\.0\.0\.0|:::|\[::\]):$PORT"; then
    bad "port $PORT is bound on all interfaces; home-host must be relay-only/loopback"
  else
    ok "port $PORT is not bound on all interfaces"
  fi
fi

note "step 8 - recent sandbox logs"
logs="$(adb_cmd logcat -d -t "$LOG_LINES" -s YaverSandbox YaverRootfs 2>/dev/null | tr -d '\r')"
if [ -n "$logs" ]; then
  printf "%s\n" "$logs" | tail -40 >&2
  ok "captured recent Yaver sandbox logs"
else
  warn "no Yaver sandbox logs captured"
fi

note "summary: PASSES=$PASSES WARNINGS=$WARNINGS FAILS=$FAILS"
if [ "$FAILS" -gt 0 ]; then
  exit 1
fi
