// convex/users.ts — User profile mutations and queries

import { mutation, query } from "./_generated/server";
import { v } from "convex/values";

// ── Queries ───────────────────────────────────────────────────────────────────

/** Get a user by their Clerk ID */
export const getUserByClerkId = query({
  args: { clerkId: v.string() },
  handler: async (ctx, { clerkId }) => {
    return await ctx.db
      .query("users")
      .withIndex("by_clerkId", q => q.eq("clerkId", clerkId))
      .unique();
  },
});

/** Get top ELO leaderboard */
export const getLeaderboard = query({
  args: { limit: v.optional(v.number()) },
  handler: async (ctx, { limit }) => {
    return await ctx.db
      .query("users")
      .withIndex("by_elo")
      .order("desc")
      .take(limit ?? 50);
  },
});

/** Get a user's game history */
export const getUserGameHistory = query({
  args: { userId: v.id("users"), limit: v.optional(v.number()) },
  handler: async (ctx, { userId, limit }) => {
    // Returns games where the user was player one or two
    // Note: Full implementation will need to query both sides
    return await ctx.db
      .query("games")
      .filter(q =>
        q.or(
          q.eq(q.field("playerOneId"), userId),
          q.eq(q.field("playerTwoId"), userId)
        )
      )
      .order("desc")
      .take(limit ?? 20);
  },
});

// ── Mutations ─────────────────────────────────────────────────────────────────

/** Create or update a user profile (called on login) */
export const upsertUser = mutation({
  args: {
    clerkId: v.string(),
    username: v.string(),
    email: v.string(),
    avatarUrl: v.optional(v.string()),
  },
  handler: async (ctx, { clerkId, username, email, avatarUrl }) => {
    const existing = await ctx.db
      .query("users")
      .withIndex("by_clerkId", q => q.eq("clerkId", clerkId))
      .unique();

    if (existing) {
      await ctx.db.patch(existing._id, { username, email, avatarUrl });
      return existing._id;
    }

    return await ctx.db.insert("users", {
      clerkId,
      username,
      email,
      elo: 1200,
      gamesPlayed: 0,
      gamesWon: 0,
      isBanned: false,
      avatarUrl,
    });
  },
});

/** Update ELO after a game — called from Convex action post-game */
export const updateElo = mutation({
  args: {
    winnerId: v.id("users"),
    loserId: v.id("users"),
    winnerNewElo: v.number(),
    loserNewElo: v.number(),
  },
  handler: async (ctx, { winnerId, loserId, winnerNewElo, loserNewElo }) => {
    const winner = await ctx.db.get(winnerId);
    const loser = await ctx.db.get(loserId);
    if (!winner || !loser) throw new Error("User not found");

    await ctx.db.patch(winnerId, {
      elo: winnerNewElo,
      gamesPlayed: winner.gamesPlayed + 1,
      gamesWon: winner.gamesWon + 1,
    });
    await ctx.db.patch(loserId, {
      elo: loserNewElo,
      gamesPlayed: loser.gamesPlayed + 1,
    });
  },
});
