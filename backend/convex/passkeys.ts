"use node";
// WebAuthn / passkey support for yaver.io web sign-in.
//
// Strictly additive to the existing Apple / Google / GitHub / GitLab /
// Microsoft / email flows. A user can attach 1..N passkeys to their
// existing users row and sign in with any of them; falling back to
// OAuth/email is always available. Losing the passkey ≠ losing the
// account.
//
// All four flows below mint the same session token that /auth/login
// already issues, so downstream code (validate / refresh / device
// pairing / heartbeat) doesn't need to know about WebAuthn.

import { action, internalAction } from "./_generated/server";
import { v } from "convex/values";
import { api, internal } from "./_generated/api";
import {
  generateRegistrationOptions,
  verifyRegistrationResponse,
  generateAuthenticationOptions,
  verifyAuthenticationResponse,
} from "@simplewebauthn/server";

// Allowlist of RP IDs / origins the backend accepts. localhost is dev;
// yaver.io is production. Requests outside this set are rejected
// before any crypto runs. Override with WEBAUTHN_EXTRA_ORIGINS (comma
// list) for staging / preview deploys.
const STATIC_ORIGINS = [
  "https://yaver.io",
  "https://www.yaver.io",
  "http://localhost:3000",
  "http://localhost:3001",
];

function allowedOrigins(): string[] {
  const extra = (process.env.WEBAUTHN_EXTRA_ORIGINS || "")
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
  return [...STATIC_ORIGINS, ...extra];
}

function rpIdForOrigin(origin: string): string {
  try {
    const url = new URL(origin);
    return url.hostname === "www.yaver.io" ? "yaver.io" : url.hostname;
  } catch {
    return "yaver.io";
  }
}

function rpName(): string {
  return process.env.WEBAUTHN_RP_NAME || "Yaver";
}

// Convert SimpleWebAuthn's Uint8Array credentialID/publicKey into the
// base64url strings we persist. The library exposes `isoBase64URL` from
// helpers but we do it inline so the dependency surface stays tiny.
function bytesToB64url(bytes: Uint8Array): string {
  let bin = "";
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
  return btoa(bin).replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/, "");
}

function b64urlToBytes(input: string): Uint8Array {
  const pad = input.length % 4 === 0 ? "" : "=".repeat(4 - (input.length % 4));
  const bin = atob(input.replaceAll("-", "+").replaceAll("_", "/") + pad);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

// ── Registration ────────────────────────────────────────────────────

/**
 * Step 1 of registration. Caller must already be signed in (they pass
 * their session token; the route handler verifies it before calling
 * this action). Returns the WebAuthn options blob the browser hands to
 * navigator.credentials.create().
 */
export const registerStart = internalAction({
  args: {
    userDocId: v.id("users"),
    origin: v.string(),
    deviceLabel: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    if (!allowedOrigins().includes(args.origin)) {
      throw new Error("origin not allowed");
    }
    const rpID = rpIdForOrigin(args.origin);

    const profile = await ctx.runQuery(internal.auth.getUserPublicProfile, {
      userDocId: args.userDocId,
    });
    if (!profile) throw new Error("user not found");

    const existing = await ctx.runQuery(internal.passkeysDb.listForUser, {
      userId: args.userDocId,
    });

    const options = await generateRegistrationOptions({
      rpName: rpName(),
      rpID,
      // Stable per-user identifier the authenticator persists. Not
      // user-visible. Using userId (string) keeps this stable across
      // renames or doc-id renumbers.
      userName: profile.email,
      userDisplayName: profile.fullName || profile.email,
      userID: new TextEncoder().encode(profile.userId),
      attestationType: "none",
      // Tell the authenticator to skip credentials this user already
      // registered so the same passkey isn't enrolled twice.
      excludeCredentials: existing.map((p: { credentialId: string; transports: string[] | null }) => ({
        id: p.credentialId,
        transports: (p.transports as AuthenticatorTransport[] | null | undefined) ?? undefined,
      })),
      authenticatorSelection: {
        // residentKey "preferred" means "save it as a discoverable
        // passkey when the platform can". iCloud Keychain + Google
        // Password Manager + Windows Hello all create discoverable
        // credentials, which is what enables the username-less "Sign
        // in with passkey" button on the login page.
        residentKey: "preferred",
        userVerification: "preferred",
      },
    });

    await ctx.runMutation(internal.passkeysDb.recordChallenge, {
      challenge: options.challenge,
      purpose: "register",
      userId: args.userDocId,
    });

    return options;
  },
});

/**
 * Step 2 of registration. The browser produced an attestation; we
 * verify it against the challenge we issued in step 1, then persist
 * the credential.
 */
export const registerFinish = internalAction({
  args: {
    userDocId: v.id("users"),
    origin: v.string(),
    response: v.any(), // RegistrationResponseJSON from @simplewebauthn/browser
    deviceLabel: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    if (!allowedOrigins().includes(args.origin)) {
      throw new Error("origin not allowed");
    }
    const rpID = rpIdForOrigin(args.origin);

    const expectedChallenge = args.response?.response?.clientDataJSON
      ? extractClientDataChallenge(args.response.response.clientDataJSON)
      : null;
    if (!expectedChallenge) throw new Error("invalid response");

    const challengeRow = await ctx.runQuery(internal.passkeysDb.findChallenge, {
      challenge: expectedChallenge,
      purpose: "register",
    });
    if (!challengeRow) throw new Error("challenge expired or unknown");
    if (challengeRow.userId !== args.userDocId) throw new Error("challenge user mismatch");

    const verification = await verifyRegistrationResponse({
      response: args.response,
      expectedChallenge,
      expectedOrigin: args.origin,
      expectedRPID: rpID,
      requireUserVerification: false,
    });
    if (!verification.verified || !verification.registrationInfo) {
      throw new Error("attestation failed");
    }

    const cred = verification.registrationInfo.credential;
    const credentialIdB64 = cred.id; // already base64url string in v13
    const publicKeyB64 = bytesToB64url(cred.publicKey);

    await ctx.runMutation(internal.passkeysDb.insertCredential, {
      userId: args.userDocId,
      credentialId: credentialIdB64,
      publicKey: publicKeyB64,
      counter: cred.counter,
      transports: cred.transports ?? undefined,
      deviceLabel: args.deviceLabel,
      backedUp: verification.registrationInfo.credentialBackedUp,
    });

    await ctx.runMutation(internal.passkeysDb.consumeChallenge, {
      challenge: expectedChallenge,
    });

    return { ok: true, credentialId: credentialIdB64 };
  },
});

// ── Authentication (login) ──────────────────────────────────────────

/**
 * Step 1 of login. Anonymous: the browser doesn't tell us *which*
 * user it is — we issue a discoverable-credential challenge. The
 * browser presents whichever passkey the user picks (or auto-fills),
 * and the assertion in step 2 carries the credential id we look up.
 */
export const loginStart = action({
  args: {
    origin: v.string(),
  },
  handler: async (ctx, args) => {
    if (!allowedOrigins().includes(args.origin)) {
      throw new Error("origin not allowed");
    }
    const rpID = rpIdForOrigin(args.origin);

    const options = await generateAuthenticationOptions({
      rpID,
      // Empty allowCredentials → "discoverable credentials" mode. The
      // browser shows the passkey picker; user can sign in without
      // typing email first.
      allowCredentials: [],
      userVerification: "preferred",
    });

    await ctx.runMutation(internal.passkeysDb.recordChallenge, {
      challenge: options.challenge,
      purpose: "login",
      userId: undefined,
    });

    return options;
  },
});

/**
 * Step 2 of login. Verify the assertion, look up the credential, mint
 * a new session token. Same shape as POST /auth/login responses so the
 * web client treats the result identically.
 */
export const loginFinish = action({
  args: {
    origin: v.string(),
    response: v.any(), // AuthenticationResponseJSON
  },
  handler: async (ctx, args): Promise<{ token: string; userId: string; userDocId: string; email: string | null }> => {
    if (!allowedOrigins().includes(args.origin)) {
      throw new Error("origin not allowed");
    }
    const rpID = rpIdForOrigin(args.origin);

    const expectedChallenge = args.response?.response?.clientDataJSON
      ? extractClientDataChallenge(args.response.response.clientDataJSON)
      : null;
    if (!expectedChallenge) throw new Error("invalid response");

    const challengeRow = await ctx.runQuery(internal.passkeysDb.findChallenge, {
      challenge: expectedChallenge,
      purpose: "login",
    });
    if (!challengeRow) throw new Error("challenge expired or unknown");

    const credentialId = String(args.response?.id || "");
    if (!credentialId) throw new Error("missing credential id");

    type StoredCred = {
      _id: any;
      userId: any;
      credentialId: string;
      publicKey: string;
      counter: number;
      transports: string[] | null;
    };
    const stored = (await ctx.runQuery(internal.passkeysDb.findByCredentialId, {
      credentialId,
    })) as StoredCred | null;
    if (!stored) throw new Error("unknown credential");

    const verification = await verifyAuthenticationResponse({
      response: args.response,
      expectedChallenge,
      expectedOrigin: args.origin,
      expectedRPID: rpID,
      credential: {
        id: stored.credentialId,
        publicKey: b64urlToBytes(stored.publicKey) as Uint8Array<ArrayBuffer>,
        counter: stored.counter,
        transports: (stored.transports as AuthenticatorTransport[] | null | undefined) ?? undefined,
      },
      requireUserVerification: false,
    });
    if (!verification.verified) throw new Error("assertion failed");

    await ctx.runMutation(internal.passkeysDb.touchCredential, {
      credentialId: stored.credentialId,
      counter: verification.authenticationInfo.newCounter,
    });
    await ctx.runMutation(internal.passkeysDb.consumeChallenge, {
      challenge: expectedChallenge,
    });

    // Mint a session — same code path as /auth/login.
    const tokenBytes = new Uint8Array(32);
    crypto.getRandomValues(tokenBytes);
    const token = Array.from(tokenBytes)
      .map((b) => b.toString(16).padStart(2, "0"))
      .join("");
    const tokenHash = await sha256HexLocal(token);
    const expiresAt = Date.now() + 365 * 24 * 60 * 60 * 1000;
    await ctx.runMutation(internal.auth.createSession, {
      tokenHash,
      userId: stored.userId,
      expiresAt,
    });

    const profile: { userId: string; email: string } | null = await ctx.runQuery(internal.auth.getUserPublicProfile, {
      userDocId: stored.userId,
    });

    return {
      token,
      userId: profile?.userId ?? String(stored.userId),
      userDocId: String(stored.userId),
      email: profile?.email ?? null,
    };
  },
});

// ── Signup-with-passkey ─────────────────────────────────────────────
//
// Anonymous flow: no session token required. Creates a NEW user row
// and a first passkey atomically inside signupFinish. Used as the
// initial-signup path on web + mobile so a brand-new user can land on
// the dashboard with one Touch ID prompt and zero passwords.
//
// Email-already-registered case: signupStart returns
// { error: "EMAIL_EXISTS", hasPasskey } so the client can route the
// user to the right next step (sign in with passkey, or sign in with
// their existing OAuth/email and add a passkey from settings). This
// avoids the silent-failure trap where the user goes through the
// Touch ID prompt only to be rejected at signupFinish.

/**
 * Step 1 of signup. Validates the email isn't already registered,
 * issues a registration challenge, returns the WebAuthn options blob
 * for navigator.credentials.create() / native passkey.create().
 */
export const signupStart = action({
  args: {
    origin: v.string(),
    email: v.string(),
    fullName: v.string(),
  },
  handler: async (
    ctx,
    args,
  ): Promise<
    | { ok: true; options: any }
    | {
        ok: false;
        error: "EMAIL_EXISTS" | "INVALID_EMAIL";
        hasPasskey?: boolean;
        providers?: string[];
      }
  > => {
    if (!allowedOrigins().includes(args.origin)) {
      throw new Error("origin not allowed");
    }
    const email = args.email.trim().toLowerCase();
    if (!email || !email.includes("@")) {
      return { ok: false, error: "INVALID_EMAIL" };
    }

    const availability: {
      available: boolean;
      hasPasskey: boolean;
      providers?: string[];
      emailVerified?: boolean;
    } = await ctx.runQuery(internal.passkeysDb.emailAvailable, { email });
    if (!availability.available) {
      // Don't leak emailVerified here — it's an internal account-state
      // flag and anonymous callers don't need it for the UI routing.
      // hasPasskey + providers is enough to render "Sign in with X" hints.
      return {
        ok: false,
        error: "EMAIL_EXISTS",
        hasPasskey: availability.hasPasskey,
        providers: availability.providers ?? [],
      };
    }

    const rpID = rpIdForOrigin(args.origin);
    const options = await generateRegistrationOptions({
      rpName: rpName(),
      rpID,
      userName: email,
      userDisplayName: args.fullName.trim() || email,
      // userID is the WebAuthn-internal handle the authenticator
      // persists. For a fresh signup we have no users row yet, so
      // bind it to the email — the email is the stable identifier
      // until the user picks a userId post-signup. The authenticator
      // never reveals this back to the RP, so the choice is private.
      userID: new TextEncoder().encode(email),
      attestationType: "none",
      excludeCredentials: [],
      authenticatorSelection: {
        residentKey: "preferred",
        userVerification: "preferred",
      },
    });

    await ctx.runMutation(internal.passkeysDb.recordChallenge, {
      challenge: options.challenge,
      purpose: "signup",
      userId: undefined,
    });

    return { ok: true, options };
  },
});

/**
 * Step 2 of signup. Verifies the attestation, creates the user row
 * and first passkey atomically (via createPasskeyUser mutation), mints
 * a session token. Same response shape as loginFinish.
 */
export const signupFinish = action({
  args: {
    origin: v.string(),
    email: v.string(),
    fullName: v.string(),
    response: v.any(),
    deviceLabel: v.optional(v.string()),
  },
  handler: async (
    ctx,
    args,
  ): Promise<{ token: string; userId: string; userDocId: string; email: string }> => {
    if (!allowedOrigins().includes(args.origin)) {
      throw new Error("origin not allowed");
    }
    const rpID = rpIdForOrigin(args.origin);
    const email = args.email.trim().toLowerCase();

    const expectedChallenge = args.response?.response?.clientDataJSON
      ? extractClientDataChallenge(args.response.response.clientDataJSON)
      : null;
    if (!expectedChallenge) throw new Error("invalid response");

    const challengeRow = await ctx.runQuery(internal.passkeysDb.findChallenge, {
      challenge: expectedChallenge,
      purpose: "signup",
    });
    if (!challengeRow) throw new Error("challenge expired or unknown");

    const verification = await verifyRegistrationResponse({
      response: args.response,
      expectedChallenge,
      expectedOrigin: args.origin,
      expectedRPID: rpID,
      requireUserVerification: false,
    });
    if (!verification.verified || !verification.registrationInfo) {
      throw new Error("attestation failed");
    }

    const cred = verification.registrationInfo.credential;
    const credentialIdB64 = cred.id;
    const publicKeyB64 = bytesToB64url(cred.publicKey);

    // createPasskeyUser is atomic: re-checks email + credentialId
    // uniqueness inside a single mutation, so a concurrent signup with
    // the same email racing this one safely fails with EMAIL_EXISTS.
    const userDocId = await ctx.runMutation(internal.passkeysDb.createPasskeyUser, {
      email,
      fullName: args.fullName,
      credentialId: credentialIdB64,
      publicKey: publicKeyB64,
      counter: cred.counter,
      transports: cred.transports ?? undefined,
      deviceLabel: args.deviceLabel,
      backedUp: verification.registrationInfo.credentialBackedUp,
    });

    await ctx.runMutation(internal.passkeysDb.consumeChallenge, {
      challenge: expectedChallenge,
    });

    // Mint a session — same shape as loginFinish.
    const tokenBytes = new Uint8Array(32);
    crypto.getRandomValues(tokenBytes);
    const token = Array.from(tokenBytes)
      .map((b) => b.toString(16).padStart(2, "0"))
      .join("");
    const tokenHash = await sha256HexLocal(token);
    const expiresAt = Date.now() + 365 * 24 * 60 * 60 * 1000;
    await ctx.runMutation(internal.auth.createSession, {
      tokenHash,
      userId: userDocId,
      expiresAt,
    });

    const profile: { userId: string; email: string } | null = await ctx.runQuery(
      internal.auth.getUserPublicProfile,
      { userDocId },
    );

    return {
      token,
      userId: profile?.userId ?? String(userDocId),
      userDocId: String(userDocId),
      email: profile?.email ?? email,
    };
  },
});

// SHA-256 hex of a string. Mirrors auth.sha256Hex but is reachable
// from a "use node" action (importing from auth.ts would pull mutation
// helpers that V8/node can both serve, but we keep this self-contained
// so the dep graph stays simple).
async function sha256HexLocal(input: string): Promise<string> {
  const buf = new TextEncoder().encode(input);
  const hash = await crypto.subtle.digest("SHA-256", buf);
  return Array.from(new Uint8Array(hash))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

// Pull the original challenge string back out of the clientDataJSON.
// SimpleWebAuthn checks this internally too, but we need it earlier to
// look the row up in our challenge table.
function extractClientDataChallenge(clientDataJSONb64url: string): string | null {
  try {
    const json = new TextDecoder().decode(b64urlToBytes(clientDataJSONb64url));
    const parsed = JSON.parse(json);
    return typeof parsed.challenge === "string" ? parsed.challenge : null;
  } catch {
    return null;
  }
}
