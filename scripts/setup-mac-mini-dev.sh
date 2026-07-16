#!/usr/bin/env bash
#
# Bootstrap a user-owned Mac mini as a Yaver remote slave for Yaver/Talos
# development. This is deliberately Darwin-only and Codex-first: the box is a
# build/simulator worker, not a GUI editor workstation.
#
# Usage:
#   scp scripts/setup-mac-mini-dev.sh mac-mini:/tmp/
#   ssh mac-mini bash /tmp/setup-mac-mini-dev.sh
#
# Useful env:
#   CODEX_MODEL=gpt-5.5                 Codex default model written to config
#   SKIP_XCODE_DOWNLOAD=1               Skip simulator runtime downloads
#   REMOVE_GUI_EDITORS=1                brew-uninstall Cursor/VS Code if present
#   YAVER_PROJECTS="$HOME/Workspace/yaver.io $HOME/Workspace/talos"

set -euo pipefail

CODEX_MODEL="${CODEX_MODEL:-gpt-5.5}"
NODE_MAJOR="${NODE_MAJOR:-20}"
SKIP_XCODE_DOWNLOAD="${SKIP_XCODE_DOWNLOAD:-0}"
REMOVE_GUI_EDITORS="${REMOVE_GUI_EDITORS:-0}"
YAVER_PROJECTS="${YAVER_PROJECTS:-$HOME/Workspace/yaver.io $HOME/Workspace/talos}"

log() { printf '\n==> %s\n' "$*"; }
warn() { printf 'WARN: %s\n' "$*" >&2; }
fail() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

if [[ "$(uname -s)" != "Darwin" ]]; then
  fail "this script is for macOS remote workers only"
fi

if [[ "$(id -u)" -eq 0 ]]; then
  fail "run as the normal logged-in developer user, not root"
fi

sudo -v

ensure_homebrew() {
  if command -v brew >/dev/null 2>&1; then
    eval "$("$(brew --prefix)"/bin/brew shellenv 2>/dev/null || true)"
    return
  fi
  log "Installing Homebrew"
  NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
  if [[ -x /opt/homebrew/bin/brew ]]; then
    eval "$(/opt/homebrew/bin/brew shellenv)"
  elif [[ -x /usr/local/bin/brew ]]; then
    eval "$(/usr/local/bin/brew shellenv)"
  else
    fail "Homebrew install finished but brew is not on a known path"
  fi
}

brew_install_if_missing() {
  local formula="$1"
  local bin="${2:-$1}"
  if command -v "$bin" >/dev/null 2>&1; then
    return
  fi
  log "Installing $formula"
  brew install "$formula"
}

ensure_xcode() {
  log "Checking Xcode"
  if ! command -v xcodebuild >/dev/null 2>&1; then
    fail "xcodebuild is missing. Install full Xcode from the App Store or Apple Developer first."
  fi
  local developer_dir
  developer_dir="$(xcode-select -p 2>/dev/null || true)"
  if [[ "$developer_dir" != /Applications/Xcode.app/* ]]; then
    if [[ -d /Applications/Xcode.app/Contents/Developer ]]; then
      log "Selecting /Applications/Xcode.app"
      sudo xcode-select -s /Applications/Xcode.app/Contents/Developer
    else
      fail "full Xcode is required; current developer dir is '${developer_dir:-unset}'"
    fi
  fi
  log "$(xcodebuild -version | tr '\n' ' ')"
  sudo xcodebuild -license accept >/dev/null 2>&1 || true
  sudo xcodebuild -runFirstLaunch -checkForNewerComponents
}

download_xcode_platforms() {
  if [[ "$SKIP_XCODE_DOWNLOAD" == "1" ]]; then
    warn "Skipping Xcode platform downloads because SKIP_XCODE_DOWNLOAD=1"
    return
  fi
  for platform in iOS tvOS watchOS visionOS; do
    log "Ensuring $platform simulator runtime is installed"
    if ! xcodebuild -downloadPlatform "$platform"; then
      warn "Could not download $platform runtime automatically; install it from Xcode Settings > Components if needed"
    fi
  done
}

runtime_id_for() {
  local platform="$1"
  xcrun simctl list runtimes 2>/dev/null \
    | grep "$platform" \
    | grep -v unavailable \
    | sed -n 's/.*(\(com\.apple\.CoreSimulator\.SimRuntime\.[^)]*\)).*/\1/p' \
    | tail -1 || true
}

device_type_for() {
  local pattern="$1"
  xcrun simctl list devicetypes 2>/dev/null \
    | grep "$pattern" \
    | sed -n 's/.*(\(com\.apple\.CoreSimulator\.SimDeviceType\.[^)]*\)).*/\1/p' \
    | head -1 || true
}

ensure_sim_device() {
  local name="$1"
  local device_pattern="$2"
  local runtime_pattern="$3"
  if xcrun simctl list devices 2>/dev/null | grep -Fq "$name ("; then
    log "Simulator '$name' already exists"
    return
  fi
  local dtype runtime
  dtype="$(device_type_for "$device_pattern")"
  runtime="$(runtime_id_for "$runtime_pattern")"
  if [[ -z "$dtype" || -z "$runtime" ]]; then
    warn "Skipping '$name' simulator; missing device type or runtime for $device_pattern / $runtime_pattern"
    return
  fi
  log "Creating simulator '$name'"
  xcrun simctl create "$name" "$dtype" "$runtime" >/dev/null
}

configure_codex() {
  log "Installing/updating Codex CLI"
  npm install -g @openai/codex

  local config_dir="$HOME/.codex"
  local config_file="$config_dir/config.toml"
  mkdir -p "$config_dir"
  touch "$config_file"

  if grep -q '^model = ' "$config_file"; then
    perl -0pi -e "s/^model = .*\$/model = \"$CODEX_MODEL\"/m" "$config_file"
  else
    printf 'model = "%s"\n' "$CODEX_MODEL" | cat - "$config_file" > "$config_file.tmp"
    mv "$config_file.tmp" "$config_file"
  fi

  if ! grep -q '^model_reasoning_effort = ' "$config_file"; then
    printf 'model_reasoning_effort = "medium"\n' >> "$config_file"
  fi

  for project in $YAVER_PROJECTS; do
    mkdir -p "$project"
    if ! grep -Fq "[projects.\"$project\"]" "$config_file"; then
      {
        printf '\n[projects."%s"]\n' "$project"
        printf 'trust_level = "trusted"\n'
      } >> "$config_file"
    fi
  done
}

configure_yaver() {
  log "Installing/updating Yaver CLI"
  npm install -g yaver-cli
  if command -v yaver >/dev/null 2>&1; then
    yaver mcp setup codex || warn "Yaver MCP setup for Codex failed; run 'yaver auth' and retry"
  fi
}

remove_gui_editors_if_requested() {
  if [[ "$REMOVE_GUI_EDITORS" != "1" ]]; then
    return
  fi
  log "Removing GUI editor casks managed by Homebrew"
  for cask in cursor visual-studio-code; do
    if brew list --cask "$cask" >/dev/null 2>&1; then
      brew uninstall --cask "$cask" || warn "Could not uninstall cask $cask"
    fi
  done
  for app in "/Applications/Cursor.app" "/Applications/Visual Studio Code.app"; do
    if [[ -d "$app" ]]; then
      ls -ld "$app"
      warn "$app is not Homebrew-managed; leaving it in place. Remove manually if desired."
    fi
  done
}

write_status_script() {
  local bin_dir="$HOME/.local/bin"
  mkdir -p "$bin_dir"
  cat > "$bin_dir/yaver-mac-mini-status" <<'STATUS'
#!/usr/bin/env bash
set -euo pipefail
echo "== Xcode =="
xcodebuild -version
echo
echo "== SDKs =="
xcodebuild -showsdks
echo
echo "== Simulators =="
xcrun simctl list devices available | grep -E 'Yaver-|iPhone|iPad|Apple TV|Apple Watch|Apple Vision' | head -80 || true
echo
echo "== Runners =="
command -v codex >/dev/null && codex --version || true
command -v yaver >/dev/null && yaver --version || true
STATUS
  chmod +x "$bin_dir/yaver-mac-mini-status"
}

ensure_homebrew

log "Installing build/runtime packages"
brew_install_if_missing node node
brew_install_if_missing git git
brew_install_if_missing ripgrep rg
brew_install_if_missing jq jq
brew_install_if_missing watchman watchman
brew_install_if_missing cocoapods pod

if [[ "$(node -v | sed 's/^v//' | cut -d. -f1)" -lt "$NODE_MAJOR" ]]; then
  warn "Node is older than $NODE_MAJOR.x; run 'brew upgrade node' if builds complain"
fi

ensure_xcode
download_xcode_platforms

log "Creating named simulator devices for Yaver surfaces"
ensure_sim_device "Yaver-Mobile" "iPhone" "iOS"
ensure_sim_device "Yaver-Tablet" "iPad" "iOS"
ensure_sim_device "Yaver-TV" "Apple TV" "tvOS"
ensure_sim_device "Yaver-Watch" "Apple Watch" "watchOS"
ensure_sim_device "Yaver-Vision" "Apple Vision" "visionOS"
warn "CarPlay uses the iOS simulator/runtime plus app entitlements; there is no separate CarPlay simulator runtime to create here."

configure_codex
configure_yaver
remove_gui_editors_if_requested
write_status_script

log "Mac mini remote worker bootstrap complete"
printf 'Next steps on the Mac mini:\n'
printf '  yaver auth --headless\n'
printf '  codex login --device-auth\n'
printf '  yaver serve\n'
printf '  yaver-mac-mini-status\n'
