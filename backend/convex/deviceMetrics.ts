import { v } from "convex/values";
import { mutation, query } from "./_generated/server";
import { validateSessionInternal } from "./auth";

const ONE_HOUR_MS = 60 * 60 * 1000;

/**
 * Report metrics from a desktop agent. Called every ~60s.
 * Also prunes entries older than 1 hour for this device.
 */
export const report = mutation({
  args: {
    tokenHash: v.string(),
    deviceId: v.string(),
    cpuPercent: v.number(),
    memoryUsedMb: v.number(),
    memoryTotalMb: v.number(),
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

    const now = Date.now();

    // Insert new metric
    await ctx.db.insert("deviceMetrics", {
      deviceId: args.deviceId,
      timestamp: now,
      cpuPercent: args.cpuPercent,
      memoryUsedMb: args.memoryUsedMb,
      memoryTotalMb: args.memoryTotalMb,
    });

    // Prune old entries (older than 1 hour)
    const cutoff = now - ONE_HOUR_MS;
    const old = await ctx.db
      .query("deviceMetrics")
      .withIndex("by_deviceId", (q) =>
        q.eq("deviceId", args.deviceId).lt("timestamp", cutoff)
      )
      .collect();
    for (const entry of old) {
      await ctx.db.delete(entry._id);
    }
  },
});

/**
 * Get metrics for a device (last 1 hour). Used by mobile app.
 */
export const getMetrics = query({
  args: {
    tokenHash: v.string(),
    deviceId: v.string(),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    // SECURITY (audit 2026-07-13): verify the device belongs to the caller —
    // mirrors the ownership check the `report` write path already has. Without
    // it any signed-in user could read another user's device telemetry by
    // supplying a known deviceId (authenticated IDOR).
    const device = await ctx.db
      .query("devices")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .unique();
    if (!device || device.userId !== session.user._id) {
      throw new Error("Device not found or unauthorized");
    }

    const cutoff = Date.now() - ONE_HOUR_MS;
    const metrics = await ctx.db
      .query("deviceMetrics")
      .withIndex("by_deviceId", (q) =>
        q.eq("deviceId", args.deviceId).gt("timestamp", cutoff)
      )
      .collect();

    return metrics.map((m) => ({
      timestamp: m.timestamp,
      cpuPercent: m.cpuPercent,
      memoryUsedMb: m.memoryUsedMb,
      memoryTotalMb: m.memoryTotalMb,
    }));
  },
});
