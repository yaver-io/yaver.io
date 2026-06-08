import { mutation, query } from "./_generated/server";
import { v } from "convex/values";
import { validateSessionInternal } from "./auth";
import { Id } from "./_generated/dataModel";

// Social graph (the address book). A mutual connection is two rows, one
// per perspective. Every collaboration surface (device share, project
// invite, support link) can pick a friend from here instead of re-typing
// an email or pasting a code. Carries no sensitive data.

interface PeerProfile {
  userId: string;       // public userId string (not the _id)
  fullName: string;
  email: string;
}

async function peerProfile(ctx: any, userDocId: Id<"users">): Promise<PeerProfile> {
  const user = await ctx.db.get(userDocId);
  return {
    userId: user?.userId ?? "",
    fullName: user?.fullName ?? "Unknown",
    email: user?.email ?? "",
  };
}

/** Resolve a target user by public userId string (preferred) or email. */
async function resolveTargetUser(
  ctx: any,
  peerUserId?: string,
  peerEmail?: string,
): Promise<any | null> {
  const rawUserId = (peerUserId ?? "").trim();
  if (rawUserId) {
    const matches = await ctx.db
      .query("users")
      .filter((q: any) => q.eq(q.field("userId"), rawUserId))
      .collect();
    return matches[0] ?? null;
  }
  const rawEmail = (peerEmail ?? "").trim().toLowerCase();
  if (rawEmail) {
    return await ctx.db
      .query("users")
      .withIndex("by_email", (q: any) => q.eq("email", rawEmail))
      .first();
  }
  return null;
}

/** Fetch this user's perspective row for a given peer (or null). */
async function getRow(ctx: any, userId: Id<"users">, peerUserId: Id<"users">) {
  return await ctx.db
    .query("connections")
    .withIndex("by_user_peer", (q: any) => q.eq("userId", userId).eq("peerUserId", peerUserId))
    .first();
}

// ─── Mutations ──────────────────────────────────────────────────

/**
 * Send (or accept) a connection request. If the peer already requested
 * us, this auto-accepts — symmetric, like adding back on a mutual graph.
 * Target by public userId string or email.
 */
export const request = mutation({
  args: {
    tokenHash: v.string(),
    peerUserId: v.optional(v.string()),
    peerEmail: v.optional(v.string()),
    nickname: v.optional(v.string()),
    source: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");
    const me = session.user._id;

    const peer = await resolveTargetUser(ctx, args.peerUserId, args.peerEmail);
    if (!peer) throw new Error("No Yaver user found with that user id or email");
    if (peer._id === me) throw new Error("Cannot connect to yourself");

    const now = Date.now();
    const source = (args.source ?? "manual").trim() || "manual";
    const nickname = (args.nickname ?? "").trim() || undefined;

    const mine = await getRow(ctx, me, peer._id);
    const theirs = await getRow(ctx, peer._id, me);

    if (mine) {
      if (mine.status === "blocked") throw new Error("You blocked this user — unblock first");
      if (mine.status === "accepted") return { ok: true, status: "accepted" };
      // mine is pending
      if (mine.direction === "incoming") {
        // They already requested us — requesting them back = accept.
        await ctx.db.patch(mine._id, { status: "accepted", acceptedAt: now });
        if (theirs && theirs.status === "pending") {
          await ctx.db.patch(theirs._id, { status: "accepted", acceptedAt: now });
        }
        return { ok: true, status: "accepted" };
      }
      // mine is outgoing pending — already requested.
      return { ok: true, status: "pending" };
    }

    // No row yet from my side. If the peer blocked me, don't leak — pretend sent.
    if (theirs && theirs.status === "blocked") return { ok: true, status: "pending" };

    // Fresh request: two rows.
    await ctx.db.insert("connections", {
      userId: me,
      peerUserId: peer._id,
      status: "pending",
      direction: "outgoing",
      ...(nickname ? { nickname } : {}),
      source,
      createdAt: now,
    });
    if (!theirs) {
      await ctx.db.insert("connections", {
        userId: peer._id,
        peerUserId: me,
        status: "pending",
        direction: "incoming",
        source,
        createdAt: now,
      });
    } else if (theirs.status === "pending" && theirs.direction === "outgoing") {
      // Race: they requested at the same time — accept both.
      await ctx.db.patch(theirs._id, { status: "accepted", acceptedAt: now });
      const refreshedMine = await getRow(ctx, me, peer._id);
      if (refreshedMine) await ctx.db.patch(refreshedMine._id, { status: "accepted", acceptedAt: now });
      return { ok: true, status: "accepted" };
    }

    return { ok: true, status: "pending", peerUserId: peer.userId };
  },
});

/** Accept an incoming pending request from a peer. */
export const accept = mutation({
  args: { tokenHash: v.string(), peerUserId: v.string() },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");
    const me = session.user._id;

    const peer = await resolveTargetUser(ctx, args.peerUserId);
    if (!peer) throw new Error("User not found");

    const mine = await getRow(ctx, me, peer._id);
    if (!mine || mine.status !== "pending" || mine.direction !== "incoming") {
      throw new Error("No incoming request from this user");
    }

    const now = Date.now();
    await ctx.db.patch(mine._id, { status: "accepted", acceptedAt: now });
    const theirs = await getRow(ctx, peer._id, me);
    if (theirs && theirs.status === "pending") {
      await ctx.db.patch(theirs._id, { status: "accepted", acceptedAt: now });
    } else if (!theirs) {
      // Defensive: peer row missing — recreate as accepted outgoing.
      await ctx.db.insert("connections", {
        userId: peer._id,
        peerUserId: me,
        status: "accepted",
        direction: "outgoing",
        createdAt: now,
        acceptedAt: now,
      });
    }
    return { ok: true };
  },
});

/**
 * Remove a connection (decline / cancel / unfriend). Deletes both rows.
 */
export const remove = mutation({
  args: { tokenHash: v.string(), peerUserId: v.string() },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");
    const me = session.user._id;

    const peer = await resolveTargetUser(ctx, args.peerUserId);
    if (!peer) throw new Error("User not found");

    const mine = await getRow(ctx, me, peer._id);
    const theirs = await getRow(ctx, peer._id, me);
    // Don't delete the peer's row if it's a block — that's their record.
    if (mine) await ctx.db.delete(mine._id);
    if (theirs && theirs.status !== "blocked") await ctx.db.delete(theirs._id);
    return { ok: true };
  },
});

/**
 * Block a peer. Keeps a block record on my side and removes me from
 * their connection list.
 */
export const block = mutation({
  args: { tokenHash: v.string(), peerUserId: v.string() },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");
    const me = session.user._id;

    const peer = await resolveTargetUser(ctx, args.peerUserId);
    if (!peer) throw new Error("User not found");
    if (peer._id === me) throw new Error("Cannot block yourself");

    const now = Date.now();
    const mine = await getRow(ctx, me, peer._id);
    if (mine) {
      await ctx.db.patch(mine._id, { status: "blocked", blockedAt: now });
    } else {
      await ctx.db.insert("connections", {
        userId: me,
        peerUserId: peer._id,
        status: "blocked",
        direction: "outgoing",
        createdAt: now,
        blockedAt: now,
      });
    }
    const theirs = await getRow(ctx, peer._id, me);
    if (theirs) await ctx.db.delete(theirs._id);
    return { ok: true };
  },
});

/** Unblock a peer (removes the block record). */
export const unblock = mutation({
  args: { tokenHash: v.string(), peerUserId: v.string() },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");
    const me = session.user._id;
    const peer = await resolveTargetUser(ctx, args.peerUserId);
    if (!peer) throw new Error("User not found");
    const mine = await getRow(ctx, me, peer._id);
    if (mine && mine.status === "blocked") await ctx.db.delete(mine._id);
    return { ok: true };
  },
});

/** Set / clear a private display nickname for a peer. */
export const setNickname = mutation({
  args: { tokenHash: v.string(), peerUserId: v.string(), nickname: v.string() },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");
    const me = session.user._id;
    const peer = await resolveTargetUser(ctx, args.peerUserId);
    if (!peer) throw new Error("User not found");
    const mine = await getRow(ctx, me, peer._id);
    if (!mine) throw new Error("Not connected to this user");
    const nickname = args.nickname.trim();
    await ctx.db.patch(mine._id, nickname ? { nickname } : { nickname: undefined });
    return { ok: true };
  },
});

// ─── Queries ────────────────────────────────────────────────────

/**
 * List my connections, enriched with each peer's display profile.
 * Optional status filter ("accepted" | "pending" | "blocked").
 */
export const list = query({
  args: {
    tokenHash: v.string(),
    status: v.optional(v.union(v.literal("accepted"), v.literal("pending"), v.literal("blocked"))),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) return { accepted: [], incoming: [], outgoing: [], blocked: [] };
    const me = session.user._id;

    const rows = args.status
      ? await ctx.db
          .query("connections")
          .withIndex("by_user_status", (q) => q.eq("userId", me).eq("status", args.status!))
          .collect()
      : await ctx.db
          .query("connections")
          .withIndex("by_user", (q) => q.eq("userId", me))
          .collect();

    const accepted: any[] = [];
    const incoming: any[] = [];
    const outgoing: any[] = [];
    const blocked: any[] = [];
    for (const row of rows) {
      const profile = await peerProfile(ctx, row.peerUserId);
      const item = {
        peerUserId: profile.userId,
        fullName: profile.fullName,
        email: profile.email,
        nickname: row.nickname,
        source: row.source,
        createdAt: row.createdAt,
        acceptedAt: row.acceptedAt,
      };
      if (row.status === "accepted") accepted.push(item);
      else if (row.status === "blocked") blocked.push(item);
      else if (row.direction === "incoming") incoming.push(item);
      else outgoing.push(item);
    }
    return { accepted, incoming, outgoing, blocked };
  },
});

/**
 * Search for a user to connect with by exact public userId or email.
 * Returns the profile plus my current connection status with them.
 */
export const search = query({
  args: { tokenHash: v.string(), query: v.string() },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) return null;
    const me = session.user._id;
    const needle = args.query.trim();
    if (!needle) return null;

    const peer = needle.includes("@")
      ? await resolveTargetUser(ctx, undefined, needle)
      : await resolveTargetUser(ctx, needle);
    if (!peer) return null;
    if (peer._id === me) return { self: true };

    const mine = await getRow(ctx, me, peer._id);
    return {
      userId: peer.userId,
      fullName: peer.fullName,
      email: peer.email,
      connectionStatus: mine?.status ?? "none",
      direction: mine?.direction,
    };
  },
});

/**
 * Suggested connections — people you already collaborate with via existing
 * guest/support/team edges but haven't added to your address book yet.
 * Zero typing: turns latent relationships into one-tap connect.
 */
export const suggested = query({
  args: { tokenHash: v.string() },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) return [];
    const me = session.user._id;

    // Already-known peers (any status) — exclude these from suggestions.
    const known = new Set<string>();
    const myRows = await ctx.db
      .query("connections")
      .withIndex("by_user", (q) => q.eq("userId", me))
      .collect();
    for (const r of myRows) known.add(String(r.peerUserId));
    known.add(String(me));

    const candidates = new Map<string, string>(); // userDocId -> source label

    // guestAccess: people I host, and hosts I'm a guest of.
    const asHost = await ctx.db
      .query("guestAccess")
      .withIndex("by_hostUserId", (q) => q.eq("hostUserId", me))
      .filter((q) => q.eq(q.field("revokedAt"), undefined))
      .collect();
    for (const g of asHost) candidates.set(String(g.guestUserId), "guest");
    const asGuest = await ctx.db
      .query("guestAccess")
      .withIndex("by_guestUserId", (q) => q.eq("guestUserId", me))
      .filter((q) => q.eq(q.field("revokedAt"), undefined))
      .collect();
    for (const g of asGuest) candidates.set(String(g.hostUserId), "host");

    // teamMembers: teammates on teams I belong to.
    const myMemberships = await ctx.db
      .query("teamMembers")
      .withIndex("by_user", (q) => q.eq("userId", me))
      .collect();
    for (const m of myMemberships) {
      const mates = await ctx.db
        .query("teamMembers")
        .withIndex("by_team", (q) => q.eq("teamId", m.teamId))
        .collect();
      for (const mate of mates) candidates.set(String(mate.userId), "team");
    }

    const out: any[] = [];
    for (const [docId, source] of candidates) {
      if (known.has(docId)) continue;
      const profile = await peerProfile(ctx, docId as Id<"users">);
      if (!profile.userId) continue;
      out.push({
        userId: profile.userId,
        fullName: profile.fullName,
        email: profile.email,
        source,
      });
    }
    return out;
  },
});
