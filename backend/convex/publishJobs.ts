// Publish-job queue — Convex side.
//
// Pairs with desktop/agent/publish_worker.go. The CLI / mobile / web
// enqueue a publish job for a Mac-farm node via `queuePublishJob`;
// that node's heartbeat loop calls `claimNextPublishJob`, runs the
// build (its own local /deploy/ship), keeps `lastProgressAt` fresh
// while the 15-20 min archive runs, and finally calls
// `reportPublishJobResult`. The UI watches `listPublishJobsForOwner`.
//
// This is a deliberate copy of the agentRescue 3-tier pattern
// (queue → claim → report) — same security model, same atomic-claim
// semantics — because that pattern is proven and survives a wedged
// relay tunnel (Convex is plain HTTPS).
//
// Security model (identical to agentRescue):
//   - Only the target device's owner can queue. Caller is resolved
//     via session tokenHash; reject if device.userId !== caller.
//   - The agent authenticates with its own session token;
//     claimNextPublishJob only returns rows whose ownerUserId matches
//     the agent's session user, so a leaked deviceId alone can't
//     claim someone else's job.
//   - Atomic claim: queued → claimed inside one mutation.
//
// Privacy contract (convex_privacy_test.go): NO path, NO build logs,
// NO secrets ever land here. Only app NAME + targets + stack. The
// farm node resolves the path locally from the app name.

import { v } from "convex/values";
import { mutation, query } from "./_generated/server";
import { validateSessionInternal } from "./auth";

// A queued job must be picked up within this window or it's reaped.
// Picking up is fast (heartbeat cadence ~30 s); the long part is the
// build itself, which is governed by RUNNING_GRACE_MS below.
const CLAIM_TTL_MS = 10 * 60 * 1000; // 10 min to be claimed

// Once claimed/running, the worker refreshes lastProgressAt on every
// heartbeat. If it goes silent longer than this the job is considered
// dead (crashed worker, killed box) and reaped. A cold iOS archive on
// an 8 GB box can take ~20 min, so the grace is generous.
const RUNNING_GRACE_MS = 35 * 60 * 1000; // 35 min of silence = dead

type PublishJobStatus =
  | "queued"
  | "claimed"
  | "running"
  | "done"
  | "failed"
  | "expired";

/**
 * Owner enqueues a publish job for one of their Mac-farm nodes.
 *
 * Dedupes against a still-live queued/claimed/running job with the
 * same (deviceId, app, sorted targets) — a second "Publish" tap while
 * one is already in flight returns the existing job instead of
 * spawning a duplicate build.
 */
export const queuePublishJob = mutation({
  args: {
    tokenHash: v.string(),
    deviceId: v.string(),
    app: v.string(),
    stack: v.string(),
    targets: v.array(v.string()),
    sourceSurface: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) {
      throw new Error("invalid or expired session");
    }
    const callerUserId = session.user._id;

    const device = await ctx.db
      .query("devices")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .first();
    if (!device) {
      throw new Error(`device ${args.deviceId} not found`);
    }
    if (device.userId !== callerUserId) {
      throw new Error("only the device's owner can queue a publish job");
    }

    // ── Billing gate SEAM (deliberately inert) ────────────────────
    // The farm node here is the user's OWN self-hosted Mac. Per the
    // business model (launch is free + open source; LemonSqueezy /
    // managed billing is dormant-by-design, paid features post-YC),
    // the self-hosted floor must NEVER be paywalled — gating publish-
    // to-your-own-Mac behind a subscription would break the free
    // launch. So this passes unconditionally for an owned device.
    //
    // This is the SINGLE labelled place a future *Yaver-managed* farm
    // (Yaver-owned Mac hardware, like cloudMachines) would enforce
    // subscriptions.canProvisionManaged — fail-closed, with the owner
    // allowlist bypass — mirroring managed cloud. A managed farm node
    // would carry an `origin: "managed"` marker on its device/farm row;
    // until that SKU exists there is nothing to check. Do NOT wire
    // canProvisionManaged here for self-hosted nodes.
    const isManagedFarmNode = false; // future: device.origin === "managed"
    if (isManagedFarmNode) {
      // future: enforce subscriptions.canProvisionManaged for the
      // Yaver-owned managed-farm SKU. Intentionally unreachable today.
      throw new Error("managed farm not yet available");
    }

    const targets = [...args.targets]
      .map((t) => t.trim())
      .filter((t) => t.length > 0);
    if (targets.length === 0) {
      throw new Error("at least one target is required");
    }
    const targetKey = [...targets].sort().join(",");

    const now = Date.now();

    // Dedup: a live job for the same device + app + target set.
    const liveStatuses: PublishJobStatus[] = ["queued", "claimed", "running"];
    for (const st of liveStatuses) {
      const rows = await ctx.db
        .query("publishJobs")
        .withIndex("by_device_status", (q) =>
          q.eq("deviceId", args.deviceId).eq("status", st),
        )
        .collect();
      for (const row of rows) {
        if (
          row.app === args.app &&
          [...row.targets].sort().join(",") === targetKey &&
          row.expiresAt > now
        ) {
          return { jobId: row.jobId, deduped: true };
        }
      }
    }

    // Agent-friendly external id (the agent reports against this, not
    // the Convex _id, so the report path doesn't need an _id round-trip).
    const jobId = `pj_${now.toString(36)}_${Math.random()
      .toString(36)
      .slice(2, 8)}`;

    await ctx.db.insert("publishJobs", {
      jobId,
      deviceId: args.deviceId,
      ownerUserId: callerUserId,
      app: args.app,
      stack: args.stack,
      targets,
      status: "queued",
      createdAt: now,
      expiresAt: now + CLAIM_TTL_MS,
      sourceSurface: args.sourceSurface,
    });

    return { jobId, deduped: false };
  },
});

/**
 * Farm node claims the next queued job for its own device.
 *
 * Also reaps the dead: queued rows past CLAIM_TTL and claimed/running
 * rows silent past RUNNING_GRACE flip to "expired" so the queue never
 * wedges. Atomic queued → claimed. Returns null when idle (the
 * heartbeat loop calls this every tick; empty is the common case).
 */
export const claimNextPublishJob = mutation({
  args: {
    tokenHash: v.string(),
    deviceId: v.string(),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) {
      throw new Error("invalid or expired session");
    }
    const callerUserId = session.user._id;

    const device = await ctx.db
      .query("devices")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .first();
    if (!device) {
      throw new Error(`device ${args.deviceId} not registered`);
    }
    if (device.userId !== callerUserId) {
      throw new Error("agent token does not own this device");
    }

    const now = Date.now();

    // Reap dead claimed/running jobs (crashed worker) first so a
    // wedged build doesn't block the queue forever.
    for (const st of ["claimed", "running"] as PublishJobStatus[]) {
      const stuck = await ctx.db
        .query("publishJobs")
        .withIndex("by_device_status", (q) =>
          q.eq("deviceId", args.deviceId).eq("status", st),
        )
        .collect();
      for (const row of stuck) {
        const last = row.lastProgressAt ?? row.claimedAt ?? row.createdAt;
        if (now - last > RUNNING_GRACE_MS) {
          await ctx.db.patch(row._id, {
            status: "expired",
            finishedAt: now,
            message: "worker went silent — reaped",
          });
        }
      }
    }

    const queued = await ctx.db
      .query("publishJobs")
      .withIndex("by_device_status", (q) =>
        q.eq("deviceId", args.deviceId).eq("status", "queued"),
      )
      .order("asc") // oldest first (FIFO)
      .collect();

    for (const row of queued) {
      if (row.expiresAt <= now) {
        await ctx.db.patch(row._id, {
          status: "expired",
          finishedAt: now,
          message: "not claimed in time",
        });
        continue;
      }
      await ctx.db.patch(row._id, {
        status: "claimed",
        claimedAt: now,
        lastProgressAt: now,
      });
      return {
        jobId: row.jobId,
        app: row.app,
        stack: row.stack,
        targets: row.targets,
      };
    }
    return null;
  },
});

/**
 * Worker heartbeat for a long-running build: flips claimed → running
 * (idempotent) and refreshes lastProgressAt so the reaper doesn't kill
 * a healthy 20-min archive. `message` is a short status line ("xcode
 * archive…", "uploading…") — never log output.
 */
export const reportPublishJobProgress = mutation({
  args: {
    tokenHash: v.string(),
    jobId: v.string(),
    message: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) {
      throw new Error("invalid or expired session");
    }
    const row = await ctx.db
      .query("publishJobs")
      .withIndex("by_jobId", (q) => q.eq("jobId", args.jobId))
      .first();
    if (!row) {
      return { ok: true, missing: true };
    }
    if (row.ownerUserId !== session.user._id) {
      throw new Error("agent token does not own this job");
    }
    if (row.status !== "claimed" && row.status !== "running") {
      return { ok: true, alreadyTerminal: true };
    }
    await ctx.db.patch(row._id, {
      status: "running",
      lastProgressAt: Date.now(),
      message: args.message,
    });
    return { ok: true };
  },
});

/**
 * Worker reports the terminal outcome. `result` is per-target
 * metadata only (target / ok / exitCode / errorClass / durationMs) —
 * the same shape /deploy/ship's composite summary emits. NO logs.
 */
export const reportPublishJobResult = mutation({
  args: {
    tokenHash: v.string(),
    jobId: v.string(),
    status: v.union(v.literal("done"), v.literal("failed")),
    result: v.optional(
      v.array(
        v.object({
          target: v.string(),
          ok: v.boolean(),
          exitCode: v.number(),
          errorClass: v.optional(v.string()),
          durationMs: v.optional(v.number()),
        }),
      ),
    ),
    message: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) {
      throw new Error("invalid or expired session");
    }
    const row = await ctx.db
      .query("publishJobs")
      .withIndex("by_jobId", (q) => q.eq("jobId", args.jobId))
      .first();
    if (!row) {
      return { ok: true, missing: true };
    }
    if (row.ownerUserId !== session.user._id) {
      throw new Error("agent token does not own this job");
    }
    if (row.status === "done" || row.status === "failed" || row.status === "expired") {
      return { ok: true, alreadyTerminal: true };
    }
    await ctx.db.patch(row._id, {
      status: args.status,
      result: args.result,
      message: args.message,
      finishedAt: Date.now(),
    });
    return { ok: true };
  },
});

/**
 * UI / CLI subscription — recent publish jobs for the caller. Owner
 * only. Optionally filtered to one device. Default limit 20.
 */
export const listPublishJobsForOwner = query({
  args: {
    tokenHash: v.string(),
    deviceId: v.optional(v.string()),
    limit: v.optional(v.number()),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) {
      return null;
    }
    const limit = Math.max(1, Math.min(args.limit ?? 20, 100));
    const all = await ctx.db
      .query("publishJobs")
      .withIndex("by_owner", (q) => q.eq("ownerUserId", session.user._id))
      .collect();
    return all
      .filter((row) => !args.deviceId || row.deviceId === args.deviceId)
      .sort((a, b) => b.createdAt - a.createdAt)
      .slice(0, limit)
      .map((row) => ({
        jobId: row.jobId,
        deviceId: row.deviceId,
        app: row.app,
        stack: row.stack,
        targets: row.targets,
        status: row.status,
        result: row.result,
        message: row.message,
        createdAt: row.createdAt,
        claimedAt: row.claimedAt,
        lastProgressAt: row.lastProgressAt,
        finishedAt: row.finishedAt,
        sourceSurface: row.sourceSurface,
      }));
  },
});
