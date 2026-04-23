export ZSH="$HOME/.oh-my-zsh"

ZSH_THEME="ys"
plugins=(git colored-man-pages command-not-found sudo)

if [[ -x /opt/homebrew/bin/brew ]]; then
  eval "$(/opt/homebrew/bin/brew shellenv)"
fi

export PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:$PATH"
export PATH="$HOME/.local/bin:$PATH"
export PATH="$HOME/Library/Python/3.9/bin:$PATH"
export PATH="$HOME/.maestro/bin:$PATH"

if [[ -d "$HOME/Library/Android/sdk" ]]; then
  export ANDROID_SDK_ROOT="$HOME/Library/Android/sdk"
  export ANDROID_HOME="$ANDROID_SDK_ROOT"
  export PATH="$ANDROID_SDK_ROOT/platform-tools:$ANDROID_SDK_ROOT/emulator:$ANDROID_SDK_ROOT/cmdline-tools/latest/bin:$PATH"
fi

if command -v brew >/dev/null 2>&1; then
  export PATH="$(brew --prefix)/share/android-commandlinetools/bin:$PATH"
fi

if [[ -x /usr/libexec/java_home ]] && /usr/libexec/java_home -v 17 >/dev/null 2>&1; then
  export JAVA_HOME=$(/usr/libexec/java_home -v 17)
  export PATH="$JAVA_HOME/bin:$PATH"
fi

[[ -s "$ZSH/oh-my-zsh.sh" ]] && source "$ZSH/oh-my-zsh.sh"

alias tmux="tmux -2"
alias ta='tmux attach -t'
alias ts='tmux new-session -s'
alias tl='tmux list-sessions'
alias gcl='git clone'
alias ..g='cd "$(git rev-parse --show-toplevel)"'
alias cl='claude'
alias codex54='codex -p gpt54'
alias cx54='codex -p gpt54'
alias oc='opencode'
alias oc-openai='opencode -m openai/gpt-5'
alias oc-auth-openai='opencode providers login'

tla() {
  local d
  for d in /Users/*/Workspace/talos(N) /home/*/Workspace/talos(N) /root/Workspace/talos(N); do
    if [ -d "$d" ]; then
      cd "$d" && return
    fi
  done
  echo "talos not found"
}

tlt() {
  if [ -n "$TMUX" ]; then
    tmux switch-client -l
  else
    tmux attach-session
  fi
}

tdc() {
  tmux detach-client
}

if [[ -o interactive ]]; then
  if [[ -f ~/.fzf.zsh ]]; then
    source ~/.fzf.zsh
  fi

  if [[ -f /opt/homebrew/opt/fzf/shell/completion.zsh ]]; then
    source /opt/homebrew/opt/fzf/shell/completion.zsh
  fi

  if [[ -f /opt/homebrew/opt/fzf/shell/key-bindings.zsh ]]; then
    source /opt/homebrew/opt/fzf/shell/key-bindings.zsh
  fi

  bindkey -e

  if typeset -f fzf-history-widget >/dev/null; then
    bindkey -M emacs '^R' fzf-history-widget
    bindkey -M viins '^R' fzf-history-widget
    bindkey -M vicmd '^R' fzf-history-widget
  fi
fi

PROMPT=$'%F{blue}#%f %F{cyan}%n%f at %F{green}%m%f in %F{yellow}%~%f [%*]\n%F{red}$ %f'
