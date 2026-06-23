#!/usr/bin/env bash
# shoot-fgs-usecase-video.sh — produce the Google Play FOREGROUND_SERVICE_SPECIAL_USE
# *use-case narrative* justification video end to end, on a disposable x86 Hetzner
# box, then DELETE the box. This is the unattended driver behind the
# shoot-fgs-video.yml workflow (where GLM_API_KEY + HCLOUD_TOKEN come from repo
# secrets), but it also runs locally if those are exported.
#
# The video story (real, not faked): start the on-device sandbox (FGS) → give the
# on-device agent a real coding task (GLM when a key is present) → show it working
# → background the app (captioned: Android would kill it mid-task without the FGS)
# → the task finishes in the background and posts a "task finished" notification →
# stop. The studio capture layer (desktop/agent/studio + UseCaseProofSteps) drives
# and records it; we only orchestrate the box + build here.
#
# COST SAFETY (CLAUDE.md hard rule): the box is metered and is ALWAYS deleted on
# exit (trap) and by a hard watchdog — a hang can never bill indefinitely.
#
# Required env:
#   HCLOUD_TOKEN                 Hetzner API token (the box).
#   HCLOUD_SSH_PRIVATE_KEY_PATH  private key whose public half is a named hcloud key.
#   HCLOUD_SSH_KEY_NAME          that named key in hcloud (default: yaver-ci).
# Optional env:
#   GLM_API_KEY / ZAI_API_KEY    z.ai key → the recorded task uses the `glm` runner.
#                                Absent → a real key-free shell/build task is used.
#   YAVER_SESSION_TOKEN          a mobile session token to inject (skips UI sign-in).
#   OUT_DIR                      where to copy artifacts (default: ./fgs-shoot-out).
#   CI_SERVER_TYPE/LOCATION      box shape (default cx33 / hel1 — x86 for redroid).
#   MAX_RUNTIME_SEC              watchdog kill (default 5400 = 90 min).
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HCLOUD_DIR="$REPO_ROOT/ci/hcloud"
OUT_DIR="${OUT_DIR:-$REPO_ROOT/fgs-shoot-out}"
LOG="${LOG:-$OUT_DIR/shoot.log}"
mkdir -p "$OUT_DIR"

export CI_SERVER_TYPE="${CI_SERVER_TYPE:-cpx42}"    # x86 (8 vCPU/16GB), always available — the app runs fine once Kotlin sources are restored
export CI_SERVER_LOCATION="${CI_SERVER_LOCATION:-hel1}"
export CI_SERVER_IMAGE="${CI_SERVER_IMAGE:-ubuntu-24.04}"
export CI_SERVER_NAME="${CI_SERVER_NAME:-yaver-fgs-shoot-$(date +%s 2>/dev/null || echo run)}"
export HCLOUD_SSH_KEY_NAME="${HCLOUD_SSH_KEY_NAME:-yaver-ci}"
export REPO_ROOT
MAX_RUNTIME_SEC="${MAX_RUNTIME_SEC:-5400}"
# BETA_BOX: reuse an existing PERSISTENT box (the warm beta box) instead of a
# throwaway. In this mode PHASE 1 powers it ON (no create) and cleanup powers it
# OFF (NEVER delete) — caches persist for fast iteration. Empty = ephemeral box
# (provision + delete).
BETA_BOX="${BETA_BOX:-}"

say() { printf '\n[shoot %s] %s\n' "$(date +%H:%M:%S 2>/dev/null || echo --)" "$*" | tee -a "$LOG"; }
die() { say "FATAL: $*"; exit 1; }

: "${HCLOUD_SSH_PRIVATE_KEY_PATH:?HCLOUD_SSH_PRIVATE_KEY_PATH required}"
# BETA mode uses the local `hcloud` CLI config (active context), so no token env
# is needed. Ephemeral mode (create/delete via common.sh) requires HCLOUD_TOKEN.
if [ -z "$BETA_BOX" ]; then
  : "${HCLOUD_TOKEN:?HCLOUD_TOKEN required (or set BETA_BOX to use the hcloud CLI config)}"
fi
[ -n "${HCLOUD_TOKEN:-}" ] && export HCLOUD_TOKEN
export HCLOUD_SSH_PRIVATE_KEY_PATH

# --- bulletproof teardown: delete the box on ANY exit -----------------------
cleanup() {
  local code=$?
  if [ -n "$BETA_BOX" ]; then
    # Persistent beta box: POWER OFF, never delete (box+data persist).
    say "cleanup (exit=$code) — powering OFF $BETA_BOX (NOT deleting)"
    # retry: the box can be transiently "locked" right after a power action.
    for i in 1 2 3 4 5; do
      if hcloud server poweroff "$BETA_BOX" >>"$LOG" 2>&1; then break; fi
      say "poweroff attempt $i locked/failed — retrying in 10s"
      sleep 10
    done
    # verify it ends up off (never leave the beta box running)
    st="$(hcloud server list -o noheader -o columns=name,status 2>/dev/null | awk -v n="$BETA_BOX" '$1==n{print $2}')"
    say "cleanup done — $BETA_BOX status=${st:-unknown}"
    return
  fi
  say "cleanup (exit=$code) — deleting box $CI_SERVER_NAME"
  bash "$HCLOUD_DIR/delete-server.sh" >>"$LOG" 2>&1 || say "delete-server returned nonzero (may be gone)"
  # belt-and-suspenders: delete by name directly too
  local id
  id="$(hcloud server list -o noheader -o columns=id,name 2>/dev/null | awk -v n="$CI_SERVER_NAME" '$2==n{print $1}')"
  [ -n "$id" ] && hcloud server delete "$id" >>"$LOG" 2>&1 || true
  say "cleanup done"
}
trap cleanup EXIT INT TERM

# --- hard watchdog: a hung run cannot bill forever --------------------------
( sleep "$MAX_RUNTIME_SEC"; say "WATCHDOG: ${MAX_RUNTIME_SEC}s elapsed — killing run"; kill -TERM $$ 2>/dev/null ) &
WATCHDOG_PID=$!
disown "$WATCHDOG_PID" 2>/dev/null || true

SSH_OPTS=(-i "$HCLOUD_SSH_PRIVATE_KEY_PATH" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null
  -o ConnectTimeout=15 -o ServerAliveInterval=15 -o ServerAliveCountMax=20 -o TCPKeepAlive=yes)
rsh() { ssh "${SSH_OPTS[@]}" "root@$IP" "$@"; }
# rsh_script runs a remote script FILE over ssh up to N times — transient idle
# drops ("Broken pipe" / "Operation timed out") on long quiet commands shouldn't
# abort the whole run. A file (not a heredoc) so stdin can be re-fed each retry.
# Phases are idempotent (toolchain checks command -v; gradle resumes; capture
# re-runs cleanly).
rsh_script() {
  local n="$1" f="$2"
  local i
  for i in $(seq 1 "$n"); do
    if rsh "bash -s" < "$f"; then return 0; fi
    say "ssh phase failed (attempt $i/$n) — retrying in 20s"
    sleep 20
  done
  return 1
}

# ---------------------------------------------------------------------------
if [ -n "$BETA_BOX" ]; then
  say "PHASE 1 — power ON existing warm box $BETA_BOX (no create; will power off, never delete)"
  hcloud server poweron "$BETA_BOX" >>"$LOG" 2>&1 || say "poweron returned nonzero (maybe already on)"
  for i in $(seq 1 30); do
    IP="$(hcloud server ip "$BETA_BOX" 2>/dev/null)"
    [ -n "$IP" ] && ssh "${SSH_OPTS[@]}" -o BatchMode=yes "root@$IP" 'echo READY' 2>/dev/null | grep -q READY && break
    sleep 4
  done
  [ -n "$IP" ] || die "could not resolve/reach $BETA_BOX"
  # sync-repo.sh + other helpers read the IP from this file — write the beta IP
  # so they don't use a stale ephemeral-box IP from a previous run.
  mkdir -p "$REPO_ROOT/ci/.artifacts"
  echo "$IP" > "$REPO_ROOT/ci/.artifacts/server-ip"
  say "beta box ip=$IP"
else
say "PHASE 1 — provision $CI_SERVER_NAME (x86; will fall back across types/regions)"
# x86 is reliably available; the earlier "x86 crash" was actually a missing
# Kotlin source (prebuild dropped SandboxService), now restored in PHASE 4.
provisioned=""
for combo in \
  "$CI_SERVER_TYPE:$CI_SERVER_LOCATION" \
  "cpx42:hel1" "cpx42:nbg1" "cpx42:fsn1" \
  "cx43:nbg1" "cx43:fsn1" "cx43:hel1" \
  "ccx23:hel1" "ccx23:nbg1"; do
  CI_SERVER_TYPE="${combo%%:*}"; CI_SERVER_LOCATION="${combo##*:}"
  export CI_SERVER_TYPE CI_SERVER_LOCATION
  say "trying $CI_SERVER_TYPE/$CI_SERVER_LOCATION"
  if bash "$HCLOUD_DIR/create-server.sh" >>"$LOG" 2>&1; then provisioned=1; break; fi
done
[ -n "$provisioned" ] || die "provision failed across all type/location fallbacks"
IP="$(cat "$REPO_ROOT/ci/.artifacts/server-ip")"
say "box ip=$IP"
bash "$HCLOUD_DIR/wait-for-ssh.sh" >>"$LOG" 2>&1 || die "ssh never ready"
fi

say "PHASE 2 — install x86 toolchain (docker, node, go1.26, jdk17, android sdk+ndk, ffmpeg)"
# NB: ci/remote/bootstrap.sh is hardcoded arm64 (the old cax box), so we install
# the x86 toolchain explicitly here. Each later ssh phase sources the env file
# this writes (non-login ssh shells don't read /etc/profile.d automatically).
cat > /tmp/yaver-shoot-phase2.sh <<'REMOTE'
  set -ex
  export DEBIAN_FRONTEND=noninteractive
  # wait out any boot-time apt lock (unattended-upgrades) before installing.
  for _ in $(seq 1 30); do fuser /var/lib/dpkg/lock-frontend >/dev/null 2>&1 || break; sleep 5; done
  # swap so the gradle/kotlin daemons aren't OOM-killed mid-build (run 4 died on
  # "Gradle daemon disappeared" = OOM). Cheap insurance even on a 16GB box.
  if ! swapon --show | grep -q .; then
    fallocate -l 8G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile || true
  fi
  apt-get update -qq
  # critical packages MUST succeed (set -e aborts → die); kernel-module pkg is
  # best-effort (often unavailable for the exact running kernel — must not take
  # unzip/ffmpeg down with it).
  apt-get install -y -qq ca-certificates curl gnupg unzip rsync git ffmpeg openjdk-17-jdk-headless
  apt-get install -y -qq "linux-modules-extra-$(uname -r)" || echo "WARN: linux-modules-extra unavailable (redroid binder load will use the privileged-helper path)"
  # docker
  command -v docker >/dev/null || curl -fsSL https://get.docker.com | sh
  systemctl enable --now docker || true
  # node 20
  command -v node >/dev/null || { curl -fsSL https://deb.nodesource.com/setup_20.x | bash - ; apt-get install -y -qq nodejs ; }
  # arch-aware: arm64 box → arm64 Go + arm64 APK (the app runs NATIVELY, no x86
  # crash, no NDK cross-compile needed). x86 box → amd64 + NDK (legacy path).
  ARCH="$(uname -m)"
  if [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then GOARCH=arm64; TARGET_ABI=arm64-v8a; else GOARCH=amd64; TARGET_ABI=x86_64; fi
  echo "box arch=$ARCH → GOARCH=$GOARCH TARGET_ABI=$TARGET_ABI"
  # go 1.26 (matching arch) — agent go.mod requires 1.26
  /usr/local/go/bin/go version 2>/dev/null | grep -q go1.26 || \
    curl -fsSL "https://go.dev/dl/go1.26.1.linux-${GOARCH}.tar.gz" | tar -C /usr/local -xz
  ln -sf /usr/local/go/bin/go /usr/local/bin/go
  # android sdk: cmdline-tools + platform + build-tools (+ ndk ONLY for x86 cross-compile)
  export ANDROID_HOME=/opt/android-sdk
  mkdir -p "$ANDROID_HOME/cmdline-tools"
  if [ ! -x "$ANDROID_HOME/cmdline-tools/latest/bin/sdkmanager" ]; then
    curl -fsSL https://dl.google.com/android/repository/commandlinetools-linux-11076708_latest.zip -o /tmp/cmt.zip
    unzip -q -o /tmp/cmt.zip -d "$ANDROID_HOME/cmdline-tools/"
    mv "$ANDROID_HOME/cmdline-tools/cmdline-tools" "$ANDROID_HOME/cmdline-tools/latest"
  fi
  SDKM="$ANDROID_HOME/cmdline-tools/latest/bin/sdkmanager"
  yes | "$SDKM" --licenses >/dev/null 2>&1 || true
  NDK_PKG=""; [ "$TARGET_ABI" = "x86_64" ] && NDK_PKG="ndk;27.1.12297006"
  "$SDKM" "platform-tools" "platforms;android-35" "build-tools;35.0.0" $NDK_PKG >/dev/null 2>&1 || \
    "$SDKM" "platform-tools" "platforms;android-35" "build-tools;35.0.0" $NDK_PKG
  {
    echo "export ANDROID_HOME=/opt/android-sdk"
    echo "export ANDROID_SDK_ROOT=/opt/android-sdk"
    echo "export ANDROID_NDK_HOME=/opt/android-sdk/ndk/27.1.12297006/"
    echo "export TARGET_ABI=$TARGET_ABI"
    echo 'export PATH="$PATH:/usr/local/go/bin:/opt/android-sdk/cmdline-tools/latest/bin:/opt/android-sdk/platform-tools"'
  } > /etc/profile.d/yaver-shoot.sh
  echo "toolchain: $(docker --version) | $(node --version) | $(go version) | java $(java -version 2>&1|head -1)"
REMOTE
rsh_script 3 /tmp/yaver-shoot-phase2.sh >>"$LOG" 2>&1 || die "toolchain install failed (see log)"

say "PHASE 2.5 — ensure binder_linux (redroid prereq; Hetzner's kernel often lacks a published modules-extra)"
if rsh "modprobe binder_linux 2>/dev/null && echo HAVE" 2>/dev/null | grep -q HAVE; then
  say "binder_linux already loadable"
else
  say "binder module missing for $(rsh uname -r) — installing a stock kernel + modules-extra and rebooting"
  rsh "bash -s" <<'REMOTE' >>"$LOG" 2>&1 || say "kernel install had warnings"
    set -x
    export DEBIAN_FRONTEND=noninteractive
    apt-get install -y -qq linux-generic linux-modules-extra-generic || apt-get install -y -qq linux-image-generic linux-modules-extra-$(uname -r) || true
REMOTE
  say "rebooting box to pick up the stock kernel"
  rsh "nohup sh -c 'sleep 1; reboot' >/dev/null 2>&1 &" || true
  sleep 25
  bash "$HCLOUD_DIR/wait-for-ssh.sh" >>"$LOG" 2>&1 || die "box did not come back after reboot"
  if rsh "modprobe binder_linux 2>/dev/null && echo HAVE" 2>/dev/null | grep -q HAVE; then
    say "binder_linux loadable after reboot ($(rsh uname -r))"
  else
    say "WARNING: binder_linux STILL unavailable after reboot ($(rsh uname -r)) — redroid will fail; consider a pre-baked snapshot"
  fi
fi

say "PHASE 3 — sync repo on the box (git pull from GitHub — robust, no Mac↔box transfer)"
# rsync from the Mac kept dropping on the flaky Mac↔box link. Everything is
# committed to main, so the box pulls directly from GitHub (box→GitHub is
# reliable). git reset --hard (no clean) preserves node_modules + caches → fast.
cat > /tmp/yaver-shoot-phase3.sh <<'REMOTE'
  set -ex
  if [ -d /opt/yaver/.git ]; then
    cd /opt/yaver
    git fetch origin main
    git reset --hard origin/main
  else
    rm -rf /opt/yaver
    git clone https://github.com/kivanccakmak/yaver.io.git /opt/yaver
  fi
  git -C /opt/yaver rev-parse --short HEAD
REMOTE
rsh_script 3 /tmp/yaver-shoot-phase3.sh >>"$LOG" 2>&1 || die "git sync failed"

say "PHASE 4 — build x86_64 sandbox payload + debug APK (heavy, ~30-45m)"
# build-android-sandbox.sh ABI=x86_64 cross-compiles libyaver + fetches proot into
# jniLibs/x86_64; then gradle assembleDebug bakes them + the new Kotlin in.
cat > /tmp/yaver-shoot-phase4.sh <<'REMOTE'
  set -x
  . /etc/profile.d/yaver-shoot.sh
  cd /opt/yaver/mobile
  # 1) JS deps
  npm ci --legacy-peer-deps || npm install --legacy-peer-deps || echo "npm-FAILED"
  # 1b) preserve the committed AndroidManifest.xml (declares SandboxService +
  #     FOREGROUND_SERVICE_SPECIAL_USE + subtype) — `prebuild --clean` regenerates
  #     it and DROPS the hand-declared service (run 6: analyzer found no service →
  #     FGS never started → no notification). Mirrors CLAUDE.md cold-start "restore
  #     force-tracked overlays after prebuild".
  cp android/app/src/main/AndroidManifest.xml /tmp/yaver-manifest.bak
  # CRITICAL: back up ALL committed custom Kotlin (MainApplication, the Yaver*
  # modules, and the sandbox/ package incl. SandboxService) — prebuild --clean
  # regenerates android/ and DROPS them, which is why SandboxService was missing
  # → ClassNotFoundException → no notification. Restoring just the manifest (which
  # only *declares* the service) was the bug.
  rm -rf /tmp/yaver-java.bak && cp -r android/app/src/main/java /tmp/yaver-java.bak
  # 2) regenerate a COMPLETE native android project (--clean: the repo only
  #    force-tracks overlay files, so reuse fails at settings.gradle).
  npx expo prebuild --platform android --clean --no-install || echo "prebuild-FAILED"
  # 2b) restore the real manifest + ALL committed Kotlin sources over the regen'd ones
  cp /tmp/yaver-manifest.bak android/app/src/main/AndroidManifest.xml
  cp -r /tmp/yaver-java.bak/io android/app/src/main/java/
  test -f android/app/src/main/java/io/yaver/mobile/sandbox/SandboxService.kt && echo "sources: SandboxService.kt restored" || echo "sources: WARN SandboxService missing"
  # 3) cross-compile libyaver(x86_64)+proot into jniLibs/x86_64 AFTER prebuild
  #    (prebuild --clean would otherwise wipe them).
  cd /opt/yaver
  ABI="${TARGET_ABI:-arm64-v8a}" bash scripts/build-android-sandbox.sh || echo "sandbox-payload-FAILED"
  ls -la "mobile/android/app/src/main/jniLibs/${TARGET_ABI:-arm64-v8a}/" 2>/dev/null || echo "no ${TARGET_ABI} jniLibs"
  # 4) debug APK, gradle capped to the box RAM
  cd mobile/android
  # bound memory: heap + single worker + no parallel + no RN per-arch duplication
  # (we only need x86_64) to avoid the OOM that killed run 4.
  printf 'org.gradle.jvmargs=-Xmx6g -XX:MaxMetaspaceSize=1g\norg.gradle.daemon=false\norg.gradle.parallel=false\norg.gradle.workers.max=2\nreactNativeArchitectures=%s\n' "${TARGET_ABI:-arm64-v8a}" >> gradle.properties
  ./gradlew :app:assembleDebug --no-daemon --stacktrace --max-workers=2 || echo "gradle-FAILED"
  find /opt/yaver/mobile/android -name '*.apk' -path '*debug*' -print
REMOTE
rsh_script 2 /tmp/yaver-shoot-phase4.sh >>"$LOG" 2>&1 || say "APK build had warnings (check log)"

say "PHASE 5 — boot redroid, install APK, drive the use-case capture, record"
# This phase reuses the agent's studio capture layer over the local runner ON the
# box. The agent binary + studio package run there; we invoke the recorder with
# the narrative use-case config. See desktop/agent/studio + ops_studio.go.
# env baked into the script file (so rsh_script can re-feed it on retry).
{ printf "export GLM_API_KEY=%q YAVER_SESSION_TOKEN=%q SHOOT_MODE=%q\n" \
    "${GLM_API_KEY:-${ZAI_API_KEY:-}}" "${YAVER_SESSION_TOKEN:-}" "${SHOOT_MODE:-fgs}"
  cat <<'REMOTE'
  set -x
  . /etc/profile.d/yaver-shoot.sh
  cd /opt/yaver
  # load binder for redroid (privileged helper, no host change needed)
  modprobe binder_linux devices="binder,hwbinder,vndbinder" 2>/dev/null || \
    docker run --rm --privileged -v /lib/modules:/lib/modules debian:bookworm-slim \
      bash -c 'apt-get update -qq && apt-get install -y -qq kmod && modprobe binder_linux devices=binder,hwbinder,vndbinder' || true
  APK="$(find /opt/yaver/mobile/android -name '*.apk' -path '*debug*' | head -1)"
  echo "APK=$APK"
  [ -n "$APK" ] || { echo "no APK built — aborting capture"; exit 3; }
  mkdir -p /root/redroid-data
  # Build the agent for linux/amd64 so we can run the studio recorder on the box.
  ( cd desktop/agent && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /usr/local/bin/yaver-agent . ) || echo "agent-build-FAILED"
  # The studio recorder reads its narrative job spec from a file; write it.
  # SHOOT_MODE=fgs (default): start the foreground service DIRECTLY (no sign-in)
  #   → real Yaver foreground notification + clean use-case captions, no failing
  #     in-app task steps. This is the ship-now FGS justification video.
  # SHOOT_MODE=glm: drive the Tasks UI to run a real GLM coding task (needs the
  #   on-redroid sign-in gate solved first — the follow-up iteration).
  if [ "${SHOOT_MODE:-fgs}" = "glm" ]; then
    USECASE='{"whatRuns":"an on-device coding agent running a real task","progressText":"running","completionText":"Task finished","taskActions":[{"kind":"taptext","text":"Tasks","caption":"Give the on-device agent a real task","sec":3},{"kind":"type","text":"create a hello world node script and run it","sec":2},{"kind":"key","text":"ENTER","sec":2}]}'
  else
    USECASE='{"whatRuns":"the on-device coding agent (the Yaver sandbox) the user starts"}'
  fi
  cat > /root/fgs-job.json <<JOB
{
  "permission": "FOREGROUND_SERVICE_SPECIAL_USE",
  "path": "/opt/yaver/mobile",
  "manifest": "/opt/yaver/mobile/android/app/src/main/AndroidManifest.xml",
  "app": "Yaver",
  "apk": "$APK",
  "package": "io.yaver.mobile",
  "activity": ".MainActivity",
  "startAction": "io.yaver.mobile.sandbox.START",
  "hostWorkDir": "/root/redroid-data",
  "maxSec": 90,
  "useCase": $USECASE
}
JOB
  # The recorder subcommand drives RedroidSurface + UseCaseProofSteps and writes
  # permission-demo(-captioned).mp4 + justification.md next to the job file.
  yaver-agent studio permission-video --capture --job /root/fgs-job.json --out /root/fgs-out || echo "recorder-FAILED (see log)"
  ls -la /root/fgs-out 2>/dev/null || true
REMOTE
} > /tmp/yaver-shoot-phase5.sh
rsh_script 2 /tmp/yaver-shoot-phase5.sh >>"$LOG" 2>&1 || say "capture phase had warnings (check log)"

say "PHASE 6 — pull artifacts"
mkdir -p "$OUT_DIR"
pulled=""
for i in 1 2 3 4; do
  if scp "${SSH_OPTS[@]}" -r "root@$IP:/root/fgs-out/*" "$OUT_DIR/" >>"$LOG" 2>&1; then pulled=1; break; fi
  say "artifact pull attempt $i failed (flaky link) — retrying in 10s"
  sleep 10
done
[ -n "$pulled" ] || say "no artifacts pulled (capture likely failed, or link down — see $LOG)"
say "artifacts in $OUT_DIR:"; ls -la "$OUT_DIR" | tee -a "$LOG"

say "DONE — box will be deleted by trap"
