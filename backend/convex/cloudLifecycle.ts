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
  /** True when the failure is transient (e.g. the provider is still finalizing
   *  the snapshot) and the wake will retry itself — the caller should show
   *  "waking, hang on" rather than a fatal error. */
  retryable?: boolean;
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
): Promise<{ serverId: string; ip: string; location: string }> {
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
      };
      const id = j.server?.id;
      const ip = j.server?.public_net?.ipv4?.ip;
      if (!id || !ip) throw new Error("create-from-snapshot returned no id/ip");
      return { serverId: String(id), ip, location };
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
    // Flip to "stopping" + "snapshotting" phase up front so EVERY surface
    // (not just the one that tapped Park) can render the close-down ladder
    // while the snapshot — the slow part — runs. "stopping" is in the
    // healthy/in-flight status set, so this doesn't read as an outage.
    await ctx.runMutation(internal.cloudMachines.setStatus, { machineId, status: "stopping" });

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
      return { ok: true, status: "paused", dryRun: false };
    }

    await ctx.runMutation(internal.cloudMachines.setPhase, {
      machineId, phase: "snapshotting", progress: 35,
    });
    // LEGACY (no volume yet): snapshot first; a failed snapshot ABORTS (never
    // delete an unrecoverable box) — mirrors cloudMachines.ts destroy invariant.
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
      machineId, snapshotId: snapId, attempt: 1,
    });
    return { ok: true, status: "stopping", snapshotId: snapId, dryRun: false };
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

// idleSweepCron — the Convex-NATIVE scheduled entry for auto-off (registered in
// crons.ts). Runs from Convex itself (not the external Hetzner cron box that used
// to POST /crons/run) so the "never bill me for an idle box" guarantee can't
// silently break if an external scheduler is down — the single most important
// property here. Decoupled from the prepaid meter (YAVER_CLOUD_METER_LIVE): a
// live snapshot+delete only needs YAVER_CLOUD_IDLE_ENABLE + a present
// HCLOUD_TOKEN (pauseMachine stays token-fail-closed, and aborts the delete if
// the snapshot fails — the box is never lost).
export const idleSweepCron = internalAction({
  args: {},
  handler: async (ctx): Promise<IdleSweepResult> => {
    const raw = (process.env.YAVER_CLOUD_IDLE_ENABLE ?? "").trim().toLowerCase();
    const enabled = raw === "1" || raw === "true" || raw === "yes" || raw === "on";
    const mins = Number(process.env.YAVER_CLOUD_IDLE_MINUTES);
    return await ctx.runAction(internal.cloudLifecycle.idleSweep, {
      enabled,
      idleMinutes: Number.isFinite(mins) && mins > 0 ? mins : 45,
      dryRun: false, // live; pauseMachine is HCLOUD_TOKEN fail-closed on its own
    });
  },
});

// RESUME = prepaid-floor gate → recreate the Hetzner server from the
// pause snapshot → persist new id/ip → status "active". HCLOUD_TOKEN
// absent ⇒ dry-run state transition.
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
  },
  handler: async (ctx, { machineId, snapshotId, attempt }): Promise<LifecycleResult> => {
    const machine = await ctx.runQuery(internal.cloudMachines.getInternal, { machineId });
    if (!machine) return { ok: false, reason: "machine not found" };
    if (!machine.hetznerServerId) {
      // Already deleted — just settle the row.
      await ctx.runMutation(internal.cloudMachines.setStatus, {
        machineId, status: "paused", lastSnapshotId: snapshotId,
      });
      return { ok: true, status: "paused", snapshotId };
    }
    const token = process.env.HCLOUD_TOKEN;
    if (!token) return { ok: false, reason: "HCLOUD_TOKEN unset" };

    let status: string;
    try {
      status = await hetznerImageStatus(token, snapshotId);
    } catch (e) {
      // Transient API blip — retry rather than risk anything.
      if (attempt < 60) {
        await ctx.scheduler.runAfter(15_000, internal.cloudLifecycle.finalizePause, {
          machineId, snapshotId, attempt: attempt + 1,
        });
      }
      return { ok: false, reason: `image status check failed: ${e instanceof Error ? e.message : String(e)}`, retryable: true };
    }

    if (status === "creating") {
      // Still being written — wait. ~15 min budget (60 × 15s).
      if (attempt < 60) {
        await ctx.scheduler.runAfter(15_000, internal.cloudLifecycle.finalizePause, {
          machineId, snapshotId, attempt: attempt + 1,
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
    await ctx.runMutation(internal.cloudMachines.setPhase, {
      machineId, phase: "parked", progress: 100,
    });
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
    // Clear any stale "ready" left from before the park so no surface
    // briefly shows 100% while the box is still cold.
    await ctx.runMutation(internal.cloudMachines.setPhase, {
      machineId, phase: "booting", progress: 20,
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
        volumeIds.push(machine.volumeId);
      }
      // With a volume, the boot image is a SLIM base (OS + toolchain only) — the
      // data rides on the volume, so there is no fat disk to restore. That is
      // the ~10min → ~1-2min win. Without a volume we fall back to the old
      // full-disk snapshot.
      const bootImage = (machine.volumeId && machine.baseImageId) || machine.lastSnapshotId;
      const { serverId, ip } = await hetznerCreateFromImage(
        token!,
        machine.hostname || `yaver-${machineId}`,
        serverType,
        locationCandidates,
        bootImage,
        resolveBootSshKeys(machine),
        volumeIds,
      );
      await ctx.runMutation(internal.cloudMachines.setProvisioned, {
        machineId, hetznerServerId: serverId, serverIp: ip,
        hostname: machine.hostname || "", serverType,
      });
      // Resumed box has a NEW IP — re-point its DNS A record so the
      // <id>.cloud.yaver.io hostname keeps resolving (IP-direct works
      // regardless; this keeps the hostname/tunnel path alive).
      if (machine.hostname) await cloudflareUpsertA(machine.hostname, ip);
      await ctx.runMutation(internal.cloudMachines.setStatus, { machineId, status: "active" });
      // Server RECORD exists, but the OS is still booting + the agent
      // hasn't re-registered on the relay yet. Do NOT claim "ready" — sit
      // at "registering"/85 and let resumeHealthCheck flip to ready only
      // once /health actually answers. This is the fix for the wake that
      // used to jump to 100% while the box was still cold.
      await ctx.runMutation(internal.cloudMachines.setPhase, {
        machineId, phase: "registering", progress: 85,
      });
      await ctx.scheduler.runAfter(20_000, internal.cloudMachines.resumeHealthCheck, {
        machineId, attempt: 1,
      });
      return { ok: true, status: "active", serverId, ip, dryRun: false };
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
        await ctx.runMutation(internal.cloudMachines.setStatus, {
          machineId,
          status: "paused",
          errorMessage: `Snapshot ${machine.lastSnapshotId} is still finalizing on the provider — waking automatically as soon as it's ready.`,
        });
        // Self-retry with backoff until the image finalizes.
        const attempt = (args.resumeAttempt ?? 0) + 1;
        if (attempt <= 10) {
          await ctx.scheduler.runAfter(60_000, internal.cloudLifecycle.resumeMachine, {
            machineId,
            resumeAttempt: attempt,
          });
        }
        return { ok: false, reason: "snapshot still finalizing — wake will retry automatically", retryable: true };
      }
      await ctx.runMutation(internal.cloudMachines.setStatus, {
        machineId, status: "error",
        errorMessage: `Resume failed: recreate-from-snapshot ${msg}. Snapshot ${machine.lastSnapshotId} retained — retry.`,
      });
      return { ok: false, reason: "recreate failed (snapshot retained)" };
    }
  },
});
