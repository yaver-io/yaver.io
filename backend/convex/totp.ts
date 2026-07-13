import { v } from "convex/values";
import { mutation, query, internalMutation } from "./_generated/server";
import { validateSessionInternal, sha256Hex, randomHex } from "./auth";

// ── TOTP Algorithm (RFC 6238) ───────────────────────────────────────

const TOTP_DIGITS = 6;
const TOTP_PERIOD = 30; // seconds
const TOTP_WINDOW = 1; // allow T-1, T, T+1

/** Base32 encode bytes. */
function base32Encode(data: Uint8Array): string {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let bits = 0;
  let value = 0;
  let output = "";
  for (const byte of data) {
    value = (value << 8) | byte;
    bits += 8;
    while (bits >= 5) {
      output += alphabet[(value >>> (bits - 5)) & 31];
      bits -= 5;
    }
  }
  if (bits > 0) {
    output += alphabet[(value << (5 - bits)) & 31];
  }
  return output;
}

/** Base32 decode string to bytes. */
export function base32Decode(input: string): Uint8Array {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  const cleanInput = input.toUpperCase().replace(/=+$/, "");
  let bits = 0;
  let value = 0;
  const output: number[] = [];
  for (const char of cleanInput) {
    const idx = alphabet.indexOf(char);
    if (idx === -1) continue;
    value = (value << 5) | idx;
    bits += 5;
    if (bits >= 8) {
      output.push((value >>> (bits - 8)) & 255);
      bits -= 8;
    }
  }
  return new Uint8Array(output);
}

/** Compute HMAC-SHA1 using Web Crypto. */
async function hmacSha1(key: Uint8Array, data: Uint8Array): Promise<Uint8Array> {
  const cryptoKey = await crypto.subtle.importKey(
    "raw",
    key.buffer as ArrayBuffer,
    { name: "HMAC", hash: "SHA-1" },
    false,
    ["sign"]
  );
  const sig = await crypto.subtle.sign("HMAC", cryptoKey, data.buffer as ArrayBuffer);
  return new Uint8Array(sig);
}

/** Generate a TOTP code for a given secret and time counter. */
async function generateTOTP(secret: Uint8Array, counter: number): Promise<string> {
  // Convert counter to 8-byte big-endian
  const counterBytes = new Uint8Array(8);
  let c = counter;
  for (let i = 7; i >= 0; i--) {
    counterBytes[i] = c & 0xff;
    c = Math.floor(c / 256);
  }

  const hash = await hmacSha1(secret, counterBytes);

  // Dynamic truncation
  const offset = hash[hash.length - 1] & 0x0f;
  const code =
    ((hash[offset] & 0x7f) << 24) |
    ((hash[offset + 1] & 0xff) << 16) |
    ((hash[offset + 2] & 0xff) << 8) |
    (hash[offset + 3] & 0xff);

  const otp = code % Math.pow(10, TOTP_DIGITS);
  return otp.toString().padStart(TOTP_DIGITS, "0");
}

/** Verify a TOTP code against a secret, allowing a time window. */
export async function verifyTOTP(secret: Uint8Array, code: string): Promise<boolean> {
  const now = Math.floor(Date.now() / 1000);
  const counter = Math.floor(now / TOTP_PERIOD);

  for (let i = -TOTP_WINDOW; i <= TOTP_WINDOW; i++) {
    const expected = await generateTOTP(secret, counter + i);
    if (expected === code) return true;
  }
  return false;
}

// ── Mutations/Queries ───────────────────────────────────────────────

/**
 * Generate a TOTP secret for the user. Does NOT enable 2FA yet.
 * User must verify a code first via verifyAndEnableTotp.
 */
export const setupTotp = mutation({
  args: { tokenHash: v.string() },
  handler: async (ctx, args) => {
    const result = await validateSessionInternal(ctx, args.tokenHash);
    if (!result) throw new Error("Unauthorized");

    if (result.user.totpEnabled) {
      throw new Error("2FA is already enabled. Disable it first.");
    }

    // Generate 20-byte secret
    const secretBytes = new Uint8Array(20);
    crypto.getRandomValues(secretBytes);
    const secret = base32Encode(secretBytes);

    // Store secret (not yet enabled)
    await ctx.db.patch(result.user._id, { totpSecret: secret });

    const otpAuthUrl = `otpauth://totp/Yaver:${encodeURIComponent(result.user.email)}?secret=${secret}&issuer=Yaver&digits=${TOTP_DIGITS}&period=${TOTP_PERIOD}`;

    return { secret, otpAuthUrl };
  },
});

/**
 * Verify a TOTP code and enable 2FA. Returns recovery codes (show once).
 */
export const verifyAndEnableTotp = mutation({
  args: {
    tokenHash: v.string(),
    code: v.string(),
  },
  handler: async (ctx, args) => {
    const result = await validateSessionInternal(ctx, args.tokenHash);
    if (!result) throw new Error("Unauthorized");

    const secret = result.user.totpSecret;
    if (!secret) throw new Error("No TOTP secret. Call setup first.");
    if (result.user.totpEnabled) throw new Error("2FA is already enabled.");

    const secretBytes = base32Decode(secret);
    const valid = await verifyTOTP(secretBytes, args.code.trim());
    if (!valid) throw new Error("INVALID_CODE");

    // Generate 8 recovery codes
    const recoveryCodes: string[] = [];
    const recoveryHashes: string[] = [];
    for (let i = 0; i < 8; i++) {
      const code = randomHex(5); // 10-char hex
      recoveryCodes.push(code);
      recoveryHashes.push(await sha256Hex(code));
    }

    await ctx.db.patch(result.user._id, {
      totpEnabled: true,
      totpRecoveryCodes: JSON.stringify(recoveryHashes),
    });

    return { recoveryCodes };
  },
});

/**
 * Disable 2FA. Requires a valid TOTP code.
 */
export const disableTotp = mutation({
  args: {
    tokenHash: v.string(),
    code: v.string(),
  },
  handler: async (ctx, args) => {
    const result = await validateSessionInternal(ctx, args.tokenHash);
    if (!result) throw new Error("Unauthorized");

    if (!result.user.totpEnabled) throw new Error("2FA is not enabled.");

    const secret = result.user.totpSecret;
    if (!secret) throw new Error("No TOTP secret.");

    const secretBytes = base32Decode(secret);
    const valid = await verifyTOTP(secretBytes, args.code.trim());
    if (!valid) throw new Error("INVALID_CODE");

    await ctx.db.patch(result.user._id, {
      totpSecret: undefined,
      totpEnabled: undefined,
      totpRecoveryCodes: undefined,
    });

    return { ok: true };
  },
});

/**
 * Get 2FA status for the authenticated user.
 */
export const getTotpStatus = query({
  args: { tokenHash: v.string() },
  handler: async (ctx, args) => {
    const result = await validateSessionInternal(ctx, args.tokenHash);
    if (!result) return null;

    let recoveryCodesRemaining = 0;
    if (result.user.totpRecoveryCodes) {
      try {
        const hashes = JSON.parse(result.user.totpRecoveryCodes) as string[];
        recoveryCodesRemaining = hashes.length;
      } catch {
        // ignore
      }
    }

    return {
      enabled: result.user.totpEnabled ?? false,
      recoveryCodesRemaining,
    };
  },
});

/**
 * Create a pending auth record (called when login succeeds but 2FA is required).
 * Returns a pendingToken for the client to use with verifyTotpForLogin.
 */
export const createPendingAuth = internalMutation({
  args: { userId: v.id("users") },
  handler: async (ctx, args) => {
    const pendingToken = randomHex(20); // 40-char hex
    await ctx.db.insert("pendingAuth", {
      pendingToken,
      userId: args.userId,
      attempts: 0,
      expiresAt: Date.now() + 5 * 60 * 1000, // 5 minutes
      createdAt: Date.now(),
    });
    return { pendingToken };
  },
});

/**
 * Verify TOTP code for a pending auth and create a real session.
 * Supports both TOTP codes and recovery codes.
 */
export const verifyTotpForLogin = internalMutation({
  args: {
    pendingToken: v.string(),
    code: v.string(),
  },
  handler: async (ctx, args) => {
    const pending = await ctx.db
      .query("pendingAuth")
      .withIndex("by_pendingToken", (q) => q.eq("pendingToken", args.pendingToken))
      .unique();

    if (!pending) throw new Error("INVALID_PENDING");
    if (pending.expiresAt < Date.now()) {
      await ctx.db.delete(pending._id);
      throw new Error("PENDING_EXPIRED");
    }
    if (pending.attempts >= 5) {
      await ctx.db.delete(pending._id);
      throw new Error("TOO_MANY_ATTEMPTS");
    }

    // Increment attempts
    await ctx.db.patch(pending._id, { attempts: pending.attempts + 1 });

    const user = await ctx.db.get(pending.userId);
    if (!user || !user.totpSecret) throw new Error("USER_NOT_FOUND");

    const code = args.code.trim();
    const secretBytes = base32Decode(user.totpSecret);
    let valid = await verifyTOTP(secretBytes, code);

    // Try recovery code if TOTP failed
    if (!valid && user.totpRecoveryCodes) {
      const codeHash = await sha256Hex(code);
      const hashes: string[] = JSON.parse(user.totpRecoveryCodes);
      const idx = hashes.indexOf(codeHash);
      if (idx !== -1) {
        valid = true;
        // Remove used recovery code
        hashes.splice(idx, 1);
        await ctx.db.patch(user._id, {
          totpRecoveryCodes: JSON.stringify(hashes),
        });
      }
    }

    if (!valid) throw new Error("INVALID_CODE");

    // Create session
    const tokenBytes = new Uint8Array(32);
    crypto.getRandomValues(tokenBytes);
    const token = Array.from(tokenBytes)
      .map((b) => b.toString(16).padStart(2, "0"))
      .join("");
    const tokenHash = await sha256Hex(token);
    const expiresAt = Date.now() + 365 * 24 * 60 * 60 * 1000; // 1 year

    await ctx.db.insert("sessions", {
      tokenHash,
      userId: pending.userId,
      expiresAt,
      createdAt: Date.now(),
    });

    // Clean up pending auth
    await ctx.db.delete(pending._id);

    return { token };
  },
});
