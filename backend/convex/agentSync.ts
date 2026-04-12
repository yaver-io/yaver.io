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
    path: v.string(),
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
    const patch = { ...args, userId, updatedAt: Date.now() };
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

async function resolveUser(ctx: any): Promise<Id<"users">> {
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
