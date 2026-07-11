import { internalMutation, internalQuery } from "./_generated/server";
import { v } from "convex/values";

// SECURITY: these were public mutation/query — anyone with the deploy URL could
// read, flood, or wipe auth logs (2026-07-07 audit HIGH). They are now internal*
// (callable only from other Convex functions, e.g. the auth httpAction), which
// closes the hole. writeLog's sole caller is http.ts (server-side runMutation);
// recentLogs/clearAll had no external callers.

export const writeLog = internalMutation({
  args: {
    level: v.union(v.literal("info"), v.literal("error"), v.literal("warn")),
    provider: v.string(),
    step: v.string(),
    message: v.string(),
    details: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    await ctx.db.insert("authLogs", {
      ...args,
      createdAt: Date.now(),
    });
  },
});

export const recentLogs = internalQuery({
  args: { limit: v.optional(v.number()) },
  handler: async (ctx, args) => {
    const limit = args.limit ?? 50;
    return await ctx.db
      .query("authLogs")
      .withIndex("by_createdAt")
      .order("desc")
      .take(limit);
  },
});

export const clearAll = internalMutation({
  args: {},
  handler: async (ctx) => {
    const logs = await ctx.db.query("authLogs").collect();
    for (const log of logs) {
      await ctx.db.delete(log._id);
    }
    return logs.length;
  },
});
