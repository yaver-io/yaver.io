// convex/matchmaking.ts — Matchmaking queue logic

import { mutation, query } from "./_generated/server";
import { v } from "convex/values";

const ELO_RANGE = 200; // Initial matchmaking ELO spread

/** Get the current matchmaking queue */
export const getMatchmakingQueue = query({
  args: {},
  handler: async (ctx) => {
    return await ctx.db
      .query("matchmakingQueue")
      .withIndex("by_joinedAt")
      .order("asc")
      .take(50);
  },
});

/** Join the matchmaking queue */
export const joinQueue = mutation({
  args: {
    userId: v.id("users"),
    userElo: v.number(),
  },
  handler: async (ctx, { userId, userElo }) => {
    // Remove any existing queue entry for this user
    const existing = await ctx.db
      .query("matchmakingQueue")
      .withIndex("by_user", q => q.eq("userId", userId))
      .unique();
    if (existing) await ctx.db.delete(existing._id);

    // Add to queue
    await ctx.db.insert("matchmakingQueue", {
      userId,
      eloMin: userElo - ELO_RANGE,
      eloMax: userElo + ELO_RANGE,
      joinedAt: Date.now(),
    });
  },
});

/** Leave the matchmaking queue */
export const leaveQueue = mutation({
  args: { userId: v.id("users") },
  handler: async (ctx, { userId }) => {
    const entry = await ctx.db
      .query("matchmakingQueue")
      .withIndex("by_user", q => q.eq("userId", userId))
      .unique();
    if (entry) await ctx.db.delete(entry._id);
  },
});
