// gatewayPolicy.ts — per-user limits for the Yaver Gateway, set ONLY by
// the operator (ownerAllowlist) and IMMUTABLE by the user.
//
// Why a dedicated table (not userSettings): userSettings is user-writable,
// so any limit kept there could be raised by the tenant. These rows live
// in their own table whose only writers are the operator-gated HTTP routes
// (/gateway/policy/set → setPolicyInternal). A tenant has no mutation that
// touches their own caps or re-enables a disabled account.
//
// Read by /gateway/authorize on every request: it returns the per-user
// ceilings + an allow/deny that folds in enabled, balance, and the daily
// cap. The Cloudflare Worker enforces the ceilings it receives.

import { v } from "convex/values";
import { internalQuery, internalMutation } from "./_generated/server";

function todayUTC(now: number): string {
  return new Date(now).toISOString().slice(0, 10); // YYYY-MM-DD
}

// One-call context for /gateway/authorize: the user's policy row (or null)
// plus how many REAL (non-dryRun) inference cents they've spent today, so
// the route can enforce a daily cap without a second round-trip.
export const getAuthContext = internalQuery({
  args: { userId: v.id("users"), now: v.optional(v.number()) },
  handler: async (ctx, { userId, now }) => {
    const policy = await ctx.db
      .query("gatewayPolicy")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .unique();

    const today = todayUTC(now ?? Date.now());
    const rows = await ctx.db
      .query("managedUsage")
      .withIndex("by_user_date", (q) => q.eq("userId", userId).eq("date", today))
      .collect();
    let spentTodayCents = 0;
    for (const r of rows) {
      if (r.kind === "inference" && !r.dryRun) spentTodayCents += r.chargedCents;
    }

    return {
      hasPolicy: policy !== null,
      enabled: policy ? policy.enabled : true, // no row → default-enabled (env ceilings still apply)
      dailyCapCents: policy?.dailyCapCents ?? 0,
      hourlyCapCents: policy?.hourlyCapCents ?? 0,
      maxTokensPerRequest: policy?.maxTokensPerRequest ?? 0,
      maxCentsPerRequest: policy?.maxCentsPerRequest ?? 0,
      freeGrantCents: policy?.freeGrantCents ?? 0,
      spentTodayCents,
    };
  },
});

export const getPolicyInternal = internalQuery({
  args: { userId: v.id("users") },
  handler: async (ctx, { userId }) => {
    return await ctx.db
      .query("gatewayPolicy")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .unique();
  },
});

// Operator-only (gated in the HTTP route by isOwner). Upserts the per-user
// policy. setBy is the operator's userId for audit.
export const setPolicyInternal = internalMutation({
  args: {
    userId: v.id("users"),
    enabled: v.optional(v.boolean()),
    dailyCapCents: v.optional(v.number()),
    hourlyCapCents: v.optional(v.number()),
    maxTokensPerRequest: v.optional(v.number()),
    maxCentsPerRequest: v.optional(v.number()),
    freeGrantCents: v.optional(v.number()),
    note: v.optional(v.string()),
    setBy: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const now = Date.now();
    const existing = await ctx.db
      .query("gatewayPolicy")
      .withIndex("by_user", (q) => q.eq("userId", args.userId))
      .unique();
    const patch = {
      enabled: args.enabled ?? existing?.enabled ?? true,
      dailyCapCents: args.dailyCapCents ?? existing?.dailyCapCents,
      hourlyCapCents: args.hourlyCapCents ?? existing?.hourlyCapCents,
      maxTokensPerRequest: args.maxTokensPerRequest ?? existing?.maxTokensPerRequest,
      maxCentsPerRequest: args.maxCentsPerRequest ?? existing?.maxCentsPerRequest,
      freeGrantCents: args.freeGrantCents ?? existing?.freeGrantCents,
      note: args.note ?? existing?.note,
      setBy: args.setBy ?? existing?.setBy,
      updatedAt: now,
    };
    if (existing) {
      await ctx.db.patch(existing._id, patch);
      return { ok: true, userId: args.userId, created: false };
    }
    await ctx.db.insert("gatewayPolicy", {
      userId: args.userId,
      createdAt: now,
      ...patch,
    });
    return { ok: true, userId: args.userId, created: true };
  },
});
