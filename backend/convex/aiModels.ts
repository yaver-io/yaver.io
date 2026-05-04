import { mutation, query } from "./_generated/server";

export const PREDEFINED_MODELS = [
  // Claude Code (Anthropic SDK). modelIds are the canonical full IDs
  // that the Claude CLI / Anthropic API accept directly — `--model
  // claude-opus-4-7` works on the CLI, the API likewise accepts these
  // full strings. Default = opus to match
  // web/components/dashboard/DevicesView.tsx::DEFAULT_MODEL_BY_RUNNER
  // and mobile/DeviceContext::DEFAULT_MODEL_BY_RUNNER.
  {
    modelId: "claude-opus-4-7",
    runnerId: "claude",
    name: "Opus 4.7",
    description: "Most powerful — complex reasoning and architecture",
    isDefault: true,
    sortOrder: 1,
  },
  {
    modelId: "claude-sonnet-4-6",
    runnerId: "claude",
    name: "Sonnet 4.6",
    description: "Fast and capable — best for most tasks",
    sortOrder: 2,
  },
  {
    modelId: "claude-haiku-4-5-20251001",
    runnerId: "claude",
    name: "Haiku 4.5",
    description: "Fastest — quick edits and simple tasks",
    sortOrder: 3,
  },
  // Codex CLI (OpenAI). ChatGPT-account auth (the common path) does
  // NOT support `o3-mini` — Codex CLI 400s with "The 'o3-mini' model
  // is not supported when using Codex with a ChatGPT account."
  // gpt-5.4 is the default the web's DEFAULT_MODEL_BY_RUNNER also
  // uses (web/components/dashboard/DevicesView.tsx) so the surfaces
  // stay in sync.
  {
    modelId: "gpt-5.4",
    runnerId: "codex",
    name: "GPT-5.4",
    description: "Default — coding + general purpose",
    isDefault: true,
    sortOrder: 1,
  },
  {
    modelId: "gpt-5-codex",
    runnerId: "codex",
    name: "GPT-5 Codex",
    description: "Coding-tuned alternative",
    sortOrder: 2,
  },
  {
    modelId: "gpt-5-mini",
    runnerId: "codex",
    name: "GPT-5 Mini",
    description: "Fastest — quick edits and simple tasks",
    sortOrder: 3,
  },
];

export const list = query({
  args: {},
  handler: async (ctx) => {
    const models = await ctx.db.query("aiModels").collect();
    models.sort((a, b) => a.sortOrder - b.sortOrder);
    return models;
  },
});

export const seed = mutation({
  args: {},
  handler: async (ctx) => {
    // Upsert every model in the predefined list.
    for (const model of PREDEFINED_MODELS) {
      const existing = await ctx.db
        .query("aiModels")
        .withIndex("by_modelId", (q) =>
          q.eq("modelId", model.modelId).eq("runnerId", model.runnerId)
        )
        .first();
      if (existing) {
        await ctx.db.patch(existing._id, model);
      } else {
        await ctx.db.insert("aiModels", model);
      }
    }
    // Drop any rows that the predefined list no longer contains —
    // otherwise renaming or replacing a model (e.g. codex's o3-mini →
    // gpt-5-codex) leaves the obsolete row in the table forever and
    // /agent/runners keeps offering the broken pick.
    const keep = new Set(
      PREDEFINED_MODELS.map((m) => `${m.runnerId}::${m.modelId}`)
    );
    const all = await ctx.db.query("aiModels").collect();
    for (const row of all) {
      if (!keep.has(`${row.runnerId}::${row.modelId}`)) {
        await ctx.db.delete(row._id);
      }
    }
  },
});
