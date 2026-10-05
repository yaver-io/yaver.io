"use strict";

// Respectful defaults for an explicitly requested Yaver CI/automation host.
// This module is intentionally credential-blind: it never reads, writes, or
// transfers provider auth. It only creates a missing file and never appends to
// or rewrites an existing user configuration.

const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { execFileSync, spawnSync } = require("node:child_process");

function commandExists(name) {
  const probe = process.platform === "win32"
    ? spawnSync("where.exe", [name], { stdio: "ignore", windowsHide: true })
    : spawnSync("/bin/sh", ["-c", `command -v ${name}`], { stdio: "ignore" });
  return !probe.error && probe.status === 0;
}

function writeNewFile(filename, body, mode = 0o600) {
  fs.mkdirSync(path.dirname(filename), { recursive: true, mode: 0o700 });
  try {
    fs.writeFileSync(filename, body, { encoding: "utf8", flag: "wx", mode });
    return true;
  } catch (error) {
    if (error && error.code === "EEXIST") return false;
    throw error;
  }
}

function zshrcBody() {
  return `# Seeded once by yaver-cli for an explicitly requested Yaver CI host.
export ZSH="$HOME/.oh-my-zsh"
ZSH_THEME="ys"
plugins=(git colored-man-pages command-not-found sudo)
[[ -r "$ZSH/oh-my-zsh.sh" ]] && source "$ZSH/oh-my-zsh.sh"

export PATH="$HOME/.local/bin:$HOME/.opencode/bin:$HOME/.yaver/bin/current/$(uname -s | tr '[:upper:]' '[:lower:]')-$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/'):/usr/local/go/bin:$PATH"
export EDITOR="vim"
export VISUAL="$EDITOR"
HISTFILE="$HOME/.zsh_history"
HISTSIZE=100000
SAVEHIST=100000
setopt append_history share_history hist_ignore_dups hist_reduce_blanks

alias tmux="tmux -2"
alias ta="tmux attach -t"
alias ts="tmux new-session -s"
alias tl="tmux list-sessions"
alias oc="opencode"
alias oc-flash="opencode -m deepseek/deepseek-flash"
alias ..g='cd "$(git rev-parse --show-toplevel)"'

if [[ -o interactive ]] && command -v fzf >/dev/null 2>&1; then
  if fzf --help 2>&1 | grep -q -- '--zsh'; then
    source <(fzf --zsh)
  else
    [[ -r /usr/share/doc/fzf/examples/key-bindings.zsh ]] && source /usr/share/doc/fzf/examples/key-bindings.zsh
    [[ -r /usr/share/doc/fzf/examples/completion.zsh ]] && source /usr/share/doc/fzf/examples/completion.zsh
  fi
fi

bindkey -e
`;
}

function tmuxBody() {
  return `# Seeded once by yaver-cli for an explicitly requested Yaver CI host.
set-option -gw xterm-keys on
bind h split-window -h -c "#{pane_current_path}"
bind v split-window -v -c "#{pane_current_path}"
bind c new-window -c "#{pane_current_path}"
unbind '"'
unbind %
bind-key Up select-pane -U
bind-key Down select-pane -D
bind-key Left select-pane -L
bind-key Right select-pane -R
set -g history-limit 1000000
set -g mouse on
bind -n S-PageUp copy-mode -u
bind -n S-PageDown send-keys -X -N 1 page-down
set -g status-bg "#262626"
set -g status-fg white
set -g status-right ""
setw -g mode-keys vi
set -g @plugin 'tmux-plugins/tpm'
set -g @plugin 'tmux-plugins/tmux-sensible'
set -g @plugin 'tmux-plugins/tmux-yank'
if-shell -b '[ -x "$HOME/.tmux/plugins/tpm/tpm" ]' 'run-shell "$HOME/.tmux/plugins/tpm/tpm"'
`;
}

function openCodeBody() {
  return `${JSON.stringify({
    $schema: "https://json-schema.org/draft/2020-12/schema",
    default_agent: "build",
    enabled_providers: ["deepseek"],
    mcp: {
      yaver: {
        command: ["yaver", "mcp"],
        enabled: true,
        type: "local",
      },
    },
    model: "deepseek/deepseek-flash",
    small_model: "deepseek/deepseek-chat",
    permission: "ask",
    provider: {
      deepseek: {
        name: "DeepSeek",
        options: { baseURL: "https://api.deepseek.com/v1" },
        models: {
          "deepseek-chat": { name: "DeepSeek Chat" },
          "deepseek-flash": { name: "DeepSeek V4.1 Flash" },
        },
      },
    },
  }, null, 2)}\n`;
}

function cloneIfMissing(destination, repository, exists = commandExists) {
  if (fs.existsSync(destination) || !exists("git")) return false;
  fs.mkdirSync(path.dirname(destination), { recursive: true, mode: 0o700 });
  execFileSync("git", ["clone", "--quiet", "--depth", "1", repository, destination], {
    stdio: "ignore",
    windowsHide: true,
  });
  return true;
}

function seedCIHostDefaults(options = {}) {
  const home = options.home || os.homedir();
  const platform = options.platform || process.platform;
  const exists = options.commandExists || commandExists;
  const runClone = options.cloneIfMissing || cloneIfMissing;
  const result = { created: [], preserved: [], notes: [] };
  if (platform !== "linux") return result;

  const zshrc = path.join(home, ".zshrc");
  if (fs.existsSync(zshrc)) {
    result.preserved.push(zshrc);
  } else if (exists("zsh")) {
    try {
      runClone(path.join(home, ".oh-my-zsh"), "https://github.com/ohmyzsh/ohmyzsh.git", exists);
    } catch (error) {
      result.notes.push(`Oh My Zsh clone skipped: ${error.message}`);
    }
    if (writeNewFile(zshrc, zshrcBody())) result.created.push(zshrc);
  }

  const tmuxCandidates = [path.join(home, ".tmux.conf"), path.join(home, ".config", "tmux", "tmux.conf")];
  const existingTmux = tmuxCandidates.find((candidate) => fs.existsSync(candidate));
  if (existingTmux) {
    result.preserved.push(existingTmux);
  } else if (exists("tmux")) {
    try {
      runClone(path.join(home, ".tmux", "plugins", "tpm"), "https://github.com/tmux-plugins/tpm.git", exists);
    } catch (error) {
      result.notes.push(`tmux plugin manager clone skipped: ${error.message}`);
    }
    if (writeNewFile(tmuxCandidates[0], tmuxBody())) result.created.push(tmuxCandidates[0]);
  }

  const openCodeConfig = path.join(home, ".config", "opencode", "opencode.json");
  if (fs.existsSync(openCodeConfig)) {
    result.preserved.push(openCodeConfig);
  } else if (exists("opencode")) {
    if (writeNewFile(openCodeConfig, openCodeBody())) result.created.push(openCodeConfig);
    result.notes.push("OpenCode defaults were seeded without credentials; authenticate DeepSeek locally on this endpoint.");
  }

  // A raw host benefits from a conventional default branch, but identity and
  // signing configuration are personal and must never be guessed or copied.
  if (exists("git")) {
    try {
      const current = spawnSync("git", ["config", "--global", "--get", "init.defaultBranch"], {
        encoding: "utf8",
        windowsHide: true,
      });
      if (current.status !== 0 || !String(current.stdout || "").trim()) {
        execFileSync("git", ["config", "--global", "init.defaultBranch", "main"], { stdio: "ignore", windowsHide: true });
        result.notes.push("Git default branch set to main; identity/signing were left untouched.");
      }
    } catch (error) {
      result.notes.push(`Git default branch seed skipped: ${error.message}`);
    }
  }

  return result;
}

module.exports = {
  openCodeBody,
  seedCIHostDefaults,
  tmuxBody,
  writeNewFile,
  zshrcBody,
};
