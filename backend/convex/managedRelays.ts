import { v } from "convex/values";
import { mutation, query, internalMutation, internalQuery } from "./_generated/server";

// Get user's managed relay (public query for UI)
export const getByUser = internalQuery({
  args: { userId: v.id("users") },
  handler: async (ctx, { userId }) => {
    return await ctx.db
      .query("managedRelays")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .first();
  },
});

// Get user's managed relay (internal — for webhook/action use)
export const getByUserInternal = internalQuery({
  args: { userId: v.id("users") },
  handler: async (ctx, { userId }) => {
    return await ctx.db
      .query("managedRelays")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .first();
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

// Create a pending managed relay (called after payment confirmed)
export const create = internalMutation({
  args: {
    userId: v.id("users"),
    subscriptionId: v.id("subscriptions"),
    region: v.string(),
    password: v.string(),
  },
  handler: async (ctx, args) => {
    // Check if user already has a relay
    const existing = await ctx.db
      .query("managedRelays")
      .withIndex("by_user", (q) => q.eq("userId", args.userId))
      .first();
    if (existing && existing.status !== "stopped" && existing.status !== "error") {
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
    domain: v.string(),
  },
  handler: async (ctx, args) => {
    await ctx.db.patch(args.relayId, {
      status: "active",
      hetznerServerId: args.hetznerServerId,
      serverIp: args.serverIp,
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
    await ctx.db.patch(relayId, { lastHealthCheck: Date.now() });
  },
});
