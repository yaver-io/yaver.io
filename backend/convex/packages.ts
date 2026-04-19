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

import { query, mutation } from "./_generated/server";
import { v } from "convex/values";

/** List the whole catalogue, sorted by (kind, sortOrder, name). */
export const list = query({
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
export const get = query({
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
export const upsert = mutation({
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
export const seed = mutation({
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
        ],
        checkCommand: "jq --version",
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
