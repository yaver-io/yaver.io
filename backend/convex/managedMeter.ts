// Generic managed-resource meter — the single debit path for every
// Yaver-Premium reseller meter that ISN'T the original compute SKU.
//
// One prepaid wallet (prepaidCredits, owned by cloudLifecycle.ts), many
// meters. The compute meter (creditUsage / recordUsageAndDeduct in
// cloudLifecycle.ts) stays as-is. Everything else Yaver resells —
// inference tokens (GLM/OpenRouter gateway), managed backend (Convex
// proxy), managed web (Cloudflare proxy), publish (Mac-farm build +
// ASC/Play upload) — calls recordManagedUsage here, which appends a
// managedUsage row and debits the SAME wallet. Adding a new meter is a
// new `kind` string + a markup default; no new table, no new wallet.
//
// Money is integer cents end-to-end (no float drift). chargedCents =
// providerCostCents x markup(kind) — the arbitrage spread. `dryRun`
// (default true) mirrors cloudLifecycle's fail-closed launch posture:
// ledger + balance move so the UX is real, but go-live is one env flip
// (YAVER_MANAGED_METER_LIVE) per project_business_model.
//
// Privacy: every field is a counter/id/timestamp or a NON-SECRET label
// (kind/provider/unit/model/ref). No token/key/path/output. Pinned by
// desktop/agent/convex_privacy_test.go (TestManagedUsageFields_*).

import { internalMutation, mutation } from "./_generated/server";
import { v } from "convex/values";
import { resolveUser } from "./agentSync";

// Markup over raw upstream COGS, per meter kind, env-overridable. The
// inference spread is intentionally lighter than compute (2x) because
// the user's mental reference price is Claude/ChatGPT — GLM-class tokens
// are ~7-10x cheaper, so even 1.5x reads as "cheap" while still earning.
// Set YAVER_MANAGED_MARKUP_<KIND> (e.g. "1.8") to retune without deploy.
const MARKUP_BY_KIND: Record<string, number> = {
  inference: 1.5,
  backend: 2,
  web: 2,
  publish: 1.3, // build-minutes; thin — the value is the convenience
  compute: 2,   // parity with cloudLifecycle if compute ever routes here
  ci: 2,        // CI minutes; COGS (Hetzner ~$0.00015/cpu-min) is so far below
                // GitHub's $0.008 anchor that 2x still lands ~10-20x under GHA.
                // providerCostCents is 0 when CI ran on the user's own box or
                // the operator fleet → charged 0 (free), still logged for the
                // savings ledger. See docs/yaver-managed-cloud-ci-absorption.md.
};

export function managedMarkup(kind: string): number {
  const env = Number(process.env[`YAVER_MANAGED_MARKUP_${(kind || "").toUpperCase()}`]);
  if (Number.isFinite(env) && env > 0) return env;
  return MARKUP_BY_KIND[kind] ?? 2;
}

function todayUTC(now: number): string {
  return new Date(now).toISOString().slice(0, 10);
}

// Per-user à-la-carte gate. The global YAVER_MANAGED_METER_LIVE flag is
// the platform-wide kill switch; this is the per-user opt-in ON TOP of
// it. A user only incurs REAL (non-dryRun) charges for a meter kind they
// have explicitly turned on via userSettings.managedServices (the
// capability shelf — docs/yaver-normie-concierge-fair-metering.md). The
// reseller meters route 1:1 to a service key (inference→inference,
// backend→backend, web→web, publish→publish); anything else is treated
// as not-opted-in and stays simulated. This is defense-in-depth: even if
// a gateway/proxy caller passes dryRun:false, a user who never enabled
// the capability is never billed for real.
async function userOptedIntoKind(ctx: any, userId: string, kind: string): Promise<boolean> {
  const settings = await ctx.db
    .query("userSettings")
    .withIndex("by_userId", (q: any) => q.eq("userId", userId))
    .first();
  const svc = settings?.managedServices as Record<string, boolean> | undefined;
  if (!svc) return false;
  return svc[kind] === true;
}

// Local wallet row helper. The canonical wallet owner is
// cloudLifecycle.ts (ensureWalletRow); this is an intentional inline
// copy so managedMeter stays import-independent of that module. Same
// table (prepaidCredits), same semantics. If wallet shape changes,
// change it in both — the privacy test pins the field names in one list.
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

// Append one usage row and debit the wallet. Callers pass raw upstream
// COGS in cents (providerCostCents) plus the metered quantity/unit for
// the audit trail; markup is applied here so the spread lives in one
// place. Returns the new balance and a `suspend` hint (true once the
// wallet hits zero) so the calling gateway/proxy can cut the user off.
//
// This is the inference/backend/web/publish analogue of
// cloudLifecycle.recordUsageAndDeduct (compute). Internal-only — the
// edge gateway and proxies reach it via ctx.runMutation, never the
// client.
// applyManagedUsage is the shared insert+debit core. Both the internal meter
// (gateway/proxy path) and the agent-callable CI meter funnel through it so the
// markup spread + dryRun gate + wallet debit live in exactly one place.
async function applyManagedUsage(
  ctx: any,
  p: {
    userId: string;
    kind: string;
    provider: string;
    unit: string;
    quantity: number;
    providerCostCents: number;
    model?: string;
    ref?: string;
    wouldHaveCostUpstreamCents?: number;
    dryRun?: boolean;
  },
): Promise<{ balanceCents: number; suspend: boolean; charged: number }> {
  // dryRun unless BOTH the caller asked for a real charge (dryRun:false) AND
  // the user has opted this capability in. Per-user opt-in is the à-la-carte
  // gate on top of the global YAVER_MANAGED_METER_LIVE flag.
  const optedIn = await userOptedIntoKind(ctx, p.userId, p.kind);
  const sim = p.dryRun !== false || !optedIn; // default true (no real spend posture)
  const cost = Math.max(0, Math.ceil(p.providerCostCents));
  if (cost <= 0 && p.quantity <= 0) {
    const w0 = await ensureWalletRow(ctx, p.userId);
    return { balanceCents: w0.balanceCents, suspend: w0.balanceCents <= 0, charged: 0 };
  }
  const chargedCents = Math.ceil(cost * managedMarkup(p.kind));
  const now = Date.now();

  await ctx.db.insert("managedUsage", {
    userId: p.userId,
    kind: p.kind,
    provider: p.provider,
    unit: p.unit,
    quantity: p.quantity,
    providerCostCents: cost,
    chargedCents,
    model: p.model,
    ref: p.ref,
    ...(typeof p.wouldHaveCostUpstreamCents === "number" && p.wouldHaveCostUpstreamCents > 0
      ? { wouldHaveCostUpstreamCents: Math.ceil(p.wouldHaveCostUpstreamCents) }
      : {}),
    date: todayUTC(now),
    dryRun: sim,
    createdAt: now,
  });

  const w = await ensureWalletRow(ctx, p.userId);
  const newBalance = Math.max(0, w.balanceCents - chargedCents);
  await ctx.db.patch(w._id, {
    balanceCents: newBalance,
    totalUsedCents: w.totalUsedCents + chargedCents,
    lastMeteredAt: now,
    updatedAt: now,
  });

  return { balanceCents: newBalance, suspend: newBalance <= 0, charged: chargedCents };
}

export const recordManagedUsage = internalMutation({
  args: {
    userId: v.id("users"),
    kind: v.string(),
    provider: v.string(),
    unit: v.string(),
    quantity: v.number(),
    providerCostCents: v.number(),
    model: v.optional(v.string()),
    ref: v.optional(v.string()),
    wouldHaveCostUpstreamCents: v.optional(v.number()),
    dryRun: v.optional(v.boolean()),
  },
  handler: async (
    ctx,
    args,
  ): Promise<{ balanceCents: number; suspend: boolean; charged: number }> => {
    return applyManagedUsage(ctx, args);
  },
});

// recordCIUsageFromAgent — the agent-callable CI meter (kind hard-pinned to
// "ci"). The self-hosted CI runner (ci_selfhosted_runner.go) posts one row per
// completed job via convexSyncer.callMutation. The user is resolved from the
// agent's authed session (same resolveUser path as agentSync), so the agent
// never passes a userId. Stays simulated until YAVER_MANAGED_METER_LIVE + the
// per-user `ci` capability opt-in. Privacy: all fields are non-secret counters/
// labels (deviceId/provider/unit/quantity/cents/ref) — pinned by
// convex_privacy_test.go.
export const recordCIUsageFromAgent = mutation({
  args: {
    deviceId: v.optional(v.string()),
    provider: v.string(), // "self-hosted" | "operator-fleet" | "yaver-cloud"
    unit: v.string(),     // "cpu-min" | "mac-min"
    quantity: v.number(),
    providerCostCents: v.number(),
    wouldHaveCostUpstreamCents: v.optional(v.number()),
    ref: v.optional(v.string()),
    dryRun: v.optional(v.boolean()),
  },
  handler: async (ctx, args): Promise<{ balanceCents: number; suspend: boolean; charged: number }> => {
    const userId = await resolveUser(ctx);
    return applyManagedUsage(ctx, {
      userId,
      kind: "ci",
      provider: args.provider,
      unit: args.unit,
      quantity: args.quantity,
      providerCostCents: args.providerCostCents,
      wouldHaveCostUpstreamCents: args.wouldHaveCostUpstreamCents,
      ref: args.ref,
      dryRun: args.dryRun,
    });
  },
});
