// convex/schema.ts — Backgammon Platform Database Schema
// This file is deployed as-is by the Convex CLI

import { defineSchema, defineTable } from "convex/server";
import { v } from "convex/values";

export default defineSchema({

  users: defineTable({
    clerkId: v.string(),
    username: v.string(),
    email: v.string(),
    elo: v.number(),                        // default: 1200
    gamesPlayed: v.number(),
    gamesWon: v.number(),
    premiumUntil: v.optional(v.number()),   // UNIX timestamp
    isBanned: v.boolean(),
    avatarUrl: v.optional(v.string()),
  })
    .index("by_clerkId", ["clerkId"])
    .index("by_elo", ["elo"]),

  games: defineTable({
    playerOneId: v.id("users"),
    playerTwoId: v.optional(v.id("users")),  // null if vs AI
    status: v.union(
      v.literal("waiting"),
      v.literal("active"),
      v.literal("completed"),
      v.literal("abandoned")
    ),
    boardState: v.string(),           // JSON-serialised GameState
    currentTurn: v.string(),          // "white" | "black"
    dice: v.array(v.number()),
    doublingCubeValue: v.number(),    // 1, 2, 4, 8, 16, 32, 64
    doublingCubeOwner: v.optional(v.string()),  // "white" | "black" | null
    winnerId: v.optional(v.id("users")),
    winType: v.optional(v.union(
      v.literal("normal"),
      v.literal("gammon"),
      v.literal("backgammon")
    )),
    tournamentId: v.optional(v.id("tournaments")),
    isPrivate: v.boolean(),
    inviteCode: v.optional(v.string()),
    createdAt: v.number(),
    updatedAt: v.number(),
  })
    .index("by_status", ["status"])
    .index("by_tournament", ["tournamentId"])
    .index("by_invite_code", ["inviteCode"]),

  matchmakingQueue: defineTable({
    userId: v.id("users"),
    eloMin: v.number(),
    eloMax: v.number(),
    joinedAt: v.number(),
  })
    .index("by_user", ["userId"])
    .index("by_joinedAt", ["joinedAt"]),

  tournaments: defineTable({
    name: v.string(),
    description: v.optional(v.string()),
    format: v.union(
      v.literal("single_elimination"),
      v.literal("double_elimination"),
      v.literal("swiss")
    ),
    status: v.union(
      v.literal("registration"),
      v.literal("active"),
      v.literal("completed")
    ),
    entryFeeUsd: v.number(),
    maxPlayers: v.number(),
    prizePoolUsd: v.number(),
    platformCutPercent: v.number(),   // e.g. 10
    startTime: v.number(),            // UNIX timestamp
    bracket: v.string(),              // JSON bracket structure
    lemonSqueezyVariantId: v.string(),
    createdBy: v.id("users"),
    createdAt: v.number(),
  })
    .index("by_status", ["status"])
    .index("by_startTime", ["startTime"]),

  tournamentEntries: defineTable({
    tournamentId: v.id("tournaments"),
    userId: v.id("users"),
    status: v.union(
      v.literal("pending"),       // awaiting payment
      v.literal("confirmed"),     // paid and registered
      v.literal("eliminated"),
      v.literal("winner")
    ),
    lemonSqueezyOrderId: v.optional(v.string()),
    seed: v.optional(v.number()),   // bracket seeding position
    registeredAt: v.number(),
  })
    .index("by_tournament", ["tournamentId"])
    .index("by_user", ["userId"])
    .index("by_order", ["lemonSqueezyOrderId"]),

  chatMessages: defineTable({
    gameId: v.id("games"),
    userId: v.id("users"),
    username: v.string(),
    text: v.string(),
    createdAt: v.number(),
  })
    .index("by_game", ["gameId"]),

  gameReplays: defineTable({
    gameId: v.id("games"),
    r2Key: v.string(),            // Cloudflare R2 object key
    uploadedAt: v.number(),
    sizeBytes: v.number(),
  })
    .index("by_game", ["gameId"]),

});
