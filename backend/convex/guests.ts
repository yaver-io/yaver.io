import { mutation, query } from "./_generated/server";
import { v } from "convex/values";
import { validateSessionInternal } from "./auth";
import { Id } from "./_generated/dataModel";
import { internal } from "./_generated/api";
import { guestInviteHtml } from "./email";
import { getActiveInfraGrant, guestCanReachHostDevice, listGrantedDeviceIdsForGrant, listGrantedMachineIdsForGrant, revokeInfraGrantsBetweenUsers } from "./access";

const MAX_GUESTS_PER_HOST = 5;
const INVITATION_TTL_MS = 2 * 24 * 60 * 60 * 1000; // 2 days

/** Generate a short 6-character uppercase invite code. */
function generateInviteCode(): string {
  const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"; // no 0/O/1/I to avoid confusion
  const buf = new Uint8Array(6);
  crypto.getRandomValues(buf);
  return Array.from(buf).map(b => chars[b % chars.length]).join("");
}

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

    const inviteCode = generateInviteCode();
    const now = Date.now();
    await ctx.db.insert("guestInvitations", {
      hostUserId,
      guestEmail: args.guestEmail.toLowerCase(),
      inviteCode,
      status: "pending",
      createdAt: now,
      expiresAt: now + INVITATION_TTL_MS,
    });

    // Send invite email (fire-and-forget — won't block or fail the mutation)
    await ctx.scheduler.runAfter(0, internal.email.send, {
      from: "Yaver <hello@yaver.io>",
      to: args.guestEmail.toLowerCase(),
      subject: `${session.user.fullName} invited you to Yaver`,
      html: guestInviteHtml(session.user.fullName, inviteCode),
    });

    return {
      ok: true,
      inviteCode,
      guestRegistered: !!guestUser, // whether the invited email already has a Yaver account
    };
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
 * Accept a pending invitation by invite code.
 * Works regardless of the guest's email — the code is the proof of invitation.
 * This is the primary acceptance path when the guest signs up with a different
 * OAuth provider/email than the one the host invited.
 */
export const acceptByCode = mutation({
  args: {
    tokenHash: v.string(),
    inviteCode: v.string(),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const code = args.inviteCode.toUpperCase().trim();

    const invitation = await ctx.db
      .query("guestInvitations")
      .withIndex("by_inviteCode", (q) => q.eq("inviteCode", code))
      .first();

    if (!invitation) {
      throw new Error("Invalid invite code");
    }

    if (invitation.status !== "pending") {
      throw new Error("Invitation is no longer pending");
    }

    if (invitation.expiresAt < Date.now()) {
      await ctx.db.patch(invitation._id, { status: "revoked", revokedAt: Date.now() });
      throw new Error("Invitation has expired");
    }

    // Can't accept your own invitation
    if (invitation.hostUserId === session.user._id) {
      throw new Error("Cannot accept your own invitation");
    }

    // Check if already have access from this host
    const existingAccess = await ctx.db
      .query("guestAccess")
      .withIndex("by_host_guest", (q) =>
        q.eq("hostUserId", invitation.hostUserId).eq("guestUserId", session.user._id)
      )
      .filter((q) => q.eq(q.field("revokedAt"), undefined))
      .first();

    if (existingAccess) {
      throw new Error("You already have guest access to this host");
    }

    const now = Date.now();

    await ctx.db.patch(invitation._id, {
      status: "accepted",
      guestUserId: session.user._id,
      acceptedAt: now,
    });

    await ctx.db.insert("guestAccess", {
      hostUserId: invitation.hostUserId,
      guestUserId: session.user._id,
      grantedAt: now,
    });

    // Get host info to return
    const host = await ctx.db.get(invitation.hostUserId);

    return {
      ok: true,
      hostName: host?.fullName ?? "Unknown",
      hostEmail: host?.email ?? "Unknown",
    };
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
      const now = Date.now();
      const accessRecords = await ctx.db
        .query("guestAccess")
        .withIndex("by_host_guest", (q) =>
          q.eq("hostUserId", hostUserId).eq("guestUserId", guestUser._id)
        )
        .filter((q) => q.eq(q.field("revokedAt"), undefined))
        .collect();

      for (const access of accessRecords) {
        await ctx.db.patch(access._id, { revokedAt: now });
      }

      await revokeInfraGrantsBetweenUsers(ctx, hostUserId, guestUser._id, now);
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
      if (!(await guestCanReachHostDevice(ctx, hostUserId, access.guestUserId))) continue;
      const guest = await ctx.db.get(access.guestUserId);
      if (!guest) continue;
      guestUserIds.push(guest.userId);
    }

    return guestUserIds;
  },
});

// ─── Guest Config ──────────────────────────────────────────────────

/**
 * Get guest config for a specific guest (or all guests).
 * Called by the host or the agent.
 */
export const getGuestConfig = query({
  args: {
    tokenHash: v.string(),
    guestEmail: v.optional(v.string()),
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

    const configs: Array<{
      guestUserId: string;
      guestEmail: string;
      guestName: string;
      dailyTokenLimit?: number;
      allowedRunners?: string[];
      usageMode?: string;
      schedule?: { startHour: number; endHour: number; timezone?: string };
      shareAllDevices?: boolean;
      deviceIds?: string[];
      shareAllMachines?: boolean;
      machineIds?: Id<"cloudMachines">[];
      useHostApiKeys?: boolean;
      allowGuestProvidedApiKeys?: boolean;
      cpuLimitPercent?: number;
      ramLimitMb?: number;
      priorityMode?: string;
    }> = [];

    for (const access of accessRecords) {
      const guest = await ctx.db.get(access.guestUserId);
      if (!guest) continue;
      if (args.guestEmail && guest.email.toLowerCase() !== args.guestEmail.toLowerCase()) continue;
      const grant = await getActiveInfraGrant(ctx, hostUserId, access.guestUserId);
      const deviceIds = grant ? await listGrantedDeviceIdsForGrant(ctx, grant._id) : [];
      const machineIds = grant ? await listGrantedMachineIdsForGrant(ctx, grant._id) : [];

      configs.push({
        guestUserId: guest.userId,
        guestEmail: guest.email,
        guestName: guest.fullName,
        dailyTokenLimit: access.dailyTokenLimit,
        allowedRunners: grant?.allowedRunners ?? access.allowedRunners,
        usageMode: grant?.usageMode ?? access.usageMode,
        schedule: grant?.schedule ?? access.schedule,
        shareAllDevices: grant?.shareAllDevices ?? true,
        deviceIds,
        shareAllMachines: grant?.shareAllMachines ?? false,
        machineIds,
        useHostApiKeys: grant?.useHostApiKeys ?? false,
        allowGuestProvidedApiKeys: grant?.allowGuestProvidedApiKeys ?? true,
        cpuLimitPercent: grant?.cpuLimitPercent,
        ramLimitMb: grant?.ramLimitMb,
        priorityMode: grant?.priorityMode,
      });
    }

    return configs;
  },
});

/**
 * Update guest config. Called by the host.
 * Only updates the fields that are provided.
 */
export const updateGuestConfig = mutation({
  args: {
    tokenHash: v.string(),
    guestEmail: v.string(),
    dailyTokenLimit: v.optional(v.number()),
    allowedRunners: v.optional(v.array(v.string())),
    usageMode: v.optional(v.string()),
    shareAllDevices: v.optional(v.boolean()),
    deviceIds: v.optional(v.array(v.string())),
    shareAllMachines: v.optional(v.boolean()),
    machineIds: v.optional(v.array(v.id("cloudMachines"))),
    useHostApiKeys: v.optional(v.boolean()),
    allowGuestProvidedApiKeys: v.optional(v.boolean()),
    cpuLimitPercent: v.optional(v.number()),
    ramLimitMb: v.optional(v.number()),
    priorityMode: v.optional(v.string()),
    schedule: v.optional(v.object({
      startHour: v.number(),
      endHour: v.number(),
      timezone: v.optional(v.string()),
    })),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const hostUserId = session.user._id;
    const guestEmail = args.guestEmail.toLowerCase();

    // Find the guest user
    const guestUser = await ctx.db
      .query("users")
      .withIndex("by_email", (q) => q.eq("email", guestEmail))
      .first();

    if (!guestUser) {
      throw new Error("Guest not found");
    }

    // Find the active access record
    const access = await ctx.db
      .query("guestAccess")
      .withIndex("by_host_guest", (q) =>
        q.eq("hostUserId", hostUserId).eq("guestUserId", guestUser._id)
      )
      .filter((q) => q.eq(q.field("revokedAt"), undefined))
      .first();

    if (!access) {
      throw new Error("No active guest access for this email");
    }

    // Validate usageMode
    if (args.usageMode !== undefined) {
      const validModes = ["idle-only", "always", "scheduled"];
      if (!validModes.includes(args.usageMode)) {
        throw new Error(`Invalid usageMode: ${args.usageMode}. Must be one of: ${validModes.join(", ")}`);
      }
    }

    // Validate schedule hours
    if (args.schedule) {
      if (args.schedule.startHour < 0 || args.schedule.startHour > 23 ||
          args.schedule.endHour < 0 || args.schedule.endHour > 23) {
        throw new Error("Schedule hours must be between 0 and 23");
      }
    }
    if (args.shareAllDevices && args.deviceIds && args.deviceIds.length > 0) {
      throw new Error("Cannot set shareAllDevices and deviceIds together");
    }
    if (args.shareAllMachines && args.machineIds && args.machineIds.length > 0) {
      throw new Error("Cannot set shareAllMachines and machineIds together");
    }
    if (args.cpuLimitPercent !== undefined && (args.cpuLimitPercent < 1 || args.cpuLimitPercent > 100)) {
      throw new Error("cpuLimitPercent must be between 1 and 100");
    }
    if (args.ramLimitMb !== undefined && args.ramLimitMb < 128) {
      throw new Error("ramLimitMb must be at least 128");
    }
    if (args.priorityMode !== undefined) {
      const validPriorities = ["same-priority", "spare-capacity", "background"];
      if (!validPriorities.includes(args.priorityMode)) {
        throw new Error(`Invalid priorityMode: ${args.priorityMode}. Must be one of: ${validPriorities.join(", ")}`);
      }
    }

    // Build patch object — only include provided fields
    const patch: Record<string, unknown> = {};
    if (args.dailyTokenLimit !== undefined) patch.dailyTokenLimit = args.dailyTokenLimit;
    if (args.allowedRunners !== undefined) patch.allowedRunners = args.allowedRunners;
    if (args.usageMode !== undefined) patch.usageMode = args.usageMode;
    if (args.schedule !== undefined) patch.schedule = args.schedule;

    await ctx.db.patch(access._id, patch);

    const now = Date.now();
    let grant = await getActiveInfraGrant(ctx, hostUserId, guestUser._id);
    if (!grant) {
      const grantId = await ctx.db.insert("infraAccessGrants", {
        hostUserId,
        guestUserId: guestUser._id,
        status: "active",
        grantedAt: now,
        updatedAt: now,
      });
      grant = await ctx.db.get(grantId);
      if (!grant) throw new Error("Failed to create scoped grant");
    }

    const grantPatch: Record<string, unknown> = { updatedAt: now };
    if (args.shareAllDevices !== undefined) grantPatch.shareAllDevices = args.shareAllDevices;
    if (args.shareAllMachines !== undefined) grantPatch.shareAllMachines = args.shareAllMachines;
    if (args.useHostApiKeys !== undefined) grantPatch.useHostApiKeys = args.useHostApiKeys;
    if (args.allowGuestProvidedApiKeys !== undefined) {
      grantPatch.allowGuestProvidedApiKeys = args.allowGuestProvidedApiKeys;
    }
    if (args.cpuLimitPercent !== undefined) grantPatch.cpuLimitPercent = args.cpuLimitPercent;
    if (args.ramLimitMb !== undefined) grantPatch.ramLimitMb = args.ramLimitMb;
    if (args.priorityMode !== undefined) grantPatch.priorityMode = args.priorityMode;
    if (args.allowedRunners !== undefined) grantPatch.allowedRunners = args.allowedRunners;
    if (args.usageMode !== undefined) grantPatch.usageMode = args.usageMode;
    if (args.schedule !== undefined) grantPatch.schedule = args.schedule;
    await ctx.db.patch(grant._id, grantPatch);

    if (args.deviceIds !== undefined) {
      const existingLinks = await ctx.db
        .query("infraAccessGrantDevices")
        .withIndex("by_grant", (q) => q.eq("grantId", grant._id))
        .collect();
      for (const link of existingLinks) {
        await ctx.db.delete(link._id);
      }
      for (const deviceId of args.deviceIds) {
        const device = await ctx.db
          .query("devices")
          .withIndex("by_deviceId", (q) => q.eq("deviceId", deviceId))
          .unique();
        if (!device || device.userId !== hostUserId) {
          throw new Error(`Device not owned by host: ${deviceId}`);
        }
        await ctx.db.insert("infraAccessGrantDevices", {
          grantId: grant._id,
          hostUserId,
          guestUserId: guestUser._id,
          deviceId,
          createdAt: now,
        });
      }
    }

    if (args.machineIds !== undefined) {
      const existingLinks = await ctx.db
        .query("infraAccessGrantMachines")
        .withIndex("by_grant", (q) => q.eq("grantId", grant._id))
        .collect();
      for (const link of existingLinks) {
        await ctx.db.delete(link._id);
      }
      for (const machineId of args.machineIds) {
        const machine = await ctx.db.get(machineId);
        if (!machine || machine.userId !== hostUserId) {
          throw new Error("Machine not owned by host");
        }
        await ctx.db.insert("infraAccessGrantMachines", {
          grantId: grant._id,
          hostUserId,
          guestUserId: guestUser._id,
          machineId,
          createdAt: now,
        });
      }
    }

    return { ok: true };
  },
});

/**
 * Record guest usage (task-seconds consumed).
 * Called by the agent after a task completes.
 */
export const recordGuestUsage = mutation({
  args: {
    tokenHash: v.string(),
    guestUserId: v.string(),   // userId string (not doc ID)
    secondsUsed: v.number(),
    date: v.string(),          // "YYYY-MM-DD"
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const hostUserId = session.user._id;

    // Find guest user by userId string
    const guestUser = await ctx.db
      .query("users")
      .filter((q) => q.eq(q.field("userId"), args.guestUserId))
      .first();

    if (!guestUser) throw new Error("Guest user not found");

    // Upsert daily usage
    const existing = await ctx.db
      .query("guestUsage")
      .withIndex("by_host_guest_date", (q) =>
        q.eq("hostUserId", hostUserId)
          .eq("guestUserId", guestUser._id)
          .eq("date", args.date)
      )
      .first();

    if (existing) {
      await ctx.db.patch(existing._id, {
        secondsUsed: existing.secondsUsed + args.secondsUsed,
      });
    } else {
      await ctx.db.insert("guestUsage", {
        hostUserId,
        guestUserId: guestUser._id,
        date: args.date,
        secondsUsed: args.secondsUsed,
      });
    }

    return { ok: true };
  },
});

/**
 * Get guest usage for a date range.
 * Called by the host to see how much each guest used.
 */
export const getGuestUsage = query({
  args: {
    tokenHash: v.string(),
    date: v.optional(v.string()),  // specific date, defaults to today
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) return [];

    const hostUserId = session.user._id;
    const date = args.date || new Date().toISOString().slice(0, 10);

    const usageRecords = await ctx.db
      .query("guestUsage")
      .withIndex("by_hostUserId_date", (q) =>
        q.eq("hostUserId", hostUserId).eq("date", date)
      )
      .collect();

    const result: Array<{
      guestEmail: string;
      guestName: string;
      date: string;
      secondsUsed: number;
    }> = [];

    for (const usage of usageRecords) {
      const guest = await ctx.db.get(usage.guestUserId);
      if (!guest) continue;
      result.push({
        guestEmail: guest.email,
        guestName: guest.fullName,
        date: usage.date,
        secondsUsed: usage.secondsUsed,
      });
    }

    return result;
  },
});
