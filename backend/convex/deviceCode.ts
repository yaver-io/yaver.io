import { v } from "convex/values";
import { mutation, query } from "./_generated/server";
import { randomHex, sha256Hex } from "./auth";

const DEVICE_CODE_TTL_MS = 15 * 60 * 1000; // 15 minutes

/** Generate a human-readable user code like "ABCD-1234". */
function generateUserCode(): string {
  const letters = "ABCDEFGHJKLMNPQRSTUVWXYZ"; // no I, O (ambiguous)
  const digits = "0123456789";
  let code = "";
  const buf = new Uint8Array(8);
  crypto.getRandomValues(buf);
  for (let i = 0; i < 4; i++) {
    code += letters[buf[i] % letters.length];
  }
  code += "-";
  for (let i = 4; i < 8; i++) {
    code += digits[buf[i] % digits.length];
  }
  return code;
}

/**
 * Create a new device code for headless CLI auth.
 * Returns userCode (shown to user) and deviceCode (used by CLI to poll).
 */
export const createDeviceCode = mutation({
  args: {
    machineName: v.optional(v.string()),
    platform: v.optional(v.string()),
    arch: v.optional(v.string()),
    shell: v.optional(v.string()),
    environment: v.optional(v.string()),
    runtimeVersion: v.optional(v.string()),
    isWsl: v.optional(v.boolean()),
  },
  handler: async (ctx, args) => {
    // Clean up expired codes lazily (delete up to 10)
    const expired = await ctx.db
      .query("deviceCodes")
      .filter((q) => q.lt(q.field("expiresAt"), Date.now()))
      .take(10);
    for (const code of expired) {
      await ctx.db.delete(code._id);
    }

    // Generate unique userCode (retry on collision with pending codes)
    let userCode: string;
    let attempts = 0;
    do {
      userCode = generateUserCode();
      const existing = await ctx.db
        .query("deviceCodes")
        .withIndex("by_userCode", (q) => q.eq("userCode", userCode))
        .unique();
      if (!existing || existing.status !== "pending") break;
      attempts++;
    } while (attempts < 5);

    const deviceCode = randomHex(20); // 40-char hex
    const now = Date.now();

    await ctx.db.insert("deviceCodes", {
      userCode,
      deviceCode,
      status: "pending",
      machineName: args.machineName,
      platform: args.platform,
      arch: args.arch,
      shell: args.shell,
      environment: args.environment,
      runtimeVersion: args.runtimeVersion,
      isWsl: args.isWsl,
      expiresAt: now + DEVICE_CODE_TTL_MS,
      createdAt: now,
    });

    return {
      userCode,
      deviceCode,
      expiresAt: now + DEVICE_CODE_TTL_MS,
    };
  },
});

export const getDeviceCodeInfo = query({
  args: { userCode: v.string() },
  handler: async (ctx, args) => {
    const code = await ctx.db
      .query("deviceCodes")
      .withIndex("by_userCode", (q) => q.eq("userCode", args.userCode))
      .unique();

    if (!code) {
      return null;
    }

    return {
      userCode: code.userCode,
      status: code.status,
      machineName: code.machineName ?? null,
      platform: code.platform ?? null,
      arch: code.arch ?? null,
      shell: code.shell ?? null,
      environment: code.environment ?? null,
      runtimeVersion: code.runtimeVersion ?? null,
      isWsl: code.isWsl ?? false,
      expiresAt: code.expiresAt,
      createdAt: code.createdAt,
    };
  },
});

/**
 * Poll for device code status. Called by CLI every 5 seconds.
 * Returns status and token (if authorized).
 */
export const pollDeviceCode = mutation({
  args: { deviceCode: v.string() },
  handler: async (ctx, args) => {
    const code = await ctx.db
      .query("deviceCodes")
      .withIndex("by_deviceCode", (q) => q.eq("deviceCode", args.deviceCode))
      .unique();

    if (!code) {
      return { status: "expired" as const };
    }

    if (code.expiresAt < Date.now()) {
      await ctx.db.patch(code._id, { status: "expired" });
      return { status: "expired" as const };
    }

    if (code.status === "authorized" && code.pendingToken) {
      // Return token and clear it (one-time retrieval)
      const token = code.pendingToken;
      await ctx.db.patch(code._id, { pendingToken: undefined });
      // Delete the code after successful retrieval
      await ctx.db.delete(code._id);
      return { status: "authorized" as const, token };
    }

    if (code.status === "authorized") {
      // Token already retrieved
      return { status: "expired" as const };
    }

    return { status: "pending" as const };
  },
});

/**
 * Authorize a device code. Called from the web after user authenticates.
 * Creates a session and stores the raw token on the device code for CLI retrieval.
 */
export const authorizeDeviceCode = mutation({
  args: {
    userCode: v.string(),
    userId: v.id("users"),
  },
  handler: async (ctx, args) => {
    const code = await ctx.db
      .query("deviceCodes")
      .withIndex("by_userCode", (q) => q.eq("userCode", args.userCode))
      .unique();

    if (!code) {
      throw new Error("INVALID_CODE");
    }

    if (code.status !== "pending") {
      throw new Error("CODE_ALREADY_USED");
    }

    if (code.expiresAt < Date.now()) {
      await ctx.db.patch(code._id, { status: "expired" });
      throw new Error("CODE_EXPIRED");
    }

    // Generate session token
    const tokenBytes = new Uint8Array(32);
    crypto.getRandomValues(tokenBytes);
    const token = Array.from(tokenBytes)
      .map((b) => b.toString(16).padStart(2, "0"))
      .join("");
    const tokenHash = await sha256Hex(token);
    const expiresAt = Date.now() + 365 * 24 * 60 * 60 * 1000; // 1 year

    // Create session
    await ctx.db.insert("sessions", {
      tokenHash,
      userId: args.userId,
      expiresAt,
      createdAt: Date.now(),
    });

    // Store raw token for CLI retrieval, mark as authorized
    await ctx.db.patch(code._id, {
      status: "authorized",
      pendingToken: token,
    });

    return { ok: true };
  },
});
