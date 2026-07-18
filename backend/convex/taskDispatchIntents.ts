// taskDispatchIntents.ts — durable, prompt-free task dispatch queue metadata.
//
// This is NOT a task body store. The actual prompt, command body, workDir,
// files, images, stdout, and secrets remain on the client that will dispatch
// P2P/local once the target workspace is ready. Convex stores only ids,
// placement/target metadata, status, short reasons, and counters.

import { v } from "convex/values";
import { mutation, query } from "./_generated/server";
import type { Id } from "./_generated/dataModel";
import { validateSessionInternal } from "./auth";

const statuses = v.union(
  v.literal("queued"),
  v.literal("dispatching"),
  v.literal("dispatched"),
  v.literal("blocked"),
  v.literal("failed"),
  v.literal("cancelled"),
  v.literal("expired"),
);

type DispatchStatus =
  | "queued"
  | "dispatching"
  | "dispatched"
  | "blocked"
  | "failed"
  | "cancelled"
  | "expired";

async function userFromToken(ctx: any, tokenHash: string): Promise<Id<"users">> {
  const session = await validateSessionInternal(ctx, tokenHash);
  if (!session) throw new Error("Unauthorized");
  return session.user._id;
}

function trimLabel(value: string | undefined, max: number): string | undefined {
  const text = String(value || "").trim();
  return text ? text.slice(0, max) : undefined;
}

function normalizeProjectSlug(slug: string | undefined): string | undefined {
  const s = String(slug || "").trim();
  if (!s || /[\\/]/.test(s)) return undefined;
  return s.slice(0, 80);
}

function setIfDefined<T extends Record<string, any>>(patch: T, key: string, value: any) {
  if (value !== undefined) patch[key as keyof T] = value;
}

function serialize(row: any) {
  return {
    id: row._id,
    localTaskId: row.localTaskId,
    placementId: row.placementId ?? null,
    taskId: row.taskId ?? null,
    sourceSurface: row.sourceSurface ?? null,
    lane: row.lane ?? null,
    targetDeviceId: row.targetDeviceId ?? null,
    cloudMachineId: row.cloudMachineId ? String(row.cloudMachineId) : null,
    requestedRunner: row.requestedRunner ?? null,
    projectSlug: row.projectSlug ?? null,
    status: row.status,
    reason: row.reason ?? null,
    lastError: row.lastError ?? null,
    attempts: row.attempts,
    expiresAt: row.expiresAt,
    createdAt: row.createdAt,
    updatedAt: row.updatedAt,
    completedAt: row.completedAt ?? null,
  };
}

export const create = mutation({
  args: {
    tokenHash: v.string(),
    localTaskId: v.string(),
    placementId: v.optional(v.id("taskPlacements")),
    sourceSurface: v.optional(v.string()),
    lane: v.optional(v.string()),
    targetDeviceId: v.optional(v.string()),
    cloudMachineId: v.optional(v.id("cloudMachines")),
    requestedRunner: v.optional(v.string()),
    projectSlug: v.optional(v.string()),
    reason: v.optional(v.string()),
    ttlMs: v.optional(v.number()),
  },
  handler: async (ctx, args) => {
    const userId = await userFromToken(ctx, args.tokenHash);
    const localTaskId = args.localTaskId.trim();
    if (!localTaskId) throw new Error("localTaskId required");
    if (localTaskId.length > 160) throw new Error("localTaskId too long");
    if (args.placementId) {
      const placement = await ctx.db.get(args.placementId);
      if (!placement || String(placement.userId) !== String(userId)) throw new Error("placement not found");
    }
    const now = Date.now();
    const expiresAt = now + Math.max(5 * 60_000, Math.min(args.ttlMs ?? 24 * 60 * 60_000, 7 * 24 * 60 * 60_000));
    const existing = await ctx.db
      .query("taskDispatchIntents")
      .withIndex("by_local_task", (q: any) => q.eq("localTaskId", localTaskId))
      .first();
    const patch: Record<string, any> = {
      userId,
      localTaskId,
      status: "queued" as DispatchStatus,
      attempts: existing?.attempts ?? 0,
      expiresAt,
      updatedAt: now,
    };
    setIfDefined(patch, "placementId", args.placementId);
    setIfDefined(patch, "sourceSurface", trimLabel(args.sourceSurface, 80));
    setIfDefined(patch, "lane", trimLabel(args.lane, 40));
    setIfDefined(patch, "targetDeviceId", trimLabel(args.targetDeviceId, 160));
    setIfDefined(patch, "cloudMachineId", args.cloudMachineId);
    setIfDefined(patch, "requestedRunner", trimLabel(args.requestedRunner, 80));
    setIfDefined(patch, "projectSlug", normalizeProjectSlug(args.projectSlug));
    setIfDefined(patch, "reason", trimLabel(args.reason, 240));
    if (existing && String(existing.userId) === String(userId)) {
      await ctx.db.patch(existing._id, patch);
      return serialize({ ...existing, ...patch });
    }
    const id = await ctx.db.insert("taskDispatchIntents", {
      ...patch,
      createdAt: now,
    } as any);
    const inserted = await ctx.db.get(id);
    if (!inserted) throw new Error("dispatch intent insert failed");
    return serialize(inserted);
  },
});

export const update = mutation({
  args: {
    tokenHash: v.string(),
    intentId: v.optional(v.id("taskDispatchIntents")),
    localTaskId: v.optional(v.string()),
    status: statuses,
    taskId: v.optional(v.string()),
    targetDeviceId: v.optional(v.string()),
    lastError: v.optional(v.string()),
    reason: v.optional(v.string()),
    bumpAttempt: v.optional(v.boolean()),
  },
  handler: async (ctx, args) => {
    const userId = await userFromToken(ctx, args.tokenHash);
    const row = args.intentId
      ? await ctx.db.get(args.intentId)
      : args.localTaskId
        ? await ctx.db
            .query("taskDispatchIntents")
            .withIndex("by_local_task", (q: any) => q.eq("localTaskId", args.localTaskId!.trim()))
            .first()
        : null;
    if (!row || String(row.userId) !== String(userId)) throw new Error("dispatch intent not found");
    const terminal = args.status === "dispatched" || args.status === "cancelled" || args.status === "expired";
    const now = Date.now();
    const patch: Record<string, any> = {
      status: args.status,
      attempts: args.bumpAttempt ? (row.attempts ?? 0) + 1 : row.attempts ?? 0,
      updatedAt: now,
    };
    setIfDefined(patch, "taskId", trimLabel(args.taskId, 160));
    setIfDefined(patch, "targetDeviceId", trimLabel(args.targetDeviceId, 160));
    setIfDefined(patch, "lastError", trimLabel(args.lastError, 240));
    setIfDefined(patch, "reason", trimLabel(args.reason, 240));
    if (terminal) patch.completedAt = now;
    await ctx.db.patch(row._id, patch);
    return serialize({ ...row, ...patch });
  },
});

export const listRecent = query({
  args: {
    tokenHash: v.string(),
    limit: v.optional(v.number()),
    includeTerminal: v.optional(v.boolean()),
  },
  handler: async (ctx, args) => {
    const userId = await userFromToken(ctx, args.tokenHash);
    const n = Math.max(1, Math.min(100, args.limit ?? 25));
    const rows = await ctx.db
      .query("taskDispatchIntents")
      .withIndex("by_user_created", (q: any) => q.eq("userId", userId))
      .order("desc")
      .take(n);
    const now = Date.now();
    return rows
      .filter((row: any) =>
        args.includeTerminal ||
        (row.expiresAt > now && row.status !== "dispatched" && row.status !== "cancelled" && row.status !== "expired")
      )
      .map(serialize);
  },
});
