import { v } from "convex/values";
import { mutation, query, internalMutation, internalQuery } from "./_generated/server";
import { internal } from "./_generated/api";
import { isOwnerEmail, isOwnerUserId } from "./ownerAllowlist";

// The subscription that actually governs a user's billing right now.
// A user accumulates many subscription rows over time (renewals,
// decommissioned boxes, e2e test subs). The old `.first()` returned
// the OLDEST row by index order — so the Billing page could display a
// long-dead subscription. Pick deterministically: an `active` row
// wins; among equals, the most recently updated. null when none.
export const getByUser = query({
  args: { userId: v.id("users") },
  handler: async (ctx, { userId }) => {
    const rows = await ctx.db
      .query("subscriptions")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .collect();
    if (rows.length === 0) return null;
    return rows.sort((a, b) => {
      const activeDelta =
        (b.status === "active" ? 1 : 0) - (a.status === "active" ? 1 : 0);
      if (activeDelta !== 0) return activeDelta;
      return (b.updatedAt ?? b.createdAt ?? 0) - (a.updatedAt ?? a.createdAt ?? 0);
    })[0];
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

// Cancel a subscription by its Convex _id. Called when a user
// decommissions their managed box, or when reconcile sweeps up an
// orphaned paid sub: billing ends AND the reconcile recovery (which
// only acts on status==="active") will no longer resurrect the box.
// Idempotent. project_managed_cloud_onboarding_gap.
//
// This is a YAVER-INITIATED cancel, so it also cancels the
// subscription on LemonSqueezy itself — patching only the Convex row
// stops Yaver's reconcile but a real paying customer would keep being
// billed by LemonSqueezy until period end. The webhook-driven `cancel`
// path below intentionally does NOT do this (LemonSqueezy is already
// the originator there). Because the webhook runs `cancel` before it
// calls deprovision→cancelById, this guard short-circuits in that
// path — no double cancel.
export const cancelById = internalMutation({
  args: { subscriptionId: v.id("subscriptions") },
  handler: async (ctx, { subscriptionId }) => {
    const sub = await ctx.db.get(subscriptionId);
    if (!sub || sub.status === "cancelled") return false;
    await ctx.db.patch(subscriptionId, {
      status: "cancelled",
      cancelledAt: Date.now(),
      updatedAt: Date.now(),
    });
    if (sub.lemonSqueezyId) {
      await ctx.scheduler.runAfter(
        0,
        internal.http.cancelLemonSqueezySubscription,
        { lemonSqueezyId: sub.lemonSqueezyId },
      );
    }
    return true;
  },
});

// Active managed-cloud subscriptions (plan "yaver-cloud-*"). Used by
// the reconcile job: a paid sub MUST have a live box, else the user
// paid for nothing. project_managed_cloud_onboarding_gap (recovery).
export const listActiveManaged = internalQuery({
  args: {},
  handler: async (ctx) => {
    const rows = await ctx.db
      .query("subscriptions")
      .withIndex("by_status", (q) => q.eq("status", "active"))
      .collect();
    return rows
      .filter((s) =>
        typeof s.plan === "string" &&
        (s.plan.startsWith("yaver-cloud") || s.plan === "cloud-agent" || s.plan === "cloud-workspace")
      )
      .map((s) => ({
        subscriptionId: s._id,
        userId: s.userId,
        plan: s.plan,
      }));
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
      // email OR userId allowlist — OAuth owner accounts often have
      // no email, so id-based is the reliable owner bypass.
      if (user && (isOwnerEmail((user as any).email) || isOwnerUserId(String(user._id)))) {
        return true;
      }
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

// Persist an in-app plan switch (Cloud Agent ⇄ Cloud Workspace) on the
// user's governing subscription, immediately. The LemonSqueezy variant
// swap (so future renewals bill the new price) is handled separately by
// plans.changePlan; this just moves the local `plan` label that the
// entitlement + reconcile logic reads. Scoped to ONE user's own sub.
export const setPlan = internalMutation({
  args: { userId: v.id("users"), plan: v.string() },
  handler: async (ctx, { userId, plan }) => {
    const rows = await ctx.db
      .query("subscriptions")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .collect();
    const sub = rows
      .filter((s) => s.status === "active")
      .sort((a, b) => (b.updatedAt ?? b.createdAt ?? 0) - (a.updatedAt ?? a.createdAt ?? 0))[0];
    if (!sub) return null;
    await ctx.db.patch(sub._id, { plan, updatedAt: Date.now() });
    return { subscriptionId: sub._id, lemonSqueezyId: sub.lemonSqueezyId ?? null };
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
