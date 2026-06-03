// Mutations called by the Yaver agent to keep its local state in sync with
// the yaver.io dashboard's Convex backend. All of these authenticate via the
// bearer token header (the user's auth token) and resolve userId from that.

import { v } from "convex/values";
import { mutation, query } from "./_generated/server";
import type { Id } from "./_generated/dataModel";

// ---- Projects ----

export const upsertProject = mutation({
  args: {
    deviceId: v.string(),
    slug: v.string(),
    // path was removed — absolute paths contain the user's home-dir
    // username; the privacy contract keeps them on the agent. Legacy
    // agents that still send it are tolerated via v.optional below.
    path: v.optional(v.string()),
    name: v.string(),
    stack: v.optional(v.string()),
    backend: v.optional(v.string()),
    auth: v.optional(v.string()),
    activeEnv: v.optional(v.string()),
    localPort: v.optional(v.number()),
    tunnelUrl: v.optional(v.string()),
    gitBranch: v.optional(v.string()),
    lastCommit: v.optional(v.string()),
    status: v.union(
      v.literal("running"),
      v.literal("stopped"),
      v.literal("error"),
      v.literal("creating"),
    ),
  },
  handler: async (ctx, args) => {
    const userId = await resolveUser(ctx);
    const existing = await ctx.db
      .query("userProjects")
      .withIndex("by_user_slug", (q) => q.eq("userId", userId).eq("slug", args.slug))
      .first();
    // Never store the absolute path. A legacy agent that still sends
    // one gets silently stripped at the mutation boundary.
    const { path: _ignored, ...rest } = args;
    void _ignored;
    const patch = { ...rest, userId, updatedAt: Date.now() };
    if (existing) {
      await ctx.db.patch(existing._id, patch);
      return existing._id;
    }
    return ctx.db.insert("userProjects", patch);
  },
});

export const listMyProjects = query({
  args: {},
  handler: async (ctx) => {
    const userId = await resolveUser(ctx);
    return ctx.db
      .query("userProjects")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .order("desc")
      .take(50);
  },
});

export const deleteProject = mutation({
  args: { slug: v.string() },
  handler: async (ctx, { slug }) => {
    const userId = await resolveUser(ctx);
    const rows = await ctx.db
      .query("userProjects")
      .withIndex("by_user_slug", (q) => q.eq("userId", userId).eq("slug", slug))
      .collect();
    for (const r of rows) await ctx.db.delete(r._id);
  },
});

// ---- Services ----

export const upsertServices = mutation({
  args: {
    deviceId: v.string(),
    services: v.array(v.object({
      name: v.string(),
      image: v.optional(v.string()),
      port: v.number(),
      status: v.string(),
      projectSlug: v.optional(v.string()),
      cpuPercent: v.optional(v.number()),
      ramMB: v.optional(v.number()),
    })),
  },
  handler: async (ctx, { deviceId, services }) => {
    const userId = await resolveUser(ctx);
    // Wipe existing rows for this device (simple sync — not incremental).
    const existing = await ctx.db
      .query("userServices")
      .withIndex("by_device", (q) => q.eq("deviceId", deviceId))
      .collect();
    for (const r of existing) await ctx.db.delete(r._id);
    for (const s of services) {
      await ctx.db.insert("userServices", { ...s, userId, deviceId, updatedAt: Date.now() });
    }
  },
});

export const listMyServices = query({
  args: {},
  handler: async (ctx) => {
    const userId = await resolveUser(ctx);
    return ctx.db
      .query("userServices")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .order("desc")
      .take(200);
  },
});

// ---- Deployments ----

export const recordDeploy = mutation({
  args: {
    deviceId: v.string(),
    projectSlug: v.string(),
    deployId: v.string(),
    target: v.optional(v.string()),
    environment: v.optional(v.string()),
    status: v.string(),
    commit: v.optional(v.string()),
    message: v.optional(v.string()),
    duration: v.optional(v.string()),
    startedAt: v.number(),
    finishedAt: v.optional(v.number()),
  },
  handler: async (ctx, args) => {
    const userId = await resolveUser(ctx);
    return ctx.db.insert("userDeployments", { ...args, userId });
  },
});

export const listMyDeployments = query({
  args: { projectSlug: v.optional(v.string()) },
  handler: async (ctx, { projectSlug }) => {
    const userId = await resolveUser(ctx);
    if (projectSlug) {
      return ctx.db
        .query("userDeployments")
        .withIndex("by_project", (q) => q.eq("userId", userId).eq("projectSlug", projectSlug))
        .order("desc")
        .take(50);
    }
    return ctx.db
      .query("userDeployments")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .order("desc")
      .take(50);
  },
});

// ---- Activity / audit ----

export const recordActivity = mutation({
  args: {
    deviceId: v.string(),
    action: v.string(),
    target: v.optional(v.string()),
    outcome: v.string(),
    error: v.optional(v.string()),
    timestamp: v.number(),
  },
  handler: async (ctx, args) => {
    const userId = await resolveUser(ctx);
    return ctx.db.insert("userActivity", { ...args, userId });
  },
});

// Batched state sync. One mutation carries every per-tick update in one
// network round-trip — projects, services, and new activity entries —
// instead of N mutations from the agent's convex_state_sync loop. The
// agent also deduplicates unchanged payloads client-side, so a quiet
// box (no new projects, no new audit events) makes zero Convex calls
// on its 60-second tick.
//
// Cost rationale: each agent used to burn ~180 Convex calls/hour on
// state sync alone (3 sub-calls × once per project × 60s). Batching
// drops that to at most 1 call/tick = 60/hour — and typically zero
// because dedup fires first.
export const batchSync = mutation({
  args: {
    deviceId: v.string(),
    projects: v.optional(v.array(v.object({
      slug: v.string(),
      name: v.string(),
      stack: v.optional(v.string()),
      backend: v.optional(v.string()),
      auth: v.optional(v.string()),
      activeEnv: v.optional(v.string()),
      localPort: v.optional(v.number()),
      tunnelUrl: v.optional(v.string()),
      gitBranch: v.optional(v.string()),
      lastCommit: v.optional(v.string()),
      status: v.union(
        v.literal("running"),
        v.literal("stopped"),
        v.literal("error"),
        v.literal("creating"),
      ),
    }))),
    services: v.optional(v.array(v.object({
      name: v.string(),
      image: v.optional(v.string()),
      port: v.number(),
      status: v.string(),
      projectSlug: v.optional(v.string()),
      cpuPercent: v.optional(v.number()),
      ramMB: v.optional(v.number()),
    }))),
    activity: v.optional(v.array(v.object({
      action: v.string(),
      target: v.optional(v.string()),
      outcome: v.string(),
      error: v.optional(v.string()),
      timestamp: v.number(),
    }))),
  },
  handler: async (ctx, args) => {
    const userId = await resolveUser(ctx);
    const now = Date.now();

    for (const p of args.projects ?? []) {
      const existing = await ctx.db
        .query("userProjects")
        .withIndex("by_user_slug", (q) =>
          q.eq("userId", userId).eq("slug", p.slug),
        )
        .first();
      const patch = { ...p, deviceId: args.deviceId, userId, updatedAt: now };
      if (existing) {
        await ctx.db.patch(existing._id, patch);
      } else {
        await ctx.db.insert("userProjects", patch);
      }
    }

    // Services: wipe-and-insert per device, matching upsertServices's
    // behaviour. Whole block is cheap — it's O(N services for this
    // device), same as the legacy path, just rolled into one mutation.
    if (args.services !== undefined) {
      const existing = await ctx.db
        .query("userServices")
        .withIndex("by_device", (q) => q.eq("deviceId", args.deviceId))
        .collect();
      for (const r of existing) await ctx.db.delete(r._id);
      for (const s of args.services) {
        await ctx.db.insert("userServices", {
          ...s, userId, deviceId: args.deviceId, updatedAt: now,
        });
      }
    }

    for (const entry of args.activity ?? []) {
      await ctx.db.insert("userActivity", {
        deviceId: args.deviceId,
        ...entry,
        userId,
      });
    }

    return { ok: true };
  },
});

export const listMyActivity = query({
  args: {},
  handler: async (ctx) => {
    const userId = await resolveUser(ctx);
    return ctx.db
      .query("userActivity")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .order("desc")
      .take(50);
  },
});

// ---- Helpers ----

export async function resolveUser(ctx: any): Promise<Id<"users">> {
  const identity = await ctx.auth.getUserIdentity();
  if (!identity) throw new Error("unauthenticated");
  // Match by email (Yaver's users table uses email as the lookup key).
  const user = await ctx.db
    .query("users")
    .withIndex("by_email", (q: any) => q.eq("email", identity.email))
    .first();
  if (!user) throw new Error("user not found");
  return user._id as Id<"users">;
}
