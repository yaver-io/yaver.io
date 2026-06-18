// Passkey DB ops — queries and mutations only. Lives next to
// passkeys.ts (which is a "use node" actions file) because Convex
// disallows queries/mutations in Node-runtime modules.

import { mutation, query } from "./_generated/server";
import { v } from "convex/values";
import { internal } from "./_generated/api";
import { randomHex } from "./auth";
import { verifyEmailHtml } from "./email";

const challengePurpose = v.union(
  v.literal("register"),
  v.literal("login"),
  v.literal("signup"),
);

// ── Challenges ──────────────────────────────────────────────────────

export const recordChallenge = mutation({
  args: {
    challenge: v.string(),
    purpose: challengePurpose,
    userId: v.optional(v.id("users")),
  },
  handler: async (ctx, args) => {
    // 5-minute TTL is more than enough for a Touch ID prompt and
    // short enough that a leaked challenge can't be replayed later.
    const expiresAt = Date.now() + 5 * 60 * 1000;
    await ctx.db.insert("passkeyChallenges", {
      challenge: args.challenge,
      purpose: args.purpose,
      userId: args.userId,
      expiresAt,
      createdAt: Date.now(),
    });
  },
});

export const findChallenge = query({
  args: {
    challenge: v.string(),
    purpose: challengePurpose,
  },
  handler: async (ctx, args) => {
    const row = await ctx.db
      .query("passkeyChallenges")
      .withIndex("by_challenge", (q) => q.eq("challenge", args.challenge))
      .unique();
    if (!row) return null;
    if (row.purpose !== args.purpose) return null;
    if (row.expiresAt < Date.now()) return null;
    return { _id: row._id, userId: row.userId ?? null };
  },
});

export const consumeChallenge = mutation({
  args: { challenge: v.string() },
  handler: async (ctx, args) => {
    const row = await ctx.db
      .query("passkeyChallenges")
      .withIndex("by_challenge", (q) => q.eq("challenge", args.challenge))
      .unique();
    if (row) await ctx.db.delete(row._id);
  },
});

// Sweep stale challenges. Called from the existing cleanup cron so we
// don't accumulate 5-minute rows indefinitely. Safe to call on a
// schedule even when nobody is registering.
export const sweepExpiredChallenges = mutation({
  args: {},
  handler: async (ctx) => {
    const now = Date.now();
    const stale = await ctx.db
      .query("passkeyChallenges")
      .withIndex("by_expiresAt", (q) => q.lt("expiresAt", now))
      .take(200);
    for (const row of stale) await ctx.db.delete(row._id);
    return { deleted: stale.length };
  },
});

// ── Credentials ─────────────────────────────────────────────────────

export const listForUser = query({
  args: { userId: v.id("users") },
  handler: async (ctx, args) => {
    const rows = await ctx.db
      .query("passkeys")
      .withIndex("by_userId", (q) => q.eq("userId", args.userId))
      .collect();
    return rows.map((row) => ({
      _id: row._id,
      credentialId: row.credentialId,
      transports: row.transports ?? null,
      deviceLabel: row.deviceLabel ?? null,
      backedUp: row.backedUp ?? null,
      createdAt: row.createdAt,
      lastUsedAt: row.lastUsedAt ?? null,
    }));
  },
});

export const insertCredential = mutation({
  args: {
    userId: v.id("users"),
    credentialId: v.string(),
    publicKey: v.string(),
    counter: v.number(),
    transports: v.optional(v.array(v.string())),
    deviceLabel: v.optional(v.string()),
    backedUp: v.optional(v.boolean()),
  },
  handler: async (ctx, args) => {
    const existing = await ctx.db
      .query("passkeys")
      .withIndex("by_credentialId", (q) => q.eq("credentialId", args.credentialId))
      .unique();
    if (existing) throw new Error("CREDENTIAL_EXISTS");
    return await ctx.db.insert("passkeys", {
      userId: args.userId,
      credentialId: args.credentialId,
      publicKey: args.publicKey,
      counter: args.counter,
      transports: args.transports,
      deviceLabel: args.deviceLabel,
      backedUp: args.backedUp,
      createdAt: Date.now(),
    });
  },
});

export const findByCredentialId = query({
  args: { credentialId: v.string() },
  handler: async (ctx, args) => {
    const row = await ctx.db
      .query("passkeys")
      .withIndex("by_credentialId", (q) => q.eq("credentialId", args.credentialId))
      .unique();
    if (!row) return null;
    return {
      _id: row._id,
      userId: row.userId,
      credentialId: row.credentialId,
      publicKey: row.publicKey,
      counter: row.counter,
      transports: row.transports ?? null,
    };
  },
});

export const touchCredential = mutation({
  args: { credentialId: v.string(), counter: v.number() },
  handler: async (ctx, args) => {
    const row = await ctx.db
      .query("passkeys")
      .withIndex("by_credentialId", (q) => q.eq("credentialId", args.credentialId))
      .unique();
    if (!row) return;
    await ctx.db.patch(row._id, {
      counter: args.counter,
      lastUsedAt: Date.now(),
    });
  },
});

export const removeCredential = mutation({
  args: { userId: v.id("users"), credentialId: v.string() },
  handler: async (ctx, args) => {
    const row = await ctx.db
      .query("passkeys")
      .withIndex("by_credentialId", (q) => q.eq("credentialId", args.credentialId))
      .unique();
    if (!row) return { ok: false };
    if (row.userId !== args.userId) throw new Error("NOT_OWNER");
    await ctx.db.delete(row._id);
    return { ok: true };
  },
});

// ── Signup-with-passkey ─────────────────────────────────────────────

// emailAvailable lets the signupStart action gate before we issue a
// challenge. We don't want a passkey-signup attempt to silently fail
// at signupFinish after the user has already done the Touch ID prompt.
//
// Returns:
//   { available: true }                    — email is unused → proceed
//   { available: false, hasPasskey: true } — email exists AND has a passkey already
//   { available: false, hasPasskey: false }— email exists via OAuth/email; user
//                                            should sign in normally + enroll
//                                            from settings (PasskeyEnrollPrompt)
export const emailAvailable = query({
  args: { email: v.string() },
  handler: async (ctx, args) => {
    const normalized = args.email.trim().toLowerCase();
    const existing = await ctx.db
      .query("users")
      .withIndex("by_email", (q) => q.eq("email", normalized))
      .unique();
    if (!existing) {
      return { available: true, hasPasskey: false, providers: [] as string[] };
    }
    const passkeys = await ctx.db
      .query("passkeys")
      .withIndex("by_userId", (q) => q.eq("userId", existing._id))
      .take(1);
    const identities = await ctx.db
      .query("authIdentities")
      .withIndex("by_userId", (q) => q.eq("userId", existing._id))
      .collect();
    const providers = Array.from(new Set(identities.map((row) => row.provider))).sort();
    if (providers.length === 0) providers.push(existing.provider);
    return {
      available: false,
      hasPasskey: passkeys.length > 0,
      providers,
      emailVerified: existing.emailVerified === true,
    };
  },
});

// createPasskeyUser is the mutation called from signupFinish AFTER
// attestation has been verified. Atomically:
//   1. Re-check email is still free (race protection — someone else
//      could have signed up with the same email between signupStart
//      and signupFinish).
//   2. Insert the users row with provider="passkey" and no
//      passwordHash. The user can later add a password / link OAuth
//      from settings.
//   3. Insert the first passkey credential.
//   4. Insert the auth identity row so future OAuth flows that match
//      this email can link rather than collide.
//   5. Insert default userSettings (relay, etc.) — same pattern as
//      email signup.
//
// Returns the new userDocId so the action can mint a session token.
export const createPasskeyUser = mutation({
  args: {
    email: v.string(),
    fullName: v.string(),
    credentialId: v.string(),
    publicKey: v.string(),
    counter: v.number(),
    transports: v.optional(v.array(v.string())),
    deviceLabel: v.optional(v.string()),
    backedUp: v.optional(v.boolean()),
  },
  handler: async (ctx, args) => {
    const email = args.email.trim().toLowerCase();
    const dupe = await ctx.db
      .query("users")
      .withIndex("by_email", (q) => q.eq("email", email))
      .unique();
    if (dupe) throw new Error("EMAIL_EXISTS");

    const credDupe = await ctx.db
      .query("passkeys")
      .withIndex("by_credentialId", (q) => q.eq("credentialId", args.credentialId))
      .unique();
    if (credDupe) throw new Error("CREDENTIAL_EXISTS");

    const userId = randomHex(16);
    const userDocId = await ctx.db.insert("users", {
      userId,
      email,
      fullName: args.fullName.trim() || email,
      provider: "passkey",
      providerId: args.credentialId,
      // Passkey signup has no email-ownership proof (the user just
      // typed it). Stay unverified until they click the verify-email
      // link, which then unlocks auto-link with OAuth providers
      // returning the same address.
      emailVerified: false,
      createdAt: Date.now(),
    });

    await ctx.db.insert("passkeys", {
      userId: userDocId,
      credentialId: args.credentialId,
      publicKey: args.publicKey,
      counter: args.counter,
      transports: args.transports,
      deviceLabel: args.deviceLabel,
      backedUp: args.backedUp,
      createdAt: Date.now(),
    });

    await ctx.db.insert("authIdentities", {
      userId: userDocId,
      provider: "passkey",
      providerId: args.credentialId,
      email,
      createdAt: Date.now(),
      lastUsedAt: Date.now(),
    });

    // Default settings — copied from auth.ts createEmailUser path.
    await ctx.db.insert("userSettings", {
      userId: userDocId,
      forceRelay: false,
      moreOptionalTools: [],
    });

    // Email-verification token: passkey signup gives no proof of
    // email ownership, so we mint a verification link and let the
    // user click through at their leisure. Until they do,
    // users.emailVerified stays false and email-keyed OAuth
    // auto-linking refuses to link this account.
    const verificationToken = randomHex(32);
    await ctx.db.insert("emailVerifications", {
      token: verificationToken,
      userId: userDocId,
      email,
      expiresAt: Date.now() + 24 * 60 * 60 * 1000,
      createdAt: Date.now(),
    });
    await ctx.scheduler.runAfter(0, internal.email.send, {
      from: "Yaver <kivanc@yaver.io>",
      to: email,
      subject: "Verify your email for Yaver",
      html: verifyEmailHtml(args.fullName || email, verificationToken),
      replyTo: "kivanc@yaver.io",
    });

    return userDocId;
  },
});
