import { mutation, query } from "./_generated/server";

export const PREDEFINED_MODELS = [
  // Claude models
  {
    modelId: "sonnet",
    runnerId: "claude",
    name: "Sonnet",
    description: "Fast and capable — best for most tasks",
    isDefault: true,
    sortOrder: 1,
  },
  {
    modelId: "opus",
    runnerId: "claude",
    name: "Opus",
    description: "Most powerful — complex reasoning and architecture",
    sortOrder: 2,
  },
  {
    modelId: "haiku",
    runnerId: "claude",
    name: "Haiku",
    description: "Fastest — quick edits and simple tasks",
    sortOrder: 3,
  },
  // Codex models. ChatGPT-account auth (the common path) does NOT
  // support `o3-mini` — Codex CLI 400s with "The 'o3-mini' model is
  // not supported when using Codex with a ChatGPT account." The
  // gpt-5 family is what works on ChatGPT-account auth and stays
  // valid on API-key auth too. `gpt-5-codex` is the coding-tuned
  // default; the rest are alternatives the user can pick from the
  // model dropdown in the device-details modal.
  {
    modelId: "gpt-5-codex",
    runnerId: "codex",
    name: "GPT-5 Codex",
    description: "Coding-tuned default — works on ChatGPT-account auth",
    isDefault: true,
    sortOrder: 1,
  },
  {
    modelId: "gpt-5",
    runnerId: "codex",
    name: "GPT-5",
    description: "General-purpose — broader knowledge",
    sortOrder: 2,
  },
  {
    modelId: "gpt-5-mini",
    runnerId: "codex",
    name: "GPT-5 Mini",
    description: "Fastest — quick edits and simple tasks",
    sortOrder: 3,
  },
  // Aider models (aider auto-selects, but user can override)
  {
    modelId: "sonnet",
    runnerId: "aider",
    name: "Sonnet",
    description: "Default Aider model",
    isDefault: true,
    sortOrder: 1,
  },
  {
    modelId: "opus",
    runnerId: "aider",
    name: "Opus",
    sortOrder: 2,
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
  },
});
