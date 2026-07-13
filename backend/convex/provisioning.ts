// provisioning.ts — zero-touch (DPP / WiFi-Easy-Connect-style) device
// provisioning. The whole point: a Yaver-powered box (Talos edge node,
// "blackbox" RPi, any third-party hardware) can be flashed at the factory,
// have its QR scanned by the buyer BEFORE it is ever powered on, and then
// self-credential to that buyer's account on first boot — no human at the
// device, no shared LAN, no relay password, works through NAT.
//
// Three actors, three steps:
//
//   1. MINT (builder, flash time) — `yaver provision mint` generates a
//      per-device Ed25519 bootstrap keypair + a one-time high-entropy
//      claimSecret. The PRIVATE key + claimSecret go onto the SD boot
//      seed; the PUBLIC key + claimSecret + deviceId go into a QR printed
//      on the label. mintProvisionedDevice registers {publicKey,
//      sha256(claimSecret)} here in `minted` state. Convex never sees the
//      private key or the raw claimSecret.
//
//   2. CLAIM (end user, device still off) — the buyer scans the QR with
//      the Yaver app, which calls claimProvisionedDevice with the raw
//      claimSecret. We verify sha256(claimSecret) === stored hash and bind
//      ownerUserId. First-claim-wins (TOFU); re-flash resets via revoke.
//      The user OWNS the box at this moment, before it has booted.
//
//   3. ATTEST (device, first boot) — the agent reads its SD seed, signs
//      `provision-attest|<deviceId>|<timestampMs>` with its Ed25519
//      private key, and POSTs {claimSecret, signature, timestamp} to
//      /devices/provision-attest. We verify the signature against the
//      pre-claimed public key + the claimSecret hash + a fresh timestamp,
//      then mint an owner-bound session token. If the user hasn't claimed
//      yet, we return {awaiting-claim} and the device keeps polling.
//
// Security shape: the QR carries only the PUBLIC key, so a leaked label
// lets someone steal the *binding* (claim into their account) but never
// impersonate the hardware (no private key) — the genuine SD seed is the
// only thing that can pass attestation. Mitigate binding theft with the
// high-entropy claimSecret (only someone who saw the real label has it),
// optional factory serial→order binding, and re-flash reset. This is
// exactly DPP's trust model.

import { v } from "convex/values";
import {
  mutation,
  query,
  internalMutation,
} from "./_generated/server";
import { api, internal } from "./_generated/api";
import { sha256Hex, validateSessionInternal } from "./auth";
import * as ed from "@noble/ed25519";

// @noble/ed25519 v2 needs a SHA-512 implementation for verifyAsync. Wire
// it to Convex's Web Crypto subtle digest so we pull in zero extra deps
// (no @noble/hashes). This runs once at module load.
ed.etc.sha512Async = async (...m: Uint8Array[]) => {
  // Copy into a fresh ArrayBuffer-backed view so the type satisfies
  // BufferSource (concatBytes' result is typed as possibly SharedArrayBuffer-
  // backed, which crypto.subtle.digest rejects at the type level).
  const concatenated = ed.etc.concatBytes(...m);
  const buf = new Uint8Array(concatenated.length);
  buf.set(concatenated);
  return new Uint8Array(await crypto.subtle.digest("SHA-512", buf));
};

// Attestation replay window. The device signs a wall-clock timestamp; we
// reject signatures older/newer than this. Generous enough to survive a
// freshly-booted Pi whose clock hasn't NTP-synced by more than a few
// minutes, tight enough that a captured attestation can't be replayed
// indefinitely. (And replay only ever re-mints a token for the legit
// owner, so the blast radius is small even at the edge of the window.)
const ATTEST_SKEW_MS = 10 * 60 * 1000;

// Standard-base64 → Uint8Array. We keep keys/sigs in base64 over the wire
// because that's what the Go agent emits (ed25519 raw bytes, std encoding).
function b64ToBytes(b64: string): Uint8Array {
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

// ── 1. MINT (builder / flash-time) ──────────────────────────────────────
//
// Called by `yaver provision mint`. The CLI generates the keypair locally
// and only hands us the PUBLIC key + the claimSecret HASH. Authenticated:
// the caller must own (or be allowed to mint under) the product. For v1 we
// scope by product ownership; an unknown productId is allowed (treated as
// an ad-hoc product owned by the caller) so a solo builder can mint
// without first registering a product row.
export const mintProvisionedDevice = mutation({
  args: {
    tokenHash: v.string(),
    deviceId: v.string(),
    publicKey: v.string(),
    claimSecretHash: v.string(),
    productId: v.optional(v.string()),
    name: v.optional(v.string()),
    platform: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    // Resolve product (optional). If supplied, the caller must own it.
    let model: string | undefined;
    if (args.productId) {
      const product = await ctx.db
        .query("deviceProducts")
        .withIndex("by_productId", (q) => q.eq("productId", args.productId!))
        .unique();
      if (product) {
        if (product.ownerUserId !== session.user._id) {
          throw new Error("not authorized to mint under this product");
        }
        model = product.name;
      }
    }

    // Idempotent on deviceId: re-minting the same id (e.g. a flash retry)
    // updates the keypair only while the row is still unclaimed. Once a
    // device is claimed/active we refuse to silently re-key it — that
    // would let a second flasher hijack an owned box.
    const existing = await ctx.db
      .query("provisionedDevices")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .unique();
    if (existing) {
      if (existing.status !== "minted") {
        throw new Error(
          `device ${args.deviceId} already ${existing.status}; re-flash (revoke) before re-minting`,
        );
      }
      await ctx.db.patch(existing._id, {
        publicKey: args.publicKey,
        claimSecretHash: args.claimSecretHash,
        productId: args.productId,
        model,
        name: args.name ?? existing.name,
        platform: args.platform ?? existing.platform,
      });
      return { ok: true, deviceId: args.deviceId, reminted: true };
    }

    await ctx.db.insert("provisionedDevices", {
      deviceId: args.deviceId,
      publicKey: args.publicKey,
      claimSecretHash: args.claimSecretHash,
      productId: args.productId,
      model,
      status: "minted",
      name: args.name,
      platform: args.platform,
      mintedAt: Date.now(),
    });
    return { ok: true, deviceId: args.deviceId, minted: true };
  },
});

// registerProduct — builder declares a SKU/model so the claim UI can show
// a friendly name. Slug is unique globally (first-come); the caller owns
// it. Idempotent re-registration by the same owner updates display fields.
export const registerProduct = mutation({
  args: {
    tokenHash: v.string(),
    productId: v.string(),
    name: v.string(),
    vendor: v.optional(v.string()),
    defaultServices: v.optional(v.array(v.string())),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const slug = args.productId.trim().toLowerCase();
    if (!slug) throw new Error("productId required");

    const existing = await ctx.db
      .query("deviceProducts")
      .withIndex("by_productId", (q) => q.eq("productId", slug))
      .unique();
    if (existing) {
      if (existing.ownerUserId !== session.user._id) {
        throw new Error("product slug already taken by another account");
      }
      await ctx.db.patch(existing._id, {
        name: args.name,
        vendor: args.vendor,
        defaultServices: args.defaultServices,
      });
      return { ok: true, productId: slug, updated: true };
    }

    await ctx.db.insert("deviceProducts", {
      productId: slug,
      ownerUserId: session.user._id,
      name: args.name,
      vendor: args.vendor,
      defaultServices: args.defaultServices,
      createdAt: Date.now(),
    });
    return { ok: true, productId: slug, created: true };
  },
});

// peekProvisionedDevice — read-only lookup for the claim UI. Given the
// deviceId from a scanned QR, returns the public-safe fields (model name,
// status, whether it's already claimed) so the app can render a sensible
// "Add Talos Edge Node?" sheet before the user commits. No secret, no
// owner identity beyond a self/claimed flag.
export const peekProvisionedDevice = query({
  args: { tokenHash: v.optional(v.string()), deviceId: v.string() },
  handler: async (ctx, args) => {
    const row = await ctx.db
      .query("provisionedDevices")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .unique();
    if (!row) return { found: false as const };

    let claimedBySelf = false;
    if (args.tokenHash && row.ownerUserId) {
      const session = await validateSessionInternal(ctx, args.tokenHash);
      claimedBySelf = !!session && session.user._id === row.ownerUserId;
    }
    return {
      found: true as const,
      deviceId: row.deviceId,
      model: row.model ?? null,
      productId: row.productId ?? null,
      name: row.name ?? null,
      platform: row.platform ?? null,
      status: row.status,
      claimed: !!row.ownerUserId,
      claimedBySelf,
    };
  },
});

// ── 2. CLAIM (end user, scan QR, device may be off) ─────────────────────
//
// The buyer scans the QR → app calls this with the raw claimSecret. We
// prove possession of the physical label by hashing it and matching the
// pre-registered hash, then bind ownership. After this the user owns the
// box; the device self-credentials whenever it next boots and attests.
export const claimProvisionedDevice = mutation({
  args: {
    tokenHash: v.string(),
    deviceId: v.string(),
    claimSecret: v.string(),
    name: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const row = await ctx.db
      .query("provisionedDevices")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .unique();
    if (!row) throw new Error("no provisioned device with this id");
    if (row.status === "revoked") {
      throw new Error("device has been revoked; re-flash to re-provision");
    }

    const secretHash = await sha256Hex(args.claimSecret);
    if (secretHash !== row.claimSecretHash) {
      // Wrong secret → the caller doesn't actually hold this label.
      throw new Error("claim secret does not match");
    }

    // First-claim-wins. If already owned, only the owner may re-issue
    // (idempotent rename); anyone else is rejected.
    if (row.ownerUserId && row.ownerUserId !== session.user._id) {
      throw new Error("device already claimed by another account");
    }

    await ctx.db.patch(row._id, {
      ownerUserId: session.user._id,
      // Don't downgrade an already-active device back to "claimed".
      status: row.status === "active" ? "active" : "claimed",
      claimedAt: row.claimedAt ?? Date.now(),
      name: args.name?.trim() || row.name,
    });
    return {
      ok: true,
      deviceId: row.deviceId,
      model: row.model ?? null,
      alreadyActive: row.status === "active",
    };
  },
});

// listMine — devices the caller has claimed (claimed or active). Powers a
// "My provisioned hardware" section that shows boxes the user owns even
// before they've come online for the first time.
export const listMine = query({
  args: { tokenHash: v.string() },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) return { items: [] };
    const rows = await ctx.db
      .query("provisionedDevices")
      .withIndex("by_owner", (q) => q.eq("ownerUserId", session.user._id))
      .collect();
    return {
      items: rows
        .map((r) => ({
          deviceId: r.deviceId,
          model: r.model ?? null,
          name: r.name ?? null,
          status: r.status,
          claimedAt: r.claimedAt ?? null,
          activatedAt: r.activatedAt ?? null,
          lastAttestAt: r.lastAttestAt ?? null,
        }))
        .sort((a, b) => (b.claimedAt ?? 0) - (a.claimedAt ?? 0)),
    };
  },
});

// revoke — owner resets ownership (sold the box, re-flashing, lost it).
// Returns the row to a clean slate so a fresh mint/claim can happen. The
// already-issued session token isn't killed here; that's the normal
// device-removal path's job.
export const revoke = mutation({
  args: { tokenHash: v.string(), deviceId: v.string() },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");
    const row = await ctx.db
      .query("provisionedDevices")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .unique();
    if (!row) throw new Error("no provisioned device with this id");
    if (row.ownerUserId && row.ownerUserId !== session.user._id) {
      throw new Error("not the owner");
    }
    await ctx.db.patch(row._id, { status: "revoked" });
    return { ok: true };
  },
});

// ── 3. ATTEST (device, first boot) ──────────────────────────────────────
//
// Internal mutation invoked from the /devices/provision-attest HTTP route.
// No bearer — the proof IS the Ed25519 signature + claimSecret + fresh
// timestamp. Returns one of:
//   {status:"awaiting-claim"} — minted but nobody owns it yet; device polls
//   {status:"revoked"}        — ownership reset; device should stop
//   {status:"active", token}  — proofs valid; owner-bound session minted
export const attest = internalMutation({
  args: {
    deviceId: v.string(),
    claimSecret: v.string(),
    timestampMs: v.number(),
    signature: v.string(), // base64 std, 64-byte ed25519 sig
  },
  handler: async (ctx, args) => {
    const row = await ctx.db
      .query("provisionedDevices")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .unique();
    if (!row) return { status: "not-found" as const };
    if (row.status === "revoked") return { status: "revoked" as const };

    // Proof 1 — possession of the SD seed's claimSecret.
    const secretHash = await sha256Hex(args.claimSecret);
    if (secretHash !== row.claimSecretHash) {
      return { status: "bad-secret" as const };
    }

    // Proof 2 — fresh timestamp (anti-replay).
    const skew = Math.abs(Date.now() - args.timestampMs);
    if (skew > ATTEST_SKEW_MS) {
      return { status: "stale" as const, skewMs: skew };
    }

    // Proof 3 — Ed25519 signature over the canonical message, verified
    // against the PRE-CLAIMED public key. This is what binds the booting
    // box to the genuine factory keypair: a QR-photo attacker has the
    // public key + claimSecret but not the private key, so cannot forge
    // this signature.
    const message = new TextEncoder().encode(
      `provision-attest|${args.deviceId}|${args.timestampMs}`,
    );
    let sigOk = false;
    try {
      sigOk = await ed.verifyAsync(
        b64ToBytes(args.signature),
        message,
        b64ToBytes(row.publicKey),
      );
    } catch {
      sigOk = false;
    }
    if (!sigOk) return { status: "bad-signature" as const };

    // All proofs pass. Record the contact attempt regardless of claim
    // state so the UI can show "device waiting, last seen now".
    const now = Date.now();

    // Not yet claimed by a human → tell the device to keep polling.
    if (!row.ownerUserId) {
      await ctx.db.patch(row._id, { lastAttestAt: now });
      return { status: "awaiting-claim" as const };
    }

    // Claimed → mint an owner-bound session token (same shape as the
    // device-code authorize path) and flip to active.
    const tokenBytes = new Uint8Array(32);
    crypto.getRandomValues(tokenBytes);
    const token = Array.from(tokenBytes)
      .map((b) => b.toString(16).padStart(2, "0"))
      .join("");
    const tokenHash = await sha256Hex(token);
    await ctx.runMutation(internal.auth.createSession, {
      tokenHash,
      userId: row.ownerUserId,
      deviceId: row.deviceId,
      expiresAt: now + 365 * 24 * 60 * 60 * 1000,
    });

    await ctx.db.patch(row._id, {
      status: "active",
      activatedAt: row.activatedAt ?? now,
      lastAttestAt: now,
    });

    return { status: "active" as const, token };
  },
});
