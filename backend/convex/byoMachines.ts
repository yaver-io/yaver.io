// byoMachines — lifecycle bookkeeping for boxes a user runs on their OWN
// provider account (BYO Hetzner/DigitalOcean). Counter/id/timestamp only;
// NEVER a token/key/path (privacy contract; pinned by
// convex_privacy_test.go). The agent emits state transitions here via
// convexSyncer (Convex-auth identity → resolveUser, so a caller can only
// ever write their OWN rows). Reads go through the session-authed HTTP
// route (/byo/machines) which scopes by the resolved userId.

import { mutation, internalMutation, internalQuery } from "./_generated/server";
import { v } from "convex/values";
import { resolveUser } from "./agentSync";

const STATE = v.union(v.literal("active"), v.literal("stopped"), v.literal("deleted"));

// Upsert a BYO box's lifecycle state. Keyed by (userId, serverId) so a
// re-emit just patches. Sets the right timestamp for the transition.
// userId comes from the authenticated identity — never from args — so
// one user can never write another user's row (or read their Hetzner).
export const upsert = mutation({
  args: {
    provider: v.string(),
    serverId: v.string(),
    state: STATE,
    name: v.optional(v.string()),
    deviceId: v.optional(v.string()),
    region: v.optional(v.string()),
    plan: v.optional(v.string()),
    serverIp: v.optional(v.string()),
    imageId: v.optional(v.string()),
    snapshotImageId: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const userId = await resolveUser(ctx);
    const now = Date.now();
    const existing = await ctx.db
      .query("byoMachines")
      .withIndex("by_user_server", (q) => q.eq("userId", userId).eq("serverId", args.serverId))
      .first();

    // Only set fields that were provided (don't clobber a known name/ip
    // with undefined on a state-only re-emit).
    const patch: Record<string, unknown> = {
      provider: args.provider,
      state: args.state,
      updatedAt: now,
    };
    for (const k of ["name", "deviceId", "region", "plan", "serverIp", "imageId", "snapshotImageId"] as const) {
      if (args[k] !== undefined) patch[k] = args[k];
    }
    if (args.state === "active") patch.lastUpAt = now;
    if (args.state === "stopped") patch.stoppedAt = now;
    if (args.state === "deleted") patch.deletedAt = now;

    if (existing) {
      await ctx.db.patch(existing._id, patch);
      return existing._id;
    }
    return ctx.db.insert("byoMachines", {
      userId,
      serverId: args.serverId,
      name: args.name ?? args.serverId,
      createdAt: now,
      ...(patch as any),
    });
  },
});

// Internal read for the session-authed HTTP route. Scoped to one user.
export const listForUserInternal = internalQuery({
  args: { userId: v.id("users") },
  handler: async (ctx, { userId }) => {
    const rows = await ctx.db
      .query("byoMachines")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .order("desc")
      .take(100);
    // Surface only the non-sensitive bookkeeping fields (the table holds
    // nothing else, but be explicit).
    return rows.map((m) => ({
      id: String(m._id),
      provider: m.provider,
      serverId: m.serverId,
      deviceId: m.deviceId ?? null,
      name: m.name,
      region: m.region ?? null,
      plan: m.plan ?? null,
      serverIp: m.serverIp ?? null,
      imageId: m.imageId ?? null,
      snapshotImageId: m.snapshotImageId ?? null,
      state: m.state,
      createdAt: m.createdAt,
      lastUpAt: m.lastUpAt ?? null,
      stoppedAt: m.stoppedAt ?? null,
      deletedAt: m.deletedAt ?? null,
      updatedAt: m.updatedAt,
    }));
  },
});

/**
 * Permanently forget bookkeeping for a provider resource independently
 * confirmed to no longer exist. This never calls a provider and cannot delete
 * infrastructure. Every immutable identity field must match so an operator
 * cannot remove the wrong tenant's or wrong server's binding by typo.
 */
export const purgeMissingResourceBinding = internalMutation({
  args: {
    userId: v.id("users"),
    provider: v.string(),
    serverId: v.string(),
    deviceId: v.string(),
    serverIp: v.string(),
  },
  handler: async (ctx, args) => {
    const row = await ctx.db
      .query("byoMachines")
      .withIndex("by_user_server", (q) => q.eq("userId", args.userId).eq("serverId", args.serverId))
      .unique();
    if (!row) return { ok: true, alreadyGone: true };
    if (
      row.provider !== args.provider ||
      row.deviceId !== args.deviceId ||
      row.serverIp !== args.serverIp
    ) {
      throw new Error("BYO_RESOURCE_IDENTITY_MISMATCH");
    }
    await ctx.db.delete(row._id);
    return { ok: true, alreadyGone: false, deletedId: String(row._id) };
  },
});

/**
 * Convert an existing bookkeeping row to client-custodied BYO ownership.
 * This changes metadata only: it never calls the provider and accepts no
 * credential. The immutable provider server id and Primary IPv4 must match the
 * existing row, preventing an operator typo from rebinding another resource.
 */
export const adoptExistingClientManaged = internalMutation({
  args: {
    machineId: v.id("cloudMachines"),
    providerServerId: v.string(),
    primaryIpv4: v.string(),
    name: v.string(),
    region: v.optional(v.string()),
    plan: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const machine = await ctx.db.get(args.machineId);
    if (!machine) throw new Error("MACHINE_NOT_FOUND");
    if (machine.provider !== "hetzner") throw new Error("PROVIDER_MISMATCH");
    if (String(machine.hetznerServerId || "") !== args.providerServerId.trim()) {
      throw new Error("SERVER_ID_MISMATCH");
    }
    if (String(machine.serverIp || "") !== args.primaryIpv4.trim()) {
      throw new Error("PRIMARY_IP_MISMATCH");
    }
    if (!machine.deviceId) throw new Error("DEVICE_NOT_REGISTERED");
    const device = await ctx.db
      .query("devices")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", machine.deviceId!))
      .unique();
    if (!device || device.userId !== machine.userId || device.removed === true) {
      throw new Error("OWNED_DEVICE_NOT_FOUND");
    }
    const name = args.name.trim();
    if (!name) throw new Error("NAME_REQUIRED");
    const now = Date.now();

    await ctx.db.patch(machine._id, {
      origin: "self-hosted",
      tier: "byok",
      hostname: name,
      updatedAt: now,
    });
    await ctx.db.patch(device._id, { name, alias: name });

    const existing = await ctx.db
      .query("byoMachines")
      .withIndex("by_user_server", (q) => q.eq("userId", machine.userId).eq("serverId", args.providerServerId.trim()))
      .unique();
    const byo = {
      provider: "hetzner",
      serverId: args.providerServerId.trim(),
      deviceId: machine.deviceId,
      name,
      region: args.region,
      plan: args.plan,
      serverIp: args.primaryIpv4.trim(),
      state: "active" as const,
      lastUpAt: now,
      updatedAt: now,
    };
    const byoId = existing
      ? (await ctx.db.patch(existing._id, byo), existing._id)
      : await ctx.db.insert("byoMachines", { userId: machine.userId, createdAt: now, ...byo });
    return { ok: true, userId: machine.userId, deviceId: machine.deviceId, byoId };
  },
});
