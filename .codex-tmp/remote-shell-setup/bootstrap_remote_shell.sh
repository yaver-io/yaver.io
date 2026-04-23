#!/usr/bin/env bash
set -euo pipefail

backup_if_exists() {
  local path="$1"
  if [[ -e "$path" && ! -e "${path}.pre-codex-bak" ]]; then
    cp -R "$path" "${path}.pre-codex-bak"
  fi
}

backup_if_exists "$HOME/.zshrc"
backup_if_exists "$HOME/.tmux.conf"

mkdir -p "$HOME/.config"

if [[ ! -x /opt/homebrew/bin/brew ]]; then
  NONINTERACTIVE=1 bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
fi

eval "$(/opt/homebrew/bin/brew shellenv)"

grep -qxF 'eval "$(/opt/homebrew/bin/brew shellenv)"' "$HOME/.zprofile" 2>/dev/null || \
  printf '\neval "$(/opt/homebrew/bin/brew shellenv)"\n' >> "$HOME/.zprofile"

brew install tmux fzf ripgrep

if [[ ! -d "$HOME/.oh-my-zsh" ]]; then
  RUNZSH=no CHSH=no KEEP_ZSHRC=yes sh -c "$(curl -fsSL https://raw.githubusercontent.com/ohmyzsh/ohmyzsh/master/tools/install.sh)"
fi

mkdir -p "$HOME/.tmux/plugins"
if [[ ! -d "$HOME/.tmux/plugins/tpm" ]]; then
  git clone https://github.com/tmux-plugins/tpm "$HOME/.tmux/plugins/tpm"
fi

cp "$HOME/.zshrc.codex-new" "$HOME/.zshrc"
cp "$HOME/.tmux.conf.codex-new" "$HOME/.tmux.conf"

TMUX_PLUGIN_MANAGER_PATH="$HOME/.tmux/plugins" "$HOME/.tmux/plugins/tpm/bin/install_plugins" >/dev/null 2>&1 || true

zsh -lic 'command -v brew; command -v tmux; command -v rg; test -d ~/.oh-my-zsh; echo zsh-ready'
