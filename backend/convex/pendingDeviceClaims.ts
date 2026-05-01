// pendingDeviceClaims.ts — the "I just installed yaver on a fresh box,
// claim it from my dashboard" path. Without this slice, a truly-fresh
// box (no prior `yaver auth`, so no Convex devices row, just a relay
// tunnel registered with `bootstrap-pending`) is invisible from afar:
// /devices/bootstrap returns "Device not found" because the (deviceId,
// hardwareId, publicKey) triple isn't in the devices table yet, and
// nothing else surfaces the box to the user's dashboard.
//
// The fix is a holding table keyed by relay-password hash. The agent
// posts to /devices/bootstrap-pending with its relay password; we hash
// it server-side and store the row. The user's dashboard queries this
// table, scoped to whatever relay password their managedRelay row
// carries — only a user whose password hashes to the same digest can
// see (and therefore claim) the pending box. Self-hosted shared-
// password setups don't get scoped scoping; the v1 contract is "you
// must be on a per-user managed relay to use pending-claim".
//
// Lifecycle: created on first /devices/bootstrap-pending; refreshed on
// subsequent retries; deleted on claim or by the cron sweep below.

import { v } from "convex/values";
import {
  mutation,
  query,
  internalMutation,
} from "./_generated/server";
import { sha256Hex, validateSessionInternal } from "./auth";

// 24h: an unclaimed pending box is either dead, claimed via a different
// path, or the user's relay password rotated. Either way the row is
// stale and worth nothing — sweep it.
const STALE_PENDING_MS = 24 * 60 * 60 * 1000;

// createOrUpdate is the agent-side write. Called from the
// /devices/bootstrap-pending HTTP route after we've hashed the relay
// password. We DO NOT trust caller-supplied identity for ownership
// purposes — relay password is the only signal. The user proves
// ownership later by hashing their own managedRelays.password and
// matching the hash here.
export const createOrUpdate = internalMutation({
  args: {
    deviceId: v.string(),
    hardwareId: v.string(),
    publicKey: v.string(),
    relayPasswordHash: v.string(),
    name: v.optional(v.string()),
    platform: v.optional(v.string()),
    quicHost: v.optional(v.string()),
    quicPort: v.optional(v.number()),
    relayLabel: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    // If the device is already a fully-owned row, this endpoint must
    // not silently shadow it — that would let a hostile relay-password
    // holder hijack the surface. Reject early.
    const existingDevice = await ctx.db
      .query("devices")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .unique();
    if (existingDevice) {
      // Hardware fingerprint and public key must match. If they don't,
      // someone is reusing a deviceId with a different hardware — that
      // is exactly the identity-hijack we're guarding against.
      if (
        existingDevice.hardwareId !== args.hardwareId ||
        existingDevice.publicKey !== args.publicKey
      ) {
        throw new Error("device id collides with an existing owned row");
      }
      // Owned row exists with matching identity — nothing to do here;
      // the agent should be calling /devices/bootstrap, not pending.
      return { ok: true, alreadyClaimed: true };
    }

    const now = Date.now();
    const existingPending = await ctx.db
      .query("pendingDeviceClaims")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .unique();
    if (existingPending) {
      // Identity must match — same agent registering again, not a
      // different box claiming the slot.
      if (
        existingPending.hardwareId !== args.hardwareId ||
        existingPending.publicKey !== args.publicKey
      ) {
        throw new Error("identity mismatch for existing pending claim");
      }
      await ctx.db.patch(existingPending._id, {
        relayPasswordHash: args.relayPasswordHash,
        name: args.name ?? existingPending.name,
        platform: args.platform ?? existingPending.platform,
        quicHost: args.quicHost ?? existingPending.quicHost,
        quicPort: args.quicPort ?? existingPending.quicPort,
        relayLabel: args.relayLabel ?? existingPending.relayLabel,
        lastSeenAt: now,
      });
      return { ok: true, refreshed: true };
    }

    await ctx.db.insert("pendingDeviceClaims", {
      deviceId: args.deviceId,
      hardwareId: args.hardwareId,
      publicKey: args.publicKey,
      relayPasswordHash: args.relayPasswordHash,
      name: args.name,
      platform: args.platform,
      quicHost: args.quicHost,
      quicPort: args.quicPort,
      relayLabel: args.relayLabel,
      firstSeenAt: now,
      lastSeenAt: now,
    });
    return { ok: true, created: true };
  },
});

// listForUser is the dashboard read. We look up the caller's
// managedRelay password, hash it, and return any pending claims that
// match. Empty list when the user has no managedRelay or no claims.
export const listForUser = query({
  args: { tokenHash: v.string() },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) return { items: [] };

    const relay = await ctx.db
      .query("managedRelays")
      .withIndex("by_user", (q) => q.eq("userId", session.user._id))
      .first();
    if (!relay || !relay.password) return { items: [] };

    const passwordHash = await sha256Hex(relay.password);
    const items = await ctx.db
      .query("pendingDeviceClaims")
      .withIndex("by_relayPasswordHash", (q) => q.eq("relayPasswordHash", passwordHash))
      .collect();

    // Drop anything past the stale window — the cron sweep is best-effort,
    // and we don't want the UI to ever surface a 36h-old "pending" row.
    const cutoff = Date.now() - STALE_PENDING_MS;
    return {
      items: items
        .filter((row) => row.lastSeenAt >= cutoff)
        .map((row) => ({
          id: row._id,
          deviceId: row.deviceId,
          hardwareId: row.hardwareId,
          name: row.name,
          platform: row.platform,
          quicHost: row.quicHost,
          quicPort: row.quicPort,
          firstSeenAt: row.firstSeenAt,
          lastSeenAt: row.lastSeenAt,
          relayLabel: row.relayLabel,
        }))
        .sort((a, b) => b.lastSeenAt - a.lastSeenAt),
    };
  },
});

// claim is the dashboard write. Validates the caller's session, matches
// the relay password hash, and copies the holding-row data into a real
// devices row owned by the user. Then deletes the pending row so the
// dashboard list cleans up immediately.
export const claim = mutation({
  args: {
    tokenHash: v.string(),
    deviceId: v.string(),
    // Optional override for the device name — UI lets the user rename
    // before claiming so a fleet of identical fresh installs gets sane
    // labels right away. Falls back to the agent-supplied hostname.
    name: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");

    const pending = await ctx.db
      .query("pendingDeviceClaims")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", args.deviceId))
      .unique();
    if (!pending) {
      throw new Error("no pending claim for this deviceId");
    }

    const relay = await ctx.db
      .query("managedRelays")
      .withIndex("by_user", (q) => q.eq("userId", session.user._id))
      .first();
    if (!relay || !relay.password) {
      throw new Error("no managed relay for this user — cannot validate ownership");
    }
    const passwordHash = await sha256Hex(relay.password);
    if (passwordHash !== pending.relayPasswordHash) {
      // Different relay password → caller is not the host whose box
      // joined the relay. Treat as a 403, not a 404, so the UI can
      // distinguish "wrong user" from "no such pending claim".
      throw new Error("relay-password mismatch — this pending claim came in via a different relay");
    }

    // Devices row may have been created in parallel by /devices/bootstrap
    // landing for the same triple. Idempotent: if already owned by this
    // user, just clear the pending row and return.
    const existing = await ctx.db
      .query("devices")
      .withIndex("by_deviceId", (q) => q.eq("deviceId", pending.deviceId))
      .unique();
    if (existing) {
      if (existing.userId !== session.user._id) {
        throw new Error("device already claimed by another user");
      }
      await ctx.db.delete(pending._id);
      return { ok: true, deviceId: pending.deviceId, alreadyOwned: true };
    }

    const platform = ((): "macos" | "windows" | "linux" | "android" | "ios" => {
      const p = (pending.platform || "").toLowerCase();
      if (p === "macos" || p === "darwin") return "macos";
      if (p === "windows") return "windows";
      if (p === "android") return "android";
      if (p === "ios") return "ios";
      return "linux";
    })();

    const now = Date.now();
    await ctx.db.insert("devices", {
      userId: session.user._id,
      deviceId: pending.deviceId,
      name: args.name?.trim() || pending.name?.trim() || pending.deviceId.slice(0, 8),
      platform,
      publicKey: pending.publicKey,
      quicHost: pending.quicHost || "",
      quicPort: pending.quicPort || 18080,
      isOnline: true,
      // Critical: the box is in bootstrap mode the moment we claim it.
      // The user's next action is to run owner-claim from the dashboard
      // (which hits /auth/pair/owner-claim on the agent), so the row
      // should land with needsAuth=true so the lifecycle derivation
      // and existing reauth UI both kick in immediately.
      needsAuth: true,
      lastHeartbeat: now,
      createdAt: now,
      hardwareId: pending.hardwareId,
    });

    await ctx.db.delete(pending._id);
    return { ok: true, deviceId: pending.deviceId, claimed: true };
  },
});

// sweepStale is the cron-driven cleanup. Runs daily; deletes claims
// older than STALE_PENDING_MS that nobody picked up. Safe to call by
// hand if the table grows during incident response.
export const sweepStale = internalMutation({
  args: {},
  handler: async (ctx) => {
    const cutoff = Date.now() - STALE_PENDING_MS;
    const stale = await ctx.db
      .query("pendingDeviceClaims")
      .withIndex("by_lastSeenAt", (q) => q.lt("lastSeenAt", cutoff))
      .collect();
    for (const row of stale) {
      await ctx.db.delete(row._id);
    }
    return { swept: stale.length };
  },
});
