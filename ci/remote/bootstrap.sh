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
export NEEDRESTART_MODE=a

log "apt base"
apt-get update -y
apt-get install -y --no-install-recommends \
  ca-certificates curl gnupg git jq rsync tmux unzip zip \
  build-essential pkg-config \
  python3 python3-venv python3-pip pipx \
  software-properties-common lsb-release \
  ufw iproute2 net-tools bubblewrap uidmap

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
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg | \
    gpg --dearmor -o /etc/apt/keyrings/docker.gpg
  chmod a+r /etc/apt/keyrings/docker.gpg
  arch="$(dpkg --print-architecture)"
  codename="$(. /etc/os-release && echo "$VERSION_CODENAME")"
  echo "deb [arch=${arch} signed-by=/etc/apt/keyrings/docker.gpg] \
    https://download.docker.com/linux/ubuntu ${codename} stable" \
    > /etc/apt/sources.list.d/docker.list
  apt-get update -y
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
  curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-arm64.tar.gz" \
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
if ! command -v ollama >/dev/null 2>&1; then
  curl -fsSL https://ollama.com/install.sh | sh
fi
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

log "aider via pipx"
# pipx needs a normal PATH setup; run it as root against /usr/local
export PIPX_HOME=/opt/pipx
export PIPX_BIN_DIR=/usr/local/bin
mkdir -p "$PIPX_HOME"
if ! command -v aider >/dev/null 2>&1; then
  pipx install --force aider-chat
fi

log "opencode"
if ! command -v opencode >/dev/null 2>&1; then
  # Official installer puts binary in ~/.opencode/bin
  curl -fsSL https://opencode.ai/install | bash
  # Symlink into PATH for non-interactive shells
  if [ -x /root/.opencode/bin/opencode ]; then
    ln -sf /root/.opencode/bin/opencode /usr/local/bin/opencode
  fi
fi

log "yaver CLI (linux-arm64)"
if ! command -v yaver >/dev/null 2>&1; then
  deb_url="$(curl -fsSL https://api.github.com/repos/kivanccakmak/yaver.io/releases/latest \
    | jq -r '.assets[]? | select(.name|test("_arm64\\.deb$")) | .browser_download_url' \
    | head -1)"
  tgz_url="$(curl -fsSL https://api.github.com/repos/kivanccakmak/yaver.io/releases/latest \
    | jq -r '.assets[]? | select(.name=="yaver-linux-arm64.tar.gz") | .browser_download_url' \
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
