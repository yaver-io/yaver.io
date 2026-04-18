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

/** List recent auth logs. */
export const listAuthLogs = query({
  args: {},
  handler: async (ctx) => {
    return await ctx.db
      .query("authLogs")
      .withIndex("by_createdAt")
      .order("desc")
      .take(100);
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

/** Export one user's account bundle for migration between deployments. */
export const exportUserBundleByEmail = query({
  args: { email: v.string() },
  handler: async (ctx, args) => {
    const user = await ctx.db
      .query("users")
      .withIndex("by_email", (q) => q.eq("email", args.email))
      .unique();
    if (!user) return null;

    const settings = await ctx.db
      .query("userSettings")
      .withIndex("by_userId", (q) => q.eq("userId", user._id))
      .first();

    const devices = await ctx.db
      .query("devices")
      .withIndex("by_userId", (q) => q.eq("userId", user._id))
      .collect();

    return { user, settings, devices };
  },
});

/** Import or update one user's account bundle from another deployment. */
export const importUserBundle = mutation({
  args: {
    user: v.object({
      email: v.string(),
      fullName: v.string(),
      provider: v.union(
        v.literal("google"),
        v.literal("microsoft"),
        v.literal("apple"),
        v.literal("github"),
        v.literal("gitlab"),
        v.literal("email"),
      ),
      providerId: v.string(),
      userId: v.string(),
      createdAt: v.number(),
      surveyCompleted: v.optional(v.boolean()),
      avatarUrl: v.optional(v.string()),
      passwordHash: v.optional(v.string()),
      totpSecret: v.optional(v.string()),
      totpEnabled: v.optional(v.boolean()),
      totpRecoveryCodes: v.optional(v.string()),
    }),
    settings: v.optional(v.object({
      forceRelay: v.optional(v.boolean()),
      runnerId: v.optional(v.string()),
      customRunnerCommand: v.optional(v.string()),
      relayUrl: v.optional(v.string()),
      relayPassword: v.optional(v.string()),
      tunnelUrl: v.optional(v.string()),
      speechProvider: v.optional(v.string()),
      speechApiKey: v.optional(v.string()),
      ttsEnabled: v.optional(v.boolean()),
      verbosity: v.optional(v.number()),
      keyStorage: v.optional(v.string()),
    })),
    devices: v.array(v.object({
      deviceId: v.string(),
      name: v.string(),
      platform: v.union(v.literal("macos"), v.literal("windows"), v.literal("linux"), v.literal("android"), v.literal("ios")),
      quicHost: v.string(),
      quicPort: v.number(),
      isOnline: v.boolean(),
      lastHeartbeat: v.number(),
      createdAt: v.number(),
      deviceClass: v.optional(v.union(v.literal("desktop"), v.literal("edge-mobile"), v.literal("server"))),
      edgeProfile: v.optional(v.any()),
      publicKey: v.optional(v.string()),
      runnerDown: v.optional(v.boolean()),
      runners: v.optional(v.any()),
      needsAuth: v.optional(v.boolean()),
      hardwareId: v.optional(v.string()),
    })),
  },
  handler: async (ctx, args) => {
    const existingByProvider = await ctx.db
      .query("users")
      .withIndex("by_provider", (q) => q.eq("provider", args.user.provider).eq("providerId", args.user.providerId))
      .unique();
    const existingByEmail = await ctx.db
      .query("users")
      .withIndex("by_email", (q) => q.eq("email", args.user.email))
      .unique();

    const existing = existingByProvider || existingByEmail;
    let userDocId = existing?._id;

    const userPatch = {
      email: args.user.email,
      fullName: args.user.fullName,
      provider: args.user.provider,
      providerId: args.user.providerId,
      userId: args.user.userId,
      createdAt: args.user.createdAt,
      surveyCompleted: args.user.surveyCompleted,
      avatarUrl: args.user.avatarUrl,
      passwordHash: args.user.passwordHash,
      totpSecret: args.user.totpSecret,
      totpEnabled: args.user.totpEnabled,
      totpRecoveryCodes: args.user.totpRecoveryCodes,
    };

    if (userDocId) {
      await ctx.db.patch(userDocId, userPatch);
    } else {
      userDocId = await ctx.db.insert("users", userPatch);
    }

    if (args.settings) {
      const existingSettings = await ctx.db
        .query("userSettings")
        .withIndex("by_userId", (q) => q.eq("userId", userDocId!))
        .first();
      const settingsPatch = {
        userId: userDocId!,
        ...args.settings,
      };
      if (existingSettings) {
        await ctx.db.patch(existingSettings._id, settingsPatch);
      } else {
        await ctx.db.insert("userSettings", settingsPatch);
      }
    }

    let importedDevices = 0;
    for (const device of args.devices) {
      const existingDevice = await ctx.db
        .query("devices")
        .withIndex("by_deviceId", (q) => q.eq("deviceId", device.deviceId))
        .unique();
      const devicePatch = {
        userId: userDocId!,
        ...device,
      };
      if (existingDevice) {
        await ctx.db.patch(existingDevice._id, devicePatch);
      } else {
        await ctx.db.insert("devices", devicePatch);
      }
      importedDevices += 1;
    }

    return {
      userId: userDocId,
      email: args.user.email,
      importedDevices,
      hasSettings: !!args.settings,
    };
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
