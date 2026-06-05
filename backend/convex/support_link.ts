// support_link.ts — Yaver Support Links (docs/mesh-support-link.md).
//
// A supporter mints a shareable link (yaver.io/j/<code>). A friend opens it,
// installs Yaver, signs in (1-tap OAuth), and on an explicit CONSENT screen
// chooses what to allow. Redemption creates a REVERSE infra grant
// (host = friend, guest = supporter) so the friend's device joins the
// supporter's mesh and the supporter can ssh/exec/code into it from their own
// (AI-wrapped) CLI. The link only OFFERS scope; the friend's consent decides
// the actual grant. Least-privilege by default (view + files; terminal and
// desktop control are opt-in).
//
// Two entry styles, mirroring mesh.ts: agent calls use ctx.auth via resolveUser
// (CLI → /api/mutation); web/console calls use a session-token hash.

import { mutation, query } from "./_generated/server";
import { v } from "convex/values";
import type { Id } from "./_generated/dataModel";
import { resolveUser } from "./agentSync";
import { validateSessionInternal } from "./auth";

async function userFromToken(ctx: any, tokenHash: string): Promise<Id<"users">> {
  const session = await validateSessionInternal(ctx, tokenHash);
  if (!session) throw new Error("Unauthorized");
  return session.user._id;
}

function genCode(): string {
  const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"; // no 0/O/1/I
  const buf = new Uint8Array(8);
  crypto.getRandomValues(buf);
  return Array.from(buf).map((b) => chars[b % chars.length]).join("");
}

const INVITE_TTL_MS = 24 * 60 * 60 * 1000; // link redeem window: 24h

type CreateArgs = {
  offerTerminal?: boolean;
  offerDesktopControl?: boolean;
  defaultTtlHours?: number;
  label?: string;
  singleUse?: boolean;
};

async function createInviteForUser(ctx: any, inviterUserId: Id<"users">, args: CreateArgs) {
  let code = genCode();
  for (let i = 0; i < 5; i++) {
    const clash = await ctx.db
      .query("supportInvites")
      .withIndex("by_code", (q: any) => q.eq("code", code))
      .first();
    if (!clash) break;
    code = genCode();
  }
  const now = Date.now();
  await ctx.db.insert("supportInvites", {
    inviterUserId,
    code,
    status: "pending",
    singleUse: args.singleUse ?? true,
    offerTerminal: args.offerTerminal ?? false,
    offerDesktopControl: args.offerDesktopControl ?? false,
    defaultTtlHours: args.defaultTtlHours ?? 24,
    label: args.label,
    createdAt: now,
    expiresAt: now + INVITE_TTL_MS,
  });
  return { code };
}

type RedeemArgs = {
  code: string;
  deviceId: string;
  allowTerminal?: boolean;
  allowDesktopControl?: boolean;
  persistent?: boolean;
};

async function redeemForUser(ctx: any, friendUserId: Id<"users">, args: RedeemArgs) {
  const invite = await ctx.db
    .query("supportInvites")
    .withIndex("by_code", (q: any) => q.eq("code", args.code))
    .first();
  if (!invite) throw new Error("invite not found");
  if (invite.status !== "pending") throw new Error(`invite ${invite.status}`);
  if (invite.expiresAt < Date.now()) {
    await ctx.db.patch(invite._id, { status: "expired" });
    throw new Error("invite expired");
  }
  if (invite.inviterUserId === friendUserId) {
    throw new Error("cannot redeem your own support link");
  }

  // Friend's consent, clamped to what the link offers.
  const allowTerminal = !!args.allowTerminal && invite.offerTerminal;
  const allowDesktopControl = !!args.allowDesktopControl && invite.offerDesktopControl;
  const scope = allowTerminal ? "full" : "support";
  const now = Date.now();
  const expiresAt = args.persistent ? undefined : now + invite.defaultTtlHours * 60 * 60 * 1000;

  // Reverse infra grant: friend is the host, supporter is the guest.
  const grantId = await ctx.db.insert("infraAccessGrants", {
    hostUserId: friendUserId,
    guestUserId: invite.inviterUserId,
    status: "active",
    shareAllDevices: false,
    allowDesktopControl,
    allowTunnelForward: true, // needed for ssh/mesh routing to the device
    allowGuestProvidedApiKeys: true, // supporter brings their own AI plan/keys
    useHostApiKeys: false, // never hand the supporter the friend's keys
    grantedAt: now,
    updatedAt: now,
    expiresAt,
    origin: "support-link",
  });
  await ctx.db.insert("infraAccessGrantDevices", {
    grantId,
    hostUserId: friendUserId,
    guestUserId: invite.inviterUserId,
    deviceId: args.deviceId,
    createdAt: now,
  });
  // guestAccess row carries the SCOPE the agent enforces for this supporter.
  const existingGA = await ctx.db
    .query("guestAccess")
    .withIndex("by_host_guest", (q: any) =>
      q.eq("hostUserId", friendUserId).eq("guestUserId", invite.inviterUserId),
    )
    .first();
  if (existingGA) {
    await ctx.db.patch(existingGA._id, { scope, revokedAt: undefined, grantedAt: now });
  } else {
    await ctx.db.insert("guestAccess", {
      hostUserId: friendUserId,
      guestUserId: invite.inviterUserId,
      grantedAt: now,
      scope,
    });
  }

  if (invite.singleUse) {
    await ctx.db.patch(invite._id, {
      status: "redeemed",
      redeemedByUserId: friendUserId,
      redeemedDeviceId: args.deviceId,
      redeemedAt: now,
      grantId,
    });
  }

  const inviter = await ctx.db.get(invite.inviterUserId);
  return {
    ok: true,
    scope,
    allowDesktopControl,
    expiresAt: expiresAt ?? null,
    inviterName: inviter ? (inviter as any).fullName || (inviter as any).email : "your supporter",
  };
}

async function revokeGrantForUser(ctx: any, userId: Id<"users">, grantId: Id<"infraAccessGrants">) {
  const grant = await ctx.db.get(grantId);
  if (!grant) return { ok: true };
  if (grant.hostUserId !== userId && grant.guestUserId !== userId) {
    throw new Error("Forbidden");
  }
  await ctx.db.patch(grantId, { status: "revoked", revokedAt: Date.now() });
  const ga = await ctx.db
    .query("guestAccess")
    .withIndex("by_host_guest", (q: any) =>
      q.eq("hostUserId", grant.hostUserId).eq("guestUserId", grant.guestUserId),
    )
    .first();
  if (ga && !ga.revokedAt) await ctx.db.patch(ga._id, { revokedAt: Date.now() });
  return { ok: true };
}

async function denyAllForUser(ctx: any, userId: Id<"users">) {
  const now = Date.now();
  const grants = await ctx.db
    .query("infraAccessGrants")
    .withIndex("by_hostUserId", (q: any) => q.eq("hostUserId", userId))
    .filter((q: any) => q.eq(q.field("status"), "active"))
    .collect();
  for (const g of grants) await ctx.db.patch(g._id, { status: "revoked", revokedAt: now });
  const gas = await ctx.db
    .query("guestAccess")
    .withIndex("by_hostUserId", (q: any) => q.eq("hostUserId", userId))
    .filter((q: any) => q.eq(q.field("revokedAt"), undefined))
    .collect();
  for (const ga of gas) await ctx.db.patch(ga._id, { revokedAt: now });
  return { ok: true, revoked: grants.length };
}

async function connectionsForUser(ctx: any, userId: Id<"users">) {
  const now = Date.now();
  const active = (g: any) => g.status === "active" && (!g.expiresAt || g.expiresAt > now);
  const asSupporter = (
    await ctx.db
      .query("infraAccessGrants")
      .withIndex("by_guestUserId", (q: any) => q.eq("guestUserId", userId))
      .collect()
  ).filter((g: any) => active(g) && g.origin === "support-link");
  const asFriend = (
    await ctx.db
      .query("infraAccessGrants")
      .withIndex("by_hostUserId", (q: any) => q.eq("hostUserId", userId))
      .collect()
  ).filter((g: any) => active(g) && g.origin === "support-link");

  const shape = async (g: any, counterpartId: Id<"users">) => {
    const u = await ctx.db.get(counterpartId);
    const devRow = await ctx.db
      .query("infraAccessGrantDevices")
      .withIndex("by_grant", (q: any) => q.eq("grantId", g._id))
      .first();
    return {
      grantId: g._id,
      deviceId: devRow?.deviceId ?? null,
      counterpartName: u ? (u as any).fullName || (u as any).email : "unknown",
      counterpartEmail: u ? (u as any).email : undefined,
      allowDesktopControl: g.allowDesktopControl === true,
      expiresAt: g.expiresAt ?? null,
      grantedAt: g.grantedAt,
    };
  };
  return {
    supporting: await Promise.all(asSupporter.map((g: any) => shape(g, g.hostUserId))),
    supportedBy: await Promise.all(asFriend.map((g: any) => shape(g, g.guestUserId))),
  };
}

// --- Agent entry points (ctx.auth via resolveUser) ---

export const createSupportInvite = mutation({
  args: {
    offerTerminal: v.optional(v.boolean()),
    offerDesktopControl: v.optional(v.boolean()),
    defaultTtlHours: v.optional(v.number()),
    label: v.optional(v.string()),
    singleUse: v.optional(v.boolean()),
  },
  handler: async (ctx, args) => createInviteForUser(ctx, await resolveUser(ctx), args),
});

export const redeemSupportInvite = mutation({
  args: {
    code: v.string(),
    deviceId: v.string(),
    allowTerminal: v.optional(v.boolean()),
    allowDesktopControl: v.optional(v.boolean()),
    persistent: v.optional(v.boolean()),
  },
  handler: async (ctx, args) => redeemForUser(ctx, await resolveUser(ctx), args),
});

export const revokeSupportGrant = mutation({
  args: { grantId: v.id("infraAccessGrants") },
  handler: async (ctx, { grantId }) => revokeGrantForUser(ctx, await resolveUser(ctx), grantId),
});

export const denyAllSupport = mutation({
  args: {},
  handler: async (ctx) => denyAllForUser(ctx, await resolveUser(ctx)),
});

export const listSupportConnections = query({
  args: {},
  handler: async (ctx) => connectionsForUser(ctx, await resolveUser(ctx)),
});

// --- Public: landing/consent page reads invite metadata before sign-in ---

export const getSupportInviteInfo = query({
  args: { code: v.string() },
  handler: async (ctx, { code }) => {
    const invite = await ctx.db
      .query("supportInvites")
      .withIndex("by_code", (q) => q.eq("code", code))
      .first();
    if (!invite) return { valid: false as const };
    const expired = invite.expiresAt < Date.now();
    const usable = invite.status === "pending" && !expired;
    const inviter = await ctx.db.get(invite.inviterUserId);
    return {
      valid: usable,
      status: expired && invite.status === "pending" ? "expired" : invite.status,
      offerTerminal: invite.offerTerminal,
      offerDesktopControl: invite.offerDesktopControl,
      defaultTtlHours: invite.defaultTtlHours,
      label: invite.label,
      inviter: inviter
        ? {
            name: (inviter as any).fullName || (inviter as any).email || "A Yaver user",
            email: (inviter as any).email,
            avatarUrl: (inviter as any).avatarUrl,
          }
        : null,
    };
  },
});

// --- Web/console entry points (session-token hash) ---

export const createSupportInviteWeb = mutation({
  args: {
    tokenHash: v.string(),
    offerTerminal: v.optional(v.boolean()),
    offerDesktopControl: v.optional(v.boolean()),
    defaultTtlHours: v.optional(v.number()),
    label: v.optional(v.string()),
    singleUse: v.optional(v.boolean()),
  },
  handler: async (ctx, { tokenHash, ...args }) =>
    createInviteForUser(ctx, await userFromToken(ctx, tokenHash), args),
});

export const redeemSupportInviteWeb = mutation({
  args: {
    tokenHash: v.string(),
    code: v.string(),
    deviceId: v.string(),
    allowTerminal: v.optional(v.boolean()),
    allowDesktopControl: v.optional(v.boolean()),
    persistent: v.optional(v.boolean()),
  },
  handler: async (ctx, { tokenHash, ...args }) =>
    redeemForUser(ctx, await userFromToken(ctx, tokenHash), args),
});

export const revokeSupportGrantWeb = mutation({
  args: { tokenHash: v.string(), grantId: v.id("infraAccessGrants") },
  handler: async (ctx, { tokenHash, grantId }) =>
    revokeGrantForUser(ctx, await userFromToken(ctx, tokenHash), grantId),
});

export const denyAllSupportWeb = mutation({
  args: { tokenHash: v.string() },
  handler: async (ctx, { tokenHash }) => denyAllForUser(ctx, await userFromToken(ctx, tokenHash)),
});

export const listSupportConnectionsWeb = query({
  args: { tokenHash: v.string() },
  handler: async (ctx, { tokenHash }) => connectionsForUser(ctx, await userFromToken(ctx, tokenHash)),
});
