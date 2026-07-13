// packages.ts — public install-catalogue lookup.
//
// The Go agent calls GET /packages on startup (+ periodically) to
// fetch a curated list of tools/packages with install commands for
// every supported package manager. That lets us add new tools
// without shipping a CLI release, and lets the mobile / web tools
// tab show a richer catalogue than the hardcoded agent list.
//
// This is a PUBLIC endpoint. Nothing in here should be user-specific
// or sensitive. See `backend/convex/schema.ts` for the contract.

import { internalQuery, internalMutation } from "./_generated/server";
import { v } from "convex/values";

/** List the whole catalogue, sorted by (kind, sortOrder, name). */
export const list = internalQuery({
  args: {
    kind: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const q = args.kind
      ? ctx.db.query("packageRegistry").withIndex("by_kind", (ix) => ix.eq("kind", args.kind!))
      : ctx.db.query("packageRegistry");
    const rows = await q.collect();
    rows.sort((a, b) => {
      if (a.kind !== b.kind) return a.kind.localeCompare(b.kind);
      if (a.sortOrder !== b.sortOrder) return a.sortOrder - b.sortOrder;
      return a.name.localeCompare(b.name);
    });
    return rows.map((r) => ({
      name: r.name,
      kind: r.kind,
      description: r.description,
      tags: r.tags ?? [],
      installs: r.installs,
      checkCommand: r.checkCommand ?? "",
      docUrl: r.docUrl ?? "",
      updatedAt: r.updatedAt,
    }));
  },
});

/** Look up a single package by slug. */
export const get = internalQuery({
  args: { name: v.string() },
  handler: async (ctx, args) => {
    const row = await ctx.db
      .query("packageRegistry")
      .withIndex("by_name", (ix) => ix.eq("name", args.name))
      .unique();
    return row
      ? {
          name: row.name,
          kind: row.kind,
          description: row.description,
          tags: row.tags ?? [],
          installs: row.installs,
          checkCommand: row.checkCommand ?? "",
          docUrl: row.docUrl ?? "",
          updatedAt: row.updatedAt,
        }
      : null;
  },
});

// Admin-only (dev) — upsert a single entry. Invoked by `npx convex run
// packages:upsert '{...}'` during seeding or when adding a new tool.
export const upsert = internalMutation({
  args: {
    name: v.string(),
    kind: v.string(),
    description: v.string(),
    tags: v.optional(v.array(v.string())),
    installs: v.array(
      v.object({
        platform: v.optional(v.string()),
        packageManager: v.string(),
        command: v.string(),
      }),
    ),
    checkCommand: v.optional(v.string()),
    docUrl: v.optional(v.string()),
    sortOrder: v.optional(v.number()),
  },
  handler: async (ctx, args) => {
    const existing = await ctx.db
      .query("packageRegistry")
      .withIndex("by_name", (ix) => ix.eq("name", args.name))
      .unique();
    const now = Date.now();
    const fields = {
      name: args.name,
      kind: args.kind,
      description: args.description,
      tags: args.tags ?? [],
      installs: args.installs,
      checkCommand: args.checkCommand ?? "",
      docUrl: args.docUrl ?? "",
      sortOrder: args.sortOrder ?? 100,
      updatedAt: now,
    };
    if (existing) {
      await ctx.db.patch(existing._id, fields);
      return existing._id;
    }
    return await ctx.db.insert("packageRegistry", fields);
  },
});

// Initial seed — runnable via `npx convex run packages:seed`.
// Keep this small; most growth should happen via `upsert`.
export const seed = internalMutation({
  args: {},
  handler: async (ctx) => {
    const now = Date.now();
    const entries: Array<{
      name: string;
      kind: string;
      description: string;
      tags?: string[];
      installs: Array<{ platform?: string; packageManager: string; command: string }>;
      checkCommand?: string;
      docUrl?: string;
      sortOrder: number;
    }> = [
      {
        name: "ollama",
        kind: "model-runtime",
        description: "Local LLM runtime — pull models, serve them over HTTP at :11434.",
        tags: ["ai", "local", "llm"],
        docUrl: "https://ollama.com/download",
        sortOrder: 10,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install ollama" },
          { platform: "linux", packageManager: "curl", command: "curl -fsSL https://ollama.com/install.sh | sh" },
          { platform: "windows", packageManager: "winget", command: "winget install ollama.ollama" },
        ],
        checkCommand: "ollama --version",
      },
      {
        name: "aider",
        kind: "ai-runner",
        description: "Terminal pair-programmer that pairs well with Ollama for the hybrid planner.",
        tags: ["ai", "runner"],
        docUrl: "https://aider.chat/docs/install.html",
        sortOrder: 20,
        installs: [
          { packageManager: "pipx", command: "pipx install aider-chat" },
          { packageManager: "pip", command: "pip install --user aider-chat" },
          { packageManager: "uv", command: "uv tool install aider-chat" },
        ],
        checkCommand: "aider --version",
      },
      {
        name: "claude-code",
        kind: "ai-runner",
        description: "Anthropic's CLI coding agent. Highest quality, highest cost.",
        tags: ["ai", "runner", "frontier"],
        docUrl: "https://docs.claude.com/en/docs/claude-code/setup",
        sortOrder: 5,
        installs: [
          { packageManager: "npm", command: "npm install -g @anthropic-ai/claude-code" },
        ],
        checkCommand: "claude --version",
      },
      {
        name: "codex",
        kind: "ai-runner",
        description: "OpenAI Codex CLI — token-efficient daily driver.",
        tags: ["ai", "runner"],
        docUrl: "https://github.com/openai/codex",
        sortOrder: 6,
        installs: [
          { packageManager: "npm", command: "npm install -g @openai/codex" },
          { platform: "darwin", packageManager: "brew", command: "brew install codex" },
        ],
        checkCommand: "codex --version",
      },
      {
        name: "opencode",
        kind: "ai-runner",
        description: "Open-source Claude-style coding agent.",
        tags: ["ai", "runner", "open-source"],
        docUrl: "https://opencode.ai",
        sortOrder: 30,
        installs: [
          { packageManager: "npm", command: "npm install -g opencode-ai" },
        ],
        checkCommand: "opencode --version",
      },
      {
        name: "qwen2.5-coder",
        kind: "model",
        description: "Qwen2.5 Coder (14B) — strong open-weights coder. Implementer tier in hybrid mode.",
        tags: ["ai", "model", "ollama"],
        docUrl: "https://ollama.com/library/qwen2.5-coder",
        sortOrder: 40,
        installs: [
          { packageManager: "ollama", command: "ollama pull qwen2.5-coder:14b" },
        ],
        checkCommand: "ollama list | grep -q qwen2.5-coder",
      },
      {
        name: "docker",
        kind: "devtool",
        description: "Containerise tasks — required for guest isolation + sandbox mode.",
        tags: ["runtime", "container"],
        sortOrder: 50,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install --cask docker" },
          { platform: "linux", packageManager: "apt-get", command: "sudo apt-get update && sudo apt-get install -y docker.io docker-compose-v2" },
          { platform: "linux", packageManager: "dnf", command: "sudo dnf install -y docker docker-compose-plugin" },
          { platform: "linux", packageManager: "pacman", command: "sudo pacman -S --noconfirm docker docker-compose" },
        ],
        checkCommand: "docker --version",
      },
      {
        name: "node",
        kind: "language",
        description: "Node.js runtime — required for Expo, Vite, Next.js.",
        tags: ["runtime", "javascript"],
        sortOrder: 60,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install node" },
          { platform: "linux", packageManager: "apt-get", command: "curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash - && sudo apt-get install -y nodejs" },
          { platform: "linux", packageManager: "dnf", command: "sudo dnf install -y nodejs" },
          { platform: "windows", packageManager: "winget", command: "winget install OpenJS.NodeJS.LTS" },
        ],
        checkCommand: "node --version",
      },
      {
        name: "python",
        kind: "language",
        description: "Python 3 — ML tooling, some CLIs.",
        tags: ["runtime", "python"],
        sortOrder: 70,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install python@3.12" },
          { platform: "linux", packageManager: "apt-get", command: "sudo apt-get update && sudo apt-get install -y python3 python3-pip python3-venv" },
          { platform: "linux", packageManager: "dnf", command: "sudo dnf install -y python3 python3-pip" },
        ],
        checkCommand: "python3 --version",
      },
      {
        name: "go",
        kind: "language",
        description: "Go toolchain — rebuild the agent or relay from source.",
        tags: ["runtime", "golang"],
        sortOrder: 80,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install go" },
          { platform: "linux", packageManager: "apt-get", command: "sudo apt-get install -y golang-go" },
          { platform: "linux", packageManager: "dnf", command: "sudo dnf install -y golang" },
        ],
        checkCommand: "go version",
      },
      {
        name: "rust",
        kind: "language",
        description: "Rust toolchain — some runners + Hermes compiler.",
        tags: ["runtime", "rust"],
        sortOrder: 90,
        installs: [
          { packageManager: "curl", command: "curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y" },
        ],
        checkCommand: "rustc --version",
      },
      {
        name: "git",
        kind: "devtool",
        description: "Version control — every scaffold depends on it.",
        tags: ["devtool"],
        sortOrder: 100,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install git" },
          { platform: "linux", packageManager: "apt-get", command: "sudo apt-get install -y git" },
          { platform: "linux", packageManager: "dnf", command: "sudo dnf install -y git" },
          { platform: "linux", packageManager: "pacman", command: "sudo pacman -S --noconfirm git" },
        ],
        checkCommand: "git --version",
      },
      {
        name: "gh",
        kind: "devtool",
        description: "GitHub CLI — auth, PRs, releases, gists.",
        tags: ["github", "devtool"],
        sortOrder: 110,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install gh" },
          { platform: "linux", packageManager: "apt-get", command: "sudo apt-get install -y gh" },
          { platform: "linux", packageManager: "dnf", command: "sudo dnf install -y gh" },
          { platform: "linux", packageManager: "pacman", command: "sudo pacman -S --noconfirm github-cli" },
        ],
        checkCommand: "gh --version",
      },
      {
        name: "ripgrep",
        kind: "devtool",
        description: "Fast recursive grep with sensible defaults. Used by every AI coding agent.",
        tags: ["devtool", "search"],
        sortOrder: 120,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install ripgrep" },
          { platform: "linux", packageManager: "apt-get", command: "sudo apt-get install -y ripgrep" },
          { platform: "linux", packageManager: "dnf", command: "sudo dnf install -y ripgrep" },
          { platform: "linux", packageManager: "pacman", command: "sudo pacman -S --noconfirm ripgrep" },
          { packageManager: "cargo", command: "cargo install ripgrep" },
        ],
        checkCommand: "rg --version",
      },
      {
        name: "jq",
        kind: "devtool",
        description: "JSON processor for shell pipelines.",
        tags: ["devtool", "json"],
        sortOrder: 130,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install jq" },
          { platform: "linux", packageManager: "apt-get", command: "sudo apt-get install -y jq" },
          { platform: "linux", packageManager: "dnf", command: "sudo dnf install -y jq" },
          { platform: "linux", packageManager: "pacman", command: "sudo pacman -S --noconfirm jq" },
        ],
        checkCommand: "jq --version",
      },
      {
        name: "yq",
        kind: "devtool",
        description: "YAML/JSON processor — `jq` for YAML.",
        tags: ["devtool", "yaml"],
        sortOrder: 135,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install yq" },
          { platform: "linux", packageManager: "snap", command: "sudo snap install yq" },
          { platform: "linux", packageManager: "apt-get", command: "sudo apt-get install -y yq" },
          { packageManager: "go", command: "go install github.com/mikefarah/yq/v4@latest" },
        ],
        checkCommand: "yq --version",
      },
      {
        name: "fd",
        kind: "devtool",
        description: "Modern replacement for `find` — fast, sensible defaults.",
        tags: ["devtool", "search"],
        sortOrder: 140,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install fd" },
          { platform: "linux", packageManager: "apt-get", command: "sudo apt-get install -y fd-find" },
          { platform: "linux", packageManager: "dnf", command: "sudo dnf install -y fd-find" },
          { platform: "linux", packageManager: "pacman", command: "sudo pacman -S --noconfirm fd" },
          { packageManager: "cargo", command: "cargo install fd-find" },
        ],
        checkCommand: "fd --version",
      },
      {
        name: "bat",
        kind: "devtool",
        description: "Syntax-highlighted `cat` with git integration.",
        tags: ["devtool", "pager"],
        sortOrder: 145,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install bat" },
          { platform: "linux", packageManager: "apt-get", command: "sudo apt-get install -y bat" },
          { platform: "linux", packageManager: "dnf", command: "sudo dnf install -y bat" },
          { platform: "linux", packageManager: "pacman", command: "sudo pacman -S --noconfirm bat" },
          { packageManager: "cargo", command: "cargo install --locked bat" },
        ],
        checkCommand: "bat --version",
      },
      {
        name: "eza",
        kind: "devtool",
        description: "Modern `ls` with colours, icons, git status.",
        tags: ["devtool"],
        sortOrder: 150,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install eza" },
          { platform: "linux", packageManager: "apt-get", command: "sudo apt-get install -y eza" },
          { platform: "linux", packageManager: "pacman", command: "sudo pacman -S --noconfirm eza" },
          { packageManager: "cargo", command: "cargo install eza" },
        ],
        checkCommand: "eza --version",
      },
      {
        name: "fzf",
        kind: "devtool",
        description: "Fuzzy-finder for shell history, files, git branches, everything.",
        tags: ["devtool", "fuzzy"],
        sortOrder: 155,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install fzf" },
          { platform: "linux", packageManager: "apt-get", command: "sudo apt-get install -y fzf" },
          { platform: "linux", packageManager: "dnf", command: "sudo dnf install -y fzf" },
          { platform: "linux", packageManager: "pacman", command: "sudo pacman -S --noconfirm fzf" },
        ],
        checkCommand: "fzf --version",
      },
      {
        name: "zoxide",
        kind: "devtool",
        description: "Smarter `cd` — jumps to directories by frecency.",
        tags: ["devtool", "shell"],
        sortOrder: 160,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install zoxide" },
          { platform: "linux", packageManager: "apt-get", command: "sudo apt-get install -y zoxide" },
          { platform: "linux", packageManager: "pacman", command: "sudo pacman -S --noconfirm zoxide" },
          { packageManager: "cargo", command: "cargo install zoxide --locked" },
        ],
        checkCommand: "zoxide --version",
      },
      {
        name: "delta",
        kind: "devtool",
        description: "Syntax-aware diff viewer for git. Pairs with `git config`.",
        tags: ["devtool", "git"],
        sortOrder: 165,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install git-delta" },
          { platform: "linux", packageManager: "apt-get", command: "sudo apt-get install -y git-delta" },
          { platform: "linux", packageManager: "pacman", command: "sudo pacman -S --noconfirm git-delta" },
          { packageManager: "cargo", command: "cargo install git-delta" },
        ],
        checkCommand: "delta --version",
      },
      {
        name: "lazygit",
        kind: "devtool",
        description: "TUI for git — stage, diff, rebase, log without leaving the terminal.",
        tags: ["devtool", "git", "tui"],
        sortOrder: 170,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install lazygit" },
          { platform: "linux", packageManager: "pacman", command: "sudo pacman -S --noconfirm lazygit" },
          { platform: "linux", packageManager: "dnf", command: "sudo dnf install -y lazygit" },
          { packageManager: "go", command: "go install github.com/jesseduffield/lazygit@latest" },
        ],
        checkCommand: "lazygit --version",
      },
      {
        name: "glab",
        kind: "devtool",
        description: "GitLab CLI — same UX as `gh` but for GitLab.",
        tags: ["devtool", "gitlab"],
        sortOrder: 175,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install glab" },
          { platform: "linux", packageManager: "apt-get", command: "sudo apt-get install -y glab" },
        ],
        checkCommand: "glab --version",
      },
      {
        name: "tmux",
        kind: "devtool",
        description: "Terminal multiplexer — persistent sessions, split panes.",
        tags: ["devtool", "terminal"],
        sortOrder: 180,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install tmux" },
          { platform: "linux", packageManager: "apt-get", command: "sudo apt-get install -y tmux" },
          { platform: "linux", packageManager: "dnf", command: "sudo dnf install -y tmux" },
          { platform: "linux", packageManager: "pacman", command: "sudo pacman -S --noconfirm tmux" },
        ],
        checkCommand: "tmux -V",
      },
      {
        name: "neovim",
        kind: "devtool",
        description: "Modernised vim. Plugins via Lua.",
        tags: ["devtool", "editor"],
        sortOrder: 185,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install neovim" },
          { platform: "linux", packageManager: "apt-get", command: "sudo apt-get install -y neovim" },
          { platform: "linux", packageManager: "dnf", command: "sudo dnf install -y neovim" },
          { platform: "linux", packageManager: "pacman", command: "sudo pacman -S --noconfirm neovim" },
          { platform: "linux", packageManager: "snap", command: "sudo snap install nvim --classic" },
        ],
        checkCommand: "nvim --version",
      },
      {
        name: "htop",
        kind: "devtool",
        description: "Interactive process viewer.",
        tags: ["devtool", "system"],
        sortOrder: 190,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install htop" },
          { platform: "linux", packageManager: "apt-get", command: "sudo apt-get install -y htop" },
          { platform: "linux", packageManager: "dnf", command: "sudo dnf install -y htop" },
          { platform: "linux", packageManager: "pacman", command: "sudo pacman -S --noconfirm htop" },
        ],
        checkCommand: "htop --version",
      },
      {
        name: "btop",
        kind: "devtool",
        description: "Beautiful resource monitor — CPU, RAM, disk, net in one TUI.",
        tags: ["devtool", "system"],
        sortOrder: 195,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install btop" },
          { platform: "linux", packageManager: "apt-get", command: "sudo apt-get install -y btop" },
          { platform: "linux", packageManager: "pacman", command: "sudo pacman -S --noconfirm btop" },
          { platform: "linux", packageManager: "snap", command: "sudo snap install btop" },
        ],
        checkCommand: "btop --version",
      },
      {
        name: "httpie",
        kind: "devtool",
        description: "Friendly HTTP client for the terminal.",
        tags: ["devtool", "http"],
        sortOrder: 200,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install httpie" },
          { platform: "linux", packageManager: "apt-get", command: "sudo apt-get install -y httpie" },
          { packageManager: "pipx", command: "pipx install httpie" },
        ],
        checkCommand: "http --version",
      },
      {
        name: "just",
        kind: "devtool",
        description: "Command runner — nicer `make` for project recipes.",
        tags: ["devtool", "build"],
        sortOrder: 210,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install just" },
          { platform: "linux", packageManager: "apt-get", command: "sudo apt-get install -y just" },
          { platform: "linux", packageManager: "pacman", command: "sudo pacman -S --noconfirm just" },
          { packageManager: "cargo", command: "cargo install just" },
        ],
        checkCommand: "just --version",
      },
      {
        name: "pnpm",
        kind: "language",
        description: "Fast, disk-efficient package manager for Node.js projects.",
        tags: ["runtime", "javascript"],
        sortOrder: 220,
        installs: [
          { packageManager: "npm", command: "npm install -g pnpm" },
          { platform: "darwin", packageManager: "brew", command: "brew install pnpm" },
          { packageManager: "curl", command: "curl -fsSL https://get.pnpm.io/install.sh | sh -" },
        ],
        checkCommand: "pnpm --version",
      },
      {
        name: "bun",
        kind: "language",
        description: "All-in-one JS/TS runtime + package manager. Drops into most Node projects.",
        tags: ["runtime", "javascript"],
        sortOrder: 225,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install oven-sh/bun/bun" },
          { packageManager: "curl", command: "curl -fsSL https://bun.sh/install | bash" },
        ],
        checkCommand: "bun --version",
      },
      {
        name: "deno",
        kind: "language",
        description: "Secure TypeScript runtime with standard library.",
        tags: ["runtime", "javascript"],
        sortOrder: 230,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install deno" },
          { packageManager: "curl", command: "curl -fsSL https://deno.land/install.sh | sh" },
        ],
        checkCommand: "deno --version",
      },
      {
        name: "uv",
        kind: "language",
        description: "Ultrafast Python package + project manager. Drop-in for pip + venv + pyenv.",
        tags: ["runtime", "python"],
        sortOrder: 235,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install uv" },
          { packageManager: "pipx", command: "pipx install uv" },
          { packageManager: "curl", command: "curl -LsSf https://astral.sh/uv/install.sh | sh" },
        ],
        checkCommand: "uv --version",
      },
      {
        name: "pipx",
        kind: "language",
        description: "Install Python apps in isolated venvs.",
        tags: ["runtime", "python"],
        sortOrder: 240,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install pipx" },
          { platform: "linux", packageManager: "apt-get", command: "sudo apt-get install -y pipx" },
          { packageManager: "pip", command: "pip install --user pipx && python3 -m pipx ensurepath" },
        ],
        checkCommand: "pipx --version",
      },
      {
        name: "kubectl",
        kind: "devtool",
        description: "Kubernetes CLI.",
        tags: ["devtool", "kubernetes"],
        sortOrder: 250,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install kubectl" },
          { platform: "linux", packageManager: "snap", command: "sudo snap install kubectl --classic" },
          { platform: "linux", packageManager: "apt-get", command: "sudo apt-get install -y kubectl" },
        ],
        checkCommand: "kubectl version --client",
      },
      {
        name: "helm",
        kind: "devtool",
        description: "The package manager for Kubernetes.",
        tags: ["devtool", "kubernetes"],
        sortOrder: 255,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install helm" },
          { platform: "linux", packageManager: "snap", command: "sudo snap install helm --classic" },
          { packageManager: "curl", command: "curl -fsSL -o get_helm.sh https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 && chmod 700 get_helm.sh && ./get_helm.sh" },
        ],
        checkCommand: "helm version",
      },
      {
        name: "k9s",
        kind: "devtool",
        description: "TUI for managing Kubernetes clusters.",
        tags: ["devtool", "kubernetes", "tui"],
        sortOrder: 260,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install k9s" },
          { platform: "linux", packageManager: "snap", command: "sudo snap install k9s" },
          { platform: "linux", packageManager: "pacman", command: "sudo pacman -S --noconfirm k9s" },
        ],
        checkCommand: "k9s version",
      },
      {
        name: "terraform",
        kind: "devtool",
        description: "Infrastructure-as-code.",
        tags: ["devtool", "infra"],
        sortOrder: 270,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew tap hashicorp/tap && brew install hashicorp/tap/terraform" },
          { platform: "linux", packageManager: "apt-get", command: "sudo apt-get install -y terraform" },
          { platform: "linux", packageManager: "snap", command: "sudo snap install terraform --classic" },
        ],
        checkCommand: "terraform version",
      },
      {
        name: "fly",
        kind: "devtool",
        description: "Fly.io CLI — deploy apps to the Fly edge network.",
        tags: ["devtool", "cloud"],
        sortOrder: 280,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install flyctl" },
          { packageManager: "curl", command: "curl -L https://fly.io/install.sh | sh" },
        ],
        checkCommand: "fly version",
      },
      {
        name: "wrangler",
        kind: "devtool",
        description: "Cloudflare Workers CLI — deploy, tail logs, manage KV/D1/R2.",
        tags: ["devtool", "cloud", "cloudflare"],
        sortOrder: 285,
        installs: [
          { packageManager: "npm", command: "npm install -g wrangler" },
          { platform: "darwin", packageManager: "brew", command: "brew install cloudflare-wrangler" },
        ],
        checkCommand: "wrangler --version",
      },
      {
        name: "vercel",
        kind: "devtool",
        description: "Vercel CLI — deploy Next.js and static sites.",
        tags: ["devtool", "cloud", "vercel"],
        sortOrder: 290,
        installs: [
          { packageManager: "npm", command: "npm install -g vercel" },
          { platform: "darwin", packageManager: "brew", command: "brew install vercel-cli" },
        ],
        checkCommand: "vercel --version",
      },
      {
        name: "aws",
        kind: "devtool",
        description: "AWS CLI.",
        tags: ["devtool", "cloud", "aws"],
        sortOrder: 295,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install awscli" },
          { platform: "linux", packageManager: "snap", command: "sudo snap install aws-cli --classic" },
          { platform: "linux", packageManager: "apt-get", command: "sudo apt-get install -y awscli" },
        ],
        checkCommand: "aws --version",
      },
      {
        name: "gcloud",
        kind: "devtool",
        description: "Google Cloud CLI.",
        tags: ["devtool", "cloud", "gcp"],
        sortOrder: 300,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install --cask google-cloud-sdk" },
          { platform: "linux", packageManager: "snap", command: "sudo snap install google-cloud-cli --classic" },
        ],
        checkCommand: "gcloud version",
      },
      {
        name: "sqlite3",
        kind: "devtool",
        description: "Portable SQL database in a single file.",
        tags: ["devtool", "database"],
        sortOrder: 310,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install sqlite" },
          { platform: "linux", packageManager: "apt-get", command: "sudo apt-get install -y sqlite3 libsqlite3-dev" },
          { platform: "linux", packageManager: "dnf", command: "sudo dnf install -y sqlite sqlite-devel" },
        ],
        checkCommand: "sqlite3 --version",
      },
      {
        name: "cloudflared",
        kind: "devtool",
        description: "Cloudflare tunnel — expose localhost to the internet with auth.",
        tags: ["devtool", "networking"],
        sortOrder: 320,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install cloudflared" },
          { platform: "linux", packageManager: "apt-get", command: "curl -fsSL https://pkg.cloudflare.com/install.sh | sudo bash && sudo apt-get install -y cloudflared" },
        ],
        checkCommand: "cloudflared --version",
      },
      {
        name: "tailscale",
        kind: "devtool",
        description: "Zero-config VPN — every device on one flat network.",
        tags: ["devtool", "networking"],
        sortOrder: 325,
        installs: [
          { platform: "darwin", packageManager: "brew", command: "brew install --cask tailscale" },
          { platform: "linux", packageManager: "apt-get", command: "curl -fsSL https://tailscale.com/install.sh | sh" },
        ],
        checkCommand: "tailscale version",
      },
    ];

    let inserted = 0, updated = 0;
    for (const entry of entries) {
      const existing = await ctx.db
        .query("packageRegistry")
        .withIndex("by_name", (ix) => ix.eq("name", entry.name))
        .unique();
      const fields = {
        name: entry.name,
        kind: entry.kind,
        description: entry.description,
        tags: entry.tags ?? [],
        installs: entry.installs,
        checkCommand: entry.checkCommand ?? "",
        docUrl: entry.docUrl ?? "",
        sortOrder: entry.sortOrder,
        updatedAt: now,
      };
      if (existing) {
        await ctx.db.patch(existing._id, fields);
        updated++;
      } else {
        await ctx.db.insert("packageRegistry", fields);
        inserted++;
      }
    }
    return { inserted, updated, total: entries.length };
  },
});
