import { internalMutation, internalQuery } from "./_generated/server";
import { v } from "convex/values";

export const writeLog = internalMutation({
  args: {
    userId: v.optional(v.string()),
    platform: v.string(),
    appVersion: v.string(),
    buildNumber: v.string(),
    level: v.union(v.literal("info"), v.literal("error"), v.literal("warn")),
    step: v.string(),
    message: v.string(),
    details: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    await ctx.db.insert("mobileStreamLogs", {
      ...args,
      createdAt: Date.now(),
    });
  },
});

export const recent = internalQuery({
  args: { limit: v.optional(v.number()) },
  handler: async (ctx, args) => {
    return await ctx.db
      .query("mobileStreamLogs")
      .withIndex("by_createdAt")
      .order("desc")
      .take(args.limit ?? 100);
  },
});

export const clearAll = internalMutation({
  args: {},
  handler: async (ctx) => {
    const logs = await ctx.db.query("mobileStreamLogs").collect();
    for (const log of logs) {
      await ctx.db.delete(log._id);
    }
    return logs.length;
  },
});
