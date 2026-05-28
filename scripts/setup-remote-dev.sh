#!/usr/bin/env bash
#
# yaver.io/scripts/setup-remote-dev.sh
#
# Bootstraps any Linux host (Ubuntu 22.04+ or any Debian-family distro) as
# an always-on remote dev box for the AR-glasses + SSH workflow:
#
#   AR host (Xreal Beam Pro / iPhone) ──→ SSH ──→ your dev host
#                                                  ├─ tmux session "main"
#                                                  ├─ mosh-server (latency mask)
#                                                  ├─ Claude Code CLI
#                                                  ├─ OpenAI Codex CLI
#                                                  └─ Yaver Go agent
#
# Host-agnostic — the script does not depend on any specific cloud provider.
# Run it on a Yaver managed-cloud box, your own VM (any cloud), a
# self-hosted NUC, or a Linux laptop. Auto-detects CPU arch (ARM64/x86_64).
#
# Idempotent — safe to re-run.
#
# Usage (from your laptop, after copying the script):
#   scp scripts/setup-remote-dev.sh user@<your-dev-host>:/tmp/
#   ssh user@<your-dev-host> bash /tmp/setup-remote-dev.sh
#
# Defaults assume Ubuntu/Debian + apt. For other distros, swap the package
# manager calls.

set -euo pipefail

REMOTE_USER="${REMOTE_USER:-$(whoami)}"
TMUX_SESSION="${TMUX_SESSION:-main}"
NODE_MAJOR="${NODE_MAJOR:-20}"

log() { printf '\n\033[1;36m▶ %s\033[0m\n' "$*"; }
warn() { printf '\033[1;33m⚠ %s\033[0m\n' "$*"; }
fail() { printf '\033[1;31m✗ %s\033[0m\n' "$*"; exit 1; }

# Need sudo for apt/systemd; if running as root we skip the prefix.
if [ "$(id -u)" -eq 0 ]; then SUDO=""; else SUDO="sudo"; fi

# ─── 1. Base OS packages ──────────────────────────────────────────────────
log "Updating apt + installing base packages (tmux, mosh, git, build tools)"
export DEBIAN_FRONTEND=noninteractive
$SUDO apt-get update -qq
$SUDO apt-get install -y -qq \
  tmux \
  mosh \
  git \
  curl \
  wget \
  ca-certificates \
  build-essential \
  python3-pip \
  ripgrep \
  fd-find \
  bat \
  htop \
  jq \
  unzip \
  fzf

# ─── 2. Node.js (for Claude Code + Codex CLI) ─────────────────────────────
if ! command -v node >/dev/null 2>&1 || [ "$(node -v | cut -d. -f1 | tr -d v)" -lt "$NODE_MAJOR" ]; then
  log "Installing Node.js $NODE_MAJOR.x"
  curl -fsSL "https://deb.nodesource.com/setup_${NODE_MAJOR}.x" | $SUDO bash -
  $SUDO apt-get install -y -qq nodejs
fi
log "Node $(node -v) + npm $(npm -v) ready"

# ─── 3. Claude Code CLI ───────────────────────────────────────────────────
if ! command -v claude >/dev/null 2>&1; then
  log "Installing Claude Code CLI (@anthropic-ai/claude-code)"
  $SUDO npm install -g @anthropic-ai/claude-code
fi
if claude --version >/dev/null 2>&1; then
  log "Claude Code: $(claude --version)"
else
  log "Claude Code: installed; run 'claude login' inside tmux to complete auth"
fi

# ─── 4. OpenAI Codex CLI ──────────────────────────────────────────────────
if ! command -v codex >/dev/null 2>&1; then
  log "Installing OpenAI Codex CLI (@openai/codex)"
  $SUDO npm install -g @openai/codex \
    || warn "Codex install failed — package name may have moved; try @openai/codex-cli or skip"
fi

# ─── 5. Yaver Go agent — placeholder ──────────────────────────────────────
# When/if the Go agent is published as a binary release, drop the curl-and-
# install snippet here. For now, leave a comment so the operator knows where
# to plug it in.
log "Yaver Go agent: install separately via your usual yaver_lazy_setup or yaver_managed_cloud_onboarding flow"

# ─── 6. Tmux config tuned for AR-glasses readability ──────────────────────
HOME_DIR="$(eval echo "~${REMOTE_USER}")"
log "Writing $HOME_DIR/.tmux.conf for AR-glasses readability"
$SUDO -u "$REMOTE_USER" tee "$HOME_DIR/.tmux.conf" > /dev/null << 'TMUXCONF'
# Yaver remote-dev tmux config — designed for Xreal/Viture AR glasses where
# the virtual screen is ~200" but effective text resolution is modest.
# Keep panes wide, history deep, status bar minimal.

set -g default-terminal "tmux-256color"
set -ga terminal-overrides ",xterm-256color:Tc,*256col*:Tc"

# Bigger history because scroll-back is the only way to recover from a
# disconnect surge.
set -g history-limit 100000

# Mouse on — glasses + Bluetooth keyboard + occasional touchpad/phone scroll.
set -g mouse on

# Faster escape — avoid the half-second neovim lag.
set -sg escape-time 10

# Vi-style copy mode (works in Termux too).
setw -g mode-keys vi

# Minimal, high-contrast status bar so glasses can read it at a glance.
set -g status-style "bg=#1e293b,fg=#e2e8f0"
set -g status-left  "#[fg=#22d3ee,bold] #S #[default]"
set -g status-right "#[fg=#94a3b8]%H:%M #[fg=#64748b]│ #[fg=#22d3ee]#h #[default]"
set -g status-left-length 30

# Window title with current path — glasses-friendly bold.
setw -g window-status-current-style "fg=#22d3ee,bold"
setw -g window-status-format         " #I:#W "
setw -g window-status-current-format " #I:#W "

# Panes — thicker borders so they're legible through 50° FoV.
set -g pane-border-style        "fg=#334155"
set -g pane-active-border-style "fg=#22d3ee"

# Reload config.
bind r source-file ~/.tmux.conf \; display-message "tmux.conf reloaded"

# Sensible split bindings (\| matches the visual orientation).
bind | split-window -h -c "#{pane_current_path}"
bind - split-window -v -c "#{pane_current_path}"

# Reachable pane navigation with vim-style hjkl.
bind h select-pane -L
bind j select-pane -D
bind k select-pane -U
bind l select-pane -R
TMUXCONF

# ─── 7. Persistent tmux session on boot ───────────────────────────────────
log "Creating systemd unit to keep tmux session '$TMUX_SESSION' alive at boot"
$SUDO tee /etc/systemd/system/tmux-main.service > /dev/null << EOF
[Unit]
Description=Persistent tmux session for remote dev
After=network.target

[Service]
Type=forking
User=$REMOTE_USER
ExecStart=/usr/bin/tmux new-session -d -s $TMUX_SESSION
ExecStop=/usr/bin/tmux kill-session -t $TMUX_SESSION
RemainAfterExit=yes
Restart=on-failure

[Install]
WantedBy=multi-user.target
EOF
$SUDO systemctl daemon-reload
$SUDO systemctl enable tmux-main.service >/dev/null
# Start it now if not already running.
$SUDO -u "$REMOTE_USER" tmux has-session -t "$TMUX_SESSION" 2>/dev/null \
  || $SUDO -u "$REMOTE_USER" tmux new-session -d -s "$TMUX_SESSION"

# ─── 8. Mosh — masks SSH latency, survives IP changes ─────────────────────
log "Opening UFW port range 60000-60010/udp for mosh (idempotent)"
if command -v ufw >/dev/null 2>&1; then
  $SUDO ufw allow 60000:60010/udp >/dev/null 2>&1 || true
fi

# ─── 9. Friendly motd announcing the workflow ─────────────────────────────
$SUDO tee /etc/motd > /dev/null << 'MOTD'

  ╔══════════════════════════════════════════════════════════════════════╗
  ║  Yaver remote-dev host — AR-glasses / Beam Pro workflow              ║
  ║                                                                       ║
  ║  Reconnect:    mosh user@<this-host>                                  ║
  ║  Resume:       tmux a -t main                                         ║
  ║  Tools:        claude · codex · yaver · git                          ║
  ║                                                                       ║
  ║  Auth on first run (device-code, no browser callback needed):         ║
  ║    claude login    → copy URL to phone browser                        ║
  ║    codex login     → same                                              ║
  ║    yaver auth link start → same                                        ║
  ║                                                                       ║
  ║  See BEAM_PRO_DEV.md for the full guide.                              ║
  ╚══════════════════════════════════════════════════════════════════════╝

MOTD

log "All done. Connect from your Beam Pro / Termux with:"
printf "\n    mosh %s@%s\n    tmux a -t %s\n\n" "$REMOTE_USER" "$(hostname -I | awk '{print $1}')" "$TMUX_SESSION"
log "Next: run \`claude login\` once inside tmux to complete OAuth."
