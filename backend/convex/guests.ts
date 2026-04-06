import { mutation, query } from "./_generated/server";
import { v } from "convex/values";
import { validateSessionInternal } from "./auth";
import { Id } from "./_generated/dataModel";

const MAX_GUESTS_PER_HOST = 5;
const INVITATION_TTL_MS = 2 * 24 * 60 * 60 * 1000; // 2 days

// ─── Mutations ──────────────────────────────────────────────────

/**
 * Invite a guest by email. Only the host can invite.
 * Creates a pending invitation that expires in 2 days.
 */
export const invite = mutation({
  args: {
    tokenHash: v.string(),
    guestEmail: v.string(),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const hostUserId = session.user._id;

    // Can't invite yourself
    if (args.guestEmail.toLowerCase() === session.user.email.toLowerCase()) {
      throw new Error("Cannot invite yourself");
    }

    // Check active guest count (accepted + pending, excluding revoked/expired)
    const activeAccess = await ctx.db
      .query("guestAccess")
      .withIndex("by_hostUserId", (q) => q.eq("hostUserId", hostUserId))
      .filter((q) => q.eq(q.field("revokedAt"), undefined))
      .collect();

    if (activeAccess.length >= MAX_GUESTS_PER_HOST) {
      throw new Error(`Maximum ${MAX_GUESTS_PER_HOST} guests allowed`);
    }

    // Check for existing active invitation or access for this email
    const existingInvitations = await ctx.db
      .query("guestInvitations")
      .withIndex("by_host_guest", (q) =>
        q.eq("hostUserId", hostUserId).eq("guestEmail", args.guestEmail.toLowerCase())
      )
      .collect();

    for (const inv of existingInvitations) {
      if (inv.status === "pending" && inv.expiresAt > Date.now()) {
        throw new Error("Pending invitation already exists for this email");
      }
      if (inv.status === "accepted") {
        throw new Error("This user already has guest access");
      }
    }

    // Also check guestAccess table for active access
    const guestUser = await ctx.db
      .query("users")
      .withIndex("by_email", (q) => q.eq("email", args.guestEmail.toLowerCase()))
      .first();

    if (guestUser) {
      const existingAccess = await ctx.db
        .query("guestAccess")
        .withIndex("by_host_guest", (q) =>
          q.eq("hostUserId", hostUserId).eq("guestUserId", guestUser._id)
        )
        .filter((q) => q.eq(q.field("revokedAt"), undefined))
        .first();

      if (existingAccess) {
        throw new Error("This user already has guest access");
      }
    }

    const now = Date.now();
    await ctx.db.insert("guestInvitations", {
      hostUserId,
      guestEmail: args.guestEmail.toLowerCase(),
      status: "pending",
      createdAt: now,
      expiresAt: now + INVITATION_TTL_MS,
    });

    return { ok: true };
  },
});

/**
 * Accept a pending invitation. Called by the guest.
 * The guest must be signed in and their email must match the invitation.
 */
export const accept = mutation({
  args: {
    tokenHash: v.string(),
    hostUserId: v.id("users"),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const guestEmail = session.user.email.toLowerCase();

    // Find the pending invitation
    const invitation = await ctx.db
      .query("guestInvitations")
      .withIndex("by_host_guest", (q) =>
        q.eq("hostUserId", args.hostUserId).eq("guestEmail", guestEmail)
      )
      .filter((q) => q.eq(q.field("status"), "pending"))
      .first();

    if (!invitation) {
      throw new Error("No pending invitation found");
    }

    if (invitation.expiresAt < Date.now()) {
      // Mark as expired
      await ctx.db.patch(invitation._id, { status: "revoked", revokedAt: Date.now() });
      throw new Error("Invitation has expired");
    }

    const now = Date.now();

    // Update invitation status
    await ctx.db.patch(invitation._id, {
      status: "accepted",
      guestUserId: session.user._id,
      acceptedAt: now,
    });

    // Create guestAccess record
    await ctx.db.insert("guestAccess", {
      hostUserId: args.hostUserId,
      guestUserId: session.user._id,
      grantedAt: now,
    });

    return { ok: true };
  },
});

/**
 * Revoke guest access. Called by the host.
 * Works for both pending invitations and active access.
 */
export const revoke = mutation({
  args: {
    tokenHash: v.string(),
    guestEmail: v.string(),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const hostUserId = session.user._id;
    const guestEmail = args.guestEmail.toLowerCase();

    // Revoke any pending invitations
    const invitations = await ctx.db
      .query("guestInvitations")
      .withIndex("by_host_guest", (q) =>
        q.eq("hostUserId", hostUserId).eq("guestEmail", guestEmail)
      )
      .collect();

    for (const inv of invitations) {
      if (inv.status === "pending" || inv.status === "accepted") {
        await ctx.db.patch(inv._id, { status: "revoked", revokedAt: Date.now() });
      }
    }

    // Revoke active guestAccess
    const guestUser = await ctx.db
      .query("users")
      .withIndex("by_email", (q) => q.eq("email", guestEmail))
      .first();

    if (guestUser) {
      const accessRecords = await ctx.db
        .query("guestAccess")
        .withIndex("by_host_guest", (q) =>
          q.eq("hostUserId", hostUserId).eq("guestUserId", guestUser._id)
        )
        .filter((q) => q.eq(q.field("revokedAt"), undefined))
        .collect();

      for (const access of accessRecords) {
        await ctx.db.patch(access._id, { revokedAt: Date.now() });
      }
    }

    return { ok: true };
  },
});

// ─── Queries ────────────────────────────────────────────────────

/**
 * List all guests for a host (invitations + active access).
 * Called by the host.
 */
export const listGuests = query({
  args: {
    tokenHash: v.string(),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) return [];

    const hostUserId = session.user._id;

    // Get all invitations (pending + accepted)
    const invitations = await ctx.db
      .query("guestInvitations")
      .withIndex("by_hostUserId", (q) => q.eq("hostUserId", hostUserId))
      .collect();

    const result: Array<{
      email: string;
      status: "pending" | "accepted" | "revoked" | "expired";
      fullName?: string;
      createdAt: number;
      expiresAt?: number;
      acceptedAt?: number;
      revokedAt?: number;
    }> = [];

    for (const inv of invitations) {
      let status = inv.status as "pending" | "accepted" | "revoked" | "expired";
      if (status === "pending" && inv.expiresAt < Date.now()) {
        status = "expired";
      }

      let fullName: string | undefined;
      if (inv.guestUserId) {
        const guest = await ctx.db.get(inv.guestUserId);
        fullName = guest?.fullName;
      }

      result.push({
        email: inv.guestEmail,
        status,
        fullName,
        createdAt: inv.createdAt,
        expiresAt: inv.expiresAt,
        acceptedAt: inv.acceptedAt,
        revokedAt: inv.revokedAt,
      });
    }

    return result;
  },
});

/**
 * List all hosts that have granted guest access to the current user.
 * Called by the guest (mobile app) to discover available hosts.
 */
export const listHosts = query({
  args: {
    tokenHash: v.string(),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) return { pending: [], active: [] };

    const guestUserId = session.user._id;
    const guestEmail = session.user.email.toLowerCase();

    // Get pending invitations for this email
    const pendingInvitations = await ctx.db
      .query("guestInvitations")
      .withIndex("by_guestEmail", (q) => q.eq("guestEmail", guestEmail))
      .filter((q) => q.eq(q.field("status"), "pending"))
      .collect();

    const pending: Array<{
      hostUserId: string;
      hostName: string;
      hostEmail: string;
      createdAt: number;
      expiresAt: number;
    }> = [];

    for (const inv of pendingInvitations) {
      if (inv.expiresAt < Date.now()) continue;
      const host = await ctx.db.get(inv.hostUserId);
      if (!host) continue;
      pending.push({
        hostUserId: inv.hostUserId,
        hostName: host.fullName,
        hostEmail: host.email,
        createdAt: inv.createdAt,
        expiresAt: inv.expiresAt,
      });
    }

    // Get active access grants
    const accessRecords = await ctx.db
      .query("guestAccess")
      .withIndex("by_guestUserId", (q) => q.eq("guestUserId", guestUserId))
      .filter((q) => q.eq(q.field("revokedAt"), undefined))
      .collect();

    const active: Array<{
      hostUserId: string;
      hostName: string;
      hostEmail: string;
      grantedAt: number;
    }> = [];

    for (const access of accessRecords) {
      const host = await ctx.db.get(access.hostUserId);
      if (!host) continue;
      active.push({
        hostUserId: access.hostUserId,
        hostName: host.fullName,
        hostEmail: host.email,
        grantedAt: access.grantedAt,
      });
    }

    return { pending, active };
  },
});

/**
 * Get the list of approved guest userIds for a host.
 * Called by the desktop agent to know who to allow.
 * Returns userId strings (not doc IDs) for matching against token validation.
 */
export const getGuestUserIds = query({
  args: {
    tokenHash: v.string(),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) return [];

    const hostUserId = session.user._id;

    const accessRecords = await ctx.db
      .query("guestAccess")
      .withIndex("by_hostUserId", (q) => q.eq("hostUserId", hostUserId))
      .filter((q) => q.eq(q.field("revokedAt"), undefined))
      .collect();

    const guestUserIds: string[] = [];
    for (const access of accessRecords) {
      const guest = await ctx.db.get(access.guestUserId);
      if (guest) {
        guestUserIds.push(guest.userId);
      }
    }

    return guestUserIds;
  },
});
