import { v } from "convex/values";
import { mutation, query, internalMutation, internalQuery } from "./_generated/server";

// Get user's managed relay (public query for UI)
export const getByUser = internalQuery({
  args: { userId: v.id("users") },
  handler: async (ctx, { userId }) => {
    const rows = await ctx.db
      .query("managedRelays")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .collect();
    return rows.sort((a, b) => {
      const aLive = a.status !== "stopped" && a.status !== "error" ? 1 : 0;
      const bLive = b.status !== "stopped" && b.status !== "error" ? 1 : 0;
      return bLive - aLive || (b.updatedAt ?? b.createdAt) - (a.updatedAt ?? a.createdAt);
    })[0] ?? null;
  },
});

// Get user's managed relay (internal — for webhook/action use)
export const getByUserInternal = internalQuery({
  args: { userId: v.id("users") },
  handler: async (ctx, { userId }) => {
    const rows = await ctx.db
      .query("managedRelays")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .collect();
    return rows.sort((a, b) => {
      const aLive = a.status !== "stopped" && a.status !== "error" ? 1 : 0;
      const bLive = b.status !== "stopped" && b.status !== "error" ? 1 : 0;
      return bLive - aLive || (b.updatedAt ?? b.createdAt) - (a.updatedAt ?? a.createdAt);
    })[0] ?? null;
  },
});

export const listBySubscription = internalQuery({
  args: { subscriptionId: v.id("subscriptions") },
  handler: async (ctx, { subscriptionId }) => {
    return await ctx.db
      .query("managedRelays")
      .withIndex("by_subscription", (q) => q.eq("subscriptionId", subscriptionId))
      .collect();
  },
});

/** Get one relay row by its Convex id (internal — used by deprovision so it
 *  can tell shared-pool rows from dedicated ones before touching the box). */
export const getById = internalQuery({
  args: { relayId: v.id("managedRelays") },
  handler: async (ctx, { relayId }) => {
    return await ctx.db.get(relayId);
  },
});

// Create a pending managed relay (called after payment confirmed)
export const create = internalMutation({
  args: {
    userId: v.id("users"),
    // Optional for the owner-dev path (/billing/relay-pro/dev-activate) —
    // a dev relay is provisioned on the owner's real Hetzner account
    // WITHOUT a LemonSqueezy subscription; canProvisionManaged's owner
    // bypass is what authorises the spend. Paid relays always pass it.
    subscriptionId: v.optional(v.id("subscriptions")),
    region: v.string(),
    password: v.string(),
  },
  handler: async (ctx, args) => {
    // Check if user already has a relay
    const existingRows = await ctx.db
      .query("managedRelays")
      .withIndex("by_user", (q) => q.eq("userId", args.userId))
      .collect();
    const existing = existingRows
      .filter((row) => row.status !== "stopped" && row.status !== "error")
      .sort((a, b) => (b.updatedAt ?? b.createdAt) - (a.updatedAt ?? a.createdAt))[0];
    if (existing && existing.status !== "stopped" && existing.status !== "error") {
      // A resubscribe gets a new subscription row. Relink the reusable relay
      // so cancellation/refund of the current purchase can always find and
      // deprovision its resource.
      if (String(existing.subscriptionId || "") !== String(args.subscriptionId || "")) {
        await ctx.db.patch(existing._id, {
          subscriptionId: args.subscriptionId,
          updatedAt: Date.now(),
        });
      }
      return existing._id;
    }

    return await ctx.db.insert("managedRelays", {
      userId: args.userId,
      subscriptionId: args.subscriptionId,
      status: "provisioning",
      region: args.region,
      password: args.password,
      quicPort: 4433,
      httpPort: 443,
      createdAt: Date.now(),
      updatedAt: Date.now(),
    });
  },
});

// Update relay after Hetzner provisioning
export const updateProvisioned = internalMutation({
  args: {
    relayId: v.id("managedRelays"),
    hetznerServerId: v.string(),
    serverIp: v.string(),
    serverIpv6: v.optional(v.string()),
    domain: v.string(),
  },
  handler: async (ctx, args) => {
    await ctx.db.patch(args.relayId, {
      // Provider inventory exists, but paid delivery is not active until the
      // HTTPS operation succeeds in provisionRelay.healthCheck.
      status: "provisioning",
      hetznerServerId: args.hetznerServerId,
      serverIp: args.serverIp,
      serverIpv6: args.serverIpv6,
      domain: args.domain,
      updatedAt: Date.now(),
    });
  },
});

// Mark relay as stopping/stopped
/**
 * Record the grace snapshot taken at deprovision.
 *
 * Without this the snapshot is billed forever AND unrestorable — the relay
 * teardown created one and discarded the id, so the "a resubscribe can be
 * restored from it" promise was not achievable, and the orphan sweep could not
 * tell the snapshot apart from junk. 2026-07-21 audit.
 */
export const setSnapshot = internalMutation({
  args: { relayId: v.id("managedRelays"), lastSnapshotId: v.string() },
  handler: async (ctx, { relayId, lastSnapshotId }) => {
    await ctx.db.patch(relayId, {
      lastSnapshotId,
      lastSnapshotAt: Date.now(),
      updatedAt: Date.now(),
    });
    return { ok: true };
  },
});

export const setStatus = internalMutation({
  args: {
    relayId: v.id("managedRelays"),
    status: v.string(),
    errorMessage: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    await ctx.db.patch(args.relayId, {
      status: args.status,
      updatedAt: Date.now(),
      ...(args.errorMessage ? { errorMessage: args.errorMessage } : {}),
    });
  },
});

// Record health check
export const recordHealthCheck = internalMutation({
  args: { relayId: v.id("managedRelays") },
  handler: async (ctx, { relayId }) => {
    await ctx.db.patch(relayId, {
      status: "active",
      lastHealthCheck: Date.now(),
      errorMessage: undefined,
      updatedAt: Date.now(),
    });
  },
});
