import { v } from "convex/values";
import { internalMutation, mutation, query } from "./_generated/server";
import { validateSessionInternal } from "./auth";
import * as ed from "@noble/ed25519";

const BROKER_ID_RE = /^acb_[a-f0-9]{24}$/;
const TOPIC_ID_RE = /^act_[a-f0-9]{32}$/;
const DEVICE_ID_RE = /^[A-Za-z0-9._:-]{1,128}$/;
const FINGERPRINT_RE = /^(?:[A-Fa-f0-9]{2}:){31}[A-Fa-f0-9]{2}$/;
const NONCE_RE = /^[a-f0-9]{32}$/;

function randomId(prefix: "acb" | "act", bytes: number): string {
  const raw = new Uint8Array(bytes);
  crypto.getRandomValues(raw);
  return `${prefix}_${Array.from(raw, (b) => b.toString(16).padStart(2, "0")).join("")}`;
}

function base64Bytes(value: string): Uint8Array {
  const binary = atob(value);
  return Uint8Array.from(binary, (character) => character.charCodeAt(0));
}

function deviceProofMessage(deviceId: string, timestamp: number, nonce: string): string {
  return `yaver-access-device-v1\n${deviceId}\n${timestamp}\n${nonce}`;
}

export function normalizeAccessBrokerEndpoint(input: string): string {
  let u: URL;
  try {
    u = new URL(input.trim());
  } catch {
    throw new Error("broker endpoint must be an absolute mqtts:// or wss:// URL");
  }
  if (u.protocol !== "mqtts:" && u.protocol !== "wss:") {
    throw new Error("broker endpoint must use mqtts:// or wss://");
  }
  if (!u.hostname || u.username || u.password || u.search || u.hash) {
    throw new Error("broker endpoint must not contain credentials, query parameters, or fragments");
  }
  u.pathname = u.pathname.replace(/\/+$/, "") || "/";
  return u.toString();
}

export function normalizeHostedAccessEndpoint(input: string): string {
  let u: URL;
  try {
    u = new URL(input.trim());
  } catch {
    throw new Error("hosted access endpoint must be an absolute wss:// URL");
  }
  if (u.protocol !== "wss:" || !u.hostname || u.username || u.password || u.search || u.hash) {
    throw new Error("hosted access endpoint must use wss:// without credentials, query parameters, or fragments");
  }
  u.pathname = u.pathname.replace(/\/+$/, "") || "/";
  return u.toString();
}

export const ensureHosted = internalMutation({
  args: { tokenHash: v.string(), endpoint: v.string() },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");
    const endpoint = normalizeHostedAccessEndpoint(args.endpoint);
    const existing = (await ctx.db
      .query("accessBrokers")
      .withIndex("by_owner", (q) => q.eq("userId", session.user._id))
      .collect())
      .find((row) => row.deployment === "yaver-hosted" && !row.disabledAt);
    if (existing) {
      if (existing.endpoint !== endpoint) {
        await ctx.db.patch(existing._id, { endpoint, updatedAt: Date.now() });
      }
      return { ok: true, brokerId: existing.brokerId, endpoint, transport: "websocket" as const };
    }
    const now = Date.now();
    const brokerId = randomId("acb", 12);
    await ctx.db.insert("accessBrokers", {
      userId: session.user._id,
      brokerId,
      tenantTopicId: randomId("act", 16),
      name: "Yaver Access",
      endpoint,
      deployment: "yaver-hosted",
      createdAt: now,
      updatedAt: now,
    });
    return { ok: true, brokerId, endpoint, transport: "websocket" as const };
  },
});

export const registerByoBroker = mutation({
  args: {
    tokenHash: v.string(),
    name: v.string(),
    endpoint: v.string(),
    caFingerprint: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");
    const name = args.name.trim().slice(0, 80);
    if (!name) throw new Error("broker name required");
    const endpoint = normalizeAccessBrokerEndpoint(args.endpoint);
    const caFingerprint = args.caFingerprint?.trim().toUpperCase();
    if (caFingerprint && !FINGERPRINT_RE.test(caFingerprint)) {
      throw new Error("CA fingerprint must be a SHA-256 fingerprint");
    }
    const now = Date.now();
    const brokerId = randomId("acb", 12);
    const tenantTopicId = randomId("act", 16);
    await ctx.db.insert("accessBrokers", {
      userId: session.user._id,
      brokerId,
      tenantTopicId,
      name,
      endpoint,
      deployment: "byo",
      caFingerprint,
      createdAt: now,
      updatedAt: now,
    });
    return { ok: true, brokerId, tenantTopicId, name, endpoint };
  },
});

export const listMine = query({
  args: { tokenHash: v.string() },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");
    const brokers = await ctx.db
      .query("accessBrokers")
      .withIndex("by_owner", (q) => q.eq("userId", session.user._id))
      .collect();
    const grants = await ctx.db
      .query("accessBrokerDevices")
      .withIndex("by_owner", (q) => q.eq("userId", session.user._id))
      .collect();
    return {
      brokers: brokers.map((row) => ({
        brokerId: row.brokerId,
        name: row.name,
        endpoint: row.endpoint,
        deployment: row.deployment,
        transport: row.deployment === "yaver-hosted" ? "websocket" as const : "mqtt" as const,
        caFingerprint: row.caFingerprint ?? null,
        enabled: !row.disabledAt,
        createdAt: row.createdAt,
        deviceIds: grants.filter((grant) => grant.brokerId === row.brokerId && grant.state === "active").map((grant) => grant.deviceId),
      })),
    };
  },
});

export const enrollOwnedDevice = mutation({
  args: {
    tokenHash: v.string(),
    brokerId: v.string(),
    deviceId: v.string(),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");
    if (!BROKER_ID_RE.test(args.brokerId)) throw new Error("invalid broker id");
    if (!DEVICE_ID_RE.test(args.deviceId)) throw new Error("invalid device id");

    const broker = await ctx.db
      .query("accessBrokers")
      .withIndex("by_broker_id", (q) => q.eq("brokerId", args.brokerId))
      .unique();
    if (!broker || broker.userId !== session.user._id || broker.disabledAt) {
      throw new Error("broker unavailable");
    }
    const device = await ctx.db
      .query("devices")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .unique();
    if (!device || device.userId !== session.user._id || device.removed) {
      throw new Error("device is not owned by this account");
    }
    if (!device.signPublicKey) {
      throw new Error("device must publish its Yaver signing key before enrollment");
    }

    const existing = await ctx.db
      .query("accessBrokerDevices")
      .withIndex("by_broker_device", (q) =>
        q.eq("brokerId", args.brokerId).eq("deviceId", args.deviceId),
      )
      .unique();
    const now = Date.now();
    if (existing) {
      await ctx.db.patch(existing._id, {
        userId: session.user._id,
        signPublicKey: device.signPublicKey,
        state: "active",
        enrolledAt: now,
        revokedAt: undefined,
      });
    } else {
      await ctx.db.insert("accessBrokerDevices", {
        userId: session.user._id,
        brokerId: args.brokerId,
        deviceId: args.deviceId,
        signPublicKey: device.signPublicKey,
        state: "active",
        enrolledAt: now,
      });
    }
    return { ok: true, brokerId: broker.brokerId, deviceId: device.deviceId };
  },
});

export const authorizeConnection = query({
  args: {
    tokenHash: v.string(),
    brokerId: v.string(),
    role: v.union(v.literal("controller"), v.literal("device")),
    deviceId: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");
    const broker = await ctx.db
      .query("accessBrokers")
      .withIndex("by_broker_id", (q) => q.eq("brokerId", args.brokerId))
      .unique();
    if (!broker || broker.userId !== session.user._id || broker.disabledAt) {
      throw new Error("broker unavailable");
    }
    if (!BROKER_ID_RE.test(broker.brokerId) || !TOPIC_ID_RE.test(broker.tenantTopicId)) {
      throw new Error("broker registration is invalid");
    }
    if (args.role === "controller") {
      return {
        ok: true as const,
        role: "controller" as const,
        brokerId: broker.brokerId,
        tenantTopicId: broker.tenantTopicId,
        endpoint: broker.endpoint,
        transport: broker.deployment === "yaver-hosted" ? "websocket" as const : "mqtt" as const,
        caFingerprint: broker.caFingerprint ?? null,
      };
    }
    if (!args.deviceId || !DEVICE_ID_RE.test(args.deviceId)) {
      throw new Error("device id required");
    }
    const grant = await ctx.db
      .query("accessBrokerDevices")
      .withIndex("by_broker_device", (q) =>
        q.eq("brokerId", broker.brokerId).eq("deviceId", args.deviceId!),
      )
      .unique();
    if (!grant || grant.userId !== session.user._id || grant.state !== "active") {
      throw new Error("device is not enrolled for this broker");
    }
    return {
      ok: true as const,
      role: "device" as const,
      brokerId: broker.brokerId,
      tenantTopicId: broker.tenantTopicId,
      endpoint: broker.endpoint,
      transport: broker.deployment === "yaver-hosted" ? "websocket" as const : "mqtt" as const,
      caFingerprint: broker.caFingerprint ?? null,
      deviceId: grant.deviceId,
      signPublicKey: grant.signPublicKey,
    };
  },
});

// A previously enrolled headless device reconnects with its installation
// Ed25519 key, even if its Yaver account bearer has expired. Enrollment itself
// remains owner-session-only. The bounded nonce set makes a captured proof
// unusable twice while avoiding an unbounded proof table.
export const authorizeDeviceProof = internalMutation({
  args: {
    deviceId: v.string(),
    timestamp: v.number(),
    nonce: v.string(),
    signature: v.string(),
  },
  handler: async (ctx, args) => {
    const now = Date.now();
    if (!DEVICE_ID_RE.test(args.deviceId) || !NONCE_RE.test(args.nonce) ||
        !Number.isSafeInteger(args.timestamp) || Math.abs(now - args.timestamp) > 120_000) {
      throw new Error("invalid device proof");
    }
    const grants = await ctx.db
      .query("accessBrokerDevices")
      .withIndex("by_device", (q) => q.eq("deviceId", args.deviceId))
      .collect();
    for (const grant of grants) {
      if (grant.state !== "active") continue;
      const broker = await ctx.db
        .query("accessBrokers")
        .withIndex("by_broker_id", (q) => q.eq("brokerId", grant.brokerId))
        .unique();
      if (!broker || broker.disabledAt || broker.deployment !== "yaver-hosted" || broker.userId !== grant.userId) continue;
      const device = await ctx.db
        .query("devices")
        .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
        .unique();
      if (!device || device.removed || device.userId !== grant.userId || device.signPublicKey !== grant.signPublicKey) continue;
      const recent = (grant.recentProofs ?? []).filter((proof) => proof.expiresAt > now);
      if (recent.some((proof) => proof.nonce === args.nonce)) throw new Error("device proof replay rejected");
      let verified = false;
      try {
        verified = await ed.verifyAsync(
          base64Bytes(args.signature),
          new TextEncoder().encode(deviceProofMessage(args.deviceId, args.timestamp, args.nonce)),
          base64Bytes(grant.signPublicKey),
        );
      } catch { /* invalid encoding */ }
      if (!verified) throw new Error("invalid device proof");
      await ctx.db.patch(grant._id, {
        recentProofs: [...recent.slice(-7), { nonce: args.nonce, expiresAt: now + 120_000 }],
      });
      return {
        ok: true as const,
        role: "device" as const,
        deviceId: grant.deviceId,
        brokerId: broker.brokerId,
        tenantTopicId: broker.tenantTopicId,
        endpoint: broker.endpoint,
        transport: "websocket" as const,
        caFingerprint: broker.caFingerprint ?? null,
      };
    }
    throw new Error("device is not enrolled in hosted access");
  },
});

export const revokeDevice = mutation({
  args: { tokenHash: v.string(), brokerId: v.string(), deviceId: v.string() },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");
    const grant = await ctx.db
      .query("accessBrokerDevices")
      .withIndex("by_broker_device", (q) =>
        q.eq("brokerId", args.brokerId).eq("deviceId", args.deviceId),
      )
      .unique();
    if (!grant || grant.userId !== session.user._id) throw new Error("device grant unavailable");
    await ctx.db.patch(grant._id, { state: "revoked", revokedAt: Date.now() });
    return { ok: true };
  },
});
