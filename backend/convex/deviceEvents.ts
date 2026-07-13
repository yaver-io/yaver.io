import { v } from "convex/values";
import { mutation, query } from "./_generated/server";
import { validateSessionInternal } from "./auth";

/**
 * Record a device lifecycle event (crash, restart, OOM, etc.).
 * Called by the desktop agent.
 */
export const record = mutation({
  args: {
    tokenHash: v.string(),
    deviceId: v.string(),
    event: v.union(
      v.literal("crash"),
      v.literal("restart"),
      v.literal("oom"),
      v.literal("started"),
      v.literal("stopped"),
    ),
    details: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    // Verify device belongs to this user
    const device = await ctx.db
      .query("devices")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .unique();
    if (!device || device.userId !== session.user._id) {
      throw new Error("Device not found or unauthorized");
    }

    await ctx.db.insert("deviceEvents", {
      deviceId: args.deviceId,
      event: args.event,
      details: args.details,
      timestamp: Date.now(),
    });

    // Keep only last 100 events per device
    const all = await ctx.db
      .query("deviceEvents")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .collect();
    if (all.length > 100) {
      const toDelete = all.slice(0, all.length - 100);
      for (const entry of toDelete) {
        await ctx.db.delete(entry._id);
      }
    }
  },
});

/**
 * Get recent events for a device. Used by mobile app.
 */
export const getEvents = query({
  args: {
    tokenHash: v.string(),
    deviceId: v.string(),
    limit: v.optional(v.number()),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    // SECURITY (audit 2026-07-13): verify device ownership before returning its
    // event history — mirrors the `record` write path. Prevents authenticated
    // IDOR read of another user's device lifecycle events.
    const device = await ctx.db
      .query("devices")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .unique();
    if (!device || device.userId !== session.user._id) {
      throw new Error("Device not found or unauthorized");
    }

    const limit = args.limit ?? 50;
    const events = await ctx.db
      .query("deviceEvents")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .order("desc")
      .take(limit);

    return events.map((e) => ({
      event: e.event,
      details: e.details,
      timestamp: e.timestamp,
    }));
  },
});
