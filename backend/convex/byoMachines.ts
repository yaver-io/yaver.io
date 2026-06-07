// byoMachines — lifecycle bookkeeping for boxes a user runs on their OWN
// provider account (BYO Hetzner/DigitalOcean). Counter/id/timestamp only;
// NEVER a token/key/path (privacy contract; pinned by
// convex_privacy_test.go). The agent emits state transitions here via
// convexSyncer (Convex-auth identity → resolveUser, so a caller can only
// ever write their OWN rows). Reads go through the session-authed HTTP
// route (/byo/machines) which scopes by the resolved userId.

import { mutation, internalQuery } from "./_generated/server";
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
