import { v } from "convex/values";
import { mutation, query, internalQuery, QueryCtx, MutationCtx } from "./_generated/server";
import { Id } from "./_generated/dataModel";
import { internal } from "./_generated/api";
import { deleteInfraGrantArtifactsForUser } from "./access";
import {
  welcomeHtml,
  providerLinkedHtml,
  providerUnlinkedHtml,
  accountsMergedHtml,
  mergeStartedHtml,
  verifyEmailHtml,
} from "./email";
import { base32Decode, verifyTOTP } from "./totp";

type OAuthProvider =
  | "google"
  | "microsoft"
  | "apple"
  | "github"
  | "gitlab"
  | "email"
  | "passkey";

// ── Helpers ──────────────────────────────────────────────────────────

/** SHA-256 hex digest of a string. Works in Convex runtime (Web Crypto). */
export async function sha256Hex(input: string): Promise<string> {
  const encoder = new TextEncoder();
  const data = encoder.encode(input);
  const hashBuffer = await crypto.subtle.digest("SHA-256", data);
  const hashArray = Array.from(new Uint8Array(hashBuffer));
  return hashArray.map((b) => b.toString(16).padStart(2, "0")).join("");
}

/** Generate a random hex string of `bytes` length (default 32 = 256 bits). */
export function randomHex(bytes: number = 32): string {
  const buf = new Uint8Array(bytes);
  crypto.getRandomValues(buf);
  return Array.from(buf)
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

async function ensureAuthIdentity(
  ctx: MutationCtx,
  userId: Id<"users">,
  provider: OAuthProvider,
  providerId: string,
  email?: string,
) {
  const existing = await ctx.db
    .query("authIdentities")
    .withIndex("by_provider", (q) => q.eq("provider", provider).eq("providerId", providerId))
    .unique();
  if (existing) {
    if (existing.userId !== userId) {
      throw new Error("IDENTITY_ALREADY_LINKED");
    }
    await ctx.db.patch(existing._id, {
      email: email || existing.email,
      lastUsedAt: Date.now(),
    });
    return existing._id;
  }
  return await ctx.db.insert("authIdentities", {
    userId,
    provider,
    providerId,
    email,
    createdAt: Date.now(),
    lastUsedAt: Date.now(),
  });
}

async function ensureUserSettings(ctx: MutationCtx, userId: Id<"users">) {
  const settings = await ctx.db
    .query("userSettings")
    .withIndex("by_userId", (q) => q.eq("userId", userId))
    .first();
  if (!settings) {
    const defaultRelay = await getDefaultRelay(ctx);
    await ctx.db.insert("userSettings", { userId, forceRelay: false, ...defaultRelay });
    return;
  }
  if (!settings.relayPassword) {
    const defaultRelay = await getDefaultRelay(ctx);
    if (defaultRelay.relayPassword) {
      await ctx.db.patch(settings._id, defaultRelay);
    }
  }
}

async function findUserForOAuth(
  ctx: QueryCtx | MutationCtx,
  provider: OAuthProvider,
  providerId: string,
  _email: string,
) {
  // Resolution is strictly by linking, never by email. The email
  // fallback that used to live here auto-merged accounts whose
  // emails happened to match across providers — surprising semantics
  // when a user has different addresses per identity (Apple iCloud
  // vs Gmail vs work Microsoft) and a real risk if a verified email
  // collided across distinct humans. authIdentities is the source of
  // truth; users.by_provider is the legacy compatibility lookup for
  // accounts created before the authIdentities table existed.
  const identity = await ctx.db
    .query("authIdentities")
    .withIndex("by_provider", (q) => q.eq("provider", provider).eq("providerId", providerId))
    .unique();
  if (identity) {
    const linked = await ctx.db.get(identity.userId);
    if (linked) return linked;
  }

  const byProvider = await ctx.db
    .query("users")
    .withIndex("by_provider", (q) => q.eq("provider", provider).eq("providerId", providerId))
    .unique();
  return byProvider ?? null;
}

// ── Shared helpers for destructive auth flows ──────────────────────
// These live here so `unlinkAuthIdentity`, `createAccountMergeIntent`,
// and `completeAccountMerge` share one implementation for the common
// bits: TOTP re-verification, security event writing, notification
// email scheduling. Every destructive action goes through all three,
// so users can't be silently attacked via a stolen session.

/**
 * If the user has 2FA enabled, require a valid TOTP code or recovery
 * code on destructive auth actions. Throws with TOTP_REQUIRED when the
 * caller didn't supply a code, or INVALID_TOTP when the code is wrong.
 * Consumes the recovery code if one matched.
 */
async function requireFreshTotp(
  ctx: MutationCtx,
  user: {
    _id: Id<"users">;
    totpEnabled?: boolean;
    totpSecret?: string;
    totpRecoveryCodes?: string;
  },
  providedCode: string | undefined,
) {
  if (!user.totpEnabled) return;
  const code = (providedCode ?? "").trim();
  if (!code) throw new Error("TOTP_REQUIRED");
  if (!user.totpSecret) return; // misconfigured — fail open rather than lock the user out

  const secretBytes = base32Decode(user.totpSecret);
  const ok = await verifyTOTP(secretBytes, code);
  if (ok) return;

  if (user.totpRecoveryCodes) {
    const codeHash = await sha256Hex(code);
    const hashes: string[] = JSON.parse(user.totpRecoveryCodes);
    const idx = hashes.indexOf(codeHash);
    if (idx !== -1) {
      hashes.splice(idx, 1);
      await ctx.db.patch(user._id, {
        totpRecoveryCodes: JSON.stringify(hashes),
      });
      return;
    }
  }
  throw new Error("INVALID_TOTP");
}

async function recordAuthSecurityEvent(
  ctx: MutationCtx,
  userId: Id<"users">,
  eventType:
    | "link_added"
    | "link_removed"
    | "merge_started"
    | "merge_completed"
    | "merge_cancelled"
    | "merge_received",
  detailObj: Record<string, unknown>,
) {
  await ctx.db.insert("securityEvents", {
    userId,
    eventType,
    details: JSON.stringify(detailObj),
    read: false,
    createdAt: Date.now(),
  });
}

function scheduleSecurityEmail(
  ctx: MutationCtx,
  to: string,
  subject: string,
  html: string,
) {
  if (!to) return;
  void ctx.scheduler.runAfter(0, internal.email.send, {
    from: "Yaver Security <kivanc@yaver.io>",
    to,
    subject,
    html,
    replyTo: "kivanc@yaver.io",
  });
}

async function mergeUserInto(
  ctx: MutationCtx,
  sourceUserId: Id<"users">,
  targetUserId: Id<"users">,
) {
  if (sourceUserId === targetUserId) return;

  const sourceUser = await ctx.db.get(sourceUserId);
  const targetUser = await ctx.db.get(targetUserId);
  if (!sourceUser || !targetUser) return;

  const sourceSettings = await ctx.db
    .query("userSettings")
    .withIndex("by_userId", (q) => q.eq("userId", sourceUserId))
    .first();
  const targetSettings = await ctx.db
    .query("userSettings")
    .withIndex("by_userId", (q) => q.eq("userId", targetUserId))
    .first();
  if (sourceSettings && !targetSettings) {
    await ctx.db.insert("userSettings", {
      userId: targetUserId,
      forceRelay: sourceSettings.forceRelay,
      runnerId: sourceSettings.runnerId,
      customRunnerCommand: sourceSettings.customRunnerCommand,
      relayUrl: sourceSettings.relayUrl,
      relayPassword: sourceSettings.relayPassword,
      tunnelUrl: sourceSettings.tunnelUrl,
      speechProvider: sourceSettings.speechProvider,
      speechApiKey: sourceSettings.speechApiKey,
      ttsEnabled: sourceSettings.ttsEnabled,
      ttsProvider: sourceSettings.ttsProvider,
      verbosity: sourceSettings.verbosity,
      keyStorage: sourceSettings.keyStorage,
    });
    await ctx.db.delete(sourceSettings._id);
  } else if (sourceSettings && targetSettings) {
    await ctx.db.patch(targetSettings._id, {
      forceRelay: targetSettings.forceRelay ?? sourceSettings.forceRelay,
      runnerId: targetSettings.runnerId ?? sourceSettings.runnerId,
      customRunnerCommand: targetSettings.customRunnerCommand ?? sourceSettings.customRunnerCommand,
      relayUrl: targetSettings.relayUrl ?? sourceSettings.relayUrl,
      relayPassword: targetSettings.relayPassword ?? sourceSettings.relayPassword,
      tunnelUrl: targetSettings.tunnelUrl ?? sourceSettings.tunnelUrl,
      speechProvider: targetSettings.speechProvider ?? sourceSettings.speechProvider,
      speechApiKey: targetSettings.speechApiKey ?? sourceSettings.speechApiKey,
      ttsEnabled: targetSettings.ttsEnabled ?? sourceSettings.ttsEnabled,
      ttsProvider: targetSettings.ttsProvider ?? sourceSettings.ttsProvider,
      verbosity: targetSettings.verbosity ?? sourceSettings.verbosity,
      keyStorage: targetSettings.keyStorage ?? sourceSettings.keyStorage,
    });
    await ctx.db.delete(sourceSettings._id);
  }

  const sessions = await ctx.db
    .query("sessions")
    .withIndex("by_userId", (q) => q.eq("userId", sourceUserId))
    .collect();
  for (const session of sessions) {
    await ctx.db.delete(session._id);
  }

  const devices = await ctx.db
    .query("devices")
    .withIndex("by_userId", (q) => q.eq("userId", sourceUserId))
    .collect();
  for (const device of devices) {
    const existingTargetDevice = await ctx.db
      .query("devices")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", device.deviceId))
      .unique();
    if (existingTargetDevice && existingTargetDevice._id !== device._id && existingTargetDevice.userId === targetUserId) {
      await ctx.db.patch(existingTargetDevice._id, {
        name: device.name || existingTargetDevice.name,
        platform: device.platform,
        publicKey: device.publicKey || existingTargetDevice.publicKey,
        quicHost: device.quicHost || existingTargetDevice.quicHost,
        quicPort: device.quicPort || existingTargetDevice.quicPort,
        isOnline: device.isOnline,
        runnerDown: device.runnerDown ?? existingTargetDevice.runnerDown,
        runners: device.runners ?? existingTargetDevice.runners,
        installedRunnerIds: device.installedRunnerIds ?? existingTargetDevice.installedRunnerIds,
        lastHeartbeat: Math.max(device.lastHeartbeat, existingTargetDevice.lastHeartbeat),
        hardwareId: device.hardwareId || existingTargetDevice.hardwareId,
      });
      await ctx.db.delete(device._id);
    } else {
      await ctx.db.patch(device._id, { userId: targetUserId });
    }
  }

  const identities = await ctx.db
    .query("authIdentities")
    .withIndex("by_userId", (q) => q.eq("userId", sourceUserId))
    .collect();
  for (const identity of identities) {
    const existing = await ctx.db
      .query("authIdentities")
      .withIndex("by_provider", (q) => q.eq("provider", identity.provider).eq("providerId", identity.providerId))
      .unique();
    if (existing && existing._id !== identity._id && existing.userId === targetUserId) {
      await ctx.db.delete(identity._id);
      continue;
    }
    await ctx.db.patch(identity._id, {
      userId: targetUserId,
      email: identity.email,
      lastUsedAt: Date.now(),
    });
  }

  const surveys = await ctx.db
    .query("developerSurveys")
    .withIndex("by_userId", (q) => q.eq("userId", sourceUserId))
    .collect();
  for (const survey of surveys) {
    await ctx.db.patch(survey._id, { userId: targetUserId });
  }

  // ---- Generic reassignments for every other user-keyed table -------
  //
  // Each entry names a table and the userId-like column. We iterate in
  // batches of source-owned rows and just patch the column to point at
  // target. For tables with multiple user columns (guest/host), we run
  // the helper once per column.
  //
  // This has to stay in sync with the schema: when a new user-keyed
  // table is added, add it here too or its rows will be orphaned on
  // every future merge. The unit tests in auth_linking_test.ts guard
  // against regressions by seeding a row in every listed table and
  // asserting ownership flips.

  const singleOwnerTables: Array<{
    table: any;
    index: string;
    field: string;
    sourceValue: Id<"users"> | string;
    targetValue: Id<"users"> | string;
  }> = [
    { table: "passkeys", index: "by_userId", field: "userId", sourceValue: sourceUserId, targetValue: targetUserId },
    { table: "subscriptions", index: "by_user", field: "userId", sourceValue: sourceUserId, targetValue: targetUserId },
    { table: "managedRelays", index: "by_user", field: "userId", sourceValue: sourceUserId, targetValue: targetUserId },
    { table: "cloudMachines", index: "by_user", field: "userId", sourceValue: sourceUserId, targetValue: targetUserId },
    { table: "sdkTokens", index: "by_userId", field: "userId", sourceValue: sourceUserId, targetValue: targetUserId },
    { table: "securityEvents", index: "by_userId", field: "userId", sourceValue: sourceUserId, targetValue: targetUserId },
    { table: "userProjects", index: "by_user", field: "userId", sourceValue: sourceUserId, targetValue: targetUserId },
    { table: "userServices", index: "by_user", field: "userId", sourceValue: sourceUserId, targetValue: targetUserId },
    { table: "userDeployments", index: "by_user", field: "userId", sourceValue: sourceUserId, targetValue: targetUserId },
    { table: "userActivity", index: "by_user", field: "userId", sourceValue: sourceUserId, targetValue: targetUserId },
    { table: "runnerUsage", index: "by_userId", field: "userId", sourceValue: sourceUser.userId, targetValue: targetUser.userId },
    { table: "dailyTaskCounts", index: "by_userId_date", field: "userId", sourceValue: sourceUser.userId, targetValue: targetUser.userId },
  ];
  for (const { table, index, field, sourceValue, targetValue } of singleOwnerTables) {
    const rows = await ctx.db
      .query(table as any)
      .withIndex(index as any, (q: any) => q.eq(field, sourceValue))
      .collect();
    for (const row of rows as any[]) {
      await ctx.db.patch(row._id, { [field]: targetValue } as any);
    }
  }

  // Teams: move ownership, then collapse team memberships so target
  // doesn't end up with two rows for the same team.
  const ownedTeams = await ctx.db
    .query("teams")
    .withIndex("by_owner", (q) => q.eq("ownerId", sourceUserId))
    .collect();
  for (const team of ownedTeams) {
    await ctx.db.patch(team._id, { ownerId: targetUserId });
  }
  const sourceMemberships = await ctx.db
    .query("teamMembers")
    .withIndex("by_user", (q) => q.eq("userId", sourceUserId))
    .collect();
  for (const member of sourceMemberships) {
    const existing = await ctx.db
      .query("teamMembers")
      .withIndex("by_team_user", (q) => q.eq("teamId", member.teamId).eq("userId", targetUserId))
      .unique();
    if (existing) {
      // target already belongs — drop the source row; keep the stronger role
      const rankedRole = member.role === "admin" ? "admin" : existing.role;
      if (existing.role !== rankedRole) {
        await ctx.db.patch(existing._id, { role: rankedRole });
      }
      await ctx.db.delete(member._id);
    } else {
      await ctx.db.patch(member._id, { userId: targetUserId });
    }
  }
  // Reassign invitedBy references so audit history stays intact.
  const invitedByRows = await ctx.db
    .query("teamMembers")
    .collect();
  for (const row of invitedByRows) {
    if (row.invitedBy === sourceUserId) {
      await ctx.db.patch(row._id, { invitedBy: targetUserId });
    }
  }

  // Guest tables — both hostUserId and guestUserId can match source.
  const guestDualTables: Array<{ table: any; hostIndex: string; guestIndex: string }> = [
    { table: "guestInvitations", hostIndex: "by_hostUserId", guestIndex: "" },
    { table: "guestAccess", hostIndex: "by_hostUserId", guestIndex: "by_guestUserId" },
    { table: "guestUsage", hostIndex: "by_host_guest_date", guestIndex: "" },
    { table: "infraAccessGrants", hostIndex: "by_hostUserId", guestIndex: "by_guestUserId" },
    { table: "infraAccessGrantDevices", hostIndex: "by_hostUserId", guestIndex: "by_guestUserId" },
    { table: "infraAccessGrantMachines", hostIndex: "by_hostUserId", guestIndex: "by_guestUserId" },
    { table: "hostShareInvites", hostIndex: "by_hostUserId", guestIndex: "by_guestUserId" },
    { table: "hostShareSessions", hostIndex: "by_host_status", guestIndex: "by_guest_status" },
  ];
  for (const { table, hostIndex, guestIndex } of guestDualTables) {
    if (hostIndex) {
      const rows = await ctx.db
        .query(table as any)
        .withIndex(hostIndex as any, (q: any) => q.eq("hostUserId", sourceUserId))
        .collect();
      for (const row of rows as any[]) {
        await ctx.db.patch(row._id, { hostUserId: targetUserId } as any);
      }
    }
    if (guestIndex) {
      const rows = await ctx.db
        .query(table as any)
        .withIndex(guestIndex as any, (q: any) => q.eq("guestUserId", sourceUserId))
        .collect();
      for (const row of rows as any[]) {
        await ctx.db.patch(row._id, { guestUserId: targetUserId } as any);
      }
    }
  }

  // guestInvitations also stores guestUserId optionally.
  const invitedAsGuest = await ctx.db
    .query("guestInvitations")
    .collect();
  for (const row of invitedAsGuest) {
    if (row.guestUserId === sourceUserId) {
      await ctx.db.patch(row._id, { guestUserId: targetUserId });
    }
  }

  // Ephemeral tables — clear source-owned rows rather than reassign.
  // These are all short-lived state: an orphan password reset or pending
  // auth row buys nothing, and reassigning invite/merge intents would
  // be actively surprising (the user didn't consent to those on target).
  // deviceCodes has no userId column — they're looked up by opaque code
  // and expire on their own, so we leave them alone.
  const ephemeralTables: Array<{ table: any; index?: string }> = [
    { table: "passwordResets" },
    { table: "pendingAuth" },
    { table: "oauthLinkIntents", index: "by_userId" },
    { table: "passkeyChallenges" },
    { table: "emailVerifications", index: "by_userId" },
  ];
  for (const { table, index } of ephemeralTables) {
    const rows = index
      ? await ctx.db
          .query(table as any)
          .withIndex(index as any, (q: any) => q.eq("userId", sourceUserId))
          .collect()
      : await ctx.db
          .query(table as any)
          .collect();
    for (const row of rows as any[]) {
      if (row.userId === sourceUserId) {
        await ctx.db.delete(row._id);
      }
    }
  }

  // Source-owned merge intents are no longer meaningful.
  const srcMergeIntents = await ctx.db
    .query("accountMergeIntents")
    .withIndex("by_targetUserId", (q) => q.eq("targetUserId", sourceUserId))
    .collect();
  for (const intent of srcMergeIntents) {
    await ctx.db.delete(intent._id);
  }

  await ctx.db.patch(targetUserId, {
    email: targetUser.email || sourceUser.email,
    fullName: targetUser.fullName || sourceUser.fullName,
    avatarUrl: targetUser.avatarUrl || sourceUser.avatarUrl,
    surveyCompleted: targetUser.surveyCompleted ?? sourceUser.surveyCompleted,
  });

  await ctx.db.delete(sourceUserId);
}

/**
 * Fetch the first platform relay server and generate a unique per-user relay password.
 * Each user gets their own random password — the relay validates it via Convex backend.
 * Returns { relayUrl, relayPassword } or {} if no relay configured.
 */
async function getDefaultRelay(ctx: MutationCtx): Promise<{ relayUrl?: string; relayPassword?: string }> {
  const config = await ctx.db
    .query("platformConfig")
    .withIndex("by_key", (q) => q.eq("key", "relay_servers"))
    .unique();
  if (!config?.value) return {};
  try {
    const relays = JSON.parse(config.value);
    if (!Array.isArray(relays) || relays.length === 0) return {};
    const first = relays[0];
    return {
      relayUrl: first.httpUrl || undefined,
      relayPassword: first.password || undefined, // shared relay password from platform config
    };
  } catch {
    return {};
  }
}

/** Validate a session token hash and return the associated user, or null. */
export async function validateSessionInternal(
  ctx: QueryCtx,
  tokenHash: string
): Promise<{
  user: {
    _id: Id<"users">;
    userId: string;
    email: string;
    fullName: string;
    provider: "google" | "microsoft" | "apple" | "github" | "gitlab" | "email" | "passkey";
    providerId: string;
    passwordHash?: string;
    avatarUrl?: string;
    surveyCompleted?: boolean;
    totpSecret?: string;
    totpEnabled?: boolean;
    totpRecoveryCodes?: string;
    emailVerified?: boolean;
    emailVerifiedAt?: number;
    createdAt: number;
  };
  sessionId: Id<"sessions">;
} | null> {
  let session = await ctx.db
    .query("sessions")
    .withIndex("by_tokenHash", (q) => q.eq("tokenHash", tokenHash))
    .unique();

  // Rotation grace: the presented token may be the immediately-previous
  // one of a session that just rotated. Accept it until the grace
  // window lapses so a fire-and-forget rotation can't 401 a concurrent
  // / in-flight request before the new token propagates.
  if (!session) {
    const rotated = await ctx.db
      .query("sessions")
      .withIndex("by_prevTokenHash", (q) => q.eq("prevTokenHash", tokenHash))
      .unique();
    if (
      rotated &&
      rotated.prevTokenValidUntil !== undefined &&
      rotated.prevTokenValidUntil > Date.now()
    ) {
      session = rotated;
    }
  }

  if (!session) return null;
  if (session.expiresAt < Date.now()) return null;

  const user = await ctx.db.get(session.userId);
  if (!user) return null;

  return { user, sessionId: session._id };
}

// ── Mutations ────────────────────────────────────────────────────────

// Verified-email auto-link gate. The set of providers we trust to
// have attested email ownership at sign-in. Email signup is
// excluded — it currently has no proof step. Passkey signup is
// excluded for the same reason (user types the email, no
// challenge). Both graduate to verified once they click the
// verify-email link, which flips users.emailVerified=true.
const PROVIDER_VERIFIES_EMAIL: Record<OAuthProvider, boolean> = {
  google: true,
  microsoft: true,
  apple: true,
  github: true,
  gitlab: true,
  email: false,
  passkey: false,
};

/**
 * Look for an existing user we can safely link this new OAuth identity
 * to, when the strict (provider, providerId) lookup misses. Returns the
 * existing user only when:
 *
 *   1. Email match is exact (lowercased, trimmed, non-empty).
 *   2. There is exactly ONE candidate row (no ambiguity).
 *   3. The candidate's email is verified — either via OAuth-by-construction
 *      (provider ∈ verified set) OR the user already clicked their
 *      verify-email link (users.emailVerified=true).
 *   4. The new incoming identity is itself an OAuth provider that
 *      attests email (otherwise we'd be linking an unverified passkey
 *      / email-signup to an existing OAuth account, the dangerous
 *      direction — covered separately by the verify-email-then-link
 *      flow).
 *
 * Returning a candidate here ≠ silent merge: the caller still records a
 * `link_added` security event and sends a notification email so the user
 * can see what happened.
 */
async function findExistingUserForAutoLink(
  ctx: QueryCtx | MutationCtx,
  newProvider: OAuthProvider,
  email: string,
): Promise<{ _id: Id<"users">; email: string } | null> {
  if (!PROVIDER_VERIFIES_EMAIL[newProvider]) return null;
  const normalized = email.trim().toLowerCase();
  if (!normalized || !normalized.includes("@")) return null;

  const candidates = await ctx.db
    .query("users")
    .withIndex("by_email", (q) => q.eq("email", normalized))
    .collect();
  if (candidates.length !== 1) return null;

  const candidate = candidates[0];
  const candidateProviderVerifies = PROVIDER_VERIFIES_EMAIL[candidate.provider as OAuthProvider];
  const candidateExplicitlyVerified = candidate.emailVerified === true;
  if (!candidateProviderVerifies && !candidateExplicitlyVerified) {
    return null;
  }
  return candidate;
}

/**
 * Upsert a user by provider + providerId.
 * Returns the user's _id.
 */
export const createOrUpdateUser = mutation({
  args: {
    email: v.string(),
    fullName: v.string(),
    provider: v.union(
      v.literal("google"),
      v.literal("microsoft"),
      v.literal("apple"),
      v.literal("github"),
      v.literal("gitlab"),
      v.literal("email"),
    ),
    providerId: v.string(),
    avatarUrl: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    // Normalize email at the boundary so by_email lookups (used by the
    // auto-link path below + the new /auth/email-providers endpoint) are
    // case-insensitive. Most OAuth providers already return lowercase,
    // but Apple's RFC-2822 relay addresses + the Identity-token path can
    // surface mixed-case in rare cases.
    const email = args.email.trim().toLowerCase();
    const existing = await findUserForOAuth(ctx, args.provider, args.providerId, email);
    if (existing) {
      // Don't overwrite users.email on every sign-in. Each provider has
      // its own address (Apple relay vs Gmail vs work Microsoft), and
      // the per-identity email lives on authIdentities. Patching the
      // user-row email each time made the displayed account email
      // flip between providers depending on which surface signed in
      // most recently — confusing and made Apple Hide-My-Email relays
      // briefly mask the real Gmail. Only seed it the first time a
      // resolved user has no email yet, and let unlinkAuthIdentity
      // re-promote when the primary provider is removed.
      const patch: Record<string, string | undefined | boolean | number> = {
        avatarUrl: args.avatarUrl,
      };
      if (!existing.email && email) {
        patch.email = email;
      }
      if (args.fullName && (!existing.fullName || existing.fullName === existing.email)) {
        patch.fullName = args.fullName;
      }
      // OAuth identities are verified-by-IdP. Promote the user's
      // emailVerified to true the first time they sign in via OAuth
      // even if they originally signed up via email/passkey.
      if (existing.emailVerified !== true && PROVIDER_VERIFIES_EMAIL[args.provider]) {
        patch.emailVerified = true;
        patch.emailVerifiedAt = Date.now();
      }
      await ctx.db.patch(existing._id, patch);
      await ensureUserSettings(ctx, existing._id);
      await ensureAuthIdentity(ctx, existing._id, args.provider, args.providerId, email);
      return existing._id;
    }

    // Auto-link fallback: the strict (provider, providerId) lookup
    // missed, so this is the first time the IdP has seen this
    // user-on-Yaver pair. Before creating a brand-new users row,
    // check whether an existing account already owns this email with
    // a verified status — if so, link the new identity to it rather
    // than create a parallel account. See findExistingUserForAutoLink
    // for the safety gate.
    const linkTarget = await findExistingUserForAutoLink(ctx, args.provider, email);
    if (linkTarget) {
      await ensureAuthIdentity(ctx, linkTarget._id, args.provider, args.providerId, email);
      await ensureUserSettings(ctx, linkTarget._id);
      await recordAuthSecurityEvent(ctx, linkTarget._id, "link_added", {
        provider: args.provider,
        email,
        via: "auto_link_by_verified_email",
      });
      if (linkTarget.email) {
        scheduleSecurityEmail(
          ctx,
          linkTarget.email,
          `Sign-in method added: ${args.provider}`,
          providerLinkedHtml(args.fullName || linkTarget.email, args.provider),
        );
      }
      return linkTarget._id;
    }

    const userId = randomHex(16);
    const userDocId = await ctx.db.insert("users", {
      userId,
      email,
      fullName: args.fullName,
      provider: args.provider,
      providerId: args.providerId,
      avatarUrl: args.avatarUrl,
      // OAuth signup → email is attested by the IdP.
      emailVerified: PROVIDER_VERIFIES_EMAIL[args.provider],
      emailVerifiedAt: PROVIDER_VERIFIES_EMAIL[args.provider] ? Date.now() : undefined,
      createdAt: Date.now(),
    });
    await ensureUserSettings(ctx, userDocId);
    await ensureAuthIdentity(ctx, userDocId, args.provider, args.providerId, email);

    await ctx.scheduler.runAfter(0, internal.email.send, {
      from: "Kivanc from Yaver <kivanc@yaver.io>",
      to: email,
      subject: "Welcome to Yaver",
      html: welcomeHtml(args.fullName || email),
      replyTo: "kivanc@yaver.io",
    });

    return userDocId;
  },
});

export const listAuthIdentities = query({
  args: { tokenHash: v.string() },
  handler: async (ctx, args) => {
    const result = await validateSessionInternal(ctx, args.tokenHash);
    if (!result) return null;
    const identities = await ctx.db
      .query("authIdentities")
      .withIndex("by_userId", (q) => q.eq("userId", result.user._id))
      .collect();
    const mapped = identities.map((identity) => ({
      provider: identity.provider,
      email: identity.email ?? null,
      createdAt: identity.createdAt,
      lastUsedAt: identity.lastUsedAt,
      isPrimary:
        identity.provider === result.user.provider &&
        identity.providerId === result.user.providerId,
    }));
    if (!mapped.some((identity) => identity.isPrimary)) {
      mapped.unshift({
        provider: result.user.provider,
        email: result.user.email,
        createdAt: result.user.createdAt,
        lastUsedAt: result.user.createdAt,
        isPrimary: true,
      });
    }
    return mapped;
  },
});

export const createOAuthLinkIntent = mutation({
  args: {
    tokenHash: v.string(),
    provider: v.union(
      v.literal("google"),
      v.literal("microsoft"),
      v.literal("apple"),
      v.literal("github"),
      v.literal("gitlab"),
    ),
    client: v.optional(v.string()),
    returnTo: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const result = await validateSessionInternal(ctx, args.tokenHash);
    if (!result) throw new Error("Unauthorized");
    const token = randomHex(24);
    await ctx.db.insert("oauthLinkIntents", {
      token,
      userId: result.user._id,
      provider: args.provider,
      client: args.client,
      returnTo: args.returnTo,
      expiresAt: Date.now() + 15 * 60 * 1000,
      createdAt: Date.now(),
    });
    return { token };
  },
});

export const completeOAuthLink = mutation({
  args: {
    linkToken: v.string(),
    provider: v.union(
      v.literal("google"),
      v.literal("microsoft"),
      v.literal("apple"),
      v.literal("github"),
      v.literal("gitlab"),
    ),
    providerId: v.string(),
    email: v.string(),
    fullName: v.string(),
    avatarUrl: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const intent = await ctx.db
      .query("oauthLinkIntents")
      .withIndex("by_token", (q) => q.eq("token", args.linkToken))
      .unique();
    if (!intent || intent.expiresAt < Date.now()) {
      throw new Error("INVALID_LINK_TOKEN");
    }

    const targetUser = await ctx.db.get(intent.userId);
    if (!targetUser) throw new Error("TARGET_USER_NOT_FOUND");

    const source = await findUserForOAuth(ctx, args.provider, args.providerId, args.email);
    if (source && source._id !== targetUser._id) {
      await mergeUserInto(ctx, source._id, targetUser._id);
    }

    await ensureAuthIdentity(ctx, targetUser._id, args.provider, args.providerId, args.email);
    await ensureAuthIdentity(ctx, targetUser._id, targetUser.provider, targetUser.providerId, targetUser.email);
    await ensureUserSettings(ctx, targetUser._id);
    await ctx.db.patch(targetUser._id, {
      email: targetUser.email || args.email,
      fullName: targetUser.fullName || args.fullName,
      avatarUrl: targetUser.avatarUrl || args.avatarUrl,
    });
    await ctx.db.delete(intent._id);

    await recordAuthSecurityEvent(ctx, targetUser._id, "link_added", {
      provider: args.provider,
      email: args.email,
    });

    const notifyTarget = targetUser.email || args.email;
    if (notifyTarget) {
      scheduleSecurityEmail(
        ctx,
        notifyTarget,
        `Sign-in method added: ${args.provider}`,
        providerLinkedHtml(targetUser.fullName || args.fullName || "", args.provider),
      );
    }

    return { ok: true, userId: targetUser._id };
  },
});

/**
 * Unlink an OAuth provider identity from the current account.
 *
 * Refuses when this would leave the user with no way to sign in:
 *   - If the identity doesn't exist, returns ok=false (idempotent-ish).
 *   - If it's the only identity on the account, throws ONLY_IDENTITY.
 *
 * If the unlinked identity was the primary one tracked on `users`, we
 * promote any remaining identity to primary by patching users.{provider,
 * providerId,email} so `findUserForOAuth` keeps working for the fallback
 * users-table lookup.
 */
export const unlinkAuthIdentity = mutation({
  args: {
    tokenHash: v.string(),
    provider: v.union(
      v.literal("google"),
      v.literal("microsoft"),
      v.literal("apple"),
      v.literal("github"),
      v.literal("gitlab"),
      v.literal("email"),
    ),
    totpCode: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const result = await validateSessionInternal(ctx, args.tokenHash);
    if (!result) throw new Error("Unauthorized");

    // Security envelope: TOTP re-verification when 2FA is on — stops a
    // stolen session from silently stripping the user's sign-in methods.
    await requireFreshTotp(ctx, result.user, args.totpCode);

    const identities = await ctx.db
      .query("authIdentities")
      .withIndex("by_userId", (q) => q.eq("userId", result.user._id))
      .collect();

    const target = identities.find((identity) => identity.provider === args.provider);
    if (!target) {
      return { ok: false, reason: "not_linked" as const };
    }
    if (identities.length <= 1) {
      throw new Error("ONLY_IDENTITY");
    }

    const wasPrimary =
      result.user.provider === target.provider &&
      result.user.providerId === target.providerId;

    await ctx.db.delete(target._id);

    // If we just removed the last email-provider identity, clear
    // passwordHash so email+password login is actually disabled. Without
    // this, unlinking "email" is cosmetic — the user can still sign in
    // with /auth/login.
    if (args.provider === "email") {
      const stillHasEmailIdentity = identities.some(
        (identity) => identity._id !== target._id && identity.provider === "email",
      );
      if (!stillHasEmailIdentity && result.user.passwordHash) {
        await ctx.db.patch(result.user._id, { passwordHash: undefined });
      }
    }

    if (wasPrimary) {
      const next = identities.find((identity) => identity._id !== target._id);
      if (next) {
        await ctx.db.patch(result.user._id, {
          provider: next.provider,
          providerId: next.providerId,
          email: next.email || result.user.email,
        });
      }
    }

    await recordAuthSecurityEvent(ctx, result.user._id, "link_removed", {
      provider: args.provider,
      remaining: identities.length - 1,
    });

    if (result.user.email) {
      scheduleSecurityEmail(
        ctx,
        result.user.email,
        `Sign-in method removed: ${args.provider}`,
        providerUnlinkedHtml(result.user.fullName || "", args.provider),
      );
    }

    return { ok: true, remaining: identities.length - 1 };
  },
});

/**
 * Manual account-merge: the currently signed-in user starts an intent. A
 * separate session (the SOURCE account) approves it by calling
 * completeAccountMerge with the same mergeToken. This lets a user who
 * accidentally created two Yaver accounts fold one into the other without
 * going through an OAuth provider collision.
 */
export const createAccountMergeIntent = mutation({
  args: {
    tokenHash: v.string(),
    client: v.optional(v.string()),
    totpCode: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const result = await validateSessionInternal(ctx, args.tokenHash);
    if (!result) throw new Error("Unauthorized");

    await requireFreshTotp(ctx, result.user, args.totpCode);

    // Rate limit: destructive op, so we cap both pending intents and
    // intents-per-hour. A stolen session can't mint unlimited merge
    // URLs that way — and the user sees a security email on the first
    // one they didn't trigger, which is the real defense.
    const existing = await ctx.db
      .query("accountMergeIntents")
      .withIndex("by_targetUserId", (q) => q.eq("targetUserId", result.user._id))
      .collect();
    const pending = existing.filter((i) => i.status === "pending" && i.expiresAt > Date.now());
    if (pending.length >= 3) {
      throw new Error("TOO_MANY_PENDING_MERGES");
    }
    const lastHour = Date.now() - 60 * 60 * 1000;
    const recent = existing.filter((i) => i.createdAt > lastHour);
    if (recent.length >= 10) {
      throw new Error("MERGE_RATE_LIMIT");
    }

    const token = randomHex(24);
    const expiresAt = Date.now() + 30 * 60 * 1000; // 30 minutes
    await ctx.db.insert("accountMergeIntents", {
      token,
      targetUserId: result.user._id,
      targetEmail: result.user.email,
      status: "pending",
      client: args.client,
      expiresAt,
      createdAt: Date.now(),
    });

    await recordAuthSecurityEvent(ctx, result.user._id, "merge_started", {
      expiresAt,
      client: args.client,
    });

    if (result.user.email) {
      scheduleSecurityEmail(
        ctx,
        result.user.email,
        "Yaver account-merge request started",
        mergeStartedHtml(result.user.fullName || "", result.user.email),
      );
    }

    return { mergeToken: token, expiresAt, targetEmail: result.user.email };
  },
});

/**
 * Public status of a merge intent. The approval page calls this with the
 * mergeToken from the URL so it can display the target email before asking
 * for confirmation. No auth required — the mergeToken itself is the secret.
 */
export const getAccountMergeIntentStatus = query({
  args: { token: v.string() },
  handler: async (ctx, args) => {
    const intent = await ctx.db
      .query("accountMergeIntents")
      .withIndex("by_token", (q) => q.eq("token", args.token))
      .unique();
    if (!intent) return { status: "unknown" as const };
    if (intent.expiresAt < Date.now() && intent.status === "pending") {
      return { status: "expired" as const, targetEmail: intent.targetEmail };
    }
    return {
      status: intent.status,
      targetEmail: intent.targetEmail,
      expiresAt: intent.expiresAt,
      completedAt: intent.completedAt,
    };
  },
});

/**
 * Complete an account merge. The caller's session (sourceTokenHash) is the
 * account that will be merged AWAY (deleted). The mergeToken identifies
 * the target account to receive the data. Refuses to merge an account
 * into itself. After success, the source account's session is no longer
 * valid (its user row is deleted).
 */
export const completeAccountMerge = mutation({
  args: {
    mergeToken: v.string(),
    sourceTokenHash: v.string(),
    sourceTotpCode: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const intent = await ctx.db
      .query("accountMergeIntents")
      .withIndex("by_token", (q) => q.eq("token", args.mergeToken))
      .unique();
    if (!intent) throw new Error("INVALID_MERGE_TOKEN");
    if (intent.status !== "pending") throw new Error("MERGE_ALREADY_RESOLVED");
    if (intent.expiresAt < Date.now()) throw new Error("MERGE_TOKEN_EXPIRED");

    const source = await validateSessionInternal(ctx, args.sourceTokenHash);
    if (!source) throw new Error("Unauthorized");

    if (source.user._id === intent.targetUserId) {
      throw new Error("CANNOT_MERGE_SELF");
    }

    // Require 2FA from the SOURCE user — they're the ones consenting to
    // have their account deleted. If source has no 2FA this is a no-op.
    await requireFreshTotp(ctx, source.user, args.sourceTotpCode);

    const target = await ctx.db.get(intent.targetUserId);
    if (!target) throw new Error("TARGET_USER_NOT_FOUND");

    const sourceEmailSnapshot = source.user.email;
    const sourceNameSnapshot = source.user.fullName || "";

    await mergeUserInto(ctx, source.user._id, intent.targetUserId);
    await ctx.db.patch(intent._id, {
      status: "completed",
      completedAt: Date.now(),
    });

    // Audit + email the surviving user. Source user row was deleted
    // inside mergeUserInto so we can't write a security event against
    // it — but the target should know their account just absorbed
    // another one.
    await recordAuthSecurityEvent(ctx, intent.targetUserId, "merge_completed", {
      mergedFromEmail: sourceEmailSnapshot,
    });

    if (target.email) {
      scheduleSecurityEmail(
        ctx,
        target.email,
        "Yaver accounts merged",
        accountsMergedHtml(target.fullName || "", sourceEmailSnapshot, target.email),
      );
    }
    // Courtesy email to the now-deleted source address so the human
    // behind it has a record — the row is gone but the mailbox isn't.
    if (sourceEmailSnapshot) {
      scheduleSecurityEmail(
        ctx,
        sourceEmailSnapshot,
        "Your Yaver account was merged",
        accountsMergedHtml(sourceNameSnapshot, sourceEmailSnapshot, target.email),
      );
    }

    return {
      ok: true,
      mergedFrom: sourceEmailSnapshot,
      mergedInto: target.email,
    };
  },
});

/**
 * Cancel a pending merge intent. Called by the target user if they change
 * their mind. No-op if the intent is already resolved or missing.
 */
export const cancelAccountMergeIntent = mutation({
  args: {
    tokenHash: v.string(),
    mergeToken: v.string(),
  },
  handler: async (ctx, args) => {
    const result = await validateSessionInternal(ctx, args.tokenHash);
    if (!result) throw new Error("Unauthorized");
    const intent = await ctx.db
      .query("accountMergeIntents")
      .withIndex("by_token", (q) => q.eq("token", args.mergeToken))
      .unique();
    if (!intent || intent.targetUserId !== result.user._id) return { ok: false };
    if (intent.status !== "pending") return { ok: false };
    await ctx.db.patch(intent._id, { status: "cancelled" });
    await recordAuthSecurityEvent(ctx, result.user._id, "merge_cancelled", {
      mergeToken: args.mergeToken,
    });
    return { ok: true };
  },
});

/**
 * Create a session for a user. Accepts a pre-hashed token (sha256).
 * Returns the session _id.
 */
export const createSession = mutation({
  args: {
    tokenHash: v.string(),
    userId: v.id("users"),
    deviceId: v.optional(v.string()),
    expiresAt: v.number(),
  },
  handler: async (ctx, args) => {
    return await ctx.db.insert("sessions", {
      tokenHash: args.tokenHash,
      userId: args.userId,
      deviceId: args.deviceId,
      expiresAt: args.expiresAt,
      createdAt: Date.now(),
    });
  },
});

/**
 * Validate a session by tokenHash. Returns the user if valid, null otherwise.
 */
export const validateSession = query({
  args: {
    tokenHash: v.string(),
  },
  handler: async (ctx, args) => {
    const result = await validateSessionInternal(ctx, args.tokenHash);
    if (!result) return null;
    return {
      userDocId: result.user._id,
      userId: result.user.userId,
      email: result.user.email,
      fullName: result.user.fullName,
      provider: result.user.provider,
      avatarUrl: result.user.avatarUrl,
      surveyCompleted: result.user.surveyCompleted ?? false,
      emailVerified: result.user.emailVerified === true,
      emailVerifiedAt: result.user.emailVerifiedAt,
    };
  },
});

/**
 * Refresh a session — extends expiresAt by 1 year and OPTIONALLY
 * rotates the token hash. Returns the new expiresAt (and, when rotated,
 * acknowledges the rotation so the HTTP layer can return the fresh
 * raw token to the agent).
 *
 * Grace window is 1 year — matches the post-refresh lifetime, so a
 * headless Mac Mini that was off for up to a year comes back signed
 * in automatically. Past a year of total silence we refuse: that's
 * "this thing might be lost" territory and a re-auth is appropriate.
 *
 * Token rotation: when the agent generates a fresh raw token and
 * passes its hash as `newTokenHash`, we swap the stored hash in the
 * same mutation. This caps the blast radius of a leaked token to
 * ~24 h (the daily refresh cadence) without any user-visible churn —
 * the agent writes the rotated token back to ~/.yaver/config.json.
 */
export const refreshSession = mutation({
  args: {
    tokenHash: v.string(),
    // Optional: the agent-provided hash of a freshly-minted replacement
    // token. When present, we rotate; when absent, we just extend.
    // Callers on old clients that don't rotate continue to work.
    newTokenHash: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const session = await ctx.db
      .query("sessions")
      .withIndex("by_tokenHash", (q) => q.eq("tokenHash", args.tokenHash))
      .unique();

    if (!session) return null;

    // 1-year grace window. Was 90 days — too aggressive for a Mac
    // Mini in a closet. A year of contact silence is a reasonable
    // "forgotten device" threshold.
    const gracePeriod = 365 * 24 * 60 * 60 * 1000;
    if (session.expiresAt < Date.now() - gracePeriod) return null;

    const newExpiresAt = Date.now() + 365 * 24 * 60 * 60 * 1000;
    // ~2 min grace so the old token keeps validating while the rotated
    // one propagates to every client/connection (mobile has several
    // independent fire-and-forget refresh triggers).
    const ROTATION_GRACE_MS = 2 * 60 * 1000;
    const patch: {
      expiresAt: number;
      tokenHash?: string;
      prevTokenHash?: string;
      prevTokenValidUntil?: number;
    } = {
      expiresAt: newExpiresAt,
    };
    if (args.newTokenHash && args.newTokenHash !== args.tokenHash) {
      // Guard against collision with another existing session — extremely
      // unlikely with 256-bit tokens but cheap to check.
      const collision = await ctx.db
        .query("sessions")
        .withIndex("by_tokenHash", (q) => q.eq("tokenHash", args.newTokenHash!))
        .unique();
      if (!collision) {
        patch.tokenHash = args.newTokenHash;
        // Keep the just-rotated token alive for the grace window.
        patch.prevTokenHash = args.tokenHash;
        patch.prevTokenValidUntil = Date.now() + ROTATION_GRACE_MS;
      }
    }
    await ctx.db.patch(session._id, patch);
    return {
      expiresAt: newExpiresAt,
      rotated: patch.tokenHash !== undefined,
    };
  },
});

/**
 * Delete a session (logout).
 */
export const deleteSession = mutation({
  args: {
    tokenHash: v.string(),
  },
  handler: async (ctx, args) => {
    const session = await ctx.db
      .query("sessions")
      .withIndex("by_tokenHash", (q) => q.eq("tokenHash", args.tokenHash))
      .unique();

    if (session) {
      await ctx.db.delete(session._id);
    }
  },
});

/**
 * Delete ALL sessions for a user (logout everywhere).
 * Validates the token first, then deletes every session for that user.
 */
export const deleteAllSessions = mutation({
  args: {
    tokenHash: v.string(),
  },
  handler: async (ctx, args) => {
    const result = await validateSessionInternal(ctx, args.tokenHash);
    if (!result) return;

    const sessions = await ctx.db
      .query("sessions")
      .withIndex("by_userId", (q) => q.eq("userId", result.user._id))
      .collect();
    for (const session of sessions) {
      await ctx.db.delete(session._id);
    }
  },
});

export const deleteSessionsByDeviceId = mutation({
  args: {
    userId: v.id("users"),
    deviceId: v.string(),
  },
  handler: async (ctx, args) => {
    const sessions = await ctx.db
      .query("sessions")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .collect();
    for (const session of sessions) {
      if (session.userId === args.userId) {
        await ctx.db.delete(session._id);
      }
    }
  },
});

/**
 * Create a user with email/password.
 */
export const createEmailUser = mutation({
  args: {
    email: v.string(),
    fullName: v.string(),
    passwordHash: v.string(),
  },
  handler: async (ctx, args) => {
    // Check for duplicate email
    const existing = await ctx.db
      .query("users")
      .withIndex("by_email", (q) => q.eq("email", args.email))
      .unique();

    if (existing) {
      throw new Error("EMAIL_EXISTS");
    }

    const userId = randomHex(16);
    const userDocId = await ctx.db.insert("users", {
      userId,
      email: args.email,
      fullName: args.fullName,
      provider: "email",
      providerId: args.email,
      passwordHash: args.passwordHash,
      emailVerified: false,
      createdAt: Date.now(),
    });
    // Create default settings for new user with platform relay as default
    const defaultRelay = await getDefaultRelay(ctx);
    await ctx.db.insert("userSettings", {
      userId: userDocId,
      forceRelay: false,
      ...defaultRelay,
    });
    await ensureAuthIdentity(ctx, userDocId, "email", args.email, args.email);

    // Send welcome email
    await ctx.scheduler.runAfter(0, internal.email.send, {
      from: "Kivanc from Yaver <kivanc@yaver.io>",
      to: args.email,
      subject: "Welcome to Yaver",
      html: welcomeHtml(args.fullName || args.email),
      replyTo: "kivanc@yaver.io",
    });

    // Verification email: enqueue a fresh token so the user can
    // later link OAuth providers to this account by proving they
    // own the inbox.
    await scheduleEmailVerification(ctx, userDocId, args.email, args.fullName);

    return userDocId;
  },
});

/**
 * Provider hints for an existing email — used when a fresh signup
 * collides with an account that's already in the system. Returned
 * inside EMAIL_EXISTS_USE_PROVIDER so the UI can route the user to
 * "Continue with Apple to link" instead of dead-ending at the error.
 *
 * Anonymous and rate-limited to (email-shaped strings only); the same
 * presence signal is already leakable via /auth/login wrong-password
 * vs unknown-user responses, so this isn't strictly new surface.
 */
export const lookupExistingProvidersByEmail = query({
  args: { email: v.string() },
  handler: async (ctx, args) => {
    const normalized = args.email.trim().toLowerCase();
    if (!normalized || !normalized.includes("@")) {
      return { exists: false, providers: [] as string[], hasPasskey: false };
    }
    const user = await ctx.db
      .query("users")
      .withIndex("by_email", (q) => q.eq("email", normalized))
      .unique();
    if (!user) {
      return { exists: false, providers: [] as string[], hasPasskey: false };
    }
    const identities = await ctx.db
      .query("authIdentities")
      .withIndex("by_userId", (q) => q.eq("userId", user._id))
      .collect();
    const providers = Array.from(new Set(identities.map((row) => row.provider))).sort();
    if (providers.length === 0) providers.push(user.provider);
    const passkeys = await ctx.db
      .query("passkeys")
      .withIndex("by_userId", (q) => q.eq("userId", user._id))
      .take(1);
    return {
      exists: true,
      providers,
      hasPasskey: passkeys.length > 0,
      // Deliberately omitting emailVerified — that flag is internal
      // account-state and an anonymous caller doesn't need it for the
      // signup-collision UI. Leaking it would let an attacker probe
      // which accounts have unlocked auto-linking.
    };
  },
});

// ── Email verification ──────────────────────────────────────────────

/**
 * Schedule an email-verification email for an unverified user. Idempotent:
 * if there's already an unconsumed, unexpired token for this user we
 * re-use it rather than mint a new one (avoids flooding the inbox when
 * settings UI auto-resends). Token is 32 bytes hex; URL is built off the
 * canonical web origin so the link works regardless of where the email
 * client renders it.
 */
async function scheduleEmailVerification(
  ctx: MutationCtx,
  userId: Id<"users">,
  email: string,
  fullName: string,
) {
  const normalized = email.trim().toLowerCase();
  if (!normalized) return;

  const existing = await ctx.db
    .query("emailVerifications")
    .withIndex("by_userId", (q) => q.eq("userId", userId))
    .collect();
  const now = Date.now();
  let token = "";
  for (const row of existing) {
    if (!row.consumedAt && row.expiresAt > now && row.email === normalized) {
      token = row.token;
      break;
    }
  }
  if (!token) {
    token = randomHex(32);
    await ctx.db.insert("emailVerifications", {
      token,
      userId,
      email: normalized,
      expiresAt: now + 24 * 60 * 60 * 1000,
      createdAt: now,
    });
  }

  await ctx.scheduler.runAfter(0, internal.email.send, {
    from: "Yaver <kivanc@yaver.io>",
    to: normalized,
    subject: "Verify your email for Yaver",
    html: verifyEmailHtml(fullName || normalized, token),
    replyTo: "kivanc@yaver.io",
  });
}

/**
 * Re-send a verification email for the currently signed-in user. Used
 * by Settings "Verify email" button and by the post-signup banner.
 */
export const requestEmailVerification = mutation({
  args: { tokenHash: v.string() },
  handler: async (ctx, args) => {
    const result = await validateSessionInternal(ctx, args.tokenHash);
    if (!result) throw new Error("Unauthorized");
    if (result.user.emailVerified === true) {
      return { ok: true, alreadyVerified: true };
    }
    if (!result.user.email) {
      return { ok: false, error: "NO_EMAIL_ON_ACCOUNT" };
    }
    await scheduleEmailVerification(ctx, result.user._id, result.user.email, result.user.fullName);
    return { ok: true, alreadyVerified: false };
  },
});

/**
 * Consume a verification token. Flips users.emailVerified=true and
 * unlocks email-keyed auto-linking for this account. Single-use:
 * subsequent attempts with the same token return alreadyConsumed=true
 * so the UI can render an idempotent success state.
 */
export const confirmEmailVerification = mutation({
  args: { token: v.string() },
  handler: async (ctx, args) => {
    const row = await ctx.db
      .query("emailVerifications")
      .withIndex("by_token", (q) => q.eq("token", args.token))
      .unique();
    if (!row) return { ok: false, error: "TOKEN_NOT_FOUND" };
    if (row.expiresAt < Date.now()) {
      return { ok: false, error: "TOKEN_EXPIRED" };
    }
    if (row.consumedAt) {
      return { ok: true, alreadyConsumed: true };
    }
    const user = await ctx.db.get(row.userId);
    if (!user) return { ok: false, error: "USER_NOT_FOUND" };
    if (user.email !== row.email) {
      // The user changed their email after the token was issued.
      // Don't auto-flip emailVerified for a stale address.
      return { ok: false, error: "EMAIL_CHANGED" };
    }
    await ctx.db.patch(row.userId, {
      emailVerified: true,
      emailVerifiedAt: Date.now(),
    });
    await ctx.db.patch(row._id, { consumedAt: Date.now() });
    return { ok: true, alreadyConsumed: false };
  },
});

/**
 * Look up an email user for login. Returns user with passwordHash.
 */
export const lookupEmailUser = query({
  args: { email: v.string() },
  handler: async (ctx, args) => {
    const user = await ctx.db
      .query("users")
      .withIndex("by_email", (q) => q.eq("email", args.email))
      .unique();

    if (!user || user.provider !== "email") return null;

    return {
      _id: user._id,
      userId: user.userId,
      email: user.email,
      fullName: user.fullName,
      passwordHash: user.passwordHash,
    };
  },
});

/**
 * Check if a user has TOTP enabled. Used by login to decide if 2FA is required.
 */
export const getUserWithTotp = query({
  args: { userId: v.id("users") },
  handler: async (ctx, args) => {
    const user = await ctx.db.get(args.userId);
    if (!user) return null;
    return { totpEnabled: user.totpEnabled ?? false };
  },
});

/**
 * Get the user document _id from a session token hash.
 * Used by device code authorization to pass a typed Id<"users"> to mutations.
 */
export const getUserDocId = query({
  args: { tokenHash: v.string() },
  handler: async (ctx, args) => {
    const result = await validateSessionInternal(ctx, args.tokenHash);
    if (!result) return null;
    return result.user._id;
  },
});

/**
 * Resolve a userDocId to the public user profile (for signup/login to surface
 * the stable, shareable userId string instead of the internal doc id).
 */
export const getUserPublicProfile = query({
  args: { userDocId: v.id("users") },
  handler: async (ctx, args) => {
    const user = await ctx.db.get(args.userDocId);
    if (!user) return null;
    return {
      userId: user.userId,
      email: user.email,
      fullName: user.fullName,
    };
  },
});

/**
 * Update user profile fields (e.g. fullName).
 */
export const updateProfile = mutation({
  args: {
    tokenHash: v.string(),
    fullName: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const result = await validateSessionInternal(ctx, args.tokenHash);
    if (!result) throw new Error("Unauthorized");

    const patch: Record<string, string> = {};
    if (args.fullName !== undefined) patch.fullName = args.fullName;

    if (Object.keys(patch).length > 0) {
      await ctx.db.patch(result.user._id, patch);
    }
  },
});

/**
 * Delete a user account and all associated data (sessions, devices).
 * Requires a valid session token.
 */
export const deleteAccount = mutation({
  args: {
    tokenHash: v.string(),
  },
  handler: async (ctx, args) => {
    const result = await validateSessionInternal(ctx, args.tokenHash);
    if (!result) {
      throw new Error("Unauthorized");
    }

    const userId = result.user._id;

    // Delete all sessions for this user
    const sessions = await ctx.db
      .query("sessions")
      .withIndex("by_userId", (q) => q.eq("userId", userId))
      .collect();
    for (const session of sessions) {
      await ctx.db.delete(session._id);
    }

    // Delete all devices for this user
    const devices = await ctx.db
      .query("devices")
      .withIndex("by_userId", (q) => q.eq("userId", userId))
      .collect();
    for (const device of devices) {
      await ctx.db.delete(device._id);
    }

    await deleteInfraGrantArtifactsForUser(ctx, userId);

    // Delete the user
    await ctx.db.delete(userId);
  },
});

// ── SDK Tokens ──────────────────────────────────────────────────────

/** Default scopes for SDK tokens — feedback-related only, no task execution. */
const DEFAULT_SDK_SCOPES = ["feedback", "blackbox", "voice", "builds"];

/**
 * Create an SDK token for the Feedback SDK.
 * Requires a valid CLI session token. The SDK token is independent —
 * CLI reauth does not invalidate it.
 */
export const createSdkToken = mutation({
  args: {
    tokenHash: v.string(),
    sessionTokenHash: v.string(),
    label: v.optional(v.string()),
    scopes: v.optional(v.array(v.string())),
    allowedCIDRs: v.optional(v.array(v.string())),
    expiresInMs: v.optional(v.number()), // custom expiry (default 1 year)
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.sessionTokenHash);
    if (!session) throw new Error("Unauthorized");

    const existing = await ctx.db
      .query("sdkTokens")
      .withIndex("by_tokenHash", (q) => q.eq("tokenHash", args.tokenHash))
      .unique();
    if (existing) throw new Error("SDK token already exists");

    const expiresIn = args.expiresInMs ?? 365 * 24 * 60 * 60 * 1000;
    const expiresAt = Date.now() + expiresIn;
    await ctx.db.insert("sdkTokens", {
      tokenHash: args.tokenHash,
      userId: session.user._id,
      label: args.label,
      scopes: args.scopes ?? DEFAULT_SDK_SCOPES,
      allowedCIDRs: args.allowedCIDRs,
      expiresAt,
      createdAt: Date.now(),
    });
    return { expiresAt };
  },
});

/**
 * Validate an SDK token. Returns user info, scopes, and allowedCIDRs.
 * Handles rotation grace period: a replaced token is valid for 5 minutes.
 */
export const validateSdkToken = query({
  args: {
    tokenHash: v.string(),
  },
  handler: async (ctx, args) => {
    const sdkToken = await ctx.db
      .query("sdkTokens")
      .withIndex("by_tokenHash", (q) => q.eq("tokenHash", args.tokenHash))
      .unique();

    if (!sdkToken) return null;
    if (sdkToken.expiresAt < Date.now()) return null;

    // Rotation grace: replaced tokens valid for 5 minutes
    if (sdkToken.replacedAt) {
      const gracePeriod = 5 * 60 * 1000;
      if (Date.now() - sdkToken.replacedAt > gracePeriod) return null;
    }

    const user = await ctx.db.get(sdkToken.userId);
    if (!user) return null;

    return {
      userId: user.userId,
      email: user.email,
      fullName: user.fullName,
      provider: user.provider,
      scopes: sdkToken.scopes ?? DEFAULT_SDK_SCOPES,
      allowedCIDRs: sdkToken.allowedCIDRs ?? [],
      delegatedGuestUserId: sdkToken.delegatedGuestUserId ? String(sdkToken.delegatedGuestUserId) : undefined,
      delegatedGuestScope: sdkToken.delegatedGuestScope,
      sourceSurface: sdkToken.sourceSurface,
      targetDeviceId: sdkToken.targetDeviceId,
      allowedProjects: sdkToken.allowedProjects ?? [],
    };
  },
});

/**
 * Rotate an SDK token: create a new one, mark old as replaced (5min grace).
 * Called with the current SDK token hash (for auth) and a new token hash.
 */
export const rotateSdkToken = mutation({
  args: {
    currentTokenHash: v.string(),
    newTokenHash: v.string(),
  },
  handler: async (ctx, args) => {
    const current = await ctx.db
      .query("sdkTokens")
      .withIndex("by_tokenHash", (q) => q.eq("tokenHash", args.currentTokenHash))
      .unique();
    if (!current) throw new Error("Invalid token");
    if (current.expiresAt < Date.now()) throw new Error("Token expired");
    if (current.replacedAt) throw new Error("Token already rotated");

    // Create new token inheriting scopes and allowedCIDRs
    const expiresAt = Date.now() + 365 * 24 * 60 * 60 * 1000;
    await ctx.db.insert("sdkTokens", {
      tokenHash: args.newTokenHash,
      userId: current.userId,
      label: current.label,
      scopes: current.scopes,
      allowedCIDRs: current.allowedCIDRs,
      delegatedGuestUserId: current.delegatedGuestUserId,
      delegatedGuestScope: current.delegatedGuestScope,
      sourceSurface: current.sourceSurface,
      targetDeviceId: current.targetDeviceId,
      allowedProjects: current.allowedProjects,
      expiresAt,
      createdAt: Date.now(),
    });

    // Mark old token as replaced (5min grace period)
    await ctx.db.patch(current._id, {
      replacedBy: args.newTokenHash,
      replacedAt: Date.now(),
    });

    return { expiresAt };
  },
});

/**
 * List all SDK tokens for the authenticated user.
 */
export const listSdkTokens = query({
  args: {
    sessionTokenHash: v.string(),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.sessionTokenHash);
    if (!session) return [];

    const tokens = await ctx.db
      .query("sdkTokens")
      .withIndex("by_userId", (q) => q.eq("userId", session.user._id))
      .collect();

    return tokens.map((t) => ({
      label: t.label,
      scopes: t.scopes,
      allowedCIDRs: t.allowedCIDRs,
      createdAt: t.createdAt,
      expiresAt: t.expiresAt,
      expired: t.expiresAt < Date.now(),
      rotated: !!t.replacedAt,
    }));
  },
});

/**
 * Revoke (delete) an SDK token.
 */
export const revokeSdkToken = mutation({
  args: {
    sessionTokenHash: v.string(),
    sdkTokenHash: v.string(),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.sessionTokenHash);
    if (!session) throw new Error("Unauthorized");

    const sdkToken = await ctx.db
      .query("sdkTokens")
      .withIndex("by_tokenHash", (q) => q.eq("tokenHash", args.sdkTokenHash))
      .unique();

    if (!sdkToken) return;
    if (sdkToken.userId !== session.user._id) {
      throw new Error("Unauthorized");
    }
    await ctx.db.delete(sdkToken._id);
  },
});

// ── Security Events ─────────────────────────────────────────────────

/**
 * Report a security event (new IP, token rotation, etc.).
 */
export const reportSecurityEvent = mutation({
  args: {
    tokenHash: v.string(), // for auth — can be session or SDK token
    eventType: v.string(),
    details: v.string(),   // JSON blob
  },
  handler: async (ctx, args) => {
    // Validate via session first
    let userId: Id<"users"> | null = null;
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (session) {
      userId = session.user._id;
    } else {
      // Try SDK token
      const sdkToken = await ctx.db
        .query("sdkTokens")
        .withIndex("by_tokenHash", (q) => q.eq("tokenHash", args.tokenHash))
        .unique();
      if (sdkToken && sdkToken.expiresAt >= Date.now()) {
        userId = sdkToken.userId;
      }
    }
    if (!userId) throw new Error("Unauthorized");

    await ctx.db.insert("securityEvents", {
      userId,
      eventType: args.eventType,
      details: args.details,
      read: false,
      createdAt: Date.now(),
    });
  },
});

/**
 * List unread security events for the authenticated user.
 */
export const listSecurityEvents = query({
  args: {
    tokenHash: v.string(),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) return [];

    return await ctx.db
      .query("securityEvents")
      .withIndex("by_userId", (q) => q.eq("userId", session.user._id))
      .order("desc")
      .take(50);
  },
});

/** Look up a user by email (internal only — used by webhook handlers). */
export const getUserByEmail = internalQuery({
  args: { email: v.string() },
  handler: async (ctx, { email }) => {
    return await ctx.db
      .query("users")
      .withIndex("by_email", (q) => q.eq("email", email))
      .first();
  },
});

/**
 * Change password for an authenticated email user.
 */
export const changePassword = mutation({
  args: {
    tokenHash: v.string(),
    newPasswordHash: v.string(),
  },
  handler: async (ctx, args) => {
    const result = await validateSessionInternal(ctx, args.tokenHash);
    if (!result) throw new Error("Unauthorized");

    if (result.user.provider !== "email") {
      throw new Error("Password change is only available for email accounts");
    }

    await ctx.db.patch(result.user._id, { passwordHash: args.newPasswordHash });
    return { ok: true };
  },
});

// ── Password Reset ──────────────────────────────────────────────────

const RESET_TOKEN_TTL_MS = 60 * 60 * 1000;        // 1 hour
const RESET_DAILY_LIMIT = 5;                       // max resets per email per day
const RESET_COOLDOWN_MS = 60 * 1000;               // 1 min between requests

/**
 * Create a password reset record. Returns tokenHash so the HTTP action
 * can build the reset link with the raw token.
 *
 * Rate-limiting:
 *  - Max 5 requests per email per 24h
 *  - 60s cooldown between consecutive requests
 */
export const createPasswordReset = mutation({
  args: {
    email: v.string(),
    tokenHash: v.string(),
  },
  handler: async (ctx, args) => {
    const email = args.email.toLowerCase().trim();

    // Look up user — must be an email provider user
    const user = await ctx.db
      .query("users")
      .withIndex("by_email", (q) => q.eq("email", email))
      .unique();

    if (!user || user.provider !== "email") {
      // Return silently — don't reveal whether account exists
      return { ok: true, sent: false };
    }

    const now = Date.now();
    const dayAgo = now - 24 * 60 * 60 * 1000;

    // Fetch recent resets for this email
    const recentResets = await ctx.db
      .query("passwordResets")
      .withIndex("by_email", (q) => q.eq("email", email))
      .collect();

    const resetsToday = recentResets.filter((r) => r.createdAt > dayAgo);

    // Daily limit
    if (resetsToday.length >= RESET_DAILY_LIMIT) {
      return { ok: true, sent: false, rateLimited: true };
    }

    // Cooldown — check the most recent request
    const latest = resetsToday.sort((a, b) => b.createdAt - a.createdAt)[0];
    if (latest && now - latest.createdAt < RESET_COOLDOWN_MS) {
      return { ok: true, sent: false, rateLimited: true };
    }

    // Create the reset record
    await ctx.db.insert("passwordResets", {
      tokenHash: args.tokenHash,
      email,
      userId: user._id,
      expiresAt: now + RESET_TOKEN_TTL_MS,
      createdAt: now,
    });

    return { ok: true, sent: true, userId: user._id, fullName: user.fullName };
  },
});

/**
 * Validate a password reset token and apply the new password.
 * The token can only be used once.
 */
export const resetPassword = mutation({
  args: {
    tokenHash: v.string(),
    newPasswordHash: v.string(),
  },
  handler: async (ctx, args) => {
    const reset = await ctx.db
      .query("passwordResets")
      .withIndex("by_tokenHash", (q) => q.eq("tokenHash", args.tokenHash))
      .unique();

    if (!reset) {
      throw new Error("INVALID_TOKEN");
    }

    if (reset.usedAt) {
      throw new Error("TOKEN_USED");
    }

    if (reset.expiresAt < Date.now()) {
      throw new Error("TOKEN_EXPIRED");
    }

    // Mark token as used
    await ctx.db.patch(reset._id, { usedAt: Date.now() });

    // Update the user's password
    await ctx.db.patch(reset.userId, { passwordHash: args.newPasswordHash });

    // Invalidate all existing sessions for this user (force re-login)
    const sessions = await ctx.db
      .query("sessions")
      .withIndex("by_userId", (q) => q.eq("userId", reset.userId))
      .collect();

    for (const session of sessions) {
      await ctx.db.delete(session._id);
    }

    return { ok: true };
  },
});
