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

// Markup over raw provider COGS, per SKU, env-overridable. Defaults:
// cpu 2x (100% margin), gpu 3x (GPU COGS is lumpier + pricier). Set
// YAVER_CLOUD_MARKUP_CPU / _GPU (a number like "2.5") to retune without
// a redeploy. User pays markup x raw in every state (live + stopped).
const MARKUP_BY_TYPE: Record<string, number> = { cpu: 2, gpu: 3 };
export function markup(machineType: string): number {
  const env = Number(process.env[`YAVER_CLOUD_MARKUP_${(machineType || "cpu").toUpperCase()}`]);
  if (Number.isFinite(env) && env > 0) return env;
  return MARKUP_BY_TYPE[machineType] ?? 2;
}
// Back-compat default (cpu) for any external reference. Prefer markup().
export const MARKUP_X = MARKUP_BY_TYPE.cpu;

// Raw Hetzner COGS basis. Managed SKU = cpx51 €54.90/mo (16 vCPU/32 GB,
// the Talos-grade monorepo box — see MACHINE_SPECS in cloudMachines.ts).
// Stopped = snapshot storage only (~€0.80/mo for the larger image, still
// rounds to ~0c/h). Cents/hour; monthly ÷ 730. ⚠️ Keep this in sync with
// MACHINE_SPECS.cpu.hetznerType and re-verify the price with
// GET /v1/server_types before HCLOUD_TOKEN goes live. Region/type
// variance can be passed as an explicit rate later — conservative
// defaults here.
const HETZNER_COST_CENTS_PER_HOUR: Record<string, { live: number; stopped: number }> = {
  // €54.90/mo ≈ 752 c/mo ... (USD ~ ; we bill USD-cents, treat €≈$ for
  // the wallet — exact FX is a P6/top-up concern, not the meter).
  cpu: { live: Math.round((5490 / 730)), stopped: Math.round((80 / 730)) },    // ~7.5c/h live, ~0c/h stopped
  gpu: { live: Math.round((19900 / 730)), stopped: Math.round((100 / 730)) },  // GPU tier placeholder
};

function rawRate(machineType: string, state: "live" | "stopped"): number {
  const r = HETZNER_COST_CENTS_PER_HOUR[machineType] ?? HETZNER_COST_CENTS_PER_HOUR.cpu;
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
const INCLUDED_HOURS: Record<string, { cpu: number; gpu: number }> = {
  "cloud-agent": { cpu: 40, gpu: 0 },
  "cloud-workspace": { cpu: 40, gpu: 0 },
};
export function includedHoursForPlan(plan: string, machineType: string): number {
  const t = machineType === "gpu" ? "gpu" : "cpu";
  const envKey = `YAVER_CLOUD_INCLUDED_HOURS_${(plan || "").toUpperCase().replace(/-/g, "_")}_${t.toUpperCase()}`;
  const env = Number(process.env[envKey]);
  if (Number.isFinite(env) && env >= 0) return env;
  return INCLUDED_HOURS[plan]?.[t] ?? 0;
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

// Top-up. Real money path (LemonSqueezy credit packs) is P6/Codex —
// this is the internal primitive it (and owner-dev tooling) calls.
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

// Idempotent real-money top-up. The web credit-pack checkout pays via
// LemonSqueezy; its `order_created` webhook calls this with the provider
// order id. LemonSqueezy re-delivers webhooks, so we key on orderId in
// creditTopups and no-op on a duplicate — a re-delivery can never
// double-credit the wallet. Returns the (possibly unchanged) balance.
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
    const type = machineType === "gpu" ? "gpu" : "cpu";
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
    const type = machineType === "gpu" ? "gpu" : "cpu";
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

    // Included-hours first (live only). Consume the subscriber's monthly
    // active-hour grant for THIS machineType before charging the prepaid
    // overage wallet. A user with no allowance row (pay-as-you-go) covers
    // 0 seconds, so everything below is byte-identical to the legacy
    // wallet path. Stopped (snapshot) ticks never draw the grant — for
    // cpx-class boxes the raw stopped rate already rounds to ~0c/h, so a
    // parked workspace is effectively free and the base absorbs it.
    let coveredSeconds = 0;
    let remainingIncluded = 0;
    if (state === "live") {
      const period = billingPeriodUTC(now);
      const type = machineType === "gpu" ? "gpu" : "cpu";
      const allow = await ctx.db
        .query("includedAllowance")
        .withIndex("by_user_period_type", (q: any) =>
          q.eq("userId", userId).eq("period", period).eq("machineType", type),
        )
        .unique();
      if (allow) {
        const left = Math.max(0, allow.includedSeconds - allow.usedSeconds);
        coveredSeconds = Math.min(seconds, left);
        if (coveredSeconds > 0) {
          await ctx.db.patch(allow._id, {
            usedSeconds: allow.usedSeconds + coveredSeconds,
            updatedAt: now,
          });
        }
        remainingIncluded = left - coveredSeconds;
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
  ): Promise<{ ok: boolean; balanceCents: number; requiredCents: number }> => {
    const w = await getWalletInternalRow(ctx, userId);
    const need = minimumReserveCents(machineType);
    const have = w?.balanceCents ?? 0;
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
  snapshotId?: string;
  serverId?: string;
  ip?: string;
  balanceCents?: number;
  requiredCents?: number;
};
type MeterResult = { metered: number; suspended: number; dryRun: boolean };
type IdleSweepResult = { checked: number; paused: number; enabled: boolean; dryRun: boolean };

const HETZNER_API = "https://api.hetzner.cloud/v1";

function hetznerServerType(machineType: string): string {
  if (machineType === "cpu") {
    return process.env.YAVER_CLOUD_CPU_TYPE || "cpx51"; // 32GB Talos-grade monorepo SKU
  }
  return process.env.YAVER_CLOUD_GPU_TYPE || "cpx51";
}
function hetznerLocation(region: string | undefined): string {
  return region === "us" ? "ash" : "nbg1";
}

// Snapshot a server, returning the created image id. Throws on
// failure so the caller can ABORT the delete (fail-closed: never
// delete a box without a recoverable snapshot).
async function hetznerSnapshot(token: string, serverId: string, desc: string): Promise<string> {
  const r = await fetch(`${HETZNER_API}/servers/${serverId}/actions/create_image`, {
    method: "POST",
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
    body: JSON.stringify({ type: "snapshot", description: desc }),
  });
  if (!r.ok) throw new Error(`snapshot HTTP ${r.status}`);
  const j = (await r.json()) as { image?: { id?: number } };
  if (!j.image?.id) throw new Error("snapshot returned no image id");
  return String(j.image.id);
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
  location: string,
  imageId: string,
): Promise<{ serverId: string; ip: string }> {
  const imageVal: string | number = /^\d+$/.test(imageId) ? Number(imageId) : imageId;
  const r = await fetch(`${HETZNER_API}/servers`, {
    method: "POST",
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
    body: JSON.stringify({
      name,
      server_type: serverType,
      image: imageVal,
      location,
      labels: { service: "yaver-cloud-machine", managed: "true", resumed: "true" },
    }),
  });
  if (!r.ok) throw new Error(`create-from-snapshot HTTP ${r.status}: ${await r.text()}`);
  const j = (await r.json()) as {
    server?: { id?: number; public_net?: { ipv4?: { ip?: string } } };
  };
  const id = j.server?.id;
  const ip = j.server?.public_net?.ipv4?.ip;
  if (!id || !ip) throw new Error("create-from-snapshot returned no id/ip");
  return { serverId: String(id), ip };
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
// safe floor is marked "suspended" (the agent-side force-stop is
// driven by a P3 route / the agent watchdog reacting to that status).
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
        await ctx.runMutation(internal.cloudMachines.setStatus, {
          machineId: m.machineId,
          status: "suspended",
          errorMessage: "monthly included hours used up and prepaid overage balance below safe floor — auto-stopping (top up or wait for next period to resume)",
        });
        suspended++;
      }
    }
    return { metered, suspended, dryRun: sim };
  },
});

// PAUSE = snapshot (fail-closed) → delete the Hetzner server (a
// powered-off server still bills full price; only delete stops it) →
// status "paused", snapshot id persisted for resume. HCLOUD_TOKEN
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
      await ctx.runMutation(internal.cloudMachines.setStatus, { machineId, status: "paused" });
      return { ok: true, status: "paused", dryRun: true,
        reason: token ? undefined : "HCLOUD_TOKEN unset — fail-closed dry-run (no real spend)" };
    }
    // Real: snapshot first; a failed snapshot ABORTS (never delete an
    // unrecoverable box) — mirrors cloudMachines.ts destroy invariant.
    let snapId: string;
    try {
      snapId = await hetznerSnapshot(
        token!, machine.hetznerServerId,
        `yaver-pause-machine-${machineId}-${Date.now()}`,
      );
    } catch (e) {
      await ctx.runMutation(internal.cloudMachines.setStatus, {
        machineId, status: "error",
        errorMessage: `Pause aborted: snapshot failed (${e instanceof Error ? e.message : String(e)}). Box still running, data safe — retry.`,
      });
      return { ok: false, reason: "snapshot failed — NOT deleted (recover-safety)" };
    }
    await ctx.runMutation(internal.cloudMachines.setStatus, {
      machineId, status: "stopping", lastSnapshotId: snapId,
    });
    try {
      await hetznerDelete(token!, machine.hetznerServerId);
    } catch (e) {
      await ctx.runMutation(internal.cloudMachines.setStatus, {
        machineId, status: "error", lastSnapshotId: snapId,
        errorMessage: `Snapshot ok (image ${snapId}) but delete failed: ${e instanceof Error ? e.message : String(e)} — box may still bill. Retry pause.`,
      });
      return { ok: false, reason: "delete failed (snapshot safe)", snapshotId: snapId };
    }
    await ctx.runMutation(internal.cloudMachines.setStatus, {
      machineId, status: "paused", lastSnapshotId: snapId,
    });
    return { ok: true, status: "paused", snapshotId: snapId, dryRun: false };
  },
});

// ── Idle auto-shutdown (margin protection) ───────────────────────────
//
// A running managed box bills Hetzner every hour even when nobody uses
// it — the single biggest silent margin leak (and a violation of the
// scale-to-zero rule). idleSweep pauses (snapshot+delete) any ACTIVE
// managed box whose last MEANINGFUL activity (lastActivityAt — task /
// exec / inference, NOT mere agent liveness) is older than the threshold.
// The user resumes on demand (existing resumeMachine / web "resume").
//
// DEFAULT OFF (enabled=false) until the box agent reports activity via
// /machine/activity — otherwise we'd pause boxes that ARE in use but not
// yet reporting. pauseMachine is itself fail-closed on HCLOUD_TOKEN, so
// even enabled it's a dry-run state transition until the token is set.

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
      // pauseMachine snapshots then deletes; it is fail-closed on
      // HCLOUD_TOKEN (dry-run state transition if unset) and aborts the
      // delete if the snapshot fails — never loses the box.
      await ctx.runAction(internal.cloudLifecycle.pauseMachine, {
        machineId: c.machineId,
        dryRun: sim,
      });
      paused++;
    }
    return { checked, paused, enabled: true, dryRun: sim };
  },
});

// RESUME = prepaid-floor gate → recreate the Hetzner server from the
// pause snapshot → persist new id/ip → status "active". HCLOUD_TOKEN
// absent ⇒ dry-run state transition.
export const resumeMachine = internalAction({
  args: { machineId: v.id("cloudMachines"), dryRun: v.optional(v.boolean()) },
  handler: async (ctx, { machineId, dryRun }): Promise<LifecycleResult> => {
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
      await ctx.runMutation(internal.cloudMachines.setStatus, { machineId, status: "active" });
      return { ok: true, status: "active", dryRun: true,
        reason: token ? undefined : "HCLOUD_TOKEN unset — fail-closed dry-run (no real spend)" };
    }
    if (!machine.lastSnapshotId) {
      await ctx.runMutation(internal.cloudMachines.setStatus, {
        machineId, status: "error",
        errorMessage: "Resume failed: no pause snapshot id recorded — cannot recreate the box.",
      });
      return { ok: false, reason: "no snapshot id" };
    }
    await ctx.runMutation(internal.cloudMachines.setStatus, { machineId, status: "resuming" });
    try {
      const { serverId, ip } = await hetznerCreateFromImage(
        token!,
        machine.hostname || `yaver-${machineId}`,
        hetznerServerType(machine.machineType ?? "cpu"),
        hetznerLocation(machine.region),
        machine.lastSnapshotId,
      );
      await ctx.runMutation(internal.cloudMachines.setProvisioned, {
        machineId, hetznerServerId: serverId, serverIp: ip,
        hostname: machine.hostname || "",
      });
      // Resumed box has a NEW IP — re-point its DNS A record so the
      // <id>.cloud.yaver.io hostname keeps resolving (IP-direct works
      // regardless; this keeps the hostname/tunnel path alive).
      if (machine.hostname) await cloudflareUpsertA(machine.hostname, ip);
      await ctx.runMutation(internal.cloudMachines.setStatus, { machineId, status: "active" });
      return { ok: true, status: "active", serverId, ip, dryRun: false };
    } catch (e) {
      await ctx.runMutation(internal.cloudMachines.setStatus, {
        machineId, status: "error",
        errorMessage: `Resume failed: recreate-from-snapshot ${e instanceof Error ? e.message : String(e)}. Snapshot ${machine.lastSnapshotId} retained — retry.`,
      });
      return { ok: false, reason: "recreate failed (snapshot retained)" };
    }
  },
});
