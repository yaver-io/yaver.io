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
  ufw iproute2 net-tools

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
