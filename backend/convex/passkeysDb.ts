// Passkey DB ops — queries and mutations only. Lives next to
// passkeys.ts (which is a "use node" actions file) because Convex
// disallows queries/mutations in Node-runtime modules.

import { mutation, query } from "./_generated/server";
import { v } from "convex/values";

// ── Challenges ──────────────────────────────────────────────────────

export const recordChallenge = mutation({
  args: {
    challenge: v.string(),
    purpose: v.union(v.literal("register"), v.literal("login")),
    userId: v.optional(v.id("users")),
  },
  handler: async (ctx, args) => {
    // 5-minute TTL is more than enough for a Touch ID prompt and
    // short enough that a leaked challenge can't be replayed later.
    const expiresAt = Date.now() + 5 * 60 * 1000;
    await ctx.db.insert("passkeyChallenges", {
      challenge: args.challenge,
      purpose: args.purpose,
      userId: args.userId,
      expiresAt,
      createdAt: Date.now(),
    });
  },
});

export const findChallenge = query({
  args: {
    challenge: v.string(),
    purpose: v.union(v.literal("register"), v.literal("login")),
  },
  handler: async (ctx, args) => {
    const row = await ctx.db
      .query("passkeyChallenges")
      .withIndex("by_challenge", (q) => q.eq("challenge", args.challenge))
      .unique();
    if (!row) return null;
    if (row.purpose !== args.purpose) return null;
    if (row.expiresAt < Date.now()) return null;
    return { _id: row._id, userId: row.userId ?? null };
  },
});

export const consumeChallenge = mutation({
  args: { challenge: v.string() },
  handler: async (ctx, args) => {
    const row = await ctx.db
      .query("passkeyChallenges")
      .withIndex("by_challenge", (q) => q.eq("challenge", args.challenge))
      .unique();
    if (row) await ctx.db.delete(row._id);
  },
});

// Sweep stale challenges. Called from the existing cleanup cron so we
// don't accumulate 5-minute rows indefinitely. Safe to call on a
// schedule even when nobody is registering.
export const sweepExpiredChallenges = mutation({
  args: {},
  handler: async (ctx) => {
    const now = Date.now();
    const stale = await ctx.db
      .query("passkeyChallenges")
      .withIndex("by_expiresAt", (q) => q.lt("expiresAt", now))
      .take(200);
    for (const row of stale) await ctx.db.delete(row._id);
    return { deleted: stale.length };
  },
});

// ── Credentials ─────────────────────────────────────────────────────

export const listForUser = query({
  args: { userId: v.id("users") },
  handler: async (ctx, args) => {
    const rows = await ctx.db
      .query("passkeys")
      .withIndex("by_userId", (q) => q.eq("userId", args.userId))
      .collect();
    return rows.map((row) => ({
      _id: row._id,
      credentialId: row.credentialId,
      transports: row.transports ?? null,
      deviceLabel: row.deviceLabel ?? null,
      backedUp: row.backedUp ?? null,
      createdAt: row.createdAt,
      lastUsedAt: row.lastUsedAt ?? null,
    }));
  },
});

export const insertCredential = mutation({
  args: {
    userId: v.id("users"),
    credentialId: v.string(),
    publicKey: v.string(),
    counter: v.number(),
    transports: v.optional(v.array(v.string())),
    deviceLabel: v.optional(v.string()),
    backedUp: v.optional(v.boolean()),
  },
  handler: async (ctx, args) => {
    const existing = await ctx.db
      .query("passkeys")
      .withIndex("by_credentialId", (q) => q.eq("credentialId", args.credentialId))
      .unique();
    if (existing) throw new Error("CREDENTIAL_EXISTS");
    return await ctx.db.insert("passkeys", {
      userId: args.userId,
      credentialId: args.credentialId,
      publicKey: args.publicKey,
      counter: args.counter,
      transports: args.transports,
      deviceLabel: args.deviceLabel,
      backedUp: args.backedUp,
      createdAt: Date.now(),
    });
  },
});

export const findByCredentialId = query({
  args: { credentialId: v.string() },
  handler: async (ctx, args) => {
    const row = await ctx.db
      .query("passkeys")
      .withIndex("by_credentialId", (q) => q.eq("credentialId", args.credentialId))
      .unique();
    if (!row) return null;
    return {
      _id: row._id,
      userId: row.userId,
      credentialId: row.credentialId,
      publicKey: row.publicKey,
      counter: row.counter,
      transports: row.transports ?? null,
    };
  },
});

export const touchCredential = mutation({
  args: { credentialId: v.string(), counter: v.number() },
  handler: async (ctx, args) => {
    const row = await ctx.db
      .query("passkeys")
      .withIndex("by_credentialId", (q) => q.eq("credentialId", args.credentialId))
      .unique();
    if (!row) return;
    await ctx.db.patch(row._id, {
      counter: args.counter,
      lastUsedAt: Date.now(),
    });
  },
});

export const removeCredential = mutation({
  args: { userId: v.id("users"), credentialId: v.string() },
  handler: async (ctx, args) => {
    const row = await ctx.db
      .query("passkeys")
      .withIndex("by_credentialId", (q) => q.eq("credentialId", args.credentialId))
      .unique();
    if (!row) return { ok: false };
    if (row.userId !== args.userId) throw new Error("NOT_OWNER");
    await ctx.db.delete(row._id);
    return { ok: true };
  },
});
