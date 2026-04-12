import { v } from "convex/values";
import { mutation, query } from "./_generated/server";

/**
 * Admin utilities for user data cleanup.
 * These are not exposed via HTTP — only callable via Convex client (scripts/dashboard).
 */

/** List all users. */
export const listAllUsers = query({
  args: {},
  handler: async (ctx) => {
    return await ctx.db.query("users").collect();
  },
});

/** List all sessions. */
export const listAllSessions = query({
  args: {},
  handler: async (ctx) => {
    return await ctx.db.query("sessions").collect();
  },
});

/** List all devices. */
export const listAllDevices = query({
  args: {},
  handler: async (ctx) => {
    return await ctx.db.query("devices").collect();
  },
});

/** Find all users by email. Returns array of user documents. */
export const getUsersByEmail = query({
  args: { email: v.string() },
  handler: async (ctx, args) => {
    return await ctx.db
      .query("users")
      .withIndex("by_email", (q) => q.eq("email", args.email))
      .collect();
  },
});

/** Delete ALL user data from the system — every table that holds user/device/session state. */
export const deleteAllUserData = mutation({
  args: {},
  handler: async (ctx) => {
    const tables = [
      "users",
      "sessions",
      "devices",
      "userSettings",
      "developerSurveys",
      "runnerUsage",
      "dailyTaskCounts",
      "deviceMetrics",
      "deviceEvents",
      "passwordResets",
      "pendingAuth",
      "authLogs",
      "developerLogs",
      "deviceCodes",
      "downloads",
      "guestInvitations",
      "guestAccess",
      "guestUsage",
      "sdkTokens",
      "securityEvents",
      "mobileStreamLogs",
      "teams",
      "teamMembers",
      "subscriptions",
      "managedRelays",
      "cloudMachines",
    ] as const;

    const counts: Record<string, number> = {};
    for (const table of tables) {
      const docs = await ctx.db.query(table).collect();
      for (const doc of docs) {
        await ctx.db.delete(doc._id);
      }
      counts[table] = docs.length;
    }

    return counts;
  },
});

/** Delete all rows from a single table (paginated to stay within limits). */
export const clearTable = mutation({
  args: { table: v.string() },
  handler: async (ctx, args) => {
    const docs = await ctx.db.query(args.table as any).take(500);
    for (const doc of docs) {
      await ctx.db.delete(doc._id);
    }
    return { table: args.table, deleted: docs.length, hasMore: docs.length === 500 };
  },
});

/** Delete a user and ALL their data by user _id. */
export const deleteUserData = mutation({
  args: { userId: v.id("users") },
  handler: async (ctx, args) => {
    const user = await ctx.db.get(args.userId);
    if (!user) throw new Error("User not found");

    const counts: Record<string, number> = {};

    // Delete from all user-scoped tables
    const tables = ["sessions", "devices", "userSettings", "developerSurveys", "runnerUsage", "dailyTaskCounts", "deviceMetrics", "deviceEvents"] as const;
    for (const table of tables) {
      const docs = await ctx.db.query(table).collect();
      const userDocs = docs.filter((d: any) => d.userId === args.userId);
      for (const doc of userDocs) {
        await ctx.db.delete(doc._id);
      }
      counts[table] = userDocs.length;
    }

    // Delete the user
    await ctx.db.delete(args.userId);

    return {
      email: user.email,
      ...counts,
    };
  },
});
