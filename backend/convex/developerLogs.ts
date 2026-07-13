import { internalMutation, internalQuery } from "./_generated/server";
import { v } from "convex/values";

// Only emails listed in CLOUD_PREVIEW_OWNER_EMAIL can write/read developer logs.
// Comma-separated list set in the Convex deployment env. Empty = no developers.
function getDeveloperEmails(): string[] {
  const raw = process.env.CLOUD_PREVIEW_OWNER_EMAIL ?? "";
  return raw
    .split(",")
    .map((s) => s.trim().toLowerCase())
    .filter(Boolean);
}

function isDeveloper(email?: string): boolean {
  if (!email) return false;
  const allow = getDeveloperEmails();
  return allow.includes(email.toLowerCase());
}

/** Write a developer log entry. Only accepted from developer emails. */
export const writeLog = internalMutation({
  args: {
    email: v.optional(v.string()),
    userId: v.optional(v.string()),
    source: v.union(v.literal("agent"), v.literal("mobile"), v.literal("web"), v.literal("relay")),
    level: v.union(v.literal("info"), v.literal("error"), v.literal("warn"), v.literal("debug")),
    tag: v.string(),
    message: v.string(),
    data: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    if (!isDeveloper(args.email)) return null;
    return await ctx.db.insert("developerLogs", {
      userId: args.userId,
      email: args.email,
      source: args.source,
      level: args.level,
      tag: args.tag,
      message: args.message,
      data: args.data ? args.data.slice(0, 8000) : undefined,
      createdAt: Date.now(),
    });
  },
});

/** Get recent developer logs. */
export const getLogs = internalQuery({
  args: {
    limit: v.optional(v.number()),
    email: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const limit = args.limit ?? 100;
    if (args.email) {
      return await ctx.db
        .query("developerLogs")
        .withIndex("by_email", (q) => q.eq("email", args.email!))
        .order("desc")
        .take(limit);
    }
    return await ctx.db
      .query("developerLogs")
      .order("desc")
      .take(limit);
  },
});

/** Clear all developer logs. */
export const clearLogs = internalMutation({
  args: {},
  handler: async (ctx) => {
    const logs = await ctx.db.query("developerLogs").collect();
    for (const log of logs) {
      await ctx.db.delete(log._id);
    }
    return logs.length;
  },
});
