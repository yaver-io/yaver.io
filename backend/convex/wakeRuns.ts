// wakeRuns.ts — durable Cloud Workspace wake/provision/park progress ledger.
//
// Privacy contract: this module stores only control-plane ids, phase/status
// labels, timings, profile labels, non-secret provider action/resource ids,
// dry-run flags, and short curated reason/error text. It must never store
// prompts, logs, repo paths, hostnames, provider IPs, tokens, or source data.

import { v } from "convex/values";
import { internalMutation, internalQuery, query } from "./_generated/server";
import type { Id } from "./_generated/dataModel";
import { validateSessionInternal } from "./auth";

const runKinds = v.union(v.literal("provision"), v.literal("wake"), v.literal("park"));
const runStatuses = v.union(
  v.literal("queued"),
  v.literal("running"),
  v.literal("succeeded"),
  v.literal("failed"),
  v.literal("retrying"),
  v.literal("blocked"),
  v.literal("cancelled"),
);

async function userFromToken(ctx: any, tokenHash: string): Promise<Id<"users">> {
  const session = await validateSessionInternal(ctx, tokenHash);
  if (!session) throw new Error("Unauthorized");
  return session.user._id;
}

function trimLabel(value: string | undefined, max: number): string | undefined {
  const text = String(value || "").trim();
  return text ? text.slice(0, max) : undefined;
}

async function latestOpenRun(
  ctx: any,
  machineId: Id<"cloudMachines">,
  kind: "provision" | "wake" | "park",
) {
  const rows = await ctx.db
    .query("wakeRuns")
    .withIndex("by_machine_started", (q: any) => q.eq("machineId", machineId))
    .order("desc")
    .take(12);
  return rows.find((row: any) =>
    row.kind === kind &&
    (row.status === "queued" || row.status === "running" || row.status === "retrying" || row.status === "blocked")
  ) ?? null;
}

export const start = internalMutation({
  args: {
    userId: v.id("users"),
    machineId: v.id("cloudMachines"),
    placementId: v.optional(v.id("taskPlacements")),
    taskId: v.optional(v.string()),
    kind: runKinds,
    status: v.optional(runStatuses),
    phase: v.optional(v.string()),
    progress: v.optional(v.number()),
    resourceClass: v.optional(v.string()),
    machineType: v.optional(v.string()),
    targetDeviceId: v.optional(v.string()),
    reason: v.optional(v.string()),
    provider: v.optional(v.string()),
    providerResourceId: v.optional(v.string()),
    providerActionId: v.optional(v.string()),
    providerStatus: v.optional(v.string()),
    dryRun: v.optional(v.boolean()),
  },
  handler: async (ctx, args) => {
    const now = Date.now();
    const existing = await latestOpenRun(ctx, args.machineId, args.kind);
    const patch = {
      userId: args.userId,
      machineId: args.machineId,
      placementId: args.placementId,
      taskId: trimLabel(args.taskId, 120),
      kind: args.kind,
      status: args.status ?? "queued",
      phase: trimLabel(args.phase, 80),
      progress: typeof args.progress === "number" ? Math.max(0, Math.min(100, args.progress)) : undefined,
      resourceClass: trimLabel(args.resourceClass, 40),
      machineType: trimLabel(args.machineType, 40),
      targetDeviceId: trimLabel(args.targetDeviceId, 120),
      reason: trimLabel(args.reason, 240),
      provider: trimLabel(args.provider, 40),
      providerResourceId: trimLabel(args.providerResourceId, 120),
      providerActionId: trimLabel(args.providerActionId, 120),
      providerStatus: trimLabel(args.providerStatus, 80),
      dryRun: args.dryRun,
      updatedAt: now,
    };
    if (existing) {
      await ctx.db.patch(existing._id, patch);
      return existing._id;
    }
    return await ctx.db.insert("wakeRuns", {
      ...patch,
      startedAt: now,
    });
  },
});

export const markProgress = internalMutation({
  args: {
    machineId: v.id("cloudMachines"),
    kind: v.optional(runKinds),
    status: v.optional(runStatuses),
    phase: v.optional(v.string()),
    progress: v.optional(v.number()),
    error: v.optional(v.string()),
    reason: v.optional(v.string()),
    provider: v.optional(v.string()),
    providerResourceId: v.optional(v.string()),
    providerActionId: v.optional(v.string()),
    providerStatus: v.optional(v.string()),
    dryRun: v.optional(v.boolean()),
  },
  handler: async (ctx, args) => {
    const machine = await ctx.db.get(args.machineId);
    if (!machine) return { ok: false, reason: "machine not found" };
    const kind = args.kind ??
      (String(machine.status || "") === "stopping" || String(machine.status || "") === "paused" ? "park" : "wake");
    const existing = await latestOpenRun(ctx, args.machineId, kind);
    if (!existing) {
      await ctx.db.insert("wakeRuns", {
        userId: machine.userId,
        machineId: args.machineId,
        kind,
        status: args.status ?? "running",
        phase: trimLabel(args.phase, 80),
        progress: typeof args.progress === "number" ? Math.max(0, Math.min(100, args.progress)) : undefined,
        machineType: trimLabel((machine as any).machineType, 40),
        targetDeviceId: trimLabel((machine as any).deviceId, 120),
        reason: trimLabel(args.reason, 240),
        error: trimLabel(args.error, 240),
        provider: trimLabel(args.provider ?? (machine as any).provider ?? "hetzner", 40),
        providerResourceId: trimLabel(args.providerResourceId ?? (machine as any).cloudResourceId ?? (machine as any).hetznerServerId, 120),
        providerActionId: trimLabel(args.providerActionId, 120),
        providerStatus: trimLabel(args.providerStatus ?? (machine as any).providerStatus, 80),
        dryRun: args.dryRun,
        startedAt: Date.now(),
        updatedAt: Date.now(),
        completedAt: args.status === "succeeded" || args.status === "failed" || args.status === "cancelled" ? Date.now() : undefined,
      });
      return { ok: true, inserted: true };
    }
    const patch: Record<string, unknown> = { updatedAt: Date.now() };
    if (args.status) patch.status = args.status;
    if (args.phase) patch.phase = trimLabel(args.phase, 80);
    if (typeof args.progress === "number") patch.progress = Math.max(0, Math.min(100, args.progress));
    if (args.reason) patch.reason = trimLabel(args.reason, 240);
    if (args.error) patch.error = trimLabel(args.error, 240);
    if (args.provider) patch.provider = trimLabel(args.provider, 40);
    if (args.providerResourceId) patch.providerResourceId = trimLabel(args.providerResourceId, 120);
    if (args.providerActionId) patch.providerActionId = trimLabel(args.providerActionId, 120);
    if (args.providerStatus) patch.providerStatus = trimLabel(args.providerStatus, 80);
    if (typeof args.dryRun === "boolean") patch.dryRun = args.dryRun;
    if (args.status === "succeeded" || args.status === "failed" || args.status === "cancelled") {
      patch.completedAt = Date.now();
    }
    await ctx.db.patch(existing._id, patch);
    return { ok: true, id: existing._id };
  },
});

export const latestForMachine = internalQuery({
  args: { machineId: v.id("cloudMachines"), limit: v.optional(v.number()) },
  handler: async (ctx, args) => {
    return await ctx.db
      .query("wakeRuns")
      .withIndex("by_machine_started", (q: any) => q.eq("machineId", args.machineId))
      .order("desc")
      .take(Math.max(1, Math.min(20, args.limit ?? 5)));
  },
});

export const listRecent = query({
  args: {
    tokenHash: v.string(),
    limit: v.optional(v.number()),
  },
  handler: async (ctx, args) => {
    const userId = await userFromToken(ctx, args.tokenHash);
    const rows = await ctx.db
      .query("wakeRuns")
      .withIndex("by_user_started", (q: any) => q.eq("userId", userId))
      .order("desc")
      .take(Math.max(1, Math.min(50, args.limit ?? 20)));
    return rows.map((row: any) => ({
      id: row._id,
      machineId: row.machineId,
      placementId: row.placementId ?? null,
      taskId: row.taskId ?? null,
      kind: row.kind,
      status: row.status,
      phase: row.phase ?? null,
      progress: row.progress ?? null,
      resourceClass: row.resourceClass ?? null,
      machineType: row.machineType ?? null,
      targetDeviceId: row.targetDeviceId ?? null,
      reason: row.reason ?? null,
      error: row.error ?? null,
      provider: row.provider ?? null,
      providerResourceId: row.providerResourceId ?? null,
      providerActionId: row.providerActionId ?? null,
      providerStatus: row.providerStatus ?? null,
      dryRun: row.dryRun ?? null,
      startedAt: row.startedAt,
      updatedAt: row.updatedAt,
      completedAt: row.completedAt ?? null,
    }));
  },
});
