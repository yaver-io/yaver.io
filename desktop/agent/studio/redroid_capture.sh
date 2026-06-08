#!/usr/bin/env bash
# redroid_capture.sh — Studio Android capture-surface bootstrap.
#
# Stands up a real Android (AOSP) instance via redroid (Android-in-Docker) on a
# Linux host — NO emulator, NO KVM — installs an APK, drives a flow, and records
# an H.264 MP4. This is the "redroid" CaptureSurface from
# docs/yaver-store-asset-studio.md §6, runnable standalone or invoked by the
# agent on a runner.
#
# Architecture: docker pulls the redroid image matching the host arch
# automatically (multi-arch manifest). On arm64 hosts, arm64 APKs + native libs
# run NATIVELY (required for Yaver's own arm64-only app). On x86_64 hosts, only
# apps whose splits include x86_64 libs will fully run.
#
# Usage:
#   APK=app.apk PKG=io.yaver.mobile OUT=demo.mp4 [FLOW=flow.yaml] [DURATION=40] \
#     bash redroid_capture.sh
#
# Requires: docker, and adb (auto-installed via apt if absent). Run as a user
# with docker + sudo (for the binder kernel module). Records up to ~3 min
# (adb screenrecord limit); pass FLOW=<maestro.yaml> for a scripted walkthrough,
# otherwise it just launches the app so you can confirm the surface works.
set -euo pipefail

APK="${APK:?set APK=/path/to/app.apk}"
PKG="${PKG:?set PKG=app.package.name}"
OUT="${OUT:-demo.mp4}"
FLOW="${FLOW:-}"
DURATION="${DURATION:-40}"
IMAGE="${IMAGE:-redroid/redroid:13.0.0-latest}"
NAME="${NAME:-yaver-studio-redroid}"
ADB_ADDR="127.0.0.1:5555"

log() { printf '\n[studio] %s\n' "$*"; }

arch="$(uname -m)"
log "host arch: $arch  (image=$IMAGE — docker selects the matching arch)"
[ -f "$APK" ] || { echo "APK not found: $APK" >&2; exit 1; }

# 1. binder kernel support — redroid needs the binder_linux module loaded in the
#    HOST kernel (it then mounts binderfs itself inside the privileged container).
#    PROVEN on magara (Ubuntu 20.04, kernel 5.4, x86_64, 2026-06-08): load it via
#    a privileged helper container so NO host sudo is required — the Docker daemon
#    runs as root, so a --privileged container with /lib/modules mounted can
#    insmod into the running host kernel. This is what makes the on-prem path work
#    on a box where the user has docker-group access but not passwordless sudo.
log "ensuring binder_linux is loaded (via privileged helper — no host sudo)"
if ! lsmod 2>/dev/null | grep -q '^binder_linux'; then
  if command -v sudo >/dev/null 2>&1 && sudo -n true 2>/dev/null; then
    sudo modprobe binder_linux devices="binder,hwbinder,vndbinder" 2>/dev/null || sudo modprobe binder_linux 2>/dev/null || true
  fi
  if ! lsmod 2>/dev/null | grep -q '^binder_linux'; then
    docker run --rm --privileged -v /lib/modules:/lib/modules debian:bullseye-slim bash -c \
      'apt-get update -qq >/dev/null 2>&1; apt-get install -y -qq kmod >/dev/null 2>&1; modprobe binder_linux devices=binder,hwbinder,vndbinder || modprobe binder_linux' \
      || echo "WARN: could not load binder_linux — host kernel may lack the module; install linux-modules-extra-\$(uname -r)"
  fi
fi
lsmod 2>/dev/null | grep -q '^binder_linux' && log "binder_linux loaded" || log "WARN: binder_linux NOT loaded — redroid may not boot"

# 2. adb
if ! command -v adb >/dev/null 2>&1; then
  log "installing adb"
  sudo apt-get update -qq && sudo apt-get install -y -qq android-tools-adb >/dev/null
fi

# 3. (re)launch redroid
log "starting redroid container"
docker rm -f "$NAME" >/dev/null 2>&1 || true
docker run -itd --rm --privileged \
  --name "$NAME" \
  -p 5555:5555 \
  "$IMAGE" \
  androidboot.redroid_width=1080 androidboot.redroid_height=2340 androidboot.redroid_dpi=440 \
  >/dev/null

# 4. wait for boot
log "waiting for Android to boot (up to 120s)"
adb disconnect "$ADB_ADDR" >/dev/null 2>&1 || true
adb connect "$ADB_ADDR" >/dev/null
booted=""
for i in $(seq 1 60); do
  if [ "$(adb -s "$ADB_ADDR" shell getprop sys.boot_completed 2>/dev/null | tr -d '\r')" = "1" ]; then
    booted=1; break
  fi
  sleep 2
  adb connect "$ADB_ADDR" >/dev/null 2>&1 || true
done
[ -n "$booted" ] || { echo "redroid did not boot — check 'docker logs $NAME' (likely binder/kernel)"; docker logs --tail 30 "$NAME" 2>&1 || true; exit 2; }
log "Android booted"

# 5. install APK
log "installing $APK"
adb -s "$ADB_ADDR" install -r -g "$APK" || adb -s "$ADB_ADDR" install -r "$APK"

# 6. record + drive
log "recording to $OUT (max ${DURATION}s)"
adb -s "$ADB_ADDR" shell screenrecord --time-limit "$DURATION" /sdcard/studio.mp4 &
REC_PID=$!
sleep 2

if [ -n "$FLOW" ] && command -v maestro >/dev/null 2>&1; then
  log "running Maestro flow $FLOW"
  maestro --device "$ADB_ADDR" test "$FLOW" || echo "WARN: flow failed; keeping partial recording"
else
  log "launching $PKG (no Maestro flow — surface check only)"
  adb -s "$ADB_ADDR" shell monkey -p "$PKG" -c android.intent.category.LAUNCHER 1 >/dev/null 2>&1 || true
  # Let the launch settle / leave time for a manual walkthrough if watching live.
  sleep "$((DURATION - 4))"
fi

wait "$REC_PID" 2>/dev/null || true
adb -s "$ADB_ADDR" pull /sdcard/studio.mp4 "$OUT"
log "pulled recording → $OUT"

# 7. teardown (the capture surface must not linger / bill)
log "tearing down redroid"
docker rm -f "$NAME" >/dev/null 2>&1 || true
log "done. $OUT"
