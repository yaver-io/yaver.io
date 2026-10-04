import { mutation, query, internalMutation } from "./_generated/server";

export const PREDEFINED_MODELS = [
  // Claude Code (Anthropic SDK). modelIds are the canonical full IDs
  // that the Claude CLI / Anthropic API accept directly — `--model
  // claude-opus-4-8` works on the CLI, the API likewise accepts these
  // full strings. Default = opus to match
  // web/components/dashboard/DevicesView.tsx::DEFAULT_MODEL_BY_RUNNER
  // and mobile/DeviceContext::DEFAULT_MODEL_BY_RUNNER.
  {
    modelId: "claude-opus-4-8",
    runnerId: "claude-code",
    name: "Opus 4.8",
    description: "Most powerful — complex reasoning and architecture",
    isDefault: true,
    sortOrder: 1,
  },
  {
    modelId: "claude-sonnet-4-6",
    runnerId: "claude-code",
    name: "Sonnet 4.6",
    description: "Fast and capable — best for most tasks",
    sortOrder: 2,
  },
  {
    modelId: "claude-haiku-4-5-20251001",
    runnerId: "claude-code",
    name: "Haiku 4.5",
    description: "Fastest — quick edits and simple tasks",
    sortOrder: 3,
  },
  // Codex CLI (OpenAI). ChatGPT-account auth (the common path) does
  // NOT support `o3-mini` — Codex CLI 400s with "The 'o3-mini' model
  // is not supported when using Codex with a ChatGPT account."
  // Product default is owned by modelDefaults.ts and mirrored here so a
  // freshly seeded deployment is correct before platform config is read.
  {
    modelId: "gpt-5.6-sol",
    runnerId: "codex",
    name: "GPT-5.6 Sol",
    description: "Default — coding + general purpose",
    isDefault: true,
    sortOrder: 1,
    defaultReasoningEffort: "medium",
    supportedReasoningEfforts: ["low", "medium", "high", "xhigh", "max"],
  },
  {
    modelId: "gpt-5.6-terra",
    runnerId: "codex",
    name: "GPT-5.6 Terra",
    description: "Steady everyday work",
    sortOrder: 2,
    defaultReasoningEffort: "medium",
    supportedReasoningEfforts: ["low", "medium", "high", "xhigh", "max"],
  },
  {
    modelId: "gpt-5.6-luna",
    runnerId: "codex",
    name: "GPT-5.6 Luna",
    description: "Fast, high-volume work",
    sortOrder: 3,
    defaultReasoningEffort: "medium",
    supportedReasoningEfforts: ["low", "medium", "high", "xhigh", "max"],
  },
  {
    modelId: "gpt-5.5",
    runnerId: "codex",
    name: "GPT-5.5",
    description: "Prior generation",
    sortOrder: 4,
    defaultReasoningEffort: "medium",
    supportedReasoningEfforts: ["low", "medium", "high", "xhigh"],
  },
  {
    modelId: "gpt-5.4-mini",
    runnerId: "codex",
    name: "GPT-5.4 Mini",
    description: "Small, fast coding",
    sortOrder: 5,
    defaultReasoningEffort: "medium",
    supportedReasoningEfforts: ["low", "medium", "high", "xhigh"],
  },
  {
    modelId: "gpt-5.3-codex-spark",
    runnerId: "codex",
    name: "GPT-5.3 Codex Spark",
    description: "Ultra-fast coding",
    sortOrder: 6,
    defaultReasoningEffort: "high",
    supportedReasoningEfforts: ["low", "medium", "high", "xhigh"],
  },
  // OpenCode uses the user's endpoint-local provider configuration. The
  // product default mirrors the direct DeepSeek setup used by the owner's
  // trusted machines; Yaver never receives that provider credential.
  {
    modelId: "deepseek/deepseek-v4-flash",
    runnerId: "opencode",
    name: "DeepSeek V4 Flash",
    description: "Selectable when the endpoint's live DeepSeek provider probe accepts this alias",
    providerId: "deepseek",
    providerName: "DeepSeek",
    lifecycle: "active" as const,
    sortOrder: 1,
  },
  {
    modelId: "deepseek/deepseek-v4-pro",
    runnerId: "opencode",
    name: "DeepSeek V4 Pro",
    description: "Higher-capability DeepSeek V4 model",
    providerId: "deepseek",
    providerName: "DeepSeek",
    lifecycle: "active" as const,
    sortOrder: 2,
  },
  {
    modelId: "deepseek/deepseek-v4-flash-vision-exp",
    runnerId: "opencode",
    name: "DeepSeek V4 Flash Vision Exp",
    description: "Experimental multimodal DeepSeek V4 Flash model",
    providerId: "deepseek",
    providerName: "DeepSeek",
    lifecycle: "active" as const,
    sortOrder: 3,
  },
  {
    modelId: "deepseek/deepseek-chat",
    runnerId: "opencode",
    name: "DeepSeek Chat",
    description: "Verified direct DeepSeek coding model",
    providerId: "deepseek",
    providerName: "DeepSeek",
    lifecycle: "active" as const,
    isDefault: true,
    sortOrder: 4,
  },
  // These managed-provider rows remain useful cross-device metadata. They are
  // offered on a machine only when its live `opencode models` probe exposes
  // them; Convex no longer pretends a provider is usable merely because a row
  // exists here.
  {
    modelId: "bedrock/deepseek.r1-v1:0",
    runnerId: "opencode",
    name: "Bedrock DeepSeek R1",
    description: "Managed DeepSeek through Amazon Bedrock",
    providerId: "bedrock",
    providerName: "Amazon Bedrock",
    lifecycle: "active" as const,
    sortOrder: 20,
  },
  {
    modelId: "bedrock/deepseek.v3-1-v1:0",
    runnerId: "opencode",
    name: "Bedrock DeepSeek V3.1",
    description: "Managed DeepSeek through Amazon Bedrock",
    providerId: "bedrock",
    providerName: "Amazon Bedrock",
    lifecycle: "active" as const,
    sortOrder: 21,
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

// internalMutation: bootstrap-only bulk upsert+delete; never over the wire.
export const seed = internalMutation({
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
