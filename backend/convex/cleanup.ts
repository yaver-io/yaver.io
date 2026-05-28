import { internalMutation } from "./_generated/server";

const SEVEN_DAYS_MS = 7 * 24 * 60 * 60 * 1000;
const ONE_DAY_MS = 24 * 60 * 60 * 1000;

/** Resolve the effective retention window. Reads the orgPolicy
 *  singleton; falls back to 7 days when no policy is set. The solo
 *  developer path never visits the admin console, so the default is
 *  exactly the same as the pre-policy hard-coded value. */
async function retentionMs(ctx: { db: any }): Promise<number> {
  try {
    const policy = await ctx.db
      .query("orgPolicy")
      .withIndex("by_singleton", (q: any) => q.eq("singletonKey", "org"))
      .first();
    if (!policy?.auditRetentionDays || policy.auditRetentionDays < 1) {
      return SEVEN_DAYS_MS;
    }
    return policy.auditRetentionDays * ONE_DAY_MS;
  } catch {
    return SEVEN_DAYS_MS;
  }
}

export const pruneAuthLogs = internalMutation({
  args: {},
  handler: async (ctx) => {
    const cutoff = Date.now() - (await retentionMs(ctx));
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
    const cutoff = Date.now() - (await retentionMs(ctx));
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
    const cutoff = Date.now() - (await retentionMs(ctx));
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
    const cutoff = Date.now() - (await retentionMs(ctx));
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
