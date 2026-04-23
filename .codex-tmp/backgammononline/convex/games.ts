// convex/games.ts — Game mutations and queries

import { mutation, query } from "./_generated/server";
import { v } from "convex/values";

// ── Queries ───────────────────────────────────────────────────────────────────

/** Get live game state — auto-subscribes in React via useQuery */
export const getGame = query({
  args: { gameId: v.id("games") },
  handler: async (ctx, { gameId }) => {
    return await ctx.db.get(gameId);
  },
});

/** Get all active games (for lobby view) */
export const getActiveGames = query({
  args: {},
  handler: async (ctx) => {
    return await ctx.db
      .query("games")
      .withIndex("by_status", q => q.eq("status", "waiting"))
      .take(20);
  },
});

// ── Mutations ─────────────────────────────────────────────────────────────────

/** Create a new online game */
export const createGame = mutation({
  args: {
    playerOneId: v.id("users"),
    isPrivate: v.optional(v.boolean()),
    initialBoardState: v.string(),
  },
  handler: async (ctx, { playerOneId, isPrivate, initialBoardState }) => {
    const now = Date.now();
    const inviteCode = isPrivate
      ? Math.random().toString(36).substring(2, 8).toUpperCase()
      : undefined;

    return await ctx.db.insert("games", {
      playerOneId,
      status: "waiting",
      boardState: initialBoardState,
      currentTurn: "white",
      dice: [],
      doublingCubeValue: 1,
      doublingCubeOwner: undefined,
      isPrivate: isPrivate ?? false,
      inviteCode,
      createdAt: now,
      updatedAt: now,
    });
  },
});

/** Join an existing game */
export const joinGame = mutation({
  args: {
    gameId: v.id("games"),
    playerTwoId: v.id("users"),
  },
  handler: async (ctx, { gameId, playerTwoId }) => {
    const game = await ctx.db.get(gameId);
    if (!game || game.status !== "waiting") throw new Error("Game not available");

    await ctx.db.patch(gameId, {
      playerTwoId,
      status: "active",
      updatedAt: Date.now(),
    });
    return gameId;
  },
});

/** Apply a move and update board state server-side */
export const makeMove = mutation({
  args: {
    gameId: v.id("games"),
    userId: v.id("users"),
    boardState: v.string(),
    remainingDice: v.array(v.number()),
  },
  handler: async (ctx, { gameId, userId, boardState, remainingDice }) => {
    const game = await ctx.db.get(gameId);
    if (!game || game.status !== "active") throw new Error("Game not active");

    // TODO: Add server-side move validation in Phase 2
    await ctx.db.patch(gameId, {
      boardState,
      dice: remainingDice,
      updatedAt: Date.now(),
    });
  },
});

/** Roll dice server-side (provably fair — Phase 2) */
export const rollDice = mutation({
  args: {
    gameId: v.id("games"),
    userId: v.id("users"),
  },
  handler: async (ctx, { gameId, userId }) => {
    const d1 = Math.ceil(Math.random() * 6);
    const d2 = Math.ceil(Math.random() * 6);
    const dice = d1 === d2 ? [d1, d1, d1, d1] : [d1, d2];

    await ctx.db.patch(gameId, { dice, updatedAt: Date.now() });
    return dice;
  },
});

/** End turn and switch players */
export const endTurn = mutation({
  args: {
    gameId: v.id("games"),
    nextTurn: v.string(),
  },
  handler: async (ctx, { gameId, nextTurn }) => {
    await ctx.db.patch(gameId, {
      currentTurn: nextTurn,
      dice: [],
      updatedAt: Date.now(),
    });
  },
});

/** Mark a game as completed */
export const completeGame = mutation({
  args: {
    gameId: v.id("games"),
    winnerId: v.id("users"),
    winType: v.union(v.literal("normal"), v.literal("gammon"), v.literal("backgammon")),
    finalBoardState: v.string(),
  },
  handler: async (ctx, { gameId, winnerId, winType, finalBoardState }) => {
    await ctx.db.patch(gameId, {
      status: "completed",
      winnerId,
      winType,
      boardState: finalBoardState,
      updatedAt: Date.now(),
    });
  },
});

/** Resign a game */
export const resign = mutation({
  args: {
    gameId: v.id("games"),
    resigningUserId: v.id("users"),
  },
  handler: async (ctx, { gameId, resigningUserId }) => {
    const game = await ctx.db.get(gameId);
    if (!game) throw new Error("Game not found");

    const winnerId =
      game.playerOneId === resigningUserId ? game.playerTwoId : game.playerOneId;

    await ctx.db.patch(gameId, {
      status: "completed",
      winnerId,
      winType: "normal",
      updatedAt: Date.now(),
    });
  },
});

/** Send a chat message in a game */
export const sendChatMessage = mutation({
  args: {
    gameId: v.id("games"),
    userId: v.id("users"),
    username: v.string(),
    text: v.string(),
  },
  handler: async (ctx, { gameId, userId, username, text }) => {
    if (text.trim().length === 0) return;
    await ctx.db.insert("chatMessages", {
      gameId,
      userId,
      username,
      text: text.slice(0, 500),
      createdAt: Date.now(),
    });
  },
});

/** Get chat messages for a game */
export const getChatMessages = query({
  args: { gameId: v.id("games") },
  handler: async (ctx, { gameId }) => {
    return await ctx.db
      .query("chatMessages")
      .withIndex("by_game", q => q.eq("gameId", gameId))
      .order("asc")
      .take(100);
  },
});
