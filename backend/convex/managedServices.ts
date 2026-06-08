// Per-user à-la-carte managed-service opt-in + honest burn breakdown.
//
// This is the data layer behind the web "capability shelf" cockpit
// (docs/yaver-normie-concierge-fair-metering.md). The normie turns on
// only the Yaver-managed capabilities he wants — Hermes reload alone, or
// reload + backend + web, or the always-on agent box, or App-Store
// publish — and each is an independent switch stored in
// userSettings.managedServices.
//
// Fairness contract (doc §6): the cockpit shows an HONEST per-capability
// spend breakdown, not a single opaque number. burnBreakdown reads the
// existing meter ledgers (managedUsage for inference/backend/web/publish,
// creditUsage for compute) and aggregates charged cents per capability so
// the user can always see exactly what each layer cost. No new ledger,
// no secrets — counters only.
//
// All functions are internal + userId-keyed (the cloudLifecycle.getWallet
// pattern). The web/mobile reach them through HTTP routes in http.ts that
// derive userId from the bearer session (authenticateRequest), so a
// client can only ever read/write its OWN opt-in + ledger. Wallet/meter
// internals stay in cloudLifecycle.ts + managedMeter.ts; this module only
// READS them and toggles the opt-in flags.

import { internalQuery, internalMutation } from "./_generated/server";
import { v } from "convex/values";

// The à-la-carte capabilities, in ladder order (cheapest hook first →
// hero last). Keys match userSettings.managedServices and, for the four
// reseller meters, the managedUsage `kind`. reload + agentBox both bill
// through the compute meter (creditUsage); they're separate switches
// because they're separate user-facing capabilities.
export const MANAGED_SERVICE_KEYS = [
  "reload",
  "backend",
  "web",
  "agentBox",
  "inference",
  "publish",
  "studio",
] as const;
export type ManagedServiceKey = (typeof MANAGED_SERVICE_KEYS)[number];

// Which meter `kind` each capability's spend shows up under. reload +
// agentBox both surface as "compute" in the ledger.
const SERVICE_TO_METER_KIND: Record<ManagedServiceKey, string> = {
  reload: "compute",
  backend: "backend",
  web: "web",
  agentBox: "compute",
  inference: "inference",
  publish: "publish",
  // Store Studio (screenshots / preview & permission videos) — its own ledger
  // line so the cockpit shows Studio spend distinctly. Billed on farm/build/
  // render minutes; a run on the owner's OWN runner costs 0 (free BYO exit).
  studio: "studio",
};

function isServiceKey(s: string): s is ManagedServiceKey {
  return (MANAGED_SERVICE_KEYS as readonly string[]).includes(s);
}

function daysAgoUTC(now: number, days: number): string {
  return new Date(now - days * 86_400_000).toISOString().slice(0, 10);
}

function fullServiceMap(svc: Record<string, boolean> | undefined): Record<string, boolean> {
  const out: Record<string, boolean> = {};
  for (const k of MANAGED_SERVICE_KEYS) out[k] = svc?.[k] === true;
  return out;
}

/**
 * Read the caller's à-la-carte service opt-in set. Returns every
 * capability key with its current boolean (default false = off) so the
 * cockpit can render the full shelf without guessing the schema.
 */
export const getServicesForUser = internalQuery({
  args: { userId: v.id("users") },
  handler: async (ctx, { userId }) => {
    const settings = await ctx.db
      .query("userSettings")
      .withIndex("by_userId", (q) => q.eq("userId", userId))
      .first();
    return { services: fullServiceMap(settings?.managedServices as Record<string, boolean> | undefined) };
  },
});

/**
 * Toggle ONE capability on/off for the caller. One switch per tap from
 * the capability shelf. Unknown service keys are rejected so the schema
 * stays the single source of truth for what's offerable.
 */
export const setServiceForUser = internalMutation({
  args: {
    userId: v.id("users"),
    service: v.string(),
    enabled: v.boolean(),
  },
  handler: async (ctx, { userId, service, enabled }) => {
    if (!isServiceKey(service)) {
      throw new Error(`unknown managed service: ${service}`);
    }
    const existing = await ctx.db
      .query("userSettings")
      .withIndex("by_userId", (q) => q.eq("userId", userId))
      .first();

    // Merge: keep prior enabled flags, apply this one. Drop the key when
    // turning off so the stored object stays minimal (matches
    // userSettings.mergeManagedPatch semantics).
    const prior = (existing?.managedServices ?? {}) as Record<string, boolean>;
    const merged: Record<string, boolean> = {};
    for (const [k, val] of Object.entries(prior)) {
      if (typeof val === "boolean" && val) merged[k] = true;
    }
    if (enabled) merged[service] = true;
    else delete merged[service];
    const next = Object.keys(merged).length === 0 ? undefined : merged;

    if (existing) {
      await ctx.db.patch(existing._id, { managedServices: next });
    } else {
      await ctx.db.insert("userSettings", { userId, managedServices: next });
    }
    return { ok: true, services: fullServiceMap(merged) };
  },
});

type BurnRow = {
  kind: string;
  chargedCents: number;
  providerCostCents: number;
  quantity: number;
  count: number;
  dryRunCents: number;
};

async function aggregateBurn(ctx: any, userId: string, days: number) {
  const cutoff = daysAgoUTC(Date.now(), days);
  const byKind = new Map<string, BurnRow>();
  const bump = (kind: string, charged: number, cogs: number, qty: number, sim: boolean) => {
    const r =
      byKind.get(kind) ??
      { kind, chargedCents: 0, providerCostCents: 0, quantity: 0, count: 0, dryRunCents: 0 };
    r.chargedCents += charged;
    r.providerCostCents += cogs;
    r.quantity += qty;
    r.count += 1;
    if (sim) r.dryRunCents += charged;
    byKind.set(kind, r);
  };

  // Reseller meters (inference/backend/web/publish + any compute routed
  // through managedMeter).
  const managed = await ctx.db
    .query("managedUsage")
    .withIndex("by_user_date", (q: any) => q.eq("userId", userId).gte("date", cutoff))
    .collect();
  for (const u of managed) bump(u.kind, u.chargedCents, u.providerCostCents, u.quantity, u.dryRun);

  // Compute meter (cloudLifecycle.ts creditUsage). seconds → quantity.
  const compute = await ctx.db
    .query("creditUsage")
    .withIndex("by_user_date", (q: any) => q.eq("userId", userId).gte("date", cutoff))
    .collect();
  for (const c of compute) bump("compute", c.chargedCents, c.hetznerCostCents, c.seconds, c.dryRun);

  const rows = Array.from(byKind.values()).sort((a, b) => b.chargedCents - a.chargedCents);
  const totalChargedCents = rows.reduce((s, r) => s + r.chargedCents, 0);
  const totalDryRunCents = rows.reduce((s, r) => s + r.dryRunCents, 0);
  return {
    cutoff,
    rows,
    totalChargedCents,
    totalDryRunCents,
    realChargedCents: Math.max(0, totalChargedCents - totalDryRunCents),
  };
}

function clampDays(days: number | undefined): number {
  return days && days > 0 ? Math.min(days, 90) : 7;
}

/**
 * Honest per-capability spend breakdown for the cockpit. Aggregates the
 * meter ledgers over the last `days` (default 7) into charged cents,
 * provider COGS, and metered quantity per capability. This is the
 * transparency the fairness contract demands (doc §6 rule 4): a headline
 * number for calm, the per-layer breakdown one tap away.
 *
 * `dryRunCents` is tracked separately so the cockpit can show "simulated"
 * spend distinctly from real charges during the dry-run launch posture.
 */
export const burnBreakdownForUser = internalQuery({
  args: { userId: v.id("users"), days: v.optional(v.number()) },
  handler: async (ctx, { userId, days }) => {
    const d = clampDays(days);
    const agg = await aggregateBurn(ctx, userId, d);
    return {
      days: d,
      since: agg.cutoff,
      rows: agg.rows,
      totalChargedCents: agg.totalChargedCents,
      totalDryRunCents: agg.totalDryRunCents,
      realChargedCents: agg.realChargedCents,
    };
  },
});

/**
 * One-call cockpit summary: wallet balance + which capabilities are on +
 * a rough "days left at current pace" estimate. Lets the web cockpit
 * render the whole shelf header from a single fetch.
 */
export const cockpitSummaryForUser = internalQuery({
  args: { userId: v.id("users"), days: v.optional(v.number()) },
  handler: async (ctx, { userId, days }) => {
    const d = clampDays(days);
    const wallet = await ctx.db
      .query("prepaidCredits")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .unique();
    const balanceCents = wallet?.balanceCents ?? 0;

    const settings = await ctx.db
      .query("userSettings")
      .withIndex("by_userId", (q) => q.eq("userId", userId))
      .first();
    const enabled = fullServiceMap(settings?.managedServices as Record<string, boolean> | undefined);

    const agg = await aggregateBurn(ctx, userId, d);
    const perDayCents = d > 0 ? agg.totalChargedCents / d : 0;
    // Whole days of runway at the recent pace; null when idle (no burn →
    // "no estimate" rather than Infinity).
    const daysLeft = perDayCents > 0 ? Math.floor(balanceCents / perDayCents) : null;

    return {
      balanceCents,
      currency: wallet?.currency ?? "usd",
      enabled,
      anyEnabled: Object.values(enabled).some(Boolean),
      windowDays: d,
      windowChargedCents: agg.totalChargedCents,
      estPerDayCents: Math.round(perDayCents),
      estDaysLeft: daysLeft,
      lowBalance: balanceCents > 0 && daysLeft !== null && daysLeft <= 3,
      empty: balanceCents <= 0,
    };
  },
});

// Static helper for the meter-kind mapping, exported for callers that
// need to know which ledger a capability bills to.
export function meterKindForService(service: string): string | null {
  return isServiceKey(service) ? SERVICE_TO_METER_KIND[service] : null;
}
