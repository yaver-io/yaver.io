import { mutation, query } from "./_generated/server";
import { v } from "convex/values";
import { validateSessionInternal } from "./auth";
import { Id } from "./_generated/dataModel";
import { internal } from "./_generated/api";
import { guestInviteHtml } from "./email";
import { getActiveInfraGrant, guestCanReachHostDevice, guestCanReachSpecificHostDevice, listGrantedDeviceIdsForGrant, listGrantedMachineIdsForGrant, revokeInfraGrantsBetweenUsers } from "./access";

const MAX_GUESTS_PER_HOST = 5;
const INVITATION_TTL_MS = 2 * 24 * 60 * 60 * 1000; // 2 days

/** Generate a short 6-character uppercase invite code. */
function generateInviteCode(): string {
  const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"; // no 0/O/1/I to avoid confusion
  const buf = new Uint8Array(6);
  crypto.getRandomValues(buf);
  return Array.from(buf).map(b => chars[b % chars.length]).join("");
}

async function deviceSummariesById(ctx: any, deviceIds: string[]) {
  const seen = new Set<string>();
  const out: Array<{
    deviceId: string;
    name: string;
    platform: string;
    lastHeartbeat?: number;
  }> = [];
  for (const rawId of deviceIds) {
    const deviceId = String(rawId || "").trim();
    if (!deviceId || seen.has(deviceId)) continue;
    seen.add(deviceId);
    const device = await ctx.db
      .query("devices")
      .withIndex("by_deviceId", (q: any) => q.eq("deviceId", deviceId))
      .unique();
    if (!device) {
      out.push({ deviceId, name: deviceId, platform: "" });
      continue;
    }
    out.push({
      deviceId,
      name: device.name,
      platform: device.platform,
      lastHeartbeat: device.lastHeartbeat,
    });
  }
  return out;
}

async function deviceSummariesForHost(ctx: any, hostUserId: any) {
  const devices = await ctx.db
    .query("devices")
    .withIndex("by_userId", (q: any) => q.eq("userId", hostUserId))
    .collect();
  return devices.map((device: any) => ({
    deviceId: device.deviceId,
    name: device.name,
    platform: device.platform,
    lastHeartbeat: device.lastHeartbeat,
  }));
}

async function upsertGuestConversion(ctx: any, p: {
  hostUserId: any;
  guestUserId: any;
  inviteId?: any;
  accessId?: any;
  sourceScope?: "full" | "feedback-only" | "sdk-project" | "support";
  sourceProjects?: string[];
  acceptedAt?: number;
  activityAt?: number;
}) {
  const now = Date.now();
  const existing = await ctx.db
    .query("guestConversions")
    .withIndex("by_host_guest", (q: any) => q.eq("hostUserId", p.hostUserId).eq("guestUserId", p.guestUserId))
    .first();

  const cleanProjects = (p.sourceProjects ?? []).map((s) => String(s).trim()).filter(Boolean);
  if (existing) {
    await ctx.db.patch(existing._id, {
      ...(p.inviteId ? { inviteId: p.inviteId } : {}),
      ...(p.accessId ? { accessId: p.accessId } : {}),
      ...(p.sourceScope ? { sourceScope: p.sourceScope } : {}),
      ...(cleanProjects.length > 0 ? { sourceProjects: cleanProjects } : {}),
      ...(p.activityAt ? {
        lastGuestActivityAt: p.activityAt,
        guestActivityCount: (existing.guestActivityCount ?? 0) + 1,
      } : {}),
      updatedAt: now,
    });
    return;
  }

  const firstAcceptedAt = p.acceptedAt ?? p.activityAt ?? now;
  await ctx.db.insert("guestConversions", {
    hostUserId: p.hostUserId,
    guestUserId: p.guestUserId,
    ...(p.inviteId ? { inviteId: p.inviteId } : {}),
    ...(p.accessId ? { accessId: p.accessId } : {}),
    ...(p.sourceScope ? { sourceScope: p.sourceScope } : {}),
    ...(cleanProjects.length > 0 ? { sourceProjects: cleanProjects } : {}),
    firstAcceptedAt,
    ...(p.activityAt ? { lastGuestActivityAt: p.activityAt, guestActivityCount: 1 } : {}),
    conversionState: "guest-active",
    createdAt: now,
    updatedAt: now,
  });
}

// ─── Mutations ──────────────────────────────────────────────────

/**
 * Invite a guest. Only the host can invite. The host may target:
 *   - an email (guestEmail) — invitee signs in with a matching email to auto-accept,
 *     or uses the 6-char invite code if their email differs.
 *   - a public userId string (guestUserId) — the invitation is pinned to that user's
 *     account, code works too. Email is empty. This is what you tell a friend to
 *     type: "open your Yaver app, settings, copy user id, send it to me".
 *
 * Optionally, the host may pre-scope the invitation to a subset of their devices
 * (proposedDeviceIds). The guest can trim this further at accept time.
 */
export const invite = mutation({
  args: {
    tokenHash: v.string(),
    guestEmail: v.optional(v.string()),
    guestUserId: v.optional(v.string()),
    proposedDeviceIds: v.optional(v.array(v.string())),
    // Access tier: "full" (classic teammate) or "feedback-only" (hardened end-user).
    // Default is "feedback-only" — end-user distribution via Feedback SDK is the common
    // case and safer by default. Use --scope=full for teammate invites.
    scope: v.optional(v.union(v.literal("full"), v.literal("feedback-only"), v.literal("sdk-project"))),
    // Optional project narrowing — restrict this grant to one or more project
    // slugs/names on the host. Useful when a dev wants to let users file
    // feedback about Project A without exposing B & C. Empty = all projects.
    allowedProjects: v.optional(v.array(v.string())),
    // canVibe opts a tester (scope="sdk-project") into the AI-improve surface
    // (/vibing). Ignored for any other scope. Default false = test-only.
    canVibe: v.optional(v.boolean()),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const hostUserId = session.user._id;
    const rawEmail = (args.guestEmail ?? "").trim().toLowerCase();
    const rawUserId = (args.guestUserId ?? "").trim();

    if (!rawEmail && !rawUserId) {
      throw new Error("Provide either guestEmail or guestUserId");
    }

    // Resolve guest user (if any). Precedence: userId > email.
    let guestUser: Awaited<ReturnType<typeof ctx.db.get>> extends infer T ? (T | null) : never = null;
    if (rawUserId) {
      const matches = await ctx.db
        .query("users")
        .filter((q) => q.eq(q.field("userId"), rawUserId))
        .collect();
      guestUser = matches[0] ?? null;
      if (!guestUser) throw new Error("No Yaver user found with that user id");
    } else if (rawEmail) {
      guestUser = await ctx.db
        .query("users")
        .withIndex("by_email", (q) => q.eq("email", rawEmail))
        .first();
    }

    // Resolved email (for storage): prefer the host's-stated email; fall back to the
    // resolved guest's own email when invited by userId.
    const storedEmail = rawEmail || (guestUser?.email?.toLowerCase() ?? "");

    // Can't invite yourself
    if (
      (storedEmail && storedEmail === session.user.email.toLowerCase()) ||
      (guestUser && guestUser._id === hostUserId)
    ) {
      throw new Error("Cannot invite yourself");
    }

    // Validate proposed device scope — only the host's own devices
    const proposed = (args.proposedDeviceIds ?? []).map((s) => s.trim()).filter(Boolean);
    if (proposed.length > 0) {
      const hostDevices = await ctx.db
        .query("devices")
        .withIndex("by_userId", (q) => q.eq("userId", hostUserId))
        .collect();
      const ownedIds = new Set(hostDevices.map((d) => d.deviceId));
      for (const id of proposed) {
        if (!ownedIds.has(id)) {
          throw new Error(`Device not owned by host: ${id}`);
        }
      }
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

    // Check for existing active invitation for this email (if email-routed)
    if (storedEmail) {
      const existingInvitations = await ctx.db
        .query("guestInvitations")
        .withIndex("by_host_guest", (q) =>
          q.eq("hostUserId", hostUserId).eq("guestEmail", storedEmail)
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
    }

    // Pending invitation targeted at the same userId?
    if (guestUser) {
      const pendingForUser = await ctx.db
        .query("guestInvitations")
        .withIndex("by_host_guestUser", (q) =>
          q.eq("hostUserId", hostUserId).eq("guestUserId", guestUser!._id)
        )
        .collect();
      for (const inv of pendingForUser) {
        if (inv.status === "pending" && inv.expiresAt > Date.now()) {
          throw new Error("Pending invitation already exists for this user");
        }
      }

      // Active access?
      const existingAccess = await ctx.db
        .query("guestAccess")
        .withIndex("by_host_guest", (q) =>
          q.eq("hostUserId", hostUserId).eq("guestUserId", guestUser!._id)
        )
        .filter((q) => q.eq(q.field("revokedAt"), undefined))
        .first();

      if (existingAccess) {
        throw new Error("This user already has guest access");
      }
    }

    const inviteCode = generateInviteCode();
    const now = Date.now();
    // Default new invitations to the hardened scope. Hosts who want the
    // classic teammate access must opt in with scope=full.
    const scope: "full" | "feedback-only" | "sdk-project" = args.scope ?? "feedback-only";
    const invitationDoc: Record<string, unknown> = {
      hostUserId,
      guestEmail: storedEmail,
      inviteCode,
      status: "pending",
      scope,
      createdAt: now,
      expiresAt: now + INVITATION_TTL_MS,
    };
    if (guestUser) invitationDoc.guestUserId = guestUser._id;
    if (rawUserId) invitationDoc.invitedByUserId = true;
    if (proposed.length > 0) invitationDoc.proposedDeviceIds = proposed;
    // canVibe is only meaningful for the tester tier — never let a full or
    // feedback-only invite carry it (the agent would ignore it anyway, but
    // keep the stored state coherent).
    if (scope === "sdk-project" && args.canVibe === true) invitationDoc.canVibe = true;
    const allowedProjects = (args.allowedProjects ?? [])
      .map((s) => s.trim())
      .filter(Boolean);
    if (allowedProjects.length > 0) invitationDoc.allowedProjects = allowedProjects;
    await ctx.db.insert("guestInvitations", invitationDoc as any);

    // Send invite email if we have a destination address and this was email-targeted.
    // When invited-by-userId we still email them if we know the email, but skip when none.
    if (storedEmail) {
      await ctx.scheduler.runAfter(0, internal.email.send, {
        from: "Yaver <hello@yaver.io>",
        to: storedEmail,
        subject: `${session.user.fullName} invited you to Yaver`,
        html: guestInviteHtml(session.user.fullName, inviteCode),
      });
    }

    return {
      ok: true,
      inviteCode,
      guestRegistered: !!guestUser,
      guestUserId: guestUser?.userId,
      guestEmail: storedEmail || undefined,
      scope,
    };
  },
});

/**
 * Shared materializer: turn an accepted invitation into the live access/grant
 * rows. If the guest passed approvedDeviceIds (a subset of proposedDeviceIds
 * when present, otherwise arbitrary host devices), the scope is honored —
 * otherwise the default is "all host devices" (current behavior).
 */
async function materializeInvitationAccept(
  ctx: any,
  invitation: any,
  guestUserDocId: any,
  approvedDeviceIds: string[] | undefined,
  now: number,
) {
  await ctx.db.patch(invitation._id, {
    status: "accepted",
    guestUserId: guestUserDocId,
    acceptedAt: now,
  });

  const accessId = await ctx.db.insert("guestAccess", {
    hostUserId: invitation.hostUserId,
    guestUserId: guestUserDocId,
    grantedAt: now,
    // Propagate the access tier the host picked at invite time. Legacy invitations
    // without scope keep the old behavior (treated as "full" downstream).
    ...(invitation.scope ? { scope: invitation.scope } : {}),
    ...(Array.isArray(invitation.allowedProjects) && invitation.allowedProjects.length > 0
      ? { allowedProjects: invitation.allowedProjects as string[] }
      : {}),
    // Carry the tester's vibe opt-in from the invitation into the live grant.
    ...(invitation.canVibe === true ? { canVibe: true } : {}),
  });

  await upsertGuestConversion(ctx, {
    hostUserId: invitation.hostUserId,
    guestUserId: guestUserDocId,
    inviteId: invitation._id,
    accessId,
    sourceScope: invitation.scope,
    sourceProjects: Array.isArray(invitation.allowedProjects) ? invitation.allowedProjects : undefined,
    acceptedAt: now,
  });

  // Normalize + clamp approved list against proposal (if any) and ownership.
  const proposed: string[] = Array.isArray(invitation.proposedDeviceIds) ? invitation.proposedDeviceIds : [];
  const chosenRaw = (approvedDeviceIds ?? []).map((s) => String(s).trim()).filter(Boolean);
  // If the host proposed a specific scope and the guest didn't trim, honor the proposal.
  const chosen = chosenRaw.length > 0
    ? chosenRaw
    : (proposed.length > 0 ? proposed : []);

  // No scoping = legacy behavior ("shareAllDevices" default in access checks).
  if (chosen.length === 0) return;

  // Verify every chosen id is owned by the host.
  const hostDevices = await ctx.db
    .query("devices")
    .withIndex("by_userId", (q: any) => q.eq("userId", invitation.hostUserId))
    .collect();
  const ownedIds = new Set(hostDevices.map((d: any) => d.deviceId));

  // If host pre-scoped, clamp chosen to proposed ∩ owned.
  const allowedSet = proposed.length > 0
    ? new Set(proposed.filter((id: string) => ownedIds.has(id)))
    : ownedIds;
  const finalIds = chosen.filter((id) => allowedSet.has(id));
  if (finalIds.length === 0) return; // nothing to grant

  // Create an explicit infraAccessGrant + links so the access-check path
  // in access.ts recognizes the narrow scope.
  const grantId = await ctx.db.insert("infraAccessGrants", {
    hostUserId: invitation.hostUserId,
    guestUserId: guestUserDocId,
    status: "active",
    shareAllDevices: false,
    grantedAt: now,
    updatedAt: now,
  });
  for (const deviceId of finalIds) {
    await ctx.db.insert("infraAccessGrantDevices", {
      grantId,
      hostUserId: invitation.hostUserId,
      guestUserId: guestUserDocId,
      deviceId,
      createdAt: now,
    });
  }
}

/**
 * Accept a pending invitation. Called by the guest.
 * The guest must be signed in and their email must match the invitation.
 * Optional approvedDeviceIds narrows the accepted device scope.
 */
export const accept = mutation({
  args: {
    tokenHash: v.string(),
    hostUserId: v.id("users"),
    approvedDeviceIds: v.optional(v.array(v.string())),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const guestEmail = session.user.email.toLowerCase();

    // Find the pending invitation — match by (host, email) first, then fall back
    // to (host, guestUserId) for invites that were pinned to this user's id.
    let invitation = await ctx.db
      .query("guestInvitations")
      .withIndex("by_host_guest", (q) =>
        q.eq("hostUserId", args.hostUserId).eq("guestEmail", guestEmail)
      )
      .filter((q) => q.eq(q.field("status"), "pending"))
      .first();

    if (!invitation) {
      invitation = await ctx.db
        .query("guestInvitations")
        .withIndex("by_host_guestUser", (q) =>
          q.eq("hostUserId", args.hostUserId).eq("guestUserId", session.user._id)
        )
        .filter((q) => q.eq(q.field("status"), "pending"))
        .first();
    }

    if (!invitation) {
      throw new Error("No pending invitation found");
    }

    if (invitation.expiresAt < Date.now()) {
      // Mark as expired
      await ctx.db.patch(invitation._id, { status: "revoked", revokedAt: Date.now() });
      throw new Error("Invitation has expired");
    }

    const now = Date.now();
    await materializeInvitationAccept(ctx, invitation, session.user._id, args.approvedDeviceIds, now);
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
    approvedDeviceIds: v.optional(v.array(v.string())),
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

    // If host pinned the invite to a specific userId, only that user may accept.
    if (invitation.guestUserId && invitation.guestUserId !== session.user._id) {
      throw new Error("This invite code is reserved for a different account");
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
    await materializeInvitationAccept(ctx, invitation, session.user._id, args.approvedDeviceIds, now);

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
 * Mint a delegated Feedback SDK token for a guest who has the hardened
 * repo-scoped SDK grant. The token is owned by the host account but carries
 * guest identity + project/device narrowing so the host agent can enforce
 * "Feedback SDK only, this repo only, this device only" on every request.
 */
export const mintGuestFeedbackSdkToken = mutation({
  args: {
    guestTokenHash: v.string(),
    sdkTokenHash: v.string(),
    hostUserId: v.id("users"),
    targetDeviceId: v.string(),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.guestTokenHash);
    if (!session) throw new Error("Unauthorized");

    const access = await ctx.db
      .query("guestAccess")
      .withIndex("by_host_guest", (q) =>
        q.eq("hostUserId", args.hostUserId).eq("guestUserId", session.user._id)
      )
      .filter((q) => q.eq(q.field("revokedAt"), undefined))
      .first();
    if (!access) throw new Error("No active guest access for this host");
    if ((access.scope ?? "full") !== "sdk-project") {
      throw new Error("This guest grant is not enabled for Feedback SDK repo-scoped access");
    }

    const allowed = await guestCanReachSpecificHostDevice(
      ctx,
      args.hostUserId,
      session.user._id,
      args.targetDeviceId,
    );
    if (!allowed) {
      throw new Error("This guest grant does not allow the selected host device");
    }

    const existing = await ctx.db
      .query("sdkTokens")
      .withIndex("by_tokenHash", (q) => q.eq("tokenHash", args.sdkTokenHash))
      .unique();
    if (existing) throw new Error("SDK token already exists");

    const allowedProjects = Array.isArray(access.allowedProjects)
      ? access.allowedProjects.map((item) => String(item).trim()).filter(Boolean)
      : [];

    const expiresAt = Date.now() + 30 * 24 * 60 * 60 * 1000;
    await ctx.db.insert("sdkTokens", {
      tokenHash: args.sdkTokenHash,
      userId: args.hostUserId,
      label: `guest-feedback-sdk:${session.user.email}:${args.targetDeviceId}`,
      scopes: [
        "feedback",
        "blackbox",
        "voice",
        "health",
        "todolist",
        "guest-reload",
        "guest-vibing",
      ],
      delegatedGuestUserId: session.user._id,
      delegatedGuestScope: "sdk-project",
      sourceSurface: "feedback-sdk",
      targetDeviceId: args.targetDeviceId,
      ...(allowedProjects.length > 0 ? { allowedProjects } : {}),
      expiresAt,
      createdAt: Date.now(),
    });

    return {
      ok: true,
      expiresAt,
      allowedProjects,
    };
  },
});

/**
 * Look up a pending invitation by code so the guest can preview host info + the
 * host's proposed device scope before committing. Callable by any signed-in user.
 */
export const findByCode = query({
  args: {
    tokenHash: v.string(),
    inviteCode: v.string(),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) return null;

    const code = args.inviteCode.toUpperCase().trim();
    const invitation = await ctx.db
      .query("guestInvitations")
      .withIndex("by_inviteCode", (q) => q.eq("inviteCode", code))
      .first();
    if (!invitation) return null;
    if (invitation.status !== "pending") return null;
    if (invitation.expiresAt < Date.now()) return null;
    if (invitation.guestUserId && invitation.guestUserId !== session.user._id) {
      // Not for this user — don't leak details.
      return null;
    }

    const host = await ctx.db.get(invitation.hostUserId);
    const proposed: string[] = Array.isArray(invitation.proposedDeviceIds) ? invitation.proposedDeviceIds : [];

    // Enumerate host devices so the guest UI can render the picker.
    const hostDevices = await ctx.db
      .query("devices")
      .withIndex("by_userId", (q) => q.eq("userId", invitation.hostUserId))
      .collect();
    const hostDeviceSummaries = hostDevices.map((d) => ({
      deviceId: d.deviceId,
      name: d.name,
      platform: d.platform,
      lastHeartbeat: d.lastHeartbeat,
      proposed: proposed.length === 0 || proposed.includes(d.deviceId),
    }));

    return {
      inviteCode: code,
      hostUserId: invitation.hostUserId,
      hostName: host?.fullName ?? "Unknown",
      hostEmail: host?.email ?? "",
      hostUserIdString: host?.userId ?? "",
      proposedDeviceIds: proposed,
      hostDevices: hostDeviceSummaries,
      invitedByUserId: !!invitation.invitedByUserId,
      expiresAt: invitation.expiresAt,
      createdAt: invitation.createdAt,
    };
  },
});

/**
 * Look up a user's public userId → minimal profile (used by the host UI to
 * confirm "yes this is my cousin" before firing an invite).
 */
export const lookupPublicUser = query({
  args: {
    tokenHash: v.string(),
    userId: v.string(),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) return null;
    const needle = args.userId.trim();
    if (!needle) return null;
    const matches = await ctx.db
      .query("users")
      .filter((q) => q.eq(q.field("userId"), needle))
      .collect();
    const user = matches[0];
    if (!user) return null;
    // Redact: return only display-safe fields. Do NOT leak email full name if
    // user marked private — for now we return them; privacy toggles can be
    // layered later.
    return {
      userId: user.userId,
      fullName: user.fullName,
      email: user.email,
    };
  },
});

/**
 * Revoke guest access. Called by the host.
 * Works for both pending invitations and active access.
 * Accepts either guestEmail (legacy) or guestUserId (public userId string)
 * — the latter is required when the guest was invited via userId (no email).
 */
export const revoke = mutation({
  args: {
    tokenHash: v.string(),
    guestEmail: v.optional(v.string()),
    guestUserId: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const hostUserId = session.user._id;
    const guestEmail = (args.guestEmail ?? "").trim().toLowerCase();
    const guestUserIdStr = (args.guestUserId ?? "").trim();

    if (!guestEmail && !guestUserIdStr) {
      throw new Error("guestEmail or guestUserId is required");
    }

    // Resolve guest user first (prefer userId when provided).
    let guestUser: Awaited<ReturnType<typeof ctx.db.get>> extends infer T ? (T | null) : never = null;
    if (guestUserIdStr) {
      const matches = await ctx.db
        .query("users")
        .filter((q) => q.eq(q.field("userId"), guestUserIdStr))
        .collect();
      guestUser = matches[0] ?? null;
    } else if (guestEmail) {
      guestUser = await ctx.db
        .query("users")
        .withIndex("by_email", (q) => q.eq("email", guestEmail))
        .first();
    }

    // Revoke any pending invitations, indexed by whichever we have.
    const toRevoke = new Set<string>();
    if (guestEmail) {
      const byEmail = await ctx.db
        .query("guestInvitations")
        .withIndex("by_host_guest", (q) =>
          q.eq("hostUserId", hostUserId).eq("guestEmail", guestEmail)
        )
        .collect();
      byEmail.forEach((i) => toRevoke.add(String(i._id)));
    }
    if (guestUser) {
      const byUser = await ctx.db
        .query("guestInvitations")
        .withIndex("by_host_guestUser", (q) =>
          q.eq("hostUserId", hostUserId).eq("guestUserId", guestUser!._id)
        )
        .collect();
      byUser.forEach((i) => toRevoke.add(String(i._id)));
    }
    for (const idStr of toRevoke) {
      const inv = await ctx.db.get(idStr as any);
      if (!inv) continue;
      if ((inv as any).status === "pending" || (inv as any).status === "accepted") {
        await ctx.db.patch((inv as any)._id, { status: "revoked", revokedAt: Date.now() });
      }
    }

    if (guestUser) {
      const now = Date.now();
      const accessRecords = await ctx.db
        .query("guestAccess")
        .withIndex("by_host_guest", (q) =>
          q.eq("hostUserId", hostUserId).eq("guestUserId", guestUser!._id)
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
      userId?: string;
      createdAt: number;
      expiresAt?: number;
      acceptedAt?: number;
      revokedAt?: number;
      inviteCode?: string;
      invitedByUserId?: boolean;
      proposedDeviceIds?: string[];
      proposedDevices?: Array<{
        deviceId: string;
        name: string;
        platform: string;
        lastHeartbeat?: number;
      }>;
    }> = [];

    for (const inv of invitations) {
      let status = inv.status as "pending" | "accepted" | "revoked" | "expired";
      if (status === "pending" && inv.expiresAt < Date.now()) {
        status = "expired";
      }

      let fullName: string | undefined;
      let userIdStr: string | undefined;
      if (inv.guestUserId) {
        const guest = await ctx.db.get(inv.guestUserId);
        fullName = guest?.fullName;
        userIdStr = guest?.userId;
      }
      const proposedDeviceIds = Array.isArray(inv.proposedDeviceIds) ? inv.proposedDeviceIds : [];
      let displayDeviceIds = proposedDeviceIds;
      if (status === "accepted" && inv.guestUserId) {
        const grant = await getActiveInfraGrant(ctx, hostUserId, inv.guestUserId);
        if (grant) {
          displayDeviceIds = grant.shareAllDevices
            ? (await deviceSummariesForHost(ctx, hostUserId)).map((d: { deviceId: string }) => d.deviceId)
            : await listGrantedDeviceIdsForGrant(ctx, grant._id);
        }
      }

      result.push({
        email: inv.guestEmail,
        status,
        fullName,
        userId: userIdStr,
        createdAt: inv.createdAt,
        expiresAt: inv.expiresAt,
        acceptedAt: inv.acceptedAt,
        revokedAt: inv.revokedAt,
        inviteCode: status === "pending" ? inv.inviteCode : undefined,
        invitedByUserId: inv.invitedByUserId,
        proposedDeviceIds: displayDeviceIds,
        proposedDevices: displayDeviceIds.length > 0 ? await deviceSummariesById(ctx, displayDeviceIds) : undefined,
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

    // Get pending invitations for this email OR pinned to our userId.
    const byEmail = await ctx.db
      .query("guestInvitations")
      .withIndex("by_guestEmail", (q) => q.eq("guestEmail", guestEmail))
      .filter((q) => q.eq(q.field("status"), "pending"))
      .collect();
    const byUserId = await ctx.db
      .query("guestInvitations")
      .withIndex("by_guestUserId", (q) => q.eq("guestUserId", guestUserId))
      .filter((q) => q.eq(q.field("status"), "pending"))
      .collect();
    const seen = new Set<string>();
    const pendingInvitations = [...byEmail, ...byUserId].filter((inv) => {
      const id = String(inv._id);
      if (seen.has(id)) return false;
      seen.add(id);
      return true;
    });

    const pending: Array<{
      inviteId: string;
      inviteCode: string;
      hostUserId: string;
      hostName: string;
      hostEmail: string;
      hostUserIdString?: string;
      createdAt: number;
      expiresAt: number;
      invitedByUserId?: boolean;
      proposedDeviceIds?: string[];
      proposedDevices?: Array<{
        deviceId: string;
        name: string;
        platform: string;
        lastHeartbeat?: number;
      }>;
    }> = [];

    for (const inv of pendingInvitations) {
      if (inv.expiresAt < Date.now()) continue;
      const host = await ctx.db.get(inv.hostUserId);
      if (!host) continue;
      pending.push({
        inviteId: String(inv._id),
        inviteCode: inv.inviteCode,
        hostUserId: inv.hostUserId,
        hostName: host.fullName,
        hostEmail: host.email,
        hostUserIdString: host.userId,
        createdAt: inv.createdAt,
        expiresAt: inv.expiresAt,
        invitedByUserId: inv.invitedByUserId,
        proposedDeviceIds: inv.proposedDeviceIds,
        proposedDevices: Array.isArray(inv.proposedDeviceIds) && inv.proposedDeviceIds.length > 0
          ? await deviceSummariesById(ctx, inv.proposedDeviceIds)
          : undefined,
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
      devices?: Array<{
        deviceId: string;
        name: string;
        platform: string;
        lastHeartbeat?: number;
      }>;
    }> = [];

    for (const access of accessRecords) {
      const host = await ctx.db.get(access.hostUserId);
      if (!host) continue;
      const grant = await getActiveInfraGrant(ctx, access.hostUserId, guestUserId);
      const devices = grant
        ? (
            grant.shareAllDevices
              ? await deviceSummariesForHost(ctx, access.hostUserId)
              : await deviceSummariesById(ctx, await listGrantedDeviceIdsForGrant(ctx, grant._id))
          )
        : await deviceSummariesForHost(ctx, access.hostUserId);
      active.push({
        hostUserId: access.hostUserId,
        hostName: host.fullName,
        hostEmail: host.email,
        grantedAt: access.grantedAt,
        devices,
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
    deviceId: v.optional(v.string()),
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
      let allowed = false;
      if (args.deviceId && args.deviceId.trim() !== "") {
        allowed = await guestCanReachSpecificHostDevice(ctx, hostUserId, access.guestUserId, args.deviceId);
      } else {
        allowed = await guestCanReachHostDevice(ctx, hostUserId, access.guestUserId);
      }
      if (!allowed) continue;
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
      scope: "full" | "feedback-only" | "sdk-project" | "support";
      allowedProjects?: string[];
      canVibe?: boolean;
      dailyTokenLimit?: number;
      allowedRunners?: string[];
      usageMode?: string;
      schedule?: { startHour: number; endHour: number; timezone?: string };
      shareAllDevices?: boolean;
      deviceIds?: string[];
      shareAllMachines?: boolean;
      machineIds?: Id<"cloudMachines">[];
      resourcePreset?: string;
      useHostApiKeys?: boolean;
      allowGuestProvidedApiKeys?: boolean;
      allowDesktopControl?: boolean;
      allowBrowserControl?: boolean;
      allowTunnelForward?: boolean;
      requireIsolation?: boolean;
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
        // Legacy rows (pre-scope) are treated as "full" so upgrading the agent
        // without re-inviting existing teammates doesn't silently downgrade them.
        scope: access.scope ?? "full",
        allowedProjects: access.allowedProjects,
        canVibe: access.canVibe,
        dailyTokenLimit: access.dailyTokenLimit,
        allowedRunners: grant?.allowedRunners ?? access.allowedRunners,
        usageMode: grant?.usageMode ?? access.usageMode,
        schedule: grant?.schedule ?? access.schedule,
        shareAllDevices: grant?.shareAllDevices ?? true,
        deviceIds,
        shareAllMachines: grant?.shareAllMachines ?? false,
        machineIds,
        resourcePreset:
          grant?.resourcePreset ??
          (
            grant?.allowDesktopControl
              ? (grant?.useHostApiKeys ? "desktop-control-with-host-keys" : "desktop-control")
              : (grant?.useHostApiKeys ? "machine-with-host-keys" : "machine-only")
          ),
        useHostApiKeys: grant?.useHostApiKeys ?? false,
        allowGuestProvidedApiKeys: grant?.allowGuestProvidedApiKeys ?? true,
        allowDesktopControl: grant?.allowDesktopControl ?? false,
        allowBrowserControl: grant?.allowBrowserControl ?? (grant?.allowDesktopControl ?? false),
        allowTunnelForward: grant?.allowTunnelForward ?? false,
        requireIsolation: grant?.requireIsolation ?? false,
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
    resourcePreset: v.optional(v.string()),
    useHostApiKeys: v.optional(v.boolean()),
    allowGuestProvidedApiKeys: v.optional(v.boolean()),
    allowDesktopControl: v.optional(v.boolean()),
    allowBrowserControl: v.optional(v.boolean()),
    allowTunnelForward: v.optional(v.boolean()),
    requireIsolation: v.optional(v.boolean()),
    cpuLimitPercent: v.optional(v.number()),
    ramLimitMb: v.optional(v.number()),
    priorityMode: v.optional(v.string()),
    schedule: v.optional(v.object({
      startHour: v.number(),
      endHour: v.number(),
      timezone: v.optional(v.string()),
    })),
    // Change the access tier on an existing grant. Use with care: downgrading
    // "full" → "feedback-only" immediately takes effect on the agent (within
    // the 10s config refresh); upgrading is equally immediate.
    scope: v.optional(v.union(v.literal("full"), v.literal("feedback-only"), v.literal("sdk-project"))),
    // Narrow (or clear, by passing []) the set of projects this guest can
    // see feedback for / trigger fix-tasks against.
    allowedProjects: v.optional(v.array(v.string())),
    // Toggle the tester's AI-improve (vibe) opt-in. Only sticks while the
    // effective scope is sdk-project; a scope downgrade clears it.
    canVibe: v.optional(v.boolean()),
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
    if (args.resourcePreset !== undefined) {
      const validResourcePresets = [
        "machine-only",
        "machine-with-host-keys",
        "desktop-control",
        "desktop-control-with-host-keys",
      ];
      if (!validResourcePresets.includes(args.resourcePreset)) {
        throw new Error(`Invalid resourcePreset: ${args.resourcePreset}. Must be one of: ${validResourcePresets.join(", ")}`);
      }
    }

    let presetDefaults: {
      useHostApiKeys: boolean;
      allowDesktopControl: boolean;
      allowBrowserControl: boolean;
    } | null = null;
    if (args.resourcePreset === "machine-only") {
      presetDefaults = { useHostApiKeys: false, allowDesktopControl: false, allowBrowserControl: false };
    } else if (args.resourcePreset === "machine-with-host-keys") {
      presetDefaults = { useHostApiKeys: true, allowDesktopControl: false, allowBrowserControl: false };
    } else if (args.resourcePreset === "desktop-control") {
      presetDefaults = { useHostApiKeys: false, allowDesktopControl: true, allowBrowserControl: true };
    } else if (args.resourcePreset === "desktop-control-with-host-keys") {
      presetDefaults = { useHostApiKeys: true, allowDesktopControl: true, allowBrowserControl: true };
    }
    if (presetDefaults && args.useHostApiKeys !== undefined && args.useHostApiKeys !== presetDefaults.useHostApiKeys) {
      throw new Error("useHostApiKeys conflicts with resourcePreset");
    }
    if (presetDefaults && args.allowDesktopControl !== undefined && args.allowDesktopControl !== presetDefaults.allowDesktopControl) {
      throw new Error("allowDesktopControl conflicts with resourcePreset");
    }
    if (presetDefaults && args.allowBrowserControl !== undefined && args.allowBrowserControl !== presetDefaults.allowBrowserControl) {
      throw new Error("allowBrowserControl conflicts with resourcePreset");
    }

    // Build patch object — only include provided fields
    const patch: Record<string, unknown> = {};
    if (args.dailyTokenLimit !== undefined) patch.dailyTokenLimit = args.dailyTokenLimit;
    if (args.allowedRunners !== undefined) patch.allowedRunners = args.allowedRunners;
    if (args.usageMode !== undefined) patch.usageMode = args.usageMode;
    if (args.schedule !== undefined) patch.schedule = args.schedule;
    if (args.scope !== undefined) patch.scope = args.scope;
    if (args.allowedProjects !== undefined) {
      const cleaned = args.allowedProjects.map((s) => s.trim()).filter(Boolean);
      patch.allowedProjects = cleaned.length > 0 ? cleaned : undefined;
    }
    // canVibe only applies to the tester tier. Clear it whenever the effective
    // scope isn't sdk-project so a scope downgrade can't leave a stale vibe
    // grant behind.
    const effectiveScope = args.scope ?? access.scope ?? "full";
    if (args.canVibe !== undefined || (args.scope !== undefined && effectiveScope !== "sdk-project")) {
      patch.canVibe = effectiveScope === "sdk-project" && args.canVibe === true ? true : undefined;
    }

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
    if (args.resourcePreset !== undefined) grantPatch.resourcePreset = args.resourcePreset;
    if (args.shareAllDevices !== undefined) grantPatch.shareAllDevices = args.shareAllDevices;
    if (args.shareAllMachines !== undefined) grantPatch.shareAllMachines = args.shareAllMachines;
    if (presetDefaults) {
      grantPatch.useHostApiKeys = presetDefaults.useHostApiKeys;
      grantPatch.allowDesktopControl = presetDefaults.allowDesktopControl;
      grantPatch.allowBrowserControl = presetDefaults.allowBrowserControl;
    } else {
      if (args.useHostApiKeys !== undefined) grantPatch.useHostApiKeys = args.useHostApiKeys;
      if (args.allowDesktopControl !== undefined) grantPatch.allowDesktopControl = args.allowDesktopControl;
      if (args.allowBrowserControl !== undefined) grantPatch.allowBrowserControl = args.allowBrowserControl;
    }
    if (args.allowGuestProvidedApiKeys !== undefined) {
      grantPatch.allowGuestProvidedApiKeys = args.allowGuestProvidedApiKeys;
    }
    if (args.allowTunnelForward !== undefined) grantPatch.allowTunnelForward = args.allowTunnelForward;
    if (args.requireIsolation !== undefined) grantPatch.requireIsolation = args.requireIsolation;
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

    await upsertGuestConversion(ctx, {
      hostUserId,
      guestUserId: guestUser._id,
      activityAt: Date.now(),
    });

    return { ok: true };
  },
});

/**
 * Guest-facing conversion state for all surfaces. Phone/web use this
 * as the "you started from Alice's shared runtime; upgrade to your own
 * preview/compute/publish when ready" shelf. Watch/car/TV can reduce it
 * to one sentence because the object is already compact and statusful.
 */
export const getGuestConversionSurface = query({
  args: {
    tokenHash: v.string(),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) return { sources: [], recommendedServices: [] };

    const rows = await ctx.db
      .query("guestConversions")
      .withIndex("by_guest", (q) => q.eq("guestUserId", session.user._id))
      .collect();

    const settings = await ctx.db
      .query("userSettings")
      .withIndex("by_userId", (q) => q.eq("userId", session.user._id))
      .first();
    const enabledServices = settings?.managedServices ?? {};

    const sources = [];
    for (const row of rows.sort((a, b) => b.updatedAt - a.updatedAt)) {
      const host = await ctx.db.get(row.hostUserId);
      if (!host) continue;
      sources.push({
        hostUserId: host.userId,
        hostName: host.fullName,
        hostEmail: host.email,
        sourceScope: row.sourceScope ?? "full",
        sourceProjects: row.sourceProjects ?? [],
        firstAcceptedAt: row.firstAcceptedAt,
        lastGuestActivityAt: row.lastGuestActivityAt,
        guestActivityCount: row.guestActivityCount ?? 0,
        conversionState: row.conversionState,
        firstManagedService: row.firstManagedService,
        enabledServices: row.enabledServices ?? [],
      });
    }

    const hasReload = enabledServices.reload === true;
    const hasAgentBox = enabledServices.agentBox === true;
    const hasPublish = enabledServices.publish === true;
    const recommendedServices = [
      !hasReload ? {
        service: "reload",
        label: "Preview on my phone",
        reason: "Turn the shared-app moment into your own app loop.",
      } : null,
      !hasAgentBox ? {
        service: "agentBox",
        label: "My own coding runtime",
        reason: "Stop borrowing the developer's machine when you are ready to build independently.",
      } : null,
      !hasPublish ? {
        service: "publish",
        label: "Publish my app",
        reason: "Convert a working phone preview into a store release.",
      } : null,
    ].filter(Boolean);

    return {
      sources,
      hasGuestOrigin: sources.length > 0,
      enabledServices,
      recommendedServices,
    };
  },
});

/**
 * Host-facing conversion summary. This is deliberately aggregate and
 * non-financial: it tells a developer which invited normies activated
 * their own Yaver capabilities, without exposing the guest's wallet,
 * prompts, task output, project paths, or spend.
 */
export const getHostConversionSummary = query({
  args: {
    tokenHash: v.string(),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) return { guests: [], totals: { invited: 0, serviceEnabled: 0, paidUsage: 0 } };

    const rows = await ctx.db
      .query("guestConversions")
      .withIndex("by_host", (q) => q.eq("hostUserId", session.user._id))
      .collect();

    const guests = [];
    for (const row of rows.sort((a, b) => b.updatedAt - a.updatedAt)) {
      const guest = await ctx.db.get(row.guestUserId);
      if (!guest) continue;
      guests.push({
        guestUserId: guest.userId,
        guestEmail: guest.email,
        guestName: guest.fullName,
        sourceScope: row.sourceScope ?? "full",
        sourceProjects: row.sourceProjects ?? [],
        firstAcceptedAt: row.firstAcceptedAt,
        lastGuestActivityAt: row.lastGuestActivityAt,
        guestActivityCount: row.guestActivityCount ?? 0,
        conversionState: row.conversionState,
        firstManagedServiceAt: row.firstManagedServiceAt,
        firstManagedService: row.firstManagedService,
        enabledServices: row.enabledServices ?? [],
        convertedAt: row.convertedAt,
      });
    }

    return {
      guests,
      totals: {
        invited: guests.length,
        serviceEnabled: guests.filter((g) => g.conversionState === "service-enabled" || g.conversionState === "paid-usage").length,
        paidUsage: guests.filter((g) => g.conversionState === "paid-usage").length,
      },
    };
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
