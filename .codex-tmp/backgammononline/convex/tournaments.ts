// convex/tournaments.ts — Tournament bracket logic

import { mutation, query } from "./_generated/server";
import { v } from "convex/values";

/** Get all tournaments in registration or active status */
export const getActiveTournaments = query({
  args: {},
  handler: async (ctx) => {
    return await ctx.db
      .query("tournaments")
      .filter(q =>
        q.or(
          q.eq(q.field("status"), "registration"),
          q.eq(q.field("status"), "active")
        )
      )
      .order("asc")
      .take(20);
  },
});

/** Get tournament details */
export const getTournament = query({
  args: { tournamentId: v.id("tournaments") },
  handler: async (ctx, { tournamentId }) => {
    return await ctx.db.get(tournamentId);
  },
});

/** Get tournament entries (participants) */
export const getTournamentEntries = query({
  args: { tournamentId: v.id("tournaments") },
  handler: async (ctx, { tournamentId }) => {
    return await ctx.db
      .query("tournamentEntries")
      .withIndex("by_tournament", q => q.eq("tournamentId", tournamentId))
      .collect();
  },
});

/** Reserve a tournament spot (before payment) */
export const reserveTournamentSpot = mutation({
  args: {
    tournamentId: v.id("tournaments"),
    userId: v.id("users"),
  },
  handler: async (ctx, { tournamentId, userId }) => {
    const tournament = await ctx.db.get(tournamentId);
    if (!tournament || tournament.status !== "registration") {
      throw new Error("Tournament not accepting registrations");
    }

    const entries = await ctx.db
      .query("tournamentEntries")
      .withIndex("by_tournament", q => q.eq("tournamentId", tournamentId))
      .collect();

    if (entries.length >= tournament.maxPlayers) {
      throw new Error("Tournament is full");
    }

    const existingEntry = entries.find(e => e.userId === userId);
    if (existingEntry) throw new Error("Already registered");

    return await ctx.db.insert("tournamentEntries", {
      tournamentId,
      userId,
      status: "pending",
      registeredAt: Date.now(),
    });
  },
});

/** Confirm tournament entry after Lemon Squeezy payment (called from Cloudflare Worker) */
export const confirmTournamentEntry = mutation({
  args: {
    entryId: v.id("tournamentEntries"),
    lemonSqueezyOrderId: v.string(),
  },
  handler: async (ctx, { entryId, lemonSqueezyOrderId }) => {
    const entry = await ctx.db.get(entryId);
    if (!entry) throw new Error("Entry not found");

    await ctx.db.patch(entryId, {
      status: "confirmed",
      lemonSqueezyOrderId,
    });

    // Update prize pool
    const tournament = await ctx.db.get(entry.tournamentId);
    if (tournament) {
      await ctx.db.patch(entry.tournamentId, {
        prizePoolUsd: tournament.prizePoolUsd + tournament.entryFeeUsd,
      });
    }
  },
});

/** Create a tournament (admin only in production) */
export const createTournament = mutation({
  args: {
    name: v.string(),
    description: v.optional(v.string()),
    format: v.union(
      v.literal("single_elimination"),
      v.literal("double_elimination"),
      v.literal("swiss")
    ),
    entryFeeUsd: v.number(),
    maxPlayers: v.number(),
    startTime: v.number(),
    lemonSqueezyVariantId: v.string(),
    createdBy: v.id("users"),
    platformCutPercent: v.optional(v.number()),
  },
  handler: async (ctx, args) => {
    return await ctx.db.insert("tournaments", {
      ...args,
      status: "registration",
      prizePoolUsd: 0,
      platformCutPercent: args.platformCutPercent ?? 10,
      bracket: JSON.stringify({}),
      createdAt: Date.now(),
    });
  },
});
