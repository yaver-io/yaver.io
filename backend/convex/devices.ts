import { v } from "convex/values";
import { mutation, query } from "./_generated/server";
import { validateSessionInternal } from "./auth";

/**
 * Register or update a device for peer discovery.
 * Requires a valid session tokenHash.
 */
export const registerDevice = mutation({
  args: {
    tokenHash: v.string(),
    deviceId: v.string(),
    name: v.string(),
    platform: v.union(
      v.literal("macos"),
      v.literal("windows"),
      v.literal("linux")
    ),
    publicKey: v.optional(v.string()),
    quicHost: v.string(),
    quicPort: v.number(),
    hardwareId: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const existing = await ctx.db
      .query("devices")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .unique();

    if (existing) {
      // Only allow the owner to update their own device
      if (existing.userId !== session.user._id) {
        throw new Error("Device belongs to another user");
      }
      await ctx.db.patch(existing._id, {
        name: args.name,
        platform: args.platform,
        publicKey: args.publicKey,
        quicHost: args.quicHost,
        quicPort: args.quicPort,
        isOnline: true,
        lastHeartbeat: Date.now(),
        ...(args.hardwareId ? { hardwareId: args.hardwareId } : {}),
      });
      return existing._id;
    }

    return await ctx.db.insert("devices", {
      userId: session.user._id,
      deviceId: args.deviceId,
      name: args.name,
      platform: args.platform,
      publicKey: args.publicKey,
      quicHost: args.quicHost,
      quicPort: args.quicPort,
      isOnline: true,
      lastHeartbeat: Date.now(),
      createdAt: Date.now(),
      hardwareId: args.hardwareId,
    });
  },
});

/**
 * Look up the owner of a device by its stable hardware ID.
 * Used by the agent's /auth/recover endpoint to verify that the
 * caller (mobile app) is the original host of a machine that has
 * lost its auth token. No tokenHash required — the agent calls
 * this on behalf of the caller and the host check is what gates
 * the recovery action.
 */
export const ownerByHardwareId = query({
  args: {
    hardwareId: v.string(),
  },
  handler: async (ctx, args) => {
    const device = await ctx.db
      .query("devices")
      .withIndex("by_hardwareId", (q) => q.eq("hardwareId", args.hardwareId))
      .first();
    if (!device) return null;
    return {
      deviceId: device.deviceId,
      ownerUserId: device.userId,
      name: device.name,
    };
  },
});

/**
 * Update device heartbeat — marks it as online.
 */
export const heartbeat = mutation({
  args: {
    tokenHash: v.string(),
    deviceId: v.string(),
    runners: v.optional(v.array(v.object({
      taskId: v.string(),
      runnerId: v.string(),
      model: v.optional(v.string()),
      pid: v.number(),
      status: v.string(),
      title: v.string(),
    }))),
    quicHost: v.optional(v.string()),
    hardwareId: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const device = await ctx.db
      .query("devices")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .unique();

    if (!device) throw new Error("Device not found");
    if (device.userId !== session.user._id) throw new Error("Unauthorized");

    const patch: Record<string, unknown> = {
      isOnline: true,
      lastHeartbeat: Date.now(),
      runners: args.runners ?? [],
    };
    // Update stored IP if the agent reports a new one
    if (args.quicHost && args.quicHost !== device.quicHost) {
      patch.quicHost = args.quicHost;
    }
    // Capture hardwareId on heartbeats too — older agents that
    // were registered before the field existed will pick it up
    // on their next heartbeat.
    if (args.hardwareId && args.hardwareId !== device.hardwareId) {
      patch.hardwareId = args.hardwareId;
    }
    await ctx.db.patch(device._id, patch);
  },
});

/**
 * List all devices belonging to the authenticated user,
 * plus devices from hosts who granted them guest access.
 */
export const listMyDevices = query({
  args: {
    tokenHash: v.string(),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    // Own devices
    const ownDevices = await ctx.db
      .query("devices")
      .withIndex("by_userId", (q) => q.eq("userId", session.user._id))
      .collect();

    const result = ownDevices.map((d) => ({
      deviceId: d.deviceId,
      name: d.name,
      platform: d.platform,
      publicKey: d.publicKey,
      quicHost: d.quicHost,
      quicPort: d.quicPort,
      isOnline: d.isOnline,
      runnerDown: d.runnerDown ?? false,
      runners: d.runners ?? [],
      lastHeartbeat: d.lastHeartbeat,
      isGuest: false as boolean,
      hostName: undefined as string | undefined,
      hostEmail: undefined as string | undefined,
    }));

    // Devices from hosts who granted guest access
    const guestAccessRecords = await ctx.db
      .query("guestAccess")
      .withIndex("by_guestUserId", (q) => q.eq("guestUserId", session.user._id))
      .filter((q) => q.eq(q.field("revokedAt"), undefined))
      .collect();

    for (const access of guestAccessRecords) {
      const host = await ctx.db.get(access.hostUserId);
      if (!host) continue;

      const hostDevices = await ctx.db
        .query("devices")
        .withIndex("by_userId", (q) => q.eq("userId", access.hostUserId))
        .collect();

      for (const d of hostDevices) {
        result.push({
          deviceId: d.deviceId,
          name: d.name,
          platform: d.platform,
          publicKey: d.publicKey,
          quicHost: d.quicHost,
          quicPort: d.quicPort,
          isOnline: d.isOnline,
          runnerDown: d.runnerDown ?? false,
          runners: d.runners ?? [],
          lastHeartbeat: d.lastHeartbeat,
          isGuest: true,
          hostName: host.fullName,
          hostEmail: host.email,
        });
      }
    }

    return result;
  },
});

/**
 * Update the runnerDown flag for a device.
 * Called by the desktop agent when runner crashes with all retries exhausted,
 * or when runner is successfully restarted.
 */
export const setRunnerDown = mutation({
  args: {
    tokenHash: v.string(),
    deviceId: v.string(),
    runnerDown: v.boolean(),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const device = await ctx.db
      .query("devices")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .unique();

    if (!device) throw new Error("Device not found");
    if (device.userId !== session.user._id) throw new Error("Unauthorized");

    await ctx.db.patch(device._id, { runnerDown: args.runnerDown });
  },
});

/**
 * Mark a device as offline.
 * Called by the desktop agent on stop/signout.
 */
export const markOffline = mutation({
  args: {
    tokenHash: v.string(),
    deviceId: v.string(),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const device = await ctx.db
      .query("devices")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .unique();

    if (!device) throw new Error("Device not found");
    if (device.userId !== session.user._id) throw new Error("Unauthorized");

    await ctx.db.patch(device._id, {
      isOnline: false,
      lastHeartbeat: Date.now(),
    });
  },
});

/**
 * Remove (unregister) a device.
 */
export const removeDevice = mutation({
  args: {
    tokenHash: v.string(),
    deviceId: v.string(),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const device = await ctx.db
      .query("devices")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .unique();

    if (!device) throw new Error("Device not found");
    if (device.userId !== session.user._id) throw new Error("Unauthorized");

    await ctx.db.delete(device._id);
  },
});
