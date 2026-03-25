import { internalMutation } from "./_generated/server";

const SEVEN_DAYS_MS = 7 * 24 * 60 * 60 * 1000;

export const pruneAuthLogs = internalMutation({
  args: {},
  handler: async (ctx) => {
    const cutoff = Date.now() - SEVEN_DAYS_MS;
    const old = await ctx.db
      .query("authLogs")
      .withIndex("by_createdAt", (q) => q.lt("createdAt", cutoff))
      .take(500);
    for (const row of old) {
      await ctx.db.delete(row._id);
    }
    return old.length;
  },
});

export const pruneMobileStreamLogs = internalMutation({
  args: {},
  handler: async (ctx) => {
    const cutoff = Date.now() - SEVEN_DAYS_MS;
    const old = await ctx.db
      .query("mobileStreamLogs")
      .withIndex("by_createdAt", (q) => q.lt("createdAt", cutoff))
      .take(500);
    for (const row of old) {
      await ctx.db.delete(row._id);
    }
    return old.length;
  },
});

export const pruneDeveloperLogs = internalMutation({
  args: {},
  handler: async (ctx) => {
    const cutoff = Date.now() - SEVEN_DAYS_MS;
    const old = await ctx.db
      .query("developerLogs")
      .withIndex("by_createdAt", (q) => q.lt("createdAt", cutoff))
      .take(500);
    for (const row of old) {
      await ctx.db.delete(row._id);
    }
    return old.length;
  },
});

export const pruneDeviceEvents = internalMutation({
  args: {},
  handler: async (ctx) => {
    const cutoff = Date.now() - SEVEN_DAYS_MS;
    // deviceEvents uses by_deviceId index (compound: deviceId + timestamp)
    // so we scan without index and filter by timestamp
    const old = await ctx.db
      .query("deviceEvents")
      .filter((q) => q.lt(q.field("timestamp"), cutoff))
      .take(500);
    for (const row of old) {
      await ctx.db.delete(row._id);
    }
    return old.length;
  },
});
