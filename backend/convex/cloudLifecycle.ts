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

import { mutation, query, internalMutation, internalQuery, internalAction } from "./_generated/server";
import { v } from "convex/values";
import { internal } from "./_generated/api";

// 100% margin: user pays 2x raw Hetzner in every state.
export const MARKUP_X = 2;

// Raw Hetzner COGS basis. Managed SKU = cpx42 €29.99/mo (16GB, the
// RN/Hermes box — see MACHINE_SPECS in cloudMachines.ts). Stopped =
// snapshot storage only (~€0.50/mo for a typical image). Cents/hour;
// monthly ÷ 730. Region/type variance handled by passing an explicit
// rate later — these are the conservative defaults.
const HETZNER_COST_CENTS_PER_HOUR: Record<string, { live: number; stopped: number }> = {
  // €29.99/mo ≈ 411 c/mo ... (USD ~ ; we bill USD-cents, treat €≈$ for
  // the wallet — exact FX is a P6/top-up concern, not the meter).
  cpu: { live: Math.round((2999 / 730)), stopped: Math.round((50 / 730)) },   // ~4.1c/h live, ~0.07c/h stopped
  gpu: { live: Math.round((19900 / 730)), stopped: Math.round((100 / 730)) }, // GPU tier placeholder
};

function rawRate(machineType: string, state: "live" | "stopped"): number {
  const r = HETZNER_COST_CENTS_PER_HOUR[machineType] ?? HETZNER_COST_CENTS_PER_HOUR.cpu;
  return state === "stopped" ? r.stopped : r.live;
}

// Two-part minimum prepaid floor (cents): enough to (a) safely execute
// one live→stop snapshot transition + (b) keep the snapshot parked
// ≥1 month. Pure fn — P2/P3 gate "can start" on balance >= this.
export function minimumReserveCents(machineType: string): number {
  const stoppedMonth = rawRate(machineType, "stopped") * 730 * MARKUP_X;
  // Transition reserve: assume up to ~1h of live billing to snapshot+
  // delete safely (snapshot can take minutes; be generous).
  const transition = rawRate(machineType, "live") * 1 * MARKUP_X;
  return Math.ceil(stoppedMonth + transition);
}

function todayUTC(now: number): string {
  return new Date(now).toISOString().slice(0, 10);
}

// ── Wallet ───────────────────────────────────────────────────────────

export const getWallet = query({
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
  ): Promise<{ balanceCents: number; suspend: boolean; charged: number }> => {
    if (seconds <= 0) {
      const w0 = await getWalletInternalRow(ctx, userId);
      return { balanceCents: w0?.balanceCents ?? 0, suspend: false, charged: 0 };
    }
    const rateHour = rawRate(machineType, state);
    const hetznerCostCents = Math.ceil((rateHour * seconds) / 3600);
    const chargedCents = hetznerCostCents * MARKUP_X;
    const now = Date.now();

    await ctx.db.insert("creditUsage", {
      userId,
      machineId,
      date: todayUTC(now),
      state,
      seconds,
      hetznerCostCents,
      chargedCents,
      ratePerHourCents: rateHour * MARKUP_X,
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

    // Auto-stop BEFORE zero: a live box must be force-stopped while it
    // can still afford the snapshot transition + parked month.
    const floor = state === "live" ? minimumReserveCents(machineType) : 0;
    return { balanceCents: newBalance, suspend: newBalance <= floor, charged: chargedCents };
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

const HETZNER_API = "https://api.hetzner.cloud/v1";

function hetznerServerType(machineType: string): string {
  if (machineType === "cpu") {
    return process.env.YAVER_CLOUD_CPU_TYPE || "cpx42"; // 16GB RN/Hermes SKU
  }
  return process.env.YAVER_CLOUD_GPU_TYPE || "cpx42";
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
          errorMessage: "prepaid balance below safe floor — auto-stopping (top up to resume)",
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
