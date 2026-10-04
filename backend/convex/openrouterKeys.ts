// Legacy hosted-inference metadata. Provisioning and provider communication
// are intentionally retired: Yaver-operated infrastructure must never receive
// or distribute model-provider credentials. These exports remain temporarily
// so existing plans and generated clients fail closed during migration.

import { internalAction, internalMutation, internalQuery } from "./_generated/server";
import { v } from "convex/values";

export function openrouterLimitCents(_monthlyWalletCents: number): number {
  return 0;
}

export const getByUser = internalQuery({
  args: { userId: v.id("users") },
  handler: async (ctx, { userId }) => {
    return await ctx.db
      .query("openrouterKeys")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .unique();
  },
});

export const upsertMeta = internalMutation({
  args: {
    userId: v.id("users"),
    orHash: v.string(),
    name: v.string(),
    limitCents: v.number(),
    status: v.string(),
  },
  handler: async () => {
    throw new Error("hosted inference is retired in zero-knowledge mode");
  },
});

export const patchMeta = internalMutation({
  args: {
    userId: v.id("users"),
    limitCents: v.optional(v.number()),
    status: v.optional(v.string()),
    usageCents: v.optional(v.number()),
    markSynced: v.optional(v.boolean()),
  },
  handler: async (ctx, { userId, status }) => {
    const row = await ctx.db
      .query("openrouterKeys")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .unique();
    if (row && status === "disabled") {
      const now = Date.now();
      await ctx.db.patch(row._id, { status: "disabled", disabledAt: now, updatedAt: now });
    }
  },
});

export const ensureForUser = internalAction({
  args: { userId: v.id("users"), monthlyWalletCents: v.number() },
  handler: async (): Promise<{ ok: boolean; reason: string }> => ({
    ok: false,
    reason: "endpoint-direct-required",
  }),
});

export const disableForUser = internalAction({
  args: { userId: v.id("users") },
  handler: async (): Promise<{ ok: boolean; reason: string }> => ({
    ok: false,
    reason: "provider-revocation-requires-operator-cleanup",
  }),
});

export const syncUsageForUser = internalAction({
  args: { userId: v.id("users") },
  handler: async (): Promise<{ ok: boolean }> => ({ ok: false }),
});
