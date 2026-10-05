package main

// Cloud Workspace developer comfort defaults.
//
// These are deliberately scoped to Yaver-managed Cloud Workspace runtimes.
// A normal self-hosted `yaver serve` must never rewrite a developer's shell or
// editor configuration. Even on Cloud Workspace, every file is create-only:
// once it exists it belongs to the user and Yaver leaves it alone.
//
// The defaults contain public configuration only. No local dotfile is read,
// no home path/user name is embedded, and no auth/token/history material is
// copied. Optional assets are cloned from their public upstream repositories.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const cloudWorkspaceComfortMarker = "/etc/yaver/cloud-workspace"

const cloudWorkspaceZshRC = `# Yaver Cloud Workspace defaults.
# This file belongs to you now: edit or replace it freely.
export ZSH="$HOME/.oh-my-zsh"
ZSH_THEME="ys"
plugins=(git)

if [[ -r "$ZSH/oh-my-zsh.sh" ]]; then
  source "$ZSH/oh-my-zsh.sh"
fi

# fzf package layouts differ across Debian, Fedora, Arch, and Homebrew.
for yaver_fzf_file in \
  /usr/share/doc/fzf/examples/completion.zsh \
  /usr/share/doc/fzf/examples/key-bindings.zsh \
  /usr/share/fzf/completion.zsh \
  /usr/share/fzf/key-bindings.zsh \
  "$HOME/.fzf/shell/completion.zsh" \
  "$HOME/.fzf/shell/key-bindings.zsh"
do
  [[ -r "$yaver_fzf_file" ]] && source "$yaver_fzf_file"
done
unset yaver_fzf_file

export PATH="$HOME/.local/bin:$HOME/.yaver/runtimes/node/bin:$PATH"

alias ll='ls -alF'
alias la='ls -A'
alias l='ls -CF'
alias ..='cd ..'
alias ...='cd ../..'
alias v='vim'
alias gst='git status'
alias gco='git checkout'
alias gl='git log --oneline --decorate --graph -20'
alias ta='tmux new-session -A -s yaver-shell "exec zsh -l"'
`

const cloudWorkspaceTmuxConf = `# Yaver Cloud Workspace defaults.
# This file belongs to you now: edit or replace it freely.
set -g xterm-keys on
set -g default-terminal "tmux-256color"
set -as terminal-overrides ",xterm-256color:RGB"
set -g mouse on
set -g history-limit 1000000
set -g set-clipboard on
setw -g mode-keys vi

# Splits and new windows inherit the current pane's directory.
bind h split-window -h -c "#{pane_current_path}"
bind v split-window -v -c "#{pane_current_path}"
bind c new-window -c "#{pane_current_path}"
unbind '"'
unbind %

bind-key Up select-pane -U
bind-key Down select-pane -D
bind-key Left select-pane -L
bind-key Right select-pane -R

set -g status-bg "#262626"
set -g status-fg white
set -g status-right ""

set -g @plugin 'tmux-plugins/tpm'
set -g @plugin 'tmux-plugins/tmux-sensible'
set -g @plugin 'tmux-plugins/tmux-yank'
run-shell "$HOME/.tmux/plugins/tpm/tpm"
`

const cloudWorkspaceVimRC = `" Yaver Cloud Workspace defaults.
" This file belongs to you now: edit or replace it freely.
set nocompatible
filetype plugin indent on
syntax enable
set number
set relativenumber
set expandtab
set tabstop=4
set shiftwidth=4
set softtabstop=4
set autoindent
set smartindent
set hidden
set ignorecase
set smartcase
set incsearch
set hlsearch
set mouse=a
set backspace=indent,eol,start
set wildmenu
set scrolloff=5
set updatetime=300
set background=dark

if !empty(globpath(&runtimepath, 'colors/gruvbox.vim'))
  colorscheme gruvbox
endif

let mapleader=" "
nnoremap <leader><space> :nohlsearch<CR>
nnoremap <leader>b :buffers<CR>:buffer<Space>
nnoremap <leader>w :write<CR>
nnoremap <leader>q :quit<CR>
nnoremap ^ 0
nnoremap 0 ^
if has('clipboard')
  vnoremap <leader>y "+y
  nnoremap <leader>p "+p
endif
`

func cloudWorkspaceComfortEnabled() bool {
	return cloudWorkspaceComfortEnabledAt(cloudWorkspaceComfortMarker)
}

func cloudWorkspaceComfortEnabledAt(markerPath string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("YAVER_CLOUD_WORKSPACE"))) {
	case "1", "true", "yes", "on":
		return true
	}
	_, err := os.Stat(markerPath)
	return err == nil
}

// ensureCloudWorkspaceComfortDefaults writes only missing public config files.
// It is intentionally separate from asset downloads so serve startup is never
// blocked on GitHub or a package mirror.
func ensureCloudWorkspaceComfortDefaults(logf func(format string, v ...interface{})) error {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return fmt.Errorf("resolve Cloud Workspace home: %w", err)
	}
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}

	type defaultFile struct {
		path     string
		contents string
	}
	files := []defaultFile{
		{filepath.Join(home, ".zshrc"), cloudWorkspaceZshRC},
		{filepath.Join(home, ".tmux.conf"), cloudWorkspaceTmuxConf},
	}

	// Vim also accepts ~/.vim/vimrc. If either spelling exists, the user's
	// configuration wins and Yaver must not create a competing file.
	if !cloudComfortFileExists(filepath.Join(home, ".vimrc")) && !cloudComfortFileExists(filepath.Join(home, ".vim", "vimrc")) {
		files = append(files, defaultFile{filepath.Join(home, ".vimrc"), cloudWorkspaceVimRC})
	}

	for _, file := range files {
		created, writeErr := writeFileIfMissing(file.path, []byte(file.contents), 0o644)
		if writeErr != nil {
			return writeErr
		}
		if created {
			logf("Cloud Workspace: created editable default %s", trimHomePath(home, file.path))
		}
	}
	return nil
}

func cloudComfortFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func writeFileIfMissing(path string, contents []byte, mode os.FileMode) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("create config directory for %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if os.IsExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("create %s: %w", path, err)
	}
	if _, err = f.Write(contents); err != nil {
		_ = f.Close()
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	if err = f.Close(); err != nil {
		return false, fmt.Errorf("close %s: %w", path, err)
	}
	return true, nil
}

// ensureCloudWorkspaceComfortAssets is best-effort and background-safe. A
// failed clone leaves a fully usable stock zsh/tmux/vim setup; the next agent
// start retries only the still-missing public asset.
func ensureCloudWorkspaceComfortAssets(parent context.Context, logf func(format string, v ...interface{})) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}
	git := DiscoverBinary("git")
	if git == "" {
		logf("Cloud Workspace: git unavailable; Oh My Zsh/tmux/gruvbox defaults will retry later")
		return
	}

	assets := []struct {
		name string
		url  string
		dest string
	}{
		{"Oh My Zsh", "https://github.com/ohmyzsh/ohmyzsh.git", filepath.Join(home, ".oh-my-zsh")},
		{"tmux plugin manager", "https://github.com/tmux-plugins/tpm.git", filepath.Join(home, ".tmux", "plugins", "tpm")},
		{"Vim gruvbox", "https://github.com/morhetz/gruvbox.git", filepath.Join(home, ".vim", "pack", "yaver", "start", "gruvbox")},
	}

	for _, asset := range assets {
		if cloudComfortFileExists(asset.dest) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(asset.dest), 0o755); err != nil {
			logf("Cloud Workspace: create %s parent failed (non-fatal): %v", asset.name, err)
			continue
		}
		ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
		cmd := exec.CommandContext(ctx, git, "clone", "--depth=1", asset.url, asset.dest)
		cmd.Env = os.Environ()
		out, cloneErr := cmd.CombinedOutput()
		cancel()
		if cloneErr != nil {
			logf("Cloud Workspace: %s install failed (non-fatal): %v: %s", asset.name, cloneErr, strings.TrimSpace(lastLine(string(out))))
			continue
		}
		logf("Cloud Workspace: installed %s public defaults", asset.name)
	}

	// A tmux server may have started before TPM finished cloning. Reload the
	// public config, then install the declared public plugins without prompting.
	conf := filepath.Join(home, ".tmux.conf")
	if tmuxAvailable() && cloudComfortFileExists(conf) {
		ctx, cancel := context.WithTimeout(parent, 15*time.Second)
		_ = exec.CommandContext(ctx, tmuxCmdName(), "source-file", conf).Run()
		cancel()
	}
	installPlugins := filepath.Join(home, ".tmux", "plugins", "tpm", "bin", "install_plugins")
	if cloudComfortFileExists(installPlugins) {
		ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
		cmd := exec.CommandContext(ctx, installPlugins)
		cmd.Env = append(os.Environ(), "TMUX_PLUGIN_MANAGER_PATH="+filepath.Join(home, ".tmux", "plugins"))
		if out, pluginErr := cmd.CombinedOutput(); pluginErr != nil {
			logf("Cloud Workspace: tmux plugin install failed (non-fatal): %v: %s", pluginErr, strings.TrimSpace(lastLine(string(out))))
		}
		cancel()
	}
}
