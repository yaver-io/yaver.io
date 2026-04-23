import { mutation, query } from "./_generated/server";
import { v } from "convex/values";
import { validateSessionInternal } from "./auth";
import { Id } from "./_generated/dataModel";

const DEFAULT_INVITE_TTL_MINUTES = 24 * 60;
const DEFAULT_SESSION_TTL_MINUTES = 8 * 60;
const DEFAULT_IDLE_TIMEOUT_MINUTES = 30;
const MAX_INVITE_TTL_MINUTES = 7 * 24 * 60;
const MAX_SESSION_TTL_MINUTES = 3 * 24 * 60;
const MAX_IDLE_TIMEOUT_MINUTES = 12 * 60;
const MAX_ACTIVE_HOST_SHARE_INVITES = 20;
const MAX_ACTIVE_HOST_SHARE_SESSIONS = 10;

type UserDoc = {
  _id: Id<"users">;
  userId: string;
  email: string;
  fullName: string;
};

function generateInviteCode(): string {
  const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
  const buf = new Uint8Array(6);
  crypto.getRandomValues(buf);
  return Array.from(buf).map((b) => chars[b % chars.length]).join("");
}

function clampMinutes(
  value: number | undefined,
  fallback: number,
  max: number,
): number {
  const raw = typeof value === "number" && Number.isFinite(value) ? Math.floor(value) : fallback;
  if (raw <= 0) return fallback;
  return Math.min(raw, max);
}

async function resolveGuestUser(
  ctx: any,
  guestEmail?: string,
  guestUserId?: string,
): Promise<UserDoc | null> {
  const email = (guestEmail ?? "").trim().toLowerCase();
  const userId = (guestUserId ?? "").trim();
  if (userId) {
    const rows = await ctx.db
      .query("users")
      .filter((q: any) => q.eq(q.field("userId"), userId))
      .collect();
    return rows[0] ?? null;
  }
  if (email) {
    return await ctx.db
      .query("users")
      .withIndex("by_email", (q: any) => q.eq("email", email))
      .first();
  }
  return null;
}

async function getUserDocById(
  ctx: any,
  userId?: Id<"users">,
): Promise<UserDoc | null> {
  if (!userId) return null;
  return await ctx.db.get(userId) as UserDoc | null;
}

async function resolveInvitation(
  ctx: any,
  inviteCode: string,
) {
  const code = inviteCode.trim().toUpperCase();
  if (!code) return null;
  const invitation = await ctx.db
    .query("hostShareInvites")
    .withIndex("by_inviteCode", (q: any) => q.eq("inviteCode", code))
    .first();
  if (!invitation) return null;
  if (invitation.status !== "pending") return null;
  if (invitation.inviteExpiresAt <= Date.now()) return null;
  return invitation;
}

async function materializeHostShareSession(
  ctx: any,
  invitation: any,
  guestUserId: Id<"users">,
  guestDeviceId: string | undefined,
  now: number,
) {
  await ctx.db.patch(invitation._id, {
    status: "accepted",
    acceptedAt: now,
    acceptedByGuestUserId: guestUserId,
  });

  const policy = {
    toolingPreset: invitation.toolingPreset,
    resourcePreset: invitation.resourcePreset,
    allowInfra: invitation.allowInfra,
    allowTerminal: invitation.allowTerminal,
    allowTunnel: invitation.allowTunnel,
    useHostAgentTools: invitation.useHostAgentTools,
    useHostInfra: invitation.useHostInfra,
    allowedRunners: invitation.allowedRunners ?? [],
    allowedProjects: invitation.allowedProjects ?? [],
  };

  const expiresAt = now + invitation.sessionTtlMinutes * 60_000;
  const sessionId = await ctx.db.insert("hostShareSessions", {
    inviteId: invitation._id,
    hostUserId: invitation.hostUserId,
    hostDeviceId: invitation.hostDeviceId,
    guestUserId,
    guestDeviceId,
    status: "active",
    label: invitation.label,
    policy,
    createdAt: now,
    startedAt: now,
    expiresAt,
    idleTimeoutMinutes: invitation.idleTimeoutMinutes,
    lastActivityAt: now,
  });

  return { sessionId, expiresAt, policy };
}

async function findActiveSessionForHostGuestDevice(
  ctx: any,
  hostUserId: Id<"users">,
  guestUserId: Id<"users">,
  deviceId?: string,
) {
  const now = Date.now();
  const rows = await ctx.db
    .query("hostShareSessions")
    .withIndex("by_host_status", (q: any) => q.eq("hostUserId", hostUserId).eq("status", "active"))
    .collect();
  const normalizedDeviceId = (deviceId ?? "").trim();
  for (const row of rows) {
    if (row.guestUserId !== guestUserId) continue;
    if (row.expiresAt <= now) continue;
    const idleExpiry = row.lastActivityAt + row.idleTimeoutMinutes * 60_000;
    if (idleExpiry <= now) continue;
    if (normalizedDeviceId && row.hostDeviceId && row.hostDeviceId !== normalizedDeviceId) continue;
    return row;
  }
  return null;
}

export const createInvite = mutation({
  args: {
    tokenHash: v.string(),
    guestEmail: v.optional(v.string()),
    guestUserId: v.optional(v.string()),
    label: v.optional(v.string()),
    hostDeviceId: v.optional(v.string()),
    inviteTtlMinutes: v.optional(v.number()),
    sessionTtlMinutes: v.optional(v.number()),
    idleTimeoutMinutes: v.optional(v.number()),
    toolingPreset: v.optional(v.string()),
    resourcePreset: v.optional(v.string()),
    allowInfra: v.optional(v.boolean()),
    allowTerminal: v.optional(v.boolean()),
    allowTunnel: v.optional(v.boolean()),
    useHostAgentTools: v.optional(v.boolean()),
    useHostInfra: v.optional(v.boolean()),
    allowedRunners: v.optional(v.array(v.string())),
    allowedProjects: v.optional(v.array(v.string())),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const hostUserId = session.user._id;
    const guestEmail = (args.guestEmail ?? "").trim().toLowerCase();
    const guestUserIdString = (args.guestUserId ?? "").trim();
    const guestUser = await resolveGuestUser(ctx, guestEmail, guestUserIdString);
    if (guestUserIdString && !guestUser) throw new Error("No Yaver user found with that user id");
    if (guestEmail && guestUser && guestUser.email.toLowerCase() !== guestEmail) {
      throw new Error("Resolved guest account email does not match the invite email");
    }
    if (
      (guestEmail && guestEmail === session.user.email.toLowerCase()) ||
      (guestUser && guestUser._id === hostUserId)
    ) {
      throw new Error("Cannot create a host-share invite for yourself");
    }

    const activeInvites = await ctx.db
      .query("hostShareInvites")
      .withIndex("by_hostUserId", (q: any) => q.eq("hostUserId", hostUserId))
      .collect();
    const activeCount = activeInvites.filter((invite: any) =>
      invite.status === "pending" && invite.inviteExpiresAt > Date.now(),
    ).length;
    if (activeCount >= MAX_ACTIVE_HOST_SHARE_INVITES) {
      throw new Error(`Maximum ${MAX_ACTIVE_HOST_SHARE_INVITES} active host-share invites allowed`);
    }

    const activeSessions = await ctx.db
      .query("hostShareSessions")
      .withIndex("by_host_status", (q: any) => q.eq("hostUserId", hostUserId).eq("status", "active"))
      .collect();
    const liveSessionCount = activeSessions.filter((row: any) => row.expiresAt > Date.now()).length;
    if (liveSessionCount >= MAX_ACTIVE_HOST_SHARE_SESSIONS) {
      throw new Error(`Maximum ${MAX_ACTIVE_HOST_SHARE_SESSIONS} active host-share sessions allowed`);
    }

    const now = Date.now();
    const inviteTtlMinutes = clampMinutes(args.inviteTtlMinutes, DEFAULT_INVITE_TTL_MINUTES, MAX_INVITE_TTL_MINUTES);
    const sessionTtlMinutes = clampMinutes(args.sessionTtlMinutes, DEFAULT_SESSION_TTL_MINUTES, MAX_SESSION_TTL_MINUTES);
    const idleTimeoutMinutes = clampMinutes(args.idleTimeoutMinutes, DEFAULT_IDLE_TIMEOUT_MINUTES, MAX_IDLE_TIMEOUT_MINUTES);
    const allowedRunners = (args.allowedRunners ?? []).map((r) => r.trim()).filter(Boolean);
    const allowedProjects = (args.allowedProjects ?? []).map((p) => p.trim()).filter(Boolean);
    const inviteCode = generateInviteCode();

    const inviteId = await ctx.db.insert("hostShareInvites", {
      hostUserId,
      hostDeviceId: args.hostDeviceId?.trim() || undefined,
      guestEmail: guestEmail || undefined,
      guestUserId: guestUser?._id,
      label: args.label?.trim() || undefined,
      inviteCode,
      status: "pending",
      inviteExpiresAt: now + inviteTtlMinutes * 60_000,
      sessionTtlMinutes,
      idleTimeoutMinutes,
      toolingPreset: args.toolingPreset?.trim() || "all-coding-tools",
      resourcePreset: args.resourcePreset?.trim() || "balanced",
      allowInfra: args.allowInfra ?? true,
      allowTerminal: args.allowTerminal ?? true,
      allowTunnel: args.allowTunnel ?? false,
      useHostAgentTools: args.useHostAgentTools ?? true,
      useHostInfra: args.useHostInfra ?? true,
      allowedRunners: allowedRunners.length > 0 ? allowedRunners : undefined,
      allowedProjects: allowedProjects.length > 0 ? allowedProjects : undefined,
      createdAt: now,
    });

    return {
      ok: true,
      inviteId,
      inviteCode,
      inviteExpiresAt: now + inviteTtlMinutes * 60_000,
      hostName: session.user.fullName,
      hostEmail: session.user.email,
      guestRegistered: !!guestUser,
      guestEmail: guestEmail || undefined,
      guestUserId: guestUser?.userId,
      policy: {
        toolingPreset: args.toolingPreset?.trim() || "all-coding-tools",
        resourcePreset: args.resourcePreset?.trim() || "balanced",
        allowInfra: args.allowInfra ?? true,
        allowTerminal: args.allowTerminal ?? true,
        allowTunnel: args.allowTunnel ?? false,
        useHostAgentTools: args.useHostAgentTools ?? true,
        useHostInfra: args.useHostInfra ?? true,
        allowedRunners,
        allowedProjects,
        sessionTtlMinutes,
        idleTimeoutMinutes,
      },
    };
  },
});

export const findInviteByCode = query({
  args: {
    tokenHash: v.string(),
    inviteCode: v.string(),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const invitation = await resolveInvitation(ctx, args.inviteCode);
    if (!invitation) return null;

    const hostUser = await getUserDocById(ctx, invitation.hostUserId);
    if (!hostUser) return null;
    const guestTarget = await getUserDocById(ctx, invitation.guestUserId);

    return {
      inviteCode: invitation.inviteCode,
      status: invitation.status,
      label: invitation.label,
      hostUserId: String(invitation.hostUserId),
      hostUserIdString: hostUser.userId,
      hostName: hostUser.fullName,
      hostEmail: hostUser.email,
      guestEmail: invitation.guestEmail,
      guestUserId: guestTarget?.userId,
      hostDeviceId: invitation.hostDeviceId,
      inviteExpiresAt: invitation.inviteExpiresAt,
      sessionTtlMinutes: invitation.sessionTtlMinutes,
      idleTimeoutMinutes: invitation.idleTimeoutMinutes,
      toolingPreset: invitation.toolingPreset,
      resourcePreset: invitation.resourcePreset,
      allowInfra: invitation.allowInfra,
      allowTerminal: invitation.allowTerminal,
      allowTunnel: invitation.allowTunnel,
      useHostAgentTools: invitation.useHostAgentTools,
      useHostInfra: invitation.useHostInfra,
      allowedRunners: invitation.allowedRunners ?? [],
      allowedProjects: invitation.allowedProjects ?? [],
      targeted: !!(invitation.guestEmail || invitation.guestUserId),
      createdAt: invitation.createdAt,
    };
  },
});

export const joinByCode = mutation({
  args: {
    tokenHash: v.string(),
    inviteCode: v.string(),
    guestDeviceId: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const invitation = await resolveInvitation(ctx, args.inviteCode);
    if (!invitation) throw new Error("Invite not found or expired");

    if (invitation.guestUserId && invitation.guestUserId !== session.user._id) {
      throw new Error("This invite is pinned to another Yaver user");
    }
    if (invitation.guestEmail && invitation.guestEmail !== session.user.email.toLowerCase()) {
      throw new Error("This invite is pinned to another email address");
    }
    if (invitation.hostUserId === session.user._id) {
      throw new Error("Host cannot redeem their own host-share invite");
    }

    const now = Date.now();
    const existing = await ctx.db
      .query("hostShareSessions")
      .withIndex("by_invite", (q: any) => q.eq("inviteId", invitation._id))
      .collect();
    const live = existing.find((row: any) => row.status === "active" && row.expiresAt > now);
    if (live) {
      throw new Error("This invite has already been redeemed into an active session");
    }

    const result = await materializeHostShareSession(
      ctx,
      invitation,
      session.user._id,
      args.guestDeviceId?.trim() || undefined,
      now,
    );
    const hostUser = await getUserDocById(ctx, invitation.hostUserId);
    return {
      ok: true,
      sessionId: String(result.sessionId),
      hostName: hostUser?.fullName ?? "",
      hostEmail: hostUser?.email ?? "",
      expiresAt: result.expiresAt,
      policy: result.policy,
    };
  },
});

export const revokeInvite = mutation({
  args: {
    tokenHash: v.string(),
    inviteCode: v.string(),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const invitation = await ctx.db
      .query("hostShareInvites")
      .withIndex("by_inviteCode", (q: any) => q.eq("inviteCode", args.inviteCode.trim().toUpperCase()))
      .first();
    if (!invitation) throw new Error("Invite not found");
    if (invitation.hostUserId !== session.user._id) throw new Error("Only the host can revoke this invite");

    const now = Date.now();
    await ctx.db.patch(invitation._id, {
      status: "revoked",
      revokedAt: now,
    });
    const sessions = await ctx.db
      .query("hostShareSessions")
      .withIndex("by_invite", (q: any) => q.eq("inviteId", invitation._id))
      .collect();
    for (const row of sessions) {
      if (row.status === "active") {
        await ctx.db.patch(row._id, {
          status: "revoked",
          endedAt: now,
          endedReason: "host_revoked_invite",
        });
      }
    }
    return { ok: true };
  },
});

export const listInvites = query({
  args: {
    tokenHash: v.string(),
    role: v.optional(v.union(v.literal("host"), v.literal("guest"))),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const role = args.role ?? "host";
    if (role === "guest") {
      const targetedInvites = await ctx.db
        .query("hostShareInvites")
        .withIndex("by_guestUserId", (q: any) => q.eq("guestUserId", session.user._id))
        .collect();
      return await Promise.all(targetedInvites.map(async (invite: any) => {
        const host = await getUserDocById(ctx, invite.hostUserId);
        return {
          inviteCode: invite.inviteCode,
          status: invite.status,
          label: invite.label,
          hostName: host?.fullName ?? "",
          hostEmail: host?.email ?? "",
          inviteExpiresAt: invite.inviteExpiresAt,
          sessionTtlMinutes: invite.sessionTtlMinutes,
          idleTimeoutMinutes: invite.idleTimeoutMinutes,
          toolingPreset: invite.toolingPreset,
          resourcePreset: invite.resourcePreset,
        };
      }));
    }

    const invites = await ctx.db
      .query("hostShareInvites")
      .withIndex("by_hostUserId", (q: any) => q.eq("hostUserId", session.user._id))
      .collect();
    return await Promise.all(invites.map(async (invite: any) => {
      const guest = invite.acceptedByGuestUserId
        ? await getUserDocById(ctx, invite.acceptedByGuestUserId)
        : await getUserDocById(ctx, invite.guestUserId);
      return {
        inviteCode: invite.inviteCode,
        status: invite.status,
        label: invite.label,
      guestEmail: invite.guestEmail,
      guestUserId: guest?.userId,
      guestName: guest?.fullName,
      hostDeviceId: invite.hostDeviceId,
      guestDeviceId: invite.guestDeviceId,
      inviteExpiresAt: invite.inviteExpiresAt,
        sessionTtlMinutes: invite.sessionTtlMinutes,
        idleTimeoutMinutes: invite.idleTimeoutMinutes,
        toolingPreset: invite.toolingPreset,
        resourcePreset: invite.resourcePreset,
        createdAt: invite.createdAt,
        acceptedAt: invite.acceptedAt,
        revokedAt: invite.revokedAt,
      };
    }));
  },
});

export const listSessions = query({
  args: {
    tokenHash: v.string(),
    role: v.optional(v.union(v.literal("host"), v.literal("guest"))),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const role = args.role ?? "host";
    const rows = role === "guest"
      ? await ctx.db
          .query("hostShareSessions")
          .withIndex("by_guest_status", (q: any) => q.eq("guestUserId", session.user._id).eq("status", "active"))
          .collect()
      : await ctx.db
          .query("hostShareSessions")
          .withIndex("by_host_status", (q: any) => q.eq("hostUserId", session.user._id).eq("status", "active"))
          .collect();

    return await Promise.all(rows.map(async (row: any) => {
      const host = await getUserDocById(ctx, row.hostUserId);
      const guest = await getUserDocById(ctx, row.guestUserId);
      return {
        sessionId: String(row._id),
        inviteId: String(row.inviteId),
        status: row.status,
        label: row.label,
        hostName: host?.fullName ?? "",
        hostEmail: host?.email ?? "",
        guestName: guest?.fullName ?? "",
        guestEmail: guest?.email ?? "",
        hostDeviceId: row.hostDeviceId,
        guestDeviceId: row.guestDeviceId,
        policy: row.policy,
        startedAt: row.startedAt,
        expiresAt: row.expiresAt,
        idleTimeoutMinutes: row.idleTimeoutMinutes,
        lastActivityAt: row.lastActivityAt,
      };
    }));
  },
});

export const endSession = mutation({
  args: {
    tokenHash: v.string(),
    sessionId: v.id("hostShareSessions"),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const row = await ctx.db.get(args.sessionId);
    if (!row) throw new Error("Session not found");
    if (row.hostUserId !== session.user._id) {
      throw new Error("Only the host can end this session");
    }
    if (row.status !== "active") {
      return { ok: true, status: row.status };
    }

    const now = Date.now();
    await ctx.db.patch(args.sessionId, {
      status: "ended",
      endedAt: now,
      endedReason: "host_ended_session",
      lastActivityAt: now,
    });
    return { ok: true, status: "ended", endedAt: now };
  },
});

export const getAccessForHostDevice = query({
  args: {
    tokenHash: v.string(),
    guestUserId: v.string(),
    deviceId: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const guest = await resolveGuestUser(ctx, undefined, args.guestUserId);
    if (!guest) return null;
    const active = await findActiveSessionForHostGuestDevice(ctx, session.user._id, guest._id, args.deviceId);
    if (!active) return null;
    return {
      sessionId: String(active._id),
      inviteId: String(active.inviteId),
      label: active.label,
      hostDeviceId: active.hostDeviceId,
      guestDeviceId: active.guestDeviceId,
      guestUserId: guest.userId,
      guestEmail: guest.email,
      guestName: guest.fullName,
      policy: active.policy,
      expiresAt: active.expiresAt,
      idleTimeoutMinutes: active.idleTimeoutMinutes,
      lastActivityAt: active.lastActivityAt,
    };
  },
});

export const getAccessForGuestDevice = query({
  args: {
    tokenHash: v.string(),
    hostUserId: v.string(),
    deviceId: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const host = await resolveGuestUser(ctx, undefined, args.hostUserId);
    if (!host) return null;
    const now = Date.now();
    const rows = await ctx.db
      .query("hostShareSessions")
      .withIndex("by_guest_status", (q: any) => q.eq("guestUserId", session.user._id).eq("status", "active"))
      .collect();
    const normalizedDeviceID = (args.deviceId ?? "").trim();
    for (const row of rows) {
      if (row.hostUserId !== host._id) continue;
      if (row.expiresAt <= now) continue;
      const idleExpiry = row.lastActivityAt + row.idleTimeoutMinutes * 60_000;
      if (idleExpiry <= now) continue;
      if (normalizedDeviceID && row.guestDeviceId && row.guestDeviceId !== normalizedDeviceID) continue;
      return {
        sessionId: String(row._id),
        inviteId: String(row.inviteId),
        label: row.label,
        hostDeviceId: row.hostDeviceId,
        guestDeviceId: row.guestDeviceId,
        hostUserId: host.userId,
        hostEmail: host.email,
        hostName: host.fullName,
        policy: row.policy,
        expiresAt: row.expiresAt,
        idleTimeoutMinutes: row.idleTimeoutMinutes,
        lastActivityAt: row.lastActivityAt,
      };
    }
    return null;
  },
});

export const touchSessionActivity = mutation({
  args: {
    tokenHash: v.string(),
    sessionId: v.id("hostShareSessions"),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");
    const row = await ctx.db.get(args.sessionId);
    if (!row) throw new Error("Session not found");
    if (row.hostUserId !== session.user._id) throw new Error("Only the host can touch this session");
    if (row.status !== "active") throw new Error("Session is not active");
    const now = Date.now();
    if (row.expiresAt <= now) throw new Error("Session expired");
    await ctx.db.patch(args.sessionId, { lastActivityAt: now });
    return { ok: true, lastActivityAt: now };
  },
});
