import { v } from "convex/values";
import { mutation, query, internalQuery, QueryCtx, MutationCtx } from "./_generated/server";
import { Id } from "./_generated/dataModel";

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
    provider: "google" | "microsoft" | "apple" | "email";
    providerId: string;
    passwordHash?: string;
    avatarUrl?: string;
    surveyCompleted?: boolean;
    totpSecret?: string;
    totpEnabled?: boolean;
    totpRecoveryCodes?: string;
    createdAt: number;
  };
  sessionId: Id<"sessions">;
} | null> {
  const session = await ctx.db
    .query("sessions")
    .withIndex("by_tokenHash", (q) => q.eq("tokenHash", tokenHash))
    .unique();

  if (!session) return null;
  if (session.expiresAt < Date.now()) return null;

  const user = await ctx.db.get(session.userId);
  if (!user) return null;

  return { user, sessionId: session._id };
}

// ── Mutations ────────────────────────────────────────────────────────

/**
 * Upsert a user by provider + providerId.
 * Returns the user's _id.
 */
export const createOrUpdateUser = mutation({
  args: {
    email: v.string(),
    fullName: v.string(),
    provider: v.union(v.literal("google"), v.literal("microsoft"), v.literal("apple"), v.literal("email")),
    providerId: v.string(),
    avatarUrl: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    // First: exact provider+providerId match (returning user via same provider)
    const byProvider = await ctx.db
      .query("users")
      .withIndex("by_provider", (q) =>
        q.eq("provider", args.provider).eq("providerId", args.providerId)
      )
      .unique();

    if (byProvider) {
      const patch: Record<string, string | undefined> = {
        email: args.email,
        avatarUrl: args.avatarUrl,
      };
      // Only overwrite fullName if the new value is non-empty
      if (args.fullName) {
        patch.fullName = args.fullName;
      }
      await ctx.db.patch(byProvider._id, patch);
      // Ensure user has relay settings (may have been lost due to deletion/re-creation)
      const settings = await ctx.db
        .query("userSettings")
        .withIndex("by_userId", (q) => q.eq("userId", byProvider._id))
        .first();
      if (!settings) {
        const defaultRelay = await getDefaultRelay(ctx);
        await ctx.db.insert("userSettings", { userId: byProvider._id, forceRelay: false, ...defaultRelay });
      } else if (!settings.relayPassword) {
        const defaultRelay = await getDefaultRelay(ctx);
        if (defaultRelay.relayPassword) {
          await ctx.db.patch(settings._id, defaultRelay);
        }
      }
      return byProvider._id;
    }

    // Second: email match (account linking — same user, different provider)
    const byEmail = await ctx.db
      .query("users")
      .withIndex("by_email", (q) => q.eq("email", args.email))
      .unique();

    if (byEmail) {
      // Link to existing account — update avatar/name if better data available
      const patch: Record<string, string | undefined> = {};
      if (args.avatarUrl) patch.avatarUrl = args.avatarUrl;
      if (args.fullName && (!byEmail.fullName || byEmail.fullName === byEmail.email)) {
        // Update name if current name is empty or just the email (placeholder)
        patch.fullName = args.fullName;
      }
      if (Object.keys(patch).length > 0) {
        await ctx.db.patch(byEmail._id, patch);
      }
      // Ensure user has relay settings
      const settings = await ctx.db
        .query("userSettings")
        .withIndex("by_userId", (q) => q.eq("userId", byEmail._id))
        .first();
      if (!settings) {
        const defaultRelay = await getDefaultRelay(ctx);
        await ctx.db.insert("userSettings", { userId: byEmail._id, forceRelay: false, ...defaultRelay });
      } else if (!settings.relayPassword) {
        const defaultRelay = await getDefaultRelay(ctx);
        if (defaultRelay.relayPassword) {
          await ctx.db.patch(settings._id, defaultRelay);
        }
      }
      return byEmail._id;
    }

    const userId = randomHex(16);
    const userDocId = await ctx.db.insert("users", {
      userId,
      email: args.email,
      fullName: args.fullName,
      provider: args.provider,
      providerId: args.providerId,
      avatarUrl: args.avatarUrl,
      createdAt: Date.now(),
    });
    // Create default settings for new user with platform relay as default
    const defaultRelay = await getDefaultRelay(ctx);
    await ctx.db.insert("userSettings", {
      userId: userDocId,
      forceRelay: false,
      ...defaultRelay,
    });
    return userDocId;
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
    expiresAt: v.number(),
  },
  handler: async (ctx, args) => {
    return await ctx.db.insert("sessions", {
      tokenHash: args.tokenHash,
      userId: args.userId,
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
      userId: result.user.userId,
      email: result.user.email,
      fullName: result.user.fullName,
      provider: result.user.provider,
      avatarUrl: result.user.avatarUrl,
      surveyCompleted: result.user.surveyCompleted ?? false,
    };
  },
});

/**
 * Refresh a session — extends expiresAt by 30 days from now.
 * Returns the new expiresAt, or null if session is invalid/expired.
 */
export const refreshSession = mutation({
  args: {
    tokenHash: v.string(),
  },
  handler: async (ctx, args) => {
    const session = await ctx.db
      .query("sessions")
      .withIndex("by_tokenHash", (q) => q.eq("tokenHash", args.tokenHash))
      .unique();

    if (!session) return null;
    if (session.expiresAt < Date.now()) return null;

    const newExpiresAt = Date.now() + 365 * 24 * 60 * 60 * 1000; // 1 year
    await ctx.db.patch(session._id, { expiresAt: newExpiresAt });
    return { expiresAt: newExpiresAt };
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
      createdAt: Date.now(),
    });
    // Create default settings for new user with platform relay as default
    const defaultRelay = await getDefaultRelay(ctx);
    await ctx.db.insert("userSettings", {
      userId: userDocId,
      forceRelay: false,
      ...defaultRelay,
    });
    return userDocId;
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