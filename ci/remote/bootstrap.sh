#!/usr/bin/env bash
# Bootstrap the yaver-test-ephemeral box (Ubuntu 24.04, arm64).
#
# Idempotent: safe to rerun. Installs every toolchain we need for
# Yaver remote verification + guest-sharing + hybrid-mode tests.
#
# This box is NOT a permanent service. It's a throwaway test target.
# Do not put secrets here. Do not depend on it being up.
set -euo pipefail

log()  { printf '\n=== %s ===\n' "$*"; }

export DEBIAN_FRONTEND=noninteractive
# Optional: skip Ollama (on-box local inference). The managed/dev golden cloud
# image sets YAVER_SKIP_OLLAMA=1 — inference runs via the gateway, not on-box —
# which trims image size + bake time. Default (unset/0) keeps Ollama for the
# test box's local-voice path.
SKIP_OLLAMA="${YAVER_SKIP_OLLAMA:-0}"
export NEEDRESTART_MODE=a

# Target architecture. The box may be arm64 (cax) OR amd64 (cx/cpx) — everything
# arch-specific below (Go tarball, JDK path, Android ABI, yaver asset, apt
# hygiene) keys off DEB_ARCH so the SAME bootstrap runs on either. Added
# 2026-07-11 so a Hetzner arm capacity outage isn't fatal — when cax is sold out
# we provision cx (amd64) instead. See docs/yaver-provisioning-robustness.md.
DEB_ARCH="$(dpkg --print-architecture 2>/dev/null || echo arm64)"
case "$DEB_ARCH" in
  amd64) GO_ARCH=amd64; ANDROID_ABI=x86_64;   YAVER_ARCH=amd64 ;;
  *)     DEB_ARCH=arm64; GO_ARCH=arm64; ANDROID_ABI=arm64-v8a; YAVER_ARCH=arm64 ;;
esac
log "target arch: $DEB_ARCH (go=$GO_ARCH android=$ANDROID_ABI)"

log "apt base"
# Multi-arch hygiene FIRST. On a previous-state box where someone ran
# `dpkg --add-architecture amd64`, the Hetzner ports mirror 404s on
# amd64 indices and each retry takes ~15s to time out — running
# apt-get update before this cleanup blows >5 min on dead lookups.
# Removing the foreign architecture is cheap (dpkg state edit, no
# network), and only needs the apt update to follow once.
if dpkg --print-foreign-architectures 2>/dev/null | grep -q amd64; then
  log "removing leaked amd64 foreign architecture"
  dpkg --remove-architecture amd64 2>&1 | head -3 || true
fi

# Apply Acquire::Retries=0 + 5s timeouts GLOBALLY for every apt-get in
# this bootstrap (and all future runs on this box). Without this, the
# nested apt-get update calls inside the docker / nodesource / etc.
# blocks bypass the fail-fast envelope and re-introduce the multi-
# minute hang on dead amd64 mirror lookups. Persistent file is the
# right scope: the box is a CI test target, owner is in full control,
# and a fast-fail policy is the right default for a fresh provision.
install -m 0644 /dev/stdin /etc/apt/apt.conf.d/99-yaver-fast.conf <<'EOF'
Acquire::Retries "0";
Acquire::http::Timeout "5";
Acquire::https::Timeout "5";
EOF

# Strip explicit `[arch=amd64]` references from any sources.list.d/* entry left
# behind by prior provisioning — ARM boxes only. On an amd64 box amd64 IS the
# native arch, so stripping it (or the arm64-force rewrite) would break apt; skip
# the whole arm-only hygiene there.
if [ "$DEB_ARCH" = "arm64" ]; then
  find /etc/apt/sources.list.d -type f -name '*.list' -exec \
    sed -i 's/\[arch=amd64\]//g; s/\[arch=amd64,arm64\]/[arch=arm64]/g' {} \;
  sed -i 's/\[arch=amd64\]//g; s/\[arch=amd64,arm64\]/[arch=arm64]/g' /etc/apt/sources.list 2>/dev/null || true
fi

apt-get update -y || true
apt-get install -y --no-install-recommends \
  ca-certificates curl gnupg git jq rsync tmux unzip zip \
  build-essential pkg-config \
  python3 python3-venv python3-pip pipx \
  software-properties-common lsb-release \
  ufw iproute2 net-tools bubblewrap uidmap \
  unattended-upgrades \
  sudo

# OS security auto-upgrades — one of the golden cloud-image's auto-upgrade
# layers (the yaver agent self-updates its own binary separately; a weekly
# re-bake refreshes the base image). Enable non-interactively; best-effort.
dpkg-reconfigure -f noninteractive unattended-upgrades 2>/dev/null || true
systemctl enable --now unattended-upgrades 2>/dev/null || true

log "yaver user — non-root home for Workspace + agent"
# Everything user-facing (cloned repos, the agent itself, opencode/codex/aider
# configs) belongs under /home/yaver, NOT /root. The box may be cloned from a
# snapshot where this user already exists; useradd's --comment "" flag plus
# the existence check keeps it idempotent.
if ! id yaver >/dev/null 2>&1; then
  useradd --create-home --shell /bin/bash --comment "Yaver agent" yaver
fi
install -d -m 0755 -o yaver -g yaver /home/yaver/Workspace
# Passwordless sudo for installs the agent triggers itself (apt, systemctl
# restart yaver-agent, etc). Scoped to yaver only — other users untouched.
install -m 0440 /dev/stdin /etc/sudoers.d/90-yaver <<'EOF'
yaver ALL=(ALL) NOPASSWD: ALL
EOF

log "codex/runner sandbox prerequisites"
cat >/etc/sysctl.d/99-yaver-runner-sandbox.conf <<'EOF'
kernel.unprivileged_userns_clone=1
user.max_user_namespaces=1048576
EOF
if [ -f /proc/sys/kernel/apparmor_restrict_unprivileged_userns ]; then
  echo "kernel.apparmor_restrict_unprivileged_userns=0" >> /etc/sysctl.d/99-yaver-runner-sandbox.conf
fi
sysctl --system >/dev/null 2>&1 || true

log "docker"
if ! command -v docker >/dev/null 2>&1; then
  install -m 0755 -d /etc/apt/keyrings
  # --no-tty + --batch so gpg doesn't try to open /dev/tty when this
  # block runs over a non-interactive ssh pipe (e.g. `ssh box bash -s`
  # or `tee` redirection). Newer gpg builds (>= 2.4) refuse to read
  # without --no-tty when stdin isn't a tty, hard-failing the script
  # under set -e.
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg | \
    gpg --no-tty --batch --yes --dearmor -o /etc/apt/keyrings/docker.gpg
  chmod a+r /etc/apt/keyrings/docker.gpg
  arch="$(dpkg --print-architecture)"
  codename="$(. /etc/os-release && echo "$VERSION_CODENAME")"
  echo "deb [arch=${arch} signed-by=/etc/apt/keyrings/docker.gpg] \
    https://download.docker.com/linux/ubuntu ${codename} stable" \
    > /etc/apt/sources.list.d/docker.list
  # Tolerate the amd64-leak noise like every other apt-get update in
  # this script — set -e treats apt's E: lines as fatal otherwise.
  apt-get update -y || true
  apt-get install -y docker-ce docker-ce-cli containerd.io \
    docker-buildx-plugin docker-compose-plugin
  systemctl enable --now docker
fi
if id yaver >/dev/null 2>&1; then
  usermod -aG docker yaver || true
fi

log "node 22 (nodesource)"
if ! command -v node >/dev/null 2>&1 || \
   ! node --version 2>/dev/null | grep -q '^v2[2-9]'; then
  curl -fsSL https://deb.nodesource.com/setup_22.x | bash -
  apt-get install -y nodejs
fi

log "go 1.22"
GO_VERSION="1.22.8"
if ! /usr/local/go/bin/go version 2>/dev/null | grep -q "go${GO_VERSION}"; then
  rm -rf /usr/local/go
  curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz" \
    -o /tmp/go.tgz
  tar -C /usr/local -xzf /tmp/go.tgz
  rm -f /tmp/go.tgz
  install -m 0644 /dev/stdin /etc/profile.d/go.sh <<'EOF'
export PATH=/usr/local/go/bin:$PATH
export GOPATH=$HOME/go
EOF
fi
export PATH=/usr/local/go/bin:$PATH

log "ollama"
if [ "$SKIP_OLLAMA" = "1" ]; then
  log "ollama: skipped (YAVER_SKIP_OLLAMA=1)"
elif ! command -v ollama >/dev/null 2>&1; then
  curl -fsSL https://ollama.com/install.sh | sh
fi

# ─────────────────────────────────────────────────────────────────
# Remote-runtime toolchains: Java 17 + Android SDK + Flutter +
# WebRTC capture stack (ffmpeg/GStreamer) + qemu/binfmt + zram swap.
# Required for Yaver to boot a Flutter or Kotlin app on this box and
# stream the emulator screen back to the user's phone over WebRTC.
# ─────────────────────────────────────────────────────────────────

log "java 17 (for android sdkmanager + gradle)"
if ! command -v java >/dev/null 2>&1 || ! java -version 2>&1 | grep -q '"17'; then
  apt-get install -y openjdk-17-jdk-headless
fi
install -m 0644 /dev/stdin /etc/profile.d/java.sh <<'EOF'
export JAVA_HOME=/usr/lib/jvm/java-17-openjdk-${DEB_ARCH}
EOF

log "android sdk + emulator + ARM64-v8a system image"
ANDROID_SDK_ROOT=/opt/android-sdk
export ANDROID_SDK_ROOT ANDROID_HOME=$ANDROID_SDK_ROOT
mkdir -p "$ANDROID_SDK_ROOT/cmdline-tools"
if [ ! -x "$ANDROID_SDK_ROOT/cmdline-tools/latest/bin/sdkmanager" ]; then
  CMDTOOLS_URL=https://dl.google.com/android/repository/commandlinetools-linux-11076708_latest.zip
  curl -fsSL "$CMDTOOLS_URL" -o /tmp/cmdtools.zip
  unzip -q -o /tmp/cmdtools.zip -d "$ANDROID_SDK_ROOT/cmdline-tools/"
  # archive extracts to ./cmdline-tools — the modern layout wants ./latest
  mv "$ANDROID_SDK_ROOT/cmdline-tools/cmdline-tools" "$ANDROID_SDK_ROOT/cmdline-tools/latest"
  rm -f /tmp/cmdtools.zip
fi
SDKMGR="$ANDROID_SDK_ROOT/cmdline-tools/latest/bin/sdkmanager"
export PATH="$ANDROID_SDK_ROOT/cmdline-tools/latest/bin:$ANDROID_SDK_ROOT/platform-tools:$ANDROID_SDK_ROOT/emulator:$PATH"
# Single combined install: --install accepts licenses inline when fed
# `yes`. Earlier we ran --licenses as a separate step before --install;
# that pattern sometimes left licenses partially accepted and the
# install then errored with "Package path is not valid" because the
# repository cache wasn't fully refreshed. Inline install fixes that
# AND auto-accepts only for the requested packages.
# google_atd is the headless-optimized Test Driver image (~40% smaller
# than google_apis_playstore, no Play surface) and ships ARM64-v8a
# variants that run native on Hetzner cax (Ampere Altra).
yes | "$SDKMGR" --install \
  "platform-tools" "emulator" \
  "platforms;android-35" \
  "system-images;android-35;google_atd;${ANDROID_ABI}" 2>&1 | tail -8 || true
# Belt-and-suspenders: confirm the bits we need actually landed.
"$SDKMGR" --list_installed 2>&1 \
  | grep -E "platform-tools|emulator|platforms;android-35|system-images;android-35" \
  | head -8 || true

# Default AVD. avdmanager-from-cmdline-tools/latest. -d 33 = Pixel 7
# device profile — sane defaults (1080x2400, 6.3", densities). Skip if
# already created.
AVDMGR="$ANDROID_SDK_ROOT/cmdline-tools/latest/bin/avdmanager"
if [ -x "$AVDMGR" ] && ! "$AVDMGR" list avd 2>&1 | grep -q "Yaver_API_35"; then
  echo "no" | "$AVDMGR" create avd \
    -n Yaver_API_35 \
    -k "system-images;android-35;google_atd;${ANDROID_ABI}" \
    -d "pixel_7" || true
fi
install -m 0644 /dev/stdin /etc/profile.d/android.sh <<EOF
export ANDROID_HOME=$ANDROID_SDK_ROOT
export ANDROID_SDK_ROOT=$ANDROID_SDK_ROOT
export PATH=\$PATH:\$ANDROID_HOME/cmdline-tools/latest/bin:\$ANDROID_HOME/platform-tools:\$ANDROID_HOME/emulator
EOF

log "flutter sdk (linux arm64 — git clone, no official tarball)"
# IMPORTANT: Flutter does NOT publish official Linux ARM64 tarballs
# (verified against releases_linux.json — zero ARM64 archive entries
# as of 3.27.4). The supported install path on aarch64 Linux is to
# git-clone the flutter repo and let Flutter bootstrap itself: the
# Dart SDK download IS published per-arch and `flutter --version`
# auto-fetches the right one on first run.
FLUTTER_ROOT=/opt/flutter
if [ ! -x "$FLUTTER_ROOT/bin/flutter" ]; then
  git clone --depth 1 -b stable https://github.com/flutter/flutter.git "$FLUTTER_ROOT"
fi
# git safe.directory: avoid "fatal: detected dubious ownership" when
# the agent later spawns flutter under a different user.
git config --global --add safe.directory "$FLUTTER_ROOT" || true
install -m 0644 /dev/stdin /etc/profile.d/flutter.sh <<EOF
export FLUTTER_ROOT=$FLUTTER_ROOT
export PATH=\$PATH:\$FLUTTER_ROOT/bin
EOF
# Pre-warm: triggers Dart SDK download for arm64 + initial pub-cache
# fill. ~1-2 min on first run, instant after. Allowed to fail —
# subsequent agent-driven `flutter` calls retry the bootstrap.
"$FLUTTER_ROOT/bin/flutter" --version 2>&1 | tail -3 || true

log "webrtc capture stack: ffmpeg + gstreamer + xvfb"
# ffmpeg with libx264 + x11grab is enough for the v1 capture pipeline
# (read framebuffer → encode H.264 → pipe stdout into Pion's NAL
# extractor). gstreamer is staged so the v2 pipeline (zero-copy
# kmssink → vaapih264enc) is one config flip away once we benchmark.
# xvfb is the headless X11 display — Swift+GTK Phase 2 needs it; the
# emulator path goes through adb screenrecord and doesn't, but we
# install it now since it's tiny and used by the doctor's
# remote-runtime preflight.
apt-get install -y \
  ffmpeg \
  gstreamer1.0-tools gstreamer1.0-plugins-good \
  gstreamer1.0-plugins-bad gstreamer1.0-libav \
  xvfb x11-utils dbus-x11

log "chromium for the browser-video recorder (browser_video.go)"
# The agent records its web tasks to MP4 headlessly (CDP screenshot loop →
# ffmpeg, no Xvfb). chromedp finds the browser via findChromePath
# (google-chrome / chromium / chromium-browser on PATH). Ubuntu 24.04 has no
# usable chromium .deb (Google Chrome ships no arm64 build, and `chromium` is a
# snap shim), so this is BEST-EFFORT and must never abort the image build:
# a missing browser only disables video recording, not the box. The Debian
# container image (Dockerfile.yaver-cloud) gets a proper chromium .deb instead.
apt-get install -y chromium-browser fonts-liberation \
  || apt-get install -y chromium fonts-liberation \
  || log "chromium unavailable on this distro/arch — browser video recording disabled"

log "qemu (TCG fallback for android emulator on no-KVM hosts)"
# Hetzner cloud doesn't expose /dev/kvm; the emulator uses TCG (pure
# software). qemu-system-arm provides the runtime; binfmt-support
# would let us cross-run x86_64 system images via translation if we
# ever needed to (we don't — Phase 1 sticks to ARM64 native).
apt-get install -y qemu-system-arm qemu-utils

log "zram swap (4 GB compressed) — survives Gradle daemon spikes on cax21 8GB"
# Until Hetzner has cax31 capacity we keep limping along on 8 GB. zram
# gives ~2-3 GB of compressed working RAM at the cost of CPU; under a
# Gradle/Flutter build OOM scenario this is what keeps the agent alive.
# Idempotent: only writes the unit if not already present.
apt-get install -y zram-tools
if [ ! -f /etc/default/zramswap ] || ! grep -q '^PERCENT=' /etc/default/zramswap; then
  install -m 0644 /dev/stdin /etc/default/zramswap <<'EOF'
ALGO=zstd
PERCENT=50
PRIORITY=100
EOF
  systemctl enable --now zramswap.service || true
fi

log "linger + sandbox — yaver user can run user-mode systemd units"
# Required for `yaver serve` running as a non-root user when the
# agent later orchestrates per-session emulator lifecycles via
# systemd-run --user.
if id yaver >/dev/null 2>&1; then
  loginctl enable-linger yaver || true
fi

log "remote-runtime preflight check"
# Surface what's installed + what's still missing so the
# yaver-doctor surface (and CI) can grep for it.
{
  echo "java=$(java -version 2>&1 | head -1)"
  echo "android_sdk=$ANDROID_SDK_ROOT"
  echo "android_avds=$($AVDMGR list avd 2>&1 | awk '/Name:/ {print $2}' | xargs)"
  echo "flutter=$($FLUTTER_ROOT/bin/flutter --version 2>&1 | head -1)"
  echo "ffmpeg=$(ffmpeg -version 2>&1 | head -1)"
  echo "kvm=$([ -e /dev/kvm ] && echo present || echo absent_TCG_fallback)"
  echo "zram=$(swapon --show 2>&1 | head -3 | tr '\n' ' ')"
} > /var/lib/yaver-remote-runtime.preflight 2>&1 || true
if [ "$SKIP_OLLAMA" = "1" ]; then
  log "ollama: enable + model pull skipped (YAVER_SKIP_OLLAMA=1)"
else
  systemctl enable --now ollama || true

  log "pull qwen2.5-coder:1.5b"
  # Retry once — first pull can race with ollama service coming up.
  for attempt in 1 2; do
    if ollama list 2>/dev/null | grep -q '^qwen2.5-coder:1.5b'; then
      break
    fi
    if ollama pull qwen2.5-coder:1.5b; then break; fi
    if [ "$attempt" = "1" ]; then sleep 5; fi
  done
fi

log "aider via pipx"
# pipx needs a normal PATH setup; run it as root against /usr/local
export PIPX_HOME=/opt/pipx
export PIPX_BIN_DIR=/usr/local/bin
mkdir -p "$PIPX_HOME"
if ! command -v aider >/dev/null 2>&1; then
  pipx install --force aider-chat
fi

log "claude code + codex (npm globals)"
# The two subscription-OAuth runners. Install-only: auth NEVER lands on this
# box at provision time — it arrives later via runner_auth_mirror /
# credentials_import from a signed-in machine, or `codex login --device-auth` /
# `claude auth login` relayed through the agent's browser-auth flow.
if ! command -v claude >/dev/null 2>&1; then
  npm install -g @anthropic-ai/claude-code || log "WARN: claude-code install failed (non-fatal)"
fi
if ! command -v codex >/dev/null 2>&1; then
  npm install -g @openai/codex || log "WARN: codex install failed (non-fatal)"
fi

log "opencode"
if ! command -v opencode >/dev/null 2>&1; then
  # Official installer puts binary in ~/.opencode/bin. Run it as the yaver
  # user so its config + bin land under /home/yaver/.opencode, then symlink
  # into PATH for non-interactive shells.
  sudo -iu yaver bash -c 'curl -fsSL https://opencode.ai/install | bash'
  if [ -x /home/yaver/.opencode/bin/opencode ]; then
    ln -sf /home/yaver/.opencode/bin/opencode /usr/local/bin/opencode
  fi
fi

log "yaver CLI (linux-$YAVER_ARCH)"
if ! command -v yaver >/dev/null 2>&1; then
  deb_url="$(curl -fsSL https://api.github.com/repos/kivanccakmak/yaver.io/releases/latest \
    | jq -r --arg a "_${YAVER_ARCH}.deb" '.assets[]? | select(.name|endswith($a)) | .browser_download_url' \
    | head -1)"
  tgz_url="$(curl -fsSL https://api.github.com/repos/kivanccakmak/yaver.io/releases/latest \
    | jq -r --arg n "yaver-linux-${YAVER_ARCH}.tar.gz" '.assets[]? | select(.name==$n) | .browser_download_url' \
    | head -1)"
  if [ -n "${deb_url:-}" ] && [ "$deb_url" != "null" ]; then
    curl -fsSL "$deb_url" -o /tmp/yaver.deb
    dpkg -i /tmp/yaver.deb
    rm -f /tmp/yaver.deb
  elif [ -n "${tgz_url:-}" ] && [ "$tgz_url" != "null" ]; then
    curl -fsSL "$tgz_url" -o /tmp/yaver.tgz
    tar -C /usr/local/bin -xzf /tmp/yaver.tgz yaver || tar -C /tmp -xzf /tmp/yaver.tgz
    # Some release tarballs nest the binary or use a different name.
    [ -x /tmp/yaver ] && install -m 0755 /tmp/yaver /usr/local/bin/yaver
    rm -f /tmp/yaver.tgz /tmp/yaver
  else
    echo "!! No arm64 yaver release asset found — install skipped."
  fi
fi

log "wire Yaver (+ Talos) MCP into runners"
# Register Yaver as an MCP server inside claude-code / codex / opencode so a
# runner launched on this box can call Yaver's own tools — and, when the
# operator has set TALOS_MCP_URL / TALOS_MCP_LICENSE, Talos's tools too
# (federated behind Yaver's MCP as an ACL peer). Runs as the `yaver` user
# because that's who owns the runner configs (~/.claude.json, ~/.codex,
# ~/.config/opencode) and who the agent spawns runners as. `mcp setup all`
# is idempotent and skips any runner that isn't installed, so it's safe to
# re-run on every golden-image rebuild and on box wake.
if command -v yaver >/dev/null 2>&1 && id yaver >/dev/null 2>&1; then
  sudo -iu yaver env \
    TALOS_MCP_URL="${TALOS_MCP_URL:-}" \
    TALOS_MCP_AUTH="${TALOS_MCP_AUTH:-}" \
    TALOS_MCP_LICENSE="${TALOS_MCP_LICENSE:-}" \
    bash -lc 'yaver mcp setup all' || log "WARN: mcp setup all failed (non-fatal)"
fi

log "system hermesc (linux-arm64 pre-warm)"
# arm64 Linux boxes have no embedded prebuilt in the Go agent (see
# desktop/agent/hermesc_embedded.go). Build hermesc once at
# provisioning time and drop it at /usr/local/libexec/yaver/hermesc
# so the agent's resolveHermesc() path doesn't stall on the first
# reload waiting for a 1–2 min CMake build.
if [ "$(uname -m)" = "aarch64" ] || [ "$(uname -m)" = "arm64" ]; then
  if [ -f /opt/yaver/ci/remote/install-hermesc.sh ]; then
    bash /opt/yaver/ci/remote/install-hermesc.sh || true
  elif [ -f "$(dirname "$0")/install-hermesc.sh" ]; then
    bash "$(dirname "$0")/install-hermesc.sh" || true
  else
    echo "!! ci/remote/install-hermesc.sh not found — arm64 reloads will build hermesc lazily on first push"
  fi
fi

log "yaver external watchdog (systemd)"
# Single-service design: the agent's in-process TaskSupervisor runs
# every scheduled tick (heartbeat, scheduler, Convex sync, smoke
# checks). This watchdog unit is a thin outer loop that only proves
# the agent itself is alive — checks the process + the beacon file
# the supervisor refreshes. Superseded ci/remote/smoke/* (the
# install.sh below removes those units automatically).
if [ -f /opt/yaver/ci/remote/watchdog/install.sh ]; then
  bash /opt/yaver/ci/remote/watchdog/install.sh install || true
elif [ -f "$(dirname "$0")/watchdog/install.sh" ]; then
  bash "$(dirname "$0")/watchdog/install.sh" install || true
else
  echo "!! ci/remote/watchdog/install.sh not found — skipping watchdog install"
fi

log "opt-in: enable in-agent relay-password smoke"
# The smoke task was a standalone timer; now it's a supervised task
# inside yaver serve. Gated by an env var so only boxes with explicit
# YAVER_ENABLE_RELAY_SMOKE=1 hit Convex every 15 min. We set it for
# yaver-test-ephemeral by design — that box exists to prove the
# platform works end-to-end.
install -d -m 0755 /etc/systemd/system/yaver-agent.service.d
cat > /etc/systemd/system/yaver-agent.service.d/relay-smoke.conf <<'EOF'
[Service]
Environment=YAVER_ENABLE_RELAY_SMOKE=1
EOF
systemctl daemon-reload
if systemctl is-active --quiet yaver-agent.service 2>/dev/null; then
  systemctl restart yaver-agent.service || true
fi

log "normalize ownership under /home/yaver/Workspace"
# Anything rsync'd in via `rsync -az` from a Mac (uid 501, gid staff) keeps
# its source uid:gid because rsync preserves ownership by default. That
# breaks codex's bwrap sandbox: bwrap drops CAP_DAC_OVERRIDE before spawning,
# so even root inside the sandbox is treated as unprivileged against the host
# DAC, and `codex exec` hard-fails with
#   bwrap: Can't create file at <proj>/.codex: Permission denied
# Idempotent + harmless if /home/yaver/Workspace doesn't exist yet.
if [ -d /home/yaver/Workspace ]; then
  chown -R yaver:yaver /home/yaver/Workspace || true
fi
# Legacy migration: a snapshot from before the yaver-user split may still
# carry /root/Workspace. Move it once, then drop the old dir. Skip silently
# on first-run boxes where it never existed.
if [ -d /root/Workspace ] && [ ! -L /root/Workspace ]; then
  log "migrating /root/Workspace -> /home/yaver/Workspace"
  rsync -aHAX --remove-source-files /root/Workspace/ /home/yaver/Workspace/ || true
  find /root/Workspace -depth -type d -empty -delete || true
  chown -R yaver:yaver /home/yaver/Workspace || true
fi

log "done"
echo "Installed versions:"
echo "  docker: $(docker --version 2>&1 | head -1)"
echo "  node:   $(node --version 2>&1)"
echo "  go:     $(/usr/local/go/bin/go version 2>&1)"
echo "  python: $(python3 --version 2>&1)"
echo "  ollama: $(ollama --version 2>&1 | head -1)"
echo "  aider:  $(aider --version 2>&1 | head -1)"
echo "  opencode: $(opencode --version 2>&1 | head -1 || echo 'not on PATH yet')"
echo "  yaver:  $(yaver --version 2>&1 | head -1 || echo 'not installed')"
