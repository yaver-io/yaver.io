// Companion-compute bookkeeping mutations/queries, called by the Yaver agent
// (desktop/agent/companion.go) to give the dashboard cross-device visibility of
// which box runs which serverless project's crons.
//
// PRIVACY: these rows are bookkeeping ONLY — slug + bound deviceId + cron
// names/schedules + last/next-run status. No endpoint URLs, cron auth tokens,
// vault secrets, or absolute paths ever reach here; the agent strips them via
// buildCompanionUpsertPayload and desktop/agent/convex_privacy_test.go pins it.

import { v } from "convex/values";
import { mutation, query } from "./_generated/server";
import { resolveUser } from "./agentSync";

const cronEntry = v.object({
  name: v.string(),
  schedule: v.string(),
  lastOutcome: v.optional(v.string()),
  lastRunAt: v.optional(v.number()),
  nextRunAt: v.optional(v.number()),
});

/** Upsert a companion project's bookkeeping row, keyed by (deviceId, slug). */
export const upsertCompanionProject = mutation({
  args: {
    deviceId: v.string(),
    slug: v.string(),
    enabled: v.boolean(),
    crons: v.array(cronEntry),
    serviceCount: v.number(),
  },
  handler: async (ctx, args) => {
    const userId = await resolveUser(ctx);
    const existing = await ctx.db
      .query("companionProjects")
      .withIndex("by_device_slug", (q) => q.eq("deviceId", args.deviceId).eq("slug", args.slug))
      .first();
    const patch = { ...args, userId, updatedAt: Date.now() };
    if (existing) {
      await ctx.db.patch(existing._id, patch);
      return existing._id;
    }
    return ctx.db.insert("companionProjects", patch);
  },
});

/** Flip a companion project's enabled flag (e.g. on Down). */
export const setCompanionEnabled = mutation({
  args: { deviceId: v.string(), slug: v.string(), enabled: v.boolean() },
  handler: async (ctx, args) => {
    await resolveUser(ctx);
    const existing = await ctx.db
      .query("companionProjects")
      .withIndex("by_device_slug", (q) => q.eq("deviceId", args.deviceId).eq("slug", args.slug))
      .first();
    if (existing) {
      await ctx.db.patch(existing._id, { enabled: args.enabled, updatedAt: Date.now() });
    }
    return null;
  },
});

/** List the signed-in user's companion projects across all their devices. */
export const listForUser = query({
  args: {},
  handler: async (ctx) => {
    const userId = await resolveUser(ctx);
    return ctx.db
      .query("companionProjects")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .collect();
  },
});
