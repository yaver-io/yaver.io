// Rescue command queue — Convex side.
//
// This is the only control channel that survives a broken relay
// tunnel. The agent's heartbeat (independent of the relay) polls
// `claimNextRescueCommand` to fetch its next pending command, runs
// it, then calls `reportRescueResult`. The web UI / mobile / CLI
// queue commands via `queueRescueCommand` and watch live status via
// `listRescueCommandsForDevice`.
//
// Security model:
//
//   - Only the device's owner can queue. We resolve the caller via
//     their session tokenHash, fetch the device by deviceId, and
//     reject if device.userId !== caller.userId.
//   - The agent authenticates the same way (its own session token).
//     `claimNextRescueCommand` only returns commands whose
//     ownerUserId matches the agent's session user — so a stolen
//     deviceId by itself is not enough to claim somebody else's
//     command.
//   - Single-claim semantics: claim atomically transitions
//     pending → claimed. A second claim of the same command fails.
//   - 5 minute TTL on every command — caps the replay window.
//   - The `command` field is a strict enum at the schema level
//     (see schema.ts agentRescueCommands.command). The agent's
//     dispatcher must mirror this enum; adding a new command
//     requires bumping both ends.

import { v } from "convex/values";
import { mutation, query } from "./_generated/server";
import { Id } from "./_generated/dataModel";
import { validateSessionInternal } from "./auth";

const RESCUE_TTL_MS = 5 * 60 * 1000; // 5 min

const RESCUE_COMMANDS = [
  "restart",
  "reinstall-latest",
  "tunnel-reset",
  "auth-reset",
] as const;
type RescueCommand = (typeof RESCUE_COMMANDS)[number];

/**
 * Owner queues a rescue command for one of their devices.
 *
 * Returns the new command's _id. Dedupes against a pending command
 * with the same (deviceId, command) — a second click within the TTL
 * window returns the existing pending row instead of creating a
 * duplicate, so impatient users don't pile up restarts.
 */
export const queueRescueCommand = mutation({
  args: {
    tokenHash: v.string(),
    deviceId: v.string(),
    command: v.union(
      v.literal("restart"),
      v.literal("reinstall-latest"),
      v.literal("tunnel-reset"),
      v.literal("auth-reset"),
    ),
    params: v.optional(v.object({
      version: v.optional(v.string()),
    })),
    sourceSurface: v.optional(v.string()), // "web" | "mobile" | "cli"
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
      throw new Error("only the device's owner can issue rescue commands");
    }

    // Dedup: any pending command for this device + command type that
    // hasn't expired. Returns its existing id instead of inserting.
    const now = Date.now();
    const existingPending = await ctx.db
      .query("agentRescueCommands")
      .withIndex("by_device_status", (q) =>
        q.eq("deviceId", args.deviceId).eq("status", "pending")
      )
      .collect();
    for (const row of existingPending) {
      if (row.command === args.command && row.expiresAt > now) {
        return { commandId: row._id, deduped: true };
      }
    }

    const commandId = await ctx.db.insert("agentRescueCommands", {
      deviceId: args.deviceId,
      ownerUserId: callerUserId,
      command: args.command as RescueCommand,
      params: args.params,
      status: "pending",
      createdAt: now,
      expiresAt: now + RESCUE_TTL_MS,
      sourceSurface: args.sourceSurface,
    });

    return { commandId, deduped: false };
  },
});

/**
 * Agent claims the next pending rescue command for its own device.
 *
 * Atomic: the row's status flips pending → claimed inside this
 * mutation, so a parallel call returns null. The 5-minute TTL is
 * checked first; expired pending rows are flipped to "expired" and
 * skipped.
 *
 * Returns null when there's nothing to do — the agent's heartbeat
 * loop calls this every ~30 s, so noise is fine.
 */
export const claimNextRescueCommand = mutation({
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
    const pending = await ctx.db
      .query("agentRescueCommands")
      .withIndex("by_device_status", (q) =>
        q.eq("deviceId", args.deviceId).eq("status", "pending")
      )
      .order("asc") // oldest first
      .collect();

    for (const row of pending) {
      if (row.expiresAt <= now) {
        await ctx.db.patch(row._id, { status: "expired" });
        continue;
      }
      // Atomic claim. Inside a Convex mutation patches are serial;
      // a concurrent claim caller will see the new "claimed" status.
      await ctx.db.patch(row._id, {
        status: "claimed",
        claimedAt: now,
      });
      return {
        commandId: row._id,
        command: row.command,
        params: row.params,
      };
    }
    return null;
  },
});

/**
 * Agent reports the outcome of a claimed command. Status must be
 * "completed" or "failed". `result` is a short message — stdout
 * tail, exit code, or error string. The agent sets a small cap
 * (~2 KB) before sending so we don't carry log volume in Convex.
 */
export const reportRescueResult = mutation({
  args: {
    tokenHash: v.string(),
    commandId: v.id("agentRescueCommands"),
    status: v.union(v.literal("completed"), v.literal("failed")),
    result: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) {
      throw new Error("invalid or expired session");
    }
    const row = await ctx.db.get(args.commandId);
    if (!row) {
      // Already gone (cleanup) — silent success so agent retries
      // don't wedge.
      return { ok: true, missing: true };
    }
    if (row.ownerUserId !== session.user._id) {
      throw new Error("agent token does not own this command");
    }
    if (row.status !== "claimed") {
      // Idempotent — already completed/expired.
      return { ok: true, alreadyTerminal: true };
    }
    await ctx.db.patch(args.commandId, {
      status: args.status,
      result: args.result,
      completedAt: Date.now(),
    });
    return { ok: true };
  },
});

/**
 * UI subscription — list recent rescue commands for one device.
 * Owner-only. Limit defaults to 10 (most recent).
 */
export const listRescueCommandsForDevice = query({
  args: {
    tokenHash: v.string(),
    deviceId: v.string(),
    limit: v.optional(v.number()),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) {
      return null;
    }
    const device = await ctx.db
      .query("devices")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .first();
    if (!device || device.userId !== session.user._id) {
      return null;
    }
    const limit = Math.max(1, Math.min(args.limit ?? 10, 50));
    // Pull from both pending and terminal buckets.
    const all = await ctx.db
      .query("agentRescueCommands")
      .withIndex("by_owner", (q) => q.eq("ownerUserId", session.user._id))
      .collect();
    return all
      .filter((row) => row.deviceId === args.deviceId)
      .sort((a, b) => b.createdAt - a.createdAt)
      .slice(0, limit)
      .map((row) => ({
        _id: row._id,
        command: row.command,
        params: row.params,
        status: row.status,
        result: row.result,
        createdAt: row.createdAt,
        claimedAt: row.claimedAt,
        completedAt: row.completedAt,
        sourceSurface: row.sourceSurface,
      }));
  },
});
