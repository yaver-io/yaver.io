import { mutation, query } from "./_generated/server";

export const PREDEFINED_RUNNERS = [
  {
    runnerId: "claude",
    name: "Claude Code",
    command: "claude",
    args: JSON.stringify(["-p", "{prompt}", "--output-format", "stream-json", "--verbose", "--include-partial-messages", "--model", "sonnet", "--tools", "Bash", "--dangerously-skip-permissions"]),
    outputMode: "stream-json" as const,
    resumeSupported: true,
    resumeArgs: JSON.stringify(["--resume", "{sessionId}"]),
    exitCommand: "/exit",
    description: "Anthropic Claude CLI with streaming",
    isDefault: true,
    sortOrder: 1,
  },
  {
    runnerId: "codex",
    name: "OpenAI Codex",
    command: "codex",
    args: JSON.stringify(["exec", "--full-auto", "--skip-git-repo-check", "{prompt}"]),
    outputMode: "raw" as const,
    resumeSupported: false,
    exitCommand: "exit",
    description: "OpenAI Codex CLI",
    sortOrder: 2,
  },
  {
    runnerId: "aider",
    name: "Aider",
    command: "aider",
    args: JSON.stringify(["--yes", "--message", "{prompt}"]),
    outputMode: "raw" as const,
    resumeSupported: false,
    exitCommand: "/quit",
    description: "AI pair programming in terminal",
    sortOrder: 3,
  },
  {
    runnerId: "ollama",
    name: "Ollama",
    command: "ollama",
    args: JSON.stringify(["run", "qwen2.5-coder", "{prompt}"]),
    outputMode: "raw" as const,
    resumeSupported: false,
    exitCommand: "/bye",
    description: "Run local LLMs — fully private, no API keys",
    sortOrder: 4,
  },
  {
    runnerId: "opencode",
    name: "OpenCode",
    command: "opencode",
    args: JSON.stringify(["{prompt}"]),
    outputMode: "raw" as const,
    resumeSupported: false,
    description: "Terminal AI coding tool with TUI",
    sortOrder: 5,
  },
  {
    runnerId: "goose",
    name: "Goose",
    command: "goose",
    args: JSON.stringify(["run", "--text", "{prompt}"]),
    outputMode: "raw" as const,
    resumeSupported: false,
    description: "Autonomous coding agent from Block",
    sortOrder: 6,
  },
  {
    runnerId: "amp",
    name: "Amp",
    command: "amp",
    args: JSON.stringify(["{prompt}"]),
    outputMode: "raw" as const,
    resumeSupported: false,
    description: "AI coding agent by Sourcegraph",
    sortOrder: 7,
  },
  {
    runnerId: "continue",
    name: "Continue",
    command: "continue",
    args: JSON.stringify(["{prompt}"]),
    outputMode: "raw" as const,
    resumeSupported: false,
    description: "Open-source AI code assistant",
    sortOrder: 8,
  },
  {
    runnerId: "custom",
    name: "Custom Command",
    command: "",
    args: JSON.stringify([]),
    outputMode: "raw" as const,
    resumeSupported: false,
    description: "Your own terminal AI command",
    sortOrder: 99,
  },
];

export const list = query({
  args: {},
  handler: async (ctx) => {
    const runners = await ctx.db.query("aiRunners").collect();
    runners.sort((a, b) => a.sortOrder - b.sortOrder);
    return runners;
  },
});

export const seed = mutation({
  args: {},
  handler: async (ctx) => {
    for (const runner of PREDEFINED_RUNNERS) {
      const existing = await ctx.db
        .query("aiRunners")
        .withIndex("by_runnerId", (q) => q.eq("runnerId", runner.runnerId))
        .first();
      if (existing) {
        await ctx.db.patch(existing._id, runner);
      } else {
        await ctx.db.insert("aiRunners", runner);
      }
    }
  },
});
