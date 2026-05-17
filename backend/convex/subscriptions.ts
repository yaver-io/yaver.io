import { v } from "convex/values";
import { mutation, query, internalMutation, internalQuery } from "./_generated/server";
import { isOwnerEmail } from "./ownerAllowlist";

// Get user's active subscription
export const getByUser = query({
  args: { userId: v.id("users") },
  handler: async (ctx, { userId }) => {
    return await ctx.db
      .query("subscriptions")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .first();
  },
});

export const getByLemonId = internalQuery({
  args: { lemonSqueezyId: v.string() },
  handler: async (ctx, { lemonSqueezyId }) => {
    return await ctx.db
      .query("subscriptions")
      .withIndex("by_lemon_id", (q) => q.eq("lemonSqueezyId", lemonSqueezyId))
      .first();
  },
});

// isActive is the fail-closed billing gate used before ANY managed
// Hetzner server is created. Returns true ONLY if the subscription
// row exists and status === "active". A signed LemonSqueezy webhook
// is the primary proof of payment; this query is defense-in-depth so
// no internal mis-trigger / replay can spend money on Yaver's Hetzner
// account without an active subscription. Default deny.
export const isActive = internalQuery({
  args: { subscriptionId: v.id("subscriptions") },
  handler: async (ctx, { subscriptionId }) => {
    const sub = await ctx.db.get(subscriptionId);
    return !!sub && sub.status === "active";
  },
});

// canProvisionManaged is the gate the managed-provision actions
// actually call. It passes if the subscription is active OR the
// owning user is on the owner allowlist (CLOUD_PREVIEW_OWNER_EMAIL
// env). The owner bypass lets the repo owner develop the full
// Hetzner create/remove flow without a LemonSqueezy subscription
// (the email is Convex ENV config, never hardcoded — see
// ownerAllowlist.ts). With the env unset this is exactly isActive
// (fail-closed for everyone), so deploying it changes nothing until
// the owner opts in.
export const canProvisionManaged = internalQuery({
  args: {
    subscriptionId: v.optional(v.id("subscriptions")),
    userId: v.optional(v.id("users")),
  },
  handler: async (ctx, { subscriptionId, userId }) => {
    if (subscriptionId) {
      const sub = await ctx.db.get(subscriptionId);
      if (sub && sub.status === "active") return true;
    }
    if (userId) {
      const user = await ctx.db.get(userId);
      if (user && isOwnerEmail((user as any).email)) return true;
    }
    return false;
  },
});

// Create or update subscription from LemonSqueezy webhook
export const upsertFromWebhook = internalMutation({
  args: {
    lemonSqueezyId: v.string(),
    lemonSqueezyCustomerId: v.string(),
    userId: v.id("users"),
    plan: v.string(),
    status: v.string(),
    currentPeriodEnd: v.number(),
  },
  handler: async (ctx, args) => {
    const existing = await ctx.db
      .query("subscriptions")
      .withIndex("by_lemon_id", (q) => q.eq("lemonSqueezyId", args.lemonSqueezyId))
      .first();

    if (existing) {
      await ctx.db.patch(existing._id, {
        status: args.status,
        plan: args.plan,
        currentPeriodEnd: args.currentPeriodEnd,
        updatedAt: Date.now(),
      });
      return existing._id;
    }

    return await ctx.db.insert("subscriptions", {
      userId: args.userId,
      lemonSqueezyId: args.lemonSqueezyId,
      lemonSqueezyCustomerId: args.lemonSqueezyCustomerId,
      plan: args.plan,
      status: args.status,
      currentPeriodEnd: args.currentPeriodEnd,
      createdAt: Date.now(),
      updatedAt: Date.now(),
    });
  },
});

// Cancel subscription
export const cancel = internalMutation({
  args: { lemonSqueezyId: v.string() },
  handler: async (ctx, { lemonSqueezyId }) => {
    const sub = await ctx.db
      .query("subscriptions")
      .withIndex("by_lemon_id", (q) => q.eq("lemonSqueezyId", lemonSqueezyId))
      .first();
    if (sub) {
      await ctx.db.patch(sub._id, {
        status: "cancelled",
        cancelledAt: Date.now(),
        updatedAt: Date.now(),
      });
      return sub._id;
    }
    return null;
  },
});

// Mark expired subscriptions (called by cron or webhook)
export const markExpired = internalMutation({
  args: {},
  handler: async (ctx) => {
    const now = Date.now();
    const gracePeriodMs = 7 * 24 * 60 * 60 * 1000; // 7 days
    const subs = await ctx.db.query("subscriptions")
      .withIndex("by_status", (q) => q.eq("status", "cancelled"))
      .collect();

    for (const sub of subs) {
      if (sub.currentPeriodEnd + gracePeriodMs < now) {
        await ctx.db.patch(sub._id, { status: "expired", updatedAt: now });
      }
    }
  },
});
