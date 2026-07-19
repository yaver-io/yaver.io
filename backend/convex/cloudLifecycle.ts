// Managed-cloud prepaid wallet + metered stop/start ledger.
//
// ISOLATED MODULE — owns prepaidCredits + creditUsage (schema.ts).
// It reads cloudMachines via the existing read-only internalQuery
// (internal.cloudMachines.getInternal); it NEVER edits cloudMachines.ts
// / subscriptions.ts (parallel session owns those). Lifecycle state
// transitions (paused/resuming/suspended) are plain v.string() values
// set through that module's existing patch boundary from P2 — not here.
//
// Money is integer cents end-to-end (no float drift). chargedCents =
// MARKUP_X * hetznerCostCents in BOTH live and stopped states (100%
// margin). `dryRun` (default true unless an owner opt-in flips it)
// means simulate: ledger rows written with dryRun:true, balance still
// moves so the UX is real, but no real Hetzner spend is implied — the
// agent-side stop/start (P1) stays dry-run too. Free-at-launch /
// fail-closed posture per project_business_model.
//
// Privacy: every field here is a counter/timestamp/id — Convex-allowed
// (runnerUsage/dailyTaskCounts precedent). No secrets/paths/output.

import { internalMutation, internalQuery, internalAction } from "./_generated/server";
import { v } from "convex/values";
import { internal } from "./_generated/api";
import { isOwnerUserId } from "./ownerAllowlist";
import {
  cloudWorkspaceProfilePolicy,
  includedHoursForCloudWorkspaceProfile,
} from "./cloudPlacementCapacity";

function normalizeMachineType(value: string | undefined | null): string {
  const t = String(value || "").trim().toLowerCase();
  if (t === "standard" || t === "heavy" || t === "build" || t === "cpu" || t === "gpu") return t;
  return "standard";
}

// Markup over raw provider COGS, per internal SKU, env-overridable. Defaults
// keep at least 40% margin for the flat plan if included-hour grants are tuned
// conservatively. Set YAVER_CLOUD_MARKUP_STANDARD / _HEAVY / _BUILD / _CPU /
// _GPU to retune without a redeploy.
const MARKUP_BY_TYPE: Record<string, number> = { standard: 2, heavy: 2.3, build: 2.5, cpu: 2, gpu: 3 };
export function markup(machineType: string): number {
  const type = normalizeMachineType(machineType);
  const env = Number(process.env[`YAVER_CLOUD_MARKUP_${type.toUpperCase()}`]);
  if (Number.isFinite(env) && env > 0) return env;
  return MARKUP_BY_TYPE[type] ?? 2;
}
// Back-compat default for any external reference. Prefer markup().
export const MARKUP_X = MARKUP_BY_TYPE.standard;

// Raw Hetzner COGS basis. Managed SKU = cpx51 €54.90/mo (16 vCPU/32 GB,
// the large-monorepo box — see MACHINE_SPECS in cloudMachines.ts).
// Stopped = snapshot storage only (~€0.80/mo for the larger image, still
// rounds to ~0c/h). Cents/hour; monthly ÷ 730. ⚠️ Keep this in sync with
// MACHINE_SPECS.cpu.hetznerType and re-verify the price with
// GET /v1/server_types before HCLOUD_TOKEN goes live. Region/type
// variance can be passed as an explicit rate later — conservative
// defaults here.
const DEFAULT_HETZNER_COST_CENTS_PER_HOUR: Record<string, { live: number; stopped: number }> = {
  standard: { live: 2, stopped: Math.round((40 / 730)) },  // CX32-ish 8GB profile
  heavy: { live: 4, stopped: Math.round((60 / 730)) },     // CX42-ish 16GB profile
  build: { live: 8, stopped: Math.round((80 / 730)) },     // CX52-ish 32GB profile
  // €54.90/mo ≈ 752 c/mo ... (USD ~ ; we bill USD-cents, treat €≈$ for
  // the wallet — exact FX is a P6/top-up concern, not the meter).
  cpu: { live: Math.round((5490 / 730)), stopped: Math.round((80 / 730)) },    // ~7.5c/h live, ~0c/h stopped
  gpu: { live: Math.round((19900 / 730)), stopped: Math.round((100 / 730)) },  // GPU tier placeholder
};

function rawRate(machineType: string, state: "live" | "stopped"): number {
  const type = normalizeMachineType(machineType);
  const env = Number(process.env[`YAVER_CLOUD_COST_${type.toUpperCase()}_${state.toUpperCase()}_CPH`]);
  if (Number.isFinite(env) && env >= 0) return env;
  const r = DEFAULT_HETZNER_COST_CENTS_PER_HOUR[type] ?? DEFAULT_HETZNER_COST_CENTS_PER_HOUR.standard;
  return state === "stopped" ? r.stopped : r.live;
}

// Two-part minimum prepaid floor (cents): enough to (a) safely execute
// one live→stop snapshot transition + (b) keep the snapshot parked
// ≥1 month. Pure fn — P2/P3 gate "can start" on balance >= this.
export function minimumReserveCents(machineType: string): number {
  const m = markup(machineType);
  const stoppedMonth = rawRate(machineType, "stopped") * 730 * m;
  // Transition reserve: assume up to ~1h of live billing to snapshot+
  // delete safely (snapshot can take minutes; be generous).
  const transition = rawRate(machineType, "live") * 1 * m;
  return Math.ceil(stoppedMonth + transition);
}

// User-facing running rate (cents/hour) for a SKU = raw live COGS x
// markup. Pure; the wallet UI shows "~$X/hr running".
export function estimatedHourlyCents(machineType: string): number {
  return Math.ceil(rawRate(machineType, "live") * markup(machineType));
}

function todayUTC(now: number): string {
  return new Date(now).toISOString().slice(0, 10);
}

// Billing-period key for the included-hours allowance. Calendar month
// (UTC) by default; a subscription renewal webhook may instead pass an
// explicit anniversary key to grantIncludedHours.
function billingPeriodUTC(now: number): string {
  return new Date(now).toISOString().slice(0, 7); // "YYYY-MM"
}

// ── Subscription included hours (base + metered overage) ─────────────
// Each paid plan grants this many ACTIVE hours per billing period, PER
// machineType, BEFORE the prepaid wallet (overage) is charged. The
// included grant makes the monthly price feel calm ("40 hrs included");
// overage past it is metered from the prepaid wallet at markup x raw and
// auto-stops when the wallet can no longer afford the rate + snapshot
// reserve — so neither CPU nor GPU ever runs compute we can't bill.
// GPU defaults to 0 included (pure prepaid overage) unless a GPU plan
// grants some. Env-overridable for launch promos without a redeploy:
//   YAVER_CLOUD_INCLUDED_HOURS_CLOUD_AGENT_CPU=40
//   YAVER_CLOUD_INCLUDED_HOURS_CLOUD_WORKSPACE_GPU=2
// 0 / unknown plan ⇒ pure pay-as-you-go (legacy behaviour, unchanged).
const INCLUDED_HOURS: Record<string, Record<string, number>> = {
  "cloud-agent": { cpu: 40, standard: 40, heavy: 20, build: 10, gpu: 0 },
  "cloud-workspace": {
    standard: includedHoursForCloudWorkspaceProfile("standard"),
    heavy: includedHoursForCloudWorkspaceProfile("heavy"),
    build: includedHoursForCloudWorkspaceProfile("build"),
    cpu: includedHoursForCloudWorkspaceProfile("build"),
    gpu: 0,
  },
};
export function includedHoursForPlan(plan: string, machineType: string): number {
  const t = normalizeMachineType(machineType);
  const envKey = `YAVER_CLOUD_INCLUDED_HOURS_${(plan || "").toUpperCase().replace(/-/g, "_")}_${t.toUpperCase()}`;
  const env = Number(process.env[envKey]);
  if (Number.isFinite(env) && env >= 0) return env;
  return INCLUDED_HOURS[plan]?.[t] ?? 0;
}

function isCloudWorkspaceAllowancePlan(plan: string | null | undefined): boolean {
  const value = String(plan || "");
  return value === "cloud-workspace" || value.startsWith("yaver-cloud");
}

export function cloudWorkspaceCreditWeightForMachineType(machineType: string): number {
  const type = normalizeMachineType(machineType);
  if (type === "gpu") return 0;
  if (type === "build" || type === "cpu") return cloudWorkspaceProfilePolicy("build").standardCreditWeight;
  if (type === "heavy") return cloudWorkspaceProfilePolicy("heavy").standardCreditWeight;
  return cloudWorkspaceProfilePolicy("standard").standardCreditWeight;
}

export function weightedIncludedCoverage(args: {
  seconds: number;
  usedStandardCreditSeconds: number;
  includedStandardCreditSeconds: number;
  creditWeight: number;
}): {
  coveredSeconds: number;
  usedStandardCreditSeconds: number;
  remainingStandardCreditSeconds: number;
} {
  const seconds = Math.max(0, args.seconds);
  const weight = Math.max(0, args.creditWeight);
  const left = Math.max(0, args.includedStandardCreditSeconds - args.usedStandardCreditSeconds);
  if (seconds <= 0 || weight <= 0 || left <= 0) {
    return {
      coveredSeconds: 0,
      usedStandardCreditSeconds: 0,
      remainingStandardCreditSeconds: left,
    };
  }
  const coveredSeconds = Math.min(seconds, left / weight);
  const usedStandardCreditSeconds = Math.ceil(coveredSeconds * weight);
  return {
    coveredSeconds,
    usedStandardCreditSeconds,
    remainingStandardCreditSeconds: Math.max(0, left - usedStandardCreditSeconds),
  };
}

export function includedAllowanceCoversStart(args: {
  machineType: string;
  remainingStandardCreditSeconds: number;
  minimumLiveSeconds?: number;
}): boolean {
  const weight = cloudWorkspaceCreditWeightForMachineType(args.machineType);
  if (weight <= 0) return false;
  const minimumLiveSeconds = Math.max(1, args.minimumLiveSeconds ?? 3600);
  const remaining = Math.max(0, args.remainingStandardCreditSeconds);
  return remaining >= Math.ceil(minimumLiveSeconds * weight);
}

// ── Wallet ───────────────────────────────────────────────────────────

// internalQuery (not public): wallet balance is per-user money data.
// A public query taking a userId arg would let any Convex client read
// any user's balance by id. All reads go through the bearer-authed HTTP
// endpoints, which resolve userId from the session and call this via
// internal.* — never client-reachable.
export const getWallet = internalQuery({
  args: { userId: v.id("users") },
  handler: async (ctx, { userId }) => {
    const w = await ctx.db
      .query("prepaidCredits")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .unique();
    if (!w) {
      return { balanceCents: 0, totalAddedCents: 0, totalUsedCents: 0, currency: "usd", exists: false };
    }
    return {
      balanceCents: w.balanceCents,
      totalAddedCents: w.totalAddedCents,
      totalUsedCents: w.totalUsedCents,
      currency: w.currency,
      lastTopupAt: w.lastTopupAt,
      lastMeteredAt: w.lastMeteredAt,
      exists: true,
    };
  },
});

export const getWalletInternal = internalQuery({
  args: { userId: v.id("users") },
  handler: async (ctx, { userId }) =>
    ctx.db
      .query("prepaidCredits")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .unique(),
});

async function ensureWalletRow(ctx: any, userId: string) {
  const existing = await ctx.db
    .query("prepaidCredits")
    .withIndex("by_user", (q: any) => q.eq("userId", userId))
    .unique();
  if (existing) return existing;
  const now = Date.now();
  const id = await ctx.db.insert("prepaidCredits", {
    userId,
    balanceCents: 0,
    totalAddedCents: 0,
    totalUsedCents: 0,
    currency: "usd",
    createdAt: now,
    updatedAt: now,
  });
  return await ctx.db.get(id);
}

// Internal wallet funding primitive. Current flat products do NOT sell
// customer credit packs; this is used by owner-dev tooling and subscription
// allowance grants that keep the reserve floor funded.
export const topUp = internalMutation({
  args: {
    userId: v.id("users"),
    amountCents: v.number(),
    subscriptionId: v.optional(v.id("subscriptions")),
  },
  handler: async (ctx, { userId, amountCents, subscriptionId }) => {
    if (amountCents <= 0) throw new Error("topUp: amountCents must be > 0");
    const w = await ensureWalletRow(ctx, userId);
    const now = Date.now();
    await ctx.db.patch(w._id, {
      balanceCents: w.balanceCents + amountCents,
      totalAddedCents: w.totalAddedCents + amountCents,
      subscriptionId: subscriptionId ?? w.subscriptionId,
      lastTopupAt: now,
      updatedAt: now,
    });
    return { balanceCents: w.balanceCents + amountCents };
  },
});

// Idempotent wallet grant. Current callers are subscription allowances and
// owner/dev repair tooling; legacy LemonSqueezy credit-pack webhooks are
// ignored for the flat product model. We still key on orderId in creditTopups
// so replayed allowance grants can never double-credit the wallet. Returns the
// (possibly unchanged) balance.
export const topUpForOrder = internalMutation({
  args: {
    userId: v.id("users"),
    orderId: v.string(),
    amountCents: v.number(),
    source: v.string(),
    packId: v.optional(v.string()),
  },
  handler: async (
    ctx,
    { userId, orderId, amountCents, source, packId },
  ): Promise<{ balanceCents: number; credited: boolean }> => {
    if (amountCents <= 0) throw new Error("topUpForOrder: amountCents must be > 0");
    const existing = await ctx.db
      .query("creditTopups")
      .withIndex("by_order", (q) => q.eq("orderId", orderId))
      .first();
    if (existing) {
      const w = await getWalletInternalRow(ctx, userId);
      return { balanceCents: w?.balanceCents ?? 0, credited: false };
    }
    const now = Date.now();
    await ctx.db.insert("creditTopups", {
      userId,
      orderId,
      source,
      packId,
      amountCents,
      createdAt: now,
    });
    const w = await ensureWalletRow(ctx, userId);
    await ctx.db.patch(w._id, {
      balanceCents: w.balanceCents + amountCents,
      totalAddedCents: w.totalAddedCents + amountCents,
      lastTopupAt: now,
      updatedAt: now,
    });
    return { balanceCents: w.balanceCents + amountCents, credited: true };
  },
});

// Recent wallet activity for the mobile/web "Wallet" surface: the last
// N metering ticks (most recent first) + the last N top-ups. Counter/
// id/timestamp only.
// internalQuery (not public): per-user usage ledger. Same reasoning as
// getWallet — read only via the bearer-authed HTTP /usage endpoint.
export const getRecentUsage = internalQuery({
  args: { userId: v.id("users"), limit: v.optional(v.number()) },
  handler: async (ctx, { userId, limit }) => {
    const n = Math.max(1, Math.min(100, limit ?? 20));
    const usage = await ctx.db
      .query("creditUsage")
      .withIndex("by_user_date", (q) => q.eq("userId", userId))
      .order("desc")
      .take(n);
    const topups = await ctx.db
      .query("creditTopups")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .order("desc")
      .take(n);
    return {
      usage: usage.map((u) => ({
        machineId: u.machineId ? String(u.machineId) : null,
        date: u.date,
        state: u.state,
        seconds: u.seconds,
        chargedCents: u.chargedCents,
        ratePerHourCents: u.ratePerHourCents,
        dryRun: u.dryRun,
        createdAt: u.createdAt,
      })),
      topups: topups.map((t) => ({
        orderId: t.orderId,
        source: t.source,
        packId: t.packId ?? null,
        amountCents: t.amountCents,
        createdAt: t.createdAt,
      })),
    };
  },
});

// ── Included-hours allowance (subscription base) ─────────────────────

// Grant (or refresh) a plan's included active-hours for a billing period
// + machineType. Idempotent per (userId, period, machineType): re-grant
// of the SAME period sets the included ceiling and PRESERVES usedSeconds
// (a mid-period plan change moves the ceiling without refunding spent
// time). A NEW period auto-creates a fresh row (usedSeconds 0) = the
// monthly reset. Call from the subscription activation/renewal webhook
// (P6, http.ts) or owner-dev tooling — one line, no edit to this file:
//   internal.cloudLifecycle.grantIncludedHours({ userId, plan })        // cpu, calendar month
//   internal.cloudLifecycle.grantIncludedHours({ userId, plan, machineType: "gpu", hours: 2 })
export const grantIncludedHours = internalMutation({
  args: {
    userId: v.id("users"),
    plan: v.string(),
    machineType: v.optional(v.string()),
    period: v.optional(v.string()),
    hours: v.optional(v.number()),
    source: v.optional(v.string()),
  },
  handler: async (ctx, { userId, plan, machineType, period, hours, source }) => {
    const now = Date.now();
    const type = normalizeMachineType(machineType);
    const p = period || billingPeriodUTC(now);
    const h = hours ?? includedHoursForPlan(plan, type);
    const includedSeconds = Math.max(0, Math.round(h * 3600));
    const existing = await ctx.db
      .query("includedAllowance")
      .withIndex("by_user_period_type", (q) =>
        q.eq("userId", userId).eq("period", p).eq("machineType", type),
      )
      .unique();
    if (existing) {
      await ctx.db.patch(existing._id, {
        plan, includedSeconds, source: source ?? existing.source, updatedAt: now,
      });
      return { period: p, machineType: type, includedSeconds, usedSeconds: existing.usedSeconds };
    }
    await ctx.db.insert("includedAllowance", {
      userId, period: p, machineType: type, plan,
      includedSeconds, usedSeconds: 0,
      source: source ?? "subscription", createdAt: now, updatedAt: now,
    });
    return { period: p, machineType: type, includedSeconds, usedSeconds: 0 };
  },
});

// Current-period included-hours snapshot for one machineType. Drives the
// "X of 40 hrs left" fuel-gauge in the wallet UI + the entitlements
// query. internalQuery (per-user data) — read via the bearer-authed HTTP
// /billing balance endpoint, same as getWallet.
export const getAllowance = internalQuery({
  args: { userId: v.id("users"), machineType: v.optional(v.string()), period: v.optional(v.string()) },
  handler: async (ctx, { userId, machineType, period }) => {
    const type = normalizeMachineType(machineType);
    const p = period || billingPeriodUTC(Date.now());
    const row = await ctx.db
      .query("includedAllowance")
      .withIndex("by_user_period_type", (q) =>
        q.eq("userId", userId).eq("period", p).eq("machineType", type),
      )
      .unique();
    const includedSeconds = row?.includedSeconds ?? 0;
    const usedSeconds = row?.usedSeconds ?? 0;
    return {
      period: p,
      machineType: type,
      plan: row?.plan ?? null,
      includedSeconds,
      usedSeconds,
      remainingSeconds: Math.max(0, includedSeconds - usedSeconds),
      exists: !!row,
    };
  },
});

// ── Metering ─────────────────────────────────────────────────────────

async function markGuestComputePaidUsage(ctx: any, userId: any, now: number) {
  const rows = await ctx.db
    .query("guestConversions")
    .withIndex("by_guest", (q: any) => q.eq("guestUserId", userId))
    .collect();
  for (const row of rows) {
    const enabled = new Set<string>(Array.isArray(row.enabledServices) ? row.enabledServices : []);
    enabled.add("compute");
    await ctx.db.patch(row._id, {
      enabledServices: Array.from(enabled).sort(),
      ...(row.firstPaidUsageAt ? {} : { firstPaidUsageAt: now }),
      ...(row.convertedAt ? {} : { convertedAt: now }),
      conversionState: "paid-usage",
      updatedAt: now,
    });
  }
}

// Record one billable tick for a machine and deduct from the wallet.
// Append-only creditUsage row + balance decrement (clamped at 0).
// Returns the new balance + whether it dropped below the safe floor
// (caller — P2 cron — auto-stops BEFORE zero using this signal).
export const recordUsageAndDeduct = internalMutation({
  args: {
    userId: v.id("users"),
    machineId: v.optional(v.id("cloudMachines")),
    machineType: v.string(),
    state: v.union(v.literal("live"), v.literal("stopped")),
    seconds: v.number(),
    dryRun: v.boolean(),
  },
  handler: async (
    ctx,
    { userId, machineId, machineType, state, seconds, dryRun },
  ): Promise<{ balanceCents: number; suspend: boolean; charged: number; coveredSeconds: number }> => {
    if (seconds <= 0) {
      const w0 = await getWalletInternalRow(ctx, userId);
      return { balanceCents: w0?.balanceCents ?? 0, suspend: false, charged: 0, coveredSeconds: 0 };
    }
    const now = Date.now();
    const m = markup(machineType);
    const rateHour = rawRate(machineType, state);

    // Included allowance first (live only). Current Cloud Workspace uses ONE
    // standard-credit pool: standard=1x, heavy=2x, build/cpu=4x. Legacy plans
    // keep their old per-machineType active-hour rows. A user with no
    // allowance row (pay-as-you-go) covers 0 seconds, so everything below is
    // byte-identical to the wallet path. Stopped ticks never draw the grant.
    let coveredSeconds = 0;
    let remainingIncluded = 0;
    if (state === "live") {
      const period = billingPeriodUTC(now);
      const type = normalizeMachineType(machineType);
      const standardAllow = await ctx.db
        .query("includedAllowance")
        .withIndex("by_user_period_type", (q: any) =>
          q.eq("userId", userId).eq("period", period).eq("machineType", "standard"),
        )
        .unique();
      const useSharedStandardCreditPool =
        !!standardAllow &&
        isCloudWorkspaceAllowancePlan(standardAllow.plan) &&
        cloudWorkspaceCreditWeightForMachineType(type) > 0;
      const allow = useSharedStandardCreditPool ? standardAllow : await ctx.db
        .query("includedAllowance")
        .withIndex("by_user_period_type", (q: any) =>
          q.eq("userId", userId).eq("period", period).eq("machineType", type),
        )
        .unique();
      if (allow) {
        const weight = useSharedStandardCreditPool ? cloudWorkspaceCreditWeightForMachineType(type) : 1;
        const coverage = weightedIncludedCoverage({
          seconds,
          usedStandardCreditSeconds: allow.usedSeconds,
          includedStandardCreditSeconds: allow.includedSeconds,
          creditWeight: weight,
        });
        coveredSeconds = coverage.coveredSeconds;
        if (coveredSeconds > 0) {
          await ctx.db.patch(allow._id, {
            usedSeconds: allow.usedSeconds + coverage.usedStandardCreditSeconds,
            updatedAt: now,
          });
        }
        remainingIncluded = useSharedStandardCreditPool
          ? coverage.remainingStandardCreditSeconds / weight
          : coverage.remainingStandardCreditSeconds;
      }
    }

    // Only the seconds NOT covered by the included grant hit the wallet.
    const billableSeconds = seconds - coveredSeconds;
    const hetznerCostCents = Math.ceil((rateHour * billableSeconds) / 3600);
    const chargedCents = hetznerCostCents * m;

    await ctx.db.insert("creditUsage", {
      userId,
      machineId,
      date: todayUTC(now),
      state,
      seconds, // full tick duration (audit); chargedCents = overage only
      hetznerCostCents,
      chargedCents,
      ratePerHourCents: rateHour * m,
      dryRun,
      createdAt: now,
    });

    const w = await ensureWalletRow(ctx, userId);
    const newBalance = Math.max(0, w.balanceCents - chargedCents);
    await ctx.db.patch(w._id, {
      balanceCents: newBalance,
      totalUsedCents: w.totalUsedCents + chargedCents,
      lastMeteredAt: now,
      updatedAt: now,
    });

    if (!dryRun && chargedCents > 0) {
      await markGuestComputePaidUsage(ctx, userId, now);
    }

    // Auto-stop signal. A subscriber with included hours REMAINING never
    // suspends — the next tick is free. Once the grant is exhausted, the
    // overage wallet must keep ≥ the snapshot-transition reserve or we
    // force-stop the box while we still can afford to park it safely.
    // Pay-as-you-go users have remainingIncluded 0 ⇒ identical to the
    // legacy floor check. This is the no-risk guarantee for BOTH cpu and
    // gpu: compute only runs while included OR prepaid can pay for it.
    const floor = state === "live" ? minimumReserveCents(machineType) : 0;
    const outOfIncluded = remainingIncluded <= 0;
    const suspend = state === "live" && outOfIncluded && newBalance <= floor;
    return { balanceCents: newBalance, suspend, charged: chargedCents, coveredSeconds };
  },
});

async function getWalletInternalRow(ctx: any, userId: string) {
  return ctx.db
    .query("prepaidCredits")
    .withIndex("by_user", (q: any) => q.eq("userId", userId))
    .unique();
}

// Pure preflight: may a machine of this type START given the wallet?
// (P3 route + P2 resume gate call this BEFORE provisioning/resuming so
// we never start a box the wallet can't afford to safely stop.)
export const canStart = internalQuery({
  args: { userId: v.id("users"), machineType: v.string() },
  handler: async (
    ctx,
    { userId, machineType },
  ): Promise<{
    ok: boolean;
    balanceCents: number;
    requiredCents: number;
    coveredByIncludedAllowance?: boolean;
    remainingIncludedSeconds?: number;
  }> => {
    const w = await getWalletInternalRow(ctx, userId);
    const need = minimumReserveCents(machineType);
    const have = w?.balanceCents ?? 0;
    const type = normalizeMachineType(machineType);
    if (cloudWorkspaceCreditWeightForMachineType(type) > 0) {
      const period = billingPeriodUTC(Date.now());
      const standardAllow = await ctx.db
        .query("includedAllowance")
        .withIndex("by_user_period_type", (q: any) =>
          q.eq("userId", userId).eq("period", period).eq("machineType", "standard"),
        )
        .unique();
      if (standardAllow && isCloudWorkspaceAllowancePlan(standardAllow.plan)) {
        const remainingStandardCreditSeconds = Math.max(
          0,
          standardAllow.includedSeconds - standardAllow.usedSeconds,
        );
        if (includedAllowanceCoversStart({
          machineType: type,
          remainingStandardCreditSeconds,
        })) {
          return {
            ok: true,
            balanceCents: have,
            requiredCents: need,
            coveredByIncludedAllowance: true,
            remainingIncludedSeconds:
              remainingStandardCreditSeconds / cloudWorkspaceCreditWeightForMachineType(type),
          };
        }
      }
    }
    return { ok: have >= need, balanceCents: have, requiredCents: need };
  },
});

// ── Lifecycle state machine + meter (P2) ─────────────────────────────
//
// pause/resume are FULLY HETZNER-INTEGRATED here, Convex-side, using
// the platform token (managed cloud = process.env.HCLOUD_TOKEN per
// project_managed_vs_byo_hetzner; the agent-side cloud_stop/cloud_start
// in P1 is the separate BYO path). MONEY-SAFETY / no-real-spend posture
// is FAIL-CLOSED BY CONSTRUCTION: prod has no HCLOUD_TOKEN, so every
// real Hetzner call is skipped and the flow runs as a dry-run state
// transition. Set HCLOUD_TOKEN (owner, deliberately) to go live —
// exactly the same gate cloudMachines.ts:1388 destroy uses. State is
// set through the parallel session's EXISTING
// internal.cloudMachines.setStatus / setProvisioned (zero edits to
// their file). cron wiring is a deliberate ONE-LINER left for whoever
// owns crons.ts (avoid a shared-file edit mid-collision):
//   crons.interval("cloud meter", { hours: 1 },
//     internal.cloudLifecycle.meterTick, { intervalSeconds: 3600 });

// Explicit result types so Convex's generated `internal.cloudLifecycle`
// API type doesn't recurse (TS7022) — every exported fn that
// references internal.* must NOT have an inferred return type.
type LifecycleResult = {
  ok: boolean;
  status?: string;
  dryRun?: boolean;
  reason?: string;
  /** True when the failure is transient (e.g. the provider is still finalizing
   *  the snapshot) and the wake will retry itself — the caller should show
   *  "waking, hang on" rather than a fatal error. */
  retryable?: boolean;
  snapshotId?: string;
  serverId?: string;
  providerActionId?: string;
  ip?: string;
  balanceCents?: number;
  requiredCents?: number;
};
type MeterResult = { metered: number; suspended: number; dryRun: boolean };
type IdleSweepResult = { checked: number; paused: number; enabled: boolean; dryRun: boolean };

const HETZNER_API = "https://api.hetzner.cloud/v1";

function hetznerServerType(machineType: string): string {
  const type = normalizeMachineType(machineType);
  const env = process.env[`YAVER_CLOUD_${type.toUpperCase()}_TYPE`];
  if (env) return env;
  if (type === "standard") return "cx32";
  if (type === "heavy") return "cx42";
  if (type === "build") return "cx52";
  if (type === "cpu") return process.env.YAVER_CLOUD_CPU_TYPE || "cpx51"; // legacy 32GB monorepo SKU
  return process.env.YAVER_CLOUD_GPU_TYPE || "gex44";
}

// Smallest Hetzner CPX type whose disk can hold a snapshot of `diskGb`. Used
// as a resume fallback for boxes provisioned before serverType was recorded:
// a snapshot only restores onto a server type with disk >= the source disk, so
// we must pick a type big enough regardless of the (possibly downsized) global
// default. Returns undefined when the disk is unknown so the caller falls
// through to the machineType default.
function hetznerServerTypeForDisk(diskGb: number | undefined): string | undefined {
  if (!diskGb || diskGb <= 0) return undefined;
  // Hetzner CPX (AMD, shared) disk sizes, ascending.
  const ladder: Array<{ type: string; disk: number }> = [
    { type: "cpx11", disk: 40 },
    { type: "cpx21", disk: 80 },
    { type: "cpx31", disk: 160 },
    { type: "cpx41", disk: 240 },
    { type: "cpx51", disk: 360 },
  ];
  for (const rung of ladder) {
    if (rung.disk >= diskGb) return rung.type;
  }
  return "cpx51"; // biggest CPX; larger snapshots need a bigger family (rare)
}
function hetznerLocation(region: string | undefined): string {
  return region === "us" ? "ash" : "nbg1";
}

// Tenant-aware boot SSH keys for a Hetzner create. Returns the (already
// registered) key NAMES to attach so no root password is set.
//   • OWNER/internal boxes → our operator boot key (YAVER_CLOUD_SSH_KEY_NAME,
//     a Convex env — never a source literal). Full ops SSH; it's our box.
//   • CUSTOMER-sold boxes → NEVER our operator key. Managed via the
//     control-plane; a customer key would be attached from machine.sshPublicKey
//     once registered. Empty ⇒ no operator footprint on the tenant box.
function resolveBootSshKeys(machine: { userId?: unknown }): string[] {
  if (isOwnerUserId(String(machine?.userId ?? ""))) {
    const name = (process.env.YAVER_CLOUD_SSH_KEY_NAME || "").trim();
    return name ? [name] : [];
  }
  return [];
}

// Snapshot a server, returning the created image id. Throws on
// failure so the caller can ABORT the delete (fail-closed: never
// delete a box without a recoverable snapshot).
async function hetznerSnapshot(
  token: string,
  serverId: string,
  desc: string,
): Promise<{ snapshotId: string; actionId?: string }> {
  const r = await fetch(`${HETZNER_API}/servers/${serverId}/actions/create_image`, {
    method: "POST",
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
    body: JSON.stringify({ type: "snapshot", description: desc }),
  });
  if (!r.ok) throw new Error(`snapshot HTTP ${r.status}`);
  const j = (await r.json()) as { image?: { id?: number }; action?: { id?: number } };
  if (!j.image?.id) throw new Error("snapshot returned no image id");
  return { snapshotId: String(j.image.id), actionId: j.action?.id ? String(j.action.id) : undefined };
}
/**
 * ---------------------------------------------------------------------------
 * Persistent data Volume — the fix for a 10-minute wake.
 * ---------------------------------------------------------------------------
 * Scale-to-zero has to DELETE the server (Hetzner bills stopped ones), so the
 * old model snapshotted the whole boot disk and restored it on every wake —
 * ~10 minutes of re-imaging data that never changes.
 *
 * Instead we keep all mutable state (workspace, ~/.yaver, Docker data, model
 * weights) on a Hetzner Volume. A Volume SURVIVES the server delete and
 * re-attaches at create time (`volumes: [id]` + `automount`). So:
 *   • park  = just delete the server → near-instant, no snapshot to wait on,
 *             and no snapshot that can fail and lose the box,
 *   • wake  = create a server from a SLIM base image + attach the volume →
 *             ~1-2 min instead of ~10.
 * Idle cost is the volume (~€0.044/GB/mo) instead of snapshot storage — still
 * pennies next to a running box.
 */
async function hetznerCreateVolume(
  token: string,
  name: string,
  sizeGb: number,
  location: string,
): Promise<string> {
  const r = await fetch(`${HETZNER_API}/volumes`, {
    method: "POST",
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
    body: JSON.stringify({ name, size: sizeGb, location, format: "ext4" }),
  });
  if (!r.ok) throw new Error(`create volume HTTP ${r.status}: ${await r.text()}`);
  const j = (await r.json()) as { volume?: { id?: number } };
  if (!j.volume?.id) throw new Error("create volume returned no id");
  return String(j.volume.id);
}

/** Returns the volume's status + the server it is attached to (if any). */
async function hetznerVolumeInfo(
  token: string,
  volumeId: string,
): Promise<{ status: string; serverId: string | null; location: string }> {
  const r = await fetch(`${HETZNER_API}/volumes/${volumeId}`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!r.ok) throw new Error(`volume info HTTP ${r.status}`);
  const j = (await r.json()) as {
    volume?: { status?: string; server?: number | null; location?: { name?: string } };
  };
  return {
    status: String(j.volume?.status ?? "unknown"),
    serverId: j.volume?.server ? String(j.volume.server) : null,
    location: String(j.volume?.location?.name ?? ""),
  };
}

/**
 * What the provider says about a server right now. Hetzner reports
 * "initializing" → "starting" → "running" (or "off"/"unknown"), which is the
 * only visibility that exists during the window between create and the
 * agent's first answer. Best-effort: a provider hiccup must never fail a wake.
 */
async function hetznerServerStatus(token: string, serverId: string): Promise<string | null> {
  try {
    const r = await fetch(`${HETZNER_API}/servers/${serverId}`, {
      headers: { Authorization: `Bearer ${token}` },
      signal: AbortSignal.timeout(8_000),
    });
    if (!r.ok) return null;
    const j = (await r.json()) as { server?: { status?: string } };
    const s = j.server?.status;
    return typeof s === "string" && s ? s : null;
  } catch {
    return null;
  }
}

/**
 * probeProviderStatus — refresh the provider-reported server state on a row.
 * Called from resumeHealthCheck while a wake has not yet been answered by the
 * agent, so the user is told "Hetzner: initializing" instead of nothing.
 */
export const probeProviderStatus = internalAction({
  args: { machineId: v.id("cloudMachines") },
  handler: async (ctx, { machineId }): Promise<string | null> => {
    const token = process.env.HCLOUD_TOKEN;
    if (!token) return null;
    const machine = await ctx.runQuery(internal.cloudMachines.getInternal, { machineId });
    const serverId = machine?.hetznerServerId ?? machine?.cloudResourceId;
    if (!serverId) return null;
    const status = await hetznerServerStatus(token, String(serverId));
    if (!status) return null;
    await ctx.runMutation(internal.cloudMachines.setProviderStatus, { machineId, status });
    return status;
  },
});

/** Permanently delete a volume (must be detached first). Best-effort. */
async function hetznerDeleteVolume(token: string, volumeId: string): Promise<void> {
  const r = await fetch(`${HETZNER_API}/volumes/${volumeId}`, {
    method: "DELETE",
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!r.ok && r.status !== 404) throw new Error(`delete volume HTTP ${r.status}`);
}

/**
 * Size of a snapshot image in GB, as Hetzner reports it. `image_size` is the
 * compressed stored size (what idle storage is billed on and what a restore
 * has to stream); `disk_size` is the uncompressed disk it unpacks to. We keep
 * the stored size — it's the one that explains both the bill and the wait.
 * Best-effort: null on any hiccup, since this is a nice-to-have fact.
 */
async function hetznerImageSizeGb(token: string, imageId: string): Promise<number | null> {
  try {
    const r = await fetch(`${HETZNER_API}/images/${imageId}`, {
      headers: { Authorization: `Bearer ${token}` },
      signal: AbortSignal.timeout(8_000),
    });
    if (!r.ok) return null;
    const j = (await r.json()) as { image?: { image_size?: number | null; disk_size?: number | null } };
    const size = j.image?.image_size ?? j.image?.disk_size;
    return typeof size === "number" && size > 0 ? Math.round(size * 10) / 10 : null;
  } catch {
    return null;
  }
}

/** Permanently delete a snapshot image. Best-effort. */
async function hetznerDeleteImage(token: string, imageId: string): Promise<void> {
  const r = await fetch(`${HETZNER_API}/images/${imageId}`, {
    method: "DELETE",
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!r.ok && r.status !== 404) throw new Error(`delete image HTTP ${r.status}`);
}

// pruneOldSnapshot deletes a machine's PREVIOUS snapshot after a new park's
// snapshot is confirmed durable — so a box keeps at most one snapshot, never
// one per sleep. Best-effort: an orphan snapshot is a few cents/mo, so a delete
// failure must never fail the park (the new snapshot + row are already correct).
async function pruneOldSnapshot(previousId: string | undefined, currentId: string): Promise<void> {
  if (!previousId || previousId === currentId) return;
  const token = process.env.HCLOUD_TOKEN;
  if (!token) return;
  try { await hetznerDeleteImage(token, previousId); } catch { /* best-effort */ }
}

/** Detach a volume (server delete detaches automatically; this is for repair). */
async function hetznerDetachVolume(token: string, volumeId: string): Promise<void> {
  const r = await fetch(`${HETZNER_API}/volumes/${volumeId}/actions/detach`, {
    method: "POST",
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!r.ok && r.status !== 404 && r.status !== 409) {
    throw new Error(`detach volume HTTP ${r.status}`);
  }
}

/**
 * Status of a snapshot image: "creating" | "available" | "unavailable".
 *
 * create_image hands back an image id as soon as Hetzner ACCEPTS the action —
 * the image is still being written to storage. Deleting the source server at
 * that point can abort the image and leave the box UNRECOVERABLE (this is how
 * snapshot 407385579 was lost). So the park path must never delete a server
 * until its snapshot reports `available`.
 */
async function hetznerImageStatus(token: string, imageId: string): Promise<string> {
  const r = await fetch(`${HETZNER_API}/images/${imageId}`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!r.ok) throw new Error(`image status HTTP ${r.status}`);
  const j = (await r.json()) as { image?: { status?: string } };
  return String(j.image?.status ?? "unknown");
}
async function hetznerDelete(token: string, serverId: string): Promise<void> {
  const r = await fetch(`${HETZNER_API}/servers/${serverId}`, {
    method: "DELETE",
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!r.ok && r.status !== 404) throw new Error(`delete HTTP ${r.status}`);
}
async function hetznerCreateFromImage(
  token: string,
  name: string,
  serverType: string,
  locations: string[],
  imageId: string,
  sshKeys: string[] = [],
  volumeIds: string[] = [],
): Promise<{ serverId: string; ip: string; location: string; actionId?: string }> {
  const imageVal: string | number = /^\d+$/.test(imageId) ? Number(imageId) : imageId;
  // Try each candidate location, moving on when a location can't serve this
  // type or is out of capacity — a snapshot restore must land SOMEWHERE, and
  // a box created in fsn1 whose region maps to nbg1 would otherwise fail hard
  // with "unsupported location for server type". Non-location errors (bad
  // image, auth) throw immediately.
  const tried = Array.from(new Set(locations.filter(Boolean)));
  let lastErr = "";
  for (const location of tried) {
    const r = await fetch(`${HETZNER_API}/servers`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify({
        name,
        server_type: serverType,
        image: imageVal,
        location,
        // Passing an SSH key makes Hetzner set NO root password — no "server
        // created" email, no forced-expiry that blocks the agent's boot, and a
        // clean self-start. Tenant-aware: only our OWN boxes carry the operator
        // key (see resolveBootSshKeys); we never bake it into sold customer boxes.
        ...(sshKeys.length ? { ssh_keys: sshKeys } : {}),
        // Attach the persistent data volume AT CREATE — Hetzner mounts it for
        // us (automount), so a wake never has to restore the data. This is the
        // whole ~10min → ~1-2min win: the boot image stays slim and all the
        // heavy, unchanging state simply re-appears with the volume.
        ...(volumeIds && volumeIds.length
          ? { volumes: volumeIds.map((v) => Number(v)), automount: true }
          : {}),
        labels: { service: "yaver-cloud-machine", managed: "true", resumed: "true" },
      }),
    });
    if (r.ok) {
      const j = (await r.json()) as {
        server?: { id?: number; public_net?: { ipv4?: { ip?: string } } };
        action?: { id?: number };
      };
      const id = j.server?.id;
      const ip = j.server?.public_net?.ipv4?.ip;
      if (!id || !ip) throw new Error("create-from-snapshot returned no id/ip");
      return { serverId: String(id), ip, location, actionId: j.action?.id ? String(j.action.id) : undefined };
    }
    const body = await r.text();
    lastErr = `HTTP ${r.status}: ${body}`;
    // Only advance to the next location for location/capacity problems.
    const retryable = /unsupported location|resource_unavailable|no available|capacity|placement/i.test(body);
    if (!retryable) throw new Error(`create-from-snapshot ${lastErr}`);
  }
  throw new Error(`create-from-snapshot exhausted locations [${tried.join(", ")}]: ${lastErr}`);
}

// EU/US candidate locations for a resume, primary first. cpx (AMD) types are
// offered in fsn1/nbg1/hel1 (EU) and ash/hil (US); trying several rides out a
// per-location "unsupported type"/capacity gap.
function resumeLocationCandidates(region: string | undefined): string[] {
  return region === "us"
    ? ["ash", "hil"]
    : ["fsn1", "nbg1", "hel1"];
}

// Re-point (or create) the box's A record. A resumed box gets a NEW
// Hetzner IP, so <id>.cloud.yaver.io must follow it or hostname-based
// access breaks. Best-effort — IP-direct still works if this fails.
async function cloudflareUpsertA(hostname: string, ip: string): Promise<void> {
  const token = process.env.CF_API_TOKEN;
  const zone = process.env.CF_ZONE_ID;
  if (!token || !zone || !hostname || !ip) return;
  const base = `https://api.cloudflare.com/client/v4/zones/${zone}/dns_records`;
  const headers = { Authorization: `Bearer ${token}`, "Content-Type": "application/json" };
  const body = JSON.stringify({ type: "A", name: hostname, content: ip, proxied: false, ttl: 60 });
  try {
    const list = await fetch(`${base}?type=A&name=${encodeURIComponent(hostname)}`, { headers });
    const listJson = (await list.json()) as { result?: { id: string }[] };
    const recordId = listJson.result?.[0]?.id;
    if (recordId) {
      await fetch(`${base}/${recordId}`, { method: "PUT", headers, body });
    } else {
      await fetch(base, { method: "POST", headers, body });
    }
  } catch (e) {
    console.error(`[cloudLifecycle] DNS upsert for ${hostname} failed:`, e);
  }
}

// Read-only enumeration of billable machines (own internalQuery over
// the shared table — allowed; never edits cloudMachines.ts). active =>
// live meter; paused => stopped (snapshot) meter.
export const listMeterableMachines = internalQuery({
  args: {},
  handler: async (
    ctx,
  ): Promise<Array<{ machineId: any; userId: any; machineType: string; status: "active" | "paused" }>> => {
    const rows = await ctx.db.query("cloudMachines").collect();
    return rows
      // INVARIANT: "active" means the box is USABLE — its agent answered
      // /health AND reported itself signed-in (see resumeHealthCheck). It does
      // NOT mean "the provider accepted a create". A box that is merely
      // booting sits in "resuming"/"provisioning" and is never billed, so we
      // can never charge for a box the user cannot reach. Do not widen this
      // filter to the in-flight statuses.
      .filter((m: any) => m.status === "active" || m.status === "paused")
      // Only meter MANAGED (Yaver-provisioned, platform-funded) boxes.
      // A self-hosted / BYO-Hetzner box (the user's own provider token,
      // they pay the provider directly) must NEVER be billed by the
      // prepaid meter — defensive guard even though BYO boxes live in
      // the agent's local store, not cloudMachines.
      .filter((m: any) => (m.origin ?? "managed") === "managed")
      .map((m: any) => ({
        machineId: m._id,
        userId: m.userId,
        machineType: m.machineType ?? "cpu",
        status: m.status as "active" | "paused",
      }));
  },
});

// Cron entrypoint: meter every billable machine for the elapsed
// interval and deduct. A live machine whose wallet drops below the
// safe floor is parked immediately; if the provider stop path cannot
// complete, the row is marked "suspended" with a loud error instead of
// silently leaving a paid server open.
export const meterTick = internalAction({
  args: { intervalSeconds: v.number(), dryRun: v.optional(v.boolean()) },
  handler: async (ctx, { intervalSeconds, dryRun }): Promise<MeterResult> => {
    const sim = dryRun !== false; // default true (no real spend posture)
    const machines = await ctx.runQuery(internal.cloudLifecycle.listMeterableMachines, {});
    let metered = 0;
    let suspended = 0;
    for (const m of machines) {
      const state = m.status === "active" ? "live" : "stopped";
      const r = await ctx.runMutation(internal.cloudLifecycle.recordUsageAndDeduct, {
        userId: m.userId,
        machineId: m.machineId,
        machineType: m.machineType,
        state: state as "live" | "stopped",
        seconds: intervalSeconds,
        dryRun: sim,
      });
      metered++;
      if (r.suspend && state === "live") {
        const park = await ctx.runAction(internal.cloudLifecycle.pauseMachine, {
          machineId: m.machineId,
          dryRun: sim,
        });
        if (!park.ok) {
          await ctx.runMutation(internal.cloudMachines.setStatus, {
            machineId: m.machineId,
            status: "suspended",
            errorMessage: `monthly included hours used up and prepaid overage balance below safe floor — attempted auto-park but it did not complete (${park.reason || "unknown reason"}). Top up or wait for next period, then retry wake/park.`,
          });
        }
        suspended++;
      }
    }
    return { metered, suspended, dryRun: sim };
  },
});

// PAUSE = preserve recoverable state → delete the Hetzner server (a powered-off
// server still bills full price; only delete stops it) → status "paused".
// Volume-backed boxes skip snapshots; legacy boxes snapshot first. HCLOUD_TOKEN
// absent ⇒ dry-run state transition (prod default; no real spend).
export const pauseMachine = internalAction({
  args: { machineId: v.id("cloudMachines"), dryRun: v.optional(v.boolean()) },
  handler: async (ctx, { machineId, dryRun }): Promise<LifecycleResult> => {
    const machine = await ctx.runQuery(internal.cloudMachines.getInternal, { machineId });
    if (!machine) return { ok: false, reason: "machine not found" };
    if (machine.status !== "active") {
      return { ok: false, reason: `not pausable from status ${machine.status}` };
    }
    const token = process.env.HCLOUD_TOKEN;
    const live = !!token && dryRun !== true;
    if (!live || !machine.hetznerServerId) {
      const reason = !machine.hetznerServerId
        ? "No provider server id recorded — park skipped"
        : token
          ? "Dry-run park requested — provider server was not deleted"
          : "HCLOUD_TOKEN unset — fail-closed dry-run (provider server was not deleted)";
      await ctx.runMutation(internal.cloudMachines.setPhase, {
        machineId, phase: "error", progress: 0, error: reason,
      });
      await ctx.runMutation(internal.wakeRuns.markProgress, {
        machineId,
        kind: "park",
        status: "failed",
        phase: "dry-run",
        progress: 0,
        error: reason,
        provider: machine.provider ?? "hetzner",
        providerResourceId: machine.cloudResourceId ?? machine.hetznerServerId,
        dryRun: true,
      }).catch(() => {});
      return { ok: false, status: machine.status, dryRun: true, reason };
    }
    // Flip to "stopping" + "snapshotting" phase up front so EVERY surface
    // (not just the one that tapped Park) can render the close-down ladder
    // while the snapshot — the slow part — runs. "stopping" is in the
    // healthy/in-flight status set, so this doesn't read as an outage.
    await ctx.runMutation(internal.cloudMachines.setStatus, { machineId, status: "stopping" });
    // Held locally as well as persisted: `machine` was read at the top of this
    // handler, so machine.parkStartedAt is the PREVIOUS park's stamp and would
    // measure this park against a run that finished days ago.
    const parkStartedAt = Date.now();
    await ctx.runMutation(internal.cloudMachines.setLifecycleTiming, {
      machineId, parkStartedAt,
    });

    // FAST PATH — the box keeps its state on a persistent Volume, so there is
    // NOTHING to snapshot: just delete the server. The volume survives and
    // re-attaches on the next wake. Park becomes near-instant, and there is no
    // snapshot that can fail and take the box with it.
    if (machine.volumeId) {
      await ctx.runMutation(internal.cloudMachines.setPhase, {
        machineId, phase: "powering-down", progress: 78,
      });
      try {
        await hetznerDelete(token!, machine.hetznerServerId);
        await ctx.runMutation(internal.wakeRuns.markProgress, {
          machineId,
          kind: "park",
          status: "running",
          phase: "provider-delete-accepted",
          progress: 86,
          provider: machine.provider ?? "hetzner",
          providerResourceId: machine.cloudResourceId ?? machine.hetznerServerId,
          dryRun: false,
        }).catch(() => {});
      } catch (e) {
        await ctx.runMutation(internal.cloudMachines.setStatus, {
          machineId, status: "error",
          errorMessage: `Delete failed: ${e instanceof Error ? e.message : String(e)} — box may still bill. Data is safe on volume ${machine.volumeId}. Retry pause.`,
        });
        return { ok: false, reason: "delete failed (data safe on volume)" };
      }
      await ctx.runMutation(internal.cloudMachines.setStatus, { machineId, status: "paused" });
      await ctx.runMutation(internal.cloudMachines.setPhase, {
        machineId, phase: "parked", progress: 100,
      });
      {
        const now = Date.now();
        await ctx.runMutation(internal.cloudMachines.setLifecycleTiming, {
          machineId,
          parkCompletedAt: now,
          lastParkDurationMs: Math.max(0, now - parkStartedAt),
          // Volume path keeps no snapshot — the data never left. Zero it so a
          // surface can't show a stale size from an older snapshot-era park.
          snapshotSizeGb: 0,
        });
      }
      return { ok: true, status: "paused", dryRun: false };
    }

    await ctx.runMutation(internal.cloudMachines.setPhase, {
      machineId, phase: "snapshotting", progress: 35,
    });
    // LEGACY (no volume yet): snapshot first; a failed snapshot ABORTS (never
    // delete an unrecoverable box) — mirrors cloudMachines.ts destroy invariant.
    let snapId: string;
    try {
      const snapshot = await hetznerSnapshot(
        token!, machine.hetznerServerId,
        `yaver-pause-machine-${machineId}-${Date.now()}`,
      );
      snapId = snapshot.snapshotId;
      await ctx.runMutation(internal.wakeRuns.markProgress, {
        machineId,
        kind: "park",
        status: "running",
        phase: "snapshot-accepted",
        progress: 55,
        provider: machine.provider ?? "hetzner",
        providerResourceId: snapId,
        providerActionId: snapshot.actionId,
        providerStatus: "creating",
        dryRun: false,
      }).catch(() => {});
    } catch (e) {
      await ctx.runMutation(internal.cloudMachines.setStatus, {
        machineId, status: "error",
        errorMessage: `Pause aborted: snapshot failed (${e instanceof Error ? e.message : String(e)}). Box still running, data safe — retry.`,
      });
      await ctx.runMutation(internal.cloudMachines.setPhase, {
        machineId, phase: "error", error: "snapshot failed",
      });
      return { ok: false, reason: "snapshot failed — NOT deleted (recover-safety)" };
    }
    await ctx.runMutation(internal.cloudMachines.setStatus, {
      machineId, status: "stopping", lastSnapshotId: snapId,
    });
    // Snapshot ACCEPTED — but Hetzner is still writing the image. Deleting the
    // server now can abort it and lose the box for good. Hand off to
    // finalizePause, which deletes ONLY once the image reports `available`.
    await ctx.runMutation(internal.cloudMachines.setPhase, {
      machineId, phase: "powering-down", progress: 78,
    });
    await ctx.scheduler.runAfter(15_000, internal.cloudLifecycle.finalizePause, {
      // machine.lastSnapshotId here is the PRIOR park's snapshot (captured
      // before this park overwrote it) — finalizePause deletes it once the new
      // one is durable, so this machine keeps exactly one. Never another
      // machine's / user's image; it's this row's own id.
      machineId, snapshotId: snapId, attempt: 1,
      previousSnapshotId: machine.lastSnapshotId ?? undefined,
    });
    return { ok: true, status: "stopping", snapshotId: snapId, dryRun: false };
  },
});

// ── Idle auto-shutdown (margin protection) ───────────────────────────
//
// A running managed box bills Hetzner every hour even when nobody uses
// it — the single biggest silent margin leak (and a violation of the
// scale-to-zero rule). idleSweep parks any ACTIVE managed box whose last
// MEANINGFUL activity (lastActivityAt — task / exec / inference, NOT mere
// agent liveness) is older than the threshold. Volume-backed boxes delete the
// server directly; legacy boxes snapshot first.
// The user resumes on demand (existing resumeMachine / web "resume").
//
// Auto-off is default-on for managed boxes. The running agent reports
// /machine/activity while work is live; if it goes idle past the threshold, it
// self-parks. YAVER_CLOUD_IDLE_DISABLE is the operator emergency brake.
// pauseMachine is itself fail-closed on HCLOUD_TOKEN.

// Read-only candidates: active managed boxes + their effective last-
// activity stamp (fall back to provisionPhaseAt/createdAt for boxes that
// predate the field, so a brand-new box isn't paused before it reports).
export const listIdleCandidates = internalQuery({
  args: {},
  handler: async (
    ctx,
  ): Promise<Array<{ machineId: any; lastActivityAt: number }>> => {
    const rows = await ctx.db.query("cloudMachines").collect();
    return rows
      .filter((m: any) => m.status === "active" && (m.origin ?? "managed") === "managed")
      // Auto-park is OPT-OUT: it stays ON by default (undefined === enabled), so
      // an idle box still stops its own meter unless the owner explicitly turns
      // it off. Only an explicit `false` keeps a box running while idle.
      .filter((m: any) => m.autoParkEnabled !== false)
      .map((m: any) => ({
        machineId: m._id,
        lastActivityAt: m.lastActivityAt ?? m.provisionPhaseAt ?? m.createdAt ?? 0,
      }));
  },
});

export const idleSweep = internalAction({
  args: {
    enabled: v.optional(v.boolean()),
    idleMinutes: v.optional(v.number()),
    dryRun: v.optional(v.boolean()),
  },
  handler: async (ctx, { enabled, idleMinutes, dryRun }): Promise<IdleSweepResult> => {
    const on = enabled === true;
    const sim = dryRun !== false; // mirror meterTick: default simulate
    if (!on) return { checked: 0, paused: 0, enabled: false, dryRun: sim };
    const mins = Number.isFinite(idleMinutes) && (idleMinutes as number) > 0 ? (idleMinutes as number) : 45;
    const cutoff = Date.now() - mins * 60_000;
    const candidates = await ctx.runQuery(internal.cloudLifecycle.listIdleCandidates, {});
    let checked = 0;
    let paused = 0;
    for (const c of candidates) {
      checked++;
      if (c.lastActivityAt > cutoff) continue; // still active recently
      // pauseMachine preserves state then deletes; it is fail-closed on
      // HCLOUD_TOKEN (dry-run state transition if unset). Legacy snapshot
      // fallback aborts the delete if the snapshot fails — never loses the box.
      await ctx.runAction(internal.cloudLifecycle.pauseMachine, {
        machineId: c.machineId,
        dryRun: sim,
      });
      paused++;
    }
    return { checked, paused, enabled: true, dryRun: sim };
  },
});

// idleSweepCron — the Convex-NATIVE scheduled entry for auto-off (registered in
// crons.ts). Runs from Convex itself (not the external Hetzner cron box that used
// to POST /crons/run) so the "never bill me for an idle box" guarantee can't
// silently break if an external scheduler is down — the single most important
// property here. Decoupled from the prepaid meter (YAVER_CLOUD_METER_LIVE): a
// live park only needs auto-off not disabled + a present HCLOUD_TOKEN
// (pauseMachine stays token-fail-closed, and legacy snapshot fallback aborts
// the delete if the snapshot fails — the box is never lost).
export const idleSweepCron = internalAction({
  args: {},
  handler: async (ctx): Promise<IdleSweepResult> => {
    const raw = (process.env.YAVER_CLOUD_IDLE_DISABLE ?? "").trim().toLowerCase();
    const disabled = raw === "1" || raw === "true" || raw === "yes" || raw === "on";
    const mins = Number(process.env.YAVER_CLOUD_IDLE_MINUTES);
    return await ctx.runAction(internal.cloudLifecycle.idleSweep, {
      enabled: !disabled,
      idleMinutes: Number.isFinite(mins) && mins > 0 ? mins : 45,
      dryRun: false, // live; pauseMachine is HCLOUD_TOKEN fail-closed on its own
    });
  },
});

// RESUME = prepaid-floor gate → recreate the Hetzner server from the recorded
// recovery source → persist new id/ip → status "active". HCLOUD_TOKEN absent
// ⇒ dry-run state transition.
/**
 * finalizePause — the SAFE half of scale-to-zero.
 *
 * pauseMachine only *starts* the snapshot. This action polls the image until
 * Hetzner reports it `available`, and only THEN deletes the server. Invariants:
 *   • never delete a server whose snapshot is still being written (that is how
 *     a box gets lost forever),
 *   • if the image ends up `unavailable` (creation failed), do NOT delete —
 *     leave the box running with its data intact and surface an error,
 *   • the box keeps billing while we wait (a few minutes of cents) — losing the
 *     machine is far more expensive than that, so safety wins.
 */
export const finalizePause = internalAction({
  args: {
    machineId: v.id("cloudMachines"),
    snapshotId: v.string(),
    attempt: v.number(),
    // The machine's snapshot from a PRIOR park, captured before this park
    // overwrote lastSnapshotId. Deleted once the new snapshot is confirmed
    // durable so a box accumulates at most ONE snapshot, never one per sleep.
    previousSnapshotId: v.optional(v.string()),
  },
  handler: async (ctx, { machineId, snapshotId, attempt, previousSnapshotId }): Promise<LifecycleResult> => {
    const machine = await ctx.runQuery(internal.cloudMachines.getInternal, { machineId });
    if (!machine) return { ok: false, reason: "machine not found" };
    if (!machine.hetznerServerId) {
      // Already deleted — just settle the row.
      await ctx.runMutation(internal.cloudMachines.setStatus, {
        machineId, status: "paused", lastSnapshotId: snapshotId,
      });
      await pruneOldSnapshot(previousSnapshotId, snapshotId);
      return { ok: true, status: "paused", snapshotId };
    }
    const token = process.env.HCLOUD_TOKEN;
    if (!token) return { ok: false, reason: "HCLOUD_TOKEN unset" };

    let status: string;
    try {
      status = await hetznerImageStatus(token, snapshotId);
      await ctx.runMutation(internal.wakeRuns.markProgress, {
        machineId,
        kind: "park",
        status: status === "available" ? "running" : "retrying",
        phase: "snapshot-finalizing",
        progress: status === "available" ? 70 : 60,
        provider: machine.provider ?? "hetzner",
        providerResourceId: snapshotId,
        providerStatus: status,
        dryRun: false,
      }).catch(() => {});
    } catch (e) {
      // Transient API blip — retry rather than risk anything.
      if (attempt < 60) {
        await ctx.scheduler.runAfter(15_000, internal.cloudLifecycle.finalizePause, {
          machineId, snapshotId, attempt: attempt + 1, previousSnapshotId,
        });
      }
      return { ok: false, reason: `image status check failed: ${e instanceof Error ? e.message : String(e)}`, retryable: true };
    }

    if (status === "creating") {
      // Still being written — wait. ~15 min budget (60 × 15s).
      if (attempt < 60) {
        await ctx.scheduler.runAfter(15_000, internal.cloudLifecycle.finalizePause, {
          machineId, snapshotId, attempt: attempt + 1, previousSnapshotId,
        });
        return { ok: false, reason: "snapshot still finalizing", retryable: true };
      }
      await ctx.runMutation(internal.cloudMachines.setStatus, {
        machineId, status: "error", lastSnapshotId: snapshotId,
        errorMessage: `Snapshot ${snapshotId} never finalized — server NOT deleted (data safe). Retry pause.`,
      });
      return { ok: false, reason: "snapshot did not finalize — not deleted (data safe)" };
    }

    if (status !== "available") {
      // Creation failed. NEVER delete — the box is the only copy of the data.
      await ctx.runMutation(internal.cloudMachines.setStatus, {
        machineId, status: "error", lastSnapshotId: snapshotId,
        errorMessage: `Snapshot ${snapshotId} is ${status} — server NOT deleted (data safe). Retry pause.`,
      });
      return { ok: false, reason: `snapshot ${status} — not deleted (data safe)` };
    }

    // Image is durable. Now it is safe to stop the meter.
    try {
      await hetznerDelete(token, machine.hetznerServerId);
      await ctx.runMutation(internal.wakeRuns.markProgress, {
        machineId,
        kind: "park",
        status: "running",
        phase: "provider-delete-accepted",
        progress: 86,
        provider: machine.provider ?? "hetzner",
        providerResourceId: machine.cloudResourceId ?? machine.hetznerServerId,
        dryRun: false,
      }).catch(() => {});
    } catch (e) {
      await ctx.runMutation(internal.cloudMachines.setStatus, {
        machineId, status: "error", lastSnapshotId: snapshotId,
        errorMessage: `Snapshot ${snapshotId} available but delete failed: ${e instanceof Error ? e.message : String(e)} — box may still bill. Retry pause.`,
      });
      return { ok: false, reason: "delete failed (snapshot safe)", snapshotId };
    }
    await ctx.runMutation(internal.cloudMachines.setStatus, {
      machineId, status: "paused", lastSnapshotId: snapshotId,
    });
    // New snapshot is durable and the server is gone — now safe to delete this
    // machine's PREVIOUS snapshot so the user keeps exactly one (either a live
    // server or one snapshot, never a pile from each sleep). Only this machine's
    // own prior image is touched.
    await pruneOldSnapshot(previousSnapshotId, snapshotId);
    await ctx.runMutation(internal.cloudMachines.setPhase, {
      machineId, phase: "parked", progress: 100,
    });
    // Record what was actually kept. Size is read only now, once the image is
    // `available` — Hetzner reports it as 0/absent while the snapshot is still
    // being written, so asking earlier would have persisted a confident zero.
    {
      const now = Date.now();
      const sizeGb = await hetznerImageSizeGb(token, snapshotId);
      const startedAt = machine.parkStartedAt ?? null;
      await ctx.runMutation(internal.cloudMachines.setLifecycleTiming, {
        machineId,
        parkCompletedAt: now,
        snapshotCreatedAt: now,
        ...(sizeGb !== null ? { snapshotSizeGb: sizeGb } : {}),
        ...(startedAt ? { lastParkDurationMs: Math.max(0, now - startedAt) } : {}),
      });
    }
    return { ok: true, status: "paused", snapshotId };
  },
});

/**
 * ensureVolume — create + attach the persistent data Volume for a machine that
 * doesn't have one yet (the migration path off full-disk snapshots).
 *
 * Safe by construction: it only ADDS a volume. It never touches the boot disk,
 * never deletes anything, and is idempotent — if the machine already has a
 * volume, it's a no-op. Moving the data onto the volume happens ON THE BOX
 * (see scripts/machine-volume-migrate.sh); this action just makes the disk exist
 * and be attached so the box can rsync into it.
 */
export const ensureVolume = internalAction({
  args: {
    machineId: v.id("cloudMachines"),
    sizeGb: v.optional(v.number()),
  },
  handler: async (ctx, { machineId, sizeGb }): Promise<LifecycleResult> => {
    const machine = await ctx.runQuery(internal.cloudMachines.getInternal, { machineId });
    if (!machine) return { ok: false, reason: "machine not found" };
    if (machine.volumeId) {
      return { ok: true, status: machine.status, reason: "volume already present" };
    }
    const token = process.env.HCLOUD_TOKEN;
    if (!token) return { ok: false, reason: "HCLOUD_TOKEN unset" };
    if (!machine.hetznerServerId) {
      return { ok: false, reason: "machine has no running server to attach to — wake it first" };
    }
    // Size the volume off the box's real disk so the data actually fits.
    const size = Math.max(10, Math.round(sizeGb ?? machine.specs?.diskGb ?? 100));
    const location = hetznerLocation(machine.region);
    let volumeId: string;
    try {
      volumeId = await hetznerCreateVolume(
        token,
        `yaver-data-${machineId}`.slice(0, 60),
        size,
        location,
      );
    } catch (e) {
      return { ok: false, reason: `create volume failed: ${e instanceof Error ? e.message : String(e)}` };
    }
    // Attach to the currently-running server so the box can migrate its data in.
    try {
      const r = await fetch(`${HETZNER_API}/volumes/${volumeId}/actions/attach`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify({ server: Number(machine.hetznerServerId), automount: true }),
      });
      if (!r.ok) throw new Error(`attach HTTP ${r.status}: ${await r.text()}`);
    } catch (e) {
      return { ok: false, reason: `attach volume failed: ${e instanceof Error ? e.message : String(e)}` };
    }
    await ctx.runMutation(internal.cloudMachines.setVolume, {
      machineId, volumeId, volumeSizeGb: size,
    });
    return { ok: true, status: machine.status, reason: `volume ${volumeId} (${size}GB) created + attached` };
  },
});

/**
 * purgeMachineResources — permanently tear down a managed machine's cloud
 * resources (server + volume + its snapshots) and clear the pointers on the
 * record. Used to fully retire a box / reset for a clean re-provision. Careful:
 * this DELETES the snapshot(s), so the data is gone — only call when the box is
 * genuinely being retired.
 */
export const purgeMachineResources = internalAction({
  args: {
    machineId: v.id("cloudMachines"),
    deleteSnapshots: v.optional(v.boolean()),
  },
  handler: async (ctx, { machineId, deleteSnapshots }): Promise<LifecycleResult> => {
    const machine = await ctx.runQuery(internal.cloudMachines.getInternal, { machineId });
    if (!machine) return { ok: false, reason: "machine not found" };
    const token = process.env.HCLOUD_TOKEN;
    if (!token) {
      await ctx.runMutation(internal.cloudMachines.setStatus, {
        machineId,
        status: "error",
        errorMessage:
          "Platform HCLOUD_TOKEN is not configured on this Convex deployment — the cloud resource was NOT purged. Set it, then retry decommission.",
      });
      return { ok: false, reason: "HCLOUD_TOKEN unset" };
    }
    const done: string[] = [];

    if (machine.hetznerServerId) {
      try { await hetznerDelete(token, machine.hetznerServerId); done.push("server"); } catch { /* best-effort */ }
    }
    if (machine.volumeId) {
      try { await hetznerDetachVolume(token, machine.volumeId); } catch { /* may already be detached */ }
      // detach is async — give it a beat before delete
      for (let i = 0; i < 8; i++) {
        try {
          const info = await hetznerVolumeInfo(token, machine.volumeId);
          if (!info.serverId) break;
        } catch { break; }
        await new Promise((r) => setTimeout(r, 2000));
      }
      try { await hetznerDeleteVolume(token, machine.volumeId); done.push("volume"); } catch { /* best-effort */ }
    }
    if (deleteSnapshots && machine.lastSnapshotId) {
      try { await hetznerDeleteImage(token, machine.lastSnapshotId); done.push("snapshot"); } catch { /* best-effort */ }
    }
    await ctx.runMutation(internal.cloudMachines.clearResources, { machineId });
    return { ok: true, reason: `purged: ${done.join(", ") || "nothing"}` };
  },
});

/** A woken box that never became USABLE is worse than a parked one: it bills by
 *  the hour while being invisible and unreachable to its owner (the wake in the
 *  2026-07-14 report left a cx43 running for exactly this reason). Delete the
 *  server, return the row to "paused", and leave the failure on the row so the
 *  wake reads as a failure with a cause instead of a silent disappearance.
 *
 *  Nothing is lost: the box booted from lastSnapshotId and never reached a
 *  state where anyone could write to it, so the snapshot still IS its disk. The
 *  row stays wakeable, so the user can fix the cause (usually a signed-out
 *  agent) and try again. */
export const abandonWake = internalAction({
  args: { machineId: v.id("cloudMachines"), reason: v.string() },
  handler: async (ctx, { machineId, reason }): Promise<LifecycleResult> => {
    const machine = await ctx.runQuery(internal.cloudMachines.getInternal, { machineId });
    if (!machine) return { ok: false, reason: "machine not found" };
    const token = process.env.HCLOUD_TOKEN;
    const serverId = machine.hetznerServerId ?? machine.cloudResourceId;
    let deleted = false;
    if (token && serverId) {
      try {
        await hetznerDelete(token, String(serverId));
        deleted = true;
      } catch (e) {
        // Could not stop the meter. Say so loudly on the row rather than
        // pretending the box is parked — an orphan server that nobody knows
        // about is the one outcome we never accept.
        const msg = e instanceof Error ? e.message : String(e);
        await ctx.runMutation(internal.cloudMachines.setPhase, {
          machineId, phase: "error", progress: 0,
          error: `${reason} — and the server could not be deleted (${msg}). It is STILL RUNNING and billing: delete server ${serverId} manually.`,
        });
        return { ok: false, reason: "abandon failed: server still running" };
      }
    }
    if (deleted) {
      await ctx.runMutation(internal.cloudMachines.clearServerRef, { machineId });
    }
    await ctx.runMutation(internal.cloudMachines.setStatus, {
      machineId, status: "paused", errorMessage: reason,
    });
    await ctx.runMutation(internal.cloudMachines.setPhase, {
      machineId, phase: "error", progress: 0, error: reason,
    });
    // The run ended, just not well. Recording HOW is what lets a parked box
    // explain its last wake instead of looking like it slept peacefully.
    await ctx.runMutation(internal.cloudMachines.setLifecycleTiming, {
      machineId,
      wakeCompletedAt: Date.now(),
      lastWakeOutcome: machine.provisionPhase === "awaiting-yaver-auth" ? "needs-auth" : "abandoned",
    });
    return { ok: true, status: "paused", reason };
  },
});

export const resumeMachine = internalAction({
  args: {
    machineId: v.id("cloudMachines"),
    dryRun: v.optional(v.boolean()),
    // Set only by the transient-retry self-schedule below (snapshot still
    // finalizing on the provider). Bounds the auto-retry loop.
    resumeAttempt: v.optional(v.number()),
  },
  handler: async (ctx, args): Promise<LifecycleResult> => {
    const { machineId, dryRun } = args;
    const machine = await ctx.runQuery(internal.cloudMachines.getInternal, { machineId });
    if (!machine) return { ok: false, reason: "machine not found" };
    if (machine.status !== "paused" && machine.status !== "suspended") {
      return { ok: false, reason: `not resumable from status ${machine.status}` };
    }
    const gate = await ctx.runQuery(internal.cloudLifecycle.canStart, {
      userId: machine.userId,
      machineType: machine.machineType ?? "cpu",
    });
    if (!gate.ok) {
      return { ok: false, reason: "insufficient prepaid balance",
        balanceCents: gate.balanceCents, requiredCents: gate.requiredCents };
    }
    const token = process.env.HCLOUD_TOKEN;
    const live = !!token && dryRun !== true;
    if (!live) {
      const reason = token
        ? "Dry-run wake requested — provider server was not created"
        : "HCLOUD_TOKEN unset — fail-closed dry-run (provider server was not created)";
      await ctx.runMutation(internal.cloudMachines.setPhase, {
        machineId, phase: "error", progress: 0, error: reason,
      });
      await ctx.runMutation(internal.wakeRuns.markProgress, {
        machineId,
        kind: "wake",
        status: "failed",
        phase: "dry-run",
        progress: 0,
        error: reason,
        provider: machine.provider ?? "hetzner",
        providerResourceId: machine.cloudResourceId ?? machine.hetznerServerId ?? machine.lastSnapshotId ?? machine.baseImageId,
        dryRun: true,
      }).catch(() => {});
      return { ok: false, status: machine.status, dryRun: true, reason };
    }
    if (!machine.lastSnapshotId && !(machine.volumeId && machine.baseImageId)) {
      await ctx.runMutation(internal.cloudMachines.setStatus, {
        machineId, status: "error",
        errorMessage: "Resume failed: no snapshot or volume-backed base image recorded — cannot recreate the box.",
      });
      return { ok: false, reason: "no snapshot or volume-backed base image" };
    }
    await ctx.runMutation(internal.cloudMachines.setStatus, { machineId, status: "resuming" });
    // Clear any stale "ready" left from before the park so no surface
    // briefly shows 100% while the box is still cold.
    //
    // A wake used to emit exactly two ticks — booting/20 here and
    // registering/85 after the create — so every surface sat on one frozen
    // label across the whole long pole. The steps below are not decoration:
    // each one is written immediately before the call that can actually hang
    // there (snapshot lookup, volume detach, provider create), so a stuck wake
    // now names the step it is stuck on.
    await ctx.runMutation(internal.cloudMachines.setPhase, {
      machineId,
      phase: machine.volumeId ? "preparing-volume" : "checking-snapshot",
      progress: 8,
    });
    // Start the run clock and clear the PREVIOUS run's outcome — leaving a
    // stale "needs-auth" on a fresh wake would have every surface explaining
    // a failure that hasn't happened yet.
    await ctx.runMutation(internal.cloudMachines.setLifecycleTiming, {
      machineId, wakeStartedAt: Date.now(), clearWakeOutcome: true,
    });
    try {
      // Recreate on the SAME server type the box was originally created on.
      // A snapshot can only restore onto a disk >= the source disk, so falling
      // back to the current global default (which may have been downsized)
      // would 422 with "image disk is bigger than server type disk". Prefer the
      // recorded serverType; fall back to the type implied by specs.diskGb, then
      // the machineType default.
      const serverType =
        machine.serverType ||
        hetznerServerTypeForDisk(machine.specs?.diskGb) ||
        hetznerServerType(machine.machineType ?? "cpu");
      // Prefer the region's primary location, then fall back across the same
      // zone so a type/capacity gap in one location doesn't block the wake.
      let locationCandidates = Array.from(new Set([
        hetznerLocation(machine.region),
        ...resumeLocationCandidates(machine.region),
      ]));
      // A Volume is bound to ONE location — the server MUST be created there or
      // the attach fails. So when we have a volume, its location wins outright.
      const volumeIds: string[] = [];
      if (machine.volumeId) {
        const vol = await hetznerVolumeInfo(token!, machine.volumeId);
        if (vol.location) locationCandidates = [vol.location];
        // A park deletes the server but the volume can linger "attached" to the
        // now-gone server, and create-with-volumes then 422s "volume already
        // attached". Detach it first so wake self-heals instead of dead-ending.
        if (vol.serverId) {
          // The detach poll below can burn 20s in silence. Name it, or the
          // bar sits on the prior step while we're actually waiting on
          // Hetzner to release a volume.
          await ctx.runMutation(internal.cloudMachines.setPhase, {
            machineId, phase: "preparing-volume", progress: 14,
          });
          try {
            await hetznerDetachVolume(token!, machine.volumeId);
            // Detach is async; give Hetzner a moment to release it.
            for (let i = 0; i < 10; i++) {
              const again = await hetznerVolumeInfo(token!, machine.volumeId);
              if (!again.serverId) break;
              await new Promise((r) => setTimeout(r, 2000));
            }
          } catch {
            /* best-effort — if it's actually free the create will succeed */
          }
        }
        volumeIds.push(machine.volumeId);
      }
      // With a volume, the boot image is a SLIM base (OS + toolchain only) — the
      // data rides on the volume, so there is no fat disk to restore. That is
      // the ~10min → ~1-2min win. Without a volume we fall back to the old
      // full-disk snapshot.
      const bootImage = (machine.volumeId && machine.baseImageId) || machine.lastSnapshotId;
      if (!bootImage) {
        throw new Error("no snapshot or volume-backed base image");
      }
      // A row seeded from a bare snapshot (seedParkedMachine) carries no
      // hostname, and an empty hostname quietly disabled BOTH the DNS upsert
      // and the resume health check — so such a box could wake, run, bill, and
      // never be verified or addressable by name. Mint the canonical hostname
      // the provision path uses instead of leaving the row nameless.
      const hostname = machine.hostname || `${String(machineId).substring(0, 8)}.cloud.yaver.io`;
      await ctx.runMutation(internal.cloudMachines.setPhase, {
        machineId, phase: "restoring-snapshot", progress: 25,
      });
      const { serverId, ip, actionId } = await hetznerCreateFromImage(
        token!,
        hostname,
        serverType,
        locationCandidates,
        bootImage,
        resolveBootSshKeys(machine),
        volumeIds,
      );
      await ctx.runMutation(internal.wakeRuns.markProgress, {
        machineId,
        kind: "wake",
        status: "running",
        phase: "provider-create-accepted",
        progress: 35,
        provider: machine.provider ?? "hetzner",
        providerResourceId: serverId,
        providerActionId: actionId,
        providerStatus: "creating",
        dryRun: false,
      }).catch(() => {});
      await ctx.runMutation(internal.cloudMachines.setProvisioned, {
        machineId, hetznerServerId: serverId, serverIp: ip,
        hostname, serverType,
      });
      // Resumed box has a NEW IP — re-point its DNS A record so the
      // <id>.cloud.yaver.io hostname keeps resolving (IP-direct works
      // regardless; this keeps the hostname/tunnel path alive).
      await cloudflareUpsertA(hostname, ip);
      // Deliberately STAY "resuming" here. A Hetzner create returns as soon as
      // the server RECORD exists — the OS is still booting and the agent has
      // not re-registered. Flipping to "active" at this point was the bug that
      // made a wake look like a failure: "active" reads as tone "online", so
      // the row instantly left the mobile "Sleeping machines" list while the
      // box was still cold, and it wasn't a device yet either — it vanished
      // into a void with no error. "active" now means USABLE, and only
      // resumeHealthCheck (which verifies the agent answers AND is not
      // signed-out) is allowed to set it. It also gates the meter: we never
      // bill for a box the user cannot reach.
      // "registering"/85 was a lie for the same reason "active" was: a Hetzner
      // create returns when the server RECORD exists, so nothing is
      // registering yet — the OS has not finished booting and the agent has
      // not started. Parking the bar at 85% for the eight minutes that
      // followed is exactly what made a healthy wake read as a hang. Only
      // resumeHealthCheck, which has actually heard from the agent, may
      // promote this to "registering".
      await ctx.runMutation(internal.cloudMachines.setPhase, {
        machineId, phase: "booting", progress: 45,
      });
      await ctx.scheduler.runAfter(20_000, internal.cloudMachines.resumeHealthCheck, {
        machineId, attempt: 1,
      });
      return { ok: true, status: "resuming", serverId, ip, dryRun: false };
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      // TRANSIENT: Hetzner is still finalizing the snapshot ("image not yet
      // available"). A park immediately followed by a wake hits this every
      // time on a large disk (a 160 GB snapshot takes minutes to become
      // available). Burning the machine into `error` here was a dead end — the
      // start route then refuses with "not resumable" and the box can only be
      // rescued by hand-editing the row. Keep it `paused` (so Wake still works
      // and the UI shows Wake, not a fatal error) and auto-retry.
      const transient =
        /image not yet available|not yet available|is locked|being created|resource_unavailable/i.test(msg);
      if (transient) {
        const waitingOnSnapshot = Boolean(machine.lastSnapshotId);
        await ctx.runMutation(internal.cloudMachines.setStatus, {
          machineId,
          status: "paused",
          errorMessage: waitingOnSnapshot
            ? `Snapshot ${machine.lastSnapshotId} is still finalizing on the provider — waking automatically as soon as it's ready.`
            : "Provider resource is temporarily unavailable — waking automatically as soon as capacity is available.",
        });
        // Self-retry with backoff until the image finalizes.
        const attempt = (args.resumeAttempt ?? 0) + 1;
        if (attempt <= 10) {
          await ctx.scheduler.runAfter(60_000, internal.cloudLifecycle.resumeMachine, {
            machineId,
            resumeAttempt: attempt,
          });
        }
        return {
          ok: false,
          reason: waitingOnSnapshot
            ? "snapshot still finalizing — wake will retry automatically"
            : "provider temporarily unavailable — wake will retry automatically",
          retryable: true,
        };
      }
      await ctx.runMutation(internal.cloudMachines.setStatus, {
        machineId, status: "error",
        errorMessage: `Resume failed: recreate from recovery source ${msg}. Recovery source retained — retry.`,
      });
      return { ok: false, reason: "recreate failed (recovery source retained)" };
    }
  },
});

export const resizeMachine = internalAction({
  args: {
    machineId: v.id("cloudMachines"),
    dryRun: v.optional(v.boolean()),
  },
  handler: async (ctx, args): Promise<LifecycleResult> => {
    const { machineId, dryRun } = args;
    const machine = await ctx.runQuery(internal.cloudMachines.getInternal, { machineId });
    if (!machine) return { ok: false, reason: "machine not found" };
    const targetMachineType = String((machine as any).resizeTargetMachineType || "").trim();
    if (!targetMachineType) return { ok: false, reason: "no resize target recorded" };
    if (!machine.volumeId || !machine.baseImageId) {
      await ctx.runMutation(internal.cloudMachines.setPhase, {
        machineId,
        phase: "error",
        progress: 0,
        error: "Resize failed: no volume-backed base image recorded.",
      });
      return { ok: false, reason: "no volume-backed base image" };
    }
    const gate = await ctx.runQuery(internal.cloudLifecycle.canStart, {
      userId: machine.userId,
      machineType: targetMachineType,
    });
    if (!gate.ok) {
      await ctx.runMutation(internal.cloudMachines.setPhase, {
        machineId,
        phase: "resize-required",
        progress: 0,
        error: "Resize blocked: insufficient prepaid balance.",
      });
      await ctx.runMutation(internal.wakeRuns.markProgress, {
        machineId,
        kind: "provision",
        status: "blocked",
        phase: "resize-required",
        progress: 0,
        error: "insufficient prepaid balance",
      }).catch(() => {});
      return {
        ok: false,
        reason: "insufficient prepaid balance",
        balanceCents: gate.balanceCents,
        requiredCents: gate.requiredCents,
      };
    }

    const token = process.env.HCLOUD_TOKEN;
    const live = !!token && dryRun !== true;
    if (!live) {
      const reason = token
        ? "Dry-run resize requested — provider server was not recreated"
        : "HCLOUD_TOKEN unset — fail-closed dry-run (provider server was not recreated)";
      await ctx.runMutation(internal.cloudMachines.setPhase, {
        machineId,
        phase: "error",
        progress: 0,
        error: reason,
      });
      await ctx.runMutation(internal.wakeRuns.markProgress, {
        machineId,
        kind: "provision",
        status: "failed",
        phase: "dry-run",
        progress: 0,
        error: reason,
        provider: machine.provider ?? "hetzner",
        providerResourceId: machine.cloudResourceId ?? machine.hetznerServerId ?? machine.volumeId,
        dryRun: true,
      }).catch(() => {});
      return { ok: false, status: machine.status, dryRun: true, reason };
    }

    const hostname = machine.hostname || `${String(machineId).substring(0, 8)}.cloud.yaver.io`;
    const targetServerType = hetznerServerType(targetMachineType);
    await ctx.runMutation(internal.cloudMachines.setStatus, { machineId, status: "resuming" });
    await ctx.runMutation(internal.cloudMachines.setLifecycleTiming, {
      machineId,
      wakeStartedAt: Date.now(),
      clearWakeOutcome: true,
    });
    await ctx.runMutation(internal.cloudMachines.setPhase, {
      machineId,
      phase: "resizing-machine",
      progress: 8,
    });
    try {
      if (machine.hetznerServerId) {
        await ctx.runMutation(internal.cloudMachines.setPhase, {
          machineId,
          phase: "deleting-stateless-server",
          progress: 14,
        });
        await hetznerDelete(token!, machine.hetznerServerId);
        await ctx.runMutation(internal.cloudMachines.clearServerRef, { machineId });
      }

      let locationCandidates = Array.from(new Set([
        hetznerLocation(machine.region),
        ...resumeLocationCandidates(machine.region),
      ]));
      const vol = await hetznerVolumeInfo(token!, machine.volumeId);
      if (vol.location) locationCandidates = [vol.location];
      if (vol.serverId) {
        await ctx.runMutation(internal.cloudMachines.setPhase, {
          machineId,
          phase: "preparing-volume",
          progress: 20,
        });
        try {
          await hetznerDetachVolume(token!, machine.volumeId);
          for (let i = 0; i < 10; i++) {
            const again = await hetznerVolumeInfo(token!, machine.volumeId);
            if (!again.serverId) break;
            await new Promise((r) => setTimeout(r, 2000));
          }
        } catch {
          /* best-effort — create-with-volume reports the real failure if still attached */
        }
      }

      await ctx.runMutation(internal.cloudMachines.setPhase, {
        machineId,
        phase: "creating-resized-server",
        progress: 30,
      });
      const { serverId, ip, actionId } = await hetznerCreateFromImage(
        token!,
        hostname,
        targetServerType,
        locationCandidates,
        machine.baseImageId,
        resolveBootSshKeys(machine),
        [machine.volumeId],
      );
      await ctx.runMutation(internal.wakeRuns.markProgress, {
        machineId,
        kind: "provision",
        status: "running",
        phase: "provider-create-accepted",
        progress: 38,
        provider: machine.provider ?? "hetzner",
        providerResourceId: serverId,
        providerActionId: actionId,
        providerStatus: "creating",
        dryRun: false,
      }).catch(() => {});
      await ctx.runMutation(internal.cloudMachines.setResizedProvisioned, {
        machineId,
        targetMachineType,
        hetznerServerId: serverId,
        serverIp: ip,
        hostname,
        serverType: targetServerType,
      });
      await cloudflareUpsertA(hostname, ip);
      await ctx.runMutation(internal.cloudMachines.setPhase, {
        machineId,
        phase: "booting",
        progress: 45,
      });
      await ctx.scheduler.runAfter(20_000, internal.cloudMachines.resumeHealthCheck, {
        machineId,
        attempt: 1,
      });
      return { ok: true, status: "resuming", serverId, ip, dryRun: false };
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      await ctx.runMutation(internal.cloudMachines.setStatus, {
        machineId,
        status: "paused",
        errorMessage: `Resize failed: ${msg}. Data volume retained — retry resize.`,
      });
      await ctx.runMutation(internal.cloudMachines.setPhase, {
        machineId,
        phase: "error",
        progress: 0,
        error: "resize failed",
      });
      await ctx.runMutation(internal.wakeRuns.markProgress, {
        machineId,
        kind: "provision",
        status: "failed",
        phase: "error",
        progress: 0,
        error: "resize failed",
        provider: machine.provider ?? "hetzner",
        providerResourceId: machine.cloudResourceId ?? machine.hetznerServerId ?? machine.volumeId,
      }).catch(() => {});
      return { ok: false, reason: "resize failed" };
    }
  },
});
