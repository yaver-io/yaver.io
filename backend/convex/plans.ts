// plans.ts — SINGLE SOURCE OF TRUTH for sellable plan entitlements, and
// the activation orchestrator that turns a paid LemonSqueezy webhook into
// real entitlements: included active-hours + gateway inference policy +
// a monthly wallet budget.
//
// Two doors (mirrors the checkout custom_data.tier the webhook already
// passes — http.ts subscription_created branch):
//   • "hosted"  — the all-in bundle: managed inference ON (we supply the
//                 model via the gateway), included hours + a monthly AI
//                 wallet budget. Higher price.
//   • "byok"    — bring-your-own-key: managed inference OFF (the user
//                 routes to their own provider via `yaver code set byok`),
//                 cloud box + included hours only. Cheaper.
//
// Everything is env-overridable so launch promos / margin retunes never
// need a redeploy. Money is integer RETAIL cents end-to-end — the wallet
// is denominated in what we CHARGE (the meter folds markup in already, see
// cloudLifecycle.recordUsageAndDeduct / managedMeter.recordManagedUsage).
// Hours are included active-hours/month and are NOT wallet-charged (the
// includedAllowance grant covers them before the wallet is ever touched).
//
// This module ADDS NO SCHEMA. It composes the existing isolated primitives
// (grantIncludedHours, setPolicyInternal, topUpForOrder) so the parallel
// owners of cloudLifecycle/gatewayPolicy keep their boundaries. The monthly
// wallet grant rides creditTopups' existing orderId idempotency keyed on
// (subscription, period) — re-fired webhook = no-op, new month = one credit.

import { internalAction } from "./_generated/server";
import { v } from "convex/values";
import { internal } from "./_generated/api";

export type PlanTier = "hosted" | "byok";

function num(envKey: string, dflt: number): number {
  const n = Number(process.env[envKey]);
  return Number.isFinite(n) && n >= 0 ? n : dflt;
}

export interface PlanEntitlements {
  tier: PlanTier;
  // Included active-hours/month (CPU box). NOT wallet-charged.
  includedHoursCpu: number;
  // Monthly wallet credit (RETAIL cents). For "hosted" this is the
  // included managed-AI budget; for both tiers it also covers any compute
  // overage past the included hours AND keeps the snapshot-reserve floor
  // (cloudLifecycle.minimumReserveCents) funded so a PAUSED box can resume
  // (canStart gates resume on wallet >= reserve). byok gets a small float
  // for exactly that reason; hosted gets the real AI budget on top.
  monthlyWalletCents: number;
  gateway: {
    enabled: boolean; // managed inference on? (false ⇒ user must BYOK)
    dailyCapCents: number; // hard daily COGS backstop (anti-abuse)
    hourlyCapCents: number; // rolling-hour burst cap (Worker-enforced)
    maxTokensPerRequest: number;
    maxCentsPerRequest: number;
  };
}

// Defaults chosen so the MEDIAN user is profitable and the cap bounds the
// whale (see the unit-economics table). Retune via env without a redeploy.
//   $19 hosted ≈ 40h included (~$4 COGS) + $8 retail AI (~$5.3 COGS) ⇒ ~50% margin
//   $9  byok   ≈ 40h included (~$4 COGS) only                       ⇒ ~55% margin
export function planEntitlements(tier: PlanTier): PlanEntitlements {
  if (tier === "byok") {
    return {
      tier,
      includedHoursCpu: num("YAVER_PLAN_BYOK_HOURS", 40),
      monthlyWalletCents: num("YAVER_PLAN_BYOK_WALLET_CENTS", 150),
      gateway: {
        enabled: false,
        dailyCapCents: 0,
        hourlyCapCents: 0,
        maxTokensPerRequest: 0,
        maxCentsPerRequest: 0,
      },
    };
  }
  return {
    tier: "hosted",
    includedHoursCpu: num("YAVER_PLAN_HOSTED_HOURS", 40),
    monthlyWalletCents: num("YAVER_PLAN_HOSTED_WALLET_CENTS", 800),
    gateway: {
      enabled: true,
      dailyCapCents: num("YAVER_PLAN_HOSTED_DAILY_CAP_CENTS", 300), // $3/day hard ceiling
      hourlyCapCents: num("YAVER_PLAN_HOSTED_HOURLY_CAP_CENTS", 80), // 80c/hr burst
      maxTokensPerRequest: num("YAVER_PLAN_HOSTED_MAX_TOKENS", 64000),
      maxCentsPerRequest: num("YAVER_PLAN_HOSTED_MAX_CENTS_REQ", 50),
    },
  };
}

function periodUTC(now: number): string {
  return new Date(now).toISOString().slice(0, 7); // "YYYY-MM"
}

// Apply a paid plan's entitlements. IDEMPOTENT per billing period — safe
// to call on subscription_created / _updated / _resumed and on every
// monthly renewal webhook. Composed of three existing primitives; adds no
// schema. Scheduled (runAfter 0) from the LemonSqueezy webhook so a slow
// or failing grant never blocks the 200 the webhook must return.
export const applyPlanEntitlements = internalAction({
  args: {
    userId: v.id("users"),
    subscriptionId: v.optional(v.string()),
    tier: v.union(v.literal("hosted"), v.literal("byok")),
    plan: v.string(),
    period: v.optional(v.string()),
  },
  handler: async (
    ctx,
    { userId, subscriptionId, tier, plan, period },
  ): Promise<{ ok: boolean; tier: PlanTier; period: string }> => {
    const e = planEntitlements(tier);
    const p = period || periodUTC(Date.now());

    // 1) Included active-hours. grantIncludedHours is idempotent per
    //    (user, period, type) and preserves usedSeconds on re-grant — a
    //    new calendar month auto-creates a fresh row (the monthly reset).
    await ctx.runMutation(internal.cloudLifecycle.grantIncludedHours, {
      userId,
      plan,
      machineType: "cpu",
      period: p,
      hours: e.includedHoursCpu,
      source: `plan:${tier}`,
    });

    // 2) Gateway inference policy — operator-set, user-immutable. hosted ⇒
    //    enabled with anti-abuse ceilings; byok ⇒ disabled (the user must
    //    route to their own key, so our gateway key is never spent for them).
    await ctx.runMutation(internal.gatewayPolicy.setPolicyInternal, {
      userId,
      enabled: e.gateway.enabled,
      dailyCapCents: e.gateway.dailyCapCents,
      hourlyCapCents: e.gateway.hourlyCapCents,
      maxTokensPerRequest: e.gateway.maxTokensPerRequest,
      maxCentsPerRequest: e.gateway.maxCentsPerRequest,
      note: `plan ${tier} (${plan})`,
      setBy: "plan-activation",
    });

    // 2b) Per-user OpenRouter key. hosted ⇒ ensure a key exists with its
    //     hard credit limit pinned to our COGS BUDGET (the retail AI wallet
    //     ÷ inference markup — NOT what the user paid). A per-user key
    //     spreads OpenRouter's per-key rate limit across the GLM provider
    //     pool and caps third-party spend below collected revenue. byok ⇒
    //     disable any existing key (managed inference off). Scheduled so a
    //     slow OpenRouter API call never blocks the webhook's 200.
    if (e.gateway.enabled) {
      await ctx.scheduler.runAfter(0, internal.openrouterKeys.ensureForUser, {
        userId,
        monthlyWalletCents: e.monthlyWalletCents,
      });
    } else {
      await ctx.scheduler.runAfter(0, internal.openrouterKeys.disableForUser, { userId });
    }

    // 3) Monthly wallet budget. Idempotent per (subscription, period) via
    //    creditTopups' orderId dedupe — re-fired webhook no-ops, a new
    //    month credits exactly once. NOTE: unspent budget currently rolls
    //    over (it is the user's prepaid credit). A non-rollover, separate
    //    inference-grant ledger is a deliberate P2 refinement; the daily
    //    cap is the hard COGS backstop until then.
    if (e.monthlyWalletCents > 0) {
      const orderId = `sub-allowance-${subscriptionId ?? userId}-${p}`;
      await ctx.runMutation(internal.cloudLifecycle.topUpForOrder, {
        userId,
        orderId,
        amountCents: e.monthlyWalletCents,
        source: "subscription-allowance",
        packId: `plan:${tier}`,
      });
    }

    return { ok: true, tier, period: p };
  },
});

// Revoke managed-inference entitlement on cancel/expiry. Disables the
// gateway policy so a lapsed subscriber can't keep spending our gateway
// key on leftover wallet balance (the box itself is torn down by the
// webhook's cancel branch). We do NOT claw back the prepaid wallet —
// that is the user's purchased credit; managed inference is simply gated
// behind an active subscription. Included-hours allowance naturally
// lapses at the next billing period (no new grant fires). Idempotent.
export const revokePlanEntitlements = internalAction({
  args: { userId: v.id("users") },
  handler: async (ctx, { userId }): Promise<{ ok: boolean }> => {
    await ctx.runMutation(internal.gatewayPolicy.setPolicyInternal, {
      userId,
      enabled: false,
      note: "subscription cancelled/expired — managed inference revoked",
      setBy: "plan-activation",
    });
    // Disable the per-user OpenRouter key at the provider AND drop it from
    // the gateway KV, so a lapsed subscriber can't keep spending even if a
    // gateway-policy check were ever bypassed (defense in depth).
    await ctx.scheduler.runAfter(0, internal.openrouterKeys.disableForUser, { userId });
    return { ok: true };
  },
});

// In-app plan switch (Cloud Agent ⇄ Cloud Workspace) initiated by the
// user from the dashboard — NOT a webhook. Orchestrates three things:
//   1. LemonSqueezy variant swap so the next renewal bills the new price.
//   2. The local subscription `plan` label.
//   3. Entitlements, applied IMMEDIATELY (the chosen UX: a downgrade cuts
//      managed AI off now; an upgrade turns it on now).
//
// DIRECTION-ASYMMETRIC for safety (this orchestrator is reached only via
// the authed /billing/change-plan route, which scopes to the caller's own
// subscription):
//   • DOWNGRADE (→byok): fail-SAFE. Apply byok entitlements immediately
//     (disable gateway + OpenRouter key) even if the LemonSqueezy swap is
//     unconfigured or fails — the user simply gets LESS, never free more.
//     The prepaid wallet is preserved (we never claw back paid credit);
//     the monthly allowance is idempotent per period so no double-grant.
//   • UPGRADE (→hosted): fail-CLOSED. Grant managed AI ONLY if the
//     LemonSqueezy variant swap succeeded (i.e. the user will actually be
//     billed). If billing isn't wired, refuse — never hand out a paid
//     OpenRouter key without a charge.
export const changePlan = internalAction({
  args: {
    userId: v.id("users"),
    lemonSqueezyId: v.optional(v.string()),
    targetPlan: v.union(v.literal("cloud-agent"), v.literal("cloud-workspace")),
  },
  handler: async (
    ctx,
    { userId, lemonSqueezyId, targetPlan },
  ): Promise<{ ok: boolean; tier: PlanTier; billingSynced: boolean; reason?: string }> => {
    const tier: PlanTier = targetPlan === "cloud-agent" ? "hosted" : "byok";

    // 1) Try to move billing first (matters for BOTH directions, but is a
    //    hard precondition only for the upgrade).
    let billingSynced = false;
    if (lemonSqueezyId) {
      const swap = await ctx.runAction(internal.http.updateLemonSqueezyVariant, {
        lemonSqueezyId,
        tier,
      });
      billingSynced = swap.ok;
    }

    if (tier === "hosted" && !billingSynced) {
      // Refuse to grant paid managed inference without a billing change.
      return { ok: false, tier, billingSynced, reason: "billing-not-synced" };
    }

    // 2) Local plan label (immediate; reconcile + entitlements read this).
    await ctx.runMutation(internal.subscriptions.setPlan, { userId, plan: targetPlan });

    // 3) Entitlements now. applyPlanEntitlements is idempotent per period:
    //    the monthly wallet grant dedupes on (subscription, period) so a
    //    toggle can't farm credit, and byok ⇒ gateway off + OpenRouter key
    //    disabled (the immediate cutoff), hosted ⇒ key ensured + gateway on.
    await ctx.runAction(internal.plans.applyPlanEntitlements, {
      userId,
      subscriptionId: lemonSqueezyId,
      tier,
      plan: targetPlan,
    });

    return { ok: true, tier, billingSynced };
  },
});
