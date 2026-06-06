// GPU-rental orchestration bookkeeping mutations/queries, called by the Yaver
// agent (desktop/agent/gpu_rental_sync.go) to give the dashboard cross-device
// visibility of which dispatcher box rented which burst GPU / bound which
// serverless inference for an app (the call-center, or any GPU-bursting app).
//
// PRIVACY: these rows are bookkeeping ONLY — provider + opaque resource id +
// kind + gpu class + the PUBLIC inference endpoint (host-only, no key) + model
// id + the vault PROJECT NAME (never its values) + voiceSafe + status + usage
// counters. No API keys, vault values, prompts, call data, or absolute paths
// ever reach here; the agent strips them via buildGpuRentalUpsertPayload and
// desktop/agent/convex_privacy_test.go pins it. See docs/gpu-rental-orchestration.md.

import { v } from "convex/values";
import { mutation, query } from "./_generated/server";
import { resolveUser } from "./agentSync";

/** Upsert a GPU rental's bookkeeping row, keyed by (deviceId, resourceId). */
export const upsertGpuRental = mutation({
  args: {
    deviceId: v.string(),
    provider: v.string(),
    resourceId: v.string(),
    kind: v.string(),
    gpuClass: v.optional(v.string()),
    endpoint: v.optional(v.string()),
    model: v.optional(v.string()),
    bindProject: v.optional(v.string()),
    voiceSafe: v.optional(v.boolean()),
    status: v.string(),
    hoursUsed: v.optional(v.number()),
    tokensUsed: v.optional(v.number()),
    costCents: v.optional(v.number()),
  },
  handler: async (ctx, args) => {
    const userId = await resolveUser(ctx);
    const existing = await ctx.db
      .query("gpuRentals")
      .withIndex("by_device_resource", (q) =>
        q.eq("deviceId", args.deviceId).eq("resourceId", args.resourceId),
      )
      .first();
    const patch = { ...args, userId, updatedAt: Date.now() };
    if (existing) {
      await ctx.db.patch(existing._id, patch);
      return existing._id;
    }
    return ctx.db.insert("gpuRentals", patch);
  },
});

/** Mark a rental stopped (e.g. on reap) without resending the whole row. */
export const setGpuRentalStatus = mutation({
  args: { deviceId: v.string(), resourceId: v.string(), status: v.string() },
  handler: async (ctx, args) => {
    await resolveUser(ctx);
    const existing = await ctx.db
      .query("gpuRentals")
      .withIndex("by_device_resource", (q) =>
        q.eq("deviceId", args.deviceId).eq("resourceId", args.resourceId),
      )
      .first();
    if (existing) {
      await ctx.db.patch(existing._id, { status: args.status, updatedAt: Date.now() });
    }
    return null;
  },
});

/** List the signed-in user's GPU rentals across all their dispatcher boxes. */
export const listForUser = query({
  args: {},
  handler: async (ctx) => {
    const userId = await resolveUser(ctx);
    return ctx.db
      .query("gpuRentals")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .collect();
  },
});
